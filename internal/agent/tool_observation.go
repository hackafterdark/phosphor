package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/hackafterdark/phosphor/internal/message"
)

// translateToObservation converts a framework-level tool validation error
// into a human-readable, actionable message for the model.
func translateToObservation(err error, toolName string) string {
	msg := err.Error()
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "missing required parameter"):
		// Extract the parameter name from the error if possible.
		param := extractParamName(msg)
		if param != "" {
			return fmt.Sprintf("Tool Observation: Your previous attempt failed because the required parameter %q was missing for tool %q. Please review the tool definition and provide all necessary inputs.", param, toolName)
		}
		return fmt.Sprintf("Tool Observation: Your previous attempt failed because a required parameter was missing for tool %q. Please review the tool definition and provide all necessary inputs.", toolName)

	case strings.Contains(lower, "invalid pattern"):
		return fmt.Sprintf("Tool Observation: The search pattern provided for tool %q was invalid. Please ensure it follows standard regex syntax.", toolName)

	case strings.Contains(lower, "invalid json") || strings.Contains(lower, "malformed") || strings.Contains(lower, "parse error"):
		return fmt.Sprintf("Tool Observation: Your previous attempt failed because the JSON input for tool %q was invalid or malformed. Please ensure the input is valid JSON with quoted keys and proper syntax.", toolName)

	case strings.Contains(lower, "extra data"):
		return fmt.Sprintf("Tool Observation: Your previous attempt failed because the input for tool %q contained extra or unexpected data. Please review the tool definition and provide only the expected inputs.", toolName)

	case strings.Contains(lower, "context overflow") || strings.Contains(lower, "max context") || strings.Contains(lower, "too long") || strings.Contains(lower, "overflow"):
		return fmt.Sprintf("Tool Observation: Your previous attempt failed because the input for tool %q exceeded the context window limit. Please provide a shorter or more focused input.", toolName)

	case strings.Contains(lower, "tool"):
		return fmt.Sprintf("Tool Observation: Your previous attempt with tool %q failed due to a validation error. Please review the tool definition and adjust your input accordingly.", toolName)

	default:
		return fmt.Sprintf("Tool Observation: The tool %q failed with the following error: %s. Please review the tool definition and adjust your strategy.", toolName, msg)
	}
}

// extractParamName attempts to extract the missing parameter name from an error
// message like "missing required parameter: pattern".
func extractParamName(msg string) string {
	// Try to find "parameter: <name>" pattern.
	idx := strings.Index(msg, "parameter:")
	if idx == -1 {
		idx = strings.Index(msg, "parameter ")
	}
	if idx == -1 {
		return ""
	}
	param := strings.TrimSpace(msg[idx+len("parameter"):])
	param = strings.Trim(param, ": ")
	// Remove trailing words that are not part of the parameter name.
	if i := strings.Index(param, " "); i != -1 {
		param = param[:i]
	}
	return strings.TrimSpace(param)
}

// injectToolObservation creates a synthetic tool result message containing
// the translated observation and appends it to the session.
func (a *sessionAgent) injectToolObservation(ctx context.Context, sessionID, toolCallID, toolName string, err error) error {
	observation := translateToObservation(err, toolName)

	toolResult := message.ToolResult{
		ToolCallID: toolCallID,
		Name:       toolName,
		Content:    observation,
		IsError:    true,
	}

	_, createErr := a.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.Tool,
		Parts: []message.ContentPart{toolResult},
	})
	return createErr
}

// toolFailureKey returns a composite key for tracking consecutive failures
// of a specific tool call.
func toolFailureKey(toolName string) string {
	return "tool_failure:" + toolName
}
