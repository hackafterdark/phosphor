package tools

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/otel"
	"go.opentelemetry.io/otel/attribute"
)

//go:embed web_search.md.tpl
var webSearchDescriptionTmpl []byte

var webSearchDescriptionTpl = template.Must(
	template.New("webSearchDescription").
		Parse(string(webSearchDescriptionTmpl)),
)

// NewWebSearchTool creates a web search tool for sub-agents. It requires an
// *http.Client — typically the shared client that carries the securityTransport.
// If nil is passed, the function returns an error so that no developer
// accidentally initializes this tool without security controls.
func NewWebSearchTool(client *http.Client) fantasy.AgentTool {
	if client == nil {
		panic("NewWebSearchTool: client is nil (missing securityTransport)")
	}

	return fantasy.NewParallelAgentTool(
		WebSearchToolName,
		renderToolDescription(webSearchDescriptionTpl),
		func(ctx context.Context, params WebSearchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool web_search")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", WebSearchToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)
			if params.Query == "" {
				return fantasy.NewTextErrorResponse("query is required"), nil
			}

			maxResults := params.MaxResults
			if maxResults <= 0 {
				maxResults = 10
			}
			if maxResults > 20 {
				maxResults = 20
			}

			// Each search round trips through the same client (and therefore
			// the same securityTransport), which already handles the IP-block
			// policy, layered trust hierarchy, and TUI allow-prompt.
			results, err := searchDuckDuckGo(ctx, client, params.Query, maxResults)
			span.SetAttributes(
				attribute.Int("gen_ai.tool.web_search.results", len(results)),
			)
			if err != nil {
				slog.Debug("Web search failed", "query", params.Query, "err", err)
				return fantasy.NewTextErrorResponse("Failed to search: " + err.Error()), nil
			}
			slog.Debug("Web search completed", "query", params.Query, "results", len(results))

			return fantasy.NewTextResponse(sanitizeSearchResults(formatSearchResults(results))), nil
		},
	)
}

// keyEntropyRe is a regex that flags obvious high-entropy key material.
// It looks for patterns such as:
//   - 40+ alphanumeric characters in a single word (matches base64 / hex / random keys)
//   - obvious key prefixes (e.g. sk_, APIKEY_, etc.)
var keyEntropyRe = regexp.MustCompile(`(?:[A-Za-z0-9]{40,}|(?:sk[_-]?[A-Za-z0-9]{20,}|API[_-]?KEY[_-]?[A-Za-z0-9]{16,}|TOKEN[_-]?[A-Za-z0-9]{16,}|SECRET[_-]?[A-Za-z0-9]{16,}))`)

// ipLiteralRe matches obvious literal IP patterns (IPv4 / IPv6) that may have
// leaked in search output (e.g. log dumps, API responses).
var ipLiteralRe = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|1?\d\d?)\b|\b(?:[0-9a-fA-F]{1,4}:){2,7}[0-9a-fA-F]{1,4}\b\b`)

// sanitizeSearchResults redacts high-entropy key material and raw IP literals
// from the formatted search results before they are returned to the agent's
// context.
func sanitizeSearchResults(s string) string {
	// Redact high-entropy keys.
	s = keyEntropyRe.ReplaceAllStringFunc(s, func(match string) string {
		// Preserve the first 4 and last 4 characters so the context is useful.
		if len(match) > 8 {
			return match[:4] + "****" + match[len(match)-4:]
		}
		return "****"
	})

	// Redact raw IPs.
	s = ipLiteralRe.ReplaceAllString(s, "[REDACTED IP]")
	return s
}

// randomDelay adds a small random delay (100–150ms) before each search round-trip
// to stay under DuckDuckGo's request-budget threshold.
func randomDelay() {
	time.Sleep(time.Duration(100+rand.IntN(50)) * time.Millisecond)
}

// randomIntN returns a random int in [0, n).
func randomIntN(n int) int {
	if n <= 0 {
		return 0
	}
	return rand.IntN(n)
}

// netParseIP is a small wrapper around net.ParseIP for clarity.
func netParseIP(s string) net.IP {
	return net.ParseIP(s)
}

// stringsEqualFold is a small wrapper around strings.EqualFold for clarity.
func stringsEqualFold(a, b string) bool {
	return strings.EqualFold(a, b)
}

// fmtErrorf is a small wrapper around fmt.Errorf for clarity.
func fmtErrorf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
