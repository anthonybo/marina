package catalog

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ShellEnv reproduces the environment a developer's own terminal would give a
// process.
//
// This exists because launchd starts the daemon with a minimal PATH
// (/usr/local/bin:/usr/bin:/bin:…), while the tools these projects need live
// somewhere else entirely — here, pnpm and node come from nvm under
// ~/.nvm/versions/node/<version>/bin, which is put on PATH by ~/.zshrc. Running
// `pnpm run dev` with launchd's PATH fails with "pnpm: command not found", which
// is exactly what happened.
//
// So: ask the user's own login shell what its environment is, once, and reuse it.
// An interactive shell (-i) is required, not just a login shell: nvm's setup lives
// in .zshrc, which a non-interactive shell never reads.
type ShellEnv struct {
	log *slog.Logger

	mu      sync.Mutex
	env     []string
	loadedA time.Time
	ttl     time.Duration
}

// sentinel separates any chatter a shell's rc files print from the environment
// dump that follows it. Without this, a startup banner would be parsed as vars.
const sentinel = "__MARINA_ENV_BEGIN__"

// NewShellEnv returns a ShellEnv that re-reads the shell at most once per ttl.
func NewShellEnv(log *slog.Logger, ttl time.Duration) *ShellEnv {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &ShellEnv{log: log, ttl: ttl}
}

// Environ returns the environment to launch a project with: the daemon's own
// environment, overlaid with whatever the user's shell provides. On failure it
// falls back to the daemon's environment plus a best-effort PATH, so a launch is
// still attempted rather than silently blocked.
func (s *ShellEnv) Environ(ctx context.Context) []string {
	s.mu.Lock()
	if s.env != nil && time.Since(s.loadedA) < s.ttl {
		env := s.env
		s.mu.Unlock()
		return env
	}
	s.mu.Unlock()

	env := s.capture(ctx)
	if env == nil {
		env = fallbackEnv()
	}

	s.mu.Lock()
	s.env, s.loadedA = env, time.Now()
	s.mu.Unlock()
	return env
}

// LoginShell reports the user's shell, preferring what the system says over the
// daemon's own $SHELL, which launchd does not set meaningfully.
func LoginShell() string {
	if shell := os.Getenv("SHELL"); shell != "" && shell != "/bin/sh" {
		return shell
	}
	// dscl is the authoritative source on macOS.
	if user := os.Getenv("USER"); user != "" {
		out, err := exec.Command("/usr/bin/dscl", ".", "-read", "/Users/"+user, "UserShell").Output()
		if err == nil {
			if _, value, ok := strings.Cut(string(out), ":"); ok {
				if shell := strings.TrimSpace(value); shell != "" {
					return shell
				}
			}
		}
	}
	return "/bin/zsh"
}

func (s *ShellEnv) capture(ctx context.Context) []string {
	shell := LoginShell()

	// A bounded wait: an rc file that blocks must not hang a launch forever.
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// -i so .zshrc (where nvm lives) is read; -l so profile files are too.
	cmd := exec.CommandContext(runCtx, shell, "-ilc", "printf '"+sentinel+"'; env -0")
	cmd.Stdin = nil // no tty; interactive shells cope with this
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		s.log.Warn("catalog: could not read the shell environment; using a fallback PATH",
			"shell", shell, "err", err)
		return nil
	}

	text := string(out)
	i := strings.Index(text, sentinel)
	if i < 0 {
		s.log.Warn("catalog: shell environment output was not recognisable", "shell", shell)
		return nil
	}
	text = text[i+len(sentinel):]

	captured := make(map[string]string)
	for _, entry := range strings.Split(text, "\x00") {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || strings.ContainsAny(key, " \t\n") {
			continue
		}
		captured[key] = value
	}
	if captured["PATH"] == "" {
		s.log.Warn("catalog: shell environment had no PATH", "shell", shell)
		return nil
	}

	s.log.Info("catalog: loaded the shell environment for launching",
		"shell", shell, "vars", len(captured))
	return merge(os.Environ(), captured)
}

// merge overlays captured values on a base environment. Keys the shell did not
// mention are kept, so the daemon's own settings survive.
func merge(base []string, captured map[string]string) []string {
	out := make([]string, 0, len(base)+len(captured))
	seen := make(map[string]bool, len(captured))

	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if value, override := captured[key]; override {
			out = append(out, key+"="+value)
			seen[key] = true
			continue
		}
		out = append(out, entry)
	}
	for key, value := range captured {
		// PWD and OLDPWD describe the shell we interrogated, not the project.
		if seen[key] || key == "PWD" || key == "OLDPWD" || key == "SHLVL" || key == "_" {
			continue
		}
		out = append(out, key+"="+value)
	}
	return out
}

// fallbackEnv augments the daemon's PATH with the places Node toolchains usually
// live, so a launch has a chance even when the shell could not be read.
func fallbackEnv() []string {
	home, _ := os.UserHomeDir()
	extra := []string{
		home + "/.local/bin",
		home + "/Library/pnpm",
		home + "/.bun/bin",
		home + "/.cargo/bin",
		"/opt/homebrew/bin",
		"/usr/local/bin",
	}
	// nvm installs one directory per version; include whatever is present.
	if entries, err := os.ReadDir(home + "/.nvm/versions/node"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				extra = append(extra, home+"/.nvm/versions/node/"+e.Name()+"/bin")
			}
		}
	}

	path := os.Getenv("PATH")
	for _, dir := range extra {
		if _, err := os.Stat(dir); err == nil && !strings.Contains(path, dir) {
			path = dir + ":" + path
		}
	}
	return merge(os.Environ(), map[string]string{"PATH": path})
}
