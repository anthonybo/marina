package identify

import (
	"path/filepath"
	"testing"

	"github.com/anthonybo/marina/daemon/internal/scan"
)

// Scanned directories are editable in the UI, and they double as the boundaries
// this walk stops at. This test pins down *why* the catalogue refuses a root that
// is, or sits inside, a project: such a boundary truncates the walk below the
// project's real root, and the app is then named after whichever subdirectory the
// walk stopped at.
//
// Both cases are recorded, including the one that surprised me — a .git root does
// NOT survive it, because the boundary is checked before .git is ever reached.
func TestABoundaryInsideAProjectMisnamesIt(t *testing.T) {
	t.Run("repo-less project", func(t *testing.T) {
		root := t.TempDir()
		projects := mkdir(t, filepath.Join(root, "projects"))
		mono := mkdir(t, filepath.Join(projects, "mono-app"))
		writeFile(t, filepath.Join(mono, "package.json"), `{"name":"mono-app"}`)
		inner := mkdir(t, filepath.Join(mono, "frontend"))
		writeFile(t, filepath.Join(inner, "package.json"), `{"name":"frontend"}`)

		if got := resolve(t, []string{projects}, inner); got.Project != "mono-app" {
			t.Fatalf("with a sane boundary the project is %q, want mono-app", got.Project)
		}
		if got := resolve(t, []string{projects, mono}, inner); got.Project == "mono-app" {
			t.Fatal("expected the extra boundary to break naming; if this now passes, the " +
				"catalogue's refusal of such roots can be relaxed")
		}
	})

	t.Run("git repository", func(t *testing.T) {
		root := t.TempDir()
		projects := mkdir(t, filepath.Join(root, "projects"))
		repo := mkdir(t, filepath.Join(projects, "webapp"))
		mkdir(t, filepath.Join(repo, ".git"))
		writeFile(t, filepath.Join(repo, "package.json"), `{"name":"webapp"}`)
		inner := mkdir(t, filepath.Join(repo, "packages", "frontend"))
		writeFile(t, filepath.Join(inner, "package.json"), `{"name":"@storm/frontend"}`)

		if got := resolve(t, []string{projects}, inner); got.Project != "webapp" {
			t.Fatalf("with a sane boundary the project is %q, want webapp", got.Project)
		}
		// The surprise: .git is not reached, because the boundary check happens on
		// the parent before the walk can step up to it.
		if got := resolve(t, []string{projects, repo}, inner); got.Project == "webapp" {
			t.Fatal("a boundary inside a git repo no longer breaks naming; the catalogue's " +
				"refusal could then be narrowed to repo-less projects")
		}
	})
}

// Changing boundaries must actually take effect on an already-warm resolver:
// every cached answer was computed under the old rules.
func TestSetBoundariesReresolvesCachedIdentities(t *testing.T) {
	root := t.TempDir()
	projects := mkdir(t, filepath.Join(root, "projects"))
	mono := mkdir(t, filepath.Join(projects, "mono-app"))
	writeFile(t, filepath.Join(mono, "package.json"), `{"name":"mono-app"}`)
	inner := mkdir(t, filepath.Join(mono, "frontend"))
	writeFile(t, filepath.Join(inner, "package.json"), `{"name":"frontend"}`)

	sock := scan.Socket{PID: 4321, Proc: "node", Port: 3000, V4: true}
	proc := scan.Proc{Cwd: inner, Cmd: "node server.js"}

	r := New(nil)
	first := r.Identify(sock, proc)

	r.SetBoundaries([]string{projects})
	second := r.Identify(sock, proc)

	if second.Project != "mono-app" {
		t.Fatalf("after SetBoundaries the project is %q, want mono-app (was %q) — the cache was not rebuilt",
			second.Project, first.Project)
	}
	if len(r.Unresolved([]int{sock.PID})) != 0 {
		t.Fatal("the identity should be cached again after re-identifying")
	}
}
