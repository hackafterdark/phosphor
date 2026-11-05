package tools

import (
	"context"
	"maps"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

type fakeDeferredTool struct {
	info fantasy.ToolInfo
	ran  bool
}

func (t *fakeDeferredTool) Info() fantasy.ToolInfo { return t.info }

func (t *fakeDeferredTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	t.ran = true
	return fantasy.NewTextResponse("ok:" + t.info.Name), nil
}

func (t *fakeDeferredTool) SetProviderOptions(fantasy.ProviderOptions) {}

func (t *fakeDeferredTool) ProviderOptions() fantasy.ProviderOptions { return nil }

func newRegistryState() (*map[DeferredToolKey]fantasy.AgentTool, *DeferredToolRegistry) {
	state := map[DeferredToolKey]fantasy.AgentTool{}
	registry := NewDeferredToolRegistry(nil, func() map[DeferredToolKey]fantasy.AgentTool {
		current := make(map[DeferredToolKey]fantasy.AgentTool, len(state))
		maps.Copy(current, state)
		return current
	})
	return &state, registry
}

func addDeferred(state *map[DeferredToolKey]fantasy.AgentTool, registry *DeferredToolRegistry, server, raw, description string) {
	key := DeferredToolKey{Server: server, Tool: raw}
	tool := &fakeDeferredTool{info: fantasy.ToolInfo{
		Name:        "mcp_" + server + "_" + raw,
		Description: description,
		Parameters:  map[string]any{"query": map[string]any{"type": "string"}},
		Required:    []string{"query"},
	}}
	(*state)[key] = tool
	registry.DeferAll([]DeferredToolKey{key})
}

func TestToolSearchRegistryCandidatesSorted(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "zeta", "last", "does zeta things")
	addDeferred(state, registry, "alpha", "first", "does alpha things")

	var names []string
	for _, entry := range registry.Candidates() {
		names = append(names, entry.Tool.Info().Name)
	}
	require.Equal(t, []string{"mcp_alpha_first", "mcp_zeta_last"}, names)
}

func TestToolSearchRegistryIgnoresNonAllowed(t *testing.T) {
	allowedKey := DeferredToolKey{Server: "x", Tool: "allowed"}
	forbiddenKey := DeferredToolKey{Server: "x", Tool: "forbidden"}
	state := map[DeferredToolKey]fantasy.AgentTool{
		allowedKey:   &fakeDeferredTool{info: fantasy.ToolInfo{Name: "mcp_x_allowed"}},
		forbiddenKey: &fakeDeferredTool{info: fantasy.ToolInfo{Name: "mcp_x_forbidden"}},
	}
	registry := NewDeferredToolRegistry([]DeferredToolKey{allowedKey}, func() map[DeferredToolKey]fantasy.AgentTool {
		return state
	})
	require.Len(t, registry.Candidates(), 1)
	_, _, ok := registry.Lookup(forbiddenKey)
	require.False(t, ok)
}

// Colliding wrapped names (server "a_b" tool "c" vs server "a" tool
// "b_c") must stay distinct identities so one cannot overwrite the other
// in the live map.
func TestToolSearchRegistryCollidingWrappedNames(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "a_b", "c", "the a_b server's c tool")
	addDeferred(state, registry, "a", "b_c", "the a server's b_c tool")
	require.Len(t, registry.Candidates(), 2)
	for _, entry := range registry.Candidates() {
		require.Equal(t, "mcp_a_b_c", entry.Tool.Info().Name)
	}
	_, _, ok := registry.Lookup(DeferredToolKey{Server: "a_b", Tool: "c"})
	require.True(t, ok)
	_, _, ok = registry.Lookup(DeferredToolKey{Server: "a", Tool: "b_c"})
	require.True(t, ok)
}

func TestToolSearchPromotesAndRegisters(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "sentry", "get_issue", "fetch a sentry issue by id")
	addDeferred(state, registry, "demo", "echo", "echo text back")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"query":"fetch sentry issue"}`,
	})
	require.NoError(t, err)
	require.Len(t, promoted, 1)
	require.Equal(t, "mcp_sentry_get_issue", promoted[0].Info().Name)
	require.Contains(t, resp.Content, "mcp_sentry_get_issue")
	require.Contains(t, resp.Content, "query")

	// The promoted tool is guarded but delegates to the real tool.
	out, err := promoted[0].Run(context.Background(), fantasy.ToolCall{ID: "1"})
	require.NoError(t, err)
	require.Equal(t, "ok:mcp_sentry_get_issue", out.Content)
}

func TestToolSearchExactSelect(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "sentry", "get_issue", "fetch a sentry issue by id")
	addDeferred(state, registry, "demo", "echo", "echo text back")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"query":"select:mcp_demo_echo"}`,
	})
	require.NoError(t, err)
	require.Len(t, promoted, 1)
	require.Equal(t, "mcp_demo_echo", promoted[0].Info().Name)
	require.NotContains(t, resp.Content, "mcp_sentry_get_issue")
}

