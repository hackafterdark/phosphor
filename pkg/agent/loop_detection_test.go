package agent

import (
	"fmt"
	"testing"

	"charm.land/fantasy"
)

// makeStep creates a StepResult with the given tool calls and results in its Content.
func makeStep(calls []fantasy.ToolCallContent, results []fantasy.ToolResultContent) fantasy.StepResult {
	var content fantasy.ResponseContent
	for _, c := range calls {
		content = append(content, c)
	}
	for _, r := range results {
		content = append(content, r)
	}
	return fantasy.StepResult{
		Response: fantasy.Response{
			Content: content,
		},
	}
}

// makeToolStep creates a step with a single tool call and matching text result.
func makeToolStep(name, input, output string) fantasy.StepResult {
	callID := fmt.Sprintf("call_%s_%s", name, input)
	return makeStep(
		[]fantasy.ToolCallContent{
			{ToolCallID: callID, ToolName: name, Input: input},
		},
		[]fantasy.ToolResultContent{
			{ToolCallID: callID, ToolName: name, Result: fantasy.ToolResultOutputContentText{Text: output}},
		},
	)
}

// makeEmptyStep creates a step with no tool calls (e.g. a text-only response).
func makeEmptyStep() fantasy.StepResult {
	return fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "thinking..."},
			},
		},
	}
}

func TestHasRepeatedToolCalls(t *testing.T) {
	t.Run("no steps", func(t *testing.T) {
		result := hasRepeatedToolCalls(nil, 10, 5)
		if result {
			t.Error("expected false for empty steps")
		}
	})

	t.Run("fewer steps than window", func(t *testing.T) {
		steps := make([]fantasy.StepResult, 5)
		for i := range steps {
			steps[i] = makeToolStep("read", `{"file":"a.go"}`, "content")
		}
		result := hasRepeatedToolCalls(steps, 10, 5)
		if result {
			t.Error("expected false when fewer steps than window size")
		}
	})

	t.Run("all different signatures", func(t *testing.T) {
		steps := make([]fantasy.StepResult, 10)
		for i := range steps {
			steps[i] = makeToolStep("tool", fmt.Sprintf(`{"i":%d}`, i), fmt.Sprintf("result-%d", i))
		}
		result := hasRepeatedToolCalls(steps, 10, 5)
		if result {
			t.Error("expected false when all signatures are different")
		}
	})

	t.Run("exact repeat at threshold not detected", func(t *testing.T) {
		// maxRepeats=5 means > 5 is needed, so exactly 5 should return false
		steps := make([]fantasy.StepResult, 10)
		for i := range 5 {
			steps[i] = makeToolStep("read", `{"file":"a.go"}`, "content")
		}
		for i := 5; i < 10; i++ {
			steps[i] = makeToolStep("tool", fmt.Sprintf(`{"i":%d}`, i), fmt.Sprintf("result-%d", i))
		}
		result := hasRepeatedToolCalls(steps, 10, 5)
		if result {
			t.Error("expected false when count equals maxRepeats (threshold is >)")
		}
	})

	t.Run("loop detected", func(t *testing.T) {
		// 6 identical steps in a window of 10 with maxRepeats=5 → detected
		steps := make([]fantasy.StepResult, 10)
		for i := range 6 {
			steps[i] = makeToolStep("read", `{"file":"a.go"}`, "content")
		}
		for i := 6; i < 10; i++ {
			steps[i] = makeToolStep("tool", fmt.Sprintf(`{"i":%d}`, i), fmt.Sprintf("result-%d", i))
		}
		result := hasRepeatedToolCalls(steps, 10, 5)
		if !result {
			t.Error("expected true when same signature appears more than maxRepeats times")
		}
	})

	t.Run("steps without tool calls are skipped", func(t *testing.T) {
		// Mix of tool steps and empty steps — empty ones should not affect counts
		steps := make([]fantasy.StepResult, 10)
		for i := range 4 {
			steps[i] = makeToolStep("read", `{"file":"a.go"}`, "content")
		}
		for i := 4; i < 8; i++ {
			steps[i] = makeEmptyStep()
		}
		for i := 8; i < 10; i++ {
			steps[i] = makeToolStep("write", `{"file":"b.go"}`, "ok")
		}
		result := hasRepeatedToolCalls(steps, 10, 5)
		if result {
			t.Error("expected false: only 4 repeated tool calls, empty steps should be skipped")
		}
	})

	t.Run("multiple different patterns alternating", func(t *testing.T) {
		// Two patterns alternating: each appears 5 times — not above threshold
		steps := make([]fantasy.StepResult, 10)
		for i := range steps {
			if i%2 == 0 {
				steps[i] = makeToolStep("read", `{"file":"a.go"}`, "content-a")
			} else {
				steps[i] = makeToolStep("write", `{"file":"b.go"}`, "content-b")
			}
		}
		result := hasRepeatedToolCalls(steps, 10, 5)
		if result {
			t.Error("expected false: two patterns each appearing 5 times (not > 5)")
		}
	})
}

