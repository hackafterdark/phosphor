package agent

import (
	"regexp"
	"strings"

	"charm.land/fantasy"

	"github.com/hackafterdark/phosphor/pkg/agent/tools"
)

// Outgoing-message last-resort redaction (secret-protection plan, point 6).
//
// This runs in PrepareStep, the last hook fantasy calls before every provider
// HTTP request, so it catches credentials or PII that survived into the
// transcript by some path the per-tool redactors did not cover: a value the
// user pasted into their own prompt, or one reintroduced by compaction. It is
// a *masking* layer, never a block: nothing here refuses the request.
//
// It rewrites a cloned copy of the outgoing messages only; the stored session
// is left untouched, so the transcript and the UI still show the real content.
// The secret pass is forced on by default (wire_secret_redaction_force) so it
// survives even a redact_outgoing_secrets opt-out: the provider wire is the hard
// boundary this last-resort layer exists to protect.

// secretKeywords gate the gitleaks pass: a cheap O(n) substring test so the
// (heavier) detector only runs on fragments that plausibly hold a credential.
var secretKeywords = []string{
	"key", "token", "secret", "password", "passwd", "credential", "api_key",
	"apikey", "bearer", "auth", "private", "aws", "azure", "ghp_", "gho_",
	"github_pat", "sk_live", "sk_test", "xox", "ya29", "-----begin", "access_key",
	"client_secret", "connectionstring", "conn_str", "dsn", "pwd",
}

// piiGate is a cheap prefilter for the PII pass. It must never be the reason a
// PII category is missed on the wire, so it is deliberately permissive: a
// fragment is worth scanning when it holds an email '@', a ':' (a MAC or an
// IPv6), a run of four or more digits (SSN / phone / card / IPv4 / IBAN) or a
// run of eight or more alphanumeric characters (a canonical UUID, including the
// all-hex ones that carry almost no digits). The PII pass is opt-in and every
// regex is RE2, so over-triggering here is a cost decision, not a correctness
// one; under-triggering would silently leak a whole category.
func piiGate(text string) bool {
	var digits, alnumRun int
	for _, r := range text {
		switch {
		case r == '@' || r == ':':
			return true
		case r >= '0' && r <= '9':
			digits++
			alnumRun++
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
			alnumRun++
		default:
			alnumRun = 0
		}
		if digits >= 4 || alnumRun >= 8 {
			return true
		}
	}
	return false
}

func couldHoldSecret(text string) bool {
	lower := strings.ToLower(text)
	for _, k := range secretKeywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// redactOutgoingMessages returns a redacted deep copy of msgs. It never mutates
// the input: fantasy owns those messages and may keep the pre-send version.
func (a *sessionAgent) redactOutgoingMessages(msgs []fantasy.Message) []fantasy.Message {
	if !a.outgoingRedactionEnabled() {
		return msgs
	}
	out := make([]fantasy.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = msg
		if len(msg.Content) == 0 {
			continue
		}
		parts := make([]fantasy.MessagePart, len(msg.Content))
		changed := false
		for j, part := range msg.Content {
			tp, ok := part.(fantasy.TextPart)
			if !ok {
				parts[j] = part
				continue
			}
			next := a.redactOutgoingText(tp.Text)
			if next != tp.Text {
				tp.Text = next
				changed = true
			}
			parts[j] = tp
		}
		if changed {
			out[i].Content = parts
		}
	}
	return out
}

// wireSecretsEnabled reports whether the provider-boundary secret mask should
// run. It is forced on whenever either the opt-in redact_outgoing_secrets flag or
// the (default-on) wire_secret_redaction_force hard boundary is set, so turning
// the opt-in off cannot silently drop the last-resort mask at the provider wire.
func (a *sessionAgent) wireSecretsEnabled() bool {
	return a.redactOutgoingSecrets || a.wireSecretsForced
}

// outgoingRedactionEnabled reports whether any provider-wire pass should run at
// all: the forced/optional secret mask or the opt-in PII mask. It is the single
// authority for both the PrepareStep call site and redactOutgoingMessages' own
// early return, so the hard wire_secret_redaction_force boundary cannot be
// bypassed by setting redact_outgoing_secrets=false.
func (a *sessionAgent) outgoingRedactionEnabled() bool {
	return a.wireSecretsEnabled() || a.redactOutgoingPII
}

// redactOutgoingText applies the enabled passes to a single text fragment.
func (a *sessionAgent) redactOutgoingText(text string) string {
	if text == "" {
		return text
	}
	if a.wireSecretsEnabled() && couldHoldSecret(text) {
		text = tools.RedactSecretsForWire(text)
	}
	if a.redactOutgoingPII && piiGate(text) {
		text = maskPII(text)
	}
	return text
}

// ---- PII masker (self-contained, stdlib regexp == RE2 == ReDoS-safe) -------

var (
	piiEmailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?)+`)
	piiSSNRe   = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	// Phone: optional '+', then 10-15 digits possibly grouped by space/-/.()/.,
	// requiring at least 10 digits so version numbers and timestamps do not hit.
	// The leading word boundary stops it eating the numeric tail of a larger
	// alphanumeric token (a hash, order id, or IBAN that failed validation); a
	// real phone number is always delimited by punctuation or whitespace.
	piiPhoneRe = regexp.MustCompile(`\b\+?\d[\d\s\-().]{7,18}\d\b`)
	// IPv4 with octets bounded to 0-255.
	piiIPv4Re = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\b`)
	// Credit card: 13-19 digits, optionally spaced/dashed; Luhn-checked below.
	piiCCRe = regexp.MustCompile(`\b(?:\d[ \-]?){13,23}\d\b`)
	// Canonical UUID (8-4-4-4-12 hex).
	piiUUIDRe = regexp.MustCompile(`\b[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\b`)
	// MAC address, colon or dash separated, six 2-hex-octet groups.
	piiMACRe = regexp.MustCompile(`\b(?:[0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2}\b`)
	// IPv6: the eight-group full form, or a compressed form containing '::'.
	piiIPv6Re = regexp.MustCompile(`\b(?:(?:[0-9A-Fa-f]{1,4}:){7}[0-9A-Fa-f]{1,4}|(?:[0-9A-Fa-f]{1,4}:){1,6}:[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{1,4}){0,6}|::(?:[0-9A-Fa-f]{1,4}:){0,6}[0-9A-Fa-f]{1,4})\b`)
	// IBAN printed either ungrouped or in 4-char blocks; single spaces are
	// tolerated anywhere after the country/check prefix so both printings are
	// caught. Every candidate is still gated on the ISO 13616 mod-97 check in
	// ibanValid, which is what stops ordinary `XX##....` words from matching.
	piiIBANRe = regexp.MustCompile(`\b[A-Z]{2}\d{2} ?[A-Z0-9](?: ?[A-Z0-9]){10,31}\b`)
	// ibanShapeRe is the strict, ungrouped form a candidate must reduce to
	// before its ISO 13616 mod-97 check is run.
	ibanShapeRe = regexp.MustCompile(`^[A-Z]{2}\d{2}[A-Z0-9]{11,30}$`)
)

