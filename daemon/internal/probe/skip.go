package probe

import (
	"strconv"
	"strings"
)

// PortSet is a set of ports, parsed from a comma-separated list of individual
// ports and ranges: "3001-3013,9229".
//
// This exists so a port can be excluded from HTTP probing entirely. Marina's
// probe is one GET / per five minutes, which is not enough to trouble a healthy
// server — but an app that throws an unhandled exception on an unexpected request
// will exit on it, and that is the app's business, not something Marina should
// force. Excluding a port costs only the page title and the Open button.
type PortSet struct {
	ports  map[int]bool
	ranges [][2]int
}

// ParsePortSet reads a spec like "3001-3013,9229". Unparseable entries are
// ignored rather than failing startup: a typo in an optional exclusion list
// should not stop the daemon from running.
func ParsePortSet(spec string) PortSet {
	set := PortSet{ports: make(map[int]bool)}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			from, err1 := strconv.Atoi(strings.TrimSpace(lo))
			to, err2 := strconv.Atoi(strings.TrimSpace(hi))
			if err1 == nil && err2 == nil && from <= to {
				set.ranges = append(set.ranges, [2]int{from, to})
			}
			continue
		}
		if port, err := strconv.Atoi(part); err == nil {
			set.ports[port] = true
		}
	}
	return set
}

// Empty reports whether the set excludes nothing.
func (p PortSet) Empty() bool { return len(p.ports) == 0 && len(p.ranges) == 0 }

// Has reports whether port is in the set.
func (p PortSet) Has(port int) bool {
	if p.ports[port] {
		return true
	}
	for _, r := range p.ranges {
		if port >= r[0] && port <= r[1] {
			return true
		}
	}
	return false
}
