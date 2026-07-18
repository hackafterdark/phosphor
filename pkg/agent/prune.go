package agent

import (
	"fmt"
	"strings"

	"github.com/hackafterdark/phosphor/pkg/message"
)

// pruneToolOutputs truncates verbose tool results to keepChars.
func pruneToolOutputs(msgs []message.Message, keepChars int) []message.Message {
	pruned := make([]message.Message, 0, len(msgs))

	for _, msg := range msgs {
		text := msg.Content().Text
		isTool := len(msg.ToolResults()) > 0

		if isTool && len(text) > keepChars {
			truncated := text[:keepChars] + "\n... [truncated]"
			tc := message.TextContent{Text: truncated}
			newParts := make([]message.ContentPart, len(msg.Parts))
			copy(newParts, msg.Parts)
			newParts[0] = tc

			newMsg := msg
			newMsg.Parts = newParts
			pruned = append(pruned, newMsg)
		} else {
			pruned = append(pruned, msg)
		}
	}

	return pruned
}

// isOverflowError checks if an error indicates context overflow.
func isOverflowError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context") ||
		strings.Contains(msg, "overflow") ||
		strings.Contains(msg, "exceed") ||
		strings.Contains(msg, "too large") ||
		strings.Contains(msg, "token limit")
}

// PruneAndBuild builds a plain-text conversation with pruned tool outputs.
func PruneAndBuild(msgs []message.Message, keepChars int) string {
	pruned := pruneToolOutputs(msgs, keepChars)
	var buf strings.Builder
	for _, msg := range pruned {
		buf.WriteString(fmt.Sprintf("[%s] %s\n", msg.Role, msg.Content().Text))
	}
	return buf.String()
}

// AggressiveBuild prunes aggressively for overflow recovery.
func AggressiveBuild(msgs []message.Message, keepChars int) string {
	return PruneAndBuild(msgs, keepChars)
}