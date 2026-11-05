// Package tools provides the tool_search agent tool.
package tools

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"

	"charm.land/fantasy"

	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

//go:embed tool_search.md
var toolSearchDescription string

// ToolSearchToolName is the name of the tool that discovers deferred tool
// schemas. It is itself always advertised: it is the gateway to everything
// else that is deferred.
const ToolSearchToolName = "tool_search"

// DefaultToolSearchLimit is how many deferred tools a fuzzy lookup promotes
// per call.
const DefaultToolSearchLimit = 5

// maxToolSearchLimit caps the caller-provided limit.
const maxToolSearchLimit = 20

// ToolSchemaFingerprint computes a stable fingerprint of a tool's advertised
// schema (name, description, and parameter structure). It guards deferred
// tools against server-side schema drift between discovery and invocation.
func ToolSchemaFingerprint(info fantasy.ToolInfo) string {
	canonical := struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
		Required    []string       `json:"required"`
	}{
		Name:        info.Name,
		Description: info.Description,
		Parameters:  info.Parameters,
		Required:    slices.Clone(info.Required),
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		// A ToolInfo that cannot be marshaled still needs a stable identity;
		// fall back to the name so drift detection degrades to "unchanged".
		return "name:" + info.Name
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// DeferredToolKey identifies a deferred tool by its MCP server and raw
// tool name rather than the concatenated wrapped name. Wrapped names like
// mcp_<server>_<tool> are ambiguous across servers whose names contain
// underscores (server "a_b" with tool "c" and server "a" with tool "b_c"
// both wrap to mcp_a_b_c), so identity must be carried separately.
type DeferredToolKey struct {
	Server string
	Tool   string
}

// DeferredToolRegistry holds the tools whose schemas are hidden from the
// model until tool_search discovers them. The registry resolves candidates
// lazily through a live provider so drift between the deferred snapshot and
// the current MCP tool state is always observable.
type DeferredToolRegistry struct {
	// allowed is the set of tool identities eligible for deferred lookup,
	// captured when the tool list was built (agent permission filters).
	allowed map[DeferredToolKey]bool
	// live returns the current tool instances keyed by identity.
	live func() map[DeferredToolKey]fantasy.AgentTool
	// mu guards allowed (built once, but read concurrently).
	mu sync.RWMutex
}

// NewDeferredToolRegistry returns a registry whose candidates are the
// intersection of allowedKeys and the live provider's current map.
func NewDeferredToolRegistry(allowedKeys []DeferredToolKey, live func() map[DeferredToolKey]fantasy.AgentTool) *DeferredToolRegistry {
	allowed := make(map[DeferredToolKey]bool, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = true
	}
	return &DeferredToolRegistry{allowed: allowed, live: live}
}

// DeferredEntry is one discoverable tool with its identity and live schema
// fingerprint.
type DeferredEntry struct {
	Key  DeferredToolKey
	Tool fantasy.AgentTool
	// RawName is the MCP tool name before wrapping, kept for exact
	// lookups (the wrapper chain hides MCPToolName()).
	RawName     string
	Fingerprint string
}

// Candidates returns the currently available deferred tools, sorted by name
// for deterministic search and reporting.
func (r *DeferredToolRegistry) Candidates() []DeferredEntry {
	r.mu.RLock()
	allowed := r.allowed
	r.mu.RUnlock()

	live := r.live()
	entries := make([]DeferredEntry, 0, len(allowed))
	for key, tool := range live {
		if !allowed[key] {
			continue
		}
		entries = append(entries, DeferredEntry{
			Key:         key,
			Tool:        tool,
			RawName:     key.Tool,
			Fingerprint: ToolSchemaFingerprint(tool.Info()),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Tool.Info().Name < entries[j].Tool.Info().Name
	})
	return entries
}

// Lookup resolves a tool identity against the live registry state and
// returns its current fingerprint. ok is false when the tool has
// disappeared.
func (r *DeferredToolRegistry) Lookup(key DeferredToolKey) (fantasy.AgentTool, string, bool) {
	r.mu.RLock()
	allowed := r.allowed
	r.mu.RUnlock()
	if !allowed[key] {
		return nil, "", false
	}
	tool, ok := r.live()[key]
	if !ok {
		return nil, "", false
	}
	return tool, ToolSchemaFingerprint(tool.Info()), true
}

// DeferAll marks every named tool identity as eligible for deferred lookup.
func (r *DeferredToolRegistry) DeferAll(keys []DeferredToolKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, key := range keys {
		r.allowed[key] = true
	}
}

// Guard wraps a promoted tool so its schema is re-validated against the live
// registry state before every call. If the server-side schema drifted or the
// tool disappeared since discovery, the call is rejected with a
// re-discovery instruction instead of running against a stale contract.
func (r *DeferredToolRegistry) Guard(key DeferredToolKey, tool fantasy.AgentTool, fingerprint string) fantasy.AgentTool {
	return &schemaGuardTool{
		AgentTool:   tool,
		registry:    r,
		key:         key,
		fingerprint: fingerprint,
	}
}

type schemaGuardTool struct {
	fantasy.AgentTool
	registry    *DeferredToolRegistry
	key         DeferredToolKey
	fingerprint string
}

func (g *schemaGuardTool) Run(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
	name := g.Info().Name
	_, current, ok := g.registry.Lookup(g.key)
	if !ok {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"Tool %s is no longer available (the server dropped it or permissions changed). Run tool_search again to discover the current tool set.",
			name,
		)), nil
	}
	if current != g.fingerprint {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"Tool %s changed since it was discovered by tool_search (schema drift). Run tool_search again to load the current schema before calling it.",
			name,
		)), nil
	}
	return g.AgentTool.Run(ctx, params)
}

