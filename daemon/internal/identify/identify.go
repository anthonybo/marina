// Package identify turns a listening socket plus its owning process into a
// human-meaningful service: which project it belongs to, which framework is
// serving it, and whether it's something you'd actually want to click.
package identify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/anthonybo/marina/daemon/internal/scan"
)

// Kind buckets a service so the dashboard can separate "my apps" from the
// database and the macOS daemons that happen to hold a port.
type Kind string

const (
	// KindApp is a project you are developing: it resolves to a repo or package.
	KindApp Kind = "app"
	// KindInfra is a backing service such as Postgres, Redis, or Mongo.
	KindInfra Kind = "infra"
	// KindSystem is an OS or third-party background process.
	KindSystem Kind = "system"
	// KindUnknown is a listener we could not attribute to any of the above.
	KindUnknown Kind = "unknown"
)

// Service is a fully identified listener, ready to render.
type Service struct {
	Key       string `json:"key"`
	Port      int    `json:"port"`
	PID       int    `json:"pid"`
	Proc      string `json:"proc"`
	Kind      Kind   `json:"kind"`
	Project   string `json:"project,omitempty"`
	Subpath   string `json:"subpath,omitempty"`
	Label     string `json:"label"`
	Dir       string `json:"dir,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Framework string `json:"framework,omitempty"`
	Language  string `json:"language,omitempty"`
	// Entry is the launched script, e.g. "hlsProxyServer.js". It is what tells
	// thirteen workers started from one package apart.
	Entry     string `json:"entry,omitempty"`
	Cmd       string `json:"cmd,omitempty"`
	Wildcard  bool   `json:"wildcard"`
	V6Only    bool   `json:"v6Only,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"` // unix seconds
	// Hosts are the loopback addresses this socket actually answers on.
	Hosts []string `json:"-"`
	// Output says where this process writes its output, which decides whether
	// Marina can show you its terminal.
	Output scan.Output `json:"output"`
}

// infraNames maps process names to the display names people actually use.
var infraNames = map[string]string{
	"postgres":          "PostgreSQL",
	"postmaster":        "PostgreSQL",
	"mysqld":            "MySQL",
	"mariadbd":          "MariaDB",
	"redis-server":      "Redis",
	"mongod":            "MongoDB",
	"memcached":         "Memcached",
	"elasticsearch":     "Elasticsearch",
	"opensearch":        "OpenSearch",
	"clickhouse":        "ClickHouse",
	"influxd":           "InfluxDB",
	"cockroach":         "CockroachDB",
	"etcd":              "etcd",
	"nats-server":       "NATS",
	"rabbitmq":          "RabbitMQ",
	"beam.smp":          "RabbitMQ",
	"minio":             "MinIO",
	"jaeger":            "Jaeger",
	"jaeger-all-in-one": "Jaeger",
	"prometheus":        "Prometheus",
	"grafana":           "Grafana",
	"grafana-server":    "Grafana",
	"localstack":        "LocalStack",
	"kafka":             "Kafka",
	"zookeeper":         "ZooKeeper",
	"mailhog":           "MailHog",
	"mailpit":           "Mailpit",
	"ollama":            "Ollama",
	"qdrant":            "Qdrant",
	"meilisearch":       "Meilisearch",
	"typesense":         "Typesense",
}

// systemProcs are OS-level or vendor background agents. They hold ports but are
// never things you open in a browser.
var systemProcs = map[string]bool{
	"ControlCenter":         true,
	"rapportd":              true,
	"sharingd":              true,
	"identityservicesd":     true,
	"launchd":               true,
	"remoted":               true,
	"AirPlayXPCHelper":      true,
	"OneDrive":              true,
	"OneDrive Sync Service": true,
	"Dropbox":               true,
	"Google Drive":          true,
	"com.docker.backend":    true,
	"Docker Desktop":        true,
	"Spotify":               true,
	"steam_osx":             true,
	"SwiftBar":              true,
	"Google Chrome":         true,
	"Google Chrome Helper":  true,
	"Safari":                true,
	"firefox":               true,
	"nessusd":               true,
	"sshd":                  true,
	"cupsd":                 true,
	"mDNSResponder":         true,
	"nsurlsessiond":         true,
	"WindowServer":          true,
	"CoreSpotlight":         true,
	"trustd":                true,
	"Adobe Desktop Service": true,
	"Creative Cloud":        true,
}

// frameworkRule matches the process command line. Order matters: the first
// match wins, so specific tools come before the runtimes they sit on.
//
// A needle containing a space is matched as a substring of the whole command
// line. A single-word needle must match a whole token — a path segment or an
// argument — never a bare substring. That distinction is what stops
// `--expose-gc` from being detected as Expo, or `bundle` as Bun.
type frameworkRule struct {
	needle    string
	framework string
	language  string
}

var frameworkRules = []frameworkRule{
	// JS/TS meta-frameworks and dev servers.
	{"next dev", "Next.js", "TypeScript"},
	{"next start", "Next.js", "TypeScript"},
	{"/next/dist", "Next.js", "TypeScript"},
	{"/.bin/next", "Next.js", "TypeScript"},
	{"nuxt", "Nuxt", "TypeScript"},
	{"astro", "Astro", "TypeScript"},
	{"remix", "Remix", "TypeScript"},
	{"svelte-kit", "SvelteKit", "TypeScript"},
	{"storybook", "Storybook", "TypeScript"},
	{"expo", "Expo", "TypeScript"},
	{"electron", "Electron", "TypeScript"},
	{"webpack-dev-server", "Webpack", "JavaScript"},
	{"react-scripts", "CRA", "JavaScript"},
	{"ng serve", "Angular", "TypeScript"},
	{"vue-cli-service", "Vue CLI", "JavaScript"},
	{"parcel", "Parcel", "JavaScript"},
	{"esbuild", "esbuild", "JavaScript"},
	{"rollup", "Rollup", "JavaScript"},
	{"vite", "Vite", "TypeScript"},
	{"nest", "NestJS", "TypeScript"},
	{"tsx", "tsx", "TypeScript"},
	{"ts-node", "ts-node", "TypeScript"},
	{"nodemon", "nodemon", "JavaScript"},
	{"fastify", "Fastify", "TypeScript"},
	{"wrangler", "Wrangler", "TypeScript"},
	// Deliberately no rule for a bare "serve": it is a subcommand of far too
	// many things (including Marina itself) to mean anything on its own.

	// Python.
	{"uvicorn", "FastAPI", "Python"},
	{"gunicorn", "Gunicorn", "Python"},
	{"hypercorn", "Hypercorn", "Python"},
	{"manage.py", "Django", "Python"},
	{"flask", "Flask", "Python"},
	{"streamlit", "Streamlit", "Python"},
	{"jupyter", "Jupyter", "Python"},
	{"http.server", "http.server", "Python"},

	// Ruby, PHP, Go, Rust, Java, .NET.
	{"puma", "Rails", "Ruby"},
	{"rails", "Rails", "Ruby"},
	{"artisan serve", "Laravel", "PHP"},
	{"php -S", "PHP", "PHP"},
	{"air", "Go (air)", "Go"},
	{"go run", "Go", "Go"},
	{"cargo run", "Rust", "Rust"},
	{"spring", "Spring", "Java"},
	{"dotnet", ".NET", "C#"},
	{"caddy", "Caddy", ""},
	{"nginx", "nginx", ""},
	{"deno", "Deno", "TypeScript"},
	{"bun", "Bun", "TypeScript"},
}

// Resolver identifies services, caching the filesystem lookups. Directory
// metadata is cached for the daemon's lifetime; per-PID results are cached
// because a PID's identity never changes while it lives.
type Resolver struct {
	mu     sync.Mutex
	dirs   map[string]dirInfo
	byProc map[int]Service // keyed by pid, holds everything except the port

	// boundaries are directories that hold projects rather than being one, so a
	// walk upward must never adopt them as the project. The catalogue's roots are
	// exactly this: Marina already knows ~/projects is a container.
	boundaries map[string]bool
}

type dirInfo struct {
	// repo is the git root, when there is one. It is the definitive answer.
	repo string
	// projectDir is the outermost directory below a boundary that names itself a
	// project. This is what fixes a repo-less monorepo: walking up from
	// quadcitygo/frontend, the outer package.json says "quadcitygo", and that is
	// the project — "frontend" is a role inside it, never a project name.
	projectDir string
	// pkgDir is the *nearest* package.json, used only for dependency-based
	// framework detection, where nearest genuinely is what you want.
	pkgDir  string
	pkgName string
	deps    map[string]bool
}

// New returns a ready Resolver. boundaries are directories that contain projects
// (the catalogue's roots); $HOME and / are always treated as boundaries too.
func New(boundaries []string) *Resolver {
	return &Resolver{
		dirs:       make(map[string]dirInfo),
		byProc:     make(map[int]Service),
		boundaries: boundarySet(boundaries),
	}
}

// boundarySet builds the lookup used by the upward walk. $HOME and / are always
// included: without them a project directly inside the home directory could walk
// up and name itself after the home directory.
func boundarySet(boundaries []string) map[string]bool {
	set := make(map[string]bool, len(boundaries)+2)
	for _, b := range boundaries {
		if b = strings.TrimSpace(b); b != "" {
			set[filepath.Clean(b)] = true
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		set[home] = true
	}
	set["/"] = true
	return set
}

// SetBoundaries replaces the boundary set after the scanned roots change.
//
// Both caches have to go with it. dirs holds each directory's resolved project,
// which is computed by walking up until a boundary stops it — every entry is an
// answer to a question whose rules just changed. byProc holds the project a live
// PID belongs to, derived from those same directories, so keeping it would leave
// running apps labelled under the old rules until they restarted. Dropping both
// costs one round of ps and lsof on the next sweep, which is the correct price
// for adding a directory.
func (r *Resolver) SetBoundaries(boundaries []string) {
	set := boundarySet(boundaries)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.boundaries = set
	r.dirs = make(map[string]dirInfo)
	r.byProc = make(map[int]Service)
}

// Unresolved returns the subset of pids whose identity is not already cached.
// A PID's identity never changes while it lives, so in the steady state this is
// empty and the caller can skip the ps and lsof calls entirely.
func (r *Resolver) Unresolved(pids []int) []int {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []int
	for _, pid := range pids {
		if _, ok := r.byProc[pid]; !ok {
			out = append(out, pid)
		}
	}
	return out
}

// Forget drops cached per-PID identity for PIDs that are no longer running, so
// the cache cannot grow without bound across a long daemon uptime.
func (r *Resolver) Forget(alive map[int]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for pid := range r.byProc {
		if !alive[pid] {
			delete(r.byProc, pid)
		}
	}
}

// Identify builds the Service for one socket. proc may be zero-valued if the
// process disappeared between the socket sweep and the detail lookup.
func (r *Resolver) Identify(sock scan.Socket, proc scan.Proc) Service {
	r.mu.Lock()
	cached, ok := r.byProc[sock.PID]
	r.mu.Unlock()

	if ok {
		// Reuse the expensive parts; only the port-specific fields differ.
		svc := cached
		svc.Port = sock.Port
		svc.Wildcard = sock.Wildcard
		svc.V6Only = sock.V6 && !sock.V4
		svc.Hosts = sock.Hosts()
		svc.Key = key(svc)
		return svc
	}

	svc := Service{
		PID:      sock.PID,
		Proc:     sock.Proc,
		Port:     sock.Port,
		Wildcard: sock.Wildcard,
		V6Only:   sock.V6 && !sock.V4,
		Hosts:    sock.Hosts(),
		Dir:      proc.Cwd,
		Cmd:      trimCmd(proc.Cmd),
		Entry:    detectEntry(proc.Cmd),
	}
	if !proc.Started.IsZero() {
		svc.StartedAt = proc.Started.Unix()
	}

	switch {
	case sock.Proc == selfProc:
		// Marina running under launchd has a cwd of "/" and no repo to find, so
		// it would otherwise show up as an unattributed listener called "/".
		svc.Kind = KindApp
		svc.Project = "Marina"
		svc.Label = "Marina"
		svc.Subpath = "daemon"
	case systemProcs[sock.Proc]:
		svc.Kind = KindSystem
		svc.Label = sock.Proc
	case infraNames[sock.Proc] != "":
		svc.Kind = KindInfra
		svc.Label = infraNames[sock.Proc]
	default:
		r.attributeProject(&svc, proc)
	}

	if svc.Label == "" {
		svc.Label = sock.Proc
	}
	if svc.Kind == "" {
		svc.Kind = KindUnknown
	}
	svc.Key = key(svc)

	// Cache the identity, not the port-specific bits.
	r.mu.Lock()
	r.byProc[sock.PID] = svc
	r.mu.Unlock()

	return svc
}

// attributeProject tries to tie a process to a repo or package on disk. A
// process with a resolvable project becomes KindApp; anything else stays
// unknown so the UI can tuck it away.
func (r *Resolver) attributeProject(svc *Service, proc scan.Proc) {
	info := r.dirMeta(proc.Cwd)
	svc.Framework, svc.Language = detectFramework(proc.Cmd, info.deps)

	// Priority: the git root is definitive; otherwise the outermost self-named
	// project below a boundary; only then fall back to the nearest package.
	// Preferring "nearest" here is what produced project names like "frontend".
	root := info.repo
	if root == "" {
		root = info.projectDir
	}
	if root == "" {
		root = info.pkgDir
	}
	if root == "" {
		// No repo and no package.json: not something we can call a project. A
		// process launched by launchd has a cwd of "/", which is no name at all.
		if svc.Framework != "" && isNameable(proc.Cwd) {
			// Still clearly a dev server, just an unmanaged directory.
			svc.Kind = KindApp
			svc.Project = filepath.Base(proc.Cwd)
			svc.Label = svc.Project
		}
		return
	}

	svc.Kind = KindApp
	svc.Repo = root
	svc.Project = filepath.Base(root)
	if rel, err := filepath.Rel(root, proc.Cwd); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		svc.Subpath = rel
	}
	svc.Label = svc.Project

	// A monorepo package name is sometimes more informative than the folder, but
	// only surface it when it genuinely adds something. A package called
	// "iptv-epg-matcher-frontend" sitting in iptv-epg-matcher/frontend is pure
	// restatement, so it stays hidden.
	if info.pkgName != "" && svc.Subpath != "" {
		short := strings.TrimPrefix(info.pkgName, "@")
		if i := strings.LastIndexByte(short, '/'); i >= 0 {
			short = short[i+1:]
		}
		flat := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(short))
		project := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(svc.Project))
		dir := strings.ToLower(filepath.Base(proc.Cwd))
		redundant := flat == "" ||
			strings.Contains(flat, project) ||
			strings.EqualFold(short, dir)
		if !redundant {
			svc.Subpath = svc.Subpath + " (" + short + ")"
		}
	}
}

// selfProc is Marina's own binary name, recognised so the dashboard describes
// itself properly instead of guessing from launchd's environment.
const selfProc = "marina"

// isNameable reports whether a directory's basename would make a sensible
// project label. Filesystem roots and relative markers would not.
func isNameable(dir string) bool {
	switch dir {
	case "", "/", ".", "..":
		return false
	}
	return true
}

// dirMeta walks up from dir to find the git root, the nearest package.json, and
// that package's dependency set. Results are memoized per directory.
func (r *Resolver) dirMeta(dir string) dirInfo {
	if dir == "" {
		return dirInfo{}
	}
	r.mu.Lock()
	if info, ok := r.dirs[dir]; ok {
		r.mu.Unlock()
		return info
	}
	r.mu.Unlock()

	var info dirInfo

	for cur := dir; ; {
		// Nearest package.json, for framework detection from dependencies.
		if info.pkgDir == "" {
			if b, err := os.ReadFile(filepath.Join(cur, "package.json")); err == nil {
				info.pkgDir = cur
				info.pkgName, info.deps = parsePackageJSON(b)
			}
		}

		// Any directory that names itself a project is a candidate root. Assigning
		// unconditionally means the outermost one wins, which is the point.
		if name, ok := selfNamedProject(cur); ok {
			info.projectDir = cur
			if info.pkgName == "" {
				info.pkgName = name
			}
		}

		// A git root ends the search: nothing above it is this project.
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			info.repo = cur
			break
		}

		parent := filepath.Dir(cur)
		// Stop *before* stepping into a directory that merely holds projects.
		if parent == cur || r.isBoundary(parent) {
			break
		}
		cur = parent
	}

	r.mu.Lock()
	r.dirs[dir] = info
	r.mu.Unlock()
	return info
}

func (r *Resolver) isBoundary(dir string) bool {
	return r.boundaries[filepath.Clean(dir)]
}

// projectMarkers are files that declare "this directory is a project", excluding
// package.json which needs its name checked.
var projectMarkers = []string{
	"go.mod", "Cargo.toml", "pyproject.toml", "Gemfile", "go.work",
	"pnpm-workspace.yaml", "turbo.json", "nx.json", "lerna.json",
}

// selfNamedProject reports whether a directory declares itself a project.
//
// A package.json must have a "name" to count. That single condition is what keeps
// a stray ~/projects/package.json — which has no name — from being mistaken for a
// project root, and it is a fair test: real projects name themselves.
// IsProject reports whether a directory declares itself a project.
//
// Exported so the catalogue can refuse a scanned directory that is a project, or
// sits inside one. Scanned directories double as the boundaries this walk stops
// at, and a boundary inside a project truncates the walk below the real root —
// which renames a running app after whichever subdirectory it stopped at. The two
// must use the same rule, so they share this one.
func IsProject(dir string) bool {
	// .git counts here even though it is absent from projectMarkers: the walk
	// treats a git root as its own terminal condition rather than as a marker, but
	// for "is this directory a project" it is the strongest evidence there is.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	_, ok := selfNamedProject(dir)
	return ok
}

func selfNamedProject(dir string) (string, bool) {
	for _, marker := range projectMarkers {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return "", true
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "", false
	}
	name, _ := parsePackageJSON(b)
	if name == "" {
		return "", false
	}
	return name, true
}

func parsePackageJSON(b []byte) (string, map[string]bool) {
	var pkg struct {
		Name            string            `json:"name"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return "", nil
	}
	deps := make(map[string]bool, len(pkg.Dependencies)+len(pkg.DevDependencies))
	for d := range pkg.Dependencies {
		deps[d] = true
	}
	for d := range pkg.DevDependencies {
		deps[d] = true
	}
	return pkg.Name, deps
}

