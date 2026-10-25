package sandbox

import (
	"fmt"
	"strconv"
	"strings"
)

// seatbeltPrivatePrefixes are the macOS root-level symlinks into /private.
// Seatbelt matches rules against canonicalized paths, so a rule for
// /var/folders/... must also carry the /private/var/folders/... variant
// or writes to the OS temp dir spuriously fail.
var seatbeltPrivatePrefixes = []string{"/etc", "/tmp", "/var"}

// seatbeltCanonicalVariants returns the path plus its /private-anchored
// twin when it hangs off a symlinked root.
func seatbeltCanonicalVariants(path string) []string {
	for _, prefix := range seatbeltPrivatePrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return []string{path, "/private" + path}
		}
	}
	return []string{path}
}

// seatbeltRuleParams renders the SBPL parameter list for the given paths.
// kind is "subpath" for directories and "literal" for single files.
func seatbeltRuleParams(kind string, paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		for _, v := range seatbeltCanonicalVariants(p) {
			fmt.Fprintf(&b, "(%s %s) ", kind, strconv.Quote(v))
		}
	}
	return b.String()
}

// seatbeltNetworkRules renders the network posture for a Spec. The
// default keeps raw egress open (v1 workspace tier; hostname policy
// lives in the egress proxy until the ProxyOnly tier lands, §5 lesson
// 1). ProxyOnly makes the broker the only egress path: raw egress is
// denied and only the loopback broker port stays reachable.
func seatbeltNetworkRules(spec Spec) string {
	switch spec.Network {
	case NetworkDeny:
		return "(deny network*)\n\n"
	case NetworkProxyOnly:
		if spec.ProxyPort <= 0 || spec.ProxyPort > 65535 {
			// Never leave the raw surface open on a bad port;
			// callers validate, but the profile must fail closed.
			return "(deny network*)\n\n"
		}
		var b strings.Builder
		b.WriteString("(deny network*)\n")
		// Outbound to the broker only; the client-side implicit
		// bind to an ephemeral local port is allowed so connect()
		// can pick its source port.
		fmt.Fprintf(&b, "(allow network-outbound (remote ip \"127.0.0.1:%d\"))\n", spec.ProxyPort)
		b.WriteString("(allow network-bind (local ip \"*:*\"))\n\n")
		return b.String()
	default:
		return "(allow network*)\n\n"
	}
}

// seatbeltProfile renders the SBPL for a Spec. Reads are allowed globally
// per the plan's macOS position (writes are the real boundary); the
// kernel-enforced write set is workspace plus the spec's RW roots and
// files. This file is platform-agnostic on purpose so the profile corpus
// tests run on every host, not just macOS.
func seatbeltProfile(spec Spec) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n\n")

	// Base capabilities every confined dev-tool command needs. Under
	// (deny default) these grants are the entire positive surface.
	b.WriteString("(allow process-exec)\n")
	b.WriteString("(allow process-fork)\n")
	b.WriteString("(allow signal (target same-sandbox))\n")
	b.WriteString("(allow file-ioctl (regex #\"^/dev/\"))\n")
	b.WriteString("(allow sysctl-read)\n")
	// DNS resolution and TLS trust evaluation reach these Mach
	// services even when raw network access is allowed; under
	// (deny default) getaddrinfo and certificate validation fail
	// without these grants.
	b.WriteString("(allow mach-lookup\n")
	b.WriteString("  (global-name \"com.apple.dnssd.service\")\n")
	b.WriteString("  (global-name \"com.apple.SystemConfiguration.configd\")\n")
	b.WriteString("  (global-name \"com.apple.SystemConfiguration.DNSConfiguration\")\n")
	b.WriteString("  (global-name \"com.apple.SecurityServer\")\n")
	b.WriteString("  (global-name \"com.apple.trustd.agent\"))\n")
	b.WriteString(seatbeltNetworkRules(spec))

	// Global reads; the write rules below are the boundary.
	b.WriteString("(allow file-read*)\n\n")

	// Deny-read overrides: credential material stays unreadable even
	// though the global read grant is open. Seatbelt deny rules win
	// over allows, so order is defensive only.
	if params := seatbeltRuleParams("subpath", spec.DenyRead); params != "" {
		fmt.Fprintf(&b, "(deny file-read* %s)\n\n", params)
	}

	// Writes: workspace plus RW roots and files, kernel-enforced.
	params := seatbeltRuleParams("subpath", []string{spec.WorkspaceRoot})
	fmt.Fprintf(&b, "(allow file-write* %s)\n", params)
	if params := seatbeltRuleParams("subpath", spec.ReadWriteRoots); params != "" {
		fmt.Fprintf(&b, "(allow file-write* %s)\n", params)
	}
	if params := seatbeltRuleParams("literal", spec.ReadWriteFiles); params != "" {
		fmt.Fprintf(&b, "(allow file-write* %s)\n", params)
	}
	return b.String()
}
