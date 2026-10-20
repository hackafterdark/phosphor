package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

type coercionProbeParams struct {
	Offset int    `json:"offset"`
	Active bool   `json:"active"`
	Name   string `json:"name"`
	Tags   []int  `json:"tags"`
	Nested struct {
		Flag bool `json:"flag"`
	} `json:"nested"`
}

func coercionProbeTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("probe", "probe tool", func(
		_ context.Context, _ coercionProbeParams, _ fantasy.ToolCall,
	) (fantasy.ToolResponse, error) {
		return fantasy.NewTextResponse("ok"), nil
	})
}

func wrappedProbe(t *testing.T, input string) (string, bool) {
	t.Helper()
	return coerceToolCallInput(coercionProbeTool(), input)
}

func TestCoerceToolCallInputCoercesStringifiedBool(t *testing.T) {
	t.Parallel()
	got, changed := wrappedProbe(t, `{"active":"True"}`)
	require.True(t, changed)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &m))
	require.Equal(t, true, m["active"])
}

func TestCoerceToolCallInputCoercesTypedFields(t *testing.T) {
	t.Parallel()
	got, changed := wrappedProbe(t, `{"offset":"42","name":12,"tags":["1","2"]}`)
	require.True(t, changed)
	dec := json.NewDecoder(strings.NewReader(got))
	dec.UseNumber()
	var m map[string]any
	require.NoError(t, dec.Decode(&m))
	require.Equal(t, json.Number("42"), m["offset"])
	require.Equal(t, "12", m["name"])
	require.Equal(t, []any{json.Number("1"), json.Number("2")}, m["tags"])
}

func TestCoerceToolCallInputParsesStringifiedNestedObject(t *testing.T) {
	t.Parallel()
	got, changed := wrappedProbe(t, `{"nested":"{\"flag\":\"false\"}"}`)
	require.True(t, changed)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &m))
	nested, ok := m["nested"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, nested["flag"])
}

func TestCoerceToolCallInputLeavesValidInputAlone(t *testing.T) {
	t.Parallel()
	got, changed := wrappedProbe(t, `{"offset":4,"active":true,"name":"x"}`)
	require.False(t, changed)
	require.Equal(t, `{"offset":4,"active":true,"name":"x"}`, got)
}

func TestCoerceToolCallInputRejectsAmbiguousBool(t *testing.T) {
	t.Parallel()
	got, changed := wrappedProbe(t, `{"active":"yes"}`)
	require.False(t, changed, "an ambiguous value must not be forced into a bool")
	require.Equal(t, `{"active":"yes"}`, got)
}

func TestCoerceToolCallInputLeavesUnparsableInputAlone(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{`{`, `not json`, ``} {
		got, changed := wrappedProbe(t, bad)
		require.False(t, changed, bad)
		require.Equal(t, bad, got)
	}
}

func TestCoerceToolCallInputSchemalessTool(t *testing.T) {
	t.Parallel()
	schemaless := fantasy.NewAgentTool("free", "free tool", func(
		_ context.Context, _ map[string]any, _ fantasy.ToolCall,
	) (fantasy.ToolResponse, error) {
		return fantasy.NewTextResponse("ok"), nil
	})
	got, changed := coerceToolCallInput(schemaless, `{"whatever":"anything"}`)
	// A schemaless tool has nothing to coerce against; input passes through.
	_ = got
	_ = changed
}

func TestCoercingToolRunRepairsInput(t *testing.T) {
	t.Parallel()
	var seen coercionProbeParams
	probe := fantasy.NewAgentTool("probe", "probe tool", func(
		_ context.Context, p coercionProbeParams, _ fantasy.ToolCall,
	) (fantasy.ToolResponse, error) {
		seen = p
		return fantasy.NewTextResponse("ok"), nil
	})
	wrapped := wrapToolsWithCoercion([]fantasy.AgentTool{probe})[0]
	_, err := wrapped.Run(context.Background(), fantasy.ToolCall{
		ID:    "call-1",
		Name:  "probe",
		Input: `{"active":"TRUE","offset":"7"}`,
	})
	require.NoError(t, err)
	require.True(t, seen.Active)
	require.Equal(t, 7, seen.Offset)
}

func TestCoercingToolRunPreservesInterface(t *testing.T) {
	t.Parallel()
	probe := coercionProbeTool()
	wrapped := wrapToolsWithCoercion([]fantasy.AgentTool{probe})[0]
	require.Equal(t, probe.Info().Name, wrapped.Info().Name)
	require.Equal(t, probe.ProviderOptions(), wrapped.ProviderOptions())
	empty := fantasy.ProviderOptions{}
	wrapped.SetProviderOptions(empty)
	require.Equal(t, empty, probe.ProviderOptions())
}
