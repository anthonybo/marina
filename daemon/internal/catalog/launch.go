package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anthonybo/marina/daemon/internal/procs"
)

// Launcher starts catalogued projects.
//
// Deliberately narrow. It will only run the command the catalogue derived for a
// path the catalogue already knows, so a request can never supply its own
// command or point at an arbitrary directory. Processes are detached into their
// own session, exactly as if you had run them in a terminal. Output goes to a log
// file per launch, since a detached process with nowhere to write is a process you
// cannot debug.
type Launcher struct {
	catalog *Catalog
	logDir  string
	env     *ShellEnv
	log     *slog.Logger

	mu     sync.Mutex
	recent map[string]*Launch // keyed by project path
}

// Launch records one start attempt, including how it ended.
type Launch struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Command string    `json:"command"`
	PID     int       `json:"pid"`
	LogPath string    `json:"logPath"`
	At      time.Time `json:"at"`
	// Error explains a launch that did not survive.
	//
	// This field is the whole reason this type tracks outcomes at all: the first
	// version reported "starting" forever and never mentioned that the command had
	// already died with "pnpm: command not found". A launcher that cannot report a
	// failure is worse than no launcher.
	Error string `json:"error,omitempty"`
	// ExitCode is set once the process has exited.
	ExitCode *int `json:"exitCode,omitempty"`
	// Ended is when it exited, if it has.
	Ended time.Time `json:"ended,omitzero"`
	// PGID is the process group Start created. Recorded because stopping the group
	// is the only way to take down a tree, and the group survives even when the
	// leader has been reaped.
	PGID int `json:"pgid,omitempty"`
	// Adopted marks a record reclaimed from a previous daemon run.
	Adopted bool `json:"adopted,omitempty"`
	// Stopped records that the exit was asked for. Without this, stopping an app
	// would be reported as a crash — SIGTERM produces a non-zero status, and a
	// deliberate shutdown is not a failure.
	Stopped bool `json:"stopped,omitempty"`
}

// Failed reports whether this attempt is known to have gone wrong.
func (l Launch) Failed() bool { return l.Error != "" }

// Starting reports whether the attempt is still plausibly coming up. An adopted
// record describes something already running, so it is never "starting".
func (l Launch) Starting() bool {
	return !l.Failed() && !l.Adopted && l.Ended.IsZero() && time.Since(l.At) < pendingWindow
}

// Live reports whether the launched process tree still exists.
func (l Launch) Live() bool { return procs.GroupAlive(l.PGID) || procs.Alive(l.PID) }

// pendingWindow is how long a launch is reported as "starting" before we stop
// claiming anything about it.
const pendingWindow = 60 * time.Second

// failFast is how long an exit is treated as "it never got going". A dev server
// runs indefinitely; one that stops within this window did not start.
const failFast = 20 * time.Second

// NewLauncher returns a Launcher writing logs under logDir.
func NewLauncher(c *Catalog, logDir string, env *ShellEnv, log *slog.Logger) *Launcher {
	return &Launcher{
		catalog: c,
		logDir:  logDir,
		env:     env,
		log:     log,
		recent:  make(map[string]*Launch),
	}
}

// Start launches the project at path. It returns an error if the path is not in
// the catalogue, or if a start for it is already in flight.
func (l *Launcher) Start(ctx context.Context, path string) (Launch, error) {
	project, ok := l.catalog.Lookup(path)
	if !ok {
		return Launch{}, fmt.Errorf("catalog: %q is not a known project", path)
	}
	if project.Command == "" {
		return Launch{}, errors.New("catalog: no start command for this project")
	}

	l.mu.Lock()
	if existing, ok := l.recent[project.Path]; ok && existing.Starting() {
		snapshot := *existing
		l.mu.Unlock()
		return snapshot, errors.New("catalog: already starting")
	}
	l.mu.Unlock()

	if err := os.MkdirAll(l.logDir, 0o755); err != nil {
		return Launch{}, fmt.Errorf("catalog: log directory: %w", err)
	}
	logPath := filepath.Join(l.logDir, project.Name+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return Launch{}, fmt.Errorf("catalog: log file: %w", err)
	}
	defer logFile.Close()

	// The user's own shell, with the environment their terminal would provide.
	// launchd's PATH does not include nvm, so `pnpm` is simply absent without this.
	shell := LoginShell()
	env := append(l.env.Environ(ctx), "MARINA_LAUNCHED=1")

	fmt.Fprintf(logFile, "=== marina: %s\n=== dir:   %s\n=== cmd:   %s\n=== shell: %s\n=== path:  %s\n\n",
		time.Now().Format(time.RFC3339), project.Path, project.Command, shell, pathOf(env))

	cmd := exec.Command(shell, "-lc", project.Command)
	cmd.Dir = project.Path
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = env
	// Setsid puts it in its own session so ordinary signals to Marina don't reach
	// it. Surviving `launchctl bootout` additionally needs AbandonProcessGroup in
	// the daemon's plist — see scripts/install.sh.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return Launch{}, fmt.Errorf("catalog: start %s: %w", project.Name, err)
	}

	// Record the process group now, while the leader is certainly alive. Reading it
	// later can fail once the shell has been reaped, and the group is what a stop
	// needs to signal.
	pgid, pgErr := syscall.Getpgid(cmd.Process.Pid)
	if pgErr != nil {
		pgid = cmd.Process.Pid // setsid makes the leader its own group
	}

	launch := Launch{
		Path:    project.Path,
		Name:    project.Name,
		Command: project.Command,
		PID:     cmd.Process.Pid,
		PGID:    pgid,
		LogPath: logPath,
		At:      time.Now(),
	}

	l.mu.Lock()
	l.recent[project.Path] = &launch
	l.mu.Unlock()
	// Persist so a daemon restart can still stop this tree.
	l.save()

	// Watch the shell. Reaping it avoids a zombie, but the reason this matters is
	// reporting: a command that cannot start exits immediately, and that has to
	// reach the UI instead of leaving a spinner running forever.
	go l.watch(project.Path, project.Name, cmd, logPath)

	return launch, nil
}

