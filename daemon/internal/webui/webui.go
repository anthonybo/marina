// Package webui carries the built dashboard, compiled into the binary so the
// daemon is a single self-contained file with nothing to serve from disk.
package webui

import (
	"embed"
	"io/fs"
)

// dist is written by `npm run build`. In a fresh checkout it holds only a
// .gitkeep, which is enough to satisfy go:embed but means there is no dashboard
// to serve yet — hence Built().
//
//go:embed all:dist
var dist embed.FS

// placeholder explains what to run when the bundle is missing. It is tracked in
// git, unlike anything under dist, so it can never be clobbered by a build.
//
//go:embed placeholder.html
var placeholder []byte

// FS returns the dashboard's file tree, or nil if it has not been built.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// Placeholder is the page to serve when FS returns nil.
func Placeholder() []byte { return placeholder }
