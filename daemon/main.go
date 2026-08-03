// Command marina watches every local server you have running and serves a live
// dashboard for them.
//
// Run with no arguments to start the daemon. `marina status` prints the current
// state from an already-running daemon; `marina open` opens the dashboard.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anthonybo/marina/daemon/internal/api"
	"github.com/anthonybo/marina/daemon/internal/catalog"
	"github.com/anthonybo/marina/daemon/internal/health"
	"github.com/anthonybo/marina/daemon/internal/launchsock"
	"github.com/anthonybo/marina/daemon/internal/logs"
	"github.com/anthonybo/marina/daemon/internal/mdns"
	"github.com/anthonybo/marina/daemon/internal/monitor"
	"github.com/anthonybo/marina/daemon/internal/probe"
	"github.com/anthonybo/marina/daemon/internal/store"
	"github.com/anthonybo/marina/daemon/internal/tlscert"
	"github.com/anthonybo/marina/daemon/internal/webui"
)

// version is overridden at build time with -ldflags "-X main.version=…".
var version = "dev"

// sourceDir is the checkout this binary was built from, set the same way. It
// exists so Marina does not offer to launch itself: the installed daemon runs from
// ~/.local/share with cwd "/", so without being told, nothing links it to its own
// project directory and the catalogue reports it as available to start.
var sourceDir string

const defaultAddr = "127.0.0.1:7777"

func main() {
	// Subcommands are handled before flag parsing so `marina status` reads
	// naturally without a leading dash.
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		cmd := os.Args[1]
		os.Args = append(os.Args[:1], os.Args[2:]...)
		switch cmd {
		case "status":
			os.Exit(runStatus())
		case "open":
			os.Exit(runOpen())
		case "ashore":
			os.Exit(runAshore())
		case "start":
			os.Exit(runStart())
		case "stop":
			os.Exit(runStop())
		case "serve":
			// fall through to the daemon
		case "version":
			fmt.Println("marina", version)
			return
		default:
			fmt.Fprintf(os.Stderr, "marina: unknown command %q\n\nusage: marina [serve|status|ashore|start <name>|stop <name>|open|version]\n", cmd)
			os.Exit(2)
		}
	}
	os.Exit(runServe())
}

