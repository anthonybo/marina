package identify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthonybo/marina/daemon/internal/scan"
)

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resolve runs the real identification path for a process with the given cwd.
func resolve(t *testing.T, roots []string, cwd string) Service {
	t.Helper()
	r := New(roots)
	return r.Identify(
		scan.Socket{PID: 1234, Proc: "node", Port: 3000, V4: true},
		scan.Proc{Cwd: cwd, Cmd: "node /x/node_modules/.bin/next dev"},
	)
}

// TestRepoLessMonorepoUsesOuterProject is the bug this test file exists for.
//
// mono-app has no .git, an outer package.json named "mono-app", and inner
// frontend/backend packages. Resolving from the inner directory must yield the
// project "mono-app" with subpath "frontend" — never the project "frontend",
// which is a role inside a project and not a project name.
func TestRepoLessMonorepoUsesOuterProject(t *testing.T) {
	root := t.TempDir()
	projects := mkdir(t, filepath.Join(root, "projects"))

	writeFile(t, filepath.Join(projects, "mono-app", "package.json"),
		`{"name":"mono-app","scripts":{"dev":"concurrently x y"}}`)
	frontend := mkdir(t, filepath.Join(projects, "mono-app", "frontend"))
	writeFile(t, filepath.Join(frontend, "package.json"),
		`{"name":"mono-app-frontend","dependencies":{"next":"^15"}}`)

	svc := resolve(t, []string{projects}, frontend)

	if svc.Project != "mono-app" {
		t.Errorf("Project = %q, want %q", svc.Project, "mono-app")
	}
	if svc.Subpath != "frontend" {
		t.Errorf("Subpath = %q, want %q", svc.Subpath, "frontend")
	}
	if svc.Kind != KindApp {
		t.Errorf("Kind = %q, want %q", svc.Kind, KindApp)
	}
}

// TestStrayPackageJSONIsNotAProject: a package.json with no "name" is a scratch
// file, not a project root. The real ~/projects has one, and adopting it would
// name every project after the container directory.
func TestStrayPackageJSONIsNotAProject(t *testing.T) {
	root := t.TempDir()
	projects := mkdir(t, filepath.Join(root, "projects"))
	// The stray file, exactly as found in the wild: no name, no deps.
	writeFile(t, filepath.Join(projects, "package.json"), `{"dependencies":{}}`)

	app := mkdir(t, filepath.Join(projects, "myapp"))
	writeFile(t, filepath.Join(app, "package.json"), `{"name":"myapp","scripts":{"dev":"vite"}}`)

	// Resolve with NO configured roots, so only the name check can save us.
	svc := resolve(t, nil, app)
	if svc.Project != "myapp" {
		t.Errorf("Project = %q, want %q — an unnamed package.json must not win", svc.Project, "myapp")
	}
}

// TestBoundaryStopsTheWalk: even a *named* package.json in a roots directory must
// not become the project, because roots hold projects by definition.
func TestBoundaryStopsTheWalk(t *testing.T) {
	root := t.TempDir()
	projects := mkdir(t, filepath.Join(root, "projects"))
	writeFile(t, filepath.Join(projects, "package.json"), `{"name":"my-scratch-space"}`)

	app := mkdir(t, filepath.Join(projects, "toolbox", "web"))
	writeFile(t, filepath.Join(projects, "toolbox", "package.json"), `{"name":"toolbox"}`)
	writeFile(t, filepath.Join(app, "package.json"), `{"name":"toolbox-web"}`)

	svc := resolve(t, []string{projects}, app)
	if svc.Project != "toolbox" {
		t.Errorf("Project = %q, want %q", svc.Project, "toolbox")
	}
	if svc.Subpath != "web" {
		t.Errorf("Subpath = %q, want %q", svc.Subpath, "web")
	}
}

// TestGitRootWins keeps the definitive signal definitive: a .git directory ends
// the search even when an outer directory also names itself a project.
func TestGitRootWins(t *testing.T) {
	root := t.TempDir()
	projects := mkdir(t, filepath.Join(root, "projects"))
	writeFile(t, filepath.Join(projects, "umbrella", "package.json"), `{"name":"umbrella"}`)

	repo := mkdir(t, filepath.Join(projects, "umbrella", "webapp"))
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(repo, "package.json"), `{"name":"webapp"}`)
	pkg := mkdir(t, filepath.Join(repo, "packages", "backend"))
	writeFile(t, filepath.Join(pkg, "package.json"), `{"name":"@webapp/backend"}`)

	svc := resolve(t, []string{projects}, pkg)
	if svc.Project != "webapp" {
		t.Errorf("Project = %q, want %q", svc.Project, "webapp")
	}
	if svc.Subpath != "packages/backend" {
		t.Errorf("Subpath = %q, want %q", svc.Subpath, "packages/backend")
	}
}

// TestSingleLevelProject: the ordinary case must keep working.
func TestSingleLevelProject(t *testing.T) {
	root := t.TempDir()
	projects := mkdir(t, filepath.Join(root, "projects"))
	app := mkdir(t, filepath.Join(projects, "solo-app"))
	writeFile(t, filepath.Join(app, "package.json"), `{"name":"solo-app","dependencies":{"vite":"^7"}}`)

	svc := resolve(t, []string{projects}, app)
	if svc.Project != "solo-app" {
		t.Errorf("Project = %q, want %q", svc.Project, "solo-app")
	}
	if svc.Subpath != "" {
		t.Errorf("Subpath = %q, want empty for a top-level project", svc.Subpath)
	}
}

// TestNonNodeMarkersCountAsProjects covers go.mod / Cargo.toml layouts, which
// have no name field to check.
func TestNonNodeMarkersCountAsProjects(t *testing.T) {
	root := t.TempDir()
	projects := mkdir(t, filepath.Join(root, "projects"))

	svc := func(marker string) Service {
		dir := mkdir(t, filepath.Join(projects, "svc-"+marker, "cmd", "api"))
		writeFile(t, filepath.Join(projects, "svc-"+marker, marker), "x")
		return resolve(t, []string{projects}, dir)
	}

	for _, marker := range []string{"go.mod", "Cargo.toml", "pyproject.toml"} {
		got := svc(marker)
		want := "svc-" + marker
		if got.Project != want {
			t.Errorf("%s: Project = %q, want %q", marker, got.Project, want)
		}
		if got.Subpath != "cmd/api" {
			t.Errorf("%s: Subpath = %q, want %q", marker, got.Subpath, "cmd/api")
		}
	}
}

func TestSelfNamedProject(t *testing.T) {
	dir := t.TempDir()

	if _, ok := selfNamedProject(dir); ok {
		t.Error("an empty directory must not count as a project")
	}
	writeFile(t, filepath.Join(dir, "package.json"), `{"dependencies":{}}`)
	if _, ok := selfNamedProject(dir); ok {
		t.Error("an unnamed package.json must not count as a project")
	}
	writeFile(t, filepath.Join(dir, "package.json"), `{"name":"thing"}`)
	name, ok := selfNamedProject(dir)
	if !ok || name != "thing" {
		t.Errorf("selfNamedProject = (%q, %v), want (\"thing\", true)", name, ok)
	}
}
