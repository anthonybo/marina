package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/anthonybo/marina/daemon/internal/identify"
)

// RootStore persists the scanned-directory list so a directory added in the
// dashboard survives a restart.
//
// Precedence is deliberately simple, because the alternative is a rule nobody can
// predict: if this file exists it is the whole answer, and --roots/MARINA_ROOTS
// only seed a machine that has never set them here. The installer deletes this
// file when --roots is passed explicitly, so that flag always wins when someone
// reaches for it.
type RootStore struct {
	path string
}

func NewRootStore(dir string) *RootStore {
	return &RootStore{path: filepath.Join(dir, "roots.json")}
}

// Path is where the list is kept, so the UI can say where to look.
func (s *RootStore) Path() string { return s.path }

type rootsFile struct {
	Roots []string `json:"roots"`
}

// Load returns the saved roots, or fallback when nothing has been saved. The
// second value reports whether the answer came from the file, which the UI uses
// to explain where the current list comes from.
func (s *RootStore) Load(fallback []string) ([]string, bool) {
	body, err := os.ReadFile(s.path)
	if err != nil {
		return CleanRoots(fallback), false
	}
	var parsed rootsFile
	if err := json.Unmarshal(body, &parsed); err != nil {
		// A corrupt file must not cost you the boatyard entirely.
		return CleanRoots(fallback), false
	}
	cleaned := CleanRoots(parsed.Roots)
	if len(cleaned) == 0 {
		// An empty list is a real choice — it means "scan nothing" — but an empty
		// *file* is far more likely to be damage, so fall back instead.
		return CleanRoots(fallback), false
	}
	return cleaned, true
}

// Save writes the list atomically, so an interrupted write cannot leave a
// half-parsed file behind that would silently drop every root.
func (s *RootStore) Save(roots []string) error {
	body, err := json.MarshalIndent(rootsFile{Roots: CleanRoots(roots)}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ValidateRoot turns whatever the user typed into a directory worth scanning, or
// explains why it isn't one. The message is shown verbatim in the UI, so it says
// what to do rather than naming an errno.
func ValidateRoot(input string, existing []string) (string, error) {
	cleaned := CleanRoots([]string{input})
	if len(cleaned) == 0 {
		return "", fmt.Errorf("enter a directory path")
	}
	path := cleaned[0]

	if path == "/" {
		return "", fmt.Errorf("/ is too broad — add the directory that holds your projects")
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("%s does not exist", path)
	}
	if err != nil {
		return "", fmt.Errorf("cannot read %s", path)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is a file, not a directory", path)
	}
	// Readability is worth checking now rather than silently scanning nothing:
	// a missing or unreadable root is otherwise ignored without comment.
	if entries, err := os.Open(path); err != nil {
		return "", fmt.Errorf("cannot read %s — check its permissions", path)
	} else {
		entries.Close()
	}

	if slices.Contains(existing, path) {
		return "", fmt.Errorf("%s is already being scanned", path)
	}

	// Nesting is deliberately allowed. It looks like it would list things twice and
	// it cannot: the scan reads exactly one level, so a root lists its own children
	// and nothing deeper, and a directory has one parent — so no project can be a
	// direct child of two different roots.
	//
	// Refusing it was a real bug. A directory of directories of projects
	// (~/projects/draftingroom, holding eight repos) is invisible: it is not itself
	// a project, so the scan of ~/projects drops it and never looks inside. Adding
	// it is the fix, and "already covered by ~/projects" made the fix impossible
	// while the panel's own hint told you to do it.

	// The important one, and the least obvious. Scanned directories are also the
	// boundaries the identifier stops its upward walk at, so one placed at or
	// inside a project truncates that walk below the project's real root and the
	// app gets named after whichever subdirectory it stopped at — a monorepo
	// package suddenly calling itself "frontend". A git root does not save it: the
	// boundary is checked before .git is ever found. Verified in
	// identify/boundary_change_test.go.
	if identify.IsProject(path) {
		return "", fmt.Errorf("%s is a project, not a directory of projects — add its parent instead", path)
	}
	if owner, ok := enclosingProject(path); ok {
		return "", fmt.Errorf("%s is inside the project %s — scanning it would misname that project's apps",
			path, filepath.Base(owner))
	}
	return path, nil
}

// enclosingProject returns the nearest ancestor that declares itself a project.
//
// Stops at $HOME and /, which are never projects for this purpose even when the
// home directory happens to be a git repository — a dotfiles repo must not make
// every directory beneath it "inside a project".
func enclosingProject(path string) (string, bool) {
	home, _ := os.UserHomeDir()
	for cur := filepath.Dir(path); ; {
		if cur == "/" || cur == "." || (home != "" && cur == home) {
			return "", false
		}
		if identify.IsProject(cur) {
			return cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}
