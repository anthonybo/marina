package health

import (
	"testing"
	"time"
)

// TestParseCPUTime covers every format ps emits for cumulative CPU time. Getting
// this wrong silently produces nonsense percentages rather than an error.
func TestParseCPUTime(t *testing.T) {
	cases := map[string]float64{
		"0:00.00":     0,
		"0:01.50":     1.5,
		"1:30.00":     90,
		"140:19.06":   140*60 + 19.06,
		"2:03:04":     2*3600 + 3*60 + 4,
		"01-02:34:08": 86400 + 2*3600 + 34*60 + 8,
		"garbage":     0,
	}
	for input, want := range cases {
		if got := parseCPUTime(input); got != want {
			t.Errorf("parseCPUTime(%q) = %v, want %v", input, got, want)
		}
	}
}

// TestHistoryIsBounded keeps a long-running daemon from growing without limit.
func TestHistoryIsBounded(t *testing.T) {
	s := NewSampler(time.Second, 8)
	base := time.Now()
	for i := 0; i < historyLength*3; i++ {
		// Distinct sample times: a repeat of the same measurement is ignored by
		// design, so a series needs each point to come from its own sample.
		s.Record("app:x", Sample{CPU: float64(i), RSS: int64(i), At: base.Add(time.Duration(i) * time.Second)})
	}
	points := s.History("app:x")
	if len(points) != historyLength {
		t.Fatalf("history length = %d, want %d", len(points), historyLength)
	}
	// The newest sample must be last.
	if points[len(points)-1].CPU != float64(historyLength*3-1) {
		t.Errorf("last point = %v, want the most recent sample", points[len(points)-1].CPU)
	}
}

func TestForgetDropsDeadApps(t *testing.T) {
	s := NewSampler(time.Second, 8)
	now := time.Now()
	s.Record("app:alive", Sample{CPU: 1, At: now})
	s.Record("app:dead", Sample{CPU: 1, At: now})

	s.Forget(map[string]bool{"app:alive": true})

	if len(s.History("app:alive")) == 0 {
		t.Error("dropped history for a live app")
	}
	if len(s.History("app:dead")) != 0 {
		t.Error("kept history for an app that is gone")
	}
}

// TestReadyRequiresTwoSamples: CPU is a rate, so it is meaningless until there is
// an interval to divide by. Reporting 0% before then would look like an idle app.
func TestReadyRequiresTwoSamples(t *testing.T) {
	s := NewSampler(time.Second, 8)
	if s.Ready() {
		t.Error("Ready() before any sample")
	}
	s.mu.Lock()
	s.elapsed = time.Second
	s.mu.Unlock()
	if !s.Ready() {
		t.Error("Ready() should be true once an interval has elapsed")
	}
}

// fixture installs a known process table without running ps.
func fixture(t *testing.T, cores int, rows map[int]procInfo, rates map[int]float64) *Sampler {
	t.Helper()
	s := NewSampler(time.Second, cores)
	byGroup := map[int][]int{}
	for pid, info := range rows {
		byGroup[info.pgid] = append(byGroup[info.pgid], pid)
	}
	s.procs = rows
	s.byGroup = byGroup
	s.cpuRate = rates
	s.sampled = time.Now()
	s.elapsed = time.Second
	return s
}

// TestGroupsSeparatesAppsThatShareAncestry is the misattribution this design
// exists to prevent: an app Marina launched is a descendant of Marina, so a tree
// walk credited Marina with its whole cost and counted that cost twice. Process
// groups keep them apart.
func TestGroupsSeparatesAppsThatShareAncestry(t *testing.T) {
	// Group 100 is Marina. Group 200 is an app it launched.
	s := fixture(t, 8,
		map[int]procInfo{
			100: {pgid: 100, rss: 18 << 20},
			200: {pgid: 200, rss: 100 << 20},
			201: {pgid: 200, rss: 300 << 20},
		},
		map[int]float64{100: 0.5, 200: 40, 201: 60},
	)

	marina := s.Groups(100)
	if marina.Processes != 1 {
		t.Errorf("Marina Processes = %d, want 1", marina.Processes)
	}
	if want := int64(18 << 20); marina.RSS != want {
		t.Errorf("Marina RSS = %d, want %d", marina.RSS, want)
	}
	if marina.CPU != 0.5 {
		t.Errorf("Marina CPU = %v, want 0.5", marina.CPU)
	}

	app := s.Groups(200)
	if app.Processes != 2 || app.CPU != 100 {
		t.Errorf("app = %+v, want 2 processes and 100%% cpu", app)
	}
}

