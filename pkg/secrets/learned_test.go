package secrets

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLearnedLearnAndKnown(t *testing.T) {
	t.Parallel()

	s := NewLearned(0)
	secret := "sk-live-AbCdEf123456"

	require.False(t, s.Known(secret))
	require.True(t, s.Learn(secret), "first learn should report a new digest")
	require.True(t, s.Known(secret))
	require.False(t, s.Learn(secret), "re-learning the same value is idempotent")
	require.Equal(t, 1, s.Len())
}

func TestLearnedIgnoresShortValues(t *testing.T) {
	t.Parallel()

	s := NewLearned(0)
	require.False(t, s.Learn("abc1234")) // 7 bytes, below the 8-byte floor.
	require.Equal(t, 0, s.Len())
	require.False(t, s.Known("abc1234"))

	require.True(t, s.Learn("abc12345")) // 8 bytes, at the floor.
	require.True(t, s.Known("abc12345"))
}

func TestLearnedTrimsBeforeHashing(t *testing.T) {
	t.Parallel()

	s := NewLearned(0)
	s.Learn("  super-secret-value  ")
	require.True(t, s.Known("super-secret-value"))
	require.True(t, s.Known("  super-secret-value\t"))
}

// Two independently built sets carry different process keys, so the same value
// hashes to different digests. This is the property a bare digest would not
// have: without the process key an outsider holding one set's digests cannot
// confirm whether a candidate value is present.
func TestLearnedDigestIsKeyScoped(t *testing.T) {
	t.Parallel()

	a := NewLearned(0)
	b := NewLearned(0)
	value := "the-same-secret-everywhere"

	require.NotEqual(t, a.Digest(value), b.Digest(value))
	require.Equal(t, a.Digest(value), a.Digest(value), "digest is stable within a set")

	a.Learn(value)
	require.True(t, a.Known(value))
	require.False(t, b.Known(value), "a digest learned under one key is unknown under another")
}

// Regression guard for the HMAC construction. A previous implementation read the
// MAC out of the wrong offset of hmac.Sum and so produced a "digest" that was
// really the first 32 bytes of the plaintext. That leaked secret bytes into the
// token and collapsed every value sharing a 32-byte prefix onto one digest.
// These assertions pin the correct behaviour: the stored digest is the MAC, it
// is never the plaintext, and two values that share only a long prefix stay
// distinguishable.
func TestLearnedDigestIsNotPlaintext(t *testing.T) {
	t.Parallel()

	s := NewLearned(0)
	shared := strings.Repeat("Z", sha256.Size) // exactly the digest width in ASCII 'Z'
	left := shared + "-suffix-left-AAAA"
	right := shared + "-suffix-right-BBBB"

	require.NotEqual(t, hex.EncodeToString([]byte(shared)), s.Digest(left),
		"the digest must be the MAC, not the plaintext's first 32 bytes")
	require.NotEqual(t, hex.EncodeToString([]byte(left)), s.Digest(left),
		"the digest must never be the plaintext itself")
	require.NotEqual(t, s.Digest(left), s.Digest(right),
		"two values sharing a long-but-identical prefix must not collide")

	require.True(t, s.Learn(left))
	require.True(t, s.Known(left))
	require.False(t, s.Known(right), "the sibling sharing only the prefix is not learned")
}

func TestLearnedBoundedEviction(t *testing.T) {
	t.Parallel()

	s := NewLearned(3)
	first := "aaaa-11111111"
	s.Learn(first)
	s.Learn("bbbb-22222222")
	s.Learn("cccc-33333333")
	s.Learn("dddd-44444444") // pushes past the bound, evicting the oldest.

	require.LessOrEqual(t, s.Len(), 3)
	require.False(t, s.Known(first), "oldest learned value is evicted")
	require.True(t, s.Known("dddd-44444444"))
}

func TestLearnedDisabledPassthrough(t *testing.T) {
	t.Parallel()

	s := NewLearned(0)
	secret := "abcd-12345678"
	require.True(t, s.Learn(secret))
	require.True(t, s.Known(secret))

	s.SetEnabled(false)
	require.False(t, s.Enabled())
	require.False(t, s.Known(secret), "a disabled set matches nothing")
	require.False(t, s.Learn("newsecretvalue"), "a disabled set records nothing")
}

func TestLearnedReset(t *testing.T) {
	t.Parallel()

	s := NewLearned(0)
	s.Learn("abcd-12345678")
	require.Equal(t, 1, s.Len())
	s.Reset()
	require.Equal(t, 0, s.Len())
	require.True(t, s.Enabled())
	require.False(t, s.Known("abcd-12345678"))
}

func TestLearnedConcurrentLearnKnown(t *testing.T) {
	t.Parallel()

	s := NewLearned(0)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				v := "conc-" + strings.Repeat("x", 12)
				s.Learn(v)
				_ = s.Known(v)
				_ = s.Digest(v)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
