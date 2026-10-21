package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/stretchr/testify/require"
)

// recordingStreamModel captures every fantasy.Call handed to it so tests can
// assert on the exact payload about to go over the wire.
type recordingStreamModel struct {
	finishStreamModel
	mu    sync.Mutex
	calls []fantasy.Call
}

func (m *recordingStreamModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	m.calls = append(m.calls, call)
	m.mu.Unlock()
	return m.finishStreamModel.Stream(ctx, call)
}

func (m *recordingStreamModel) recordedText() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sb strings.Builder
	for _, call := range m.calls {
		for _, msg := range call.Prompt {
			for _, part := range msg.Content {
				if tp, ok := part.(fantasy.TextPart); ok {
					sb.WriteString(tp.Text)
					sb.WriteString("\n")
				}
			}
		}
	}
	return sb.String()
}

const wireMaskSecret = "AKIAIOSVPK253PIP5TGP"

func TestSummarize_AppliesWireRedactionToHistory(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	rec := &recordingStreamModel{finishStreamModel: finishStreamModel{text: "summary"}}
	agent := testSessionAgent(env, rec, rec, "system").(*sessionAgent)
	agent.wireSecretsForced = true

	session, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	_, err = env.messages.Create(t.Context(), session.ID, message.CreateMessageParams{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "AWS_ACCESS_KEY_ID := \"" + wireMaskSecret + "\""},
		},
	})
	require.NoError(t, err)

	err = agent.Summarize(t.Context(), session.ID, nil)
	require.NoError(t, err)

	got := rec.recordedText()
	require.NotEmpty(t, got, "summarizer must have been called")
	require.NotContains(t, got, wireMaskSecret,
		"summarizer request must not carry the raw secret")
	require.Contains(t, got, "<redacted:gitleaks:",
		"summarizer request must carry the masked sentinel")
}

func TestGenerateTitle_AppliesWireRedactionToPrompt(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	rec := &recordingStreamModel{finishStreamModel: finishStreamModel{text: "My Title"}}
	agent := testSessionAgent(env, rec, rec, "system").(*sessionAgent)
	agent.wireSecretsForced = true

	session, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	agent.GenerateTitle(t.Context(), session.ID,
		"check my AWS_ACCESS_KEY_ID := \""+wireMaskSecret+"\" please")

	got := rec.recordedText()
	require.NotEmpty(t, got, "title model must have been called")
	require.NotContains(t, got, wireMaskSecret,
		"title request must not carry the raw secret")
	require.Contains(t, got, "<redacted:gitleaks:",
		"title request must carry the masked sentinel")
}
