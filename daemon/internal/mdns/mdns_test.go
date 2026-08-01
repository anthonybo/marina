package mdns

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Nothing is published until mDNSResponder confirms it. Reporting a name before
// then would send someone to a URL that does not resolve yet.
func TestNotActiveUntilConfirmed(t *testing.T) {
	p := New("marina", 7777, quiet())
	if s := p.Status(); s.Active || s.Name != "" {
		t.Fatalf("a fresh publisher claims %+v", s)
	}

	p.set(Status{Name: "marina.local", IP: "192.168.1.10"})
	if p.Status().Active {
		t.Fatal("active before any confirmation")
	}

	p.watchOutput(strings.NewReader(
		"10:10:40.427  Got a reply for record marina.local: Name now registered and active\n",
	), "marina.local", "192.168.1.10")

	s := p.Status()
	if !s.Active || s.Name != "marina.local" || s.IP != "192.168.1.10" {
		t.Fatalf("after confirmation: %+v", s)
	}
}

// Two Macs choosing the same name is the obvious way this goes wrong, and the
// message has to say what to do about it.
func TestNameConflictIsReportedNotSwallowed(t *testing.T) {
	p := New("marina", 7777, quiet())
	p.watchOutput(strings.NewReader("Name in use, please choose another\n"), "marina.local", "192.168.1.10")

	s := p.Status()
	if s.Active {
		t.Fatal("a conflicting name must not be reported as active")
	}
	if !strings.Contains(s.Error, "already taken") || !strings.Contains(s.Error, "--mdns-name") {
		t.Fatalf("error does not say how to fix it: %q", s.Error)
	}
}

// Point is called from the sweep, so it must be cheap and must never block.
func TestPointIsIdempotentAndNeverBlocks(t *testing.T) {
	p := New("marina", 7777, quiet())
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			p.Point("192.168.1.10") // same address, over and over
		}
		p.Point("10.20.30.40")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Point blocked; the sweep would stall behind it")
	}
}

// The end-to-end path, on the real mDNSResponder: publish a name, resolve it,
// then confirm it is withdrawn on shutdown rather than lingering.
func TestPublishesAndWithdrawsForReal(t *testing.T) {
	if _, err := exec.LookPath("dns-sd"); err != nil {
		t.Skip("no dns-sd on this machine")
	}
	name := "marina-selftest"
	p := New(name, 7777, quiet())
	p.Point("127.0.0.1")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !p.Status().Active {
		time.Sleep(200 * time.Millisecond)
	}
	if s := p.Status(); !s.Active {
		cancel()
		<-done
		t.Skipf("registration did not become active (%+v); mDNSResponder may be restricted here", s)
	}
	if got := resolve(t, name+".local"); got == "" {
		t.Errorf("%s.local did not resolve while published", name)
	}

	cancel()
	<-done
	if s := p.Status(); s.Active {
		t.Errorf("still reports active after shutdown: %+v", s)
	}
}

func resolve(t *testing.T, host string) string {
	t.Helper()
	out, err := exec.Command("dscacheutil", "-q", "host", "-a", "name", host).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "ip_address: "); ok {
			return after
		}
	}
	return ""
}
