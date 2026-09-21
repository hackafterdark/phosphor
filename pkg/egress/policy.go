package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
)

// Policy is the net-policy allowlist model the egress broker enforces. It is the
// socket-layer counterpart to the argv-level NetworkBlocker in pkg/shell: where
// that inspects a command line, this inspects the actual connection target, so a
// request that reaches the proxy is judged on where it is really going rather
// than on what it claimed to be.
//
// The model is deliberately deny-by-default: with Enabled set and no hosts in
// AllowedHosts, every destination is refused. This mirrors the openclaw egress
// posture and the plan's "allowlisted HTTPS hops" wording — a credential may
// only ever be resolved toward a host the operator named explicitly.
type Policy struct {
	// Enabled is the master switch. When false, the broker is inert and callers
	// should behave as though isolation were off.
	Enabled bool
	// HTTPSOnly rejects any scheme other than https. Cleartext egress cannot carry
	// a resolved secret safely, so the default posture keeps it on.
	HTTPSOnly bool
	// AllowedHosts is the destination allowlist. An entry is matched, in order of
	// specificity, as an exact host ("api.example.com"), a single-label wildcard
	// ("*.example.com" matches "a.example.com" but not "example.com" or a nested
	// "a.b.example.com"), or a bare suffix domain ("example.com" is treated as
	// itself plus ".example.com" subdomains). Matching is case-insensitive and
	// ignores any port.
	AllowedHosts []string
	// DenyPrivateIPs refuses destinations whose resolved addresses fall in the
	// loopback, private/RFC1918, link-local, and cloud-metadata ranges even if
	// they would otherwise be allowlisted.
	DenyPrivateIPs bool
	// LookupHost, when set, overrides the system resolver for policy checks. It is
	// an advanced/testing hook so the resolved-address gate can be exercised without
	// depending on public DNS; production leaves it nil and uses net.DefaultResolver.
	LookupHost func(ctx context.Context, host string) ([]string, error)
	// MaxBodyBytes bounds how much request body the broker will buffer to scan and
	// rewrite. A body past it is refused rather than streamed unbounded — the DoS
	// ceiling on the sentinel transform.
	MaxBodyBytes int64
}

// Action is a policy decision.
type Action int

const (
	// Allow means the request may proceed (and, for the broker, its sentinels may
	// be resolved before forwarding).
	Allow Action = iota
	// Deny means the request must be refused outright.
	Deny
)

// DefaultMaxBodyBytes bounds the buffered egress body when a Policy does not set
// MaxBodyBytes. It is generous enough for a large JSON POST yet small enough that
// a prompt-injected "curl huge" cannot pin unbounded memory in the broker.
const DefaultMaxBodyBytes int64 = 8 << 20 // 8 MiB

// ErrUnresolvedSentinel is returned when a request carries a sealed token that
// the store cannot open. The broker refuses such a request rather than forward an
// opaque token to the destination, which is the whole point of brokering: an
// unresolvable handle never leaves the host.
var ErrUnresolvedSentinel = errors.New("egress: request carries an unresolvable secret sentinel")

// deniedCIDRs are the always-refused destination ranges when DenyPrivateIPs is on.
var deniedCIDRs = []string{
	"0.0.0.0/8",      // "this" network
	"10.0.0.0/8",     // private
	"100.64.0.0/10",  // CGNAT
	"127.0.0.0/8",    // loopback
	"169.254.0.0/16", // link-local (incl. cloud metadata 169.254.169.254)
	"172.16.0.0/12",  // private
	"192.168.0.0/16", // private
	"::1/128",        // IPv6 loopback
	"FC00::/7",       // IPv6 unique-local
	"FE80::/10",      // IPv6 link-local
}

func parseDeniedCIDRs() []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(deniedCIDRs))
	for _, c := range deniedCIDRs {
		if _, n, err := net.ParseCIDR(c); err == nil && n != nil {
			nets = append(nets, n)
		}
	}
	return nets
}

var deniedNets = parseDeniedCIDRs()

// Check judges a parsed request target. It is the single decision the Proxy and
// RoundTripper call before touching the network; returning Deny with a human
// reason lets them fail the request with a precise message rather than a bare
// error.
func (p Policy) Check(target *url.URL) (Action, string) {
	if target == nil {
		return Deny, "no request target"
	}
	scheme := strings.ToLower(target.Scheme)
	if p.HTTPSOnly && scheme != "https" {
		return Deny, "only https egress is permitted"
	}
	hostport := target.Host
	if hostport == "" {
		hostport = target.Hostname()
	}
	return p.CheckHost(hostport)
}

// CheckHost judges a CONNECT tunnel or a bare host[:port] the broker is asked to
// open. It applies the same allowlist, port, and private-IP rules as Check so the
// tunnel decision and the request decision cannot disagree.
func (p Policy) CheckHost(hostport string) (Action, string) {
	host, port := splitHostPort(hostport)
	host = stripHostPort(host)
	if host == "" {
		return Deny, "empty destination host"
	}
	if p.DenyPrivateIPs && p.deniedIP(host) {
		return Deny, "refused private or reserved ip destination: " + host
	}
	if !p.hostAllowed(host) {
		return Deny, "destination not in the egress allowlist: " + host
	}
	if p.HTTPSOnly && !isHTTPSPort(port) {
		return Deny, "only https egress is permitted"
	}
	return Allow, ""
}

