// Package procs stops local processes.
//
// This is the only place in Marina that signals anything, and it is deliberately
// small. Callers decide *what* may be stopped — the guards live with the code
// that understands what a process is — and this package only handles *how*:
// ask politely, wait, and escalate only if ignored, which is what Ctrl+C in a
// terminal does.
package procs

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"
)

// Options controls how a stop is carried out.
type Options struct {
	// Group signals the whole process group rather than one process.
	//
	// Only safe when Marina created that group. A dev server started by
	// `pnpm run dev` is a tree — shell, concurrently, then a dozen workers — and
	// signalling the group is the only way to stop all of it. But a process started
	// in the user's own terminal shares its group with that terminal's shell, so
	// signalling the group there could take the terminal down with it. Never set
	// this for a process Marina did not start.
	Group bool
	// Grace is how long to wait for a clean exit before escalating to SIGKILL.
	Grace time.Duration
}

// Result reports what happened.
type Result struct {
	PID int `json:"pid"`
	// Group is the process group signalled, when Options.Group was set.
	Group int `json:"group,omitempty"`
	// Exited is true if the process was gone by the end.
	Exited bool `json:"exited"`
	// Forced is true if SIGTERM was ignored and SIGKILL was needed.
	Forced bool   `json:"forced"`
	Error  string `json:"error,omitempty"`
}

// ErrNoSuchProcess is returned when the target is already gone.
var ErrNoSuchProcess = errors.New("procs: no such process")

const defaultGrace = 8 * time.Second

// Alive reports whether a PID exists. Signal 0 checks for existence without
// delivering anything.
func Alive(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	// EPERM means it exists but belongs to someone else.
	return errors.Is(err, syscall.EPERM)
}

// Terminate stops a process, or its group, waiting for it to go.
//
// PIDs at or below 1 are refused outright: 0 means "every process in my group"
// and negative values mean a process group, so a bad caller must not be able to
// turn a stop request into a machine-wide one.
func Terminate(ctx context.Context, pid int, opts Options) Result {
	result := Result{PID: pid}

	if pid <= 1 {
		result.Error = fmt.Sprintf("procs: refusing to signal pid %d", pid)
		return result
	}
	if !Alive(pid) {
		result.Exited = true
		result.Error = ErrNoSuchProcess.Error()
		return result
	}

	grace := opts.Grace
	if grace <= 0 {
		grace = defaultGrace
	}

	// Resolve the target: a negative pid signals the whole group.
	target := pid
	if opts.Group {
		pgid, err := syscall.Getpgid(pid)
		if err != nil {
			result.Error = fmt.Sprintf("procs: could not read the process group: %v", err)
			return result
		}
		// A group of 1 or 0 would be catastrophic to signal.
		if pgid <= 1 {
			result.Error = fmt.Sprintf("procs: refusing to signal process group %d", pgid)
			return result
		}
		result.Group = pgid
		target = -pgid
	}

	if err := syscall.Kill(target, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		result.Error = fmt.Sprintf("procs: SIGTERM: %v", err)
		return result
	}

	if waitGone(ctx, pid, grace) {
		result.Exited = true
		return result
	}

	// It ignored the request. Escalate — a dev server that won't stop on SIGTERM
	// still has to stop when you ask it to.
	result.Forced = true
	if err := syscall.Kill(target, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		result.Error = fmt.Sprintf("procs: SIGKILL: %v", err)
		return result
	}
	result.Exited = waitGone(ctx, pid, 3*time.Second)
	if !result.Exited && result.Error == "" {
		result.Error = "procs: the process did not exit"
	}
	return result
}

// TerminateGroup stops a process group by its recorded id, falling back to the
// leader's pid if no group was recorded.
//
// Taking the group id as an argument rather than deriving it matters: once the
// group leader has been reaped, Getpgid can no longer tell you what the group was,
// but the surviving children are still in it.
func TerminateGroup(ctx context.Context, pgid, leaderPID int) Result {
	if pgid <= 1 {
		return Terminate(ctx, leaderPID, Options{Group: true})
	}

	result := Result{PID: leaderPID, Group: pgid}
	target := -pgid

	if err := syscall.Kill(target, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			result.Exited = true
			return result
		}
		result.Error = fmt.Sprintf("procs: SIGTERM group %d: %v", pgid, err)
		return result
	}

	if waitGroupGone(ctx, pgid, defaultGrace) {
		result.Exited = true
		return result
	}

	result.Forced = true
	if err := syscall.Kill(target, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		result.Error = fmt.Sprintf("procs: SIGKILL group %d: %v", pgid, err)
		return result
	}
	result.Exited = waitGroupGone(ctx, pgid, 3*time.Second)
	if !result.Exited && result.Error == "" {
		result.Error = "procs: some processes did not exit"
	}
	return result
}

// GroupAlive reports whether any process remains in a group.
func GroupAlive(pgid int) bool {
	if pgid <= 1 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func waitGroupGone(ctx context.Context, pgid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !GroupAlive(pgid) {
			return true
		}
		select {
		case <-ctx.Done():
			return !GroupAlive(pgid)
		case <-time.After(100 * time.Millisecond):
		}
	}
	return !GroupAlive(pgid)
}

func waitGone(ctx context.Context, pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !Alive(pid) {
			return true
		}
		select {
		case <-ctx.Done():
			return !Alive(pid)
		case <-time.After(100 * time.Millisecond):
		}
	}
	return !Alive(pid)
}
