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

func newRegistryState() (*map[string]fantasy.AgentTool, *DeferredToolRegistry) {
	state := map[string]fantasy.AgentTool{}
	registry := NewDeferredToolRegistry(nil, func() map[string]fantasy.AgentTool {
		current := make(map[string]fantasy.AgentTool, len(state))
		maps.Copy(current, state)
		return current
	})
	return &state, registry
}

func addDeferred(state *map[string]fantasy.AgentTool, registry *DeferredToolRegistry, name, description string) {
	tool := &fakeDeferredTool{info: fantasy.ToolInfo{
		Name:        name,
		Description: description,
		Parameters:  map[string]any{"query": map[string]any{"type": "string"}},
		Required:    []string{"query"},
	}}
	(*state)[name] = tool
	registry.DeferAll([]string{name})
}

func TestToolSearchRegistryCandidatesSorted(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "mcp_zeta_last", "does zeta things")
	addDeferred(state, registry, "mcp_alpha_first", "does alpha things")

	var names []string
	for _, entry := range registry.Candidates() {
		names = append(names, entry.Tool.Info().Name)
	}
	require.Equal(t, []string{"mcp_alpha_first", "mcp_zeta_last"}, names)
}

func TestToolSearchRegistryIgnoresNonAllowed(t *testing.T) {
	state := map[string]fantasy.AgentTool{
		"mcp_x_allowed":   &fakeDeferredTool{info: fantasy.ToolInfo{Name: "mcp_x_allowed"}},
		"mcp_x_forbidden": &fakeDeferredTool{info: fantasy.ToolInfo{Name: "mcp_x_forbidden"}},
	}
	registry := NewDeferredToolRegistry([]string{"mcp_x_allowed"}, func() map[string]fantasy.AgentTool {
		return state
	})
	require.Len(t, registry.Candidates(), 1)
	_, _, ok := registry.Lookup("mcp_x_forbidden")
	require.False(t, ok)
}

func TestToolSearchPromotesAndRegisters(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "mcp_sentry_get_issue", "fetch a sentry issue by id")
	addDeferred(state, registry, "mcp_demo_echo", "echo text back")

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
	addDeferred(state, registry, "mcp_sentry_get_issue", "fetch a sentry issue by id")
	addDeferred(state, registry, "mcp_demo_echo", "echo text back")

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
	addDeferred(state, registry, "mcp_sentry_get_issue", "fetch a sentry issue by id")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })

	_, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"query":"select:get_issue"}`,
	})
	require.NoError(t, err)
	require.Len(t, promoted, 1)
}

func TestToolSearchExactSelectUnknown(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "mcp_demo_echo", "echo text back")

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
	addDeferred(state, registry, "mcp_demo_echo", "echo text back")

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
	addDeferred(state, registry, "mcp_demo_echo", "echo text back")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })
	_, err := tool.Run(context.Background(), fantasy.ToolCall{Input: `{"query":"echo"}`})
	require.NoError(t, err)
	require.Len(t, promoted, 1)

	// Server-side refresh swaps in a changed schema for the same name.
	(*state)["mcp_demo_echo"] = &fakeDeferredTool{info: fantasy.ToolInfo{
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
	addDeferred(state, registry, "mcp_demo_echo", "echo text back")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })
	_, err := tool.Run(context.Background(), fantasy.ToolCall{Input: `{"query":"echo"}`})
	require.NoError(t, err)

	delete(*state, "mcp_demo_echo")
	resp, err := promoted[0].Run(context.Background(), fantasy.ToolCall{ID: "1"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no longer available")
}

func TestToolSearchGuardPassesStableSchema(t *testing.T) {
	state, registry := newRegistryState()
	addDeferred(state, registry, "mcp_demo_echo", "echo text back")

	var promoted []fantasy.AgentTool
	tool := NewToolSearchTool(registry, func(t fantasy.AgentTool) { promoted = append(promoted, t) })
	_, err := tool.Run(context.Background(), fantasy.ToolCall{Input: `{"query":"echo"}`})
	require.NoError(t, err)

	// Same schema content in a fresh wrapper instance must pass the guard.
	(*state)["mcp_demo_echo"] = &fakeDeferredTool{info: fantasy.ToolInfo{
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

func TestToolSearchDescriptionLoaded(t *testing.T) {
	require.True(t, strings.Contains(toolSearchDescription, "tool_search"))
}