func TestToolSearchExactSelectRawMCPName(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "sentry", "get_issue", "fetch a sentry issue by id")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })

	_, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"query":"select:get_issue"}`,
	})
	require.NoError(t, err)
	require.Len(t, promoted, 1)
}

// The full wrapped name must win over a raw-name suffix match: "issue"
// is the raw tail of both tools, but "mcp_sentry_issue" itself is a raw
// name too and only one entry has that raw name.
func TestToolSearchExactSelectPrefersFullThenRawName(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "sentry", "get_issue", "fetch a sentry issue by id")
	addDeferred(state, registry, "sentry", "issue", "the issue tool itself")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })

	_, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"query":"select:issue"}`,
	})
	require.NoError(t, err)
	require.Len(t, promoted, 1)
	require.Equal(t, "mcp_sentry_issue", promoted[0].Info().Name)
}

// Two servers exposing the same raw tool name must not promote either:
// the lookup reports the ambiguity with the full names instead.
func TestToolSearchExactSelectAmbiguousRawName(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "alpha", "search", "alpha search")
	addDeferred(state, registry, "beta", "search", "beta search")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"query":"select:search"}`,
	})
	require.NoError(t, err)
	require.Empty(t, promoted)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "mcp_alpha_search")
	require.Contains(t, resp.Content, "mcp_beta_search")
}

// Exact select lookups respect the limit and report the omitted names.
func TestToolSearchExactSelectHonorsLimit(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "demo", "one", "first")
	addDeferred(state, registry, "demo", "two", "second")
	addDeferred(state, registry, "demo", "three", "third")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"query":"select:mcp_demo_one,mcp_demo_two,mcp_demo_three","limit":2}`,
	})
	require.NoError(t, err)
	require.Len(t, promoted, 2)
	require.Contains(t, resp.Content, "mcp_demo_three")
	require.Contains(t, resp.Content, "limit")
}

func TestToolSearchExactSelectUnknown(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "demo", "echo", "echo text back")

	tool := NewToolSearchTool(registry, func(fantasy.AgentTool) {})
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"query":"select:mcp_demo_missing"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "mcp_demo_missing")
}

func TestToolSearchFuzzyMiss(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "demo", "echo", "echo text back")

	tool := NewToolSearchTool(registry, func(fantasy.AgentTool) {})
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"query":"quantum tunneling spectroscopy"}`,
	})
	require.NoError(t, err)
	require.NotContains(t, resp.Content, "Discovered")
	require.Contains(t, resp.Content, "No deferred tools matched")
}

func TestToolSearchEmptyRegistry(t *testing.T) {
	_, registry := newRegistryState()
	tool := NewToolSearchTool(registry, func(fantasy.AgentTool) {})
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"query":"anything"}`,
	})
	require.NoError(t, err)
	require.Contains(t, resp.Content, "No deferred tools are available")
}

func TestToolSearchGuardRejectsDrift(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "demo", "echo", "echo text back")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })
	_, err := tool.Run(context.Background(), fantasy.ToolCall{Input: `{"query":"echo"}`})
	require.NoError(t, err)
	require.Len(t, promoted, 1)

	// Server-side refresh swaps in a changed schema for the same name.
	(*state)[DeferredToolKey{Server: "demo", Tool: "echo"}] = &fakeDeferredTool{info: fantasy.ToolInfo{
		Name:        "mcp_demo_echo",
		Description: "echo text back (now with streaming)",
		Parameters:  map[string]any{"text": map[string]any{"type": "string"}},
	}}

	resp, err := promoted[0].Run(context.Background(), fantasy.ToolCall{ID: "1"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "schema drift")
	require.Contains(t, resp.Content, "tool_search")
}

