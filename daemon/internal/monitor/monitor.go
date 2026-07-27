// Package monitor runs the polling loop that keeps Marina's picture of the
// machine current, and fans snapshots out to subscribers.
//
// Snapshots are only broadcast when something meaningful actually changes.
// Uptime is deliberately not part of that comparison: the client derives it from
// startedAt and ticks it locally, so a quiet machine produces no traffic at all.
package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/anthonybo/marina/daemon/internal/catalog"
	"github.com/anthonybo/marina/daemon/internal/health"
	"github.com/anthonybo/marina/daemon/internal/identify"
	"github.com/anthonybo/marina/daemon/internal/probe"
	"github.com/anthonybo/marina/daemon/internal/scan"
	"github.com/anthonybo/marina/daemon/internal/store"
)

// Service is one live listener, fully decorated for the UI.
type Service struct {
	identify.Service
	Probe probe.Result `json:"probe"`
	Meta  store.Meta   `json:"meta"`
	// URL is set only when the port actually answered an HTTP request.
	URL string `json:"url,omitempty"`
	// Fresh marks a service that appeared moments ago, so the UI can call it out.
	Fresh bool `json:"fresh"`
	// Display is the name to show: nickname if set, otherwise the derived label.
	Display string `json:"display"`
	// ProbeSkipped is true when this port was excluded from HTTP probing, so the
	// UI can say "not probed" rather than implying the app answered nothing.
	ProbeSkipped bool `json:"probeSkipped,omitempty"`
	// Role says whether this is a project's front door, one of its supporting
	// services, or stands alone. See roles.go.
	Role Role `json:"role"`
	// ServiceCount is how many services belong to this primary (0 otherwise).
	ServiceCount int `json:"serviceCount,omitempty"`
	// PrimaryPort is the port of the primary this service belongs to (0 otherwise).
	PrimaryPort int `json:"primaryPort,omitempty"`
}

// Counts summarizes a snapshot for the menu bar, which wants a number and not
// the whole payload.
type Counts struct {
	Total  int `json:"total"`
	Apps   int `json:"apps"`
	Infra  int `json:"infra"`
	System int `json:"system"`
	HTTP   int `json:"http"`
	// Primary counts the apps you would actually open — one per project front
	// door, plus anything standing alone. Services counts the workers behind
	// them. Apps is still the raw total of both, for anything that wants it.
	Primary  int `json:"primary"`
	Services int `json:"services"`
}

// Snapshot is the complete state Marina serves.
type Snapshot struct {
	Rev       int64        `json:"rev"`
	At        time.Time    `json:"at"`
	Services  []Service    `json:"services"`
	Counts    Counts       `json:"counts"`
	Store     store.Health `json:"store"`
	StartedAt time.Time    `json:"daemonStartedAt"`
	Version   string       `json:"version"`
	ScanMS    int64        `json:"scanMs"`
	// Ashore lists projects found on disk that aren't running.
	Ashore []Ashore `json:"ashore"`
	// AshoreSkipped counts project directories with no discoverable start
	// command, reported so the roots don't look emptier than they are.
	AshoreSkipped int `json:"ashoreSkipped"`
}

// Monitor owns the poll loop and the subscriber set.
type Monitor struct {
	resolver *identify.Resolver
	prober   *probe.Prober
	store    *store.Store
	catalog  *catalog.Catalog
	launcher *catalog.Launcher
	sampler  *health.Sampler
	log      *slog.Logger

	interval  time.Duration
	version   string
	startedAt time.Time
	noProbe   probe.PortSet
	// selfSource is the checkout this daemon was built from, counted as running so
	// Marina never lists itself as available to start.
	selfSource string

	mu       sync.RWMutex
	current  Snapshot
	sig      string
	rev      int64
	firstFor map[string]time.Time // service key -> when this daemon first saw it

	subsMu sync.Mutex
	subs   map[chan Snapshot]struct{}

	// healthLogged keeps the not-ready notice to once rather than every sweep.
	healthLogged bool
}

