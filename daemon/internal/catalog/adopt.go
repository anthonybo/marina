package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/anthonybo/marina/daemon/internal/procs"
)

// Launch records are kept on disk so a daemon restart does not lose track of the
// apps it started.
//
// This matters because launched apps deliberately outlive the daemon
// (AbandonProcessGroup in the plist). Without persistence, upgrading Marina left
// a running fleet that Marina no longer recognised as its own — so stopping it as
// a group, the only way to take down a `concurrently` tree, was refused.

// stateFile is where launch records live, beside the logs they refer to.
func (l *Launcher) stateFile() string { return filepath.Join(l.logDir, "launches.json") }

// persisted is the on-disk shape. Only what is needed to re-adopt a process.
type persisted struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Command string    `json:"command"`
	PID     int       `json:"pid"`
	PGID    int       `json:"pgid"`
	LogPath string    `json:"logPath"`
	At      time.Time `json:"at"`
}

// save writes the currently-tracked launches. Failures are logged, not fatal:
// losing the ability to group-stop is worse than a crash but not worth one.
func (l *Launcher) save() {
	l.mu.Lock()
	records := make([]persisted, 0, len(l.recent))
	for _, launch := range l.recent {
		if launch.PGID == 0 && launch.PID == 0 {
			continue
		}
		records = append(records, persisted{
			Path: launch.Path, Name: launch.Name, Command: launch.Command,
			PID: launch.PID, PGID: launch.PGID, LogPath: launch.LogPath, At: launch.At,
		})
	}
	l.mu.Unlock()

	body, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		l.log.Warn("catalog: could not encode launch state", "err", err)
		return
	}
	if err := os.MkdirAll(l.logDir, 0o755); err != nil {
		l.log.Warn("catalog: could not create the state directory", "err", err)
		return
	}
	// Write-then-rename so a crash cannot leave a half-written file.
	tmp := l.stateFile() + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		l.log.Warn("catalog: could not write launch state", "err", err)
		return
	}
	if err := os.Rename(tmp, l.stateFile()); err != nil {
		l.log.Warn("catalog: could not replace launch state", "err", err)
	}
}

// Adopt reclaims launches from a previous run whose processes are still alive, so
// they can still be stopped as a group. Records whose process group is gone are
// dropped.
func (l *Launcher) Adopt() {
	body, err := os.ReadFile(l.stateFile())
	if err != nil {
		return // nothing to adopt
	}
	var records []persisted
	if err := json.Unmarshal(body, &records); err != nil {
		l.log.Warn("catalog: launch state is unreadable; ignoring it", "err", err)
		return
	}

	adopted := 0
	l.mu.Lock()
	for _, r := range records {
		if !procs.GroupAlive(r.PGID) && !procs.Alive(r.PID) {
			continue
		}
		l.recent[r.Path] = &Launch{
			Path: r.Path, Name: r.Name, Command: r.Command,
			PID: r.PID, PGID: r.PGID, LogPath: r.LogPath, At: r.At,
			// Adopted records describe a process that is already running, so they
			// must not read as "starting" — that window is long past.
			Adopted: true,
		}
		adopted++
	}
	l.mu.Unlock()

	if adopted > 0 {
		l.log.Info("catalog: readopted launches from a previous run", "count", adopted)
	}
	l.save()
}
