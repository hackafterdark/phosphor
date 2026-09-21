// Package egress implements the architectural credential-isolation tier of the
// secret-protection plan (Phase 11): encrypted secret sentinels that are safe to
// carry in the agent's context plus a brokered, host-scoped egress path that
// resolves them back to plaintext only at allowlisted HTTPS hops.
//
// Detection (the gitleaks pass, the known-value registry, the learned memory)
// catches the *accidental* leak and the *unknown-format* secret. This package
// addresses the *malicious-exfiltration* threat that pattern-matching cannot:
// an agent that has been prompt-injected into trying to POST a credential it
// holds out to an attacker host. It does so not by trying to recognise the
// secret at the moment of egress, but by removing the plaintext from the
// agent's reach entirely:
//
//   - A credential that would enter the conversation is replaced by a sealed
//     [SecretStore] token. The model, the transcript, and every tool result ever
//     contain only the sealed token, never the bytes. The token is AES-256-GCM
//     sealed under a process key the agent can neither read nor forge, so it is
//     inert to an exfiltration target and syntactically invalid as a credential
//     (so an echoed edit cannot resurrect a key).
//   - The only place that turns a token back into plaintext is the egress broker
//     (the [Proxy] and [RoundTripperPolicy] in this package), and it does so solely for
//     a destination the operator allowlisted over HTTPS. A token that cannot be
//     resolved is refused outright rather than forwarded as an opaque blob.
//
// It is opt-in and off by default; nothing in the shipped read path changes
// until an operator turns it on.
package egress

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"sync"
)

// Sealed-sentinel tuning. minSealLen is the floor below which a value is not
// worth sealing: a token for a three-byte "secret" is larger than the secret and
// invites a collision with ordinary text, so such a value is left to the plain
// redaction path instead. maxSealLen is the DoS ceiling — sealing a multi-megabyte
// payload would be pointless and would grow the transcript, so an oversized
// candidate is refused and the caller falls back to a plain sentinel.
const (
	minSealLen = 6
	maxSealLen = 1 << 14 // 16 KiB

	// sealPrefix/s sealEnd are the framing of a sealed token:
	// <secret@v1.<label>.<base64url(payload)>.end>. The double-@ (vs the
	// tokenization store's single-colon <secret:...> form) keeps the two token
	// namespaces disinct so neither one's scanner can mis-grab the other's.
	sealPrefix = "<secret@v1."
	sealEnd    = ".end>"

	// nonceKeyBytes is the length of the separate HMAC key used to derive the
	// synthetic GCM nonce. It is distinct from the AES key so a leak of one does
	// not hand over the other.
	nonceKeyBytes = 32
	gcmNonceSize  = 12
	gcmKeySize    = 32
)

// sealPattern matches a sealed token. The label and the base64url payload are
// both restricted to a character set that excludes '.', so the two '.'-separated
// fields plus the fixed prefix and ".end>" suffix parse unambiguously.
var sealPattern = regexp.MustCompile(`<(secret@v1\.)([a-z0-9_-]+)\.([A-Za-z0-9_-]+)\.end>`)

// TokenResolver is the capability the [Proxy] and [RoundTripperPolicy] use to turn a
// sealed token back into plaintext. [SecretStore] implements it; tests substitute
// a fake so the transport logic can be exercised without real cryptography.
type TokenResolver interface {
	// Open returns the plaintext behind a sealed token and reports whether the
	// token was well-formed and authenticated. A false ok means "unresolvable":
	// the caller must refuse the request rather than forward the token as-is.
	Open(token string) (value string, ok bool)
	// HasToken reports whether text carries at least one sealed token.
	HasToken(text string) bool
}

// SecretStore seals secret values into AES-256-GCM tokens and opens them back
// to plaintext. It is the in-process counterpart of openclaw's sentinel.ts: the
// plaintext is never held in a map (unlike the reversible token store), only the
// key — a token is decryptable by anyone who holds the key and worthless to
// anyone who does not.
//
// Two properties make the scheme work inside an agent transcript:
//
//   - Stability by (value, label). The GCM nonce is derived deterministically as
//     HMAC(nonceKey, label||value) rather than drawn at random, so sealing the
//     same credential twice yields the same token. The model therefore sees one
//     stable marker for one credential across files and turns, and we keep no
//     value->token reverse map in memory.
//   - Tamper-evidence and label binding. The GCM auth tag authenticates the
//     payload, and the label is supplied as additional-authenticated-data, so a
//     token cannot be replayed under a different label and any edit of its bytes
//     fails to open.
//
// The store is safe for concurrent use: the key material is written once at
// construction and never mutated, so Open and Seal need only guard the enabled
// flag.
type SecretStore struct {
	mu       sync.RWMutex
	key      []byte // AES-256 master key; never leaves the process
	nonceKey []byte // HMAC key for the synthetic nonce; distinct from key
	aead     cipher.AEAD
	enabled  bool
}

