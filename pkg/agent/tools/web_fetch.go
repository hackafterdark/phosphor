package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "embed"
	"html/template"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/egress"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"github.com/hackafterdark/phosphor/pkg/security/externalcontent"
	"github.com/hackafterdark/phosphor/pkg/security/urlguard"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

//go:embed web_fetch.md.tpl
var webFetchDescriptionTmpl []byte

var webFetchDescriptionTpl = template.Must(
	template.New("webFetchDescription").
		Parse(string(webFetchDescriptionTmpl)),
)

// allowList is the in-memory cache of domains that have been explicitly approved.
// Session-local: cleared when the user's session restarts. Guarded by
// allowListMu — web_fetch runs concurrently in parallel tool batches.
var (
	allowListMu sync.RWMutex
	allowList   = map[string]bool{}
)

// hostInAllowList reports whether host was approved earlier in this session.
func hostInAllowList(host string) bool {
	allowListMu.RLock()
	defer allowListMu.RUnlock()
	return allowList[host]
}

// markHostAllowed records host as approved for the rest of the session.
func markHostAllowed(host string) {
	allowListMu.Lock()
	defer allowListMu.Unlock()
	allowList[host] = true
}

// resetAllowList clears the session allow list. Used by tests that need a
// known-clean trust state.
func resetAllowList() {
	allowListMu.Lock()
	defer allowListMu.Unlock()
	allowList = map[string]bool{}
}

// securityTransport is a custom http.RoundTripper that intercepts each outgoing
// request, validates the destination, and applies a layered trust model before
// the request executes.
type securityTransport struct {
	// inner is the underlying transport that actually performs the network call.
	inner http.RoundTripper

	// allowFn is invoked when a host is not yet in any allow list. It shows the
	// TUI permission prompt and returns (allowed, error).
	allowFn func(ctx context.Context, host string) (bool, error)

	// cfg is the phosphor config. When set, the transport consults the
	// web_fetch tool's allowRawIPs + IPAllowList fields before failing a
	// raw-IP destination.
	cfg *config.Config
}

// newSecurityTransport wraps the provided inner transport with an IP-block policy
// and a layered trust interceptor.
func newSecurityTransport(inner http.RoundTripper, allowFn func(ctx context.Context, host string) (bool, error), cfg *config.Config) http.RoundTripper {
	return &securityTransport{
		inner:   inner,
		allowFn: allowFn,
		cfg:     cfg,
	}
}

// Unwrap exposes the inner transport so egress.WrapClient recognizes an
// already-secured client and does not wrap it again.
func (t *securityTransport) Unwrap() http.RoundTripper {
	return t.inner
}

