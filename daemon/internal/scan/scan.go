// Package scan enumerates listening TCP sockets and the processes behind them.
//
// Everything here shells out to lsof(8) and ps(1) with absolute paths, because
// the daemon runs under launchd where PATH is minimal. A full listener sweep
// costs ~100ms, which is why Marina can afford to poll every couple of seconds.
package scan

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	lsofBin = "/usr/sbin/lsof"
	psBin   = "/bin/ps"
)

// Socket is one listening TCP endpoint owned by a local process.
type Socket struct {
	PID  int
	Proc string
	Port int
	// Wildcard reports whether the socket is bound to all interfaces rather
	// than loopback only. Both are reachable via 127.0.0.1; this only tells us
	// whether the service is also exposed on the LAN.
	Wildcard bool
	// V4 and V6 record which address families the process is listening on.
	// This matters more than it sounds: Vite binds IPv6-only by default, so
	// probing 127.0.0.1 would wrongly conclude the port speaks no HTTP.
	V4 bool
	V6 bool
}

// Hosts returns the loopback addresses worth trying for this socket, in order.
func (s Socket) Hosts() []string {
	switch {
	case s.V4 && s.V6:
		return []string{"127.0.0.1", "[::1]"}
	case s.V6:
		return []string{"[::1]"}
	default:
		return []string{"127.0.0.1"}
	}
}

// Proc carries the extra per-process detail needed to identify a service.
type Proc struct {
	Cmd     string
	Cwd     string
	Started time.Time
}

// Listeners returns every listening TCP socket on the machine, deduplicated to
// one entry per (pid, port). A process listening on both IPv4 and IPv6 shows up
// once, with Wildcard set if any of its binds covered all interfaces.
func Listeners(ctx context.Context) ([]Socket, error) {
	// +c 0 defeats lsof's 9-character command-name truncation, which would
	// otherwise report "redis-ser" and "ControlCe".
	out, err := output(ctx, lsofBin, "+c", "0", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcnt")
	if err != nil && out == "" {
		return nil, fmt.Errorf("lsof listeners: %w", err)
	}
	return parseListeners(out), nil
}

// parseListeners turns lsof field output into sockets. Split out from Listeners
// so the parsing rules can be tested against captured real-world output.
func parseListeners(out string) []Socket {
	var (
		byKey     = make(map[string]*Socket)
		order     []string
		curPID    int
		curProc   string
		curIsIPv6 bool
	)

	s := bufio.NewScanner(strings.NewReader(out))
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for s.Scan() {
		line := s.Text()
		if len(line) < 2 {
			continue
		}
		field, val := line[0], line[1:]

		switch field {
		case 'p':
			curPID, _ = strconv.Atoi(val)
			curProc = ""
		case 'c':
			curProc = unescape(val)
		case 't':
			curIsIPv6 = val == "IPv6"
		case 'n':
			if curPID == 0 {
				continue
			}
			port, wildcard, ok := parseAddr(val)
			if !ok {
				continue
			}
			key := strconv.Itoa(curPID) + ":" + strconv.Itoa(port)
			if existing, seen := byKey[key]; seen {
				existing.Wildcard = existing.Wildcard || wildcard
				existing.V4 = existing.V4 || !curIsIPv6
				existing.V6 = existing.V6 || curIsIPv6
				continue
			}
			byKey[key] = &Socket{
				PID: curPID, Proc: curProc, Port: port, Wildcard: wildcard,
				V4: !curIsIPv6, V6: curIsIPv6,
			}
			order = append(order, key)
		}
	}

	sockets := make([]Socket, 0, len(order))
	for _, key := range order {
		sockets = append(sockets, *byKey[key])
	}
	return sockets
}

// Procs resolves the working directory, full command line, and start time for
// the given PIDs. It issues exactly two subprocess calls regardless of how many
// PIDs are requested. PIDs that have already exited are simply absent from the
// result rather than being treated as an error.
func Procs(ctx context.Context, pids []int) (map[int]Proc, error) {
	procs := make(map[int]Proc, len(pids))
	if len(pids) == 0 {
		return procs, nil
	}
	list := joinInts(pids)

	// ps carries the full argv and the start time. A non-zero exit here just
	// means some PIDs vanished between the lsof sweep and now.
	if out, err := output(ctx, psBin, "-o", "pid=,lstart=,command=", "-p", list); err == nil || out != "" {
		for _, line := range strings.Split(out, "\n") {
			pid, p, ok := parsePS(line)
			if !ok {
				continue
			}
			procs[pid] = p
		}
	}

	// lsof supplies the cwd, which is the single most useful signal for
	// mapping a port back to a project directory.
	if out, err := output(ctx, lsofBin, "-a", "-d", "cwd", "-p", list, "-Fn"); err == nil || out != "" {
		var curPID int
		for _, line := range strings.Split(out, "\n") {
			if len(line) < 2 {
				continue
			}
			switch line[0] {
			case 'p':
				curPID, _ = strconv.Atoi(line[1:])
			case 'n':
				if curPID == 0 {
					continue
				}
				p := procs[curPID]
				p.Cwd = unescape(line[1:])
				procs[curPID] = p
			}
		}
	}

	return procs, nil
}

// Alive reports which of the given PIDs still exist, in one ps call.
func Alive(ctx context.Context, pids []int) map[int]bool {
	alive := make(map[int]bool, len(pids))
	if len(pids) == 0 {
		return alive
	}
	out, _ := output(ctx, psBin, "-o", "pid=", "-p", joinInts(pids))
	for _, line := range strings.Split(out, "\n") {
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			alive[pid] = true
		}
	}
	return alive
}

