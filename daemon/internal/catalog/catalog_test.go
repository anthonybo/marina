package catalog

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// write creates a file with the given contents inside dir.
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func project(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestInspectPicksTheRightCommand covers each ecosystem Marina claims to handle,
// including the package-manager detection that matters most: running a pnpm
// workspace with npm does not work.
func TestInspectPicksTheRightCommand(t *testing.T) {
	root := t.TempDir()

	node := project(t, root, "webapp")
	write(t, node, "package.json", `{"scripts":{"dev":"vite","build":"vite build"},"devDependencies":{"vite":"^7"}}`)
	write(t, node, "pnpm-lock.yaml", "lockfileVersion: 9\n")

	npmOnly := project(t, root, "plainapp")
	write(t, npmOnly, "package.json", `{"scripts":{"start":"node server.js"}}`)

	rust := project(t, root, "rustapp")
	write(t, rust, "Cargo.toml", "[package]\nname = \"rustapp\"\n")

	django := project(t, root, "djangoapp")
	write(t, django, "manage.py", "# django")
	write(t, django, "requirements.txt", "django\n")

	golang := project(t, root, "goapp")
	write(t, golang, "go.mod", "module goapp\n")
	write(t, golang, "main.go", "package main\nfunc main() {}\n")

	makeApp := project(t, root, "makeapp")
	write(t, makeApp, "Makefile", ".PHONY: dev\ndev:\n\techo hi\n")

	pyApp := project(t, root, "pyapp")
	write(t, pyApp, "requirements.txt", "flask\n")
	write(t, pyApp, "main.py", "print('hi')")

	c := New([]string{root}, time.Minute)
	projects, _ := c.Projects(context.Background())

	got := make(map[string]Project, len(projects))
	for _, p := range projects {
		got[p.Name] = p
	}

	want := map[string]struct{ command, manager string }{
		"webapp":    {"pnpm run dev", "pnpm"},
		"plainapp":  {"npm run start", "npm"},
		"rustapp":   {"cargo run", "cargo"},
		"djangoapp": {"python3 manage.py runserver", "python"},
		"goapp":     {"go run .", "go"},
		"makeapp":   {"make dev", "make"},
		"pyapp":     {"python3 main.py", "python"},
	}

	for name, expect := range want {
		p, ok := got[name]
		if !ok {
			t.Errorf("%s: not catalogued", name)
			continue
		}
		if p.Command != expect.command {
			t.Errorf("%s: command = %q, want %q", name, p.Command, expect.command)
		}
		if p.Manager != expect.manager {
			t.Errorf("%s: manager = %q, want %q", name, p.Manager, expect.manager)
		}
	}

	if fw := got["webapp"].Framework; fw != "Vite" {
		t.Errorf("webapp framework = %q, want Vite", fw)
	}
}

// TestScanSkipsWhatItCannotStart verifies Marina reports rather than guesses. A
// directory that is clearly a project but has no known start command must be
// counted, not invented a command for and not silently dropped.
func TestScanSkipsWhatItCannotStart(t *testing.T) {
	root := t.TempDir()

	// A git repo with nothing runnable in it.
	bare := project(t, root, "notes")
	write(t, bare, ".git/HEAD", "ref: refs/heads/main\n")

	// A package.json with no usable script.
	noScript := project(t, root, "lib")
	write(t, noScript, "package.json", `{"scripts":{"test":"vitest","build":"tsc"}}`)

	// Not a project at all — must not even be counted.
	plain := project(t, root, "scratch")
	write(t, plain, "notes.txt", "hello")

	// One that does work, to prove scanning continued.
	ok := project(t, root, "app")
	write(t, ok, "package.json", `{"scripts":{"dev":"next dev"},"dependencies":{"next":"^15"}}`)

	c := New([]string{root}, time.Minute)
	projects, skipped := c.Projects(context.Background())

	if len(projects) != 1 || projects[0].Name != "app" {
		t.Fatalf("projects = %+v, want only \"app\"", projects)
	}
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2 (notes and lib; scratch is not a project)", skipped)
	}
	if projects[0].Framework != "Next.js" {
		t.Errorf("framework = %q, want Next.js", projects[0].Framework)
	}
}

// TestLookupRejectsUnknownPaths is the guard the launcher depends on: a start
// request can only ever name something the catalogue actually found.
func TestLookupRejectsUnknownPaths(t *testing.T) {
	root := t.TempDir()
	app := project(t, root, "app")
	write(t, app, "package.json", `{"scripts":{"dev":"vite"}}`)

	c := New([]string{root}, time.Minute)
	c.Projects(context.Background())

	if _, ok := c.Lookup(app); !ok {
		t.Error("Lookup rejected a catalogued project")
	}
	for _, bad := range []string{"/etc", "/", filepath.Join(root, "nope"), ""} {
		if _, ok := c.Lookup(bad); ok {
			t.Errorf("Lookup(%q) succeeded — it must only match catalogued projects", bad)
		}
	}
}

