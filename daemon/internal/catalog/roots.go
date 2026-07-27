package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

	// A root inside another root would list the same projects twice, and one
	// holding an existing root would make that root redundant. Both are worth
	// saying out loud rather than quietly accepting.
	for _, other := range existing {
		if isInside(path, other) {
			return "", fmt.Errorf("%s is already covered by %s", path, other)
		}
		if isInside(other, path) {
			return "", fmt.Errorf("%s contains %s, which is already scanned — remove that one first", path, other)
		}
	}
	return path, nil
}

// isInside reports whether child is below parent.
func isInside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, "..")
}
