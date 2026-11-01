package agent

import (
	"context"
	"sync"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/agent/tools"
	"github.com/hackafterdark/phosphor/pkg/session"
)

// todoNudgeThreshold is the number of mutating tool calls allowed after a
// todos update before the model gets a nudge. Small models that drift off
// plan without touching the todo list are the target; 8 is deliberately
// loose so legitimate deep work stays quiet.
const todoNudgeThreshold = 8

// todoNudgeToolNames are the tools whose results can carry the nudge. They
// are the ones a drifting model spends long stretches on.
var todoNudgeToolNames = map[string]bool{
	"bash":      true,
	"edit":      true,
	"multiedit": true,
	"write":     true,
	"append":    true,
}

// todoNudge wraps a tool to re-anchor a drifting model on its open todo.
// The reminder is appended to the tool result exactly once, as the result
// is produced, so the transcript stays append-only and no cached prefix is
// ever invalidated.
type todoNudge struct {
	inner    fantasy.AgentTool
	sessions session.Service
	mu       *sync.Mutex
	drift    *map[string]int
}

// wrapToolsWithTodoNudge wraps every tool so the drift counter and the
// nudge share one state instance per tool set.
func wrapToolsWithTodoNudge(tools []fantasy.AgentTool, sessions session.Service) []fantasy.AgentTool {
	var mu sync.Mutex
	drift := make(map[string]int)
	out := make([]fantasy.AgentTool, len(tools))
	for i, tool := range tools {
		out[i] = &todoNudge{inner: tool, sessions: sessions, mu: &mu, drift: &drift}
	}
	return out
}

func (t *todoNudge) Info() fantasy.ToolInfo {
	return t.inner.Info()
}

func (t *todoNudge) ProviderOptions() fantasy.ProviderOptions {
	return t.inner.ProviderOptions()
}

func (t *todoNudge) SetProviderOptions(opts fantasy.ProviderOptions) {
	t.inner.SetProviderOptions(opts)
}

func (t *todoNudge) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	resp, err := t.inner.Run(ctx, call)
	if err != nil {
		return resp, err
	}
	sessionID := tools.GetSessionFromContext(ctx)
	if sessionID == "" {
		return resp, nil
	}

	t.mu.Lock()
	if call.Name == tools.TodosToolName {
		delete(*t.drift, sessionID)
		t.mu.Unlock()
		return resp, nil
	}
	if !todoNudgeToolNames[call.Name] {
		t.mu.Unlock()
		return resp, nil
	}
	(*t.drift)[sessionID]++
	if (*t.drift)[sessionID] < todoNudgeThreshold {
		t.mu.Unlock()
		return resp, nil
	}
	(*t.drift)[sessionID] = 0
	t.mu.Unlock()

	currentSession, err := t.sessions.Get(ctx, sessionID)
	if err != nil {
		return resp, nil
	}
	open := openTodoText(currentSession)
	if open == "" {
		return resp, nil
	}
	if resp.Content == "" {
		resp.Content = open
	} else {
		resp.Content += "\n" + open
	}
	return resp, nil
}

// openTodoText returns the nudge line for the active (in_progress first,
// then pending) todo, or "" when the list has nothing open.
func openTodoText(currentSession session.Session) string {
	var pending string
	for _, todo := range currentSession.Todos {
		switch todo.Status {
		case session.TodoStatusInProgress:
			if todo.ActiveForm != "" {
				return "[Reminder: Active todo in progress: \"" + todo.ActiveForm + "\". Call 'todos' when complete.]"
			}
			return "[Reminder: Active todo in progress: \"" + todo.Content + "\". Call 'todos' when complete.]"
		case session.TodoStatusPending:
			if pending == "" {
				pending = todo.Content
			}
		}
	}
	if pending != "" {
		return "[Reminder: Next pending todo: \"" + pending + "\". Call 'todos' to mark progress.]"
	}
	return ""
}