// RoundTrip intercepts each request, validates the host against the trust
// hierarchy (deny list → workspace allow list → global allow list → user prompt),
// then delegates to the inner transport.
func (t *securityTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host

	// Side-channel exfiltration defense at the transport layer: a request whose
	// query string smuggles a known secret or high-entropy token is refused here
	// too, so any caller that reaches this transport directly is still covered.
	if err := urlguard.Check(req.URL.String()); err != nil {
		return nil, err
	}

	// Strip the port for allow-list lookups.
	lookupHost := stripPort(host)

	ctx := req.Context()
	spanCtx, span := otel.StartSpan(ctx, "web_fetch network request")
	defer span.End()

	// Resolve host if it is a raw IP → fail-closed. Bracketed IPv6 literals
	// (e.g. "[::1]") must be un-escaped before parsing; net.ParseIP returns
	// nil for the bracketed form, which would let IPv6 literals fall through
	// to the trust hierarchy un-blocked.
	ipHost := req.URL.Hostname()
	if ip := net.ParseIP(ipHost); ip != nil {
		// localhost is always allowed.
		if ipHost == "127.0.0.1" || ipHost == "::1" {
			span.SetAttributes(
				attribute.Bool("gen_ai.network.is_ip", true),
				attribute.String("gen_ai.network.target", ipHost),
				attribute.String("gen_ai.network.source", "localhost"),
			)
			return t.inner.RoundTrip(req.WithContext(spanCtx))
		}
		// Check if the IP is in the config allow list.
		if t.cfg != nil && isIPAllowedByConfig(t.cfg, ipHost) {
			span.SetAttributes(
				attribute.Bool("gen_ai.network.is_ip", true),
				attribute.String("gen_ai.network.target", ipHost),
				attribute.String("gen_ai.network.source", "config_allow"),
			)
			return t.inner.RoundTrip(req.WithContext(spanCtx))
		}
		// Raw IP without allow list → fail-closed. No allowFn.
		span.SetAttributes(
			attribute.Bool("gen_ai.network.is_ip", true),
			attribute.String("gen_ai.network.target", ipHost),
			attribute.String("gen_ai.network.source", "block"),
		)
		return nil, fmt.Errorf("web_fetch request to %s blocked (raw IP)", ipHost)
	}

	// Check layered trust hierarchy.
	if !t.isAllowed(lookupHost, span) {
		// No prompt handler: fail closed rather than run untrusted code
		// with an unresolved host trust decision.
		if t.allowFn == nil {
			span.SetAttributes(
				attribute.String("gen_ai.network.target", lookupHost),
				attribute.String("gen_ai.network.reason", "no_prompt_handler"),
			)
			return nil, fmt.Errorf(
				"web_fetch request to %s blocked (host not in any allow list and no prompt handler configured)",
				lookupHost,
			)
		}
		// Fall back to user prompt.
		allowed, err := t.allowFn(spanCtx, lookupHost)
		if err != nil {
			span.SetAttributes(
				attribute.String("gen_ai.network.target", lookupHost),
				attribute.String("gen_ai.network.reason", "prompt_error"),
				attribute.String("gen_ai.network.error", err.Error()),
			)
			return nil, err
		}
		if !allowed {
			span.SetAttributes(
				attribute.String("gen_ai.network.target", lookupHost),
				attribute.String("gen_ai.network.reason", "user_declined"),
			)
			return nil, fmt.Errorf("web_fetch request to %s is blocked by user policy", lookupHost)
		}
		// User approved — write to the in-memory allow list so we don't re-prompt.
		markHostAllowed(lookupHost)
	}

	// Tag with allow info before the actual network call.
	span.SetAttributes(
		attribute.String("gen_ai.network.target", lookupHost),
		attribute.String("gen_ai.network.source", "allow_list"),
	)

	return t.inner.RoundTrip(req.WithContext(spanCtx))
}

// recordAllowEvent tags the active span with a metadata event about why the
// host was allowed (session allow list, workspace allow list, or global list).
func recordAllowEvent(span trace.Span, source string) {
	span.SetAttributes(
		attribute.String("gen_ai.network.source", source),
	)
}

// isAllowed checks a host against the in-memory allow list, workspace-level JSON
// allow list, and global-level user allow list. Returns true if any source allows it.
func (t *securityTransport) isAllowed(host string, span trace.Span) bool {
	// 1. In-memory allow list (session-level, populated by user prompt).
	if hostInAllowList(host) {
		recordAllowEvent(span, "session_allow_list")
		return true
	}

	// 2. Workspace-level allow list (.phosphor/allowed_domains.json).
	if isAllowedByWorkspace(host) {
		recordAllowEvent(span, "workspace_allow_list")
		return true
	}

	// 3. Global-level user allow list (~/.phosphor/allowed_domains.json).
	if isAllowedByGlobal(host) {
		recordAllowEvent(span, "global_allow_list")
		return true
	}

	return false
}

// isAllowedByWorkspace checks the project-local allow list (.phosphor/allowed_domains.json).
func isAllowedByWorkspace(host string) bool {
	paths := workspaceAllowPaths()
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			var doms []string
			if err := jsonUnmarshal(data, &doms); err == nil {
				for _, d := range doms {
					if strings.EqualFold(stripPort(d), host) {
						return true
					}
				}
			}
		}
	}
	return false
}

// isAllowedByGlobal checks the user-level allow list (~/.phosphor/allowed_domains.json).
func isAllowedByGlobal(host string) bool {
	p, err := globalAllowPath()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var doms []string
	if err := jsonUnmarshal(data, &doms); err != nil {
		return false
	}
	for _, d := range doms {
		if strings.EqualFold(stripPort(d), host) {
			return true
		}
	}
	return false
}

