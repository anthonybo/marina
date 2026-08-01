package catalog

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestCleanRootsExpandsEveryTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	// The case that matters: a shell expands only the first ~ in a comma list, so
	// the second arrives literally and must be expanded here.
	got := CleanRoots([]string{filepath.Join(home, "projects"), "~/git", " ~/work "})
	want := []string{
		filepath.Join(home, "projects"),
		filepath.Join(home, "git"),
		filepath.Join(home, "work"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("CleanRoots = %v, want %v", got, want)
	}
}

func TestCleanRootsDropsBlanksAndDuplicates(t *testing.T) {
	got := CleanRoots([]string{"/tmp/a", "", "  ", "/tmp/a/", "/tmp/a/../a", "/tmp/b"})
	want := []string{"/tmp/a", "/tmp/b"}
	if !slices.Equal(got, want) {
		t.Fatalf("CleanRoots = %v, want %v", got, want)
	}
}

func TestValidateRootRejectsWhatCannotBeScanned(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		input    string
		existing []string
	}{
		{"empty", "   ", nil},
		{"root directory", "/", nil},
		{"missing", filepath.Join(dir, "nope"), nil},
		{"a file", file, nil},
		{"already scanned", dir, []string{dir}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateRoot(tc.input, tc.existing); err == nil {
				t.Fatalf("ValidateRoot(%q, %v) accepted it; want an error", tc.input, tc.existing)
			}
		})
	}
}

func TestValidateRootAcceptsARealDirectory(t *testing.T) {
	dir := t.TempDir()
	got, err := ValidateRoot(dir+"/", []string{"/tmp/elsewhere"})
	if err != nil {
		t.Fatalf("ValidateRoot: %v", err)
	}
	if got != filepath.Clean(dir) {
		t.Fatalf("ValidateRoot = %q, want %q", got, filepath.Clean(dir))
	}
}

func TestRootStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewRootStore(dir)

	// Nothing saved yet: the fallback answers, and says it did not come from file.
	got, fromFile := s.Load([]string{"/tmp/seed"})
	if fromFile || !slices.Equal(got, []string{"/tmp/seed"}) {
		t.Fatalf("Load before save = %v, fromFile=%v", got, fromFile)
	}

	if err := s.Save([]string{"/tmp/a", "/tmp/b"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, fromFile = s.Load([]string{"/tmp/seed"})
	if !fromFile || !slices.Equal(got, []string{"/tmp/a", "/tmp/b"}) {
		t.Fatalf("Load after save = %v, fromFile=%v", got, fromFile)
	}
}

// A damaged file must not cost the boatyard entirely: better to scan the
// configured default than nothing at all.
func TestRootStoreFallsBackOnDamage(t *testing.T) {
	for _, body := range []string{"{not json", `{"roots":[]}`, ""} {
		dir := t.TempDir()
		s := NewRootStore(dir)
		if err := os.WriteFile(s.Path(), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, fromFile := s.Load([]string{"/tmp/seed"})
		if fromFile || !slices.Equal(got, []string{"/tmp/seed"}) {
			t.Fatalf("Load(%q) = %v, fromFile=%v; want the fallback", body, got, fromFile)
		}
	}
}

// Adding a directory must change what the next scan looks at, without waiting
// for the cache TTL to lapse.
func TestSetRootsInvalidatesTheScan(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	mustProject(t, filepath.Join(first, "alpha"))
	mustProject(t, filepath.Join(second, "beta"))

	c := New([]string{first}, time.Hour)
	if got := names(c.Projects(t.Context())); !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("first scan = %v, want [alpha]", got)
	}

	if changed := c.SetRoots([]string{first, second}); !changed {
		t.Fatal("SetRoots reported no change after adding a directory")
	}
	got := names(c.Projects(t.Context()))
	slices.Sort(got)
	if !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("after SetRoots = %v, want [alpha beta] despite the one-hour TTL", got)
	}

	if changed := c.SetRoots([]string{first, second}); changed {
		t.Fatal("SetRoots reported a change for an identical list")
	}
}

func mustProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + filepath.Base(dir) + `","scripts":{"dev":"vite"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(projects []Project, _ int) []string {
	out := make([]string, 0, len(projects))
	for _, p := range projects {
		out = append(out, p.Name)
	}
	return out
}

// A cancelled scan must not become the cached answer. The API passes the HTTP
// request's context, so a user who closes the tab mid-request would otherwise
// leave a truncated project list cached for the whole TTL.
func TestCancelledScanDoesNotPoisonTheCache(t *testing.T) {
	root := t.TempDir()
	mustProject(t, filepath.Join(root, "alpha"))
	mustProject(t, filepath.Join(root, "beta"))

	c := New([]string{root}, time.Hour)

	dead, cancel := context.WithCancel(context.Background())
	cancel()
	c.Projects(dead) // the aborted request

	got := names(c.Projects(t.Context()))
	slices.Sort(got)
	if !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("after a cancelled scan the catalogue reports %v, want [alpha beta]", got)
	}
}

// Scanned directories double as naming boundaries, so one placed at or inside a
// project silently misnames that project's running apps. Refusing it here is the
// guard; identify/boundary_change_test.go pins down why it is needed.
func TestValidateRootRefusesProjectsAndTheirInsides(t *testing.T) {
	root := t.TempDir()
	projects := mkdir(t, filepath.Join(root, "projects"))

	// A repo-less monorepo, and a git repo, each with an inner package.
	mono := mkdir(t, filepath.Join(projects, "mono-app"))
	writeJSON(t, filepath.Join(mono, "package.json"), `{"name":"mono-app"}`)
	monoInner := mkdir(t, filepath.Join(mono, "frontend"))

	repo := mkdir(t, filepath.Join(projects, "webapp"))
	mkdir(t, filepath.Join(repo, ".git"))
	repoInner := mkdir(t, filepath.Join(repo, "packages", "frontend"))

	for _, tc := range []struct{ name, path string }{
		{"a repo-less project itself", mono},
		{"inside a repo-less project", monoInner},
		{"a git repository itself", repo},
		{"deep inside a git repository", repoInner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateRoot(tc.path, nil); err == nil {
				t.Fatalf("ValidateRoot(%q) was accepted; it would misname that project's apps", tc.path)
			} else {
				t.Logf("refused with: %v", err)
			}
		})
	}

	// The container itself must still be accepted — including when it holds a
	// stray package.json with no name, which is the real state of ~/projects.
	writeJSON(t, filepath.Join(projects, "package.json"), `{"private":true}`)
	if _, err := ValidateRoot(projects, nil); err != nil {
		t.Fatalf("a directory of projects was refused: %v", err)
	}
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeJSON(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Nesting a root inside another must be allowed, and must not list anything
// twice. This is the ~/projects/workspace case: a directory of directories of
// projects is invisible, because it is not itself a project and the scan of its
// parent never looks inside it. Adding it is the only fix, and the panel's own
// hint tells you to — so refusing it made the advice impossible to follow.
func TestNestedRootsAreAllowedAndDoNotDuplicate(t *testing.T) {
	outer := t.TempDir()
	// A real project directly under the outer root.
	mustProject(t, filepath.Join(outer, "marina"))
	// A container of projects, itself not a project — invisible to a one-level scan.
	inner := mkdir(t, filepath.Join(outer, "workspace"))
	mustProject(t, filepath.Join(inner, "app-one"))
	mustProject(t, filepath.Join(inner, "app-two"))

	c := New([]string{outer}, time.Hour)
	got := names(c.Projects(t.Context()))
	slices.Sort(got)
	if !slices.Equal(got, []string{"marina"}) {
		t.Fatalf("scanning only the outer root found %v, want just [marina] — the container's projects are a level too deep", got)
	}

	// The fix must be permitted.
	if _, err := ValidateRoot(inner, []string{outer}); err != nil {
		t.Fatalf("adding the container was refused: %v", err)
	}

	c.SetRoots([]string{outer, inner})
	got = names(c.Projects(t.Context()))
	slices.Sort(got)
	want := []string{"app-one", "app-two", "marina"}
	if !slices.Equal(got, want) {
		t.Fatalf("with both roots: %v, want %v", got, want)
	}

	// Each project exactly once: one level deep means the two scans cover
	// disjoint sets, whatever the nesting.
	seen := map[string]int{}
	for _, n := range got {
		seen[n]++
	}
	for name, n := range seen {
		if n != 1 {
			t.Fatalf("%s listed %d times; nested roots must not duplicate", name, n)
		}
	}
}
