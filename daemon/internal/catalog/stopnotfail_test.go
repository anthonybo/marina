package catalog

import (
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A launch that was signalled has not failed. Exit 143 is 128+15 (SIGTERM): a
// stop from the dashboard by port, Ctrl+C in the owning terminal, a plain kill,
// or the machine shutting down. Reporting it as a failure left a working project
// labelled "failed" for a day, because the label persists until the next launch.
func TestSignalExitIsAStopNotAFailure(t *testing.T) {
	for _, tc := range []struct {
		code   int
		signal string
		isStop bool
	}{
		{143, "SIGTERM — a stop, or shutdown", true},
		{130, "SIGINT — Ctrl+C", true},
		{129, "SIGHUP — terminal closed", true},
		{137, "SIGKILL — Marina's escalation, or the OOM killer", true},
		{0, "exited cleanly", false},
		{1, "a real failure", false},
		{127, "command not found", false},
	} {
		if got := signalExit(tc.code); got != tc.isStop {
			t.Errorf("signalExit(%d) = %v, want %v (%s)", tc.code, got, tc.isStop, tc.signal)
		}
	}
}

// The stop-by-port paths signal a process group directly, so they need a way to
// say the exit was intended. Marking must also clear a failure already recorded,
// otherwise a stale label survives the stop that should have cleared it.
func TestMarkStoppedClearsAStaleFailure(t *testing.T) {
	l := &Launcher{recent: map[string]*Launch{
		"/tmp/app": {Path: "/tmp/app", Error: "the command exited with status 143"},
	}}

	if !l.recent["/tmp/app"].Failed() {
		t.Fatal("fixture should start out failed")
	}
	if !l.MarkStopped("/tmp/app") {
		t.Fatal("MarkStopped did not find the record")
	}
	rec := l.recent["/tmp/app"]
	if rec.Failed() {
		t.Fatalf("still reports failed: %q", rec.Error)
	}
	if !rec.Stopped {
		t.Fatal("Stopped was not set")
	}
	if l.MarkStopped("/tmp/never-launched") {
		t.Fatal("MarkStopped claimed a record it does not have")
	}
	if l.MarkStopped("") {
		t.Fatal("MarkStopped accepted an empty path")
	}
}

// Exercises the real watch path: a signalled process group is how Ctrl+C, a
// `kill`, and the dashboard's stop-by-port all end a launch. ExitCode() reports
// -1 for it, so inferring from the number alone marked a working project as
// failed — this test is here because the first fix looked at the number.
func TestWatchTreatsASignalledProcessAsStoppedNotFailed(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")

	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	l := &Launcher{
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		recent: map[string]*Launch{
			dir: {Path: dir, Name: "app", PID: cmd.Process.Pid, At: time.Now()},
		},
	}

	done := make(chan struct{})
	go func() { l.watch(dir, "app", cmd, logPath); close(done) }()

	// Signal the group, exactly as a stop or a Ctrl+C does.
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("watch did not return after the process was signalled")
	}

	rec := l.recent[dir]
	if rec.Failed() {
		t.Fatalf("a signalled launch was recorded as failed: %q (exit code %v)", rec.Error, *rec.ExitCode)
	}
	if !rec.Stopped {
		t.Error("a signalled launch should be marked as stopped")
	}
}
