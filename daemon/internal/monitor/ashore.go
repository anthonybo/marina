package monitor

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/anthonybo/marina/daemon/internal/catalog"
)

// Ashore is a project Marina found on disk that is not currently listening.
//
// "Ashore" because the harbour view draws these as boats hauled out of the water:
// they exist, they're yours, they're just not out working.
type Ashore struct {
	catalog.Project
	// Starting is true between a launch and its ports appearing.
	Starting bool `json:"starting"`
	// Failed is true when the last launch attempt did not survive.
	Failed bool `json:"failed"`
	// Error says what went wrong, in terms you can act on.
	Error string `json:"error,omitempty"`
	// LastSeen is when this project last had a port open, from stored history.
	LastSeen *time.Time `json:"lastSeen,omitempty"`
	// LogPath is where the launch wrote its output — the place to look next.
	LogPath string `json:"logPath,omitempty"`
	// Expect is the ports this project is likely to bind, strongest evidence
	// first: ports Marina has actually seen it use, then what its own files say.
	Expect []catalog.ExpectedPort `json:"expect,omitempty"`
	// Conflicts are expected ports that something else already holds. Starting is
	// still allowed — a dev server may pick the next free port — but you should
	// know before you click.
	Conflicts []Conflict `json:"conflicts,omitempty"`
}

// Conflict is an expected port that is already taken, and by what.
type Conflict struct {
	Port int `json:"port"`
	// HeldBy names the current occupant in the terms the dashboard already uses.
	HeldBy string `json:"heldBy"`
	Kind   string `json:"kind"`
	// Source is the evidence for expecting this port, so a conflict against a
	// mere framework default reads as weaker than one against observed history.
	Source string `json:"source"`
}

// mergePorts puts observed history in front of everything the project's own files
// imply, dropping duplicates so a port seen in both is reported once, as history.
func mergePorts(
	declared []catalog.ExpectedPort,
	historyPorts func(path string) []int,
	path string,
) []catalog.ExpectedPort {
	merged := make([]catalog.ExpectedPort, 0, len(declared)+2)
	seen := make(map[int]bool)

	if historyPorts != nil {
		for _, port := range historyPorts(path) {
			if seen[port] {
				continue
			}
			seen[port] = true
			merged = append(merged, catalog.ExpectedPort{
				Port:   port,
				Source: catalog.SourceHistory,
				Detail: "last used this port",
			})
		}
	}
	for _, p := range declared {
		if seen[p.Port] {
			continue
		}
		seen[p.Port] = true
		merged = append(merged, p)
	}

	catalog.SortPorts(merged)
	return merged
}

// occupiedPorts maps every port currently in use to what holds it, so the
// catalogue can warn before a launch collides.
func occupiedPorts(services []Service) map[int]holder {
	out := make(map[int]holder, len(services))
	for _, s := range services {
		label := s.Display
		if label == "" {
			label = s.Label
		}
		if s.Subpath != "" {
			label += " → " + s.Subpath
		}
		out[s.Port] = holder{label: label, kind: string(s.Kind)}
	}
	return out
}

// runningPaths collects the project directories currently accounted for by live
// services, so the catalogue can tell which of its entries are already up.
//
// Matching is by repo root where we have one, and otherwise by containment: a
// service whose working directory sits inside a project belongs to it.
func runningPaths(services []Service) map[string]bool {
	paths := make(map[string]bool, len(services)*2)
	add := func(path string) {
		if path == "" {
			return
		}
		paths[filepath.Clean(path)] = true
		// Also record the resolved form: lsof reports a real path, while the
		// catalogue may have scanned a symlinked one (/tmp vs /private/tmp).
		if real, err := filepath.EvalSymlinks(path); err == nil {
			paths[real] = true
		}
	}
	for _, s := range services {
		add(s.Repo)
		add(s.Dir)
	}
	return paths
}

// selfPaths returns the forms of Marina's own source directory that a catalogue
// entry might be keyed by, so it matches whichever the scan produced.
func selfPaths(sourceDir string) []string {
	if sourceDir == "" {
		return nil
	}
	clean := filepath.Clean(sourceDir)
	paths := []string{clean}
	if real, err := filepath.EvalSymlinks(clean); err == nil && real != clean {
		paths = append(paths, real)
	}
	return paths
}

// isRunning reports whether a catalogued project has any live service.
func isRunning(project catalog.Project, live map[string]bool) bool {
	for _, target := range []string{filepath.Clean(project.Path), project.Real} {
		if target == "" {
			continue
		}
		if live[target] {
			return true
		}
		// A service deep inside the project — say a monorepo package — still
		// means the project is up.
		prefix := target + string(filepath.Separator)
		for path := range live {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
	}
	return false
}

// ashoreFrom returns the catalogued projects that are not currently running,
// annotated with launch state and last-seen history.
// holder describes what currently occupies a port.
type holder struct {
	label string
	kind  string
}

func ashoreFrom(
	projects []catalog.Project,
	live map[string]bool,
	launches []catalog.Launch,
	lastSeen func(path string) *time.Time,
	historyPorts func(path string) []int,
	occupied map[int]holder,
) []Ashore {
	byPath := make(map[string]catalog.Launch, len(launches))
	for _, launch := range launches {
		byPath[filepath.Clean(launch.Path)] = launch
	}

	out := make([]Ashore, 0, len(projects))
	for _, project := range projects {
		if isRunning(project, live) {
			continue
		}
		entry := Ashore{Project: project}
		if launch, ok := byPath[filepath.Clean(project.Path)]; ok {
			entry.LogPath = launch.LogPath
			// Failure takes precedence: a project that is still nominally within
			// the starting window but has already exited must not show a spinner.
			if launch.Failed() {
				entry.Failed = true
				entry.Error = launch.Error
			} else {
				entry.Starting = launch.Starting()
			}
		}
		if lastSeen != nil {
			entry.LastSeen = lastSeen(project.Path)
		}

		// Ports Marina has actually watched this project use outrank anything
		// inferred from its files, and they cost nothing to collect.
		entry.Expect = mergePorts(project.Ports, historyPorts, project.Path)
		for _, expect := range entry.Expect {
			if held, taken := occupied[expect.Port]; taken {
				entry.Conflicts = append(entry.Conflicts, Conflict{
					Port:   expect.Port,
					HeldBy: held.label,
					Kind:   held.kind,
					Source: expect.Source,
				})
			}
		}

		out = append(out, entry)
	}
	return out
}
