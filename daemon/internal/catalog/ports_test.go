package catalog

import (
	"path/filepath"
	"testing"
)

// ports returns the detected ports as a map for easy assertions.
func portMap(t *testing.T, dir string, scripts map[string]string, framework string) map[int]ExpectedPort {
	t.Helper()
	out := make(map[int]ExpectedPort)
	for _, p := range detectPorts(dir, scripts, framework) {
		out[p.Port] = p
	}
	return out
}

// TestDetectPortsFromConfigFiles uses the patterns found in real projects on this
// machine: a vite config with an env fallback, and a dotenv file.
func TestDetectPortsFromConfigFiles(t *testing.T) {
	dir := t.TempDir()
	// sample-app's actual shape: `port: process.env.PORT || 5177`.
	write(t, dir, "vite.config.ts", "export default {\n  server: { port: process.env.PORT || 5177 },\n}\n")
	// solo-app's actual shape.
	write(t, dir, ".env", "PORT=3001\nDATABASE_PORT=5432\n")

	got := portMap(t, dir, nil, "Vite")

	if p, ok := got[5177]; !ok || p.Source != SourceConfig {
		t.Errorf("5177 = %+v, want it found from config", p)
	}
	if p, ok := got[3001]; !ok || p.Source != SourceConfig {
		t.Errorf("3001 = %+v, want it found from config", p)
	}
	// A dependency's port is not this app's port.
	if _, ok := got[5432]; ok {
		t.Error("DATABASE_PORT=5432 was taken as the app's own port")
	}
	// The framework default must not be added when the project stated a port.
	if _, ok := got[5173]; ok {
		t.Error("added the Vite default even though the project declared its port")
	}
}

// TestDetectPortsFollowsLocalScripts covers webapp's and app-two's shape,
// where `dev` is `bash scripts/dev.sh` and the ports live in that script.
func TestDetectPortsFollowsLocalScripts(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "scripts/dev.sh", "#!/usr/bin/env bash\nPORT=4310 pnpm --filter backend dev &\nvite --port 4311\n")
	scripts := map[string]string{"dev": "bash scripts/dev.sh"}

	got := portMap(t, dir, scripts, "Vite")

	for _, want := range []int{4310, 4311} {
		p, ok := got[want]
		if !ok {
			t.Errorf("%d not found by following the script", want)
			continue
		}
		if p.Source != SourceScript {
			t.Errorf("%d source = %q, want %q", want, p.Source, SourceScript)
		}
		if p.Detail != filepath.Join("scripts", "dev.sh") {
			t.Errorf("%d detail = %q, want the script path", want, p.Detail)
		}
	}
}

func TestDetectPortsFromScriptFlags(t *testing.T) {
	dir := t.TempDir()
	scripts := map[string]string{
		"dev":         "vite --port 4200",
		"dev:backend": "PORT=4201 tsx watch src/index.ts",
		"start":       "next start -p 4202",
		"unrelated":   "eslint .",
	}
	got := portMap(t, dir, scripts, "Vite")
	for _, want := range []int{4200, 4201, 4202} {
		if p, ok := got[want]; !ok || p.Source != SourceScript {
			t.Errorf("%d = %+v, want it found from a script", want, p)
		}
	}
}

// TestFrameworkDefaultIsLastResort: a convention is only used when the project
// says nothing, and is labelled so it can never be mistaken for something observed.
func TestFrameworkDefaultIsLastResort(t *testing.T) {
	dir := t.TempDir()
	got := detectPorts(dir, map[string]string{"dev": "vite"}, "Vite")

	if len(got) != 1 {
		t.Fatalf("got %d ports, want just the default: %+v", len(got), got)
	}
	if got[0].Port != 5173 || got[0].Source != SourceDefault {
		t.Errorf("got %+v, want 5173 from a default", got[0])
	}
	if got[0].Detail == "" {
		t.Error("a default must say it is a default")
	}
}

// TestPrivilegedAndAbsurdPortsIgnored keeps parse noise out.
func TestPrivilegedAndAbsurdPortsIgnored(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "app.config.js", "module.exports = { port: 80, alt: 99999, real: 4500 }\n")
	got := portMap(t, dir, nil, "")
	if _, ok := got[80]; ok {
		t.Error("port 80 is privileged and never a dev server here")
	}
	if _, ok := got[99999]; ok {
		t.Error("99999 is not a port")
	}
}

// TestConfigDiscoveryIsPatternBased proves a config file Marina has never heard of
// is still read, which is the point of globbing rather than listing names.
func TestConfigDiscoveryIsPatternBased(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "somethingbrandnew.config.ts", "export default { server: { port: 4777 } }\n")
	write(t, dir, ".env.staging", "APP_PORT=4778\n")

	got := portMap(t, dir, nil, "")
	for _, want := range []int{4777, 4778} {
		if _, ok := got[want]; !ok {
			t.Errorf("%d not found — discovery should not depend on a known filename", want)
		}
	}
}

func TestIsOwnPortVar(t *testing.T) {
	own := []string{"PORT", "VITE_PORT", "SERVER_PORT", "APP_PORT", "FRONTEND_PORT", "NUXT_PORT"}
	notOwn := []string{"DATABASE_PORT", "DB_PORT", "POSTGRES_PORT", "REDIS_PORT", "MONGO_PORT", "SMTP_PORT"}

	for _, name := range own {
		if !isOwnPortVar(name) {
			t.Errorf("%s should be treated as this app's port", name)
		}
	}
	for _, name := range notOwn {
		if isOwnPortVar(name) {
			t.Errorf("%s is a dependency's port, not this app's", name)
		}
	}
}

func TestSortPortsRanksEvidence(t *testing.T) {
	ports := []ExpectedPort{
		{Port: 5173, Source: SourceDefault},
		{Port: 4000, Source: SourceScript},
		{Port: 3000, Source: SourceHistory},
		{Port: 3500, Source: SourceConfig},
	}
	SortPorts(ports)
	want := []string{SourceHistory, SourceConfig, SourceScript, SourceDefault}
	for i, w := range want {
		if ports[i].Source != w {
			t.Errorf("position %d = %q, want %q", i, ports[i].Source, w)
		}
	}
}

// A start script that checks its dependencies are up names their ports, not its
// own. Observed on a real project: the script opened with a pg_isready probe, 5432
// was recorded as the project's own port, and the dashboard then warned that it
// could not start because PostgreSQL was occupying PostgreSQL's port.
func TestDependencyProbesAreNotTheProjectsPort(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "scripts/start.sh", `#!/usr/bin/env bash
if ! pg_isready -h localhost -p 5432 -q; then
  echo "Postgres is not accepting connections on :5432"
  exit 1
fi
redis-cli -p 6379 ping >/dev/null
node server.js --port 4310
`)
	got := portMap(t, dir, map[string]string{"dev": "bash scripts/start.sh"}, "")

	for _, dep := range []int{5432, 6379} {
		if p, ok := got[dep]; ok {
			t.Errorf("%d was taken as the project's own port (from %q); it belongs to a dependency", dep, p.Detail)
		}
	}
	if _, ok := got[4310]; !ok {
		t.Errorf("the project's real port was missed; found %v", got)
	}
}
