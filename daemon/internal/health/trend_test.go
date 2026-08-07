package health

import "testing"

func points(mb ...int64) []Point {
	out := make([]Point, 0, len(mb))
	for _, m := range mb {
		out = append(out, Point{RSS: m << 20})
	}
	return out
}

// The curve that started this: an app Marina watched climb from 0.4 GB to 2.9 GB
// while saying nothing, ending in a machine at load 298 with 5.5 GB of swap.
func TestTheLeakThatFrozeTheMachine(t *testing.T) {
	// Real figures, taken from /api/health while it was happening.
	history := points(420, 713, 937, 1336, 1717, 1985, 2775, 2625, 2626, 2570)

	got := analyse(history, 3)
	if !got.Climbing {
		t.Fatalf("did not read as climbing: %+v", got)
	}
	if got.Severity < 0.7 {
		t.Errorf("severity %.2f is too mild for %s", got.Severity, got.Why)
	}
	t.Logf("growth=%s/min severity=%.2f why=%q", humanBytes(got.GrowthPerMin), got.Severity, got.Why)
}

// The case that makes a size threshold useless: thirteen worker processes that are
// supposed to be heavy and stay heavy. Alarming here would train the alarm out of
// existence, which is the real failure mode of monitoring.
func TestBigButSteadyIsNotAnAlarm(t *testing.T) {
	history := points(2400, 2415, 2390, 2410, 2405, 2398, 2412, 2401, 2396, 2408)

	got := analyse(history, 3)
	if got.Climbing {
		t.Errorf("a steady app was called a leak: %+v", got)
	}
	if got.Severity != 0 {
		t.Errorf("severity %.2f for a steady 2.4 GB app; want 0", got.Severity)
	}
}

// An app doing real work rises and falls. Only a sustained climb counts.
func TestBusyButNotLeakingIsNotAnAlarm(t *testing.T) {
	history := points(800, 1200, 700, 1400, 900, 1300, 750, 1250, 850, 1150)

	if got := analyse(history, 3); got.Climbing {
		t.Errorf("a sawtooth was called a leak: %+v", got)
	}
}

// A dev server allocates hard for its first seconds. Judging that would fire on
// every launch, so a short history says nothing.
func TestAFreshStartIsNotJudged(t *testing.T) {
	history := points(60, 220, 480, 900)

	got := analyse(history, 3)
	if got.Climbing || got.Severity != 0 {
		t.Errorf("judged an app with four samples: %+v", got)
	}
}

// Stable but genuinely enormous still deserves a mention — quieter than a leak.
func TestStableAndHugeIsNotedButNotUrgent(t *testing.T) {
	history := points(4600, 4610, 4590, 4605, 4600, 4595, 4608, 4602, 4598, 4604)

	got := analyse(history, 3)
	if got.Climbing {
		t.Errorf("called it a leak: %+v", got)
	}
	if got.Severity == 0 {
		t.Errorf("4.6 GB went unmentioned entirely")
	}
	if got.Severity > 0.85 {
		t.Errorf("severity %.2f — a stable app should rank below a runaway one", got.Severity)
	}
	t.Logf("severity=%.2f why=%q", got.Severity, got.Why)
}

// Memory coming back down is the opposite of a problem.
func TestFallingMemoryIsNeverAnAlarm(t *testing.T) {
	history := points(3000, 2800, 2600, 2400, 2200, 2000, 1800, 1600, 1400, 1200)

	got := analyse(history, 3)
	if got.Climbing || got.Severity != 0 {
		t.Errorf("an app releasing memory raised an alarm: %+v", got)
	}
	if got.GrowthPerMin >= 0 {
		t.Errorf("growth %d should be negative", got.GrowthPerMin)
	}
}

// A steady, unspectacular leak — the kind that takes an afternoon rather than two
// minutes. Caught live against a probe growing 12 MB every three seconds: the
// fitted slope was a clean 252 MB/min and the verdict was still "not climbing",
// because minRise was set high enough to act as a second, much stricter rate gate.
// The window is thirty seconds, so a 120 MB rise silently meant 240 MB/min while
// minGrowthPerMin advertised 25.
func TestASlowLeakIsStillALeak(t *testing.T) {
	// 12 MB per three-second sample, from a real probe.
	history := points(150, 162, 174, 186, 198, 210, 222, 234, 246, 258)

	got := analyse(history, 3)
	if !got.Climbing {
		t.Fatalf("252 MB/min read as steady: %+v", got)
	}
	if got.Severity < 0.6 {
		t.Errorf("severity %.2f would not show as distress: %s", got.Severity, got.Why)
	}
	t.Logf("growth=%s/min severity=%.2f why=%q", humanBytes(got.GrowthPerMin), got.Severity, got.Why)
}

// The other side of that threshold: jitter around a flat line must not become a
// leak just because the rise gate was loosened. A few MB of sample-to-sample noise
// is normal for any process with a heap.
func TestJitterAroundFlatIsNotALeak(t *testing.T) {
	history := points(800, 803, 799, 806, 801, 804, 798, 807, 802, 805)

	got := analyse(history, 3)
	if got.Climbing {
		t.Errorf("noise read as a leak: %+v", got)
	}
}

// The shape a real leak has on macOS, taken from the live probe: a staircase up
// with sudden ~47 MB drops where the OS reclaimed pages. A least-squares fit over
// half a minute called this same process +250 MB/min or −107 MB/min depending on
// where the most recent drop landed in the window, so the boat flickered between
// sinking and fine every few seconds.
func TestPageReclaimDipsDoNotFlipTheVerdict(t *testing.T) {
	history := points(
		660, 673, 685, 698, 710, 723, 735, 748, 760, 773,
		785, 798, 811, 823, 836, 848, 861, 873, 886, 899,
	)
	// The same climb with two reclaim drops cut into it.
	dipped := points(
		660, 673, 685, 638, 650, 663, 675, 688, 700, 713,
		725, 738, 691, 703, 716, 728, 741, 753, 766, 779,
	)

	clean := analyse(history, 3)
	if !clean.Climbing {
		t.Fatalf("clean staircase not read as climbing: %+v", clean)
	}
	got := analyse(dipped, 3)
	if !got.Climbing {
		t.Errorf("two page-reclaim dips hid a real leak: %+v", got)
	}
	t.Logf("clean=%s/min dipped=%s/min", humanBytes(clean.GrowthPerMin), humanBytes(got.GrowthPerMin))
}