// FrameworkFromDeps names the framework a project uses from its declared
// dependencies alone. The catalogue needs this for projects that aren't running,
// where there is no command line to read.
func FrameworkFromDeps(deps map[string]bool) (string, string) {
	switch {
	case deps["next"]:
		return "Next.js", "TypeScript"
	case deps["nuxt"]:
		return "Nuxt", "TypeScript"
	case deps["@sveltejs/kit"]:
		return "SvelteKit", "TypeScript"
	case deps["astro"]:
		return "Astro", "TypeScript"
	case deps["@remix-run/react"]:
		return "Remix", "TypeScript"
	case deps["expo"]:
		return "Expo", "TypeScript"
	case deps["electron"]:
		return "Electron", "TypeScript"
	case deps["@nestjs/core"]:
		return "NestJS", "TypeScript"
	case deps["vite"]:
		return "Vite", "TypeScript"
	case deps["react-scripts"]:
		return "CRA", "JavaScript"
	case deps["@angular/core"]:
		return "Angular", "TypeScript"
	case deps["fastify"]:
		return "Fastify", "TypeScript"
	case deps["express"]:
		return "Express", "JavaScript"
	case deps["typescript"]:
		return "", "TypeScript"
	}
	return "", ""
}

// detectFramework prefers the command line, which reflects what is actually
// running, and falls back to declared dependencies.
func detectFramework(cmd string, deps map[string]bool) (string, string) {
	lower := strings.ToLower(cmd)
	tokens := tokenize(lower)

	for _, rule := range frameworkRules {
		matched := false
		if strings.ContainsRune(rule.needle, ' ') {
			matched = strings.Contains(lower, rule.needle)
		} else {
			matched = tokens[rule.needle]
		}
		if !matched {
			continue
		}
		// Vite backs several meta-frameworks; let the dependency set refine it.
		if rule.framework == "Vite" {
			switch {
			case deps["@sveltejs/kit"]:
				return "SvelteKit", "TypeScript"
			case deps["astro"]:
				return "Astro", "TypeScript"
			case deps["nuxt"]:
				return "Nuxt", "TypeScript"
			}
		}
		return rule.framework, rule.language
	}

	switch {
	case deps["next"]:
		return "Next.js", "TypeScript"
	case deps["vite"]:
		return "Vite", "TypeScript"
	case tokens["node"]:
		return "Node", "JavaScript"
	case tokens["python"], tokens["python3"]:
		return "Python", "Python"
	case tokens["ruby"]:
		return "Ruby", "Ruby"
	case tokens["java"]:
		return "Java", "Java"
	}
	return "", ""
}

