package record

import (
	"sort"
	"strings"
	"testing"
)

func TestNewULIDShape(t *testing.T) {
	id := newULID()
	if len(id) != 26 {
		t.Fatalf("ULID length = %d, want 26", len(id))
	}
	for _, r := range id {
		if !strings.ContainsRune(crockford, r) {
			t.Errorf("ULID %q has non-Crockford char %q", id, r)
		}
	}
	if s := shard(id); s != id[24:] {
		t.Errorf("shard = %q, want last two chars %q", s, id[24:])
	}
}

func TestNewULIDMonotonic(t *testing.T) {
	const n = 1000
	ids := make([]string, n)
	for i := range ids {
		ids[i] = newULID()
	}
	if !sort.StringsAreSorted(ids) {
		t.Error("ULIDs generated in a burst are not lexicographically sorted")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate ULID %q", id)
		}
		seen[id] = true
	}
}

func TestEncodeULIDOrderPreserving(t *testing.T) {
	lo := encodeULID([16]byte{0: 0x00, 15: 0x00})
	hi := encodeULID([16]byte{0: 0x00, 15: 0x01})
	if lo >= hi {
		t.Errorf("encode not order-preserving in low bits: %q >= %q", lo, hi)
	}
	top := encodeULID([16]byte{0: 0x01})
	if top <= lo {
		t.Errorf("encode not order-preserving in high bits: %q <= %q", top, lo)
	}
	if lo != strings.Repeat("0", 26) {
		t.Errorf("all-zero ULID = %q, want 26 zeroes", lo)
	}
}
