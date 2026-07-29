package monitor

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/anthonybo/marina/daemon/internal/catalog"
	"github.com/anthonybo/marina/daemon/internal/identify"
)

func svc(port int, dir string) Service {
	return Service{Service: identify.Service{Port: port, Dir: dir, Kind: identify.KindApp}}
}

// A one-off script that happens to hold a port must not make a project look like
// it is already running — that removed the launch button entirely. Observed with
// `node scripts/check-clipping.mjs` listening on :54656 inside a project.
func TestAToolOnADynamicPortStillLeavesTheProjectLaunchable(t *testing.T) {
	dir := t.TempDir()
	project := catalog.Project{Name: "templates", Path: dir, Real: dir, Command: "npm run dev"}

	tool := []Service{svc(54656, dir)}
	got := ashoreFrom([]catalog.Project{project}, runningPaths(serverLike(tool)), nil, nil, nil, nil)
	if len(got) != 1 {
		t.Fatalf("a check script on :54656 hid the project from the boatyard (%d entries)", len(got))
	}

	// A real dev server on a chosen port must still exclude it.
	server := []Service{svc(4321, dir)}
	got = ashoreFrom([]catalog.Project{project}, runningPaths(serverLike(server)), nil, nil, nil, nil)
	if len(got) != 0 {
		t.Fatalf("a dev server on :4321 should mark the project running, got %d boatyard entries", len(got))
	}
}

// The launcher's "did it come up?" question is different, and any listener answers
// it — a launched server that picked a random port must not hang on "starting".
func TestSettledStillCountsAnyListener(t *testing.T) {
	dir := t.TempDir()
	if paths := runningPaths([]Service{svc(54656, dir)}); !paths[filepath.Clean(dir)] {
		t.Fatal("runningPaths must still see a dynamic-port listener; the launcher needs it to settle")
	}
}

// Ports the OS handed out at random are not predictions.
func TestDynamicPortsAreNotOfferedAsPredictions(t *testing.T) {
	dir := t.TempDir()
	project := catalog.Project{Name: "templates", Path: dir, Real: dir, Command: "npm run dev"}
	history := func(string) []int { return []int{65143, 4321, 54656} }
	lastSeen := func(string) *time.Time { return nil }

	got := ashoreFrom([]catalog.Project{project}, nil, nil, lastSeen, history, nil)
	if len(got) != 1 {
		t.Fatalf("want the project listed, got %d", len(got))
	}
	for _, p := range got[0].Expect {
		if isDynamicPort(p.Port) {
			t.Fatalf("offered :%d as an expected port; it is in the OS dynamic range", p.Port)
		}
	}
	if len(got[0].Expect) == 0 || got[0].Expect[0].Port != 4321 {
		t.Fatalf("expected the real port 4321 to survive, got %+v", got[0].Expect)
	}
}