// scriptExts are stripped when tokenizing so `vite.js` matches the needle
// `vite`, and are also what marks an argument as a plausible entry point.
var scriptExts = map[string]bool{
	".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true,
	".cts": true, ".jsx": true, ".tsx": true, ".py": true, ".rb": true,
	".go": true, ".php": true, ".sh": true,
}

// tokenize reduces a command line to the set of meaningful words in it: each
// argument, each path segment, and each of those with a script extension
// removed. Matching against this set gives whole-word semantics without needing
// a regex per rule.
func tokenize(lower string) map[string]bool {
	tokens := make(map[string]bool, 32)
	add := func(s string) {
		if s == "" {
			return
		}
		tokens[s] = true
		if ext := filepath.Ext(s); scriptExts[ext] {
			tokens[strings.TrimSuffix(s, ext)] = true
		}
	}

	for _, arg := range strings.Fields(lower) {
		arg = strings.TrimLeft(arg, "-")
		// A flag like --max-old-space-size=16384 carries no framework signal in
		// its value, but splitting is harmless and keeps the rule simple.
		for _, part := range strings.FieldsFunc(arg, func(r rune) bool {
			return r == '/' || r == '\\' || r == '@' || r == '=' || r == ':'
		}) {
			add(part)
		}
	}
	return tokens
}