// workspaceAllowPaths returns the possible locations for a project-local allow list.
func workspaceAllowPaths() []string {
	return []string{
		".phosphor/allowed_domains.json",
		".local/.phosphor/allowed_domains.json",
	}
}

// globalAllowPath returns the path to the user-level allow list under $XDG_CONFIG_HOME/phosphor/
// (or ~/.phosphor/ on systems without XDG).
func globalAllowPath() (string, error) {
	var base string
	if env := os.Getenv("XDG_CONFIG_HOME"); env != "" {
		base = filepath.Join(env, "phosphor")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to resolve user home directory: %w", err)
		}
		base = filepath.Join(home, ".phosphor")
	}
	return filepath.Join(base, "allowed_domains.json"), nil
}

// stripPort removes the :port suffix from a host string, preserving IPv6 brackets.
func stripPort(host string) string {
	if strings.HasPrefix(host, "[") {
		if idx := strings.Index(host, "]"); idx != -1 {
			return host[:idx+1]
		}
	}
	if i := strings.LastIndex(host, ":"); i != -1 {
		return host[:i]
	}
	return host
}

// --- JSON helpers -------------------------------------------------------------
// A small JSON unmarshal wrapper for the allow list. We keep the decoder
// inlined so encoding/json stays scoped to this helper.

// jsonUnmarshal decodes the body as a JSON array of strings.
func jsonUnmarshal(data []byte, v any) error {
	switch v := v.(type) {
	case *[]string:
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*v = arr
		return nil
	}
	return fmt.Errorf("unsupported type %T", v)
}

// isIPAllowedByConfig checks if the given host (raw IP) is allowed by the
// web_fetch tool's config. Returns true if:
//   - 127.0.0.1 or ::1 (localhost)
//   - the host is in the user's IPAllowList (CIDR or literal)
//   - the user has set AllowRawIPs = true
func isIPAllowedByConfig(cfg *config.Config, host string) bool {
	if cfg == nil {
		return false
	}
	// localhost is always allowed.
	if host == "127.0.0.1" || host == "::1" {
		return true
	}
	// AllowRawIPs is the opt-in flag.
	if cfg.Tools.WebFetch.AllowRawIPs {
		for _, entry := range cfg.Tools.WebFetch.IPAllowList {
			if strings.EqualFold(entry, host) {
				return true
			}
			if strings.Contains(entry, "/") {
				if matchCIDR(entry, host) {
					return true
				}
			}
		}
	}
	return false
}

// matchCIDR checks if the given host (IP string) falls within the given CIDR
// notation (e.g. "192.168.0.0/24"). Returns true if the host is in the range.
func matchCIDR(cidr, host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	return network.Contains(ip)
}

// webFetchDeniedCIDRs are the destination ranges refused at dial time when a
// hostname resolves into them (DNS rebinding defense). Loopback is exempt:
// web_fetch's raw-IP policy always allows localhost, and local dev servers are
// legitimate hostnames.
var webFetchDeniedCIDRs = []string{
	"0.0.0.0/8",      // "this" network
	"10.0.0.0/8",     // private
	"100.64.0.0/10",  // CGNAT
	"169.254.0.0/16", // link-local (incl. cloud metadata 169.254.169.254)
	"172.16.0.0/12",  // private
	"192.168.0.0/16", // private
	"::/128",         // IPv6 unspecified
	"FC00::/7",       // IPv6 unique-local
	"FE80::/10",      // IPv6 link-local
}

// proxyForRequest reports the proxy decision for the request the guarded
// dialer is about to dial. The dial target of a proxied request is the
// proxy's address, not the target's, and private proxy infrastructure is
// the operator's own network decision; the egress broker tier remains the
// strict path for that case. The decision must be per-request: a global
// "any proxy env var is set" check would also exempt direct connections
// selected by NO_PROXY, silently disarming the DNS-rebinding defense.
type proxyForRequest func(*http.Request) (*url.URL, error)

