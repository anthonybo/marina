package monitor

import (
	"sort"
	"strings"

	"github.com/anthonybo/marina/daemon/internal/identify"
)

// Role says how a service relates to the rest of its project.
//
// This exists because a count of listening ports badly overstates how many apps
// you are actually running. One project can be a single UI plus a dozen workers
// that only exist to serve it — thirteen ports, but one thing you'd ever open.
type Role string

const (
	// RolePrimary is the app you would actually open: a project's UI.
	RolePrimary Role = "primary"
	// RoleService is a supporting process belonging to a primary — a worker, an
	// API, a proxy. Real, but not something you browse to on its own.
	RoleService Role = "service"
	// RoleSolo is a service with no such relationship to resolve.
	RoleSolo Role = "solo"
)

// uiFrameworks serve pages to people. Their presence is the strongest signal
// that a port is a project's front door rather than one of its workers.
var uiFrameworks = map[string]bool{
	"Vite": true, "Next.js": true, "Nuxt": true, "Astro": true,
	"SvelteKit": true, "Remix": true, "CRA": true, "Angular": true,
	"Vue CLI": true, "Storybook": true, "Expo": true, "Webpack": true,
	"Parcel": true, "Rails": true, "Django": true, "Laravel": true,
}

// uiDirs are the directory names people give the user-facing half of a project.
var uiDirs = map[string]bool{
	"frontend": true, "front-end": true, "web": true, "www": true,
	"ui": true, "client": true, "app": true, "site": true,
	"dashboard": true, "admin": true, "portal": true,
}

// primaryScore rates how likely a service is to be its project's front door.
// Zero means no evidence at all, in which case nothing is collapsed — guessing
// a primary from a group of equals would invent a hierarchy that isn't there.
//
// Pins deliberately carry no weight here. A role describes what a service *is*
// within its project; a pin says what the user wants kept close. Letting a pin
// decide the front door would mean pinning a worker you happen to be debugging
// demotes the real UI to being a service of that worker. Prominence is handled
// separately, by surfacing a whole cluster when any part of it is pinned.
func primaryScore(s Service) int {
	score := 0

	// Serving an HTML document with a title is the clearest evidence: workers
	// answer with JSON and have no title.
	if s.Probe.Title != "" {
		score += 40
	}
	if uiFrameworks[s.Framework] {
		score += 30
	}
	if isUIPath(s.Subpath) || isUIPath(s.Dir) {
		score += 20
	}
	return score
}

func isUIPath(path string) bool {
	if path == "" {
		return false
	}
	// Check the last segment, and also any segment, so "packages/frontend" and
	// "apps/web/dist" both resolve.
	for _, part := range strings.Split(strings.ToLower(path), "/") {
		if uiDirs[part] {
			return true
		}
	}
	return false
}

// assignRoles groups the apps by project and, where one member is clearly the
// front door, marks the rest as its services. Services keep every detail they
// had — this only records the relationship so the views can present a project as
// one thing with supporting parts.
func assignRoles(services []Service) {
	for i := range services {
		services[i].Role = RoleSolo
		services[i].ServiceCount = 0
		services[i].PrimaryPort = 0
	}

	groups := make(map[string][]int)
	var order []string
	for i := range services {
		s := &services[i]
		if s.Kind != identify.KindApp || s.Project == "" {
			continue
		}
		if _, seen := groups[s.Project]; !seen {
			order = append(order, s.Project)
		}
		groups[s.Project] = append(groups[s.Project], i)
	}

	for _, project := range order {
		members := groups[project]
		if len(members) < 2 {
			continue
		}

		best, bestScore := -1, 0
		for _, idx := range members {
			score := primaryScore(services[idx])
			if score == 0 {
				continue
			}
			// Highest score wins; a tie goes to the lower port so the choice is
			// stable rather than dependent on scan order.
			if score > bestScore || (score == bestScore && best >= 0 && services[idx].Port < services[best].Port) {
				best, bestScore = idx, score
			}
		}
		// No evidence anywhere in the group: leave them all as peers.
		if best < 0 {
			continue
		}

		services[best].Role = RolePrimary
		services[best].ServiceCount = len(members) - 1
		for _, idx := range members {
			if idx == best {
				continue
			}
			services[idx].Role = RoleService
			services[idx].PrimaryPort = services[best].Port
		}
	}
}

// sortServices orders the list the way the views want to render it: pinned
// first, then apps before infrastructure, and within a project the primary ahead
// of the services that belong to it.
func sortServices(s []Service) {
	kindRank := map[identify.Kind]int{
		identify.KindApp:     0,
		identify.KindUnknown: 1,
		identify.KindInfra:   2,
		identify.KindSystem:  3,
	}
	roleRank := map[Role]int{RolePrimary: 0, RoleSolo: 0, RoleService: 1}

	sort.SliceStable(s, func(i, j int) bool {
		a, b := s[i], s[j]
		if a.Meta.Pinned != b.Meta.Pinned {
			return a.Meta.Pinned
		}
		if kindRank[a.Kind] != kindRank[b.Kind] {
			return kindRank[a.Kind] < kindRank[b.Kind]
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if roleRank[a.Role] != roleRank[b.Role] {
			return roleRank[a.Role] < roleRank[b.Role]
		}
		return a.Port < b.Port
	})
}
