package logs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func store(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return New(dir), dir
}

// TestResolveRejectsTraversal is the important one: the log name is the only
// user-controlled input this package takes, and it becomes a filesystem path.
func TestResolveRejectsTraversal(t *testing.T) {
	s, _ := store(t)

	bad := []string{
		"../../etc/passwd",
		"..",
		"../secrets",
		"foo/bar",
		"foo/../../bar",
		"/etc/passwd",
		".hidden",
		"",
		strings.Repeat("a", 200),
		"foo\x00bar",
		"foo bar",
	}
	for _, name := range bad {
		if _, err := s.resolve(name); err == nil {
			t.Errorf("resolve(%q) was allowed — must be rejected", name)
		}
	}

	good := []string{"leadflow", "iptv-epg-matcher", "my_app", "app.v2", "A1"}
	for _, name := range good {
		if _, err := s.resolve(name); err != nil {
			t.Errorf("resolve(%q) rejected a legitimate project name: %v", name, err)
		}
	}
}

func TestListSortsNewestFirst(t *testing.T) {
	s, dir := store(t)

	for _, name := range []string{"old", "middle", "new"} {
		if err := os.WriteFile(filepath.Join(dir, name+".log"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Force a known ordering rather than relying on write timing.
	base := mustStat(t, filepath.Join(dir, "new.log"))
	chtime(t, filepath.Join(dir, "old.log"), base, -3600)
	chtime(t, filepath.Join(dir, "middle.log"), base, -60)

	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	want := []string{"new", "middle", "old"}
	for i, name := range want {
		if entries[i].Name != name {
			t.Errorf("entry %d = %q, want %q", i, entries[i].Name, name)
		}
	}

	// A non-log file must not appear.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, _ = s.List()
	for _, e := range entries {
		if e.Name == "notes" {
			t.Error("List included a non-.log file")
		}
	}
}

// TestListOnMissingDirectory: nothing launched yet is a normal state, not an error.
func TestListOnMissingDirectory(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "does-not-exist"))
	entries, err := s.List()
	if err != nil {
		t.Errorf("List() on a missing directory returned an error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestReadTailAndFollow(t *testing.T) {
	s, dir := store(t)
	path := filepath.Join(dir, "app.log")

	if err := os.WriteFile(path, []byte("line one\nline two\nline three\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A negative offset tails the file.
	tail, err := s.Read("app", -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tail.Data, "line three") {
		t.Errorf("tail missing the last line: %q", tail.Data)
	}
	if tail.Next != tail.Size {
		t.Errorf("Next = %d, want Size = %d", tail.Next, tail.Size)
	}

	// Following from Next returns only what is new.
	appendTo(t, path, "line four\n")
	next, err := s.Read("app", tail.Next, 0)
	if err != nil {
		t.Fatal(err)
	}
	if next.Data != "line four\n" {
		t.Errorf("follow returned %q, want just the new line", next.Data)
	}
}

// TestReadDetectsTruncation covers a relaunch: the file is rewritten from zero,
// so a client holding an old offset has to be told to start again.
func TestReadDetectsTruncation(t *testing.T) {
	s, dir := store(t)
	path := filepath.Join(dir, "app.log")

	if err := os.WriteFile(path, []byte(strings.Repeat("x", 500)), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := s.Read("app", -1, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Relaunch truncates.
	if err := os.WriteFile(path, []byte("fresh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := s.Read("app", first.Next, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Rotated {
		t.Error("Rotated = false, want true after the file was truncated")
	}
	if !strings.Contains(after.Data, "fresh") {
		t.Errorf("data = %q, want the new contents", after.Data)
	}
}

// TestReadTailStartsOnALineBoundary keeps the first visible line from being a
// fragment when tailing into the middle of the file.
func TestReadTailStartsOnALineBoundary(t *testing.T) {
	s, dir := store(t)
	body := strings.Repeat("a line of output here\n", 200)
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	chunk, err := s.Read("app", -1, 512)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(chunk.Data, "a line of output here\n") == false {
		t.Errorf("tail began mid-line: %q", chunk.Data[:40])
	}
}

func TestReadMissingLog(t *testing.T) {
	s, _ := store(t)
	if _, err := s.Read("nope", -1, 0); err == nil {
		t.Error("Read on a missing log returned no error")
	}
}

// helpers

func mustStat(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime().Unix()
}

func chtime(t *testing.T, path string, base, delta int64) {
	t.Helper()
	when := timeFromUnix(base + delta)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func timeFromUnix(sec int64) time.Time { return time.Unix(sec, 0) }

func appendTo(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}