// valueFlags are interpreter flags that consume the following argument, so the
// entry-point scan knows to skip past their values.
var valueFlags = map[string]bool{
	"--require": true, "-r": true, "--import": true, "--loader": true,
	"--experimental-loader": true, "--inspect-port": true, "--conditions": true,
	"-e": true, "--eval": true, "--config": true, "-c": true, "-m": true,
}

// detectEntry pulls the script a process was actually launched with, which is
// the only thing that distinguishes several workers started from one package.
// It returns an empty string for anything living inside a dependency directory,
// since `vite.js` tells the user nothing they don't already see in the badge.
func detectEntry(cmd string) string {
	args := strings.Fields(cmd)
	if len(args) < 2 {
		return ""
	}

	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if valueFlags[strings.ToLower(arg)] {
				i++ // skip this flag's value
			}
			continue
		}
		if strings.HasPrefix(arg, "file://") {
			continue
		}
		lower := strings.ToLower(arg)
		if strings.Contains(lower, "node_modules") ||
			strings.Contains(lower, "site-packages") ||
			strings.Contains(lower, "/.venv/") ||
			strings.Contains(lower, "/.pnpm/") {
			continue
		}
		// Only treat it as an entry point if it looks like a script.
		if !scriptExts[filepath.Ext(lower)] {
			continue
		}
		return filepath.Base(arg)
	}
	return ""
}

// key is a stable identifier that survives process restarts, so pins and
// nicknames stick to a service rather than to a PID.
func key(svc Service) string {
	switch svc.Kind {
	case KindApp:
		base := svc.Repo
		if base == "" {
			base = svc.Dir
		}
		return "app:" + base + "|" + svc.Subpath + "|" + strconv.Itoa(svc.Port)
	case KindInfra:
		return "infra:" + svc.Proc + "|" + strconv.Itoa(svc.Port)
	case KindSystem:
		return "system:" + svc.Proc + "|" + strconv.Itoa(svc.Port)
	default:
		return "other:" + svc.Proc + "|" + strconv.Itoa(svc.Port)
	}
}

// trimCmd keeps command lines readable in the UI without losing the meaningful
// leading arguments.
func trimCmd(cmd string) string {
	const max = 220
	if len(cmd) <= max {
		return cmd
	}
	return cmd[:max] + "…"
}
