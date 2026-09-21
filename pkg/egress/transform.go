package egress

import (
	"io"
	"strings"
)

// maxDefaultCarry bounds how many bytes the streaming [Transform] will hold in
// its carry-buffer while waiting for a possibly-incomplete sentinel to finish.
// A well-formed sentinel is far shorter than this, so hitting the bound means the
// "<" we were holding was not the start of a real token after all; the buffer is
// then flushed as ordinary bytes. The bound is the DoS guard the plan calls for:
// without it, an endless stream of "<" characters would grow the buffer forever.
const maxDefaultCarry = 1 << 16 // 64 KiB

// ltByte is the ASCII byte for the token opening character. It is spelled as a
// number rather than as a character literal because the token framing character is
// also the HTML/security-significant '<', and several tooling layers in the
// transcript pipeline re-quote that literal; a named numeric constant keeps the
// byte comparisons in safeSplit unambiguous regardless.
const ltByte byte = 0x3C

// RestoreString is the whole-buffer form of the sentinel transform: it replaces
// every resolvable sealed token in text with its plaintext and reports how many
// were resolved and how many were present but unresolvable. It is what the broker
// uses on a fully-buffered, size-bounded request body, where streaming buys
// nothing over a single regex pass.
//
// An unresolvable token is left in place by this function; it is the caller's
// job (the [Proxy]/[RoundTripperPolicy]) to refuse the request when unresolved > 0, so
// that an opaque handle never reaches the destination.
func RestoreString(text string, r TokenResolver) (out string, resolved, unresolved int) {
	if text == "" || r == nil || !r.HasToken(text) {
		return text, 0, 0
	}
	var b strings.Builder
	last := 0
	for _, loc := range sealPattern.FindAllStringIndex(text, -1) {
		if len(loc) < 2 {
			continue
		}
		token := text[loc[0]:loc[1]]
		if plain, ok := r.Open(token); ok {
			b.WriteString(text[last:loc[0]])
			b.WriteString(plain)
			last = loc[1]
			resolved++
		} else {
			unresolved++
		}
	}
	if resolved == 0 {
		return text, 0, unresolved
	}
	b.WriteString(text[last:])
	return b.String(), resolved, unresolved
}

// Transform is the streaming, byte-level sentinel resolver the plan specifies: it
// wraps an upstream reader and emits its bytes downstream with every sealed token
// replaced by its plaintext, while never emitting a byte that could still turn out
// to be part of an unfinished token.
//
// It carries a token that is split across read boundaries: if a chunk ends in the
// middle of "<secret@v1.…", those trailing bytes are held in a carry-buffer and
// the next chunk is pulled to decide them, rather than flushing a partial token
// as if it were data. The carry-buffer is size-bounded so a hostile or malformed
// stream cannot grow it without limit; once a suspected token start exceeds the
// bound it is released as ordinary bytes.
//
// An unresolvable token is passed through unchanged (the transport layer decides
// whether to refuse). Transform itself never errors on a bad token so the framing
// stays decoupled from the policy.
type Transform struct {
	upstream io.Reader
	resolver TokenResolver
	carry    []byte // bytes we have read but cannot yet safely emit
	eof      bool   // upstream reached EOF
	maxCarry int
	err      error

	pending []byte // safe bytes decoded but not yet handed to the caller
}

// NewTransform wraps upstream. resolver may be nil, in which case the Transform is
// a pass-through (useful for tests and for a disabled broker).
func NewTransform(upstream io.Reader, resolver TokenResolver) *Transform {
	return &Transform{upstream: upstream, resolver: resolver, maxCarry: maxDefaultCarry}
}

// Read fills p with the next safe, transformed bytes from upstream, mirroring
// io.Reader. It returns io.EOF exactly once upstream is drained and every byte has
// been flushed.
//
// Invariant it preserves: a byte is never handed to the caller while it could still
// be part of an unfinished token. Read either has such a byte buffered in pending,
// or can flush a provably-safe prefix, or must pull more upstream to decide.
func (t *Transform) Read(p []byte) (int, error) {
	if len(t.pending) > 0 {
		n := copy(p, t.pending)
		t.pending = t.pending[n:]
		return n, nil
	}
	for {
		if t.err != nil {
			return 0, t.err
		}

		s := string(t.carry)

		// A) A complete token is present. Everything before it is provably safe (the
		// regex returns the leftmost match, so no token begins in that prefix), so
		// flush the prefix plus the resolved token and keep scanning the remainder.
		if loc := sealPattern.FindStringIndex(s); len(loc) == 2 {
			lo, hi := loc[0], loc[1]
			plain := s[lo:hi]
			if t.resolver != nil {
				if v, ok := t.resolver.Open(plain); ok {
					plain = v
				}
			}
			t.pending = append(append([]byte{}, s[:lo]...), plain...)
			t.carry = []byte(s[hi:])
			n := copy(p, t.pending)
			t.pending = t.pending[n:]
			return n, nil
		}

		// B) No complete token. Find the earliest point from which one could still be
		// forming; the bytes before it are safe to release.
		flush, carryStart := t.safeSplit(s)
		if flush > 0 {
			t.pending = append([]byte{}, s[:flush]...)
			t.carry = []byte(s[carryStart:])
			n := copy(p, t.pending)
			t.pending = t.pending[n:]
			return n, nil
		}

		// Nothing safe to release right now.
		if t.eof {
			// Upstream is drained, so any carried bytes can never complete a token;
			// release them verbatim and then report EOF.
			if len(t.carry) > 0 {
				t.pending = append([]byte{}, t.carry...)
				t.carry = nil
				n := copy(p, t.pending)
				t.pending = t.pending[n:]
				return n, nil
			}
			return 0, io.EOF
		}

		// Pull another chunk and re-evaluate.
		var tmp [4096]byte
		n, err := t.upstream.Read(tmp[:])
		if n > 0 {
			t.carry = append(t.carry, tmp[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				t.eof = true
				continue
			}
			t.err = err
			if len(t.carry) == 0 {
				return 0, err
			}
		}
	}
}

// safeSplit returns (flush, carryStart) for the no-complete-token case: bytes up to
// flush are safe to emit, and carryStart is where the (possible) unfinished token
// begins. When nothing could still be forming, flush is the whole length and
// carryStart == flush.
func (t *Transform) safeSplit(s string) (flush, carryStart int) {
	for i := 0; i < len(s); i++ {
		if s[i] != ltByte {
			continue
		}
		rest := s[i:]
		if !couldBePartialToken(rest) {
			continue
		}
		if len(rest) > t.maxCarry {
			// Over the DoS bound: this "<" was not a token after all; keep scanning
			// past it rather than carry a runaway buffer.
			continue
		}
		return i, i
	}
	return len(s), len(s)
}

// couldBePartialToken reports whether s, which begins at a "<", could still grow
// into a complete token: either it is a strict prefix of the token prefix, or it
// has the full prefix but has not yet seen its ".end>" close.
func couldBePartialToken(s string) bool {
	if len(s) == 0 || s[0] != ltByte {
		return false
	}
	if len(s) < len(sealPrefix) && strings.HasPrefix(sealPrefix, s) {
		return true // still typing out "<secret@v1."
	}
	if !strings.HasPrefix(s, sealPrefix) {
		return false
	}
	return !strings.Contains(s, sealEnd) // it has the full prefix but no close yet
}
