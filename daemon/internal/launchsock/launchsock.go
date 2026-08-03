// Package launchsock adopts sockets that launchd bound on this process's behalf.
//
// This is how a port below 1024 gets served without anything running as root.
// launchd binds the port from the Sockets entry in the job's plist, then starts
// the job — as the ordinary user — and hands over the listening descriptor. The
// daemon never has elevated privileges, and neither does anything it launches.
//
// Verified on this machine before being written: a plain user LaunchAgent
// (gui/$UID, no sudo anywhere) was handed a listener on 0.0.0.0:80 in a process
// running as uid 501. That was the surprise — the assumption going in was that a
// privileged port needed a root LaunchDaemon, and it does not.
//
// # Why not a pf redirect
//
// Redirecting port 80 with pf is the usual advice and is a bad idea here. Apple's
// TN3165 says Packet Filter is not API but an implementation detail, and directs
// developers to Network Extension instead. Worse for this specific feature, pf
// rule sets are reported to be flushed when the network changes — a Wi-Fi toggle
// is enough — which is exactly the event that changes the address this whole
// feature exists to publish. It would break at the only moment it mattered.
//
// # The cgo cost, stated plainly
//
// launch_activate_socket is a C function with no Go equivalent, so this package
// requires cgo, and its presence makes the whole daemon a cgo build. That is
// acceptable here only because building Marina already requires the Xcode command
// line tools for the Swift menu bar app, so a C toolchain is a given.
package launchsock

/*
#include <errno.h>
#include <launch.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"net"
	"os"
	"unsafe"
)

// ErrNotManaged means this process was not started by launchd, so there are no
// sockets to adopt. Running from a terminal during development hits this, and it
// is not a failure — the daemon binds its own port as usual.
var ErrNotManaged = errors.New("launchsock: not started by launchd")

// ErrNoSocket means launchd is managing this job but has no socket under that
// name. The usual cause is a plist without a matching Sockets entry, which is the
// state of an installation that did not ask for a privileged port.
var ErrNoSocket = errors.New("launchsock: no socket of that name in the job's plist")

// Listeners returns the listeners launchd bound for the named Sockets entry.
//
// The name must match the key inside the plist's Sockets dictionary exactly;
// launchd reports a mismatch the same way it reports a missing entry, so a typo
// looks like "not configured".
func Listeners(name string) ([]net.Listener, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var fds *C.int
	var count C.size_t

	// Returns a POSIX error code rather than setting errno.
	if rc := C.launch_activate_socket(cname, &fds, &count); rc != 0 {
		switch rc {
		case C.ESRCH:
			return nil, ErrNotManaged
		case C.ENOENT:
			return nil, ErrNoSocket
		default:
			return nil, fmt.Errorf("launchsock: %s", C.GoString(C.strerror(rc)))
		}
	}
	// The array is heap-allocated by the library and ours to release. The
	// descriptors themselves are not: they outlive it as listeners.
	defer C.free(unsafe.Pointer(fds))

	if count == 0 {
		return nil, ErrNoSocket
	}

	// One plist entry commonly yields two descriptors — IPv4 and IPv6 — and both
	// have to be served or the page works from one kind of client and not the other.
	out := make([]net.Listener, 0, int(count))
	for i, fd := range unsafe.Slice(fds, int(count)) {
		f := os.NewFile(uintptr(fd), fmt.Sprintf("launchd-%s-%d", name, i))
		ln, err := net.FileListener(f)
		if err != nil {
			// Close what was already adopted rather than leaking descriptors on a
			// partial failure.
			for _, done := range out {
				done.Close()
			}
			f.Close()
			return nil, fmt.Errorf("launchsock: adopting descriptor %d: %w", fd, err)
		}
		// FileListener dups the descriptor, so the original is no longer needed.
		f.Close()
		out = append(out, ln)
	}
	return out, nil
}
