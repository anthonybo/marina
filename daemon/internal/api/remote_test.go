package api

import "testing"

// Marina can start and stop processes, so once it listens on a LAN address the
// only thing standing between a device on the Wi-Fi and your dev servers is this
// check. There is no authentication here that could tell devices apart.
func TestOnlyThisMachineMayMutate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		addr  string
		allow bool
	}{
		{"this machine, IPv4", "127.0.0.1:54321", true},
		{"this machine, IPv6", "[::1]:54321", true},
		{"another loopback address", "127.0.0.2:9", true},
		{"a phone on the Wi-Fi", "192.168.0.42:51234", false},
		{"a wired machine", "10.0.1.20:443", false},
		{"something on the internet", "203.0.113.9:80", false},
		{"a hostname instead of an address", "marina.local:7777", false},
		{"garbage", "not-an-address", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLoopbackClient(tc.addr); got != tc.allow {
				t.Fatalf("isLoopbackClient(%q) = %v, want %v", tc.addr, got, tc.allow)
			}
		})
	}
}