// New builds a Monitor. Nothing starts until Run is called.
func New(
	st *store.Store,
	cat *catalog.Catalog,
	launcher *catalog.Launcher,
	sampler *health.Sampler,
	interval time.Duration,
	version string,
	noProbe probe.PortSet,
	selfSource string,
	log *slog.Logger,
) *Monitor {
	return &Monitor{
		resolver:   identify.New(cat.Roots()),
		prober:     probe.New(),
		store:      st,
		catalog:    cat,
		launcher:   launcher,
		sampler:    sampler,
		log:        log,
		interval:   interval,
		version:    version,
		noProbe:    noProbe,
		selfSource: selfSource,
		startedAt:  time.Now(),
		firstFor:   make(map[string]time.Time),
		subs:       make(map[chan Snapshot]struct{}),
	}
}

// Run polls until ctx is cancelled. It performs one immediate sweep so the
// dashboard has data the moment it connects.
func (m *Monitor) Run(ctx context.Context) {
	m.tick(ctx)

	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.tick(ctx)
		}
	}
}

// Snapshot returns the most recent state.
func (m *Monitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// Subscribe returns a channel that receives every future snapshot, plus a
// cancel function. The channel is buffered and lossy by design: a slow client
// gets the newest state rather than stalling the loop.
func (m *Monitor) Subscribe() (<-chan Snapshot, func()) {
	ch := make(chan Snapshot, 1)
	m.subsMu.Lock()
	m.subs[ch] = struct{}{}
	m.subsMu.Unlock()

	return ch, func() {
		m.subsMu.Lock()
		if _, ok := m.subs[ch]; ok {
			delete(m.subs, ch)
			close(ch)
		}
		m.subsMu.Unlock()
	}
}

// Refresh forces an immediate sweep, used after a pin or rename so the change
// is reflected without waiting for the next tick.
func (m *Monitor) Refresh(ctx context.Context) { m.tick(ctx) }

// freshWindow is how long a newly started service is called out in the UI.
const freshWindow = 20 * time.Second

// isFresh answers "did this service only just start?" — which is deliberately
// not the same question as "is this the first time I've seen it?".
//
// The process's own start time is the honest signal: restarting the daemon must
// not make a server that has been up for two days look brand new. Only when the
// start time is unavailable do we fall back to our own bookkeeping, and even
// then we stay quiet during the daemon's first moments so a launch at login
// doesn't light up every row at once.
func (m *Monitor) isFresh(startedAt int64, firstSeen, now time.Time) bool {
	if startedAt > 0 {
		return now.Sub(time.Unix(startedAt, 0)) < freshWindow
	}
	if now.Sub(m.startedAt) < freshWindow+5*time.Second {
		return false
	}
	return now.Sub(firstSeen) < freshWindow
}

func (m *Monitor) tick(ctx context.Context) {
	start := time.Now()

	sockets, err := scan.Listeners(ctx)
	if err != nil {
		m.log.Warn("monitor: listener sweep failed", "err", err)
		return
	}

	alive := make(map[int]bool, len(sockets))
	pids := make([]int, 0, len(sockets))
	for _, s := range sockets {
		if !alive[s.PID] {
			alive[s.PID] = true
			pids = append(pids, s.PID)
		}
	}

	// Only pay for process detail on PIDs we haven't identified before. On a
	// quiet machine this is empty, so a tick costs one lsof call and nothing else.
	var procs map[int]scan.Proc
	if unresolved := m.resolver.Unresolved(pids); len(unresolved) > 0 {
		var err error
		if procs, err = scan.Procs(ctx, unresolved); err != nil {
			m.log.Debug("monitor: process detail failed", "err", err)
		}
	}

	// Drop cache entries for processes that no longer hold a port.
	m.resolver.Forget(alive)
	m.prober.Forget(alive)

	services := make([]Service, 0, len(sockets))
	for _, sock := range sockets {
		id := m.resolver.Identify(sock, procs[sock.PID])
		services = append(services, Service{Service: id})
	}

	// Where each app writes its output. One lsof call, and it is what lets the
	// terminals view either show a log or explain precisely why it cannot.
	var appPIDs []int
	for _, s := range services {
		if s.Kind == identify.KindApp {
			appPIDs = append(appPIDs, s.PID)
		}
	}
	outputs := scan.Outputs(ctx, appPIDs)
	for i := range services {
		if out, ok := outputs[services[i].PID]; ok {
			services[i].Output = out
		}
	}

	m.probeAll(ctx, services)

	now := time.Now()
	m.mu.Lock()
	for i := range services {
		svc := &services[i]

		first, seen := m.firstFor[svc.Key]
		if !seen {
			first = now
			m.firstFor[svc.Key] = first
		}
		svc.Fresh = m.isFresh(svc.StartedAt, first, now)

		svc.Meta = m.store.Meta(svc.Key)
		svc.Display = svc.Label
		if svc.Meta.Nickname != "" {
			svc.Display = svc.Meta.Nickname
		}
		if svc.Probe.HTTP {
			svc.URL = svc.Probe.Scheme + "://localhost:" + strconv.Itoa(svc.Port) + "/"
		}
	}
	// Forget first-seen bookkeeping for services that are gone, so a later
	// restart is correctly highlighted as fresh again.
	live := make(map[string]bool, len(services))
	for _, s := range services {
		live[s.Key] = true
	}
	for k := range m.firstFor {
		if !live[k] {
			delete(m.firstFor, k)
		}
	}
	m.mu.Unlock()

	// Roles depend on probe titles and on pins, so this has to come after both.
	assignRoles(services)
	sortServices(services)

	// What could be running, but isn't. The catalogue caches its own filesystem
	// scan, so this costs nothing on most ticks.
	livePaths := runningPaths(services)
	// This daemon is running, so the checkout it was built from is running too —
	// even though the binary lives elsewhere and reports no working directory.
	for _, path := range selfPaths(m.selfSource) {
		livePaths[path] = true
	}
	m.launcher.Settled(livePaths)
	projects, skipped := m.catalog.Projects(ctx)
	ashore := ashoreFrom(projects, livePaths, m.launcher.Recent(),
		m.store.LastSeenPath, m.store.PortsForPath, occupiedPorts(services))

	snap := Snapshot{
		At:            now,
		Services:      services,
		Counts:        countOf(services),
		Store:         m.store.Health(),
		StartedAt:     m.startedAt,
		Version:       m.version,
		ScanMS:        time.Since(start).Milliseconds(),
		Ashore:        ashore,
		AshoreSkipped: skipped,
	}

	sig := signature(services, ashore, snap.Store)

	m.mu.Lock()
	changed := sig != m.sig
	if changed {
		m.rev++
		m.sig = sig
	}
	snap.Rev = m.rev
	m.current = snap
	m.mu.Unlock()

	// Keep a short per-app series for the sparklines. This only reads the
	// sampler's cached numbers — it never triggers a sample of its own.
	if m.sampler != nil && m.sampler.Ready() {
		liveKeys := make(map[string]bool, len(services))
		for _, s := range services {
			if s.Kind != identify.KindApp || s.Role == RoleService {
				continue
			}
			liveKeys[s.Key] = true
			m.sampler.Record(s.Key, m.sampler.Groups(AppGroups(m.sampler, services, s)...))
		}
		m.sampler.Forget(liveKeys)
		m.healthLogged = true
	} else if m.sampler != nil && !m.healthLogged {
		m.log.Debug("monitor: health sampler not ready yet; no history recorded")
	}

	go m.store.RecordSeen(ctx, seenFrom(services))

	if changed {
		m.broadcast(snap)
	}
}

// probeAll probes every candidate port concurrently, bounded so a machine with
// many listeners doesn't open dozens of sockets at once. Cached results return
// immediately, so the steady-state cost here is near zero.
func (m *Monitor) probeAll(ctx context.Context, services []Service) {
	sem := make(chan struct{}, 12)
	var wg sync.WaitGroup

	for i := range services {
		svc := &services[i]
		if svc.Kind == identify.KindSystem || probe.SkipProc(svc.Proc) {
			continue
		}
		// An explicitly excluded port is never contacted at all.
		if m.noProbe.Has(svc.Port) {
			svc.ProbeSkipped = true
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			svc.Probe = m.prober.Probe(ctx, svc.PID, svc.Port, svc.Hosts)
		}()
	}
	wg.Wait()
}

func (m *Monitor) broadcast(snap Snapshot) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for ch := range m.subs {
		// Replace any queued-but-unread snapshot with this newer one.
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- snap:
		default:
		}
	}
}