func TestGetToolInteractionSignature(t *testing.T) {
	t.Run("empty content returns empty string", func(t *testing.T) {
		sig := getToolInteractionSignature(fantasy.ResponseContent{})
		if sig != "" {
			t.Errorf("expected empty string, got %q", sig)
		}
	})

	t.Run("text only content returns empty string", func(t *testing.T) {
		content := fantasy.ResponseContent{
			fantasy.TextContent{Text: "hello"},
		}
		sig := getToolInteractionSignature(content)
		if sig != "" {
			t.Errorf("expected empty string, got %q", sig)
		}
	})

	t.Run("tool call with result produces signature", func(t *testing.T) {
		content := fantasy.ResponseContent{
			fantasy.ToolCallContent{ToolCallID: "1", ToolName: "read", Input: `{"file":"a.go"}`},
			fantasy.ToolResultContent{ToolCallID: "1", ToolName: "read", Result: fantasy.ToolResultOutputContentText{Text: "content"}},
		}
		sig := getToolInteractionSignature(content)
		if sig == "" {
			t.Error("expected non-empty signature")
		}
	})

	t.Run("same interactions produce same signature", func(t *testing.T) {
		content1 := fantasy.ResponseContent{
			fantasy.ToolCallContent{ToolCallID: "1", ToolName: "read", Input: `{"file":"a.go"}`},
			fantasy.ToolResultContent{ToolCallID: "1", ToolName: "read", Result: fantasy.ToolResultOutputContentText{Text: "content"}},
		}
		content2 := fantasy.ResponseContent{
			fantasy.ToolCallContent{ToolCallID: "2", ToolName: "read", Input: `{"file":"a.go"}`},
			fantasy.ToolResultContent{ToolCallID: "2", ToolName: "read", Result: fantasy.ToolResultOutputContentText{Text: "content"}},
		}
		sig1 := getToolInteractionSignature(content1)
		sig2 := getToolInteractionSignature(content2)
		if sig1 != sig2 {
			t.Errorf("expected same signature for same interactions, got %q and %q", sig1, sig2)
		}
	})

	t.Run("different inputs produce different signatures", func(t *testing.T) {
		content1 := fantasy.ResponseContent{
			fantasy.ToolCallContent{ToolCallID: "1", ToolName: "read", Input: `{"file":"a.go"}`},
			fantasy.ToolResultContent{ToolCallID: "1", ToolName: "read", Result: fantasy.ToolResultOutputContentText{Text: "content"}},
		}
		content2 := fantasy.ResponseContent{
			fantasy.ToolCallContent{ToolCallID: "1", ToolName: "read", Input: `{"file":"b.go"}`},
			fantasy.ToolResultContent{ToolCallID: "1", ToolName: "read", Result: fantasy.ToolResultOutputContentText{Text: "content"}},
		}
		sig1 := getToolInteractionSignature(content1)
		sig2 := getToolInteractionSignature(content2)
		if sig1 == sig2 {
			t.Error("expected different signatures for different inputs")
		}
	})
}

