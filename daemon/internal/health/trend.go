package health

import (
	"math"
	"sort"
	"strconv"
)

// Trend describes where an app's memory is heading.
//
// This exists because measuring was not enough. Marina recorded an app going
// 0.4 → 0.7 → 0.9 → 1.3 → 1.7 → 1.9 → 2.7 GB over a couple of minutes, faithfully,
// in a sparkline nobody was looking at — and the first anyone knew of it was a
// machine deep in swap with a load average of 298 and a trackpad that had stopped
// responding. The numbers were right and said nothing.
//
// The signal is the *shape*, not the size. A fixed threshold cannot tell a leak
// from a project that is legitimately large: one machine here runs thirteen worker
// processes that are supposed to be heavy and stay heavy, and alarming on them
// would train the alarm out of existence. Memory that climbs and keeps climbing is
// a different thing, and it is the thing that ends in a frozen machine.
type Trend struct {
	// GrowthPerMin is the measured change in resident memory, in bytes per minute.
	// Negative means it is giving memory back.
	GrowthPerMin int64 `json:"growthPerMin"`
	// Climbing is true when memory has risen consistently across the window, not
	// merely ended higher than it started.
	Climbing bool `json:"climbing"`
	// Severity is 0 when nothing is wrong and approaches 1 for an app that is about
	// to take the machine with it. The UI reads this directly rather than
	// re-deriving thresholds of its own.
	Severity float64 `json:"severity"`
	// Why states the case in a few words, for a tooltip. Empty when Severity is 0.
	Why string `json:"why,omitempty"`
}

const (
	// trendWindow is how many recent samples the shape is read from. At a three
	// second cadence this is about a minute.
	//
	// It was half that, until a live probe leaking a steady 250 MB/min flipped
	// between "climbing" and "steady" every few seconds. The cause was real and is
	// not going away: macOS reclaims pages in chunks, so a genuinely leaking process
	// shows sudden 40–50 MB drops on its way up. Over thirty seconds one of those
	// drops is most of the window's total rise and inverts the verdict. Over a
	// minute it cannot. An alarm that blinks is worse than no alarm — you learn to
	// disbelieve it.
	trendWindow = 20

	// minSamples is the least evidence worth judging, so an app is assessed after
	// half a minute rather than waiting for the full window to fill. A dev server
	// allocates hard for its first seconds and judging that would fire on every
	// single launch, which is why this is not lower.
	minSamples = 10

	// minGrowthPerMin is the slowest climb this window can actually resolve.
	//
	// It is set by minRise below, not by taste: half a minute of samples has to
	// contain a rise big enough to be distinguishable from jitter, and 48 MB over
	// 30 s *is* 96 MB/min. Naming a gentler figure here would be a lie — a 25 MB/min
	// leak cannot be told apart from noise over thirty seconds, and the rise gate
	// would silently reject it anyway. 100 MB/min is still 6 GB in an hour.
	minGrowthPerMin = 100 << 20 // 100 MB/min

	// minRise is how much it must have gained across the window regardless of the
	// fitted slope, so a flat app with one noisy sample cannot look like a leak.
	// This is a noise floor and nothing more — deciding whether the growth *matters*
	// is minGrowthPerMin's job. Set at 120 MB it quietly did both jobs, holding the
	// real threshold at 240 MB/min while the constant above claimed 25.
	minRise = 48 << 20 // 48 MB

	// consistency is the share of sample-to-sample changes that must be upward.
	// A leak climbs most of the time; an app doing work rises and falls.
	consistency = 0.6

	// runawayGrowth is a climb fast enough to be the whole story on its own.
	runawayGrowth = 400 << 20 // 400 MB/min

	// heavyRSS is where an app is worth noticing even if it is not growing —
	// stable, but large enough that the machine feels it.
	heavyRSS = 3 << 30 // 3 GB
)

