package catalog

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ExpectedPort is a port a project is likely to bind, with where that came from.
//
// Provenance matters more than the number. "It ran here last time" is worth more
// than "Vite usually uses 5173", and someone deciding whether a conflict is real
// needs to know which of those they are looking at.
type ExpectedPort struct {
	Port int `json:"port"`
	// Source ranks the evidence: history > config > script > default.
	Source string `json:"source"`
	// Detail names the file or reason, e.g. "vite.config.ts" or "Vite default".
	Detail string `json:"detail,omitempty"`
}

// Source values, strongest first.
const (
	SourceHistory = "history" // Marina has actually seen this project on this port
	SourceConfig  = "config"  // declared in a config or .env file
	SourceScript  = "script"  // named in the start command or a script it runs
	SourceDefault = "default" // the framework's conventional port
)

func sourceRank(s string) int {
	switch s {
	case SourceHistory:
		return 0
	case SourceConfig:
		return 1
	case SourceScript:
		return 2
	default:
		return 3
	}
}

// frameworkDefaults is the one unavoidable table here, and the weakest evidence:
// a convention cannot be discovered from a project that never states it. It is
// only consulted when the project itself said nothing, and is always labelled as a
// default so it is never mistaken for something observed.
var frameworkDefaults = map[string]int{
	"Vite": 5173, "SvelteKit": 5173, "Astro": 4321, "Nuxt": 3000,
	"Next.js": 3000, "CRA": 3000, "Remix": 3000, "Angular": 4200,
	"Vue CLI": 8080, "Storybook": 6006, "NestJS": 3000, "Express": 3000,
	"Fastify": 3000, "Django": 8000, "FastAPI": 8000, "Rails": 3000,
	"Laravel": 8000, "Streamlit": 8501, "Jupyter": 8888, "Expo": 8081,
}

var (
	// --port 3000, --port=3000, -p 3000, PORT=3000, VITE_PORT=3000, …
	portFlagRe = regexp.MustCompile(`(?i)(?:--port[=\s]+|-p[=\s]+|\b[A-Z_]*PORT\s*=\s*)"?(\d{2,5})"?`)
	// port: 3000 in a JS/TS config, including `env.PORT || 5177` fallbacks.
	portConfigRe = regexp.MustCompile(`(?i)\bport\s*[:=]\s*(?:[A-Za-z_.\[\]'"()]*\s*(?:\|\||\?\?)\s*)?(\d{2,5})`)
	// PORT=3000 on its own line in a dotenv file.
	dotenvRe = regexp.MustCompile(`(?im)^\s*(?:export\s+)?([A-Z_]*PORT)\s*=\s*"?(\d{2,5})"?`)
)

// configGlobs discover a project's own config files rather than assuming their
// names. A fixed list would miss anything new and rot as tools are renamed; these
// patterns pick up vite/nuxt/astro/svelte/next/vitest configs and every dotenv
// variant without naming any of them.
var configGlobs = []string{
	".env", ".env.*",
	"*.config.js", "*.config.ts", "*.config.mjs", "*.config.cjs",
	"*.config.mts", "*.config.cts", "*.config.json",
	"Procfile", "Procfile.*",
}

// scriptExtensions are files a start command might shell out to, where the real
// ports often live.
var scriptExtensions = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true, ".mjs": true,
	".js": true, ".ts": true, ".py": true, "": true,
}

// detectPorts works out which ports a project is likely to use, from the project
// itself. Historical ports are added by the caller, which is the only part that
// knows what Marina has actually observed.
func detectPorts(dir string, scripts map[string]string, framework string) []ExpectedPort {
	var found []ExpectedPort
	seen := make(map[int]bool)

	add := func(port int, source, detail string) {
		// Ports below 1024 are privileged and never a dev server here; above 65535
		// is not a port at all.
		if port < 1024 || port > 65535 || seen[port] {
			return
		}
		seen[port] = true
		found = append(found, ExpectedPort{Port: port, Source: source, Detail: detail})
	}

	// 1. The project's own config and env files, discovered by pattern.
	for _, path := range globAll(dir, configGlobs) {
		body, err := os.ReadFile(path)
		if err != nil || len(body) > 512*1024 {
			continue
		}
		name := filepath.Base(path)
		text := string(body)

		// A dotenv file names the variable, which tells us whether the port is this
		// app's or a dependency's: DATABASE_PORT is not what this app binds.
		if strings.HasPrefix(name, ".env") {
			for _, m := range dotenvRe.FindAllStringSubmatch(text, 8) {
				if !isOwnPortVar(m[1]) {
					continue
				}
				if port, err := strconv.Atoi(m[2]); err == nil {
					add(port, SourceConfig, name)
				}
			}
			continue
		}
		for _, m := range portConfigRe.FindAllStringSubmatch(text, 6) {
			if port, err := strconv.Atoi(m[1]); err == nil {
				add(port, SourceConfig, name)
			}
		}
	}

	// 2. Ports named directly in the scripts.
	for _, key := range orderedScriptKeys(scripts) {
		for _, m := range portFlagRe.FindAllStringSubmatch(scripts[key], 4) {
			if port, err := strconv.Atoi(m[1]); err == nil {
				add(port, SourceScript, "package.json → "+key)
			}
		}
	}

	// 3. Scripts that shell out to a local file — `bash scripts/dev.sh` — hide the
	//    real ports one level down. Follow those references and read them.
	for _, key := range orderedScriptKeys(scripts) {
		for _, ref := range localScriptRefs(dir, scripts[key]) {
			body, err := os.ReadFile(ref)
			if err != nil {
				continue
			}
			rel, relErr := filepath.Rel(dir, ref)
			if relErr != nil {
				rel = filepath.Base(ref)
			}
			for _, m := range portFlagRe.FindAllStringSubmatch(string(body), 12) {
				if port, err := strconv.Atoi(m[1]); err == nil {
					add(port, SourceScript, rel)
				}
			}
			for _, m := range portConfigRe.FindAllStringSubmatch(string(body), 12) {
				if port, err := strconv.Atoi(m[1]); err == nil {
					add(port, SourceScript, rel)
				}
			}
		}
	}

	// 4. Only fall back to a convention when the project stated nothing itself.
	if len(found) == 0 && framework != "" {
		if port, ok := frameworkDefaults[framework]; ok {
			add(port, SourceDefault, framework+" default")
		}
	}

	SortPorts(found)
	return found
}

