package memory

import (
	"context"
	"strings"
	"unicode/utf8"
)

// The two Phase 6 paths are both written to ride the existing summarizer run and
// to honor §8's rate-limit floor, so the pieces they need are small and pure and
// do not reach for a model:
//
//   - a pre-compaction splice: the durable memory that would otherwise be summarized
//     away is re-stamped into the compaction summary so it survives window compaction.
//   - gated distillation: the summarizer, which is already running, is asked to emit
//     candidate Tier-B entries as an extra output field, and each is pushed through the
//     write-approval gate as a pending draft. Off by default; never auto-injects.
//
// Neither is a second model pass. The distillation piggybacks on the summary the
// summarizer already produces, so the only token it can spend is one the feature
// already agreed to pay — and even that is skipped once the budget is at the floor.

// Budget is the provider-budget snapshot the gate needs to decide whether a
// model-spending path may run and whether a write should defer to the proposal
// queue. It is assembled by the caller from the live session usage and the policy,
// and carried on the context so a tool invocation can read it without the store or
// gate ever holding a session handle.
type Budget struct {
	// Used is the tokens currently resident in the context window.
	Used int64
	// Window is the model's context-window size in tokens. A non-positive Window
	// means the budget is unknown, which is treated as "not low" so an unknown
	// never silently defers work it was not asked to defer.
	Window int64
	// FloorPct is memory.rate_limit_floor_pct: the remaining-budget percentage
	// under which model-spending memory work stops and writes defer to the queue.
	// A non-positive FloorPct disables the floor entirely.
	FloorPct float64
}

type budgetKey struct{}

var budgetValueKey budgetKey

// WithBudget carries a budget snapshot to whatever runs downstream of ctx — the
// memory tool, the gate call it makes — so the floor can be enforced at the write
// path without threading a session object through the tool layer.
func WithBudget(ctx context.Context, b Budget) context.Context {
	return context.WithValue(ctx, budgetValueKey, b)
}

// BudgetFromContext returns the budget stamped on ctx, if any.
func BudgetFromContext(ctx context.Context) (Budget, bool) {
	b, ok := ctx.Value(budgetValueKey).(Budget)
	return b, ok
}

// RemainingPct is the percent of the context window still free. An unknown window
// yields 100 so an unmeasured budget is never mistaken for an exhausted one.
func (b Budget) RemainingPct() float64 {
	if b.Window <= 0 {
		return 100
	}
	used := float64(b.Used)
	if used < 0 {
		used = 0
	}
	if used > float64(b.Window) {
		used = float64(b.Window)
	}
	return 100 * (float64(b.Window) - used) / float64(b.Window)
}

// BelowRateLimitFloor reports whether the provider budget has fallen under the
// floor at which memory must stop spending model tokens and defer writes to the
// queue rather than risk being the thing that trips a rate limit.
func (b Budget) BelowRateLimitFloor() bool {
	if b.FloorPct <= 0 || b.Window <= 0 {
		return false
	}
	return b.RemainingPct() < b.FloorPct
}

// ShouldDistill is the pure gate over the distillation path, mirroring ShouldNudge.
// It is the reason "off by default" is enforceable rather than aspirational: when
// the tri-state flag is off, no amount of remaining budget turns it on; and when
// the budget is at the floor, even an enabled distillation stands down. Searches are
// never routed through here — recall is cheap and stays allowed.
func ShouldDistill(enabled bool, b Budget) bool {
	if !enabled {
		return false
	}
	return !b.BelowRateLimitFloor()
}

// distillOpen and distillClose delimit the block the summarizer is asked to append
// when distillation is on. Wrapping the candidates in a marker the model can copy
// verbatim is what lets them ride the one summary call it is already making, so
// distillation is an extra output field rather than a second request.
const (
	distillOpen  = "<proposed_memories>"
	distillClose = "</proposed_memories>"
	// distillField separates type, summary and body within one candidate line.
	distillField = " :: "
)

