// Package health measures what each app is costing the machine.
//
// Three decisions shape everything here.
//
// First, CPU is computed by differencing cumulative CPU time between samples, not
// read from ps's %CPU column. On macOS that column is a decayed average whose
// window is unspecified; differencing `time` over a known interval gives a figure
// you can actually reason about ("3% of one core over the last 3 seconds").
//
// Second, an app's cost is its *process group*. A single process badly understates
// a dev server, because the work is in the children it spawns. But a process tree
// overstates it just as badly: anything Marina launched is a descendant of Marina,
// so walking Marina's tree credited it with a launched app's whole cost and counted
// that cost twice. A process group is exactly what job control calls one job —
// `pnpm run dev` and its twelve workers share one, and the daemon has its own.
//
// Third, this package must never become the thing it is measuring. One ps call per
// sample covers every process; nothing is done per-app at sample time; the write
// lock is held only long enough to publish; and no work is done to support features
// that are not used.
package health

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sample is what one app costs, aggregated over its process group.
type Sample struct {
	// CPU is percent of a single core. 250 means two and a half cores busy.
	CPU float64 `json:"cpu"`
	// RSS is resident memory in bytes.
	RSS int64 `json:"rss"`
	// Processes is how many processes were counted.
	Processes int `json:"processes"`
	// At is when this was measured. Callers use it to tell a fresh sample from a
	// repeat of one they have already seen.
	At time.Time `json:"at"`
}

// Point is one entry in an app's recent history, kept small enough to send often.
type Point struct {
	CPU float64 `json:"cpu"`
	RSS int64   `json:"rss"`
}

// Machine describes the host, so an app's share can be put in context.
type Machine struct {
	Cores int `json:"cores"`
	// TotalRSS is the resident memory of every process, for a rough share.
	TotalRSS int64   `json:"totalRss"`
	TotalCPU float64 `json:"totalCpu"`
}

// historyLength is how many samples to keep per app. At the default cadence this
// is a few minutes — enough for a sparkline to show a trend, small enough that
// the whole payload stays a few KB.
const historyLength = 60

type procInfo struct {
	pgid    int
	rss     int64
	cpuTime float64
}

// Sampler polls the process table and answers questions about process groups.
type Sampler struct {
	interval time.Duration
	cores    int

	mu      sync.RWMutex
	procs   map[int]procInfo
	byGroup map[int][]int
	prevCPU map[int]float64
	cpuRate map[int]float64 // per-pid percent of one core
	history map[string][]Point
	// lastPoint is the sample time each key was last recorded at, so a caller
	// sweeping on a different cadence cannot record the same measurement twice.
	lastPoint map[string]time.Time
	machine   Machine
	sampled   time.Time
	elapsed   time.Duration
}

// NewSampler returns a Sampler. Nothing runs until Run is called.
func NewSampler(interval time.Duration, cores int) *Sampler {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	if cores < 1 {
		cores = 1
	}
	return &Sampler{
		interval:  interval,
		cores:     cores,
		procs:     make(map[int]procInfo),
		byGroup:   make(map[int][]int),
		prevCPU:   make(map[int]float64),
		cpuRate:   make(map[int]float64),
		history:   make(map[string][]Point),
		lastPoint: make(map[string]time.Time),
	}
}

// Run samples until ctx is done.
//
// Deliberately on its own cadence rather than riding the port sweep: the sweep
// exists to notice structural change and must stay cheap, while metrics are
// inherently always-changing and are polled by the UI rather than pushed.
func (s *Sampler) Run(ctx context.Context) {
	s.sample(ctx)

	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sample(ctx)
		}
	}
}

// Interval reports the sampling cadence, so clients can poll no faster.
func (s *Sampler) Interval() time.Duration { return s.interval }

func (s *Sampler) sample(ctx context.Context) {
	// One call covers every process on the machine: ~40ms for 800 processes.
	// Targeting specific pids would be no cheaper and would miss their children.
	out, err := output(ctx, "/bin/ps", "-Ao", "pid=,pgid=,rss=,time=")
	if err != nil && out == "" {
		return
	}

	now := time.Now()
	procs := make(map[int]procInfo, 900)
	byGroup := make(map[int][]int, 400)
	var totalRSS int64

	// Scanned line by line rather than strings.Split, which would allocate a
	// slice holding every line of a ~50KB dump on every sample.
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	for scanner.Scan() {
		pid, pgid, rssKB, cpu, ok := parseRow(scanner.Text())
		if !ok {
			continue
		}
		info := procInfo{pgid: pgid, rss: rssKB * 1024, cpuTime: cpu}
		procs[pid] = info
		byGroup[pgid] = append(byGroup[pgid], pid)
		totalRSS += info.rss
	}

	// Read what is needed to compute rates, then compute outside the lock: the
	// exclusive section should be a handful of assignments, not two passes over
	// nine hundred processes while every reader waits.
	s.mu.RLock()
	prevCPU, lastSampled := s.prevCPU, s.sampled
	s.mu.RUnlock()

	elapsed := time.Duration(0)
	if !lastSampled.IsZero() {
		elapsed = now.Sub(lastSampled)
	}

	rates := make(map[int]float64, len(procs))
	next := make(map[int]float64, len(procs))
	var totalCPU float64
	seconds := elapsed.Seconds()

	for pid, info := range procs {
		next[pid] = info.cpuTime

		if seconds <= 0 {
			continue
		}
		previous, seen := prevCPU[pid]
		if !seen {
			continue // first time seen; no interval to divide by yet
		}
		delta := info.cpuTime - previous
		if delta <= 0 {
			// Either idle, or the pid was reused and the counter went backwards.
			continue
		}
		rate := delta / seconds * 100
		if max := float64(s.cores) * 100; rate > max {
			rate = max
		}
		rates[pid] = rate
		totalCPU += rate
	}

	s.mu.Lock()
	s.procs, s.byGroup, s.prevCPU, s.cpuRate = procs, byGroup, next, rates
	s.machine = Machine{Cores: s.cores, TotalRSS: totalRSS, TotalCPU: totalCPU}
	s.sampled, s.elapsed = now, elapsed
	s.mu.Unlock()
}

