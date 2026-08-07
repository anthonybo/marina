// Package api exposes Marina's state over HTTP: a JSON snapshot, a Server-Sent
// Events stream for live updates, and small mutation endpoints for pins and
// nicknames.
//
// The server binds to loopback only. Because a local daemon with mutating
// endpoints is reachable from any page in the user's browser, mutations are
// additionally guarded by an Origin check to shut out cross-site requests and
// DNS-rebinding.
package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anthonybo/marina/daemon/internal/catalog"
	"github.com/anthonybo/marina/daemon/internal/health"
	"github.com/anthonybo/marina/daemon/internal/identify"
	"github.com/anthonybo/marina/daemon/internal/logs"
	"github.com/anthonybo/marina/daemon/internal/monitor"
	"github.com/anthonybo/marina/daemon/internal/procs"
	"github.com/anthonybo/marina/daemon/internal/store"
	"github.com/anthonybo/marina/daemon/internal/tlscert"
)

// Server wires the monitor and store to HTTP handlers.
type Server struct {
	mon      *monitor.Monitor
	store    *store.Store
	launcher *catalog.Launcher
	logs     *logs.Store
	health   *health.Sampler
	ui       fs.FS
	addr     string
	log      *slog.Logger

	// tls holds the certificate for HTTPS listeners, nil when none is installed.
	tls *tlscert.Keeper
	// stateDir is where the CA copy lives, for the trust page.
	stateDir string

	// roots persists the scanned-directory list edited from the dashboard.
	roots *catalog.RootStore
	// rootsMu serialises read-modify-write on that list.
	rootsMu sync.Mutex

	// placeholder is served when the dashboard bundle is absent.
	placeholder []byte

	faviconClient *http.Client
}

// New builds a Server. ui may be nil, in which case placeholder is served in
// its place so the daemon still explains itself.
func New(
	mon *monitor.Monitor,
	st *store.Store,
	launcher *catalog.Launcher,
	logStore *logs.Store,
	sampler *health.Sampler,
	roots *catalog.RootStore,
	ui fs.FS,
	placeholder []byte,
	addr string,
	log *slog.Logger,
) *Server {
	return &Server{
		mon:         mon,
		store:       st,
		launcher:    launcher,
		logs:        logStore,
		health:      sampler,
		roots:       roots,
		ui:          ui,
		placeholder: placeholder,
		addr:        addr,
		log:         log,
		faviconClient: &http.Client{
			Timeout:   2 * time.Second,
			Transport: &http.Transport{DisableKeepAlives: true},
		},
	}
}

// UseTLS supplies the certificate for HTTPS listeners, and the directory holding
// the CA copy that other devices need in order to trust it.
func (s *Server) UseTLS(k *tlscert.Keeper, stateDir string) {
	s.tls = k
	s.stateDir = stateDir
}

// Handler returns the fully-routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/stream", s.handleStream)
	mux.HandleFunc("GET /api/favicon", s.handleFavicon)
	mux.HandleFunc("GET /api/history", s.handleHistory)
	mux.HandleFunc("POST /api/pin", s.guard(s.handlePin))
	mux.HandleFunc("POST /api/nickname", s.guard(s.handleNickname))
	mux.HandleFunc("POST /api/launch", s.guard(s.handleLaunch))
	mux.HandleFunc("POST /api/stop", s.guard(s.handleStop))
	mux.HandleFunc("GET /api/health", s.handleAppHealth)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/logs/content", s.handleLogContent)
	mux.HandleFunc("POST /api/logs/dismiss", s.guard(s.handleLogDismiss))
	// Reachable over plain HTTP by design — see trust.go. A device cannot fetch the
	// CA over a connection secured by the certificate that CA has to validate.
	mux.HandleFunc("GET "+trustRoutes.page, s.handleTrustPage)
	mux.HandleFunc("GET "+trustRoutes.cert, s.handleCACert)
	mux.HandleFunc("GET /api/roots", s.handleRoots)
	mux.HandleFunc("POST /api/roots/add", s.guard(s.handleRootAdd))
	mux.HandleFunc("POST /api/roots/remove", s.guard(s.handleRootRemove))

	if s.ui != nil {
		mux.Handle("/", s.spaHandler())
	} else if len(s.placeholder) > 0 {
		// The daemon is useful without the bundle, so say so rather than 404.
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if r.URL.Path != "/" {
				w.WriteHeader(http.StatusNotFound)
			}
			_, _ = w.Write(s.placeholder)
		})
	}
	return mux
}

