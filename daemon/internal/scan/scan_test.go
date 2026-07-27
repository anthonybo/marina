package scan

import (
	"testing"
	"time"
)

func TestParseAddr(t *testing.T) {
	tests := []struct {
		addr         string
		wantPort     int
		wantWildcard bool
		wantOK       bool
	}{
		{"*:3000", 3000, true, true},
		{"0.0.0.0:8080", 8080, true, true},
		{"127.0.0.1:5432", 5432, false, true},
		{"[::1]:27017", 27017, false, true},
		{"[::]:9000", 9000, true, true},
		{"*:*", 0, false, false},
		{"garbage", 0, false, false},
		{"127.0.0.1:99999", 0, false, false},
	}

	for _, tc := range tests {
		port, wildcard, ok := parseAddr(tc.addr)
		if ok != tc.wantOK || port != tc.wantPort || wildcard != tc.wantWildcard {
			t.Errorf("parseAddr(%q) = (%d, %v, %v), want (%d, %v, %v)",
				tc.addr, port, wildcard, ok, tc.wantPort, tc.wantWildcard, tc.wantOK)
		}
	}
}

// TestSocketHosts guards the IPv6 case: Vite binds [::1] only, and probing
// 127.0.0.1 would wrongly report that the port speaks no HTTP.
func TestSocketHosts(t *testing.T) {
	tests := []struct {
		name string
		sock Socket
		want []string
	}{
		{"ipv4 only", Socket{V4: true}, []string{"127.0.0.1"}},
		{"ipv6 only", Socket{V6: true}, []string{"[::1]"}},
		{"dual stack", Socket{V4: true, V6: true}, []string{"127.0.0.1", "[::1]"}},
		{"unknown falls back to ipv4", Socket{}, []string{"127.0.0.1"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.sock.Hosts()
			if len(got) != len(tc.want) {
				t.Fatalf("Hosts() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Hosts()[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestUnescape(t *testing.T) {
	tests := []struct{ in, want string }{
		{`OneDrive\x20Sync\x20Service`, "OneDrive Sync Service"},
		{"redis-server", "redis-server"},
		{`/Users/me/My\x20Projects/app`, "/Users/me/My Projects/app"},
		{`trailing\x`, `trailing\x`},
	}
	for _, tc := range tests {
		if got := unescape(tc.in); got != tc.want {
			t.Errorf("unescape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParsePS(t *testing.T) {
	line := "14193 Thu Jul 23 11:24:12 2026 /usr/bin/node --require /app/preflight.cjs src/index.ts"
	pid, proc, ok := parsePS(line)
	if !ok {
		t.Fatal("parsePS returned not-ok for a well-formed line")
	}
	if pid != 14193 {
		t.Errorf("pid = %d, want 14193", pid)
	}
	if proc.Cmd != "/usr/bin/node --require /app/preflight.cjs src/index.ts" {
		t.Errorf("cmd = %q", proc.Cmd)
	}
	want := time.Date(2026, time.July, 23, 11, 24, 12, 0, time.Local)
	if !proc.Started.Equal(want) {
		t.Errorf("started = %v, want %v", proc.Started, want)
	}

	if _, _, ok := parsePS("not a process line"); ok {
		t.Error("parsePS accepted a malformed line")
	}
}

// TestListenersDedupesAcrossFamilies verifies the parser folds a process's IPv4
// and IPv6 listeners on one port into a single entry.
func TestListenersDedupesAcrossFamilies(t *testing.T) {
	// This mirrors real `lsof -Fpcnt` output for Postgres, which listens on both.
	const out = "p4590\ncpostgres\nfcwd\ntIPv6\nn[::1]:5432\nf7\ntIPv4\nn127.0.0.1:5432\n"

	sockets := parseListeners(out)
	if len(sockets) != 1 {
		t.Fatalf("got %d sockets, want 1: %+v", len(sockets), sockets)
	}
	s := sockets[0]
	if s.PID != 4590 || s.Proc != "postgres" || s.Port != 5432 {
		t.Errorf("unexpected socket: %+v", s)
	}
	if !s.V4 || !s.V6 {
		t.Errorf("expected dual-stack, got V4=%v V6=%v", s.V4, s.V6)
	}
}