func TestGroupsDedupesAndIgnoresUnknown(t *testing.T) {
	s := fixture(t, 8,
		map[int]procInfo{10: {pgid: 10, rss: 1 << 20}},
		map[int]float64{10: 5},
	)
	// The same group twice, plus groups that do not exist.
	got := s.Groups(10, 10, 0, -1, 9999)
	if got.Processes != 1 || got.CPU != 5 {
		t.Errorf("got %+v, want one process at 5%%", got)
	}
}

// TestGroupsClampsTheAggregate: a reused pid can briefly produce a spurious delta,
// and an app reported at thousands of percent would draw a full bar and read as a
// crisis that isn't happening.
func TestGroupsClampsTheAggregate(t *testing.T) {
	rows := map[int]procInfo{}
	rates := map[int]float64{}
	for pid := 1; pid <= 20; pid++ {
		rows[pid] = procInfo{pgid: 7, rss: 1 << 20}
		rates[pid] = 400 // each already at the per-process cap for 4 cores
	}
	s := fixture(t, 4, rows, rates)

	got := s.Groups(7)
	if got.CPU != 400 {
		t.Errorf("CPU = %v, want it clamped to cores*100 = 400", got.CPU)
	}
}

func TestGroupsOfBatchesLookups(t *testing.T) {
	s := fixture(t, 8,
		map[int]procInfo{
			10: {pgid: 100}, 11: {pgid: 100}, 12: {pgid: 200},
		},
		nil,
	)
	got := s.GroupsOf(10, 11, 12, 9999)
	if len(got) != 2 {
		t.Fatalf("GroupsOf = %v, want two distinct groups", got)
	}
	seen := map[int]bool{got[0]: true, got[1]: true}
	if !seen[100] || !seen[200] {
		t.Errorf("GroupsOf = %v, want groups 100 and 200", got)
	}
}

// TestRecordIgnoresRepeatedSamples is the fix for a distorted sparkline: the
// caller sweeps every 2s while sampling happens every 3s, so without this the
// same measurement was stored more than once and the trace showed plateaus that
// never occurred.
func TestRecordIgnoresRepeatedSamples(t *testing.T) {
	s := NewSampler(time.Second, 8)
	at := time.Now()

	s.Record("app:x", Sample{CPU: 10, At: at})
	s.Record("app:x", Sample{CPU: 10, At: at}) // same measurement, seen again
	s.Record("app:x", Sample{CPU: 20, At: at.Add(3 * time.Second)})

	points := s.History("app:x")
	if len(points) != 2 {
		t.Fatalf("history = %d points, want 2 — a repeat must not be stored", len(points))
	}
	if points[0].CPU != 10 || points[1].CPU != 20 {
		t.Errorf("history = %+v, want 10 then 20", points)
	}
}

func TestRecordRejectsSamplesWithNoTime(t *testing.T) {
	s := NewSampler(time.Second, 8)
	s.Record("app:x", Sample{CPU: 10})
	if len(s.History("app:x")) != 0 {
		t.Error("a sample with no timestamp cannot be placed in a series")
	}
}

func TestParseRow(t *testing.T) {
	pid, pgid, rss, cpu, ok := parseRow("  10477   89317   391136 140:19.06")
	if !ok {
		t.Fatal("failed to parse a well-formed row")
	}
	if pid != 10477 || pgid != 89317 || rss != 391136 {
		t.Errorf("got pid=%d pgid=%d rss=%d", pid, pgid, rss)
	}
	if want := 140*60 + 19.06; cpu != want {
		t.Errorf("cpu = %v, want %v", cpu, want)
	}
	for _, bad := range []string{"", "1 2", "a b c d", "   "} {
		if _, _, _, _, ok := parseRow(bad); ok {
			t.Errorf("parseRow(%q) accepted a malformed row", bad)
		}
	}
}