// Extra describes a listener someone else already bound — in practice launchd,
// which is how the privileged ports are served without anything running as root.
type Extra struct {
	Listener net.Listener
	// TLS serves HTTPS here, using the configured certificate.
	TLS bool
	// RedirectToTLS answers with a permanent redirect to the https origin instead
	// of serving the app. Used on port 80 so that typing the bare name lands on a
	// padlock rather than on a page the browser calls "Not Secure".
	RedirectToTLS bool
}

// ListenAndServe serves until ctx is cancelled.
//
// Every listener carries the same handler, so the guard that refuses changes from
// anywhere but this machine applies identically — a privileged port must not become
// a way around it. The one exception is a redirect-only listener, which serves no
// app surface at all.
func (s *Server) ListenAndServe(ctx context.Context, extra ...Extra) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the SSE stream is intentionally long-lived.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.addr, err)
	}
	s.log.Info("marina: listening", "addr", "http://"+s.addr)

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	// Each inherited listener gets its own Serve. Their errors are logged rather
	// than returned: losing the shorter URL should not take the dashboard down with
	// it, and the address on its own port is the one people rely on.
	//
	// The plain :7777 listener is deliberately never redirected — `marina status`,
	// the menu bar app, and the install script all speak plain HTTP to it, and
	// bouncing them to https would break every one of them for a padlock they never
	// see.
	for _, e := range extra {
		addr := e.Listener.Addr().String()
		switch {
		case e.RedirectToTLS:
			s.log.Info("marina: redirecting to https", "addr", addr)
			go s.serveOn(ctx, e.Listener, s.redirectHandler(), nil, addr)
		case e.TLS:
			if s.tls == nil {
				s.log.Warn("marina: no certificate, cannot serve https here", "addr", addr)
				e.Listener.Close()
				continue
			}
			s.log.Info("marina: also listening (https)", "addr", addr, "names", s.tls.Names())
			go s.serveOn(ctx, e.Listener, srv.Handler, s.tls.Config(), addr)
		default:
			s.log.Info("marina: also listening", "addr", addr)
			go s.serveOn(ctx, e.Listener, srv.Handler, nil, addr)
		}
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// serveOn runs one inherited listener, optionally wrapped in TLS.
func (s *Server) serveOn(ctx context.Context, l net.Listener, h http.Handler, tlsCfg *tls.Config, addr string) {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         tlsCfg,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	var err error
	if tlsCfg != nil {
		// The certificate comes from TLSConfig.GetCertificate, so no files here.
		err = srv.ServeTLS(l, "", "")
	} else {
		err = srv.Serve(l)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.log.Warn("marina: listener stopped", "addr", addr, "err", err)
	}
}

// redirectHandler sends a request to the https origin for the same host.
//
// The port is dropped rather than translated: this only ever runs on port 80, and
// its counterpart is 443, so the bare name is exactly right.
func (s *Server) redirectHandler() http.Handler {
	app := s.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The two pages that exist to fix an untrusted certificate cannot be behind
		// that certificate. Everything else redirects.
		if r.URL.Path == trustRoutes.page || r.URL.Path == trustRoutes.cert {
			app.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		target := "https://" + host + r.URL.RequestURI()
		// 307 rather than 301: a permanent redirect gets cached hard by browsers,
		// and a local dashboard whose certificate might be removed later should not
		// leave a permanent instruction behind in every browser that saw it.
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	snap := s.mon.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"version":  snap.Version,
		"uptime":   int64(time.Since(snap.StartedAt).Seconds()),
		"services": snap.Counts,
		"store":    snap.Store,
	})
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.mon.Snapshot())
}