// TestLaunchRefusesUnknownPath verifies the launcher never runs anything for a
// path outside the catalogue.
func TestLaunchRefusesUnknownPath(t *testing.T) {
	root := t.TempDir()
	c := New([]string{root}, time.Minute)
	c.Projects(context.Background())

	l := testLauncher(t, c)
	for _, bad := range []string{"/etc", "/", filepath.Join(root, "nope")} {
		if _, err := l.Start(context.Background(), bad); err == nil {
			t.Errorf("Start(%q) succeeded — unknown paths must be refused", bad)
		}
	}
}

// TestLaunchReportsAFailingCommand is the behaviour whose absence made a failed
// launch look like an eternal "starting": the launcher must notice that the
// command died and say why.
func TestLaunchReportsAFailingCommand(t *testing.T) {
	root := t.TempDir()
	project(t, root, "broken")
	// A script that does not exist, standing in for `pnpm` missing from PATH.
	write(t, filepath.Join(root, "broken"), "package.json",
		`{"name":"broken","scripts":{"dev":"marina-no-such-command-xyz"}}`)

	c := New([]string{root}, time.Minute)
	c.Projects(context.Background())
	l := testLauncher(t, c)

	launch, err := l.Start(context.Background(), filepath.Join(root, "broken"))
	if err != nil {
		t.Fatalf("Start returned an error before running: %v", err)
	}

	// Wait for the watcher to observe the exit.
	var got Launch
	for i := 0; i < 100; i++ {
		time.Sleep(50 * time.Millisecond)
		for _, r := range l.Recent() {
			if r.Path == launch.Path {
				got = r
			}
		}
		if got.Failed() {
			break
		}
	}

	if !got.Failed() {
		t.Fatalf("launch never reported a failure; got %+v", got)
	}
	if got.ExitCode == nil || *got.ExitCode == 0 {
		t.Errorf("ExitCode = %v, want a non-zero status", got.ExitCode)
	}
	if !strings.Contains(got.Error, "not on the PATH") &&
		!strings.Contains(got.Error, "exited with status") {
		t.Errorf("Error = %q, want something actionable", got.Error)
	}
	// A failure must survive Settled: a port appearing elsewhere should not erase it.
	l.Settled(map[string]bool{launch.Path: true})
	stillThere := false
	for _, r := range l.Recent() {
		if r.Path == launch.Path && r.Failed() {
			stillThere = true
		}
	}
	if !stillThere {
		t.Error("a recorded failure was dropped by Settled")
	}
}

func TestDescribeExit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")

	cases := []struct {
		body string
		want string
	}{
		{"/bin/sh: pnpm: command not found\n", "not on the PATH"},
		{"Error: listen EADDRINUSE: address already in use :::3000\n", "already listening"},
		{"npm ERR! Missing script: \"dev\"\n", "no such script"},
		{"some other unrecognised explosion\n", "exited with status 1"},
	}
	for _, tc := range cases {
		if err := os.WriteFile(logPath, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := describeExit(1, logPath); !strings.Contains(got, tc.want) {
			t.Errorf("describeExit for %q = %q, want it to mention %q", tc.body, got, tc.want)
		}
	}
}

func testLauncher(t *testing.T, c *Catalog) *Launcher {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewLauncher(c, filepath.Join(t.TempDir(), "logs"), NewShellEnv(quiet, time.Minute), quiet)
}

func TestHasMakeTarget(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Makefile", ".PHONY: build\nbuild:\n\tgo build\n\ndev: build\n\tgo run .\n")

	if !hasMakeTarget(dir, "dev") {
		t.Error("hasMakeTarget(dev) = false, want true")
	}
	if !hasMakeTarget(dir, "build") {
		t.Error("hasMakeTarget(build) = false, want true")
	}
	if hasMakeTarget(dir, "start") {
		t.Error("hasMakeTarget(start) = true, want false")
	}
}

func TestPickScriptPrefersDev(t *testing.T) {
	cases := []struct {
		scripts map[string]string
		want    string
	}{
		{map[string]string{"dev": "vite", "start": "node ."}, "dev"},
		{map[string]string{"start": "node ."}, "start"},
		{map[string]string{"serve": "http-server"}, "serve"},
		{map[string]string{"build": "tsc", "test": "vitest"}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := pickScript(tc.scripts); got != tc.want {
			t.Errorf("pickScript(%v) = %q, want %q", tc.scripts, got, tc.want)
		}
	}
}

// TestLookupScansOnFirstUse covers the race that made a start request right after
// login fail with "not a known project": Lookup must populate the catalogue
// itself rather than assuming a sweep has already happened.
func TestLookupScansOnFirstUse(t *testing.T) {
	root := t.TempDir()
	app := project(t, root, "app")
	write(t, app, "package.json", `{"name":"app","scripts":{"dev":"vite"}}`)

	c := New([]string{root}, time.Minute)
	// Deliberately no Projects() call first — this is the cold-start case.
	if _, ok := c.Lookup(app); !ok {
		t.Error("Lookup failed before any explicit scan; it must scan on demand")
	}
}
