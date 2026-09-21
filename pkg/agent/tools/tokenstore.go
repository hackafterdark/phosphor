package tools

import (
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Reversible-by-id tokenization (secret-protection plan, Phase 6).
//
// When tokenization is enabled, the read-path redactors replace a detected
// credential with a token of the form <secret:kind:id> and record the id-to-
// value mapping here. The token is a syntactically invalid credential, so the
// model can never echo it back through an edit as a usable key (the
// write-back-corruption class of bug). But, unlike a static sentinel, the
// token carries a stable id we can resolve back to the original value at the
// one place allowed to hold the plaintext again: a write to a trusted local
// file (an .env the agent is legitimately editing or copying). The provider
// wire only ever sees the token, never the bytes.
//
// The map is in-process only. It is never persisted and never written to the
// transcript. Entries expire on a TTL and the store is size-bounded so a long
// session cannot grow it unbounded; both mean a stale token simply fails to
// resolve (fail closed) rather than resurrect a stale secret or leak memory.
type tokenStore struct {
	mu      sync.Mutex
	entries map[string]tokenEntry
	byValue map[string]string // value -> token, so an id is stable per value
	max     int
	ttl     time.Duration
	nowFn   func() time.Time
}

type tokenEntry struct {
	value   string
	kind    string
	issueAt time.Time
}

// tokenPatternRe matches a token produced by Issue. The id alphabet is the
// base64 raw-URL set plus the sec_ prefix; the kind is the sanitized label.
var tokenPatternRe = regexp.MustCompile(`<secret:([a-z0-9_-]+):([A-Za-z0-9_-]+)>`)

// Default tokenization parameters; overridable through the redaction policy.
const (
	defaultTokenTTL        = 30 * time.Minute
	defaultTokenMaxEntries = 4096
	tokenIDPrefix          = "sec_"
	tokenKindSecret        = "secret"
)

var secretTokens = newTokenStore(defaultTokenTTL, defaultTokenMaxEntries)

func newTokenStore(ttl time.Duration, max int) *tokenStore {
	if ttl <= 0 {
		ttl = defaultTokenTTL
	}
	if max <= 0 {
		max = defaultTokenMaxEntries
	}
	return &tokenStore{
		entries: make(map[string]tokenEntry),
		byValue: make(map[string]string),
		max:     max,
		ttl:     ttl,
		nowFn:   time.Now,
	}
}

// Issue records value under a fresh (or, if already seen this session, the
// existing) id and returns the token text to splice into redacted output.
// Re-registering the same value reuses its id so the model sees one stable
// marker for a given credential across files.
func (s *tokenStore) Issue(kind, value string) string {
	if value == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok, ok := s.byValue[value]; ok {
		if e, live := s.entries[tok]; live && !s.expired(e) {
			return tok
		}
		s.deleteLocked(tok)
	}
	if len(s.entries) >= s.max {
		s.evictLocked()
	}
	tok := "<secret:" + sanitizeTokenKind(kind) + ":" + s.newIDLocked() + ">"
	s.entries[tok] = tokenEntry{value: value, kind: kind, issueAt: s.nowFn()}
	s.byValue[value] = tok
	return tok
}

// Lookup returns the value behind a token and reports whether it is present
// and unexpired.
func (s *tokenStore) Lookup(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[token]
	if !ok || s.expired(e) {
		if ok {
			s.deleteLocked(token)
		}
		return "", false
	}
	return e.value, true
}

func (s *tokenStore) expired(e tokenEntry) bool {
	return s.ttl > 0 && s.nowFn().Sub(e.issueAt) > s.ttl
}

// Restore replaces every live token in text with its original value,
// returning the rewritten text and the number of tokens resolved. Expired or
// unknown tokens are left in place: they are inert sentinels, not secrets.
func (s *tokenStore) Restore(text string) (string, int) {
	if text == "" || !tokenPatternRe.MatchString(text) {
		return text, 0
	}
	var restored int
	out := tokenPatternRe.ReplaceAllStringFunc(text, func(tok string) string {
		if v, ok := s.Lookup(tok); ok {
			restored++
			return v
		}
		return tok
	})
	return out, restored
}

// Reset clears the store. Used by tests and on a policy change so tokens issued
// under the previous configuration can never resolve.
func (s *tokenStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]tokenEntry)
	s.byValue = make(map[string]string)
}

func (s *tokenStore) newIDLocked() string {
	var b [9]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; fall back to a
		// time-derived id rather than panic on the read path.
		return tokenIDPrefix + base64.RawURLEncoding.EncodeToString([]byte(time.Now().String()))
	}
	return tokenIDPrefix + base64.RawURLEncoding.EncodeToString(b[:])
}

func (s *tokenStore) deleteLocked(token string) {
	if e, ok := s.entries[token]; ok {
		delete(s.entries, token)
		if cur, ok := s.byValue[e.value]; ok && cur == token {
			delete(s.byValue, e.value)
		}
	}
}

// evictLocked drops the oldest entries (by issue time) down to half capacity.
// Evicting an in-use credential is acceptable: a token that can no longer be
// resolved simply stays redacted, which is the safe direction.
func (s *tokenStore) evictLocked() {
	target := s.max / 2
	if len(s.entries) <= target {
		return
	}
	type tk struct {
		tok string
		at  time.Time
	}
	all := make([]tk, 0, len(s.entries))
	for tok, e := range s.entries {
		all = append(all, tk{tok, e.issueAt})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	for _, e := range all[:len(all)-target] {
		s.deleteLocked(e.tok)
	}
	slog.Debug("Evicted oldest secret tokens", "remaining", len(s.entries))
}

// sanitizeTokenKind reduces a free-form label (usually a gitleaks rule id or a
// structural shape name) to the restricted alphabet the token pattern accepts.
func sanitizeTokenKind(kind string) string {
	if kind == "" {
		return tokenKindSecret
	}
	var b strings.Builder
	for _, r := range kind {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return tokenKindSecret
	}
	return out
}
