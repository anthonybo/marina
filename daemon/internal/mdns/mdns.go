// Package mdns publishes a short Bonjour name for this machine.
//
// The machine already has one — Bonjour derives Some-Persons-MacBook-Pro.local
// from the computer's name — and it already survives a new DHCP lease, which is
// the hard part. What it does not survive is being typed on a phone. This
// publishes an additional name, so a dev server is reachable at marina.local:3000
// on every machine, with only the port varying.
//
// It is an *additional* name. Renaming the Mac would be one command and no code,
// but it would rename the machine everywhere — AirDrop, Screen Sharing, SSH — for
// the sake of a URL, and that trade is not this program's to make.
//
// # Why a subprocess
//
// Publishing a host record means talking to mDNSResponder, whose only interface
// is the dnssd C API — reachable from Go through cgo, or from a helper. Marina is
// otherwise pure Go and has exactly three external commands, a property worth
// keeping in mind before adding a fourth. `dns-sd -P` ships with macOS, holds the
// registration for as long as it runs, and withdraws it on exit, which is exactly
// the lifetime we want. The cost is a child process to supervise; cgo's cost is
// every future build. This is the cheaper of the two.
package mdns

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Status is what the publisher is currently doing, for the UI to report honestly.
type Status struct {
	// Name is the published host name, including the .local suffix. Empty when
	// nothing is published.
	Name string `json:"name,omitempty"`
	// IP is the address the name points at.
	IP string `json:"ip,omitempty"`
	// Active is true once mDNSResponder has confirmed the registration. Until it
	// does, the name will not resolve, and saying otherwise would send someone to
	// a URL that fails.
	Active bool `json:"active"`
	// Error explains a registration that did not take, in terms worth showing.
	Error string `json:"error,omitempty"`
}

// Publisher keeps one name registered, following the address it points at.
type Publisher struct {
	name string
	port int
	log  *slog.Logger

	mu     sync.RWMutex
	status Status

	// wanted is the address the name should point at, updated by Point.
	wanted   string
	wantedCh chan string
}

// New returns a Publisher for name (without the .local suffix). The port is
// advertised alongside the host record because dns-sd requires one; nothing is
// expected to connect to it.
func New(name string, port int, log *slog.Logger) *Publisher {
	return &Publisher{
		name:     name,
		port:     port,
		log:      log,
		wantedCh: make(chan string, 1),
	}
}

// Status reports what is currently published.
func (p *Publisher) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

// Point sets the address the name should resolve to. Safe to call on every sweep:
// an unchanged address does nothing, and a changed one re-registers.
func (p *Publisher) Point(ip string) {
	p.mu.Lock()
	if ip == p.wanted {
		p.mu.Unlock()
		return
	}
	p.wanted = ip
	p.mu.Unlock()

	// Lossy on purpose: the reconciler only needs the newest address, and Point is
	// called from the sweep, which must never block on it.
	select {
	case p.wantedCh <- ip:
	default:
	}
}

// Run holds the registration until ctx is cancelled, re-registering when the
// address changes and restarting if the helper dies.
func (p *Publisher) Run(ctx context.Context) {
	var (
		cancelChild context.CancelFunc
		childDone   chan struct{}
		current     string
	)

	stop := func() {
		if cancelChild != nil {
			cancelChild()
			<-childDone
			cancelChild, childDone = nil, nil
		}
		current = ""
	}
	defer stop()

	for {
		p.mu.RLock()
		want := p.wanted
		p.mu.RUnlock()

		switch {
		case want == "" && current != "":
			// The network went away. Withdraw rather than keep advertising an
			// address that no longer reaches anything.
			p.log.Info("mdns: withdrawing", "name", p.hostname())
			stop()
			p.set(Status{})
		case want != "" && want != current:
			stop()
			childCtx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			cancelChild, childDone, current = cancel, done, want
			go func(ip string) {
				defer close(done)
				p.register(childCtx, ip)
			}(want)
		}

		select {
		case <-ctx.Done():
			return
		case <-p.wantedCh:
			// A new address; loop and reconcile.
		case <-time.After(30 * time.Second):
			// Also poll, so a helper that died is noticed even while the address
			// stays put.
			if current != "" && !p.Status().Active {
				p.log.Debug("mdns: registration is not active, retrying", "name", p.hostname())
				stop()
			}
		}
	}
}

func (p *Publisher) hostname() string { return p.name + ".local" }

// register runs the helper and holds the registration until ctx ends.
func (p *Publisher) register(ctx context.Context, ip string) {
	host := p.hostname()
	// -P is proxy registration: it publishes a host record for a name this machine
	// does not otherwise own, which is precisely the case here. The service type is
	// private so nothing claims to be a browsable web server on a port that only
	// answers on loopback.
	cmd := exec.CommandContext(ctx, "dns-sd", "-P",
		p.name, "_marina._tcp", "local", fmt.Sprint(p.port), host, ip)
	// Its own process group, so cancelling takes the helper and not this daemon.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	out, err := cmd.StdoutPipe()
	if err != nil {
		p.fail(ip, err)
		return
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		// dns-sd ships with macOS, so this is close to impossible — but a missing
		// helper must degrade to "no short name", never to a broken daemon.
		p.fail(ip, err)
		return
	}
	p.set(Status{Name: host, IP: ip, Active: false})

	// mDNSResponder confirms asynchronously, and until it does the name does not
	// resolve. Watch for the confirmation rather than assuming it.
	go p.watchOutput(out, host, ip)

	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		p.log.Warn("mdns: helper exited", "name", host, "err", err)
		p.fail(ip, fmt.Errorf("registration stopped"))
		return
	}
	if ctx.Err() != nil {
		p.set(Status{})
	}
}

func (p *Publisher) watchOutput(out io.Reader, host, ip string) {
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.Contains(line, "registered and active"):
			// Two of these arrive — one for the host record, one for the service.
			// Either means the name resolves.
			if !p.Status().Active {
				p.set(Status{Name: host, IP: ip, Active: true})
				p.log.Info("mdns: published", "name", host, "ip", ip)
			}
		case strings.Contains(line, "Name in use"), strings.Contains(line, "conflict"):
			// Another machine on this network already answers for this name, which
			// is what happens when two Macs pick the same one.
			p.set(Status{
				Name:  host,
				IP:    ip,
				Error: host + " is already taken on this network — choose another with --mdns-name",
			})
			p.log.Warn("mdns: name already in use", "name", host)
		}
	}
}

func (p *Publisher) fail(ip string, err error) {
	p.set(Status{Name: p.hostname(), IP: ip, Error: err.Error()})
}

func (p *Publisher) set(s Status) {
	p.mu.Lock()
	p.status = s
	p.mu.Unlock()
}