// NewSecretStore builds a store with freshly generated key material. Because the
// key is random per process, a token minted in one session cannot be opened by a
// later one — which is the correct lifetime for an in-context credential handle.
func NewSecretStore() (*SecretStore, error) {
	key := make([]byte, gcmKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, errors.New("egress: failed to derive sealing key")
	}
	nonceKey := make([]byte, nonceKeyBytes)
	if _, err := rand.Read(nonceKey); err != nil {
		return nil, errors.New("egress: failed to derive nonce key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithNonceSize(block, gcmNonceSize)
	if err != nil {
		return nil, err
	}
	return &SecretStore{key: key, nonceKey: nonceKey, aead: aead, enabled: true}, nil
}

// Seal turns value into a token, carrying label (a short credential name such as
// the detector rule id, e.g. "github-pat") in cleartext so the model still knows
// *which* credential is present without holding the credential itself — the same
// vendor-label-preserved trick the read-path sentinels use. It reports ok=false
// for an empty, too-short, or oversized value, or on a disabled store, in which
// case the caller should fall back to a plain non-reusable sentinel.
func (s *SecretStore) Seal(value, label string) (string, bool) {
	if value == "" || len(value) < minSealLen || len(value) > maxSealLen {
		return "", false
	}
	if !s.Enabled() {
		return "", false
	}
	nonce, aad := s.syntheticNonce(label, value)
	sealed := s.aead.Seal(nil, nonce, []byte(value), []byte(aad))
	payload := base64.RawURLEncoding.EncodeToString(concat(nonce, sealed))
	return sealPrefix + sanitizeLabel(label) + "." + payload + sealEnd, true
}

// Open reverses Seal: it returns the plaintext behind a token, or ok=false when
// the token is malformed, tampered with, or bound to a label that does not match
// its own authenticated bytes. The token is authenticated against its own
// embedded label as additional-data, so a token is valid only against the label
// it was minted with — a byte edit to either the payload or the label fails the
// GCM open rather than returning attacker-influenced plaintext.
func (s *SecretStore) Open(token string) (string, bool) {
	if !s.Enabled() {
		return "", false
	}
	if !strings.HasPrefix(token, sealPrefix) || !strings.HasSuffix(token, sealEnd) {
		return "", false
	}
	body := token[len(sealPrefix) : len(token)-len(sealEnd)]
	label, payload, found := strings.Cut(body, ".")
	if !found || label == "" || payload == "" {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(raw) <= gcmNonceSize {
		return "", false
	}
	nonce, sealed := raw[:gcmNonceSize], raw[gcmNonceSize:]
	plain, err := s.aead.Open(nil, nonce, sealed, []byte(label))
	if err != nil {
		return "", false
	}
	return string(plain), true
}

// HasToken reports whether text carries at least one sealed token. It is the
// cheap gate callers use to skip the transform entirely on clean bytes.
func (s *SecretStore) HasToken(text string) bool {
	if text == "" {
		return false
	}
	return sealPattern.MatchString(text)
}

// StripTokens replaces every sealed token in text with an inert marker. It is for
// the write-path detection gate: a sealed token the agent copied into a file is our
// own handle, not a credential, so it must be defanged before the secret detector
// runs on content that is about to hit disk, exactly as the tokenization store's
// tokens are. Without it a base64url token payload could be mistaken for a
// high-entropy finding.
func StripTokens(text string) string {
	if text == "" || !sealPattern.MatchString(text) {
		return text
	}
	return sealPattern.ReplaceAllString(text, "«sent»")
}

// syntheticNonce derives the deterministic per-(label,value) GCM nonce and the
// authenticated label string. Drawing the nonce from HMAC(nonceKey, label||value)
// rather than crypto/rand is what makes a token stable for a given credential;
// RFC 5288 forbids reusing a GCM nonce for the same key, and a repeat here is a
// *repeat of the identical plaintext under the identical AAD*, which GCM tolerates
// (it produces the same ciphertext) but never a distinct plaintext colliding on a
// nonce — that would require an HMAC preimage or collision, i.e. it is infeasible.
func (s *SecretStore) syntheticNonce(label, value string) (nonce, aad []byte) {
	mac := hmac.New(sha256.New, s.nonceKey)
	mac.Write([]byte(label))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	full := mac.Sum(nil)
	return full[:gcmNonceSize], []byte(label)
}

// Enabled reports whether the store seals and opens.
func (s *SecretStore) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// SetEnabled turns sealing/opening on or off. Disabling makes Seal return the
// not-ok fallback and Open report tokens unresolvable, so the broker refuses
// rather than forwards — the fail-closed direction.
func (s *SecretStore) SetEnabled(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = on
}

// sanitizeLabel reduces a free-form credential name to the token alphabet so the
// framing stays parseable. Empty becomes a neutral marker.
func sanitizeLabel(label string) string {
	if label == "" {
		return "secret"
	}
	var b strings.Builder
	for _, r := range label {
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
		return "secret"
	}
	return out
}

func concat(a, b []byte) []byte {
	out := make([]byte, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

// store is the process-wide secret store the read path and the broker share, so
// a token minted by redaction resolves at the proxy without any extra plumbing.
var store = func() *SecretStore {
	s, err := NewSecretStore()
	if err != nil {
		// Unreachable in practice (crypto/rand does not fail on supported
		// platforms). A nil aead would panic on Seal/Open, so a disabled store is
		// the safe fallback: it makes every Seal fall back to a plain sentinel and
		// every broker resolve refuse — no plaintext ever exposed.
		return &SecretStore{}
	}
	return s
}()

// Seal seals value under the process-wide store.
func Seal(value, label string) (string, bool) { return store.Seal(value, label) }

// Open opens a sealed token under the process-wide store.
func Open(token string) (string, bool) { return store.Open(token) }

// HasToken reports whether text carries a sealed token under the process store.
func HasToken(text string) bool { return store.HasToken(text) }

// Store exposes the process-wide secret store (for the broker and tests).
func Store() *SecretStore { return store }

// SetEnabled toggles the process-wide store. It is frozen from configuration at
// startup, the same way the read-path redaction snapshot is, so a mid-session
// change cannot silently disarm credential isolation.
func SetEnabled(on bool) { store.SetEnabled(on) }

// ResetForTest reseeds the process-wide store with fresh key material and the
// enabled flag. Intended for tests only.
func ResetForTest(on bool) error {
	s, err := NewSecretStore()
	if err != nil {
		return err
	}
	s.SetEnabled(on)
	store = s
	return nil
}
