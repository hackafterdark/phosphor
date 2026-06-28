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
	"time"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"html/template"
	_ "embed"
)

//go:embed web_fetch.md.tpl
var webFetchDescriptionTmpl []byte

var webFetchDescriptionTpl = template.Must(
	template.New("webFetchDescription").
		Parse(string(webFetchDescriptionTmpl)),
)

// allowList is the in-memory cache of domains that have been explicitly approved.
// Session-local: cleared when the user's session restarts.
var allowList = map[string]bool{}

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

// RoundTrip intercepts each request, validates the host against the trust
// hierarchy (deny list → workspace allow list → global allow list → user prompt),
// then delegates to the inner transport.
func (t *securityTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host

	// Strip the port for allow-list lookups.
	lookupHost := stripPort(host)

	ctx := req.Context()
	spanCtx, span := otel.StartSpan(ctx, "web_fetch network request")
	defer span.End()

	// Resolve host if it is a raw IP → fail-closed.
	if ip := net.ParseIP(lookupHost); ip != nil {
		// localhost is always allowed.
		if lookupHost == "127.0.0.1" || lookupHost == "::1" {
			span.SetAttributes(
				attribute.Bool("gen_ai.network.is_ip", true),
				attribute.String("gen_ai.network.target", lookupHost),
				attribute.String("gen_ai.network.source", "localhost"),
			)
			return t.inner.RoundTrip(req.WithContext(spanCtx))
		}
		// Check if the IP is in the config allow list.
		if t.cfg != nil && isIPAllowedByConfig(t.cfg, lookupHost) {
			span.SetAttributes(
				attribute.Bool("gen_ai.network.is_ip", true),
				attribute.String("gen_ai.network.target", lookupHost),
				attribute.String("gen_ai.network.source", "config_allow"),
			)
			return t.inner.RoundTrip(req.WithContext(spanCtx))
		}
		// Raw IP without allow list → fail-closed. No allowFn.
		span.SetAttributes(
			attribute.Bool("gen_ai.network.is_ip", true),
			attribute.String("gen_ai.network.target", lookupHost),
			attribute.String("gen_ai.network.source", "block"),
		)
		return nil, fmt.Errorf("web_fetch request to %s blocked (raw IP)", lookupHost)
	}

	// Check layered trust hierarchy.
	if !t.isAllowed(lookupHost, span) {
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
		allowList[lookupHost] = true
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
	if allowList[host] {
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

// NewWebFetchTool constructs a web fetch tool that uses the security transport
// and invokes the TUI permission prompt when a host is not yet approved.
func NewWebFetchTool(workingDir string, client *http.Client, allowFn func(ctx context.Context, host string) (bool, error)) fantasy.AgentTool {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 10
	transport.IdleConnTimeout = 90 * time.Second

	secure := newSecurityTransport(transport, allowFn, nil)
	secureClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: secure,
	}

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

			// The securityTransport already handles the trust hierarchy; the
			// helper layer invokes allowFn for a final prompt if the host is
			// not in any allow list.
			content, err := FetchURLAndConvertWithTrust(ctx, secureClient, params.URL, allowFn)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to fetch URL: %s", err)), nil
			}

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
	if !allowList[host] && !isAllowedByGlobal(host) && !isAllowedByWorkspace(host) {
		allowed, err := allowFn(ctx, host)
		if err != nil {
			return "", fmt.Errorf("host %s not approved by user prompt: %w", host, err)
		}
		if !allowed {
			return "", fmt.Errorf("host %s blocked by user policy", host)
		}
		allowList[host] = true
	}

	span.SetAttributes(
		attribute.String("gen_ai.network.target", host),
	)

	return FetchURLAndConvert(ctx, client, rawURL)
}