// maskPII replaces recognised PII spans with typed sentinels. Non-blocking;
// opt-in (default off) because SSN/phone/IP shapes produce false positives on
// ordinary code, logs, and test fixtures.
func maskPII(text string) string {
	if text == "" {
		return text
	}
	// Credit-card first (most specific) so its digit run is not partially
	// consumed by the looser phone/ipv4 patterns afterwards.
	text = piiCCRe.ReplaceAllStringFunc(text, func(m string) string {
		if luhnValid(m) {
			return "<pii:credit-card>"
		}
		return m
	})
	text = piiSSNRe.ReplaceAllString(text, "<pii:ssn>")
	text = piiEmailRe.ReplaceAllString(text, "<pii:email>")
	text = piiUUIDRe.ReplaceAllString(text, "<pii:uuid>")
	// IPv6 before IPv4 and MAC so an eight-group address is never half-eaten by
	// the shorter MAC octet pattern that lives in the same hex alphabet.
	text = piiIPv6Re.ReplaceAllString(text, "<pii:ip6>")
	text = piiIPv4Re.ReplaceAllString(text, "<pii:ip>")
	text = piiMACRe.ReplaceAllString(text, "<pii:mac>")
	// IBAN is masked only when it passes the mod-97 check, which is what keeps
	// ordinary "XX##...." words from matching the loose shape regex.
	text = piiIBANRe.ReplaceAllStringFunc(text, func(m string) string {
		if ibanValid(m) {
			return "<pii:iban>"
		}
		return m
	})
	// Phone runs last so it cannot devour the digit run inside an IBAN, card or
	// IPv4 that an earlier, more specific pattern already handled.
	text = piiPhoneRe.ReplaceAllStringFunc(text, func(m string) string {
		// Keep it only if it really looks like a phone (>=10 digits) and is not
		// just a leftover numeric fragment.
		if digitCount(m) >= 10 {
			return "<pii:phone>"
		}
		return m
	})
	return text
}

func digitCount(s string) int {
	var n int
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// luhnValid reports whether the digits in s pass the Luhn checksum and fall in
// the 13-19 length range used by real card numbers.
func luhnValid(s string) bool {
	digits := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			digits = append(digits, c-'0')
		}
	}
	if n := len(digits); n < 13 || n > 19 {
		return false
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i])
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// ibanValid reports whether s is a real IBAN: two uppercase country letters, a
// two-digit check field, an alphanumeric BBAN, and an ISO 13616 mod-97 check
// equal to 1. The mod-97 gate is what lets the loose shape regex run without
// masking every word that merely looks like "XX##....".
func ibanValid(s string) bool {
	// Grouped printings separate 4-char blocks with spaces; drop them and
	// normalise case before checking the strict form.
	clean := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if !ibanShapeRe.MatchString(clean) {
		return false
	}
	// ISO 13616: rotate the four leading characters to the end, expand every
	// letter into its two-digit value, then reduce the digit stream mod 97.
	// Folding mod-97 at each step keeps the accumulator inside an int.
	rearranged := clean[4:] + clean[:4]
	var rem int
	for i := 0; i < len(rearranged); i++ {
		c := rearranged[i]
		switch {
		case c >= '0' && c <= '9':
			rem = (rem*10 + int(c-'0')) % 97
		case c >= 'A' && c <= 'Z':
			rem = (rem*100 + int(c-'A') + 10) % 97
		default:
			return false
		}
	}
	return rem == 1
}
