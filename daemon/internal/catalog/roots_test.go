package catalog

import (
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
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
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
		{"inside an existing root", nested, []string{dir}},
		{"contains an existing root", dir, []string{nested}},
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
