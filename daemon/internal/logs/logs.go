// Package logs serves the output of processes Marina launched.
//
// The scope here is worth being explicit about: Marina can only show the terminal
// of something it started itself, because that is when it owns the pipe. A server
// you started in your own terminal writes to that terminal, and there is no way
// to retroactively attach to it — so those appear as "not captured" rather than
// as an empty log that implies something went wrong.
package logs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Entry describes one captured terminal.
type Entry struct {
	// Name is the project name, which is also the log's filename stem.
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	// Path is shown so you can tail it yourself if you'd rather.
	Path string `json:"path"`
	// Running is filled in by the caller, which knows the live port table.
	Running bool `json:"running"`
	// Source is "launch" for something Marina started, or "process" for a running
	// app that happens to write its output to a file Marina can read.
	Source string `json:"source"`
	// Port is set for a "process" source, and is how the client asks for it —
	// a port is resolved against live state, so no path ever comes from a client.
	Port int `json:"port,omitempty"`
}

// Chunk is a slice of a log, returned with the offsets needed to ask for more.
type Chunk struct {
	Name string `json:"name"`
	// Offset is where this data starts in the file.
	Offset int64 `json:"offset"`
	// Next is the offset to pass on the following request to continue tailing.
	Next int64 `json:"next"`
	// Size is the file's current total size.
	Size int64  `json:"size"`
	Data string `json:"data"`
	// Rotated is true when the file shrank, meaning a fresh launch truncated it
	// and any offset the client was holding is now meaningless.
	Rotated bool `json:"rotated"`
}

// Store reads log files from a single directory.
type Store struct {
	dir string
}

// New returns a Store over dir.
func New(dir string) *Store { return &Store{dir: dir} }

// Dir reports where logs are kept.
func (s *Store) Dir() string { return s.dir }

// safeName guards the only user-controlled input this package takes. A log name
// is a project name, so this is deliberately strict: no separators, no dots
// leading a traversal, nothing but the characters a directory name can hold.
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// resolve turns a requested name into a path inside the log directory, or an
// error. It checks the pattern and then confirms the cleaned path really is a
// direct child of the directory, so no encoding trick can escape it.
func (s *Store) resolve(name string) (string, error) {
	if !safeName.MatchString(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("logs: invalid name %q", name)
	}
	path := filepath.Join(s.dir, name+".log")
	if filepath.Dir(path) != filepath.Clean(s.dir) {
		return "", fmt.Errorf("logs: %q escapes the log directory", name)
	}
	return path, nil
}

// List returns every captured log, most recently written first.
func (s *Store) List() ([]Entry, error) {
	dirEntries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Nothing has been launched yet. That's not an error.
			return nil, nil
		}
		return nil, err
	}

	entries := make([]Entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".log") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(de.Name(), ".log")
		entries = append(entries, Entry{
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Path:    filepath.Join(s.dir, de.Name()),
			Source:  "launch",
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ModTime.After(entries[j].ModTime)
	})
	return entries, nil
}

// Remove deletes one of Marina's own launch logs. Only files inside the log
// directory can be named, by the same guard Read uses.
func (s *Store) Remove(name string) error {
	path, err := s.resolve(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// Read returns data from one of Marina's own launch logs, named by project.
//
// A negative offset means "the last max bytes", which is what a terminal view
// wants on first load. A non-negative offset continues from where the client left
// off, so following a live log transfers only what is new.
func (s *Store) Read(name string, offset, max int64) (Chunk, error) {
	path, err := s.resolve(name)
	if err != nil {
		return Chunk{}, err
	}
	return s.readFile(name, path, offset, max)
}

// ReadPath reads a log file outside the launch directory — the stdout of an app
// Marina did not start.
//
// The path must already have been resolved from live process state by the caller;
// this never accepts a path that came from a client. It additionally refuses
// anything that is not a regular file, so a caller mistake cannot turn into
// reading a device or a directory.
func (s *Store) ReadPath(label, path string, offset, max int64) (Chunk, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Chunk{}, err
	}
	if !info.Mode().IsRegular() {
		return Chunk{}, fmt.Errorf("logs: %q is not a regular file", path)
	}
	return s.readFile(label, path, offset, max)
}

func (s *Store) readFile(name, path string, offset, max int64) (Chunk, error) {
	if max <= 0 || max > 1<<20 {
		max = 256 << 10 // 256KB is far more than a terminal pane shows
	}

	file, err := os.Open(path)
	if err != nil {
		return Chunk{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Chunk{}, err
	}
	size := info.Size()

	chunk := Chunk{Name: name, Size: size}

	// A file that shrank was truncated by a fresh launch; the client's offset no
	// longer refers to the same bytes, so tell it to start over.
	if offset > size {
		chunk.Rotated = true
		offset = -1
	}

	start := offset
	if offset < 0 {
		start = size - max
		if start < 0 {
			start = 0
		}
	}

	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return Chunk{}, err
	}

	toRead := size - start
	if toRead > max {
		toRead = max
	}
	if toRead < 0 {
		toRead = 0
	}

	buf := make([]byte, toRead)
	n, err := io.ReadFull(file, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Chunk{}, err
	}
	buf = buf[:n]

	// When tailing from the end, don't hand back a partial first line.
	if offset < 0 && start > 0 {
		if i := indexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
			start += int64(i + 1)
		}
	}

	chunk.Offset = start
	chunk.Next = start + int64(len(buf))
	chunk.Data = string(buf)
	return chunk, nil
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}
