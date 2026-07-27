// Package catalog finds projects on disk that could be running but aren't.
//
// The live views answer "what is up right now". This answers the other half of
// the question — "what could I bring up" — by reading each project directory for
// the command that starts it. Nothing here ever executes anything; it only
// reports what it found, and the launcher validates against this list so a start
// request can never name an arbitrary path.
package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/anthonybo/marina/daemon/internal/identify"
)

// Project is a directory Marina knows how to start.
type Project struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Manager is the tool that runs it: pnpm, npm, yarn, bun, cargo, python, make.
	Manager string `json:"manager"`
	// Script is the named script, where the manager has them.
	Script string `json:"script,omitempty"`
	// Command is exactly what would run, shown to the user before it does.
	Command   string `json:"command"`
	Framework string `json:"framework,omitempty"`
	Language  string `json:"language,omitempty"`
	HasGit    bool   `json:"hasGit"`
	// Ports are the ports this project is likely to bind, strongest evidence first.
	// Populated from the project's own files; history is added by the caller.
	Ports []ExpectedPort `json:"ports,omitempty"`
	// Real is Path with symlinks resolved. Matching a project against a running
	// process needs this: macOS reports a cwd under /private/tmp for a directory
	// reached via /tmp, and a symlinked projects directory would never match.
	// Not serialised — callers display Path, which is what the user typed.
	Real string `json:"-"`
}

// Catalog scans a set of roots, caching results because the filesystem changes
// far more slowly than the port table.
type Catalog struct {
	roots []string
	ttl   time.Duration

	mu       sync.Mutex
	projects []Project
	skipped  int
	scanned  time.Time
}

// New returns a Catalog over the given roots. Missing roots are ignored.
func New(roots []string, ttl time.Duration) *Catalog {
	cleaned := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if strings.HasPrefix(root, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				root = filepath.Join(home, strings.TrimPrefix(root, "~"))
			}
		}
		cleaned = append(cleaned, filepath.Clean(root))
	}
	return &Catalog{roots: cleaned, ttl: ttl}
}

// Roots reports the directories being scanned. The identifier uses these as
// boundaries: a directory that holds projects is never itself a project.
func (c *Catalog) Roots() []string { return c.roots }

// Projects returns the catalogue, rescanning at most once per TTL. The second
// return value is how many directories were seen but had no startable command,
// so the UI can say so rather than implying the roots hold nothing else.
func (c *Catalog) Projects(ctx context.Context) ([]Project, int) {
	c.mu.Lock()
	fresh := time.Since(c.scanned) < c.ttl && c.scanned != time.Time{}
	if fresh {
		projects, skipped := c.projects, c.skipped
		c.mu.Unlock()
		return projects, skipped
	}
	c.mu.Unlock()

	projects, skipped := c.scan(ctx)

	c.mu.Lock()
	c.projects, c.skipped, c.scanned = projects, skipped, time.Now()
	c.mu.Unlock()
	return projects, skipped
}

// Lookup finds a project by its path. The launcher uses this so it can only ever
// start something the catalogue actually discovered.
//
// It scans first if it never has: a start request arriving before the first sweep
// would otherwise be refused as "not a known project", which is both wrong and
// baffling — it happens exactly when you click start right after login.
func (c *Catalog) Lookup(path string) (Project, bool) {
	c.mu.Lock()
	scanned := !c.scanned.IsZero()
	c.mu.Unlock()
	if !scanned {
		c.Projects(context.Background())
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	target, real := filepath.Clean(path), resolve(path)
	for _, p := range c.projects {
		if p.Path == target || p.Real == real {
			return p, true
		}
	}
	return Project{}, false
}

// skipDirs are never projects, and walking into them would be slow and pointless.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	"target": true, "__pycache__": true, ".venv": true, "venv": true,
	"Library": true, "Applications": true,
}

func (c *Catalog) scan(ctx context.Context) ([]Project, int) {
	var projects []Project
	skipped := 0

	for _, root := range c.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return projects, skipped
			}
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || skipDirs[entry.Name()] {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			if project, ok := inspect(dir); ok {
				projects = append(projects, project)
			} else if looksLikeProject(dir) {
				// A real project we can see but can't start: counted, not listed.
				skipped++
			}
		}
	}
	return projects, skipped
}

