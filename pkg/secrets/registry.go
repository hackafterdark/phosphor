// Package secrets implements the known-value redaction registry from the
// secret-protection plan (Phase 8).
//
// It is a process-global set of the *exact* secret strings this process loaded
// — your provider API keys, OAuth access tokens, MCP server credentials — and
// an O(n) scrubber that removes those literal values from any text that flows
// through a redaction point (bash stdout, file reads, MCP results, the
// provider wire). It is deliberately not a pattern detector: it knows nothing
// about the AKIA… or sk-… shapes, only the specific values it was handed on
// load. That makes it the cheap, high-precision, zero-ReDoS complement to the
// gitleaks value scanner (which catches the formats we did not know about): the
// registry erases the secrets we already know we own.
//
// Its coverage is exactly as wide as the Register plumbing — a secret you never
// registered is invisible to it, and a value transformed beyond the three forms
// it stores (raw, URL-encoded, JSON-escaped) is not caught here. That is
// gitleaks/entropy's job, not this one's.
package secrets

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"sync"
)

// Registry tuning. minSecretLen guards against registering a value so short it
// would scrub ordinary text (a real credential is never six bytes; a six-byte
// "secret" is allowed through on purpose rather than risk erasing common
// substrings). maxRegistryEntries bounds the live set so a long-running session
// with rotating OAuth tokens cannot grow the matcher unbounded; the oldest
// registered value is evicted first, and a value that has been evicted simply
// stops being scrubbed (fail toward leaving text intact).
const (
	minSecretLen        = 6
	maxRegistryEntries  = 512
	knownSecretSentinel = "<redacted:known-secret>"
)

// Registry is a concurrency-safe set of known secret literals with a lazily
// rebuilt strings.Replacer used to scrub them. strings.Replacer is Go's
// multi-pattern literal matcher (a trie, effectively Aho-Corasick for literals)
// and its Replace is documented safe for concurrent use, which is what lets the
// agent run redaction from parallel tool calls against one shared registry.
type Registry struct {
	mu       sync.RWMutex
	values   map[string][]string // registered value -> the forms stored for it
	forms    map[string]struct{} // every literal needle (raw + encoded + escaped)
	firsts   map[byte]struct{}   // cheap prefilter: first byte of any needle
	order    []string            // registration order, for bounded eviction
	replacer *strings.Replacer   // nil means "dirty, rebuild on next use"
	max      int
	enabled  bool
}

// New builds an empty registry. A non-positive max falls back to the default
// bound.
func New(max int) *Registry {
	if max <= 0 {
		max = maxRegistryEntries
	}
	return &Registry{
		values:  make(map[string][]string),
		forms:   make(map[string]struct{}),
		firsts:  make(map[byte]struct{}),
		max:     max,
		enabled: true,
	}
}

var def = New(maxRegistryEntries)

// Register adds an exact secret value to the default registry along with its
// URL-encoded and JSON-escaped forms, so a copy that was escaped before hitting
// the wire is still caught. Values shorter than minSecretLen are ignored.
func Register(value string) { def.Register(value) }

