package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"unicode"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"github.com/hackafterdark/phosphor/pkg/session"
	"go.opentelemetry.io/otel/attribute"
)

//go:embed todos.md
var todosDescription string

const TodosToolName = "todos"

type TodosParams struct {
	Todos []TodoItem `json:"todos" description:"The updated todo list"`
}

type TodoItem struct {
	Content    string `json:"content" description:"What needs to be done (imperative form)"`
	Status     string `json:"status" description:"Task status: pending, in_progress, or completed"`
	ActiveForm string `json:"active_form" description:"Present continuous form (e.g., 'Running tests')"`
}

type TodosResponseMetadata struct {
	IsNew         bool           `json:"is_new"`
	Todos         []session.Todo `json:"todos"`
	JustCompleted []string       `json:"just_completed,omitempty"`
	JustStarted   string         `json:"just_started,omitempty"`
	Completed     int            `json:"completed"`
	Total         int            `json:"total"`
}

func NewTodosTool(sessions session.Service) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TodosToolName,
		todosDescription,
		func(ctx context.Context, params TodosParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool todos")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", TodosToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for managing todos")
			}

			currentSession, err := sessions.Get(ctx, sessionID)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to get session: %w", err)
			}

			isNew := len(currentSession.Todos) == 0
			oldStatusByContent := make(map[string]session.TodoStatus)
			for _, todo := range currentSession.Todos {
				oldStatusByContent[todo.Content] = todo.Status
			}

			for _, item := range params.Todos {
				switch item.Status {
				case "pending", "in_progress", "completed":
				default:
					return fantasy.ToolResponse{}, fmt.Errorf("invalid status %q for todo %q", item.Status, item.Content)
				}
			}

			todos := make([]session.Todo, len(params.Todos))
			var justCompleted []string
			var justStarted string
			completedCount := 0

			for i, item := range params.Todos {
				todos[i] = session.Todo{
					Content:    item.Content,
					Status:     session.TodoStatus(item.Status),
					ActiveForm: item.ActiveForm,
				}

				newStatus := session.TodoStatus(item.Status)
				oldStatus, existed := oldStatusByContent[item.Content]

				if newStatus == session.TodoStatusCompleted {
					completedCount++
					if existed && oldStatus != session.TodoStatusCompleted {
						justCompleted = append(justCompleted, item.Content)
					}
				}

				if newStatus == session.TodoStatusInProgress {
					if !existed || oldStatus != session.TodoStatusInProgress {
						if item.ActiveForm != "" {
							justStarted = item.ActiveForm
						} else {
							justStarted = item.Content
						}
					}
				}
			}

			currentSession.Todos = todos
			_, err = sessions.Save(ctx, currentSession)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to save todos: %w", err)
			}

			response := "Todo list updated successfully.\n\n"

			// The full open list rides the tool result so the freshest plan
			// is always part of the transcript instead of the system prompt.
			// The system block must stay byte-stable for provider prefix
			// caches; see _PLANS/PROMPT_CACHE_OPTIMIZATION.md.
			response += renderOpenTodoList(todos)

			pendingCount := 0
			inProgressCount := 0

			for _, todo := range todos {
				switch todo.Status {
				case session.TodoStatusPending:
					pendingCount++
				case session.TodoStatusInProgress:
					inProgressCount++
				}
			}

			response += fmt.Sprintf("Status: %d pending, %d in progress, %d completed\n",
				pendingCount, inProgressCount, completedCount)

			response += "Todos have been modified successfully. Ensure that you continue to use the todo list to track your progress. Please proceed with the current tasks if applicable."

			metadata := TodosResponseMetadata{
				IsNew:         isNew,
				Todos:         todos,
				JustCompleted: justCompleted,
				JustStarted:   justStarted,
				Completed:     completedCount,
				Total:         len(todos),
			}

			return fantasy.WithResponseMetadata(fantasy.NewTextResponse(response), metadata), nil
		},
	)
}

// renderOpenTodoList renders the not-yet-completed todos as a tagged block
// for the model. It mirrors the format the state used to carry in the
// system prompt. Returns "" when nothing is open.
func renderOpenTodoList(todos []session.Todo) string {
	var sb strings.Builder
	for i, todo := range todos {
		if todo.Status == session.TodoStatusCompleted {
			continue
		}
		fmt.Fprintf(&sb, "%d. %s (%s)\n", i+1, sanitizeTodoText(todo.Content), todo.ActiveForm)
	}
	result := strings.TrimSpace(sb.String())
	if result == "" {
		return ""
	}
	const maxLen = 2000
	if len(result) > maxLen {
		result = strings.ToValidUTF8(result[:maxLen], "") + "\n[truncated, some items omitted]"
	}
	return "<todo_list>\n" + result + "\n</todo_list>\n\n"
}

// sanitizeTodoText strips non-printable characters, preserving newlines and
// tabs, so malformed todo content cannot corrupt the tool result.
func sanitizeTodoText(s string) string {
	var out []rune
	for _, r := range s {
		if unicode.IsPrint(r) || r == '\n' || r == '\t' {
			out = append(out, r)
		}
	}
	return string(out)
}
