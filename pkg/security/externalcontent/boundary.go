package externalcontent

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// Wrap sanitizes content and encloses it in HTML-comment boundary markers
// with a cryptographically random per-call boundary ID.
//
// The random ID is the key security property: an attacker embedding malicious
// content in a web page, email, or tool result cannot know what boundary
// string this particular call will generate, so they cannot craft a fake
// "END-UNTRUSTED-CONTENT" marker to terminate the block early and resume
// writing "trusted" instructions.
//
// source should be a short label describing where the content came from
// (e.g. "web-fetch", "mcp:my-server", "email").  It is printed with %q so
// any embedded special characters are safely escaped.
//
// The model should be instructed (via the system prompt) to treat content
// inside boundary markers as untrusted external input and never execute
// instructions found within.
func Wrap(content, source string) string {
	id := newBoundaryID()
	sanitized := Sanitize(content)
	// "-->" closes an HTML comment; scrub it from the source label so a
	// caller-supplied label cannot break the comment structure.
	safeSource := strings.ReplaceAll(source, "-->", "-- >")
	var b strings.Builder
	b.Grow(len(sanitized) + 160)
	fmt.Fprintf(&b, "<!-- UNTRUSTED-CONTENT source=%q boundary=%s -->\n", safeSource, id)
	b.WriteString(sanitized)
	fmt.Fprintf(&b, "\n<!-- END-UNTRUSTED-CONTENT boundary=%s -->", id)
	return b.String()
}

// newBoundaryID returns a 128-bit cryptographically random hex string.
// The hex alphabet (0-9, a-f) is deliberately chosen so the ID can never
// accidentally contain "-->" and close an HTML comment prematurely.
func newBoundaryID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Unreachable on any supported platform; included for completeness.
		return fmt.Sprintf("fallback-%p", &buf)
	}
	return fmt.Sprintf("%x", buf)
}
