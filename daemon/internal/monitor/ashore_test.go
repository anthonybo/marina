package monitor

import (
	"path/filepath"
	"testing"

	"github.com/anthonybo/marina/daemon/internal/catalog"
)

// TestMarinaDoesNotListItself covers a confusing state that was technically true:
// Marina appeared out on the water AND in the boatyard at the same time. The
// running daemon is the installed binary in ~/.local/share with a cwd of "/", so
// nothing connected it to its own checkout, and the catalogue reported that
// checkout as available to start.
func TestMarinaDoesNotListItself(t *testing.T) {
	source := t.TempDir()
	self := catalog.Project{Name: "marina", Path: source, Real: source, Command: "npm run dev"}
	other := catalog.Project{
		Name:    "somethingelse",
		Path:    filepath.Join(source, "..", "other"),
		Real:    filepath.Join(source, "..", "other"),
		Command: "npm run dev",
	}

	// No live service reports this directory — exactly the real situation.
	live := map[string]bool{}
	for _, path := range selfPaths(source) {
		live[path] = true
	}

	ashore := ashoreFrom([]catalog.Project{self, other}, live, nil, nil, nil, nil)

	for _, entry := range ashore {
		if entry.Name == "marina" {
			t.Error("Marina listed itself as available to start while running")
		}
	}
	if len(ashore) != 1 || ashore[0].Name != "somethingelse" {
		t.Errorf("got %d entries %+v, want only the unrelated project", len(ashore), ashore)
	}
}

// TestSelfPathsEmptyWhenUnset: a binary built without the ldflag degrades to the
// old behaviour rather than misbehaving.
func TestSelfPathsEmptyWhenUnset(t *testing.T) {
	if got := selfPaths(""); got != nil {
		t.Errorf("selfPaths(\"\") = %v, want nil", got)
	}
}