// makeReasoningStep creates a step with only reasoning content.
func makeReasoningStep(text string) fantasy.StepResult {
	return fantasy.StepResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.ReasoningContent{Text: text},
			},
		},
	}
}

func TestIsReasoningOnlyStep(t *testing.T) {
	t.Run("empty content returns false", func(t *testing.T) {
		text, ok := isReasoningOnlyStep(fantasy.ResponseContent{})
		if ok {
			t.Error("expected false for empty content")
		}
		if text != "" {
			t.Errorf("expected empty string, got %q", text)
		}
	})

	t.Run("reasoning only returns text", func(t *testing.T) {
		content := fantasy.ResponseContent{
			fantasy.ReasoningContent{Text: "let me think..."},
		}
		text, ok := isReasoningOnlyStep(content)
		if !ok {
			t.Error("expected true for reasoning-only step")
		}
		if text != "let me think..." {
			t.Errorf("expected 'let me think...', got %q", text)
		}
	})

	t.Run("reasoning with tool calls returns false", func(t *testing.T) {
		content := fantasy.ResponseContent{
			fantasy.ReasoningContent{Text: "thinking..."},
			fantasy.ToolCallContent{ToolName: "read", Input: `{"file":"a.go"}`},
		}
		_, ok := isReasoningOnlyStep(content)
		if ok {
			t.Error("expected false when tool calls present")
		}
	})

	t.Run("reasoning with text returns false", func(t *testing.T) {
		content := fantasy.ResponseContent{
			fantasy.ReasoningContent{Text: "thinking..."},
			fantasy.TextContent{Text: "the answer is 42"},
		}
		_, ok := isReasoningOnlyStep(content)
		if ok {
			t.Error("expected false when final text present")
		}
	})

	t.Run("text only returns false", func(t *testing.T) {
		content := fantasy.ResponseContent{
			fantasy.TextContent{Text: "hello world"},
		}
		_, ok := isReasoningOnlyStep(content)
		if ok {
			t.Error("expected false for text-only step")
		}
	})

	t.Run("multiple reasoning parts combined", func(t *testing.T) {
		content := fantasy.ResponseContent{
			fantasy.ReasoningContent{Text: "first part"},
			fantasy.ReasoningContent{Text: "second part"},
		}
		text, ok := isReasoningOnlyStep(content)
		if !ok {
			t.Error("expected true for multi-reasoning step")
		}
		if text != "first partsecond part" {
			t.Errorf("expected combined text, got %q", text)
		}
	})
}

