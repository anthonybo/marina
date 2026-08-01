package monitor

import (
	"testing"

	"github.com/anthonybo/marina/daemon/internal/identify"
	"github.com/anthonybo/marina/daemon/internal/probe"
	"github.com/anthonybo/marina/daemon/internal/store"
)

// app builds a test service with the fields role resolution actually reads.
func app(port int, project, subpath, framework, title string) Service {
	return Service{
		Service: identify.Service{
			Port:      port,
			Kind:      identify.KindApp,
			Project:   project,
			Subpath:   subpath,
			Framework: framework,
		},
		Probe: probe.Result{HTTP: title != "", Title: title},
	}
}

// TestAssignRolesFindsTheFrontDoor covers the case that motivated roles: one
// project serving a UI plus a dozen workers must read as one app, not thirteen.
func TestAssignRolesFindsTheFrontDoor(t *testing.T) {
	services := []Service{
		app(3001, "webapp", "packages/backend", "tsx", ""),
		app(3002, "webapp", "packages/backend", "tsx", ""),
		app(3003, "webapp", "packages/backend", "tsx", ""),
		app(5173, "webapp", "packages/frontend", "Vite", "Webapp — Dashboard"),
	}
	assignRoles(services)

	for _, s := range services {
		want := RoleService
		if s.Port == 5173 {
			want = RolePrimary
		}
		if s.Role != want {
			t.Errorf("port %d: role = %q, want %q", s.Port, s.Role, want)
		}
	}

	for _, s := range services {
		switch s.Port {
		case 5173:
			if s.ServiceCount != 3 {
				t.Errorf("primary ServiceCount = %d, want 3", s.ServiceCount)
			}
		default:
			if s.PrimaryPort != 5173 {
				t.Errorf("port %d: PrimaryPort = %d, want 5173", s.Port, s.PrimaryPort)
			}
		}
	}

	counts := countOf(services)
	if counts.Primary != 1 || counts.Services != 3 {
		t.Errorf("counts = %d primary / %d services, want 1 / 3", counts.Primary, counts.Services)
	}
}

// TestAssignRolesLeavesPeersAlone guards against inventing a hierarchy. A group
// of equals with no UI among them must stay flat.
func TestAssignRolesLeavesPeersAlone(t *testing.T) {
	services := []Service{
		app(4001, "pipeline", "cmd/ingest", "Go", ""),
		app(4002, "pipeline", "cmd/reduce", "Go", ""),
		app(4003, "pipeline", "cmd/export", "Go", ""),
	}
	assignRoles(services)

	for _, s := range services {
		if s.Role != RoleSolo {
			t.Errorf("port %d: role = %q, want %q — no evidence of a front door", s.Port, s.Role, RoleSolo)
		}
	}
	if c := countOf(services); c.Primary != 3 || c.Services != 0 {
		t.Errorf("counts = %d primary / %d services, want 3 / 0", c.Primary, c.Services)
	}
}

// TestAssignRolesIgnoresPins pins down that a pin does not rewrite a project's
// structure. Pinning a worker must not demote the real UI into being a service
// of that worker; prominence is a separate concern from role.
func TestAssignRolesIgnoresPins(t *testing.T) {
	services := []Service{
		app(3001, "webapp", "packages/backend", "tsx", ""),
		app(5173, "webapp", "packages/frontend", "Vite", "Webapp"),
	}
	services[0].Meta = store.Meta{Pinned: true}
	assignRoles(services)

	if services[1].Role != RolePrimary {
		t.Errorf("UI role = %q, want %q — a pinned worker must not displace it",
			services[1].Role, RolePrimary)
	}
	if services[0].Role != RoleService {
		t.Errorf("pinned worker role = %q, want %q", services[0].Role, RoleService)
	}
	if services[0].PrimaryPort != 5173 {
		t.Errorf("pinned worker PrimaryPort = %d, want 5173", services[0].PrimaryPort)
	}
}

// TestAssignRolesIgnoresOtherKinds keeps infrastructure and system processes out
// of app clustering entirely.
func TestAssignRolesIgnoresOtherKinds(t *testing.T) {
	services := []Service{
		{Service: identify.Service{Port: 5432, Kind: identify.KindInfra, Label: "PostgreSQL"}},
		{Service: identify.Service{Port: 6379, Kind: identify.KindInfra, Label: "Redis"}},
		{Service: identify.Service{Port: 5000, Kind: identify.KindSystem, Label: "ControlCenter"}},
	}
	assignRoles(services)
	for _, s := range services {
		if s.Role != RoleSolo {
			t.Errorf("port %d (%s): role = %q, want solo", s.Port, s.Kind, s.Role)
		}
	}
	if c := countOf(services); c.Primary != 0 || c.Services != 0 {
		t.Errorf("non-app kinds must not count as apps: got %d / %d", c.Primary, c.Services)
	}
}

// TestAssignRolesSingletonStaysSolo — a project with one port has nothing to
// collapse.
func TestAssignRolesSingletonStaysSolo(t *testing.T) {
	services := []Service{app(3000, "solo-app", "", "Vite", "solo-app")}
	assignRoles(services)
	if services[0].Role != RoleSolo {
		t.Errorf("role = %q, want %q", services[0].Role, RoleSolo)
	}
	if services[0].ServiceCount != 0 {
		t.Errorf("ServiceCount = %d, want 0", services[0].ServiceCount)
	}
}

func TestIsUIPath(t *testing.T) {
	cases := map[string]bool{
		"packages/frontend": true,
		"apps/web":          true,
		"frontend":          true,
		"admin":             true,
		"packages/backend":  false,
		"cmd/ingest":        false,
		"":                  false,
	}
	for path, want := range cases {
		if got := isUIPath(path); got != want {
			t.Errorf("isUIPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestSortServicesKeepsServicesUnderTheirPrimary verifies the render order the
// views depend on.
func TestSortServicesKeepsServicesUnderTheirPrimary(t *testing.T) {
	services := []Service{
		app(3002, "webapp", "packages/backend", "tsx", ""),
		app(5173, "webapp", "packages/frontend", "Vite", "Webapp"),
		app(3001, "webapp", "packages/backend", "tsx", ""),
	}
	assignRoles(services)
	sortServices(services)

	if services[0].Port != 5173 {
		t.Fatalf("first row = :%d, want the primary :5173", services[0].Port)
	}
	if services[1].Port != 3001 || services[2].Port != 3002 {
		t.Errorf("services out of order: got :%d, :%d", services[1].Port, services[2].Port)
	}
}