// handleStream pushes a snapshot on connect and on every subsequent change.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	updates, cancel := s.mon.Subscribe()
	defer cancel()

	send := func(snap monitor.Snapshot) bool {
		payload, err := json.Marshal(snap)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !send(s.mon.Snapshot()) {
		return
	}

	// A periodic comment keeps intermediaries from reaping an idle stream.
	beat := time.NewTicker(20 * time.Second)
	defer beat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case snap, open := <-updates:
			if !open {
				return
			}
			if !send(snap) {
				return
			}
		case <-beat.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handlePin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key    string `json:"key"`
		Pinned bool   `json:"pinned"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	s.store.SetPinned(r.Context(), body.Key, body.Pinned)
	s.mon.Refresh(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleNickname(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key      string `json:"key"`
		Nickname string `json:"nickname"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	if len(body.Nickname) > 60 {
		body.Nickname = body.Nickname[:60]
	}
	s.store.SetNickname(r.Context(), body.Key, body.Nickname)
	s.mon.Refresh(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAppHealth reports what each app is costing the machine.
//
// Deliberately a separate, polled endpoint rather than part of the snapshot. CPU
// changes on every sample, so folding it into the state that drives Server-Sent
// Events would mean broadcasting continuously and losing the property that a quiet
// machine produces no traffic at all. The UI asks for this only while something is
// looking at it.
func (s *Server) handleAppHealth(w http.ResponseWriter, r *http.Request) {
	type appHealth struct {
		Key     string        `json:"key"`
		Port    int           `json:"port"`
		Project string        `json:"project"`
		Display string        `json:"display"`
		Sample  health.Sample `json:"sample"`
		// Trend says whether this app's memory is heading somewhere bad, which is
		// the part a number alone never conveyed.
		Trend     health.Trend   `json:"trend"`
		History   []health.Point `json:"history,omitempty"`
		Services  int            `json:"services"`
		StartedAt int64          `json:"startedAt,omitempty"`
	}

	snap := s.mon.Snapshot()
	includeHistory := r.URL.Query().Get("history") != "0"

	apps := make([]appHealth, 0, 8)
	for _, svc := range snap.Services {
		if svc.Kind != identify.KindApp || svc.Role == monitor.RoleService {
			continue
		}
		count := 0
		for _, other := range snap.Services {
			if other.Role == monitor.RoleService && other.PrimaryPort == svc.Port &&
				other.Project == svc.Project {
				count++
			}
		}
		groups := monitor.AppGroups(s.health, snap.Services, svc)

		entry := appHealth{
			Key:     svc.Key,
			Port:    svc.Port,
			Project: svc.Project,
			Display: svc.Display,
			Sample:  s.health.Groups(groups...),
			// Read from the same history the sparkline draws, so what the boat shows
			// and what the chart shows can never disagree.
			Trend:     s.health.Trend(svc.Key),
			Services:  count,
			StartedAt: svc.StartedAt,
		}
		if includeHistory {
			entry.History = s.health.History(svc.Key)
		}
		apps = append(apps, entry)
	}

	sort.Slice(apps, func(i, j int) bool { return apps[i].Sample.CPU > apps[j].Sample.CPU })

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"apps":    apps,
		"machine": s.health.Machine(),
		// Clients should not poll faster than the daemon samples.
		"intervalMs": s.health.Interval().Milliseconds(),
		"ready":      s.health.Ready(),
	})
}

