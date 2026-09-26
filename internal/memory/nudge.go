package memory

import (
	"context"
	"regexp"
	"strings"
	"sync/atomic"
)

// The whole "the agent is the writer, so there is no extra inference on the normal
// path" premise has one failure mode: under multi-step reasoning load the model
// simply forgets the side-effect call to `memory`. It stays focused on answering and
// the bookkeeping call drops, which leaves the store near-empty. Discipline is not a
// strategy, so the reminder below exists as a cheap, off-able hint.
//
// Two things this deliberately is not: a second model-loop turn (that would
// re-introduce per-turn inference and first-answer latency, and the design rejects
// it), and a nag (a fully cautious agent that interrupts on every candidate becomes a
// parrot that cannot finish a task).

// NudgeText is the single reminder line injected when a decision-shaped turn produced
// no memory write. It is phrased as a demand for a decision or an explanation, so the
// agent can answer "it is transient" instead of being forced into a write.
const NudgeText = "A decision, constraint, or requirement was stated this turn and nothing was persisted. " +
	"Call memory(op=add) to record it, or say in one clause why it is transient. " +
	"Reply `//ignore-memory-this-turn` to silence this reminder for the rest of the turn."

// suppressToken is the agent's opt-out for the current turn.
const suppressToken = "//ignore-memory-this-turn"

// decisionPattern is the trigger list: a hint, not a classifier. Low precision is
// acceptable here because the cost of a false trigger is one ignorable line, and the
// nudge is skippable. It is the same list the tool description teaches, so the model
// is already predisposed to reach for the tool at these moments.
var decisionPattern = regexp.MustCompile(
	`(?i)(?:` +
		`\b(?:we|i)\s+(?:have\s+)?(?:decided|settled|went\s+with|are\s+going\s+with|will\s+use)\b` +
		`|\b(?:decided|let's\s+go\s+with|going\s+with|settled\s+on)\b` +
		`|\b(?:rule[d]??\s+out|no\s+longer\s+use|instead\s+of|rather\s+than|switch(?:ed)?\s+to)\b` +
		`|\b(?:the\s+)?(?:hard\s+)?(?:rule|constraint|requirement)\s+(?:is|that|for)\b` +
		`|\b(?:must|may\s+not|never|always)\s+(?:use|be|call|avoid|include)\b` +
		`|\b(?:remember|note|keep\s+in\s+mind)\s+that\b` +
		`|\b(?:phase|milestone|stage)\s+\d+\b` +
		`|\b(?:the\s+)?(?:plan|approach)\s+(?:is|now|remains)\b` +
		`)`,
)

// nudgeStats counts the two events that define the feature's primary health metric:
// how many eligible decision-turns were nudged, and how many then produced a write.
// Phase 1 is not "done" until this ratio is measured, because whether the agent
// actually reaches for the tool is empirical, not arguable.
type nudgeStats struct {
	eligible   atomic.Int64
	nudged     atomic.Int64
	writes     atomic.Int64
	suppressed atomic.Int64
}

var stats = &nudgeStats{}

// NudgeMetrics is the observed nudge-to-write funnel.
type NudgeMetrics struct {
	EligibleTurns int64   `json:"eligible_turns"`
	NudgedTurns   int64   `json:"nudged_turns"`
	WritesAfter   int64   `json:"writes_after_nudge"`
	Suppressed    int64   `json:"suppressed_turns"`
	Conversion    float64 `json:"conversion"`
}

// Metrics returns the funnel since process start.
func Metrics() NudgeMetrics {
	m := NudgeMetrics{
		EligibleTurns: stats.eligible.Load(),
		NudgedTurns:   stats.nudged.Load(),
		WritesAfter:   stats.writes.Load(),
		Suppressed:    stats.suppressed.Load(),
	}
	if m.EligibleTurns > 0 {
		m.Conversion = float64(m.WritesAfter) / float64(m.EligibleTurns)
	}
	return m
}

// ResetMetrics clears the counters; used by the eval harness between scenarios.
func ResetMetrics() {
	stats.eligible.Store(0)
	stats.nudged.Store(0)
	stats.writes.Store(0)
	stats.suppressed.Store(0)
}

// RecordWrite marks that a memory write happened this turn, which is how the nudge
// stays quiet once the agent has done the thing it was reminded of.
func RecordWrite() { stats.writes.Add(1) }

// ShouldNudge decides whether a completed turn deserves the reminder.
//
// It stays silent when: the caller is not the primary context (a subagent cannot
// write anyway, so a reminder there is a dead-end prompt), the agent already wrote
// memory this turn, the turn opted out, or the turn did not look decision-shaped.
func ShouldNudge(info TurnInfo) (string, bool) {
	if !info.Primary || info.WroteMemory || info.Suppressed {
		if info.Primary && !info.WroteMemory && info.Suppressed {
			stats.suppressed.Add(1)
		}
		return "", false
	}
	if !LooksDecisionShaped(info.Text) {
		return "", false
	}
	stats.eligible.Add(1)
	stats.nudged.Add(1)
	return NudgeText, true
}

// LooksDecisionShaped is the cheap keyword gate over a completed turn.
func LooksDecisionShaped(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	return decisionPattern.MatchString(text)
}

// Suppressed reports whether the turn asked to be left alone.
func Suppressed(text string) bool {
	return strings.Contains(strings.ToLower(text), suppressToken)
}

// WroteMemory reports whether a list of tool calls included a memory write, so the
// caller can decide not to remind about something that already happened.
func WroteMemory(toolNames []string) bool {
	for _, name := range toolNames {
		if strings.EqualFold(name, ToolName) {
			return true
		}
	}
	return false
}

type nudgeKey struct{}

var nudgeNoteKey nudgeKey

// WithNudge attaches a pending reminder to the context so a later stage of the turn
// can surface it without threading state through the call stack.
func WithNudge(ctx context.Context, note string) context.Context {
	return context.WithValue(ctx, nudgeNoteKey, note)
}

// NudgeFromContext returns any pending reminder for this turn.
func NudgeFromContext(ctx context.Context) string {
	v, ok := ctx.Value(nudgeNoteKey).(string)
	if !ok {
		return ""
	}
	return v
}