// AppGroups returns the process groups that make up one app: the group of its
// front door plus the groups of the services behind it.
//
// Usually that is a single group — a `concurrently` tree shares one — but taking
// the union covers a project whose parts were started separately and therefore sit
// in different groups.
func AppGroups(sampler *health.Sampler, services []Service, primary Service) []int {
	pids := make([]int, 0, 8)
	pids = append(pids, primary.PID)
	for _, s := range services {
		if s.Role == RoleService && s.PrimaryPort == primary.Port && s.Project == primary.Project {
			pids = append(pids, s.PID)
		}
	}
	// One lock acquisition for the whole app rather than one per pid.
	return sampler.GroupsOf(pids...)
}

func countOf(services []Service) Counts {
	var c Counts
	c.Total = len(services)
	for _, s := range services {
		switch s.Kind {
		case identify.KindApp:
			c.Apps++
		case identify.KindInfra:
			c.Infra++
		case identify.KindSystem:
			c.System++
		}
		if s.Probe.HTTP {
			c.HTTP++
		}
		if s.Kind == identify.KindApp {
			switch s.Role {
			case RoleService:
				c.Services++
			default:
				c.Primary++
			}
		}
	}
	return c
}

func seenFrom(services []Service) []store.Seen {
	out := make([]store.Seen, 0, len(services))
	for _, s := range services {
		var started time.Time
		if s.StartedAt > 0 {
			started = time.Unix(s.StartedAt, 0)
		}
		out = append(out, store.Seen{
			Key:       s.Key,
			Label:     s.Label,
			Project:   s.Project,
			Kind:      string(s.Kind),
			Port:      s.Port,
			PID:       s.PID,
			StartedAt: started,
		})
	}
	return out
}