func runServe() int {
	var (
		addr        = flag.String("addr", envOr("MARINA_ADDR", defaultAddr), "address to serve the dashboard on (loopback only)")
		interval    = flag.Duration("interval", envDuration("MARINA_INTERVAL", 2*time.Second), "how often to sweep for listening ports")
		dsn         = flag.String("dsn", envOr("MARINA_DSN", defaultDSN()), "Postgres DSN for pins, nicknames, and history")
		dbName      = flag.String("db", envOr("MARINA_DB", "marina"), "database name to create if missing")
		healthEvery = flag.Duration("health-interval", envDuration("MARINA_HEALTH_INTERVAL", 3*time.Second), "how often to sample per-app CPU and memory")
		noProbe     = flag.String("no-probe", envOr("MARINA_NO_PROBE", ""), "ports never to contact over HTTP, e.g. \"3001-3013,9229\"")
		mdnsName    = flag.String("mdns-name", envOr("MARINA_MDNS_NAME", "marina"), "short Bonjour name to publish for this machine, e.g. \"marina\" for marina.local; empty to publish nothing")
		lan         = flag.Bool("lan", envOr("MARINA_LAN", "") != "", "also listen on this machine's network address, so other devices can load the dashboard; changes are still refused from anything but this machine")
		roots       = flag.String("roots", envOr("MARINA_ROOTS", defaultRoots()), "comma-separated directories to scan for projects you could start")
		verbose     = flag.Bool("v", false, "verbose logging")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	// Refuse to bind anywhere but loopback: this daemon reports on every server
	// you are running, which is not something to expose to the network.
	if !isLoopback(*addr) {
		log.Error("marina: refusing to bind a non-loopback address", "addr", *addr)
		return 1
	}

	// Loopback unless asked otherwise. A dashboard that can start processes should
	// not become reachable by accident, so the network is opt-in and the flag says
	// what it costs. Mutations stay loopback-only either way — see api.guard.
	if *lan {
		if _, port, err := net.SplitHostPort(*addr); err == nil && isLoopback(*addr) {
			*addr = net.JoinHostPort("0.0.0.0", port)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st := store.New(ctx, *dsn, adminDSN(*dsn, *dbName), *dbName, log)
	defer st.Close()

	// Directories added in the dashboard are kept in roots.json and win over the
	// flag, which only seeds a machine that has never set them. The installer
	// removes that file when --roots is passed explicitly, so the flag is still
	// the way to override from outside.
	rootStore := catalog.NewRootStore(stateDir())
	scanRoots, fromFile := rootStore.Load(strings.Split(*roots, ","))
	if fromFile {
		log.Info("marina: scanning directories from roots.json", "roots", scanRoots)
	}

	// The catalogue rescans the filesystem far less often than the port table:
	// projects appear and disappear on the order of days, not seconds.
	cat := catalog.New(scanRoots, 30*time.Second)
	launchDir := filepath.Join(stateDir(), "launches")
	// The environment a launch runs with comes from the user's own shell, not from
	// launchd's minimal one — otherwise nvm-installed tools are simply missing.
	shellEnv := catalog.NewShellEnv(log, 10*time.Minute)
	launcher := catalog.NewLauncher(cat, launchDir, shellEnv, log)
	logStore := logs.New(launchDir)
	// Reclaim launches from a previous run so a restart doesn't lose the ability
	// to stop the fleets it started.
	launcher.Adopt()

	excluded := probe.ParsePortSet(*noProbe)
	if !excluded.Empty() {
		log.Info("marina: HTTP probing disabled for some ports", "spec", *noProbe)
	}

	// Metrics run on their own cadence. They are polled by the UI rather than
	// pushed, because CPU changes every sample and folding it into the change
	// signature would turn a quiet machine into a constant stream.
	sampler := health.NewSampler(*healthEvery, runtime.NumCPU())
	go sampler.Run(ctx)

	mon := monitor.New(st, cat, launcher, sampler, *interval, version, excluded, sourceDir, log)
	go mon.Run(ctx)

	// A short name for this machine, so a dev server is reachable at
	// marina.local:3000 from a phone instead of at an address that changes with the
	// lease. Driven from the snapshot stream rather than its own timer: the address
	// is part of the change signature, so a new lease already produces a snapshot,
	// and there is no reason for two things to be watching the network.
	if name := strings.TrimSpace(*mdnsName); name != "" {
		publisher := mdns.New(name, portOf(*addr), log)
		go publisher.Run(ctx)
		mon.SetAliasSource(func() (string, bool) {
			s := publisher.Status()
			return s.Name, s.Active
		})

		updates, unsubscribe := mon.Subscribe()
		defer unsubscribe()
		publisher.Point(mon.Snapshot().Net.IP)
		go func() {
			for snap := range updates {
				publisher.Point(snap.Net.IP)
			}
		}()
	}

	srv := api.New(mon, st, launcher, logStore, sampler, rootStore, webui.FS(), webui.Placeholder(), *addr, log)

	// A certificate the machine already trusts, minted by the installer with
	// mkcert. Without it the dashboard is plain HTTP and the browser says "Not
	// Secure", which is noise on a local tool but noise people reasonably want gone.
	if keeper, err := tlscert.Load(stateDir()); err == nil {
		srv.UseTLS(keeper, stateDir())
		log.Info("marina: serving https where available", "names", keeper.Names())
	} else if !errors.Is(err, tlscert.ErrAbsent) {
		log.Warn("marina: could not load the certificate", "err", err)
	}

	// Ports below 1024 need root to bind, and this daemon launches your dev
	// servers — it has no business being root, and neither do they. launchd binds
	// the ports instead and hands over the descriptors, so bare marina.local works
	// with nothing privileged running. Absent when started from a terminal or
	// installed without them, which is not an error.
	//
	// Port 80 only redirects. Serving the app on both schemes would mean a page
	// that is sometimes secure and sometimes not depending on how it was reached,
	// and no way to tell which you got.
	var extra []api.Extra
	for name, kind := range map[string]api.Extra{
		"Listeners": {RedirectToTLS: true},
		"TLS":       {TLS: true},
	} {
		listeners, err := launchsock.Listeners(name)
		switch {
		case err == nil:
			for _, l := range listeners {
				e := kind
				e.Listener = l
				extra = append(extra, e)
				log.Info("marina: adopted a socket from launchd", "socket", name, "addr", l.Addr().String())
			}
		case errors.Is(err, launchsock.ErrNotManaged), errors.Is(err, launchsock.ErrNoSocket):
			// Nothing of that name. Normal.
		default:
			log.Warn("marina: could not adopt launchd sockets", "socket", name, "err", err)
		}
	}

	if err := srv.ListenAndServe(ctx, extra...); err != nil {
		log.Error("marina: server stopped", "err", err)
		return 1
	}
	log.Info("marina: shut down cleanly")
	return 0
}

// runStatus prints the live state from a running daemon.
func runStatus() int {
	var addr = flag.String("addr", envOr("MARINA_ADDR", defaultAddr), "daemon address")
	flag.Parse()

	snap, err := fetchState(*addr)
	if err != nil {
		fmt.Printf("○ marina — not running (%v)\n", err)
		return 1
	}

	url := "http://" + *addr
	fmt.Printf("● marina — %d boats docked · %s\n", snap.Counts.Total, url)
	fmt.Printf("  %d apps · %d services · %d infra · %d system · %d speak HTTP · swept in %dms\n",
		snap.Counts.Primary, snap.Counts.Services, snap.Counts.Infra, snap.Counts.System,
		snap.Counts.HTTP, snap.ScanMS)
	if snap.Store.Connected {
		fmt.Printf("  postgres: connected (%s)\n", snap.Store.DSN)
	} else {
		msg := snap.Store.Error
		if msg == "" {
			msg = "connecting"
		}
		fmt.Printf("  postgres: offline — pins and history paused (%s)\n", firstLine(msg))
	}
	fmt.Println()

	// Group the apps by project so a monorepo reads as one block.
	services := snap.Services
	sort.SliceStable(services, func(i, j int) bool { return services[i].Port < services[j].Port })

	if n := len(snap.Ashore); n > 0 {
		fmt.Printf("  %d more ashore, not running — `marina ashore` to list them\n", n)
	}
	fmt.Println()

	printGroup(services, "app", "APPS")
	printGroup(services, "unknown", "OTHER")
	printGroup(services, "infra", "INFRASTRUCTURE")
	printGroup(services, "system", "SYSTEM")
	return 0
}

// printGroup prints one kind of service. Apps are printed as clusters — each
// front door followed by the services that belong to it — because ordering the
// whole list by port scatters a project's workers under whichever app happens to
// hold the lowest number.
func printGroup(services []stateService, kind, heading string) {
	var rows []stateService
	for _, s := range services {
		if s.Kind == kind {
			rows = append(rows, s)
		}
	}
	if len(rows) == 0 {
		return
	}

	fmt.Printf("  %s\n", heading)
	if kind != "app" {
		for _, s := range rows {
			printRow(s, false)
		}
		fmt.Println()
		return
	}

	// Front doors in port order, each trailed by its own services.
	var fronts []stateService
	services_ := map[int][]stateService{}
	for _, s := range rows {
		if s.Role == "service" {
			services_[s.PrimaryPort] = append(services_[s.PrimaryPort], s)
		} else {
			fronts = append(fronts, s)
		}
	}
	sort.SliceStable(fronts, func(i, j int) bool { return fronts[i].Port < fronts[j].Port })

	claimed := map[int]bool{}
	for _, front := range fronts {
		printRow(front, false)
		kids := services_[front.Port]
		sort.SliceStable(kids, func(i, j int) bool { return kids[i].Port < kids[j].Port })
		for _, kid := range kids {
			printRow(kid, true)
			claimed[kid.Port] = true
		}
	}
	// Never drop a live port just because its front door isn't listening.
	for _, kids := range services_ {
		for _, kid := range kids {
			if !claimed[kid.Port] {
				printRow(kid, false)
			}
		}
	}
	fmt.Println()
}

// printRow prints one service. A nested row leads with its script, since the
// project name is already established by the app above it.
func printRow(s stateService, nested bool) {
	pin := " "
	if s.Meta.Pinned {
		pin = "★"
	}

	name, detail := s.Display, s.Entry
	if s.Subpath != "" {
		name += " → " + lastSegment(s.Subpath)
	}
	if nested {
		name = "  └ " + detailName(s)
		// The name already carries the script, so the detail column shows where
		// it lives instead of repeating it.
		detail = lastSegment(s.Subpath)
	}
	if detail == "" && s.Framework == "" {
		detail = s.Proc
	}

	fmt.Printf("  %s :%-6d %-32s %-20s %-8s %s\n",
		pin, s.Port, truncate(name, 32), truncate(detail, 20), truncate(s.Framework, 8), uptimeOf(s))
}

// runAshore lists the projects Marina could start.
func runAshore() int {
	var addr = flag.String("addr", envOr("MARINA_ADDR", defaultAddr), "daemon address")
	flag.Parse()

	snap, err := fetchState(*addr)
	if err != nil {
		fmt.Printf("○ marina — not running (%v)\n", err)
		return 1
	}
	if len(snap.Ashore) == 0 {
		fmt.Println("Everything Marina knows how to start is already running.")
		return 0
	}

	sort.SliceStable(snap.Ashore, func(i, j int) bool { return snap.Ashore[i].Name < snap.Ashore[j].Name })
	fmt.Printf("%d ashore — not running\n\n", len(snap.Ashore))
	for _, p := range snap.Ashore {
		mark := " "
		if p.Starting {
			mark = "…"
		}
		fmt.Printf("  %s %-30s %-26s %s\n", mark, truncate(p.Name, 30), truncate(p.Command, 26), p.Framework)
	}
	if snap.AshoreSkipped > 0 {
		fmt.Printf("\n  %d more with no start command Marina recognises.\n", snap.AshoreSkipped)
	}
	fmt.Printf("\nStart one with: marina start <name>\n")
	return 0
}

// runStart launches a catalogued project by name.
func runStart() int {
	var addr = flag.String("addr", envOr("MARINA_ADDR", defaultAddr), "daemon address")
	flag.Parse()

	name := flag.Arg(0)
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: marina start <name>")
		return 2
	}

	snap, err := fetchState(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marina: daemon not running (%v)\n", err)
		return 1
	}

	// Exact match first, then a unique prefix, so short names work without
	// ever guessing between two candidates.
	var matches []ashoreProject
	for _, p := range snap.Ashore {
		if strings.EqualFold(p.Name, name) {
			matches = []ashoreProject{p}
			break
		}
		if strings.HasPrefix(strings.ToLower(p.Name), strings.ToLower(name)) {
			matches = append(matches, p)
		}
	}

	switch len(matches) {
	case 0:
		fmt.Fprintf(os.Stderr, "marina: no project ashore matching %q (try: marina ashore)\n", name)
		return 1
	case 1:
	default:
		fmt.Fprintf(os.Stderr, "marina: %q matches several projects:\n", name)
		for _, p := range matches {
			fmt.Fprintf(os.Stderr, "  %s\n", p.Name)
		}
		return 1
	}

	project := matches[0]
	body, _ := json.Marshal(map[string]string{"path": project.Path})
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post("http://"+*addr+"/api/launch", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "marina: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	var launch struct {
		Name    string `json:"name"`
		Command string `json:"command"`
		PID     int    `json:"pid"`
		LogPath string `json:"logPath"`
		Error   string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&launch)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "marina: %s\n", launch.Error)
		return 1
	}

	fmt.Printf("● starting %s\n  %s\n  pid %d · log %s\n", project.Name, launch.Command, launch.PID, launch.LogPath)
	return 0
}