type toolSearchParams struct {
	Query string `json:"query" description:"Fuzzy keywords, or 'select:<tool_name>[,<tool_name>...]' for exact lookup"`
	Limit int    `json:"limit,omitempty" description:"Max tools to promote (default: 5, max: 20)"`
}

// NewToolSearchTool builds the gateway tool that promotes deferred tools into
// the live tool list. register is called once per discovered tool with the
// schema-guard-wrapped instance; the coordinator wires it to the owning
// SessionAgent so promoted tools stay advertised for the session.
func NewToolSearchTool(registry *DeferredToolRegistry, register func(fantasy.AgentTool)) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolSearchToolName,
		toolSearchDescription,
		func(ctx context.Context, params toolSearchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool tool_search")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", ToolSearchToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)

			query := strings.TrimSpace(params.Query)
			if query == "" {
				return fantasy.NewTextErrorResponse("query is required"), nil
			}
			limit := params.Limit
			if limit <= 0 {
				limit = DefaultToolSearchLimit
			} else if limit > maxToolSearchLimit {
				limit = maxToolSearchLimit
			}

			entries := registry.Candidates()
			if len(entries) == 0 {
				return fantasy.NewTextResponse("No deferred tools are available. MCP servers may not be connected or their tools are not permitted for this agent."), nil
			}

			var hits []DeferredEntry
			if names, exact := parseSelectQuery(query); exact {
				var missing []string
				var ambiguous []string
				for _, name := range names {
					entry, rawMatches, found := lookupByExactName(entries, name)
					if found {
						hits = append(hits, entry)
						continue
					}
					if len(rawMatches) > 0 {
						ambiguous = append(ambiguous, fmt.Sprintf(
							"'%s' is ambiguous: it matches %s. Select one of these full names instead.",
							name, strings.Join(rawMatches, ", "),
						))
						continue
					}
					missing = append(missing, name)
				}
				if len(hits) == 0 {
					return fantasy.NewTextErrorResponse(fmt.Sprintf(
						"No deferred tool matched the exact name(s) %s. The tool may not exist or may not be deferred. Available tools: %s",
						strings.Join(missing, ", "), strings.Join(entryNames(entries), ", "),
					)), nil
				}
				var notes []string
				if len(missing) > 0 {
					// Partial exact hits: report the misses alongside the
					// schemas so the model can retry with corrected names.
					notes = append(notes, "Not found: "+strings.Join(missing, ", ")+".")
				}
				notes = append(notes, ambiguous...)
				// Exact hits respect the limit too: promoting every named
				// schema at once can flood the context. The omitted names
				// are reported so the model can look them up again.
				if len(hits) > limit {
					omitted := entryNames(hits[limit:])
					hits = hits[:limit]
					notes = append(notes, fmt.Sprintf(
						"Not promoted due to limit (%d): %s. Query them again to promote them.",
						limit, strings.Join(omitted, ", "),
					))
				}
				return promotedResponse(hits, registry, register, strings.Join(notes, "\n")), nil
			}

			hits = bm25Search(query, entries, limit)
			if len(hits) == 0 {
				return fantasy.NewTextResponse(fmt.Sprintf(
					"No deferred tools matched %q. Try different keywords or an exact 'select:<name>' lookup. Available tools: %s",
					query, strings.Join(entryNames(entries), ", "),
				)), nil
			}
			return promotedResponse(hits, registry, register, ""), nil
		},
	)
}