// looksLikeProject reports whether a directory is a project at all, regardless of
// whether we found a way to start it.
func looksLikeProject(dir string) bool {
	for _, marker := range []string{
		".git", "package.json", "go.mod", "Cargo.toml", "pyproject.toml",
		"requirements.txt", "Gemfile", "Makefile",
	} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// inspect works out how a project starts. Returns false when nothing is found —
// guessing a command would be worse than admitting we don't know one.
// Named results so the deferred port detection below actually reaches the caller:
// with value returns, a deferred assignment happens after the result is copied and
// is silently lost.
func inspect(dir string) (project Project, ok bool) {
	project = Project{
		Name:   filepath.Base(dir),
		Path:   dir,
		Real:   resolve(dir),
		HasGit: exists(filepath.Join(dir, ".git")),
	}

	if pkg, found := readPackageJSON(dir); found {
		script := pickScript(pkg.Scripts)
		if script == "" {
			return Project{}, false
		}
		project.Manager = packageManager(dir)
		project.Script = script
		project.Command = project.Manager + " run " + script
		project.Framework, project.Language = identify.FrameworkFromDeps(pkg.deps())
		if project.Language == "" {
			project.Language = "JavaScript"
		}
		project.Ports = detectPorts(dir, pkg.Scripts, project.Framework)
		return project, true
	}

	// Non-Node projects still declare ports in .env and config files. Deferred so
	// the framework set inside the switch below is available for the fallback.
	defer func() {
		if ok {
			project.Ports = detectPorts(dir, nil, project.Framework)
		}
	}()

	switch {
	case exists(filepath.Join(dir, "Cargo.toml")):
		project.Manager, project.Command = "cargo", "cargo run"
		project.Language = "Rust"
		return project, true

	case exists(filepath.Join(dir, "manage.py")):
		project.Manager, project.Command = "python", "python3 manage.py runserver"
		project.Framework, project.Language = "Django", "Python"
		return project, true

	case exists(filepath.Join(dir, "go.mod")) && exists(filepath.Join(dir, "main.go")):
		project.Manager, project.Command = "go", "go run ."
		project.Language = "Go"
		return project, true

	case hasMakeTarget(dir, "dev"):
		project.Manager, project.Script, project.Command = "make", "dev", "make dev"
		return project, true

	case hasMakeTarget(dir, "start"):
		project.Manager, project.Script, project.Command = "make", "start", "make start"
		return project, true
	}

	// A lone entry point is a reasonable guess for a Python project, but only
	// when the project declares itself Python somehow.
	if exists(filepath.Join(dir, "requirements.txt")) || exists(filepath.Join(dir, "pyproject.toml")) {
		for _, entry := range []string{"main.py", "app.py", "server.py", "run.py"} {
			if exists(filepath.Join(dir, entry)) {
				project.Manager, project.Command = "python", "python3 "+entry
				project.Language = "Python"
				return project, true
			}
		}
	}

	return Project{}, false
}

type packageJSON struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func (p packageJSON) deps() map[string]bool {
	deps := make(map[string]bool, len(p.Dependencies)+len(p.DevDependencies))
	for d := range p.Dependencies {
		deps[d] = true
	}
	for d := range p.DevDependencies {
		deps[d] = true
	}
	return deps
}

func readPackageJSON(dir string) (packageJSON, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return packageJSON{}, false
	}
	var pkg packageJSON
	if err := json.Unmarshal(b, &pkg); err != nil {
		return packageJSON{}, false
	}
	return pkg, true
}

// pickScript prefers the script a developer would actually reach for. `dev`
// first: it is the one that runs with reload and is what these projects use.
func pickScript(scripts map[string]string) string {
	for _, name := range []string{"dev", "start", "serve", "develop", "dev:all"} {
		if scripts[name] != "" {
			return name
		}
	}
	return ""
}

// packageManager reads the lockfile rather than assuming npm, because running a
// pnpm workspace with npm does not work.
func packageManager(dir string) string {
	switch {
	case exists(filepath.Join(dir, "pnpm-lock.yaml")):
		return "pnpm"
	case exists(filepath.Join(dir, "yarn.lock")):
		return "yarn"
	case exists(filepath.Join(dir, "bun.lockb")), exists(filepath.Join(dir, "bun.lock")):
		return "bun"
	default:
		return "npm"
	}
}

var makeTargetRe = regexp.MustCompile(`(?m)^([A-Za-z0-9_.-]+)\s*:`)

func hasMakeTarget(dir, target string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "Makefile"))
	if err != nil {
		return false
	}
	for _, m := range makeTargetRe.FindAllSubmatch(b, -1) {
		if string(m[1]) == target {
			return true
		}
	}
	return false
}

// resolve follows symlinks, falling back to the input when it cannot.
func resolve(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