func (l *Launcher) watch(path, name string, cmd *exec.Cmd, logPath string) {
	waitErr := cmd.Wait()

	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	launch, ok := l.recent[path]
	if !ok {
		return
	}
	launch.Ended = time.Now()
	launch.ExitCode = &code
	ranFor := launch.Ended.Sub(launch.At)

	switch {
	case launch.Stopped:
		// We asked it to stop. Not a failure, however it exited.
	case code == 0 && ranFor >= failFast:
		// Ran for a while, then stopped cleanly. Not a launch failure.
	case code == 0:
		launch.Error = "the command exited straight away without starting anything"
	default:
		launch.Error = describeExit(code, logPath)
	}

	if launch.Failed() {
		l.log.Warn("catalog: launch failed",
			"name", name, "exit", code, "ranFor", ranFor.Round(time.Millisecond),
			"reason", launch.Error, "log", logPath, "waitErr", waitErr)
	}
}

// Stop terminates a project Marina launched, and everything it started.
//
// Signalling the process group is correct here and only here: Start put the
// command in its own session, so the group contains the shell, the package
// manager, and every worker they spawned — and nothing else. This is the
// programmatic equivalent of Ctrl+C in the terminal that owns it.
func (l *Launcher) Stop(ctx context.Context, path string) (procs.Result, error) {
	project, ok := l.catalog.Lookup(path)
	if !ok {
		return procs.Result{}, fmt.Errorf("catalog: %q is not a known project", path)
	}

	l.mu.Lock()
	launch, tracked := l.recent[project.Path]
	var pid, pgid int
	if tracked {
		pid, pgid = launch.PID, launch.PGID
		// Mark it first: the watcher may observe the exit before Terminate returns,
		// and it must not read the result as a crash.
		launch.Stopped = true
	}
	l.mu.Unlock()

	if !tracked || (pid == 0 && pgid == 0) {
		return procs.Result{}, fmt.Errorf(
			"catalog: Marina did not start %s, so it cannot stop it as a group", project.Name)
	}

	result := procs.TerminateGroup(ctx, pgid, pid)
	l.log.Info("catalog: stopped", "name", project.Name, "pid", pid,
		"group", result.Group, "forced", result.Forced, "exited", result.Exited)

	l.mu.Lock()
	delete(l.recent, project.Path)
	l.mu.Unlock()
	l.save()
	return result, nil
}

// Launched reports whether Marina started the project at path and its tree is
// still alive, which is what makes a whole-group stop safe.
func (l *Launcher) Launched(path string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	launch, ok := l.recent[filepath.Clean(path)]
	return ok && !launch.Failed() && launch.Live()
}

// Recent returns the launches worth reporting: those still starting, and those
// that failed. A failure is kept until that project is launched again, so it
// cannot quietly disappear before it has been read.
func (l *Launcher) Recent() []Launch {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out []Launch
	for path, launch := range l.recent {
		switch {
		case launch.Failed(), launch.Starting():
			out = append(out, *launch)
		case launch.Live():
			// Running: not something to report, but keep the record so it can
			// still be stopped as a group.
		default:
			delete(l.recent, path)
		}
	}
	return out
}

// Settled clears bookkeeping for projects now confirmed running. A recorded
// failure is kept, because a port appearing for some other reason should not
// erase the fact that a launch went wrong.
func (l *Launcher) Settled(runningPaths map[string]bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for path, launch := range l.recent {
		// Keep records for live trees — they are how a group stop finds its target.
		if runningPaths[path] && !launch.Failed() && !launch.Live() {
			delete(l.recent, path)
		}
	}
}

// describeExit turns an exit status into something actionable, reading the tail of
// the log for a recognisable cause. Naming the cause is the difference between
// "it failed" and knowing what to do about it.
func describeExit(code int, logPath string) string {
	generic := fmt.Sprintf("the command exited with status %d", code)

	body, err := os.ReadFile(logPath)
	if err != nil {
		return generic
	}
	text := string(body)

	switch {
	case strings.Contains(text, "command not found"):
		// By far the most common failure, and the one worth quoting exactly.
		line := lastLineContaining(text, "command not found")
		return strings.TrimSpace(line) + " — that tool is not on the PATH Marina used"
	case strings.Contains(text, "EADDRINUSE"):
		return "something is already listening on the port it wanted"
	case strings.Contains(text, "Missing script"):
		return "the package manager has no such script"
	case strings.Contains(text, "ERR_PNPM_NO_IMPORTER_MANIFEST_FOUND"):
		return "pnpm found no package manifest in that directory"
	case strings.Contains(text, "No such file or directory"):
		return strings.TrimSpace(lastLineContaining(text, "No such file or directory"))
	default:
		return generic
	}
}

func lastLineContaining(text, needle string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], needle) {
			return lines[i]
		}
	}
	return needle
}

// pathOf extracts PATH from an environment slice, for the log header. Recording
// it makes a "command not found" failure self-explanatory.
func pathOf(env []string) string {
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			return value
		}
	}
	return "(unset)"
}