func TestHasConsecutiveToolFailures(t *testing.T) {
	t.Run("no steps returns false", func(t *testing.T) {
		result := hasConsecutiveToolFailures(nil)
		if result {
			t.Error("expected false for empty steps")
		}
	})

	t.Run("fewer steps than window returns false", func(t *testing.T) {
		steps := make([]fantasy.StepResult, 5)
		for i := range steps {
			steps[i] = makeStep(
				[]fantasy.ToolCallContent{{ToolCallID: "1", ToolName: "grep", Input: `{}`}},
				[]fantasy.ToolResultContent{{ToolCallID: "1", ToolName: "grep", Result: fantasy.ToolResultOutputContentError{}}},
			)
		}
		result := hasConsecutiveToolFailures(steps)
		if result {
			t.Error("expected false when fewer steps than window")
		}
	})

	t.Run("success resets failure count", func(t *testing.T) {
		// 2 failures, then a success, then 2 more failures
		// The success resets the count, so only 2 failures after reset
		// which equals toolFailureMaxCount (2) but doesn't exceed it.
		steps := make([]fantasy.StepResult, 10)
		// 2 failures before success
		for i := range 2 {
			steps[i] = makeStep(
				[]fantasy.ToolCallContent{{ToolCallID: fmt.Sprintf("fail_%d", i), ToolName: "grep", Input: `{}`}},
				[]fantasy.ToolResultContent{{ToolCallID: fmt.Sprintf("fail_%d", i), ToolName: "grep", Result: fantasy.ToolResultOutputContentError{}}},
			)
		}
		// Success
		steps[2] = makeToolStep("grep", `{}`, "ok")
		// 2 more failures after reset (at threshold, not exceeding)
		for i := 3; i < 5; i++ {
			steps[i] = makeStep(
				[]fantasy.ToolCallContent{{ToolCallID: fmt.Sprintf("fail2_%d", i), ToolName: "grep", Input: `{}`}},
				[]fantasy.ToolResultContent{{ToolCallID: fmt.Sprintf("fail2_%d", i), ToolName: "grep", Result: fantasy.ToolResultOutputContentError{}}},
			)
		}
		// Fill rest with different tools
		for i := 5; i < 10; i++ {
			steps[i] = makeToolStep("read", fmt.Sprintf(`{"i":%d}`, i), fmt.Sprintf("result-%d", i))
		}
		result := hasConsecutiveToolFailures(steps)
		if result {
			t.Error("expected false: success should reset failure count, only 2 failures after reset")
		}
	})

	t.Run("exceeds threshold returns true", func(t *testing.T) {
		// 3 consecutive failures for same tool (exceeds toolFailureMaxCount=2)
		steps := make([]fantasy.StepResult, 10)
		for i := range 3 {
			steps[i] = makeStep(
				[]fantasy.ToolCallContent{{ToolCallID: fmt.Sprintf("fail_%d", i), ToolName: "grep", Input: `{}`}},
				[]fantasy.ToolResultContent{{ToolCallID: fmt.Sprintf("fail_%d", i), ToolName: "grep", Result: fantasy.ToolResultOutputContentError{}}},
			)
		}
		// Fill rest with different tools
		for i := 3; i < 10; i++ {
			steps[i] = makeToolStep("read", fmt.Sprintf(`{"i":%d}`, i), fmt.Sprintf("result-%d", i))
		}
		result := hasConsecutiveToolFailures(steps)
		if !result {
			t.Error("expected true: 3 consecutive failures exceeds threshold of 2")
		}
	})

	t.Run("different tools tracked separately", func(t *testing.T) {
		// 2 failures for grep, 2 failures for read (each at threshold but not exceeding)
		steps := make([]fantasy.StepResult, 10)
		for i := range 2 {
			steps[i] = makeStep(
				[]fantasy.ToolCallContent{{ToolCallID: fmt.Sprintf("grep_fail_%d", i), ToolName: "grep", Input: `{}`}},
				[]fantasy.ToolResultContent{{ToolCallID: fmt.Sprintf("grep_fail_%d", i), ToolName: "grep", Result: fantasy.ToolResultOutputContentError{}}},
			)
		}
		for i := 2; i < 4; i++ {
			steps[i] = makeStep(
				[]fantasy.ToolCallContent{{ToolCallID: fmt.Sprintf("read_fail_%d", i-2), ToolName: "read", Input: `{}`}},
				[]fantasy.ToolResultContent{{ToolCallID: fmt.Sprintf("read_fail_%d", i-2), ToolName: "read", Result: fantasy.ToolResultOutputContentError{}}},
			)
		}
		// Fill rest with different tools
		for i := 4; i < 10; i++ {
			steps[i] = makeToolStep("write", fmt.Sprintf(`{"i":%d}`, i), fmt.Sprintf("result-%d", i))
		}
		result := hasConsecutiveToolFailures(steps)
		if result {
			t.Error("expected false: each tool has exactly 2 failures (at threshold, not exceeding)")
		}
	})
}