// Register adds an exact secret value to this registry. It is a no-op for
// disabled registries, empty values, and anything shorter than the minimum
// length floor.
func (r *Registry) Register(value string) {
	if value == "" {
		return
	}
	value = strings.TrimSpace(value)
	if len(value) < minSecretLen {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled {
		return
	}
	if _, seen := r.values[value]; seen {
		return
	}

	forms := secretForms(value)
	if len(forms) == 0 {
		return
	}
	r.values[value] = forms
	r.order = append(r.order, value)
	for _, f := range forms {
		if _, ok := r.forms[f]; !ok {
			r.forms[f] = struct{}{}
			r.firsts[f[0]] = struct{}{}
		}
	}
	r.replacer = nil // dirty

	if len(r.values) > r.max {
		r.evictLocked()
	}
}

// secretForms returns the distinct literal needles that stand for one value:
// the raw string, its URL-encoded form, and its JSON-escaped form. The encoded
// variants never drop characters, so every form is at least as long as the raw
// value and the minimum-length floor already cleared it.
func secretForms(value string) []string {
	candidates := []string{value, url.QueryEscape(value), jsonEscape(value)}
	out := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		if c == "" || len(c) < minSecretLen {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// jsonEscape renders s the way it would appear inside a JSON string literal
// (the surrounding quotes stripped), so a value that reached the transcript after
// being JSON-escaped is still matched literally.
func jsonEscape(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	if len(b) < 2 {
		return s
	}
	return string(b[1 : len(b)-1])
}

// Scrub replaces every occurrence of a registered value with a non-reusable
// sentinel. The hot path on clean output is a single O(len(text)) scan over the
// first-byte probe set: when no registered needle can even begin at a byte that
// appears in text, the text is returned untouched without ever building or
// running the matcher. On a possible hit the memoized strings.Replacer does one
// linear pass.
func Scrub(text string) string { return def.Scrub(text) }

// Scrub is the per-registry form of the package-level Scrub.
func (r *Registry) Scrub(text string) string {
	if text == "" {
		return text
	}
	r.mu.RLock()
	enabled := r.enabled
	replacer := r.replacer
	firsts := r.firsts
	r.mu.RUnlock()

	if !enabled || len(firsts) == 0 {
		return text
	}
	// First-byte prefilter: iterate the text once and bail if none of its bytes
	// could start any registered needle. This is the whole cost on clean data.
	if !mayContain(r, text) {
		return text
	}
	if replacer == nil {
		replacer = r.getReplacer()
	}
	return replacer.Replace(text)
}

// mayContain reports whether any byte in text is the first byte of some
// registered needle. Iterating the (short) text against the byte set keeps the
// pathological many-first-characters case linear rather than turning into one
// Contains pass per distinct first character.
func mayContain(r *Registry, text string) bool {
	r.mu.RLock()
	firsts := r.firsts
	r.mu.RUnlock()
	for i := 0; i < len(text); i++ {
		if _, ok := firsts[text[i]]; ok {
			return true
		}
	}
	return false
}

// getReplacer returns the memoized matcher, rebuilding it under the write lock
// when a registration has marked it dirty.
func (r *Registry) getReplacer() *strings.Replacer {
	r.mu.RLock()
	rep := r.replacer
	r.mu.RUnlock()
	if rep != nil {
		return rep
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.replacer == nil {
		pairs := make([]string, 0, len(r.forms)*2)
		for f := range r.forms {
			pairs = append(pairs, f, knownSecretSentinel)
		}
		r.replacer = strings.NewReplacer(pairs...)
	}
	return r.replacer
}

// evictLocked drops the oldest-registered value and all of its forms until the
// live set is back under the bound. Evicting an in-use credential is acceptable:
// it only means that value stops being auto-scrubbed; gitleaks remains as the
// backstop, and nothing is leaked that was not already.
func (r *Registry) evictLocked() {
	for len(r.values) > r.max && len(r.order) > 0 {
		victim := r.order[0]
		r.order = r.order[1:]
		forms, ok := r.values[victim]
		if !ok {
			continue
		}
		delete(r.values, victim)
		// Recompute the first-byte probe set from the surviving forms so an
		// evicted value's first byte does not linger and force needless passes.
		for _, f := range forms {
			if stillUsed := r.formStillUsedLocked(f); !stillUsed {
				delete(r.forms, f)
			}
		}
		r.rebuildFirstsLocked()
		r.replacer = nil
	}
}

// formStillUsedLocked reports whether any still-registered value keeps form f.
func (r *Registry) formStillUsedLocked(f string) bool {
	for _, forms := range r.values {
		if slices.Contains(forms, f) {
			return true
		}
	}
	return false
}

func (r *Registry) rebuildFirstsLocked() {
	r.firsts = make(map[byte]struct{})
	for f := range r.forms {
		if f != "" {
			r.firsts[f[0]] = struct{}{}
		}
	}
}

// Enabled reports whether the registry is currently scrubbing.
func Enabled() bool { return def.Enabled() }

// Enabled is the per-registry accessor.
func (r *Registry) Enabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabled
}

// SetEnabled turns the registry's scrubbing on or off. Disabling leaves text
// untouched; registration still records values so a later enable is complete.
func SetEnabled(on bool) { def.SetEnabled(on) }

// SetEnabled is the per-registry form.
func (r *Registry) SetEnabled(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = on
}

// Len reports the number of distinct registered values. Intended for tests and
// diagnostics.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.values)
}

// Reset clears the registry back to empty. Intended for tests.
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values = make(map[string][]string)
	r.forms = make(map[string]struct{})
	r.firsts = make(map[byte]struct{})
	r.order = nil
	r.replacer = nil
	r.enabled = true
}