// dialAddressAllowed validates the *resolved* socket address of a web_fetch
// dial. The trust hierarchy judges hostnames as strings; this gate judges the
// actual connection target, so a DNS answer that rebinds an approved hostname
// into a private, link-local, or metadata range is refused at the socket
// instead of silently connecting. The resolved address is held to the same
// policy as a raw-IP literal: loopback always allowed, configured allow-list
// (and `allow_raw_ips`) exempt, denied ranges refused, public addresses pass.
func dialAddressAllowed(address string, cfg *config.Config) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() {
		return true
	}
	if cfg != nil {
		if cfg.Tools.WebFetch.AllowRawIPs {
			return true
		}
		for _, cidr := range cfg.Tools.WebFetch.IPAllowList {
			if lit := net.ParseIP(cidr); lit != nil {
				if lit.Equal(ip) {
					return true
				}
				continue
			}
			if matchCIDR(cidr, ip.String()) {
				return true
			}
		}
	}
	for _, cidr := range webFetchDeniedCIDRs {
		if matchCIDR(cidr, ip.String()) {
			return false
		}
	}
	return true
}

// guardedDialer returns a dialer whose ControlContext validates the resolved
// socket address of every web_fetch connection. ControlContext runs after the
// kernel has dialed but before any data flows, so the address judged is the
// address actually used — there is no re-resolution race to exploit.
// proxyCheck reports the effective proxy decision for the current request;
// when a request is routed through a proxy the guard exempts the dial
// (the proxy, not the target, is the socket address).
func guardedDialer(cfg *config.Config, proxyCheck proxyForRequest) *net.Dialer {
	return &net.Dialer{
		ControlContext: func(ctx context.Context, _, address string, _ syscall.RawConn) error {
			if proxyCheck != nil {
				proxy, err := proxyCheck(ctxproxyReq(ctx))
				if err == nil && proxy != nil {
					return nil
				}
			}
			if !dialAddressAllowed(address, cfg) {
				host, _, _ := net.SplitHostPort(address)
				return fmt.Errorf(
					"web_fetch request to %s blocked (resolved address is in a denied range; DNS rebinding defense)",
					host,
				)
			}
			return nil
		},
		KeepAlive: egress.DefaultKeepAlive,
	}
}

// ctxproxyReq returns the request that triggered the dial so the transport's
// Proxy function can decide. The ControlContext callback carries no request,
// so [proxyStash] places it in the context one layer above the dialer.
func ctxproxyReq(ctx context.Context) *http.Request {
	if req, ok := ctx.Value(ctxProxyRequest{}).(*http.Request); ok && req != nil {
		return req
	}
	// No request available: treat the connection as direct.
	return &http.Request{URL: &url.URL{Scheme: "https", Host: "direct.invalid"}}
}

type ctxProxyRequest struct{}

// proxyStash carries the current request into the dial context so the
// guarded dialer's ControlContext can consult the transport's per-request
// proxy decision. http.Transport dials with the request's context, so
// stashing here is enough for the guard to see it.
type proxyStash struct{ base http.RoundTripper }

func (s proxyStash) RoundTrip(r *http.Request) (*http.Response, error) {
	ctx := context.WithValue(r.Context(), ctxProxyRequest{}, r)
	return s.base.RoundTrip(r.WithContext(ctx))
}

// NewSecuredHTTPClient returns an *http.Client whose transport enforces the
// web-fetch trust hierarchy: the IP-block policy on raw-IP destinations, the
// resolved-address dial gate (DNS-rebinding defense), the layered allow
// lists, and the user-prompt fallback for unknown hosts. All network tools
// (web_fetch, fetch, download, agentic_fetch, web_search) must share it.
func NewSecuredHTTPClient(cfg *config.Config, allowFn func(ctx context.Context, host string) (bool, error)) *http.Client {
	baseTransport := egress.NewHTTPTransport()
	guarded := guardedDialer(cfg, baseTransport.Proxy)
	baseTransport.DialContext = guarded.DialContext
	// The guard must survive both egress paths: the inactive path dials
	// through baseTransport (guarded above), the active path replaces it
	// with the broker's direct transport, which needs the guard composed
	// in explicitly.
	guard := func(_ context.Context, _, address string) error {
		if !dialAddressAllowed(address, cfg) {
			host, _, _ := net.SplitHostPort(address)
			return fmt.Errorf(
				"web_fetch request to %s blocked (resolved address is in a denied range; DNS rebinding defense)",
				host,
			)
		}
		return nil
	}
	secure := newSecurityTransport(
		egress.Transport(proxyStash{baseTransport}, egress.WithDialGuard(guard)),
		allowFn, cfg,
	)
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: secure,
	}
}

