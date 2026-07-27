package catalog

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Roots became mutable, so reads of them now happen while a write is in flight.
// Run under -race: this is the shape the API produces when a rescan overlaps the
// monitor's own sweep.
func TestRootsAreSafeUnderConcurrentUse(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	mustProject(t, filepath.Join(a, "alpha"))
	mustProject(t, filepath.Join(b, "beta"))

	c := New([]string{a}, 10*time.Millisecond)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Readers: the monitor's sweep and the API's payload.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					c.Projects(t.Context())
					c.Roots()
					c.Lookup(filepath.Join(a, "alpha"))
				}
			}
		}()
	}

	// Writers: someone adding and removing a directory.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 60; j++ {
				c.SetRoots([]string{a, b})
				c.SetRoots([]string{a})
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Whatever the interleaving, the final state must be coherent rather than a
	// half-applied list.
	c.SetRoots([]string{a, b})
	got := names(c.Projects(t.Context()))
	if len(got) != 2 {
		t.Fatalf("after concurrent churn the catalogue reports %v, want both projects", got)
	}
}