// runStop shuts down a running app by name.
func runStop() int {
	var addr = flag.String("addr", envOr("MARINA_ADDR", defaultAddr), "daemon address")
	flag.Parse()

	name := flag.Arg(0)
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: marina stop <name>")
		return 2
	}

	snap, err := fetchState(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marina: daemon not running (%v)\n", err)
		return 1
	}

	// Match against running apps, exact name first then a unique prefix.
	type target struct {
		name string
		port int
	}
	var matches []target
	seen := map[string]bool{}
	for _, s := range snap.Services {
		if s.Kind != "app" || s.Role == "service" {
			continue
		}
		label := s.Display
		if seen[label] {
			continue
		}
		if strings.EqualFold(label, name) {
			matches = []target{{label, s.Port}}
			seen[label] = true
			break
		}
		if strings.HasPrefix(strings.ToLower(label), strings.ToLower(name)) {
			matches = append(matches, target{label, s.Port})
			seen[label] = true
		}
	}

	switch len(matches) {
	case 0:
		fmt.Fprintf(os.Stderr, "marina: no running app matching %q\n", name)
		return 1
	case 1:
	default:
		fmt.Fprintf(os.Stderr, "marina: %q matches several running apps:\n", name)
		for _, m := range matches {
			fmt.Fprintf(os.Stderr, "  %s (:%d)\n", m.name, m.port)
		}
		return 1
	}

	body, _ := json.Marshal(map[string]any{"port": matches[0].port, "withServices": true})
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post("http://"+*addr+"/api/stop", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "marina: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	var out struct {
		Stopped []struct {
			PID    int  `json:"pid"`
			Group  int  `json:"group"`
			Exited bool `json:"exited"`
			Forced bool `json:"forced"`
		} `json:"stopped"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "marina: %s\n", out.Error)
		return 1
	}

	forced := 0
	for _, s := range out.Stopped {
		if s.Forced {
			forced++
		}
	}
	fmt.Printf("○ stopped %s — %d process%s ended", matches[0].name, len(out.Stopped),
		map[bool]string{true: "es", false: ""}[len(out.Stopped) != 1])
	if forced > 0 {
		fmt.Printf(" (%d needed SIGKILL)", forced)
	}
	fmt.Println()
	return 0
}

func runOpen() int {
	var addr = flag.String("addr", envOr("MARINA_ADDR", defaultAddr), "daemon address")
	flag.Parse()

	if _, err := fetchState(*addr); err != nil {
		fmt.Fprintf(os.Stderr, "marina: daemon not running (%v)\n", err)
		return 1
	}
	if err := exec.Command("/usr/bin/open", "http://"+*addr).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "marina: could not open browser: %v\n", err)
		return 1
	}
	return 0
}

// A trimmed mirror of the snapshot shape, so the CLI does not depend on the
// monitor package's internals.
type stateSnapshot struct {
	Counts struct {
		Total, Apps, Infra, System, HTTP int
		Primary, Services                int
	} `json:"counts"`
	ScanMS        int64           `json:"scanMs"`
	Store         store.Health    `json:"store"`
	Services      []stateService  `json:"services"`
	Ashore        []ashoreProject `json:"ashore"`
	AshoreSkipped int             `json:"ashoreSkipped"`
}

type ashoreProject struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Manager   string `json:"manager"`
	Command   string `json:"command"`
	Framework string `json:"framework"`
	Starting  bool   `json:"starting"`
}

type stateService struct {
	Port        int    `json:"port"`
	Kind        string `json:"kind"`
	Display     string `json:"display"`
	Subpath     string `json:"subpath"`
	Framework   string `json:"framework"`
	Entry       string `json:"entry"`
	Role        string `json:"role"`
	PrimaryPort int    `json:"primaryPort"`
	Proc        string `json:"proc"`
	StartedAt   int64  `json:"startedAt"`
	Meta        struct {
		Pinned bool `json:"pinned"`
	} `json:"meta"`
}

func fetchState(addr string) (stateSnapshot, error) {
	var snap stateSnapshot
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/api/state")
	if err != nil {
		return snap, errors.New("no daemon on " + addr)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return snap, fmt.Errorf("daemon returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return snap, err
	}
	return snap, nil
}

func uptimeOf(s stateService) string {
	if s.StartedAt <= 0 {
		return ""
	}
	d := time.Since(time.Unix(s.StartedAt, 0))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("up %ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("up %dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("up %dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("up %dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// defaultRoots is where this user keeps their work. Overridable, because not
// everyone puts everything in one place.
func defaultRoots() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "projects")
}

// portOf pulls the port out of a listen address, for the Bonjour registration.
// dns-sd insists on a port even when the record we want is the host one, so a bad
// parse is harmless rather than fatal.
func portOf(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return n
}

// stateDir is where Marina keeps files it generates, alongside its binary.
func stateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Join(home, ".local", "share", "marina")
}

// defaultDSN targets the local Homebrew Postgres with the current user, which
// is the common macOS setup where local connections are trusted.
func defaultDSN() string {
	return fmt.Sprintf("postgres://%s@localhost:5432/marina?sslmode=disable", currentUser())
}

// adminDSN points at the maintenance database so the daemon can CREATE DATABASE
// on first run.
func adminDSN(dsn, dbName string) string {
	if i := strings.LastIndex(dsn, "/"+dbName); i >= 0 {
		return dsn[:i] + "/postgres" + dsn[i+len("/"+dbName):]
	}
	return fmt.Sprintf("postgres://%s@localhost:5432/postgres?sslmode=disable", currentUser())
}

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return envOr("USER", "postgres")
}

func isLoopback(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		host = addr[:i]
	}
	switch strings.Trim(host, "[]") {
	case "127.0.0.1", "localhost", "::1", "":
		return true
	}
	return false
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// detailName is what to call a service listed beneath its app, where the project
// name is already on the line above.
func detailName(s stateService) string {
	if s.Entry != "" {
		return s.Entry
	}
	if s.Subpath != "" {
		return lastSegment(s.Subpath)
	}
	return s.Display
}

// lastSegment keeps "packages/backend" readable as "backend" in the narrow CLI
// column, where the parent directory is already implied by the project name.
func lastSegment(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
