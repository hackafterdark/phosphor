# RFC: Structured Observation Layer for Tool Failures

## Status

Draft

## Problem

Currently, the agent harness silently strips failed tool calls that result in framework-level validation errors (e.g., missing parameters, invalid pattern format). While this prevents infinite retry loops, it leaves the agent "blind" to why the TUI displays a failure, leading to repetitive, confident errors.

## Proposed Solution: The Structured Observation Pattern

Instead of stripping failed attempts, we will intercept framework-level validation errors and inject a semantic "Tool Observation" message into the conversation history. This provides the agent with the necessary context to self-correct without forcing it to re-parse broken raw output.

## Implementation Plan

### Phase 1: Create the Translation Layer

Implement a mapping function in `internal/agent/agent.go` to convert framework-level errors into human-readable, actionable feedback for the model.

Go

```
// Example mapping for the translation layer
func translateToObservation(err error) string {
    switch {
    case strings.Contains(err.Error(), "missing required parameter"):
        return "Tool Observation: Your previous attempt failed due to a missing required parameter. Please review the tool definition and provide all necessary inputs."
    case strings.Contains(err.Error(), "invalid pattern"):
        return "Tool Observation: The search pattern provided was invalid. Please ensure it follows standard regex syntax."
    default:
        return fmt.Sprintf("Tool Observation: The tool failed with the following error: %s", err.Error())
    }
}

```

### Phase 2: Integrate with PrepareStep

Modify the `PrepareStep` function to check for validation errors before finalizing the message history.

1. Identify when a framework error occurs.
2. Instead of stripping, append a `fantasy.NewAssistantMessage` or `fantasy.NewToolResult` containing the translated observation.
3. Add a system-prompt constraint: *"If you receive a 'Tool Observation', you are required to analyze the error and adjust your strategy before retrying."*

### Phase 3: Loop Detection Guardrail

To prevent the retry loops we previously feared, add a counter in the `sessionAgent` to track consecutive failures for a specific tool. If `failure_count > 2`, force the agent to stop and report the error to the user rather than attempting a third retry.

## Benefits

- **Informed Self-Correction:** The agent understands *why* it failed.
- **Preserved Context:** The history reflects reality (the TUI error matches the model's perspective).
- **Reduced Noise:** The model is not forced to re-reason over its own broken, raw input.

## Testing

1. **Validation Test:** Manually trigger a missing parameter error to ensure the agent receives the "Tool Observation" and corrects its input.
2. **Loop Test:** Deliberately induce 3 consecutive failures to verify the guardrail halts the execution.

A note about the `fantasy` library: because `PrepareStep` receives the full `fantasy.PrepareStepFunctionOptions`, you will have access to the current `Messages` slice. You can check the last message in that slice to see if it was a failed tool call, then append your "Observation" message before returning the `prepared` result.



# Future Potential

### "Configurable Policy"

The failure max count is hard-coded to 2 currently. Different models may recover from errors better than others. So there is some value in having it be configurable. However, instead of a raw `failureMaxCount` setting, we could implement a **Policy-based configuration**. This is a much more "Architect-friendly" way to handle it:

```
type ToolPolicy struct {
    MaxRetries      int
    Strict          bool // If true, stop on first failure for this tool
    RequiresHuman   bool // If true, pause and ask human after failure
}
```

This way, we aren't just giving the user a "knob" to turn; we are giving them the ability to define the **safety profile** of the agent based on the tool's risk level. Will need to revisit this later.