// parsePS pulls apart one `pid lstart command` row. lstart is a fixed-width
// 24-character ctime-style stamp, so the command begins at a known offset.
func parsePS(line string) (int, Proc, bool) {
	line = strings.TrimLeft(line, " ")
	sp := strings.IndexByte(line, ' ')
	if sp < 0 {
		return 0, Proc{}, false
	}
	pid, err := strconv.Atoi(line[:sp])
	if err != nil {
		return 0, Proc{}, false
	}
	rest := strings.TrimLeft(line[sp:], " ")
	if len(rest) < 24 {
		return 0, Proc{}, false
	}
	stamp, cmd := rest[:24], strings.TrimSpace(rest[24:])

	started, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.TrimSpace(stamp), time.Local)
	if err != nil {
		// Keep the command even if the timestamp is unparseable; uptime is a
		// nicety, identity is not.
		return pid, Proc{Cmd: cmd}, true
	}
	return pid, Proc{Cmd: cmd, Started: started}, true
}

// parseAddr splits an lsof endpoint such as "*:3000", "127.0.0.1:5432", or
// "[::1]:27017" into its port and whether the bind covers all interfaces.
func parseAddr(addr string) (port int, wildcard bool, ok bool) {
	// Ignore any "->" peer suffix; listeners shouldn't have one, but be safe.
	if i := strings.Index(addr, "->"); i >= 0 {
		addr = addr[:i]
	}
	i := strings.LastIndexByte(addr, ':')
	if i < 0 {
		return 0, false, false
	}
	host, portStr := addr[:i], addr[i+1:]
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return 0, false, false
	}
	switch host {
	case "*", "0.0.0.0", "[::]", "::":
		wildcard = true
	}
	return port, wildcard, true
}

// unescape reverses lsof's \xNN escaping, which it applies to spaces and other
// non-printable bytes in command names and paths.
func unescape(s string) string {
	if !strings.Contains(s, `\x`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 4
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func joinInts(vals []int) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

// output runs a command and returns stdout, tolerating partial output from a
// non-zero exit (lsof and ps both exit non-zero when some PIDs have vanished).
func output(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	err := cmd.Run()
	return stdout.String(), err
}