// CheckContext judges a parsed request target and, when private-IP denial is on,
// rechecks every address the hostname resolves to before the request is allowed.
// It is the preferred entry point for clients that already have a request context.
func (p Policy) CheckContext(ctx context.Context, target *url.URL) (Action, string) {
	if target == nil {
		return Deny, "no request target"
	}
	scheme := strings.ToLower(target.Scheme)
	if p.HTTPSOnly && scheme != "https" {
		return Deny, "only https egress is permitted"
	}
	hostport := target.Host
	if hostport == "" {
		hostport = target.Hostname()
	}
	return p.CheckHostContext(ctx, hostport)
}

// CheckHostContext applies CheckHost and then resolves the hostname for the
// post-resolution denial gate when private-IP denial is enabled. Literal IPs are
// checked by CheckHost directly, so they do not need another lookup.
func (p Policy) CheckHostContext(ctx context.Context, hostport string) (Action, string) {
	act, reason := p.CheckHost(hostport)
	if act == Deny {
		return act, reason
	}
	if !p.DenyPrivateIPs {
		return Allow, ""
	}
	host, _ := splitHostPort(hostport)
	host = stripHostPort(host)
	if host == "" || net.ParseIP(strings.Trim(host, "[]")) != nil {
		return Allow, ""
	}
	addrs, err := p.lookupHost(ctx, host)
	if err != nil {
		return Deny, fmt.Sprintf("destination %q could not be resolved safely: %v", host, err)
	}
	if err := p.checkResolvedAddresses(host, addrs); err != nil {
		return Deny, err.Error()
	}
	return Allow, ""
}

// ControlContext rejects an already-resolved socket address in a reserved range.
// It is installed on the broker's dialers so a DNS name cannot silently point at
// loopback, a private network, or a cloud-metadata endpoint after the textual
// policy check passed.
func (p Policy) ControlContext(_ context.Context, _, address string, _ syscall.RawConn) error {
	host, _ := splitHostPort(address)
	host = stripHostPort(host)
	if p.DenyPrivateIPs && p.deniedIP(host) {
		return fmt.Errorf("egress: refused private or reserved ip dial target: %s", host)
	}
	return nil
}

// hostAllowed reports whether host matches any allowlist entry.
func (p Policy) hostAllowed(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, pattern := range p.AllowedHosts {
		pat := strings.ToLower(strings.TrimSpace(pattern))
		if pat == "" {
			continue
		}
		pat = strings.TrimSuffix(stripHostPort(pat), ".")
		if pat == "" {
			continue
		}
		switch {
		case pat == h:
			return true
		case strings.HasPrefix(pat, "*."):
			// "*.example.com" matches a single left label of example.com.
			suffix := pat[1:] // ".example.com"
			if strings.HasSuffix(h, suffix) && h != suffix[1:] {
				label := strings.TrimSuffix(h, suffix)
				if label != "" && !strings.Contains(label, ".") {
					return true
				}
			}
		default:
			// A bare domain is treated as "itself or a subdomain of it", which is
			// the usual operator intent when they list a zone name.
			if h == pat || strings.HasSuffix(h, "."+pat) {
				return true
			}
		}
	}
	return false
}

// deniedIP reports whether a resolved IP falls in an always-refused range.
func (p Policy) deniedIP(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range deniedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (p Policy) lookupHost(ctx context.Context, host string) ([]string, error) {
	if p.LookupHost != nil {
		return p.LookupHost(ctx, host)
	}
	return net.DefaultResolver.LookupHost(ctx, host)
}

func (p Policy) checkResolvedAddresses(host string, addrs []string) error {
	if len(addrs) == 0 {
		return fmt.Errorf("egress: %q resolved to no usable address", host)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(strings.Trim(stripHostPort(addr), "[]"))
		if ip == nil {
			return fmt.Errorf("egress: %q resolved to an unusable address: %s", host, addr)
		}
		if p.deniedIP(ip.String()) {
			return fmt.Errorf("egress: %q resolves to a refused private or reserved address: %s", host, ip)
		}
	}
	return nil
}

// MaxBody is the effective body ceiling for the policy.
func (p Policy) MaxBody() int64 {
	if p.MaxBodyBytes <= 0 {
		return DefaultMaxBodyBytes
	}
	return p.MaxBodyBytes
}

// Dialer returns a dialer that enforces the policy's resolved-address rules.
func (p Policy) Dialer() *net.Dialer {
	return &net.Dialer{ControlContext: p.ControlContext, KeepAlive: DefaultKeepAlive}
}

func isHTTPSPort(port string) bool {
	return port == "" || port == "443"
}

func splitHostPort(hostport string) (string, string) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport, ""
	}
	return host, port
}

// stripHostPort removes a trailing ":port" and any IPv6 brackets so the matcher
// and the IP checks both work on a bare host.
func stripHostPort(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err == nil {
		return host
	}
	return strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]")
}
