package agent

import (
	"context"
	"log/slog"
	"strings"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/memory"
	"github.com/hackafterdark/phosphor/pkg/hooks"
	"github.com/hackafterdark/phosphor/pkg/message"
)

// finishTurn runs the post-turn seam once a run has completed successfully: the
// Stop hook event and, for one-shot sessions, the SessionEnd event. It is
// deliberately called after the transcript is persisted and never re-enters the
// model, because the whole point of the nudge is that it costs no inference.
//
// A note produced here (by a Stop hook's additional context or by the memory
// nudge) is queued rather than injected into the finished turn: it rides the next
// request inside its system block, never as a trailing system message in the
// message list, which strict providers reject. The agent that already recorded
// the decision is not reminded again, because the tool-call list says so.
func (a *sessionAgent) finishTurn(ctx context.Context, call SessionAgentCall, assistant *message.Message, result *fantasy.AgentResult) {
	if call.IsStateless {
		return
	}
	text := turnText(assistant, result)
	toolNames := turnToolNames(result)

	if a.stopRunner != nil && !a.isSubAgent {
		payload := hooks.BuildTurnPayload(hooks.EventStop, call.SessionID, a.workingDir, text, toolNames)
		agg, err := a.stopRunner.RunEvent(ctx, hooks.EventStop, call.SessionID, payload)
		if err != nil {
			slog.Warn("Stop hook failed; ignoring", "session", call.SessionID, "error", err)
		} else if agg.Context != "" {
			a.queueNote(call.SessionID, agg.Context)
		}
		if err == nil && agg.Halt {
			slog.Info("Stop hook requested a halt after the turn completed", "session", call.SessionID, "reason", agg.Reason)
		}
	}

	// A one-shot run is the session: nothing follows it, so this is the only
	// finalize signal that exists until the app grows an explicit session-end path.
	if call.NonInteractive && a.endRunner != nil {
		payload := hooks.BuildTurnPayload(hooks.EventSessionEnd, call.SessionID, a.workingDir, text, toolNames)
		if _, err := a.endRunner.RunEvent(ctx, hooks.EventSessionEnd, call.SessionID, payload); err != nil {
			slog.Warn("SessionEnd hook failed; ignoring", "session", call.SessionID, "error", err)
		}
	}

	if !a.memoryEnabled || a.isSubAgent {
		return
	}
	info := memory.TurnInfo{
		SessionID:   call.SessionID,
		Text:        text,
		Primary:     true,
		WroteMemory: memory.WroteMemory(toolNames),
		Suppressed:  memory.Suppressed(text),
	}
	if note, ok := memory.ShouldNudge(info); ok {
		a.queueNote(call.SessionID, note)
	}
}

// turnText returns the assistant text the turn ended on, falling back to the
// agent result response when the streaming message was not captured.
func turnText(assistant *message.Message, result *fantasy.AgentResult) string {
	if assistant != nil {
		if s := strings.TrimSpace(assistant.Content().String()); s != "" {
			return s
		}
	}
	if result != nil {
		return strings.TrimSpace(result.Response.Content.Text())
	}
	return ""
}

// turnToolNames collects the tool names called across every step of the run, so
// a hook or the nudge can see whether the bookkeeping call happened at all.
func turnToolNames(result *fantasy.AgentResult) []string {
	if result == nil {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	for _, step := range result.Steps {
		for _, tc := range step.Response.Content.ToolCalls() {
			if tc.ToolName == "" || seen[tc.ToolName] {
				continue
			}
			seen[tc.ToolName] = true
			names = append(names, tc.ToolName)
		}
	}
	return names
}

// queueNote stashes a one-line reminder for the start of the next turn. Repeated
// queues for the same session are joined so a hook note and a nudge both survive.
func (a *sessionAgent) queueNote(sessionID, note string) {
	note = strings.TrimSpace(note)
	if note == "" {
		return
	}
	if prev, ok := a.pendingNotes.Get(sessionID); ok && prev != "" {
		note = prev + "\n" + note
	}
	a.pendingNotes.Set(sessionID, note)
	slog.Debug("Queued post-turn note", "session", sessionID)
}

// takeNote removes and returns any queued reminder for the session.
func (a *sessionAgent) takeNote(sessionID string) string {
	note, ok := a.pendingNotes.Get(sessionID)
	if !ok {
		return ""
	}
	a.pendingNotes.Del(sessionID)
	return note
}