// signature captures everything a client would render differently, and nothing
// that merely ticks with the clock.
func signature(services []Service, ashore []Ashore, health store.Health) string {
	h := sha256.New()
	var buf [8]byte
	write := func(s string) { h.Write([]byte(s)); h.Write([]byte{0}) }
	writeInt := func(v int64) {
		binary.LittleEndian.PutUint64(buf[:], uint64(v))
		h.Write(buf[:])
	}

	for _, s := range services {
		write(s.Key)
		writeInt(int64(s.PID))
		writeInt(int64(s.Port))
		write(s.Display)
		write(string(s.Kind))
		write(s.Framework)
		write(s.Probe.Title)
		write(s.Probe.Scheme)
		write(string(s.Role))
		write(string(s.Output.Kind))
		write(s.Output.Path)
		writeInt(int64(s.ServiceCount))
		writeInt(int64(s.Probe.Status))
		writeInt(s.StartedAt)
		if s.Meta.Pinned {
			write("pin")
		}
		if s.Fresh {
			write("fresh")
		}
	}
	// Changes to what is available to start must reach subscribers too, including
	// a launch that has just failed — that is precisely the moment the UI needs to
	// stop showing a spinner.
	for _, a := range ashore {
		write(a.Path)
		write(a.Command)
		if a.Starting {
			write("starting")
		}
		if a.Failed {
			write("failed:" + a.Error)
		}
		// A port becoming free or taken changes the warning, so it must broadcast.
		for _, c := range a.Conflicts {
			writeInt(int64(c.Port))
			write(c.HeldBy)
		}
	}
	if health.Connected {
		write("db-up")
	} else {
		write("db-down:" + health.Error)
	}
	return hex.EncodeToString(h.Sum(nil))
}
