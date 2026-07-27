package agent

import (
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

const initialPrompt = "Explain how the session auto-titling works"

func TestInitialPromptPreservedAcrossTurns(t *testing.T) {
	env := testEnv(t)
	model := &finishStreamModel{text: "done"}
	agent := testSessionAgent(env, model, model, "system")

	session, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	// First turn: send initial prompt
	res, err := agent.Run(t.Context(), SessionAgentCall{
		Prompt:          initialPrompt,
		SessionID:       session.ID,
		MaxOutputTokens: 10000,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	// Verify the user message was created
	msgs, err := env.messages.List(t.Context(), session.ID)
	require.NoError(t, err)

	// Find the user message and verify it contains the initial prompt
	foundInitialPrompt := false
	for _, msg := range msgs {
		if msg.Role == message.User {
			for _, p := range msg.Parts {
				if tc, ok := p.(message.TextContent); ok {
					if tc.Text == initialPrompt {
						foundInitialPrompt = true
					}
				}
			}
		}
	}
	require.True(t, foundInitialPrompt, "Initial prompt should be in the conversation history")

	// Second turn: send a follow-up prompt
	followUpPrompt := "Also explain how the small model is used for title generation"
	res2, err := agent.Run(t.Context(), SessionAgentCall{
		Prompt:          followUpPrompt,
		SessionID:       session.ID,
		MaxOutputTokens: 10000,
	})
	require.NoError(t, err)
	require.NotNil(t, res2)

	// Verify BOTH prompts are in the history
	msgs2, err := env.messages.List(t.Context(), session.ID)
	require.NoError(t, err)

	userMsgCount := 0
	initialFound := false
	followUpFound := false
	for _, msg := range msgs2 {
		if msg.Role == message.User {
			userMsgCount++
			for _, p := range msg.Parts {
				if tc, ok := p.(message.TextContent); ok {
					if tc.Text == initialPrompt {
						initialFound = true
					}
					if tc.Text == followUpPrompt {
						followUpFound = true
					}
				}
			}
		}
	}

	require.Equal(t, 2, userMsgCount, "Should have 2 user messages")
	require.True(t, initialFound, "Initial prompt should still be in the conversation history on the second turn")
	require.True(t, followUpFound, "Follow-up prompt should also be in the history")
}

func TestSessionAutoTitlingDeferred(t *testing.T) {
	env := testEnv(t)
	model := &finishStreamModel{text: "Expected Generated Title"}
	agent := testSessionAgent(env, model, model, "system")

	session, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	// Before the run, verify the title is "New Session"
	s, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "New Session", s.Title)

	// Run the agent. This starts and completes the first turn.
	res, err := agent.Run(t.Context(), SessionAgentCall{
		Prompt:          "Generate a title for this session please",
		SessionID:       session.ID,
		MaxOutputTokens: 1000,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	// Immediately after Run returns, the title might not be updated yet
	// because GenerateTitle runs in a background goroutine.
	// We poll the session until it changes from "New Session".
	var updatedTitle string
	require.Eventually(t, func() bool {
		s, err := env.sessions.Get(t.Context(), session.ID)
		if err != nil {
			return false
		}
		updatedTitle = s.Title
		return s.Title != "New Session"
	}, 5*time.Second, 10*time.Millisecond, "Session title should be updated asynchronously")

	require.Equal(t, "Expected Generated Title", updatedTitle)

	// Ensure messages remain clean and unpolluted by synthetic auto_title tool calls.
	msgs, err := env.messages.ListUnfiltered(t.Context(), session.ID)
	require.NoError(t, err)

	for _, msg := range msgs {
		if msg.Role == message.Assistant {
			for _, part := range msg.ToolCalls() {
				require.NotEqual(t, "auto_title", part.Name, "Assistant message must not contain synthetic auto_title tool call")
			}
		}
		if msg.Role == message.Tool {
			for _, part := range msg.ToolResults() {
				require.NotEqual(t, "auto_title", part.Name, "Tool messages must not contain synthetic auto_title tool result")
			}
		}
	}
}

func TestGenerateTitle_EmptyUserPromptParam(t *testing.T) {
	env := testEnv(t)
	model := &finishStreamModel{text: "Expected Generated Title From History"}
	agent := testSessionAgent(env, model, model, "system")

	session, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	// Create a user message in the session history
	_, err = env.messages.Create(t.Context(), session.ID, message.CreateMessageParams{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "Explain how session auto-titling works"},
		},
	})
	require.NoError(t, err)

	// Call GenerateTitle with an empty userPrompt (as /name slash command does)
	agent.GenerateTitle(t.Context(), session.ID, "")

	s, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "Expected Generated Title From History", s.Title)
}

func TestGenerateTitle_EmptyUserPromptParam_DebouncedUpdate(t *testing.T) {
	env := testEnv(t)
	model := &finishStreamModel{text: "Expected Generated Title From History"}
	agent := testSessionAgent(env, model, model, "system")

	session, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	userMsg, err := env.messages.Create(t.Context(), session.ID, message.CreateMessageParams{
		Role: message.User,
	})
	require.NoError(t, err)

	userMsg.Parts = []message.ContentPart{
		message.TextContent{Text: "Explain how session auto-titling works"},
	}
	err = env.messages.Update(t.Context(), userMsg)
	require.NoError(t, err)

	// Call GenerateTitle with empty prompt immediately before debounce flushes
	agent.GenerateTitle(t.Context(), session.ID, "")

	s, err := env.sessions.Get(t.Context(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "Expected Generated Title From History", s.Title)
}