// promotedResponse registers every hit through the schema guard and renders
// the discovery report with full parameter schemas for the model.
func promotedResponse(hits []DeferredEntry, registry *DeferredToolRegistry, register func(fantasy.AgentTool), prefix string) fantasy.ToolResponse {
	var sb strings.Builder
	if prefix != "" {
		sb.WriteString(prefix)
		sb.WriteString("\n\n")
	}
	fmt.Fprintf(&sb, "Discovered %d tool(s). Their full schemas are now advertised and callable:\n\n", len(hits))
	for _, hit := range hits {
		info := hit.Tool.Info()
		guarded := registry.Guard(hit.Key, hit.Tool, hit.Fingerprint)
		register(guarded)
		params, err := json.Marshal(info.Parameters)
		if err != nil {
			params = []byte("{}")
		}
		fmt.Fprintf(&sb, "- %s\n  Description: %s\n  Parameters: %s\n", info.Name, info.Description, params)
		if len(info.Required) > 0 {
			fmt.Fprintf(&sb, "  Required: %s\n", strings.Join(info.Required, ", "))
		}
	}
	sb.WriteString("\nCall these tools directly now; they stay available for the rest of the session.")
	return fantasy.NewTextResponse(sb.String())
}

// parseSelectQuery recognizes the exact 'select:...' lookup syntax.
func parseSelectQuery(query string) ([]string, bool) {
	rest, ok := strings.CutPrefix(query, "select:")
	if !ok {
		return nil, false
	}
	names := make([]string, 0, 1)
	for _, part := range strings.Split(rest, ",") {
		if name := strings.TrimSpace(part); name != "" {
			names = append(names, name)
		}
	}
	return names, true
}

// lookupByExactName matches a tool name case-insensitively. The full
// wrapped name (mcp_<server>_<tool>) is checked first; only when no full
// name matches does it fall back to the raw MCP tool name. When several
// entries share the raw name, none is promoted and the ambiguous full
// names are returned so the caller can disambiguate.
func lookupByExactName(entries []DeferredEntry, name string) (DeferredEntry, []string, bool) {
	for _, entry := range entries {
		if strings.EqualFold(entry.Tool.Info().Name, name) {
			return entry, nil, true
		}
	}
	var rawMatches []string
	var match DeferredEntry
	for _, entry := range entries {
		if strings.EqualFold(entry.RawName, name) {
			rawMatches = append(rawMatches, entry.Tool.Info().Name)
			match = entry
		}
	}
	if len(rawMatches) == 1 {
		return match, nil, true
	}
	if len(rawMatches) > 1 {
		return DeferredEntry{}, rawMatches, false
	}
	return DeferredEntry{}, nil, false
}

func entryNames(entries []DeferredEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Tool.Info().Name)
	}
	return names
}

// bm25Search ranks deferred tools by relevance to the query. The tool name
// is weighted double so capability keywords in names dominate descriptions.
func bm25Search(query string, entries []DeferredEntry, limit int) []DeferredEntry {
	type scored struct {
		entry DeferredEntry
		score float64
	}

	k1, b := 1.2, 0.75
	queryTokens := tokenizeText(query)
	// Deduplicate query tokens: a repeated word must not inflate document
	// frequency (which would push IDF negative and drop real matches).
	slices.Sort(queryTokens)
	queryTokens = slices.Compact(queryTokens)
	if len(queryTokens) == 0 {
		return nil
	}

	docs := make([][]string, len(entries))
	totalLen := 0
	for i, entry := range entries {
		info := entry.Tool.Info()
		tokens := tokenizeText(info.Name)
		tokens = append(tokens, tokens...) // name carries double weight
		tokens = append(tokens, tokenizeText(info.Description)...)
		docs[i] = tokens
		totalLen += len(tokens)
	}
	if totalLen == 0 {
		return nil
	}
	avgLen := float64(totalLen) / float64(len(docs))

	// Document frequency per query token, computed once.
	df := make(map[string]int, len(queryTokens))
	for _, token := range queryTokens {
		for _, doc := range docs {
			if slices.Contains(doc, token) {
				df[token]++
			}
		}
	}

	var hits []scored
	for i, doc := range docs {
		var score float64
		for _, token := range queryTokens {
			freq := 0
			for _, t := range doc {
				if t == token {
					freq++
				}
			}
			if freq == 0 {
				continue
			}
			f := float64(freq)
			n := float64(df[token])
			idf := math.Log(1 + (float64(len(docs))-n+0.5)/(n+0.5))
			score += idf * f * (k1 + 1) / (f + k1*(1-b+b*float64(len(doc))/avgLen))
		}
		if score > 0 {
			hits = append(hits, scored{entry: entries[i], score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].entry.Tool.Info().Name < hits[j].entry.Tool.Info().Name
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	result := make([]DeferredEntry, 0, len(hits))
	for _, hit := range hits {
		result = append(result, hit.entry)
	}
	return result
}

// tokenizeText lowercases and splits on non-alphanumeric characters,
// additionally emitting underscore/colon-separated sub-tokens so
// mcp_sentry_get_issue matches "sentry" and "issue".
func tokenizeText(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == ':')
	})
	var tokens []string
	for _, field := range fields {
		parts := strings.FieldsFunc(field, func(r rune) bool {
			return r == '_' || r == ':'
		})
		tokens = append(tokens, field)
		if len(parts) > 1 {
			tokens = append(tokens, parts...)
		}
	}
	return tokens
}
