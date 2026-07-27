package api

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/anthonybo/marina/daemon/internal/catalog"
)

// rootView is one scanned directory, described well enough that the UI can show
// why it is or isn't producing anything.
type rootView struct {
	Path string `json:"path"`
	// Display is the path with $HOME collapsed to ~, which is how anyone
	// recognises their own directories.
	Display string `json:"display"`
	Exists  bool   `json:"exists"`
	// Readable separates "you typed it wrong" from "macOS won't let me look".
	Readable bool `json:"readable"`
	// Projects is how many startable projects this root contributes. A root with
	// none is the visible symptom of the one-level-deep scan.
	Projects int `json:"projects"`
}

func (s *Server) handleRoots(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.rootsPayload(r))
}

func (s *Server) rootsPayload(r *http.Request) map[string]any {
	roots := s.mon.Roots()
	views := make([]rootView, 0, len(roots))

	// Attribute each project to the root that holds it. The catalogue's scan is
	// cached, so this costs a walk over a list rather than the disk.
	projects := s.mon.CatalogProjects(r.Context())
	for _, root := range roots {
		view := rootView{Path: root, Display: collapseHome(root)}
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			view.Exists = true
			if f, err := os.Open(root); err == nil {
				view.Readable = true
				f.Close()
			}
		}
		for _, p := range projects {
			if filepath.Dir(p.Path) == root {
				view.Projects++
			}
		}
		views = append(views, view)
	}

	return map[string]any{
		"roots": views,
		// Where the list lives, so the UI can point at it rather than being vague.
		"file": s.roots.Path(),
	}
}

func (s *Server) handleRootAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &body) {
		return
	}

	current := s.mon.Roots()
	path, err := catalog.ValidateRoot(body.Path, current)
	if err != nil {
		// The message is written to be read by a person, so it goes back as the
		// body rather than being flattened into a status code.
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	updated := append(slices.Clone(current), path)
	if err := s.roots.Save(updated); err != nil {
		s.log.Error("could not save roots", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "could not save the directory list",
		})
		return
	}
	s.mon.SetRoots(r.Context(), updated)
	s.log.Info("root added", "path", path)
	writeJSON(w, http.StatusOK, s.rootsPayload(r))
}

func (s *Server) handleRootRemove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &body) {
		return
	}

	current := s.mon.Roots()
	cleaned := catalog.CleanRoots([]string{body.Path})
	if len(cleaned) == 0 || !slices.Contains(current, cleaned[0]) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "that directory is not being scanned",
		})
		return
	}

	updated := slices.DeleteFunc(slices.Clone(current), func(p string) bool {
		return p == cleaned[0]
	})
	if err := s.roots.Save(updated); err != nil {
		s.log.Error("could not save roots", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "could not save the directory list",
		})
		return
	}
	s.mon.SetRoots(r.Context(), updated)
	s.log.Info("root removed", "path", cleaned[0])
	writeJSON(w, http.StatusOK, s.rootsPayload(r))
}

// collapseHome renders /Users/you/git as ~/git.
func collapseHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return path
}