func TestHasRepeatedThinking(t *testing.T) {
	t.Run("fewer steps than threshold", func(t *testing.T) {
		steps := make([]fantasy.StepResult, 4)
		for i := range steps {
			steps[i] = makeReasoningStep("thinking...")
		}
		result := hasRepeatedThinking(steps)
		if result {
			t.Error("expected false when fewer steps than window size")
		}
	})

	t.Run("no reasoning steps returns false", func(t *testing.T) {
		steps := make([]fantasy.StepResult, 10)
		for i := range steps {
			steps[i] = makeToolStep("read", fmt.Sprintf(`{"i":%d}`, i), fmt.Sprintf("result-%d", i))
		}
		result := hasRepeatedThinking(steps)
		if result {
			t.Error("expected false when no reasoning steps")
		}
	})

	t.Run("different reasoning texts returns false", func(t *testing.T) {
		steps := make([]fantasy.StepResult, 10)
		for i := range steps {
			steps[i] = makeReasoningStep(fmt.Sprintf("thinking step %d", i))
		}
		result := hasRepeatedThinking(steps)
		if result {
			t.Error("expected false when reasoning texts are all different")
		}
	})

	t.Run("mixed reasoning and tool steps returns false", func(t *testing.T) {
		steps := make([]fantasy.StepResult, 10)
		for i := range steps {
			if i%2 == 0 {
				steps[i] = makeReasoningStep("thinking...")
			} else {
				steps[i] = makeToolStep("read", fmt.Sprintf(`{"i":%d}`, i), fmt.Sprintf("result-%d", i))
			}
		}
		result := hasRepeatedThinking(steps)
		if result {
			t.Error("expected false when reasoning steps are mixed with tool steps")
		}
	})

	t.Run("reasoning with tool calls not detected", func(t *testing.T) {
		steps := make([]fantasy.StepResult, 7)
		for i := range steps {
			steps[i] = fantasy.StepResult{
				Response: fantasy.Response{
					Content: fantasy.ResponseContent{
						fantasy.ReasoningContent{Text: "thinking..."},
						fantasy.ToolCallContent{ToolName: "read", Input: `{"file":"a.go"}`},
						fantasy.ToolResultContent{
							ToolCallID: "call_1", ToolName: "read",
							Result: fantasy.ToolResultOutputContentText{Text: "content"},
						},
					},
				},
			}
		}
		result := hasRepeatedThinking(steps)
		if result {
			t.Error("expected false: steps have tool calls, not reasoning-only")
		}
	})

	t.Run("reasoning with final text not detected", func(t *testing.T) {
		steps := make([]fantasy.StepResult, 5)
		for i := range steps {
			steps[i] = fantasy.StepResult{
				Response: fantasy.Response{
					Content: fantasy.ResponseContent{
						fantasy.ReasoningContent{Text: "thinking..."},
						fantasy.TextContent{Text: "here is the answer"},
					},
				},
			}
		}
		result := hasRepeatedThinking(steps)
		if result {
			t.Error("expected false: steps have final text, considered progress")
		}
	})

	t.Run("exact repeat at threshold not detected", func(t *testing.T) {
		// 3 identical reasoning steps with window=5, maxRepeats=2 → 3 > 2, detected
		steps := make([]fantasy.StepResult, 5)
		steps[0] = makeReasoningStep("different thinking")
		steps[1] = makeReasoningStep("same thinking")
		steps[2] = makeReasoningStep("same thinking")
		steps[3] = makeReasoningStep("same thinking")
		steps[4] = makeReasoningStep("different thinking")
		result := hasRepeatedThinking(steps)
		if !result {
			t.Error("expected true: 3 identical steps > maxRepeats=2")
		}
	})

	t.Run("loop detected with repeated reasoning", func(t *testing.T) {
		// 5 identical reasoning steps in window=5, maxRepeats=2 → 5 > 2, detected
		steps := make([]fantasy.StepResult, 5)
		for i := range steps {
			steps[i] = makeReasoningStep("looping thinking content")
		}
		result := hasRepeatedThinking(steps)
		if !result {
			t.Error("expected true when same reasoning repeats more than maxRepeats times")
		}
	})

	t.Run("exact repeat at threshold not detected", func(t *testing.T) {
		// 3 identical reasoning steps with maxRepeats=2 → 2 is not > 2, not detected
		steps := make([]fantasy.StepResult, 3)
		for i := range steps {
			steps[i] = makeReasoningStep("same thinking")
		}
		result := hasRepeatedThinking(steps)
		if result {
			t.Error("expected false when count equals maxRepeats (threshold is >)")
		}
	})
}

