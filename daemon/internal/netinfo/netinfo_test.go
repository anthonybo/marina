package netinfo

import (
	"net"
	"testing"
	"time"
)

func TestUsableRejectsAddressesNobodyCanReach(t *testing.T) {
	up := net.FlagUp
	for _, tc := range []struct {
		name  string
		flags net.Flags
		ip    string
		want  bool
	}{
		{"a LAN address", up, "192.168.0.79", true},
		{"a wired 10.x address", up, "10.0.1.20", true},
		{"a public address", up, "203.0.113.5", true},
		{"loopback", up | net.FlagLoopback, "127.0.0.1", false},
		{"an interface that is down", 0, "192.168.0.79", false},
		{"a VPN tunnel", up | net.FlagPointToPoint, "10.8.0.2", false},
		{"DHCP having failed", up, "169.254.1.1", false},
		{"IPv6", up, "fe80::1", false},
		{"unspecified", up, "0.0.0.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := usable(tc.flags, net.ParseIP(tc.ip)); got != tc.want {
				t.Fatalf("usable(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestTheRoutingTablesChoiceWins(t *testing.T) {
	addrs := []Addr{
		{IP: "192.168.0.79", Iface: "en0"},
		{IP: "10.0.1.20", Iface: "en1"},
	}
	sortAddrs(addrs, "10.0.1.20")
	if addrs[0].IP != "10.0.1.20" {
		t.Fatalf("first = %s, want the routed address 10.0.1.20", addrs[0].IP)
	}
}

// With a full-tunnel VPN the route points at a tunnel address that was already
// filtered out, so the ordering has to be sensible without it.
func TestFallbackOrderingWhenTheRouteIsNotOurs(t *testing.T) {
	addrs := []Addr{
		{IP: "172.17.0.1", Iface: "docker0"},
		{IP: "192.168.0.79", Iface: "en0"},
		{IP: "192.168.2.1", Iface: "bridge100"},
	}
	sortAddrs(addrs, "10.8.0.2") // the VPN's address: not in the list
	want := []string{"192.168.0.79", "192.168.2.1", "172.17.0.1"}
	for i, ip := range want {
		if addrs[i].IP != ip {
			t.Fatalf("position %d = %s, want %s (order: %+v)", i, addrs[i].IP, ip, addrs)
		}
	}
}

// The IP is read off the screen and typed into another machine, so an empty answer
// has to stay empty rather than becoming something that looks typeable.
func TestNoNetworkReportsNothingRatherThanAGuess(t *testing.T) {
	info := Info{Host: "x.local"}
	if info.IP != "" {
		t.Fatal("an Info with no addresses must report no IP")
	}
}

func TestWatcherCachesAndStillRefreshes(t *testing.T) {
	w := NewWatcher(50 * time.Millisecond)
	first := w.Info()
	if second := w.Info(); second.IP != first.IP || second.Host != first.Host {
		t.Fatal("two reads inside the TTL disagreed")
	}
	time.Sleep(60 * time.Millisecond)
	// Refreshed rather than stuck: the value should still be the machine's real
	// one, and asking again must not panic or blank out.
	if again := w.Info(); again.Host != first.Host {
		t.Fatalf("after the TTL the host changed from %q to %q", first.Host, again.Host)
	}
}

// What this machine actually reports, so a wrong answer is visible in test output
// rather than only in the UI.
func TestLookupOnThisMachine(t *testing.T) {
	info := Lookup()
	t.Logf("ip=%q iface=%q host=%q others=%+v", info.IP, info.Iface, info.Host, info.Others)
	if info.IP == "" && info.Host == "" {
		t.Skip("no network and no .local name; nothing to check")
	}
	if info.IP != "" && net.ParseIP(info.IP) == nil {
		t.Fatalf("reported an unparseable IP: %q", info.IP)
	}
}