func TestToolSearchGuardRejectsRemovedTool(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "demo", "echo", "echo text back")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })
	_, err := tool.Run(context.Background(), fantasy.ToolCall{Input: `{"query":"echo"}`})
	require.NoError(t, err)

	delete(*state, DeferredToolKey{Server: "demo", Tool: "echo"})
	resp, err := promoted[0].Run(context.Background(), fantasy.ToolCall{ID: "1"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no longer available")
}

func TestToolSearchGuardPassesStableSchema(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "demo", "echo", "echo text back")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })
	_, err := tool.Run(context.Background(), fantasy.ToolCall{Input: `{"query":"echo"}`})
	require.NoError(t, err)

	// Same schema content in a fresh wrapper instance must pass the guard.
	(*state)[DeferredToolKey{Server: "demo", Tool: "echo"}] = &fakeDeferredTool{info: fantasy.ToolInfo{
		Name:        "mcp_demo_echo",
		Description: "echo text back",
		Parameters:  map[string]any{"query": map[string]any{"type": "string"}},
		Required:    []string{"query"},
	}}
	resp, err := promoted[0].Run(context.Background(), fantasy.ToolCall{ID: "1"})
	require.NoError(t, err)
	require.Equal(t, "ok:mcp_demo_echo", resp.Content)
}

func TestToolSchemaFingerprintStableAndSensitive(t *testing.T) {
	base := fantasy.ToolInfo{
		Name:        "mcp_x_y",
		Description: "does x",
		Parameters:  map[string]any{"a": map[string]any{"type": "string"}},
		Required:    []string{"a"},
	}
	require.Equal(t, ToolSchemaFingerprint(base), ToolSchemaFingerprint(base))

	changed := base
	changed.Description = "does x better"
	require.NotEqual(t, ToolSchemaFingerprint(base), ToolSchemaFingerprint(changed))
}

func TestBM25SearchRanksNameWeight(t *testing.T) {
	entries := []DeferredEntry{
		{Tool: &fakeDeferredTool{info: fantasy.ToolInfo{Name: "mcp_demo_echo", Description: "generic utility"}}},
		{Tool: &fakeDeferredTool{info: fantasy.ToolInfo{Name: "mcp_logs_tail", Description: "tail the echo server logs"}}},
	}
	hits := bm25Search("echo", entries, 5)
	require.NotEmpty(t, hits)
	require.Equal(t, "mcp_demo_echo", hits[0].Tool.Info().Name)
}

func TestBM25SearchLimit(t *testing.T) {
	var entries []DeferredEntry
	for i := range 10 {
		entries = append(entries, DeferredEntry{Tool: &fakeDeferredTool{
			info: fantasy.ToolInfo{Name: "mcp_x_tool" + string(rune('a'+i)), Description: "search index lookup"},
		}})
	}
	hits := bm25Search("search index lookup", entries, 3)
	require.Len(t, hits, 3)
}

func TestTokenizeTextSubtokens(t *testing.T) {
	tokens := tokenizeText("mcp_sentry_get_issue")
	require.Contains(t, tokens, "sentry")
	require.Contains(t, tokens, "issue")
	require.Contains(t, tokens, "mcp_sentry_get_issue")
}

// A repeated query word must not inflate document frequency into a
// negative IDF that drops the only real match.
func TestBM25SearchDeduplicatesQueryTokens(t *testing.T) {
	entries := []DeferredEntry{
		{Tool: &fakeDeferredTool{info: fantasy.ToolInfo{Name: "mcp_demo_echo", Description: "echo text back"}}},
	}
	hits := bm25Search("echo echo", entries, 5)
	require.Len(t, hits, 1)
	require.Equal(t, "mcp_demo_echo", hits[0].Tool.Info().Name)
}

// Non-ASCII runs must tokenize as whole tokens so CJK queries can match
// CJK tool descriptions.
func TestTokenizeTextUnicode(t *testing.T) {
	tokens := tokenizeText("查询订单")
	require.Equal(t, []string{"查询订单"}, tokens)

	entries := []DeferredEntry{
		{Tool: &fakeDeferredTool{info: fantasy.ToolInfo{Name: "mcp_shop_lookup", Description: "查询订单 status"}}},
	}
	hits := bm25Search("查询订单", entries, 5)
	require.Len(t, hits, 1)
}

func TestToolSearchDescriptionLoaded(t *testing.T) {
	require.True(t, strings.Contains(toolSearchDescription, "tool_search"))
}