// DistillationInstruction is the extra line appended to the summary prompt when
// distillation is on. It asks for the candidate block in a grammar the parser and
// the tests agree on, and is emphatic that these are drafts to be approved.
func DistillationInstruction() string {
	return "\n\nAdditionally, if the conversation captured a durable decision, " +
		"constraint, requirement, preference or fact worth remembering across sessions, " +
		"append exactly this block after your summary (omit it entirely if there is " +
		"nothing durable):\n" + distillOpen + "\n" +
		"type :: one-line summary :: optional fuller body\n" +
		"(type is one of decision|constraint|requirement|reference|preference|fact)\n" +
		distillClose
}

// Candidate is a distilled draft the summarizer surfaced. It is not an Entry yet —
// it becomes one only through the gate, which decides whether it is committed or
// parked for approval.
type Candidate struct {
	Type    Type
	Summary string
	Body    string
}

// ParseDistillates extracts the candidate block from a finished summary. It is
// deterministic and returns nothing when the model chose not to emit the block, so
// a summarization that produced no durable candidates behaves exactly like one that
// was never asked to. A leading field that names a type in the closed taxonomy is
// read as the type; a line that omits it is read as a bare summary at the default
// type, so a model that drops the field is not penalized for it. Up to max
// candidates are returned.
func ParseDistillates(summary string, max int) []Candidate {
	open := strings.LastIndex(summary, distillOpen)
	if open < 0 {
		return nil
	}
	rest := summary[open+len(distillOpen):]
	close := strings.Index(rest, distillClose)
	if close < 0 {
		return nil
	}
	block := rest[:close]
	if max <= 0 {
		max = 8
	}
	var out []Candidate
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "(") {
			continue
		}
		fields := strings.Split(line, distillField)
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		c := Candidate{Type: TypeFact}
		if len(fields) > 0 && ValidType(fields[0]) {
			c.Type = NormalType(fields[0])
			fields = fields[1:]
		}
		if len(fields) >= 1 {
			c.Summary = fields[0]
		}
		if len(fields) >= 2 {
			c.Body = strings.Join(fields[1:], distillField)
		}
		if c.Summary == "" {
			c.Summary = c.Body
		}
		if c.Summary == "" {
			continue
		}
		out = append(out, c)
		if len(out) >= max {
			break
		}
	}
	return out
}

// StripDistillates removes the candidate block from a summary so the block never
// lands in the stored transcript: it exists to be turned into pending drafts, not
// to be replayed to the model as summary prose forever after.
func StripDistillates(summary string) string {
	open := strings.Index(summary, distillOpen)
	if open < 0 {
		return summary
	}
	rest := summary[open:]
	closed := strings.Index(rest, distillClose)
	if closed < 0 {
		// A block that was opened but never closed is truncated from the opener on:
		// keeping half of a candidate protocol would replay a dangling fence as prose.
		return strings.TrimSpace(summary[:open])
	}
	end := open + closed + len(distillClose)
	return strings.TrimSpace(summary[:open] + summary[end:])
}

// SpliceIntoSummary appends the durable window to a compaction summary so the
// memories a session was built on survive the transcript being summarized away,
// and caps the block at max bytes so survival can not re-inflate the window the
// compaction was run to shrink. Trimming is rune-safe: cutting a multibyte
// sequence in half would hand the next session broken UTF-8 as "memory."
func SpliceIntoSummary(summary, block string, max int) string {
	if strings.TrimSpace(block) == "" {
		return summary
	}
	return strings.TrimSpace(summary) + "\n\n" + CapBlock(block, max)
}

// CapBlock trims s to at most max bytes without splitting a rune. An entry rendered
// by TierABlock is admitted whole, so a block that is already within budget passes
// through untouched; this is the defensive bound for a caller that handed a block
// built elsewhere.
func CapBlock(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	trimmed := s[:max]
	for !utf8.Valid([]byte(trimmed)) && len(trimmed) > 0 {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}