// NewWebFetchTool constructs a web fetch tool that uses the security transport
// and invokes the TUI permission prompt when a host is not yet approved.
func NewWebFetchTool(workingDir string, cfg *config.Config, allowFn func(ctx context.Context, host string) (bool, error)) fantasy.AgentTool {
	secureClient := NewSecuredHTTPClient(cfg, allowFn)

	return fantasy.NewParallelAgentTool(
		WebFetchToolName,
		renderToolDescription(webFetchDescriptionTpl),
		func(ctx context.Context, params WebFetchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool web_fetch")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", WebFetchToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)
			if params.URL == "" {
				return fantasy.NewTextErrorResponse("url is required"), nil
			}

			// Side-channel exfiltration defense: refuse to dial a URL whose query
			// string carries a known secret or high-entropy credential token before
			// any request reaches the network.
			if err := urlguard.Check(params.URL); err != nil {
				span.SetAttributes(attribute.String("gen_ai.network.blocked_reason", "url_credential_guard"))
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			// The securityTransport already handles the trust hierarchy; the
			// helper layer invokes allowFn for a final prompt if the host is
			// not in any allow list.
			content, err := FetchURLAndConvertWithTrust(ctx, secureClient, params.URL, allowFn)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to fetch URL: %s", err)), nil
			}

			// Frame the page as untrusted external content so an injected page cannot
			// forge chat-template tokens or an early boundary marker to hijack the
			// agent. Applies to both the inline result and the saved large-page file.
			content = externalcontent.Wrap(content, "web-fetch")

			hasLargeContent := len(content) > LargeContentThreshold
			var result strings.Builder

			if hasLargeContent {
				tempFile, err := os.CreateTemp(workingDir, "page-*.md")
				if err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to create temporary file: %s", err)), nil
				}
				tempFilePath := tempFile.Name()

				if _, err := tempFile.WriteString(content); err != nil {
					_ = tempFile.Close() // Best effort close
					return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to write content to file: %s", err)), nil
				}
				if err := tempFile.Close(); err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to close temporary file: %s", err)), nil
				}

				fmt.Fprintf(&result, "Fetched content from %s (large page)\n\n", params.URL)
				fmt.Fprintf(&result, "Content saved to: %s\n\n", tempFilePath)
				result.WriteString("Use the view and grep tools to analyze this file.")
			} else {
				fmt.Fprintf(&result, "Fetched content from %s:\n\n", params.URL)
				result.WriteString(content)
			}

			return fantasy.NewTextResponse(result.String()), nil
		},
	)
}

// FetchURLAndConvertWithTrust performs a trust-checked fetch using the
// securityTransport. It is the entry point used by the tool closure: if a
// host is not in any allow list and the in-memory cache hasn't yet approved it,
// the caller invokes allowFn to prompt the user before proceeding.
func FetchURLAndConvertWithTrust(ctx context.Context, client *http.Client, rawURL string, allowFn func(ctx context.Context, host string) (bool, error)) (string, error) {
	ctx, span := otel.StartSpan(ctx, "fetch and convert")
	defer span.End()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL %q: %w", rawURL, err)
	}

	host := stripPort(parsed.Host)
	if !hostInAllowList(host) && !isAllowedByGlobal(host) && !isAllowedByWorkspace(host) {
		if allowFn == nil {
			// Fail closed, matching securityTransport: a host nobody
			// approved must not be fetched just because no prompt
			// handler was configured (headless / serve mode).
			return "", fmt.Errorf("host %s is not in any allow list and no approval handler is configured", host)
		}
		allowed, err := allowFn(ctx, host)
		if err != nil {
			return "", fmt.Errorf("host %s not approved by user prompt: %w", host, err)
		}
		if !allowed {
			return "", fmt.Errorf("host %s blocked by user policy", host)
		}
		markHostAllowed(host)
	}

	span.SetAttributes(
		attribute.String("gen_ai.network.target", host),
	)

	return FetchURLAndConvert(ctx, client, rawURL)
}