// handleStop shuts an app down.
//
// The guards here are the point, because this is the only destructive thing the
// daemon does:
//
//   - Only apps. Infrastructure and system processes are refused: a dev dashboard
//     has no business killing Postgres, and doing so would break Marina itself.
//   - Only what is currently listening, addressed by port or by project path —
//     never a raw PID from the client, so a stale or hostile request cannot pick
//     an arbitrary process.
//   - Never Marina. Stopping the daemon from its own dashboard leaves you with a
//     page that cannot tell you what happened.
//   - The whole process group only for apps Marina started, where it owns the
//     session. For anything else, one PID, because its group contains the user's
//     own terminal.
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Port int    `json:"port"`
		Path string `json:"path"`
		// WithServices also stops the supporting services behind an app.
		WithServices bool `json:"withServices"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Port == 0 && body.Path == "" {
		http.Error(w, "port or path required", http.StatusBadRequest)
		return
	}

	// A project Marina launched: stop the group it owns, which takes the whole
	// tree — package manager, concurrently, and every worker.
	if body.Path != "" {
		if !s.launcher.Launched(body.Path) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "Marina did not start this app, so it can only be stopped by port",
			})
			return
		}
		result, err := s.launcher.Stop(r.Context(), body.Path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		s.mon.Refresh(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"stopped": []procs.Result{result}})
		return
	}

	snap := s.mon.Snapshot()

	var target *monitor.Service
	for i := range snap.Services {
		if snap.Services[i].Port == body.Port {
			target = &snap.Services[i]
			break
		}
	}
	if target == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "nothing is listening on that port"})
		return
	}
	if reason, ok := s.refuseStop(*target); !ok {
		s.log.Warn("api: stop refused", "port", body.Port, "reason", reason)
		writeJSON(w, http.StatusForbidden, map[string]any{"error": reason})
		return
	}

	// If this app was launched by Marina, prefer the group stop: it is the only
	// way to take down the workers it spawned.
	if target.Repo != "" && s.launcher.Launched(target.Repo) {
		if result, err := s.launcher.Stop(r.Context(), target.Repo); err == nil {
			s.mon.Refresh(r.Context())
			writeJSON(w, http.StatusOK, map[string]any{"stopped": []procs.Result{result}})
			return
		}
	}

	// An app Marina did not start is still usually stoppable as a group: job
	// control puts a dev server in its own process group, which is exactly why
	// Ctrl+C stops the server and not the shell. Verify that before relying on it —
	// if anything in the group works outside this project, the group is shared and
	// must not be signalled.
	projectDir := target.Repo
	if projectDir == "" {
		projectDir = target.Dir
	}
	if pgid, err := syscall.Getpgid(target.PID); err == nil && projectDir != "" {
		ok, why := procs.GroupBelongsTo(r.Context(), pgid, projectDir)
		if ok {
			// Tell the launcher first if this is something it started. SIGTERM shows
			// up as exit 143, and without this the watcher reads a deliberate stop as
			// a crash — a label that then sticks until the next launch.
			s.launcher.MarkStopped(projectDir)
			result := procs.TerminateGroup(r.Context(), pgid, target.PID)
			s.log.Info("api: stopped group", "port", target.Port, "project", target.Project,
				"group", pgid, "forced", result.Forced, "exited", result.Exited)
			s.mon.Refresh(r.Context())
			writeJSON(w, http.StatusOK, map[string]any{
				"stopped": []procs.Result{result},
				"scope":   "process group " + strconv.Itoa(pgid),
			})
			return
		}
		s.log.Info("api: group stop not safe, falling back to single processes",
			"port", target.Port, "group", pgid, "why", why)
	}

	// Fall back to individual processes.
	targets := []monitor.Service{*target}
	if body.WithServices {
		for _, svc := range snap.Services {
			if svc.Role == monitor.RoleService && svc.PrimaryPort == target.Port && svc.Port != target.Port {
				if _, ok := s.refuseStop(svc); ok {
					targets = append(targets, svc)
				}
			}
		}
	}

	// Same reasoning as the group stop above: mark before signalling.
	s.launcher.MarkStopped(projectDir)

	results := make([]procs.Result, 0, len(targets))
	for _, svc := range targets {
		result := procs.Terminate(r.Context(), svc.PID, procs.Options{})
		s.log.Info("api: stopped", "port", svc.Port, "project", svc.Project,
			"pid", svc.PID, "exited", result.Exited, "forced", result.Forced)
		results = append(results, result)
	}

	s.mon.Refresh(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"stopped": results, "scope": "individual processes"})
}

// refuseStop states why a service may not be stopped, or approves it.
func (s *Server) refuseStop(svc monitor.Service) (string, bool) {
	switch svc.Kind {
	case identify.KindInfra:
		return "Marina won't stop infrastructure — " + svc.Label +
			" is shared, and Marina itself depends on Postgres. Stop it with brew services.", false
	case identify.KindSystem:
		return "Marina won't stop system processes like " + svc.Label, false
	}
	if svc.PID == os.Getpid() || svc.PID == os.Getppid() {
		return "that's Marina itself", false
	}
	if strings.EqualFold(svc.Project, "marina") || svc.Proc == "marina" {
		return "that's Marina itself — use launchctl if you really want it stopped", false
	}
	if svc.PID <= 1 {
		return "refusing to signal that process", false
	}
	return "", true
}

// handleLogs lists every terminal Marina can show, from both sources: logs it
// wrote itself when launching something, and running apps that happen to send
// their output to a file. It also reports the apps whose output is unreachable,
// with the specific reason — a pipe held by a terminal cannot be read by anyone
// else, and saying so is more useful than an empty pane.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	entries, err := s.logs.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	byName := make(map[string]int, len(entries))
	for i, e := range entries {
		byName[e.Name] = i
	}

	type unreachable struct {
		Project string `json:"project"`
		Port    int    `json:"port"`
		Kind    string `json:"kind"`
		Path    string `json:"path,omitempty"`
	}
	var blocked []unreachable
	seen := make(map[string]bool)

	for _, svc := range s.mon.Snapshot().Services {
		if svc.Kind != identify.KindApp {
			continue
		}
		name := svc.Project
		if name == "" {
			name = svc.Label
		}

		// A launch log we already listed: just mark it live.
		if i, ok := byName[name]; ok {
			entries[i].Running = true
			continue
		}

		if svc.Output.Readable() {
			// Marina didn't start this, but its output is a file, so it can still
			// be shown. Keyed by port, which is what the client will ask for.
			//
			// Confirm the file is still there: a process keeps its descriptor open
			// after the file is deleted, and offering a terminal that 404s on click
			// would be worse than not offering it.
			info, statErr := os.Stat(svc.Output.Path)
			if statErr == nil && info.Mode().IsRegular() {
				entries = append(entries, logs.Entry{
					Name:    name,
					Path:    svc.Output.Path,
					Size:    info.Size(),
					ModTime: info.ModTime(),
					Running: true,
					Source:  "process",
					Port:    svc.Port,
				})
				continue
			}
		}

		if !seen[name] {
			seen[name] = true
			blocked = append(blocked, unreachable{
				Project: name,
				Port:    svc.Port,
				Kind:    string(svc.Output.Kind),
				Path:    svc.Output.Path,
			})
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"logs":        entries,
		"unreachable": blocked,
		"dir":         s.logs.Dir(),
	})
}

// handleLogDismiss removes a finished terminal from the list.
//
// Only Marina's own launch logs, and only when the app is not running: these are
// files Marina wrote, so deleting one is tidying up after itself. A log belonging
// to a process Marina merely reads is never touched.
func (s *Server) handleLogDismiss(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}

	for _, svc := range s.mon.Snapshot().Services {
		if svc.Kind == identify.KindApp && strings.EqualFold(svc.Project, body.Name) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": body.Name + " is still running — stop it first",
			})
			return
		}
	}

	if err := s.logs.Remove(body.Name); err != nil {
		s.log.Debug("api: dismiss failed", "name", body.Name, "err", err)
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such log"})
		return
	}
	s.log.Info("api: dismissed terminal", "name", body.Name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleLogContent returns a slice of one log. A negative offset tails it; any
// other offset continues from there so following costs only the new bytes.
func (s *Server) handleLogContent(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	portParam := r.URL.Query().Get("port")
	if name == "" && portParam == "" {
		http.Error(w, "name or port required", http.StatusBadRequest)
		return
	}

	offset := int64(-1)
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.Error(w, "offset must be a number", http.StatusBadRequest)
			return
		}
		offset = parsed
	}
	var max int64
	if raw := r.URL.Query().Get("max"); raw != "" {
		max, _ = strconv.ParseInt(raw, 10, 64)
	}

	// A port is resolved to a path through live process state, so the client
	// never supplies a path. Anything not currently listening has no log.
	var chunk logs.Chunk
	var err error
	if portParam != "" {
		port, convErr := strconv.Atoi(portParam)
		if convErr != nil {
			http.Error(w, "port must be a number", http.StatusBadRequest)
			return
		}
		var found bool
		for _, svc := range s.mon.Snapshot().Services {
			if svc.Port == port && svc.Output.Readable() {
				label := svc.Project
				if label == "" {
					label = svc.Label
				}
				chunk, err = s.logs.ReadPath(label, svc.Output.Path, offset, max)
				found = true
				break
			}
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "no readable log for that port"})
			return
		}
	} else {
		chunk, err = s.logs.Read(name, offset, max)
	}
	if err != nil {
		// A bad name and a missing file are both "there is no such log" from the
		// caller's point of view; the daemon log records which it was.
		s.log.Debug("api: log read failed", "name", name, "port", portParam, "err", err)
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such log"})
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, chunk)
}

// handleLaunch starts a project from the catalogue.
//
// The request names a path, never a command: the launcher looks the path up in
// the catalogue and runs only the command Marina itself derived. An unknown path
// is rejected, so this endpoint cannot be used to run arbitrary things.
func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}

	launch, err := s.launcher.Start(r.Context(), body.Path)
	if err != nil {
		s.log.Warn("api: launch refused", "path", body.Path, "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.log.Info("api: launched", "name", launch.Name, "cmd", launch.Command, "pid", launch.PID)

	// Sweep immediately so the UI reflects the pending launch without waiting.
	s.mon.Refresh(r.Context())
	writeJSON(w, http.StatusOK, launch)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	hist, err := s.store.History(r.Context(), key)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, hist)
}

// handleFavicon proxies a local app's icon so the dashboard can show it without
// the browser making a request that a dev server might answer with a redirect.
// Only loopback ports of services Marina currently tracks are fetched.
func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "bad port", http.StatusBadRequest)
		return
	}

	// Restrict to ports we actually observed, and reuse the probe's icon hint
	// and the loopback address that answered it.
	path, scheme, host, ok := "/favicon.ico", "http", "127.0.0.1", false
	for _, svc := range s.mon.Snapshot().Services {
		if svc.Port == port && svc.Probe.HTTP {
			ok, scheme = true, svc.Probe.Scheme
			if svc.Probe.Host != "" {
				host = svc.Probe.Host
			}
			if svc.Probe.Favicon != "" {
				path = svc.Probe.Favicon
			}
			break
		}
	}
	if !ok {
		http.Error(w, "no http service on that port", http.StatusNotFound)
		return
	}

	url := scheme + "://" + host + ":" + strconv.Itoa(port) + path
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, "bad upstream", http.StatusBadGateway)
		return
	}
	resp, err := s.faviconClient.Do(req)
	if err != nil {
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode != http.StatusOK || strings.Contains(ct, "html") {
		// Dev servers commonly answer a missing icon with their index page.
		http.Error(w, "no icon", http.StatusNotFound)
		return
	}
	if ct == "" {
		ct = "image/x-icon"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 256*1024))
}

// spaHandler serves the embedded build, falling back to index.html so client
// routing works on a deep link.
func (s *Server) spaHandler() http.Handler {
	files := http.FileServer(http.FS(s.ui))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			clean = "index.html"
		}
		if _, err := fs.Stat(s.ui, clean); err != nil {
			index, err := fs.ReadFile(s.ui, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(index)
			return
		}
		// Hashed asset filenames are safe to cache hard; index.html is not.
		if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		files.ServeHTTP(w, r)
	})
}

// guard rejects mutations that are not this machine's own.
//
// Two separate checks, because they stop different things:
//
// The Origin check stops a cross-site request — a page you happen to be visiting
// asking this daemon to start something. A same-origin fetch from the dashboard
// sends either no Origin or our own; anything else is not ours.
//
// The client-address check stops the network. Marina can start and stop
// processes, and once it listens on a LAN address every device on the Wi-Fi can
// reach these routes — a guest, a TV, anything with a foothold. Reading state
// from another device is the point of listening at all; changing it is not, and
// there is no authentication here that could tell one device from another. So
// mutations are refused unless the request came from this machine.
//
// This lives in guard rather than beside each route so that a new mutating route
// cannot forget it: every POST is registered through here.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !s.allowedOrigin(origin) {
			s.log.Warn("api: rejected cross-origin mutation", "origin", origin, "path", r.URL.Path)
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		if !isLoopbackClient(r.RemoteAddr) {
			s.log.Warn("api: refused a mutation from the network",
				"client", r.RemoteAddr, "path", r.URL.Path)
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": "Marina only accepts changes from the machine it runs on. " +
					"Starting and stopping apps has to be done there.",
			})
			return
		}
		next(w, r)
	}
}

// isLoopbackClient reports whether a request came from this machine.
func isLoopbackClient(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// No port at all is not a shape we produce; treat it as untrusted rather
		// than guessing, because guessing wrong here grants process control.
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (s *Server) allowedOrigin(origin string) bool {
	u, err := parseOrigin(origin)
	if err != nil {
		return false
	}
	if u.host != "localhost" && u.host != "127.0.0.1" && u.host != "[::1]" && u.host != "::1" {
		return false
	}
	// The dev server (Vite) and the daemon itself are both legitimate.
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		port = ""
	}
	return u.port == port || u.port == "5199" || u.port == ""
}

type origin struct{ host, port string }

func parseOrigin(raw string) (origin, error) {
	rest, ok := strings.CutPrefix(raw, "http://")
	if !ok {
		if rest, ok = strings.CutPrefix(raw, "https://"); !ok {
			return origin{}, errors.New("unsupported scheme")
		}
	}
	if h, p, err := net.SplitHostPort(rest); err == nil {
		return origin{host: h, port: p}, nil
	}
	return origin{host: rest}, nil
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
		return false
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
