package monitor

import (
	"testing"
	"time"

	"github.com/anthonybo/marina/daemon/internal/catalog"
)

// The port a project used an hour ago beats one it used yesterday, whatever the
// numbers are. This broke in practice: a project moved from 3001 to 8930 and went
// on being predicted at 3001 for a day, because the sort broke ties on the port
// number and 3001 is smaller.
func TestMostRecentPortWinsRegardlessOfNumber(t *testing.T) {
	dir := t.TempDir()
	project := catalog.Project{Name: "app", Path: dir, Real: dir, Command: "npm run dev"}
	// As PortsForPath returns them: most recently seen first.
	history := func(string) []int { return []int{8930, 9640, 3001} }
	lastSeen := func(string) *time.Time { return nil }

	got := ashoreFrom([]catalog.Project{project}, nil, nil, lastSeen, history, nil)
	if len(got) != 1 || len(got[0].Expect) == 0 {
		t.Fatalf("no expectation produced: %+v", got)
	}
	if first := got[0].Expect[0].Port; first != 8930 {
		t.Fatalf("predicted :%d, want :8930 — the newest port, not the lowest-numbered", first)
	}
}

// A long-lived project accumulates every port it ever answered on. One had
// thirty-nine, which is an archive rather than a prediction — and every stale entry
// is another chance to warn about a conflict that is nothing to do with it.
func TestHistoryIsBounded(t *testing.T) {
	dir := t.TempDir()
	project := catalog.Project{Name: "app", Path: dir, Real: dir, Command: "npm run dev"}
	many := make([]int, 0, 39)
	for p := 8930; p < 8930+39; p++ {
		many = append(many, p)
	}
	history := func(string) []int { return many }
	lastSeen := func(string) *time.Time { return nil }

	got := ashoreFrom([]catalog.Project{project}, nil, nil, lastSeen, history, nil)
	fromHistory := 0
	for _, p := range got[0].Expect {
		if p.Source == catalog.SourceHistory {
			fromHistory++
		}
	}
	if fromHistory > maxHistoryPorts {
		t.Fatalf("offered %d history ports, want at most %d", fromHistory, maxHistoryPorts)
	}
	// And the ones kept must be the newest, i.e. the front of the list.
	if got[0].Expect[0].Port != many[0] {
		t.Fatalf("kept :%d first, want the most recent :%d", got[0].Expect[0].Port, many[0])
	}
}

// Stronger evidence still outranks history — the ordering rule that was already
// right must not have been broken by fixing the tie-break.
func TestEvidenceStrengthStillWins(t *testing.T) {
	ports := []catalog.ExpectedPort{
		{Port: 4321, Source: catalog.SourceDefault},
		{Port: 8930, Source: catalog.SourceHistory},
		{Port: 3000, Source: catalog.SourceConfig},
	}
	catalog.SortPorts(ports)
	if ports[0].Source != catalog.SourceHistory {
		t.Fatalf("first is %s, want history to outrank config and default", ports[0].Source)
	}
	if ports[len(ports)-1].Source != catalog.SourceDefault {
		t.Fatalf("last is %s, want the framework default to rank lowest", ports[len(ports)-1].Source)
	}
}
