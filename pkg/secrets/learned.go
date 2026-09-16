package secrets

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

// Learned tuning. minLearnLen is the floor for a value to be remembered as a
// judged secret. It is deliberately higher than the registry's minSecretLen:
// a value this package learns has already been *judged* a real secret by a
// detector, but a very short one (an 8-byte hex token, a common identifier) is
// the kind of thing that shows up as a gitleaks false positive, and hashing it
// would let one misclassification suppress that literal everywhere for the rest
// of the session. Anything shorter is not remembered. maxLearnedEntries bounds
// the live digest set so a long-running process that keeps scanning distinct
// candidate values cannot grow the set without limit; the oldest digest is
// evicted first (the registry's FIFO discipline), which only ever means a value
// stops being force-scrubbed, never that something leaks.
const (
	minLearnLen       = 8
	maxLearnedEntries = 4096
	learnedKeyBytes   = 32
)

// LearnedSet is the hashed learned-secret memory: a set of keyed hashes (HMAC
// digests) of secret values this process has already seen and judged sensitive,
// with no plaintext of those values retained anywhere. It is the durable
// sibling of the known-value Registry. The Registry holds the exact credentials
// we *load* (provider keys, OAuth tokens, MCP env) and erases them by literal
// match; the LearnedSet holds digests of credentials we *observe a detector
// flag*, so the same value is recognised as sensitive on every later appearance
// without us ever storing what it is.
//
// It is consulted, not used as a replacer: the redaction pass already holds the
// plaintext candidate at the moment it decides to scrub, so it asks "is the
// HMAC of this candidate in the learned set?" to upgrade a low-confidence,
// easily-false-positived detection (a generic KEY=value hit on a source file,
// which code-file FP mode would otherwise drop) into a confident scrub. That is
// the whole reason a keyed hash, rather than the value, is stored: the property
// it buys is "recognise a value we have already classified," and the digests can
// be persisted or logged where the plaintext never could.
//
// A digest is a permanent correlation id (equal value => equal digest), so this
// set does reveal that two occurrences carried the same secret. That is intended
// ("these two configs share a credential") but it is information; it is not a
// place to store arbitrary material.
type LearnedSet struct {
	mu      sync.RWMutex
	key     []byte                    // process HMAC key; never leaves this field
	digests map[[sha256.Size]byte]int // digest -> value length (length is not secret)
	order   [][sha256.Size]byte       // insertion order for bounded eviction
	max     int
	enabled bool
}

// NewLearned builds an empty learned-secret memory with a fresh random HMAC key
// (the openclaw sentinel.ts "nonce" pattern: an outsider holding a dump of the
// set cannot, without this key, offline-confirm whether a candidate value is
// present). A non-positive max falls back to the default bound.
func NewLearned(max int) *LearnedSet {
	if max <= 0 {
		max = maxLearnedEntries
	}
	key := make([]byte, learnedKeyBytes)
	// crypto/rand.Read does not return an error in supported Go versions (it
	// panics on an OS getrandom failure, which does not happen on our targets),
	// so there is no meaningful recover here; the blank assignment keeps the call
	// valid against the (n, err) signature.
	_, _ = rand.Read(key)
	return &LearnedSet{
		key:     key,
		digests: make(map[[sha256.Size]byte]int),
		max:     max,
		enabled: true,
	}
}

var learnedDef = NewLearned(maxLearnedEntries)

// Learn records the keyed hash of value in the default set as a value that has
// been judged sensitive. It is a no-op for short values and disabled sets, and
// reports whether this was a newly-remembered digest (false if already known).
func Learn(value string) bool { return learnedDef.Learn(value) }

// Known reports whether the keyed hash of value is present in the default set,
// i.e. whether this process has previously judged this exact value sensitive.
func Known(value string) bool { return learnedDef.Known(value) }

// Digest returns the hex keyed hash of value under the default set's process
// key. It is the non-reversible token meant for logs and telemetry: two equal
// values in one process share a digest, and the value can not be recovered from
// it (nor, without the key, confirmed against a guess).
func Digest(value string) string { return learnedDef.Digest(value) }

// Learn records value in this set. Empty, too-short, and (when disabled) all
// values are skipped. The computed digest replaces no existing entry if it is
// already present, so repeated detections of one secret are idempotent.
func (s *LearnedSet) Learn(value string) bool {
	if s == nil {
		return false
	}
	value = strings.TrimSpace(value)
	if len(value) < minLearnLen {
		return false
	}
	sum := s.digest(value)

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return false
	}
	if _, ok := s.digests[sum]; ok {
		return false
	}
	s.digests[sum] = len(value)
	s.order = append(s.order, sum)
	if len(s.digests) > s.max {
		s.evictLocked()
	}
	return true
}

// Known reports whether value was previously judged sensitive by this set.
func (s *LearnedSet) Known(value string) bool {
	if s == nil {
		return false
	}
	value = strings.TrimSpace(value)
	if len(value) < minLearnLen {
		return false
	}
	sum := s.digest(value)

	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return false
	}
	_, ok := s.digests[sum]
	return ok
}

// Digest is the per-set form of the package Digest.
func (s *LearnedSet) Digest(value string) string {
	if s == nil {
		return ""
	}
	sum := s.digest(value)
	return hex.EncodeToString(sum[:])
}

// digest computes HMAC-SHA256(key, value). It takes only the read lock so it is
// safe to call from the read hot path; the key is written exactly once at
// construction and never mutated afterwards.
func (s *LearnedSet) digest(value string) [sha256.Size]byte {
	s.mu.RLock()
	key := s.key
	s.mu.RUnlock()

	var out [sha256.Size]byte
	m := hmac.New(sha256.New, key)
	m.Write([]byte(value))
	copy(out[:], m.Sum(nil)) // Sum(nil) returns just the MAC.
	return out
}

// evictLocked drops the oldest-remembered digests until the set is back under
// the bound. Called with the write lock held. Evicting a still-active secret is
// safe: it only stops being force-scrubbed by this layer, and the gitleaks
// detector and the registry remain to catch the value itself.
func (s *LearnedSet) evictLocked() {
	for len(s.digests) > s.max && len(s.order) > 0 {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.digests, oldest)
	}
}

// LearnedEnabled reports whether the default set is recording and matching.
func LearnedEnabled() bool { return learnedDef.Enabled() }

// Enabled reports whether this set is recording and matching.
func (s *LearnedSet) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// SetLearnedEnabled turns the default set on or off. Disabling stops both new
// recording and Known lookups; existing digests are kept so a later enable is
// complete.
func SetLearnedEnabled(on bool) { learnedDef.SetEnabled(on) }

// ResetLearned clears the default learned-secret set back to empty. Intended
// for tests that need a clean slate between cases.
func ResetLearned() { learnedDef.Reset() }

// LearnedLen reports how many digests the default set currently holds. Intended
// for tests and diagnostics.
func LearnedLen() int { return learnedDef.Len() }

// SetEnabled is the per-set form.
func (s *LearnedSet) SetEnabled(on bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = on
}

// Len reports the number of digests held. Intended for tests and diagnostics.
func (s *LearnedSet) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.digests)
}

// Reset clears the set back to empty, keeping the process HMAC key. Intended
// for tests.
func (s *LearnedSet) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.digests = make(map[[sha256.Size]byte]int)
	s.order = nil
	s.enabled = true
}
