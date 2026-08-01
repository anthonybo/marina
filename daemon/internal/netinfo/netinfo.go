// Package netinfo answers "what address do other machines on this network use to
// reach me?".
//
// The point is a dev server you started here and want to open on a phone, a
// tablet, or another laptop. 127.0.0.1 is useless for that, and the LAN address
// changes whenever the router hands out a new lease — which is the whole reason
// this exists rather than being typed once into a config.
//
// It also reports the mDNS hostname, which is the better answer to the same
// question: a .local name keeps working across a DHCP change, so a bookmark made
// with it does not rot.
package netinfo

import (
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Addr is one address this machine answers on.
type Addr struct {
	IP string `json:"ip"`
	// Iface is the interface it belongs to: en0 is Wi-Fi on most Macs, en1 or a
	// bridge is usually wired.
	Iface string `json:"iface"`
}

// Info is how to reach this machine from elsewhere on the network.
type Info struct {
	// IP is the address to hand out. Empty when there is no usable network, which
	// must be shown as such rather than as a stale address someone might try.
	IP string `json:"ip,omitempty"`
	// Iface is which interface IP belongs to.
	Iface string `json:"iface,omitempty"`
	// Host is the mDNS name, which survives a DHCP change. Empty if the machine
	// has no .local name.
	Host string `json:"host,omitempty"`
	// Others are the remaining addresses, for a machine on Wi-Fi and Ethernet at
	// once. Ordered the same way as IP was chosen.
	Others []Addr `json:"others,omitempty"`
}

// Watcher caches the answer, because the sweep asks on every tick and the answer
// changes about as often as you move house.
//
// Caching is not only about the cost. Finding the primary address dials a UDP
// socket, and although nothing is sent, connect() on a sick network can block; a
// sweep must never wait on that.
type Watcher struct {
	ttl time.Duration

	mu   sync.Mutex
	info Info
	at   time.Time
}

func NewWatcher(ttl time.Duration) *Watcher {
	return &Watcher{ttl: ttl}
}

// Info returns the cached answer, refreshing it when stale.
func (w *Watcher) Info() Info {
	w.mu.Lock()
	if !w.at.IsZero() && time.Since(w.at) < w.ttl {
		info := w.info
		w.mu.Unlock()
		return info
	}
	w.mu.Unlock()

	// Computed outside the lock: a reader during a refresh should get the previous
	// answer rather than wait for a syscall.
	fresh := Lookup()

	w.mu.Lock()
	w.info, w.at = fresh, time.Now()
	w.mu.Unlock()
	return fresh
}

// Lookup reads the current addresses. Prefer a Watcher on any hot path.
func Lookup() Info {
	info := Info{Host: hostname()}

	addrs := usableAddrs()
	if len(addrs) == 0 {
		return info
	}
	sortAddrs(addrs, preferredIP())

	info.IP, info.Iface = addrs[0].IP, addrs[0].Iface
	if len(addrs) > 1 {
		info.Others = addrs[1:]
	}
	return info
}

// hostname returns the machine's mDNS name, or "" if it has none.
//
// os.Hostname is a syscall rather than a subprocess, which matters: Marina's
// whole external-command surface is three programs, and reading a hostname is not
// worth becoming the fourth.
func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	// Only a .local name is useful here — that is the one other machines on the
	// network can resolve. A bare or domain-qualified name is not.
	if !strings.HasSuffix(h, ".local") {
		return ""
	}
	return h
}

// usableAddrs lists the IPv4 addresses another machine on this network could
// actually connect to.
func usableAddrs() []Addr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Addr
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if !usable(iface.Flags, ipnet.IP) {
				continue
			}
			out = append(out, Addr{IP: ipnet.IP.String(), Iface: iface.Name})
		}
	}
	return out
}

// usable reports whether an address is one to hand out to someone else.
func usable(flags net.Flags, ip net.IP) bool {
	if flags&net.FlagUp == 0 || flags&net.FlagLoopback != 0 {
		return false
	}
	// Point-to-point means a tunnel — a VPN's utun. Its address is real, but it is
	// not how anyone on this network reaches this machine, which is the question.
	if flags&net.FlagPointToPoint != 0 {
		return false
	}
	v4 := ip.To4()
	if v4 == nil {
		// IPv6 is deliberately out of scope: the point is an address you can read
		// off the screen and type into a phone.
		return false
	}
	// 169.254.x.x means DHCP failed. Offering it would look like an answer.
	return !v4.IsLinkLocalUnicast() && !v4.IsUnspecified()
}

// routeProbe is the address the route lookup below asks about.
//
// 192.0.2.1 is RFC 5737 TEST-NET-1: reserved for documentation and guaranteed
// never to be assigned to anyone. Nothing is ever sent to it, but the usual
// choice for this trick is a public DNS resolver, and hard-coding somebody else's
// server — even as a routing question that never leaves the machine — is not
// something this should ship with.
const routeProbe = "192.0.2.1:80"

// preferredIP asks the routing table which address outbound traffic would use, by
// dialing a UDP socket nowhere. No packet is sent — connect() on a datagram
// socket only fixes the local end — so this is a routing lookup, not traffic. It
// works with no internet at all, as long as a default route exists.
//
// Returns "" when there is no route, which is the normal answer when offline.
func preferredIP() string {
	// A short timeout because this sits behind a cache with a TTL, and a slow
	// answer is worth less than a fast miss.
	conn, err := net.DialTimeout("udp4", routeProbe, 300*time.Millisecond)
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return addr.IP.String()
}

// sortAddrs puts the address to show first.
//
// The routing table's choice wins when it is one of ours — that is the real
// answer for a machine on both Wi-Fi and Ethernet. It is not always ours: with a
// full-tunnel VPN the route points at a utun address, which no one on this
// network can reach, so the fallback ordering has to stand on its own.
func sortAddrs(addrs []Addr, preferred string) {
	sort.SliceStable(addrs, func(i, j int) bool {
		if (addrs[i].IP == preferred) != (addrs[j].IP == preferred) {
			return addrs[i].IP == preferred
		}
		if r := ifaceRank(addrs[i].Iface) - ifaceRank(addrs[j].Iface); r != 0 {
			return r < 0
		}
		return addrs[i].Iface < addrs[j].Iface
	})
}

// ifaceRank orders interfaces by how likely they are to be the one you mean.
func ifaceRank(name string) int {
	switch {
	case strings.HasPrefix(name, "en"):
		return 0 // Wi-Fi and wired Ethernet
	case strings.HasPrefix(name, "bridge"):
		return 1 // Thunderbolt bridge, internet sharing
	default:
		return 2 // anything else: docker, vmnet, awdl
	}
}
