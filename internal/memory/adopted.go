package memory

import (
	"sort"
	"strings"
	"sync"
)

// The adopted window is the set of entries the last render actually injected.
// Hysteresis compares a fresh ranking against it so an entrant only takes an
// incumbent's slot by a clear margin (Settings.HysteresisDelta): two topics whose
// scores wobble around each other by less than the margin cannot flip the block
// back and forth and bust the provider prefix cache every session. Without it the
// window is a pure function of the vault, which sounds stable but is not: the
// smallest score change reshuffles the entries fighting over the byte-ceiling
// edge.

type adoptState struct {
	order map[string]int
}

var adopted = struct {
	mu sync.Mutex
	m  map[string]*adoptState
}{m: map[string]*adoptState{}}

// vaultKey names the vault pair a store reads, which is what an adopted window
// belongs to. Two stores opened over the same directories share the adoption,
// which is the point: the tools open a fresh store per call.
func (s *Store) vaultKey() string {
	var parts []string
	for _, b := range s.banks() {
		if b != nil {
			parts = append(parts, normalizeForCompare(b.dir))
		}
	}
	return strings.Join(parts, "|")
}

// applyHysteresis reorders a fresh candidate list in favour of the last adopted
// window: entries are still ranked by hot_score, but inside the hysteresis band an
// incumbent keeps its place against a challenger it has not clearly beaten. A
// clear win, a gap of at least the margin, always reorders.
func (s *Store) applyHysteresis(entries []Entry) []Entry {
	delta := s.settings.HysteresisDelta
	if delta <= 0 || len(entries) < 2 {
		return entries
	}
	adopted.mu.Lock()
	st := adopted.m[s.vaultKey()]
	adopted.mu.Unlock()
	if st == nil || len(st.order) == 0 {
		return entries
	}
	rank := func(id string) int {
		if r, ok := st.order[id]; ok {
			return r
		}
		return len(st.order) // a fresh entrant loses a near-tie to an incumbent
	}
	out := append([]Entry(nil), entries...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if d := a.HotScore - b.HotScore; d >= delta || d <= -delta {
			return d > 0
		}
		if ra, rb := rank(a.ID), rank(b.ID); ra != rb {
			return ra < rb
		}
		if a.Trust != b.Trust {
			return a.Trust > b.Trust
		}
		return a.ID < b.ID
	})
	return out
}

// recordAdoption stores the window the last render injected, in injected order,
// as the incumbent set the next render compares against.
func (s *Store) recordAdoption(entries []Entry) {
	st := &adoptState{order: make(map[string]int, len(entries))}
	for i, e := range entries {
		st.order[e.ID] = i
	}
	adopted.mu.Lock()
	adopted.m[s.vaultKey()] = st
	adopted.mu.Unlock()
}

// ResetAdopted forgets the adopted windows, which is how a test measures a render
// as if no session had injected bytes before it.
func ResetAdopted() {
	adopted.mu.Lock()
	adopted.m = map[string]*adoptState{}
	adopted.mu.Unlock()
}