// analyse reads the shape of an app's recent memory use.
//
// history is oldest-first. interval is the sampling cadence, needed to turn a
// per-sample rate into something a person can reason about.
func analyse(history []Point, interval float64) Trend {
	if interval <= 0 {
		return Trend{}
	}
	current := int64(0)
	if len(history) > 0 {
		current = history[len(history)-1].RSS
	}

	window := history
	if len(window) > trendWindow {
		window = window[len(window)-trendWindow:]
	}

	// Not enough to judge a shape. Deliberately silent rather than guessing.
	if len(window) < minSamples {
		return heavyOnly(current)
	}

	rise, perSample := riseAcross(window)
	growthPerMin := int64(perSample * (60.0 / interval))

	up := 0
	for i := 1; i < len(window); i++ {
		if window[i].RSS > window[i-1].RSS {
			up++
		}
	}
	climbing := growthPerMin >= minGrowthPerMin &&
		rise >= minRise &&
		float64(up)/float64(len(window)-1) >= consistency

	if !climbing {
		t := heavyOnly(current)
		t.GrowthPerMin = growthPerMin
		return t
	}

	// A climb is already trouble; how much depends on how fast, and on whether the
	// app is big enough for that climb to matter soon.
	severity := 0.5 + 0.5*clamp(float64(growthPerMin)/float64(runawayGrowth))
	if bySize := sizeSeverity(current); bySize > severity {
		severity = bySize
	}

	return Trend{
		GrowthPerMin: growthPerMin,
		Climbing:     true,
		Severity:     clamp(severity),
		Why:          "memory climbing " + humanBytes(growthPerMin) + "/min",
	}
}

// heavyOnly judges an app that is not climbing purely on its footprint.
func heavyOnly(rss int64) Trend {
	if rss < heavyRSS {
		return Trend{}
	}
	return Trend{
		Severity: clamp(sizeSeverity(rss)),
		Why:      "holding " + humanBytes(rss),
	}
}

// sizeSeverity maps a footprint to concern, reaching the top of its range at twice
// the heavy mark. Capped below 1 on its own: a large, stable app is a fact about
// the machine, not an emergency, and only a climb earns the top of the scale.
func sizeSeverity(rss int64) float64 {
	if rss < heavyRSS {
		return 0
	}
	over := float64(rss-heavyRSS) / float64(heavyRSS)
	return clamp(0.35 + 0.45*over)
}

// riseAcross measures how much the window climbed, and how fast per sample, by
// comparing the median of its first third against the median of its last third.
//
// Medians rather than a least-squares fit, and thirds rather than first-versus-
// last, because the thing being measured is not clean. A leaking process on macOS
// climbs in a staircase with sudden 40–50 MB drops where the OS reclaimed pages,
// and a straight-line fit weights those drops by how far they sit from the centre
// of the window: the same leak read as +250 MB/min or −107 MB/min depending on
// where the last drop happened to land. Two medians cannot be moved by one dip.
//
// Returns the total rise between those two points and the rise per sample between
// their centres, which are (n-k) samples apart.
func riseAcross(window []Point) (rise int64, perSample float64) {
	n := len(window)
	k := n / 3
	if k < 1 {
		k = 1
	}
	early := medianRSS(window[:k])
	late := medianRSS(window[n-k:])
	rise = late - early
	return rise, float64(rise) / float64(n-k)
}

func medianRSS(window []Point) int64 {
	values := make([]int64, len(window))
	for i, p := range window {
		values[i] = p.RSS
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// humanBytes formats for a sentence rather than a table: two significant figures
// is all anyone reads in a warning.
func humanBytes(b int64) string {
	sign := ""
	if b < 0 {
		sign, b = "-", -b
	}
	switch {
	case b >= 1<<30:
		return sign + trim(float64(b)/(1<<30)) + " GB"
	case b >= 1<<20:
		return sign + trim(float64(b)/(1<<20)) + " MB"
	default:
		return sign + trim(float64(b)/(1<<10)) + " KB"
	}
}

func trim(v float64) string {
	if v >= 10 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}