// parseRow reads one `pid pgid rss time` row without allocating a field slice.
func parseRow(line string) (pid, pgid int, rssKB int64, cpu float64, ok bool) {
	fields := [4]string{}
	found := 0
	for i := 0; i < 4; i++ {
		line = strings.TrimLeft(line, " \t")
		if line == "" {
			break
		}
		end := strings.IndexAny(line, " \t")
		if end < 0 {
			fields[i] = line
			line = ""
		} else {
			fields[i] = line[:end]
			line = line[end:]
		}
		found++
	}
	if found < 4 {
		return 0, 0, 0, 0, false
	}

	var err error
	if pid, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, 0, 0, false
	}
	if pgid, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, 0, 0, false
	}
	if rssKB, err = strconv.ParseInt(fields[2], 10, 64); err != nil {
		return 0, 0, 0, 0, false
	}
	return pid, pgid, rssKB, parseCPUTime(fields[3]), true
}

// Machine reports host totals for context.
func (s *Sampler) Machine() Machine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.machine
}

// Ready reports whether at least two samples have been taken, which is when CPU
// figures become meaningful.
func (s *Sampler) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.elapsed > 0
}

// Groups aggregates every process in the given process groups.
//
// See the package comment for why this, and not a tree walk, is the right unit.
func (s *Sampler) Groups(pgids ...int) Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[int]bool, len(pgids))
	var sample Sample

	for _, pgid := range pgids {
		if pgid <= 0 || seen[pgid] {
			continue
		}
		seen[pgid] = true
		for _, pid := range s.byGroup[pgid] {
			info, ok := s.procs[pid]
			if !ok {
				continue
			}
			sample.RSS += info.rss
			sample.CPU += s.cpuRate[pid]
			sample.Processes++
		}
	}

	// Clamp the total as well as the parts. A reused pid can briefly produce a
	// spurious delta, and an app reported at "4000%" would be obvious nonsense
	// that the meter would still draw as a full bar.
	if max := float64(s.cores) * 100; sample.CPU > max {
		sample.CPU = max
	}

	sample.At = s.sampled
	return sample
}

// GroupsOf resolves the process groups for a set of pids in one lock acquisition.
//
// Callers need the groups for a whole app — a front door plus a dozen services —
// so resolving them one at a time would take one read lock per pid on every sweep.
func (s *Sampler) GroupsOf(pids ...int) []int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[int]bool, len(pids))
	groups := make([]int, 0, 2)
	for _, pid := range pids {
		pgid := s.procs[pid].pgid
		if pgid <= 0 || seen[pgid] {
			continue
		}
		seen[pgid] = true
		groups = append(groups, pgid)
	}
	return groups
}

// Record appends a sample to an app's history.
//
// A repeat of a measurement already recorded is ignored. The caller sweeps on its
// own cadence, which does not divide evenly into the sampling interval, so without
// this the series would contain duplicated points and the sparkline would show
// plateaus that never happened.
func (s *Sampler) Record(key string, sample Sample) {
	if key == "" || sample.At.IsZero() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if last, ok := s.lastPoint[key]; ok && !sample.At.After(last) {
		return
	}
	s.lastPoint[key] = sample.At

	points := append(s.history[key], Point{CPU: round(sample.CPU, 1), RSS: sample.RSS})
	if len(points) > historyLength {
		points = points[len(points)-historyLength:]
	}
	s.history[key] = points
}

// History returns an app's recent samples, oldest first.
func (s *Sampler) History(key string) []Point {
	s.mu.RLock()
	defer s.mu.RUnlock()
	points := s.history[key]
	out := make([]Point, len(points))
	copy(out, points)
	return out
}

// Forget drops history for apps that are no longer present, so a long-running
// daemon does not accumulate series for things that stopped days ago.
func (s *Sampler) Forget(live map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.history {
		if !live[key] {
			delete(s.history, key)
			delete(s.lastPoint, key)
		}
	}
}

// parseCPUTime reads ps's cumulative CPU column: [dd-]hh:mm:ss or mm:ss.ff.
func parseCPUTime(field string) float64 {
	days := 0.0
	if dash := strings.IndexByte(field, '-'); dash > 0 {
		if d, err := strconv.ParseFloat(field[:dash], 64); err == nil {
			days = d
		}
		field = field[dash+1:]
	}

	var values [3]float64
	count := 0
	for field != "" && count < 3 {
		var part string
		if i := strings.IndexByte(field, ':'); i >= 0 {
			part, field = field[:i], field[i+1:]
		} else {
			part, field = field, ""
		}
		v, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0
		}
		values[count] = v
		count++
	}

	switch count {
	case 3:
		return days*86400 + values[0]*3600 + values[1]*60 + values[2]
	case 2:
		return days*86400 + values[0]*60 + values[1]
	case 1:
		return days*86400 + values[0]
	default:
		return 0
	}
}

func round(v float64, places int) float64 {
	factor := 1.0
	for i := 0; i < places; i++ {
		factor *= 10
	}
	return float64(int64(v*factor+0.5)) / factor
}

func output(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var buf strings.Builder
	cmd.Stdout = &buf
	err := cmd.Run()
	return buf.String(), err
}