// isOwnPortVar distinguishes "the port this app listens on" from "the port of
// something it connects to". DATABASE_PORT and REDIS_PORT describe dependencies;
// PORT, VITE_PORT, and SERVER_PORT describe this app.
func isOwnPortVar(name string) bool {
	name = strings.ToUpper(name)
	if name == "PORT" {
		return true
	}
	for _, own := range []string{"SERVER_PORT", "APP_PORT", "WEB_PORT", "DEV_PORT",
		"CLIENT_PORT", "FRONTEND_PORT", "BACKEND_PORT", "API_PORT", "HTTP_PORT"} {
		if name == own {
			return true
		}
	}
	// Tool-prefixed forms such as VITE_PORT or NUXT_PORT.
	return strings.HasSuffix(name, "_PORT") && !dependencyPortVar(name)
}

// dependencyPortVar names variables that clearly belong to something else.
func dependencyPortVar(name string) bool {
	for _, dep := range []string{
		"DB", "DATABASE", "POSTGRES", "PG", "MYSQL", "MARIADB", "MONGO", "REDIS",
		"MEMCACHED", "ELASTIC", "OPENSEARCH", "KAFKA", "ZOOKEEPER", "RABBIT",
		"AMQP", "SMTP", "MAIL", "S3", "MINIO", "CLICKHOUSE", "INFLUX", "NATS",
	} {
		if strings.HasPrefix(name, dep+"_") || strings.Contains(name, "_"+dep+"_") {
			return true
		}
	}
	return false
}

// SortPorts orders by evidence strength, and leaves equally-strong evidence in the
// order it arrived.
//
// The tie-break used to be the port number, and that quietly destroyed the ranking
// that matters most. History arrives most-recently-seen first — deliberately, since
// the port a project used an hour ago beats one it used last month — and comparing
// port numbers reordered it to lowest-first. A project moved from 3001 to 8930 went
// on being predicted at 3001, a port it had not touched in a day, because 3001 is
// the smaller number.
//
// SliceStable keeps the incoming order for equal ranks, which is what recency needs
// and is deterministic for the other sources too, since those are read in file order.
func SortPorts(ports []ExpectedPort) {
	sort.SliceStable(ports, func(i, j int) bool {
		return sourceRank(ports[i].Source) < sourceRank(ports[j].Source)
	})
}

func globAll(dir string, patterns []string) []string {
	var paths []string
	seen := make(map[string]bool)
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			continue
		}
		for _, m := range matches {
			if seen[m] {
				continue
			}
			if info, err := os.Stat(m); err != nil || !info.Mode().IsRegular() {
				continue
			}
			seen[m] = true
			paths = append(paths, m)
		}
	}
	sort.Strings(paths)
	return paths
}

// orderedScriptKeys reads the scripts most likely to hold the dev port first, then
// everything else — a project may name its port in dev:frontend rather than dev.
func orderedScriptKeys(scripts map[string]string) []string {
	preferred := []string{"dev", "start", "serve", "develop"}
	var keys []string
	seen := make(map[string]bool)
	for _, k := range preferred {
		if _, ok := scripts[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	rest := make([]string, 0, len(scripts))
	for k := range scripts {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

// localScriptRefs finds paths inside a command that point at a real file in the
// project, so the command's own scripts can be read for ports.
func localScriptRefs(dir, command string) []string {
	var refs []string
	seen := make(map[string]bool)

	for _, field := range strings.Fields(command) {
		field = strings.Trim(field, `"'`)
		if field == "" || strings.HasPrefix(field, "-") || strings.Contains(field, "node_modules") {
			continue
		}
		if !scriptExtensions[filepath.Ext(field)] {
			continue
		}
		candidate := filepath.Join(dir, field)
		if seen[candidate] {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 256*1024 {
			continue
		}
		seen[candidate] = true
		refs = append(refs, candidate)
	}
	return refs
}
