package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

type repairProbeParams struct {
	Offset int            `json:"offset"`
	Active bool           `json:"active"`
	Name   string         `json:"name"`
	Rate   float64        `json:"rate"`
	Tags   []string       `json:"tags"`
	Nums   []int          `json:"nums"`
	Config map[string]any `json:"config"`
}

func probeTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("probe", "probe tool", func(
		_ context.Context, _ repairProbeParams, _ fantasy.ToolCall,
	) (fantasy.ToolResponse, error) {
		return fantasy.NewTextResponse("ok"), nil
	})
}

func repair(t *testing.T, input string) (*fantasy.ToolCallContent, error) {
	t.Helper()
	return repairToolCall(context.Background(), fantasy.ToolCallRepairOptions{
		OriginalToolCall: fantasy.ToolCallContent{
			ToolCallID: "call-1",
			ToolName:   "probe",
			Input:      input,
		},
		AvailableTools: []fantasy.AgentTool{probeTool()},
	})
}

func repairedInput(t *testing.T, input string) map[string]any {
	t.Helper()
	got, err := repair(t, input)
	require.NoError(t, err)
	require.NotNil(t, got, "expected the call to be repaired")
	require.False(t, got.Invalid)
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(got.Input))
	dec.UseNumber()
	require.NoError(t, dec.Decode(&m))
	return m
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestRepairToolCallCoercesNumericStrings(t *testing.T) {
	t.Parallel()
	m := repairedInput(t, `{"offset":"42"}`)
	require.Equal(t, json.Number("42"), m["offset"])
}

func TestRepairToolCallKeepsBigIntegerPrecision(t *testing.T) {
	t.Parallel()
	m := repairedInput(t, `{"offset":"9007199254740993"}`)
	require.Equal(t, json.Number("9007199254740993"), m["offset"])
}

func TestRepairToolCallCoercesFloatStrings(t *testing.T) {
	t.Parallel()
	m := repairedInput(t, `{"rate":"1.5","offset":"7"}`)
	require.Equal(t, json.Number("1.5"), m["rate"])
	require.Equal(t, json.Number("7"), m["offset"])
}

func TestRepairToolCallRejectsNonNumericStringForInt(t *testing.T) {
	t.Parallel()
	got, err := repair(t, `{"offset":"lots"}`)
	require.NoError(t, err)
	require.Nil(t, got, "a non-numeric string must not be forced into an int")
}

func TestRepairToolCallCoercesBooleans(t *testing.T) {
	t.Parallel()
	m := repairedInput(t, `{"active":"TRUE"}`)
	require.Equal(t, true, m["active"])
}

func TestRepairToolCallStringifiesScalarForStringField(t *testing.T) {
	t.Parallel()
	m := repairedInput(t, `{"name":12}`)
	require.Equal(t, "12", m["name"])
}

func TestRepairToolCallParsesStringifiedArray(t *testing.T) {
	t.Parallel()
	m := repairedInput(t, `{"tags":"[\"a\",\"b\"]"}`)
	require.Equal(t, []any{"a", "b"}, m["tags"])
}

func TestRepairToolCallCoercesArrayItems(t *testing.T) {
	t.Parallel()
	m := repairedInput(t, `{"nums":["1","2"]}`)
	require.Equal(t, []any{json.Number("1"), json.Number("2")}, m["nums"])
}

func TestRepairToolCallParsesStringifiedObject(t *testing.T) {
	t.Parallel()
	m := repairedInput(t, `{"config":"{\"a\":1}"}`)
	obj, ok := m["config"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, json.Number("1"), obj["a"])
}

func TestRepairToolCallDropsUnknownKeys(t *testing.T) {
	t.Parallel()
	m := repairedInput(t, `{"offset":3,"bogus":true}`)
	require.Equal(t, []string{"offset"}, keys(m))
}

func TestRepairToolCallRepairsNearMissKeys(t *testing.T) {
	t.Parallel()
	m := repairedInput(t, `{"Offset":"5"}`)
	require.Equal(t, json.Number("5"), m["offset"])
}

func TestRepairToolCallTrimsTrailingGarbage(t *testing.T) {
	t.Parallel()
	m := repairedInput(t, `{"offset":"9"}`+"<|im_start|>")
	require.Equal(t, json.Number("9"), m["offset"])
}

func TestRepairToolCallLeavesValidInputAlone(t *testing.T) {
	t.Parallel()
	got, err := repair(t, `{"offset":4,"name":"x","active":true}`)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestRepairToolCallUnrepairableReturnsNil(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{`{`, `not json`, ``, ``} {
		got, err := repair(t, bad)
		require.NoError(t, err)
		require.Nil(t, got, bad)
	}
}

func TestRepairToolCallUnknownToolReturnsNil(t *testing.T) {
	t.Parallel()
	got, err := repairToolCall(context.Background(), fantasy.ToolCallRepairOptions{
		OriginalToolCall: fantasy.ToolCallContent{ToolCallID: "c", ToolName: "nope", Input: `{"offset":"1"}`},
		AvailableTools:   []fantasy.AgentTool{probeTool()},
	})
	require.NoError(t, err)
	require.Nil(t, got)
}

type rawParamsTool struct {
	name       string
	parameters map[string]any
}

func (r rawParamsTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{Name: r.name, Description: "raw params tool", Parameters: r.parameters}
}

func (r rawParamsTool) Run(_ context.Context, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return fantasy.NewTextResponse("ok"), nil
}

func (r rawParamsTool) ProviderOptions() fantasy.ProviderOptions { return nil }

func (r rawParamsTool) SetProviderOptions(_ fantasy.ProviderOptions) {}

func repairWith(t *testing.T, tool fantasy.AgentTool, input string) map[string]any {
	t.Helper()
	got, err := repairToolCall(context.Background(), fantasy.ToolCallRepairOptions{
		OriginalToolCall: fantasy.ToolCallContent{ToolCallID: "call-1", ToolName: tool.Info().Name, Input: input},
		AvailableTools:   []fantasy.AgentTool{tool},
	})
	require.NoError(t, err)
	require.NotNil(t, got, "expected the call to be repaired")
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(got.Input))
	dec.UseNumber()
	require.NoError(t, dec.Decode(&m))
	return m
}

func TestRepairToolCallInlinePropertiesArgIsNotSchema(t *testing.T) {
	t.Parallel()
	tool := rawParamsTool{name: "raw", parameters: map[string]any{
		"properties": map[string]any{"type": "string"},
		"active":     map[string]any{"type": "boolean"},
	}}
	m := repairWith(t, tool, `{"properties":"meta","active":"true"}`)
	require.Equal(t, "meta", m["properties"])
	require.Equal(t, true, m["active"])
}

func TestRepairToolCallSchemaValuedAdditionalProperties(t *testing.T) {
	t.Parallel()
	tool := rawParamsTool{name: "raw", parameters: map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"active": map[string]any{"type": "boolean"}},
		"additionalProperties": map[string]any{"type": "integer"},
	}}
	m := repairWith(t, tool, `{"active":"true","extra":"7"}`)
	require.Equal(t, true, m["active"])
	require.Equal(t, json.Number("7"), m["extra"])
}

func TestRepairToolCallClosedEmptyObjectDropsUnknown(t *testing.T) {
	t.Parallel()
	tool := rawParamsTool{name: "raw", parameters: map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}}
	m := repairWith(t, tool, `{"unexpected":1}`)
	require.Empty(t, m)
}