func TestDeduplicateReasoning(t *testing.T) {
	agent := &sessionAgent{}

	t.Run("no assistant messages returns unchanged", func(t *testing.T) {
		msgs := []fantasy.Message{
			{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "hello"}}},
		}
		result := agent.deduplicateReasoning(msgs)
		if len(result) != 1 {
			t.Error("expected 1 message")
		}
	})

	t.Run("single assistant message with reasoning returns unchanged", func(t *testing.T) {
		msgs := []fantasy.Message{
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: "let me think..."}},
			},
		}
		result := agent.deduplicateReasoning(msgs)
		if len(result) != 1 {
			t.Error("expected 1 message")
		}
		if len(result[0].Content) != 1 {
			t.Error("expected 1 content part")
		}
	})

	t.Run("consecutive identical reasoning stripped", func(t *testing.T) {
		msgs := []fantasy.Message{
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: "looping thinking content"}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: "looping thinking content"}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: "looping thinking content"}},
			},
		}
		result := agent.deduplicateReasoning(msgs)
		// First message keeps reasoning, subsequent ones are stripped.
		if len(result) != 3 {
			t.Errorf("expected 3 messages, got %d", len(result))
		}
		if len(result[0].Content) != 1 {
			t.Error("expected first message to keep reasoning")
		}
		if len(result[1].Content) != 0 {
			t.Errorf("expected second message to have 0 parts, got %d", len(result[1].Content))
		}
		if len(result[2].Content) != 0 {
			t.Errorf("expected third message to have 0 parts, got %d", len(result[2].Content))
		}
	})

	t.Run("different reasoning kept", func(t *testing.T) {
		msgs := []fantasy.Message{
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: "first thought"}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: "second thought"}},
			},
		}
		result := agent.deduplicateReasoning(msgs)
		if len(result) != 2 {
			t.Error("expected 2 messages")
		}
		if len(result[0].Content) != 1 || len(result[1].Content) != 1 {
			t.Error("expected both messages to keep their reasoning")
		}
	})

	t.Run("reasoning with other parts keeps non-reasoning", func(t *testing.T) {
		msgs := []fantasy.Message{
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: "thinking..."}, fantasy.TextPart{Text: "answer"}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: "thinking..."}, fantasy.TextPart{Text: "answer"}},
			},
		}
		result := agent.deduplicateReasoning(msgs)
		if len(result[0].Content) != 2 {
			t.Error("expected first message to keep both parts")
		}
		if len(result[1].Content) != 1 {
			t.Errorf("expected second message to have 1 part (text only), got %d", len(result[1].Content))
		}
	})

	t.Run("user messages break reasoning tracking", func(t *testing.T) {
		msgs := []fantasy.Message{
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: "same reasoning"}},
			},
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "follow up"}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: "same reasoning"}},
			},
		}
		result := agent.deduplicateReasoning(msgs)
		// User message resets tracking, so both assistant messages keep their reasoning.
		if len(result[0].Content) != 1 {
			t.Error("expected first assistant to keep reasoning")
		}
		if len(result[2].Content) != 1 {
			t.Error("expected second assistant (after user) to keep reasoning")
		}
	})

	t.Run("empty reasoning text not tracked", func(t *testing.T) {
		msgs := []fantasy.Message{
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: ""}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.ReasoningPart{Text: ""}},
			},
		}
		result := agent.deduplicateReasoning(msgs)
		// Empty reasoning should not be tracked or stripped.
		if len(result[0].Content) != 1 {
			t.Error("expected first message to keep empty reasoning")
		}
		if len(result[1].Content) != 1 {
			t.Error("expected second message to keep empty reasoning")
		}
	})
}
