package procs

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Member is one process in a group.
type Member struct {
	PID  int    `json:"pid"`
	PGID int    `json:"pgid"`
	Comm string `json:"comm"`
	Cwd  string `json:"cwd,omitempty"`
}

// GroupMembers lists the processes in a process group.
func GroupMembers(ctx context.Context, pgid int) []Member {
	if pgid <= 1 {
		return nil
	}
	out, _ := output(ctx, "/bin/ps", "-Ao", "pid=,pgid=,comm=")

	var members []Member
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		group, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil || group != pgid {
			continue
		}
		members = append(members, Member{PID: pid, PGID: group, Comm: strings.Join(fields[2:], " ")})
	}

	// Fill in working directories in one lsof call.
	if len(members) > 0 {
		pids := make([]string, len(members))
		for i, m := range members {
			pids[i] = strconv.Itoa(m.PID)
		}
		cwds := cwdsOf(ctx, strings.Join(pids, ","))
		for i := range members {
			members[i].Cwd = cwds[members[i].PID]
		}
	}
	return members
}

func cwdsOf(ctx context.Context, pidList string) map[int]string {
	cwds := make(map[int]string)
	out, _ := output(ctx, "/usr/sbin/lsof", "-a", "-d", "cwd", "-p", pidList, "-Fn")

	var cur int
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			cur, _ = strconv.Atoi(line[1:])
		case 'n':
			if cur != 0 {
				cwds[cur] = line[1:]
			}
		}
	}
	return cwds
}

// GroupBelongsTo reports whether every process in a group is working inside dir,
// and names the offender when one isn't.
//
// This is the guard that makes it safe to stop an app Marina did not start.
// Signalling a process group is what Ctrl+C does, and job control normally puts a
// dev server in a group of its own — the shell stays in a different one, which is
// why Ctrl+C doesn't kill your terminal. This verifies that assumption rather than
// trusting it: if anything in the group is working outside the project, the group
// is not exclusively this app's and must not be signalled.
func GroupBelongsTo(ctx context.Context, pgid int, dir string) (bool, string) {
	if pgid <= 1 {
		return false, "invalid process group"
	}
	if dir == "" {
		return false, "no project directory to check against"
	}

	root := filepath.Clean(dir)
	real := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		real = resolved
	}

	members := GroupMembers(ctx, pgid)
	if len(members) == 0 {
		return false, "the process group is empty"
	}

	for _, m := range members {
		// A process with no readable cwd can't be vouched for.
		if m.Cwd == "" {
			return false, "could not read the working directory of pid " + strconv.Itoa(m.PID)
		}
		if within(m.Cwd, root) || within(m.Cwd, real) {
			continue
		}
		return false, "pid " + strconv.Itoa(m.PID) + " (" + firstWord(m.Comm) + ") is working in " +
			m.Cwd + ", outside this project"
	}
	return true, ""
}

func within(path, root string) bool {
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return filepath.Base(s[:i])
	}
	return filepath.Base(s)
}

func output(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var buf strings.Builder
	cmd.Stdout = &buf
	err := cmd.Run()
	return buf.String(), err
}
