package probe

import "testing"

func TestParsePortSet(t *testing.T) {
	set := ParsePortSet("3001-3013, 9229 ,5432")

	in := []int{3001, 3007, 3013, 9229, 5432}
	for _, port := range in {
		if !set.Has(port) {
			t.Errorf("Has(%d) = false, want true", port)
		}
	}
	out := []int{3000, 3014, 9230, 80, 0}
	for _, port := range out {
		if set.Has(port) {
			t.Errorf("Has(%d) = true, want false", port)
		}
	}
}

func TestParsePortSetTolerantOfJunk(t *testing.T) {
	// A typo in an optional list must not break the daemon.
	set := ParsePortSet("abc, 3000, 9-, -5, 20-10, ,")
	if !set.Has(3000) {
		t.Error("valid entry was lost among invalid ones")
	}
	for _, port := range []int{9, 5, 15} {
		if set.Has(port) {
			t.Errorf("Has(%d) = true from a malformed entry", port)
		}
	}
}

func TestEmptyPortSet(t *testing.T) {
	if !ParsePortSet("").Empty() {
		t.Error("empty spec should produce an empty set")
	}
	if ParsePortSet("80").Empty() {
		t.Error("non-empty spec reported empty")
	}
	if ParsePortSet("").Has(80) {
		t.Error("empty set must exclude nothing")
	}
}
