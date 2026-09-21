package secrets

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// jsonEscapeForm mirrors the package's internal jsonEscape so a test can build
// the same JSON-escaped needle the registry stores, without depending on the
// unexported helper's behaviour drifting into a tautology.
func jsonEscapeForm(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return string(b[1 : len(b)-1])
}

func TestRegistryScrubRemovesRawValue(t *testing.T) {
	t.Parallel()

	r := New(0)
	secret := "sk-live-AbCdEf123456"
	r.Register(secret)

	out := r.Scrub("header: " + secret + " trailing")
	require.NotContains(t, out, secret)
	require.Contains(t, out, knownSecretSentinel)
	require.Contains(t, out, "header:")
	require.Contains(t, out, "trailing")
}

func TestRegistryScrubRemovesEncodedForms(t *testing.T) {
	t.Parallel()

	// Chosen so its raw, URL-encoded, and JSON-escaped forms are all distinct.
	secret := `abc"def_ghi_jkl`
	require.NotEqual(t, url.QueryEscape(secret), secret)
	require.NotEqual(t, jsonEscapeForm(t, secret), secret)

	r := New(0)
	r.Register(secret)

	for name, form := range map[string]string{
		"raw":          secret,
		"url_encoded":  url.QueryEscape(secret),
		"json_escaped": jsonEscapeForm(t, secret),
	} {
		t.Run(name, func(t *testing.T) {
			out := r.Scrub("prefix " + form + " suffix")
			require.NotContains(t, out, form)
			require.Contains(t, out, knownSecretSentinel)
		})
	}
}

func TestRegistrySkipsShortValues(t *testing.T) {
	t.Parallel()

	r := New(0)
	r.Register("abcde") // 5 bytes, below the 6-byte floor.
	require.Equal(t, 0, r.Len())
	require.Equal(t, "value=abcde", r.Scrub("value=abcde"))

	r.Register("abcdef") // 6 bytes, at the floor.
	require.Equal(t, 1, r.Len())
	require.NotContains(t, r.Scrub("v=abcdef"), "abcdef")
}

func TestRegistryPrefilterNoOpOnCleanText(t *testing.T) {
	t.Parallel()

	r := New(0)
	r.Register("zzz-abc")
	// Text shares no byte with any registered first byte (z, and the encoded
	// forms' '%'), so the prefilter returns the input untouched.
	clean := "The quick brown fox jumps 12345"
	require.Equal(t, clean, r.Scrub(clean))
}

func TestRegistryBoundedEviction(t *testing.T) {
	t.Parallel()

	r := New(3)
	first := "aaaa-1111"
	second := "bbbb-2222"
	third := "cccc-3333"
	fourth := "dddd-4444"
	r.Register(first)
	r.Register(second)
	r.Register(third)
	r.Register(fourth) // pushes past the bound, evicting the oldest.

	require.LessOrEqual(t, r.Len(), 3)
	require.NotContains(t, r.Scrub("x "+fourth), fourth)
	require.NotContains(t, r.Scrub("x "+third), third)
	// The first-registered value was evicted and no longer matched.
	require.Contains(t, r.Scrub("x "+first), first)
}

func TestRegistryDisabledPassthrough(t *testing.T) {
	t.Parallel()

	r := New(0)
	secret := "abcd-1234"
	r.Register(secret)
	require.NotContains(t, r.Scrub("hold "+secret), secret)

	r.SetEnabled(false)
	require.False(t, r.Enabled())
	require.Contains(t, r.Scrub("hold "+secret), secret)
}

func TestRegistryEmptyAndReset(t *testing.T) {
	t.Parallel()

	r := New(0)
	require.Equal(t, "", r.Scrub(""))

	r.Register("abcd-1234")
	require.Equal(t, 1, r.Len())
	r.Reset()
	require.Equal(t, 0, r.Len())
	require.True(t, r.Enabled())
	require.Equal(t, "abcd-1234", r.Scrub("abcd-1234"))
}

func TestRegistryConcurrentScrub(t *testing.T) {
	t.Parallel()

	r := New(0)
	r.Register("conc-abc-1234")
	text := "token=conc-abc-1234 end"

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				out := r.Scrub(text)
				if strings.Contains(out, "conc-abc-1234") {
					t.Errorf("value leaked through concurrent scrub")
				}
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
