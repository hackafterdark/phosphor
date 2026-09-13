package goal

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"sync"
	"text/template"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/agent/notify"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/pubsub"
)

//go:embed continuation_prompt.md.tpl
var continuationPromptTmpl []byte

var continuationTpl = template.Must(
	template.New("continuation").Parse(string(continuationPromptTmpl)),
)

type AgentRunner interface {
	Run(ctx context.Context, sessionID string, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error)
	IsSessionBusy(sessionID string) bool
	QueuedPrompts(sessionID string) int
}

type Runtime struct {
	store  Service
	agent  AgentRunner
	notify pubsub.Publisher[notify.Notification]

	// maxContinuations resolves the per-goal continuation budget at the
	// moment a continuation is about to start. It is read lazily so a
	// config change is honored without rebuilding the runtime. A nil
	// accessor falls back to DefaultMaxContinuations.
	maxContinuations func() int

	// budgetMu guards continuations.
	budgetMu      sync.Mutex
	continuations map[string]continuationState
}

// continuationState tracks how many synthetic continuation turns have been
// started for the currently active goal of a session. It is keyed so a
// recreate of the goal (a new goal ID) automatically resets the count.
type continuationState struct {
	goalID string
	count  int
}

// DefaultMaxContinuations is the fallback budget applied when no explicit
// budget is configured. It bounds a goal whose model never calls
// update_goal(complete) so an unattended run cannot loop indefinitely.
const DefaultMaxContinuations = 25

func NewRuntime(store Service, agent AgentRunner, notify pubsub.Publisher[notify.Notification], maxContinuations func() int) *Runtime {
	return &Runtime{
		store:            store,
		agent:            agent,
		notify:           notify,
		maxContinuations: maxContinuations,
		continuations:    make(map[string]continuationState),
	}
}

// budget returns the effective continuation budget. A configured value of 0
// means use the default; a negative value means unlimited.
func (r *Runtime) budget() int {
	if r.maxContinuations == nil {
		return DefaultMaxContinuations
	}
	v := r.maxContinuations()
	if v == 0 {
		return DefaultMaxContinuations
	}
	return v
}

// ResetContinuations clears the continuation counter for a session. It is
// called when a goal is created or resumed so the user gets a fresh budget
// window after consciously deciding to keep going.
func (r *Runtime) ResetContinuations(sessionID string) {
	if r == nil {
		return
	}
	r.budgetMu.Lock()
	delete(r.continuations, sessionID)
	r.budgetMu.Unlock()
}

// consumeContinuation increments and checks the budget for the session's
// active goal. It reports whether the continuation is allowed to proceed.
// When the budget is exhausted it pauses the goal, notifies the user, and
// returns false so the caller does not start another turn.
func (r *Runtime) consumeContinuation(ctx context.Context, g *Goal) bool {
	budget := r.budget()
	if budget < 0 {
		return true
	}

	r.budgetMu.Lock()
	st := r.continuations[g.SessionID]
	if st.goalID != g.GoalID {
		st = continuationState{goalID: g.GoalID}
	}
	st.count++
	r.continuations[g.SessionID] = st
	used := st.count
	r.budgetMu.Unlock()

	if used <= budget {
		return true
	}

	// Budget exhausted: pause the goal so it stops burning tokens and ask
	// the user what to do.
	if _, err := r.store.UpdateStatus(ctx, g.SessionID, g.GoalID, GoalPaused); err != nil {
		slog.Error("Failed to auto-pause goal at continuation budget", "session_id", g.SessionID, "goal_id", g.GoalID, "error", err)
	} else {
		slog.Info("Goal auto-paused at continuation budget", "session_id", g.SessionID, "goal_id", g.GoalID, "budget", budget)
	}

	if r.notify != nil {
		r.notify.Publish(pubsub.CreatedEvent, notify.Notification{
			SessionID: g.SessionID,
			Type:      notify.TypeGoalPaused,
			Message: fmt.Sprintf(
				"Goal reached the %d-continuation limit and was paused so it can't run unattended. Review the progress, then open the Commands menu (Ctrl+P) and choose \"Resume Goal\" to grant another %d continuations, or \"Clear Goal\" to finish.",
				budget, budget,
			),
		})
	}
	return false
}

func (r *Runtime) OnTurnFinished(ctx context.Context, sessionID string) {
	if r == nil {
		return
	}
	err := r.MaybeContinue(ctx, sessionID)
	if err != nil {
		slog.Error("Goal runtime continuation failed", "session_id", sessionID, "error", err)
	}
}

func (r *Runtime) MaybeContinue(ctx context.Context, sessionID string) error {
	if r == nil || r.store == nil || r.agent == nil {
		return nil
	}
	if r.agent.IsSessionBusy(sessionID) {
		return nil
	}

	if r.agent.QueuedPrompts(sessionID) > 0 {
		return nil
	}

	goal, err := r.store.Get(ctx, sessionID)
	if err != nil || goal == nil {
		return err
	}

	if goal.Status != GoalActive {
		return nil
	}

	// Enforce the continuation budget before starting another synthetic
	// turn. When exhausted this pauses the goal and asks the user what to
	// do instead of looping forever.
	if !r.consumeContinuation(ctx, goal) {
		return nil
	}

	prompt, err := r.RenderContinuationPrompt(goal)
	if err != nil {
		return fmt.Errorf("rendering continuation prompt: %w", err)
	}

	slog.Info("Starting synthetic continuation turn", "session_id", sessionID, "goal_id", goal.GoalID)

	if r.notify != nil {
		r.notify.Publish(pubsub.CreatedEvent, notify.Notification{
			SessionID: sessionID,
			Type:      notify.TypeGoalContinue,
		})
	}

	// Inject the current GoalID into the context for stale update protection.
	goalCtx := context.WithValue(ctx, GoalIDContextKey, goal.GoalID)

	_, err = r.agent.Run(goalCtx, sessionID, prompt)
	return err
}

func (r *Runtime) RenderContinuationPrompt(g *Goal) (string, error) {
	var buf bytes.Buffer
	if err := continuationTpl.Execute(&buf, g); err != nil {
		return "", err
	}
	return buf.String(), nil
}
