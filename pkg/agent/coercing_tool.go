package agent

import (
	"context"
	"encoding/json"
	"log/slog"

	"charm.land/fantasy"
)

// coercingTool wraps a fantasy.AgentTool to repair tool-call inputs whose
// parameter types do not match the tool's JSON schema before the input
// reaches the typed decoder.
//
// Fantasy's pre-execution validation (validateToolCall) is type-blind: it
// checks that the input parses as JSON and that required fields are present,
// so repairToolCall — which fantasy only invokes when validation fails —
// never sees a schema type mismatch such as {"asserted":"True"} for a
// boolean parameter. The mismatch instead surfaces as an unmarshal error
// from the typed decoder, burning a full turn on a mistake the repair layer
// could fix deterministically. This wrapper closes that gap: every call
// input is checked against the schema, and mismatches are coerced with the
// same strict, schema-driven rules as repairToolCall (only values the schema
// itself declares as the target type are produced; ambiguous values are left
// untouched so the normal error path still runs).
type coercingTool struct {
	inner fantasy.AgentTool
}

// wrapToolsWithCoercion returns a tool slice with each entry wrapped in a
// coercingTool. The wrapper is applied unconditionally — type coercion is
// independent of hooks and sub-agent context. It is applied after
// wrapToolsWithHooks so it becomes the outermost wrapper: its Run repairs
// the raw model input first, and PreToolUse hooks then inspect and may
// rewrite the repaired input.
func wrapToolsWithCoercion(tools []fantasy.AgentTool) []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, len(tools))
	for i, tool := range tools {
		out[i] = &coercingTool{inner: tool}
	}
	return out
}

func (c *coercingTool) Info() fantasy.ToolInfo {
	return c.inner.Info()
}

func (c *coercingTool) ProviderOptions() fantasy.ProviderOptions {
	return c.inner.ProviderOptions()
}

func (c *coercingTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	c.inner.SetProviderOptions(opts)
}

func (c *coercingTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if repaired, ok := coerceToolCallInput(c.inner, call.Input); ok {
		slog.Info(
			"Repaired tool call input to match tool schema (deterministic schema-driven coercion, intentional)",
			"tool", call.Name,
			"tool_call_id", call.ID,
			"original_len", len(call.Input),
			"repaired_len", len(repaired),
		)
		call.Input = repaired
	}
	return c.inner.Run(ctx, call)
}

// coerceToolCallInput attempts the schema-driven coercion on input. It
// returns the repaired JSON and true only when the input actually changed;
// unparsable input, schema-less tools, and already-valid input all return
// (input, false) unchanged.
func coerceToolCallInput(tool fantasy.AgentTool, input string) (string, bool) {
	decoded, err := decodeLoose(input)
	if err != nil {
		return input, false
	}
	params := toolParamNode(tool)
	if params == nil {
		return input, false
	}
	repaired, changed := coerceObjectForSchema(decoded, params)
	if !changed {
		return input, false
	}
	out, err := json.Marshal(repaired)
	if err != nil {
		return input, false
	}
	return string(out), true
}
