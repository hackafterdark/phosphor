package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hackafterdark/phosphor/pkg/otel"
	"github.com/hackafterdark/phosphor/pkg/permission"
	"go.opentelemetry.io/otel/attribute"
)

// AskMode is the user-facing dial: how much the memory gate interrupts. It is the
// memory analog of the tool-approval axis, but persisted, because the answer is a
// standing preference rather than a per-run flag.
type AskMode string

const (
	// AskModeAsk routes every mutation through the pending queue.
	AskModeAsk AskMode = "ask"
	// AskModeBalanced auto-commits only the safe subset: deterministic, reversible,
	// low blast radius.
	AskModeBalanced AskMode = "balanced"
	// AskModeAuto commits anything tombstoned and surfaces only hard overrides.
	AskModeAuto AskMode = "auto"
)

// confirmedTrust is the confidence score applied to an entry once the user
// confirms it. Confirming turns an inferred record into asserted content, so it
// lands at the same high-trust level the design reserves for human-stated truth
// (MEMORY_DESIGN.md §3.6) rather than the 0.5 default of an unreviewed entry.
const confirmedTrust = 0.8

// ValidAskMode reports whether s names one of the three dial positions.
func ValidAskMode(s string) bool {
	switch AskMode(strings.ToLower(s)) {
	case AskModeAsk, AskModeBalanced, AskModeAuto:
		return true
	}
	return false
}

// The closed menu of named categories the config may reference. Values off this
// list are a load-time error with a suggestion rather than a silent no-op, which
// is the whole fix for the typo footgun: a user who mistypes a protection must not
// be left believing they are protected.
var Categories = []string{
	"decisions", "requirements", "user-stated", "hot-set",
	"archive-adds", "chore-notes", "inferred", "policy",
	"other-session-working-state",
}

// SuggestCategory finds the closest known category for an unknown value so a
// validation error can say "did you mean".
func SuggestCategory(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	best, bestScore := "", 0.0
	for _, c := range Categories {
		score := TokenOverlap(value, c)
		if score > bestScore {
			best, bestScore = c, score
		}
	}
	if bestScore < 0.34 {
		return ""
	}
	return best
}

// Policy is the gate's resolved intent, from phosphor.json. Learned state is not
// here; it lives in the buckets table.
type Policy struct {
	AskMode               AskMode
	Adaptive              bool
	MinSamples            int
	Confirm               []string
	Ignore                []string
	Untunable             []string
	AllowNonPrimaryWrites bool
	MaxAsksPerTurn        int
	// RateLimitFloorPct defers anything that would spend model tokens when the
	// provider budget is nearly gone.
	RateLimitFloorPct float64
}

// DefaultPolicy is what ships: interrupt only when it is genuinely consequential.
func DefaultPolicy() Policy {
	return Policy{
		AskMode:    AskModeBalanced,
		Adaptive:   true,
		MinSamples: 5,
		// policy joins the floor because section 5 puts it on the hard-override list:
		// a standing instruction the agent wrote for itself is exactly the write that
		// may not be committed unattended, whatever the dial says.
		Untunable:      []string{"inferred", "hot-set", "user-stated", "decisions", "policy", "other-session-working-state"},
		MaxAsksPerTurn: 1,
	}
}

// WriteRequest is one gated write.
type WriteRequest struct {
	SessionID       string
	ToolCallID      string
	Primary         bool
	SkipBudgetCheck bool
	// LowBudget marks that the provider budget is at or under
	// Policy.RateLimitFloorPct, so the gate should not commit a fresh entry on the
	// agent's behalf even when its ask-mode would normally allow it — the write is
	// parked in the proposal queue instead, where it costs no model token to revisit.
	// It is the §8 rate-limit floor given effect on the write path; recall never
	// consults it because searches are cheap and always allowed.
	LowBudget bool
	Entry     Entry
}

// Outcome is what the gate decided, phrased so the agent can act on it and the UI
// can show the reason.
type Outcome struct {
	Op        Op      `json:"op"`
	Status    string  `json:"status"`
	ID        string  `json:"id"`
	Thread    string  `json:"thread"`
	Message   string  `json:"message"`
	Rationale string  `json:"rationale"`
	Proposal  int64   `json:"proposal,omitempty"`
	Related   []Entry `json:"related,omitempty"`
	Stats     Stats   `json:"stats"`
}

// Outcome.Status values. They are separate from the entry Status enum: "pending" as
// an outcome means "queued for your approval", while pending as an entry status
// means "an unconfirmed inference".
const (
	StatusCommitted    = "committed"
	StatusRefused      = "refused"
	StatusNoOp         = "no_op"
	StatusPendingWrite = "pending"
)

// Tool names, exported so the coordinator, the UI, and the permission allow-list all
// speak the same identifiers.
const (
	ToolName       = "memory"
	SearchToolName = "memory_search"
	ReadToolName   = "memory_read"
)

// Gate is the single write path into memory. Nothing else mutates the vault, which
// is what makes schema, sanitization, dedup, and index sync guarantees instead of
// hopes.
type Gate struct {
	store       *Store
	permissions permission.Service
	policy      Policy
}

// NewGate wires the gate to a store, the permission service used for the ask path,
// and the resolved policy.
func NewGate(store *Store, permissions permission.Service, policy Policy) *Gate {
	if policy.AskMode == "" {
		policy = DefaultPolicy()
	}
	return &Gate{store: store, permissions: permissions, policy: policy}
}

// Policy returns the resolved policy for the budget view and the tests.
func (g *Gate) Policy() Policy { return g.policy }

// Add is the governed ingest path: be aware of neighbours first, classify, then
// either commit or propose. Two entries being similar is never grounds to merge
// them; only a detected decision is grounds to change memory.
func (g *Gate) Add(ctx context.Context, req WriteRequest) (Outcome, error) {
	ctx, span := otel.StartSpan(ctx, "memory.gate.add")
	defer span.End()
	e := req.Entry
	span.SetAttributes(
		attribute.String("phosphor.memory.type", string(e.Type)),
		attribute.String("phosphor.memory.thread", e.Thread),
		attribute.Bool("phosphor.memory.primary", req.Primary),
		attribute.Bool("phosphor.memory.low_budget", req.LowBudget),
	)
	if strings.TrimSpace(e.Summary) == "" && strings.TrimSpace(e.Body) == "" {
		return Outcome{}, fmt.Errorf("memory entry needs a summary or a body")
	}
	// Guardrail: only the primary agent context may commit memories. A subagent's
	// throwaway goal or a cron system prompt must not be able to contaminate the
	// shared vault; reads stay open.
	if !req.Primary && !g.policy.AllowNonPrimaryWrites {
		return Outcome{Op: OpAdd, Status: StatusRefused, Message: "Non-primary agent context: memory writes are not allowed here. Recall still works."}, nil
	}
	if err := CheckContent(e.Summary + "\n" + e.Body + "\n" + strings.Join(e.Tags, "\n")); err != nil {
		return Outcome{Op: OpAdd, Status: StatusRefused, Message: err.Error()}, nil
	}
	// The provenance string is model-supplied on this path and renders verbatim
	// into the injected ⟨…⟩ badge, so it passes the same grammar gate as the
	// body rather than arriving unscreened. An unusable value refuses the write
	// with its own message rather than being silently dropped: the caller meant
	// to cite something.
	if src, err := ValidateSource(e.Source); err != nil {
		return Outcome{Op: OpAdd, Status: StatusRefused, Message: err.Error()}, nil
	} else {
		e.Source = src
	}

	// An inferred statement can only ever be a pending Tier B draft. This is the
	// invariant that kills "the agent filled a gap with a plausible assumption,
	// committed it, and is now confidently wrong forever".
	inferred := e.Inferred()
	if inferred {
		e.Status = StatusPending
	}
	if e.ID == "" {
		e.ID = NewID(e.Thread, e.Summary)
	}
	if e.Scope == "" {
		e.Scope = ScopeProject
	}

	// Phase 5 enrichment: an entry with no tags still gets keywords so the tag
	// graph can find it later. The deterministic stand-in answers in the default
	// build and the gated multilingual tokenizer under phosphor_prose. Tags the
	// model chose for itself are never overwritten: that assertion is the
	// writer"s, not the indexer"s.
	if len(e.Tags) == 0 && g.store.settings.ProseEnabled() {
		e.Tags = Keywords(candidateText(e), 6)
	}

	near, err := g.near(ctx, e)
	if err != nil {
		otel.RecordError(span, err)
		return Outcome{}, err
	}
	op := classify(e, near)
	span.SetAttributes(attribute.String("phosphor.memory.op", string(op)))

	bucket := BucketFor(op, e)
	decision, rationale := g.decide(op, e, bucket, near)
	if err := g.logAsk(ctx, req.SessionID, op, bucket, decision, rationale, e.ID); err != nil {
		otel.RecordError(span, err)
		return Outcome{}, err
	}

	// §8 rate-limit floor, given effect here rather than left decorative: once the
	// budget is at the floor, a fresh add is parked in the proposal queue instead of
	// auto-committing, because committing it would spend the one thing the floor
	// exists to protect. It is deliberately ahead of the ask branch so it holds at
	// every dial position, including auto; recall is unaffected because a search is
	// cheap and is meant to stay available even when the budget is not.
	if g.policy.RateLimitFloorPct > 0 && req.LowBudget {
		id, err := g.queueProposal(ctx, req, op, bucket, rationale, e)
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{
			Op: op, Status: StatusPendingWrite, ID: e.ID, Thread: e.Thread, Proposal: id,
			Rationale: rationale, Related: near,
			Message: fmt.Sprintf("Provider budget is near the memory rate-limit floor, so %q was queued as %q rather than committed for free. Approve it later with memory(op=confirm, id=%d).", e.Summary, e.ID, id),
		}, nil
	}

	if decision == "ask" {
		if granted, err := g.confirm(ctx, req, op, bucket, rationale, near); err != nil {
			return Outcome{}, err
		} else if !granted {
			id, err := g.queueProposal(ctx, req, op, bucket, rationale, e)
			if err != nil {
				return Outcome{}, err
			}
			return Outcome{
				Op: op, Status: StatusPendingWrite, ID: e.ID, Thread: e.Thread, Proposal: id,
				Rationale: rationale, Related: near,
				Message: fmt.Sprintf("Proposed %s for %q as %q and left it pending for your approval.", op, e.Summary, e.ID),
			}, nil
		}
	}

	out, err := g.commit(ctx, req, op, e, near, rationale)
	if err != nil {
		otel.RecordError(span, err)
		return out, err
	}
	span.SetAttributes(
		attribute.String("phosphor.memory.id", out.ID),
		attribute.String("phosphor.memory.status", string(out.Status)),
	)
	return out, nil
}

// commit performs the classified operation. Supersede and retire are tombstones:
// the prior bytes stay, so a wrong automatic call is always recoverable, which is
// what makes "it cannot affect later outcomes" true rather than aspirational.
func (g *Gate) commit(ctx context.Context, req WriteRequest, op Op, e Entry, near []Entry, rationale string) (Outcome, error) {
	var (
		message string
		stats   Stats
		err     error
	)
	switch op {
	case OpAdd, OpRefine, OpQualify:
		if err := g.store.Put(ctx, e); err != nil {
			return Outcome{}, err
		}
		message = fmt.Sprintf("Recorded %s %s (thread %s).", e.Type, e.ID, orDefault(e.Thread, "untitled"))
		if op == OpQualify && len(near) > 0 {
			// A scope-narrowing statement keeps its parent: both stay true under a
			// scope, so the broad rule is not retired by the narrow one.
			message += fmt.Sprintf(" Kept %s as the parent scope.", near[0].ID)
		}
	case OpSupersede:
		if len(near) == 0 {
			op = OpAdd
			if err := g.store.Put(ctx, e); err != nil {
				return Outcome{}, err
			}
			message = fmt.Sprintf("Recorded %s %s (no prior entry to supersede).", e.Type, e.ID)
			break
		}
		victim := near[0]
		e.Supersedes = victim.ID
		if err := g.store.Put(ctx, e); err != nil {
			return Outcome{}, err
		}
		if err := g.store.SetStatus(ctx, victim.Scope, victim.ID, StatusRetired, ""); err != nil {
			return Outcome{}, err
		}
		message = fmt.Sprintf("Recorded %s %s and retired the superseded %s.", e.Type, e.ID, victim.ID)
		if dependents, derr := g.store.Dependents(ctx, victim.ID); derr == nil && len(dependents) > 0 {
			ids := make([]string, 0, len(dependents))
			for _, d := range dependents {
				ids = append(ids, d.ID)
			}
			message += fmt.Sprintf(" %d entries were premised on it (%s) — review with memory_search.", len(dependents), strings.Join(ids, ", "))
		}
	case OpRetire:
		target := e.ID
		if target == "" && len(near) > 0 {
			target = near[0].ID
		}
		if target == "" {
			return Outcome{Op: op, Status: StatusNoOp, Message: "Nothing matched to retire."}, nil
		}
		scope := e.Scope
		if len(near) > 0 && near[0].ID == target {
			scope = near[0].Scope
		}
		if err := g.store.SetStatus(ctx, scope, target, StatusRetired, e.Supersedes); err != nil {
			return Outcome{}, err
		}
		message = fmt.Sprintf("Retired %s (kept as a tombstone for audit).", target)
		e.ID = target
	case OpNoOp:
		if len(near) > 0 {
			return Outcome{Op: op, Status: StatusNoOp, ID: near[0].ID, Thread: near[0].Thread, Related: near, Message: fmt.Sprintf("Already covered by %s; nothing written.", near[0].ID)}, nil
		}
		return Outcome{Op: op, Status: StatusNoOp, Message: "Nothing to write."}, nil
	default:
		return Outcome{Op: op, Status: StatusRefused, Message: fmt.Sprintf("Unsupported memory op %q for add; use pin, unpin, note, or retire.", op)}, nil
	}
	if stats, err = g.store.Report(ctx); err != nil {
		return Outcome{}, err
	}
	_ = g.store.RefreshLifecycle(ctx)
	return Outcome{Op: op, Status: StatusCommitted, ID: e.ID, Thread: e.Thread, Message: message, Rationale: rationale, Stats: stats, Related: near}, nil
}

// Apply runs an explicit operation the agent named (pin, unpin, note, retire,
// confirm) rather than one the gate inferred.
func (g *Gate) Apply(ctx context.Context, req WriteRequest, op Op) (Outcome, error) {
	ctx, span := otel.StartSpan(ctx, "memory.gate.apply")
	defer span.End()
	span.SetAttributes(
		attribute.String("phosphor.memory.op", string(op)),
		attribute.String("phosphor.memory.id", req.Entry.ID),
		attribute.Bool("phosphor.memory.primary", req.Primary),
	)
	if !req.Primary && !g.policy.AllowNonPrimaryWrites {
		return Outcome{Op: op, Status: StatusRefused, Message: "Non-primary agent context: memory writes are not allowed here."}, nil
	}
	if err := CheckContent(req.Entry.Body + "\n" + req.Entry.Summary); err != nil {
		return Outcome{Op: op, Status: StatusRefused, Message: err.Error()}, nil
	}
	if src, err := ValidateSource(req.Entry.Source); err != nil {
		return Outcome{Op: op, Status: StatusRefused, Message: err.Error()}, nil
	} else {
		req.Entry.Source = src
	}
	var (
		err     error
		message string
		id      = req.Entry.ID
		scope   = req.Entry.Scope
	)
	if id == "" {
		return Outcome{Op: op, Status: StatusRefused, Message: "This op needs an existing entry id."}, nil
	}
	if scope == "" {
		scope = ScopeProject
	}
	switch op {
	case OpPin:
		if err = g.store.SetPinned(ctx, scope, id, true); err != nil {
			return Outcome{}, err
		}
		message = fmt.Sprintf("Pinned %s into the always-injected window; it is now exempt from eviction.", id)
	case OpUnpin:
		if err = g.store.SetPinned(ctx, scope, id, false); err != nil {
			return Outcome{}, err
		}
		message = fmt.Sprintf("Unpinned %s; budget eviction can now claim its place.", id)
	case OpNote:
		if err = g.store.AddNote(ctx, scope, id, req.Entry.Body); err != nil {
			return Outcome{}, err
		}
		message = fmt.Sprintf("Noted against %s.", id)
	case OpRetire:
		if err = g.store.SetStatus(ctx, scope, id, StatusRetired, req.Entry.Supersedes); err != nil {
			return Outcome{}, err
		}
		message = fmt.Sprintf("Retired %s (tombstone kept).", id)
	case OpConfirm:
		if err = g.store.SetStatus(ctx, scope, id, StatusActive, ""); err != nil {
			return Outcome{}, err
		}
		if err = g.store.SetTrust(ctx, scope, id, confirmedTrust); err != nil {
			return Outcome{}, err
		}
		message = fmt.Sprintf("Confirmed %s; it is now asserted content and may be promoted.", id)
	case OpIgnore:
		if err = g.store.SetStatus(ctx, scope, id, StatusCold, ""); err != nil {
			return Outcome{}, err
		}
		message = fmt.Sprintf("Ignored %s; it stays searchable but will not be injected.", id)
	default:
		return Outcome{Op: op, Status: StatusRefused, Message: fmt.Sprintf("Unsupported memory op %q.", op)}, nil
	}
	if err := g.store.RefreshLifecycle(ctx); err != nil {
		return Outcome{}, err
	}
	stats, err := g.store.Report(ctx)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Op: op, Status: StatusCommitted, ID: id, Thread: req.Entry.Thread, Message: message, Stats: stats}, nil
}

// Record is the single entry point the tools use: it routes an explicitly named
// operation to the right governed path. Ops that act on an existing entry go to
// Apply; anything that introduces content goes through Add so it is classified
// before it is committed. A retire with no id is a contradiction claim, so it is left
// to the classifier to find the entry it contradicts.
func (g *Gate) Record(ctx context.Context, req WriteRequest, op Op) (Outcome, error) {
	switch op {
	case OpAdd, OpRefine, OpSupersede, OpQualify, OpMerge, OpNoOp, "":
		return g.Add(ctx, req)
	case OpRetire:
		if strings.TrimSpace(req.Entry.ID) == "" {
			return g.Add(ctx, req)
		}
		return g.Apply(ctx, req, op)
	case OpPin, OpUnpin, OpNote, OpConfirm, OpIgnore:
		return g.Apply(ctx, req, op)
	default:
		return Outcome{Op: op, Status: StatusRefused, Message: fmt.Sprintf("Unknown memory op %q.", op)}, nil
	}
}

// ---------------------------------------------------------------------------
// Classification
// ---------------------------------------------------------------------------

// near finds the neighbours a candidate must be reconciled against before it is
// written: FTS top-k plus the tag graph, scoped to the same scope when possible.
func (g *Gate) near(ctx context.Context, e Entry) ([]Entry, error) {
	query := strings.TrimSpace(e.Summary)
	if query == "" {
		query = e.Body
	}
	hits, err := g.store.Search(ctx, SearchQuery{
		Query: query,
		Tags:  e.Tags,
		Types: []Type{e.Type},
		Limit: 6,
	})
	if err != nil {
		return nil, err
	}
	var out []Entry
	seen := map[string]bool{}
	for _, h := range hits {
		if h.ID == e.ID || seen[h.ID] {
			continue
		}
		seen[h.ID] = true
		out = append(out, h.Entry)
	}
	// A same-thread neighbour with no textual overlap is still worth knowing about,
	// because contradiction inside one thread is the case that matters most.
	byThread, err := g.store.ByThread(ctx, e.Thread, 8)
	if err != nil {
		return out, nil
	}
	for _, h := range byThread {
		if h.ID == e.ID || seen[h.ID] {
			continue
		}
		if h.Type != e.Type {
			continue
		}
		seen[h.ID] = true
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := TokenOverlap(candidateText(e), out[i].Body+out[i].Summary), TokenOverlap(candidateText(e), out[j].Body+out[j].Summary)
		if si != sj {
			return si > sj
		}
		return out[i].Updated > out[j].Updated
	})
	return out, nil
}

func candidateText(e Entry) string {
	return e.Summary + " " + e.Body + " " + strings.Join(e.Tags, " ")
}

// nearThreshold is the similarity above which two entries are about the same
// subject. It is deliberately high: a false merge costs more nuance than a
// duplicate costs tokens.
const nearThreshold = 0.62

// classify decides what a write actually is. It is decision-driven, not
// similarity-driven: resemblance raises a question, a decision answers one.
func classify(e Entry, near []Entry) Op {
	if len(near) == 0 {
		return OpAdd
	}
	top := near[0]
	sim := TokenOverlap(candidateText(e), top.Body+" "+top.Summary)
	switch {
	case sim >= nearThreshold && sameSubject(e, top) && !contradicts(e, top):
		// Same subject, more nuance, no conflict: fold into the existing row.
		if len(strings.Fields(e.Body)) <= len(strings.Fields(top.Body)) && sim > 0.85 {
			return OpNoOp
		}
		return OpRefine
	case contradicts(e, top):
		// A clean value replacement retires the old row; a scoped or conditional
		// statement is a qualification and must never silently retire the parent.
		if isScoped(e) || isScoped(top) {
			return OpQualify
		}
		return OpSupersede
	case sim > 0.4 && sim < nearThreshold && sameSubject(e, top):
		return OpRefine
	default:
		return OpAdd
	}
}

func sameSubject(a, b Entry) bool {
	if a.Thread != "" && b.Thread != "" && a.Thread == b.Thread {
		return true
	}
	if a.Type != b.Type {
		return false
	}
	return TokenOverlap(candidateText(a), b.Body+" "+b.Summary) > 0.35
}

// contradicts reports a clean value clash: high similarity plus a negation or a
// replacement marker on one side. A qualified statement is not a contradiction.
func contradicts(a, b Entry) bool {
	if TokenOverlap(candidateText(a), b.Body+" "+b.Summary) < 0.4 {
		return false
	}
	ta, tb := strings.ToLower(a.Body+" "+a.Summary), strings.ToLower(b.Body+" "+b.Summary)
	negated := func(s string) bool {
		for _, w := range []string{" not ", "no longer", "instead of", "rather than", "ruled out", "retired", "deprecated", "switched to", "replaced by"} {
			if strings.Contains(s, w) {
				return true
			}
		}
		return false
	}
	return negated(ta) || negated(tb)
}

func isScoped(e Entry) bool {
	s := strings.ToLower(e.Body + " " + e.Summary)
	for _, w := range []string{"except", "unless", "only when", "when ", "if ", "but ", "however", "for backend", "for frontend", "scoped to"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------------

// Bucket is the key the adaptive layer learns per: op × type × scope. Learning is
// never hand-tuned by the user; they set the dial and this shapes around it.
type Bucket struct {
	Name     string  `db:"bucket" json:"bucket"`
	Ask      int64   `db:"ask" json:"ask"`
	AutoLean float64 `db:"auto_lean" json:"auto_lean"`
	Risk     float64 `db:"risk" json:"risk"`
	Samples  int64   `db:"samples" json:"samples"`
}

// BucketFor names the bucket an operation falls in.
func BucketFor(op Op, e Entry) string {
	scope := string(e.Scope)
	if scope == "" {
		scope = string(ScopeProject)
	}
	tier := "B"
	if e.Pinned || (e.Status == StatusActive && e.Type.IsDecisionLike()) {
		tier = "A"
	}
	return fmt.Sprintf("%s/%s/%s/%s", op, e.Type, tier, scope)
}

// decide resolves whether a write may be committed unattended. The cascade
// mirrors the tool-approval one so memory approvals and tool approvals are learned
// once together: hard override, then static policy, then learned state, then the
// dial breaking ties.
func (g *Gate) decide(op Op, e Entry, bucket string, near []Entry) (decision, rationale string) {
	hard, why, isHard := g.hardOverride(op, e, bucket)
	if isHard {
		return hard, why
	}
	if static, why, ok := g.staticPolicy(op, e, bucket); ok {
		return static, why
	}
	if g.policy.Adaptive {
		if b, ok, err := g.Bucket(context.Background(), bucket); err == nil && ok {
			if b.Samples >= int64(g.policy.MinSamples) {
				switch {
				case b.Ask == 1:
					return "ask", fmt.Sprintf("learned: this bucket (%s) has been corrected before (risk %.2f over %d decisions)", bucket, b.Risk, b.Samples)
				case b.Ask == 0 && !e.Type.IsDecisionLike():
					return "auto", fmt.Sprintf("learned: this bucket (%s) has been approved repeatedly (lean %.2f over %d decisions)", bucket, b.AutoLean, b.Samples)
				}
			}
		}
	}
	if safe(op, e, near) {
		switch g.policy.AskMode {
		case AskModeAuto:
			return "auto", "dial auto: reversible, deterministic, and not a hot-set write"
		case AskModeAsk:
			return "ask", "dial ask: every mutation is proposed first"
		default:
			return "auto", "balanced dial: reversible, deterministic, low blast radius"
		}
	}
	return "ask", "consequential change: needs your confirmation before it can be committed"
}

// safe is the auto-commit test: deterministic outcome, reversible by construction,
// bounded blast radius. A Tier A write carries a higher bar than a Tier B add,
// because a hot byte is read by every future prompt.
func safe(op Op, e Entry, near []Entry) bool {
	if e.Inferred() {
		return false
	}
	switch op {
	case OpAdd, OpRefine, OpNoOp:
		return !e.Pinned
	case OpSupersede, OpRetire:
		// Tombstones keep the prior bytes, so these stay reversible.
		return len(near) == 1 && !near[0].Pinned && !near[0].Type.IsDecisionLike()
	case OpQualify:
		// Collapsing scope is a judgment call; it is on the ask side always.
		return false
	default:
		return false
	}
}

func (g *Gate) hardOverride(op Op, e Entry, bucket string) (string, string, bool) {
	if containsFold(g.policy.Untunable, categoryFor(op, e, bucket)) {
		return "ask", fmt.Sprintf("untunable floor: %q may never be committed without asking", categoryFor(op, e, bucket)), true
	}
	if e.Inferred() && (e.Pinned || e.Status == StatusActive) {
		// An inference never enters the hot window unconfirmed, at any dial setting.
		return "ask", "an inferred statement cannot be promoted unconfirmed", true
	}
	return "", "", false
}

func (g *Gate) staticPolicy(op Op, e Entry, bucket string) (string, string, bool) {
	category := categoryFor(op, e, bucket)
	for _, c := range g.policy.Confirm {
		if strings.EqualFold(c, category) {
			return "ask", fmt.Sprintf("policy: you asked to confirm %q", c), true
		}
	}
	for _, c := range g.policy.Ignore {
		if strings.EqualFold(c, category) {
			return "auto", fmt.Sprintf("policy: you asked not to be asked about %q", c), true
		}
	}
	return "", "", false
}

func categoryFor(op Op, e Entry, bucket string) string {
	switch {
	case strings.Contains(bucket, "/A/"):
		return "hot-set"
	case e.Type == TypePolicy:
		return "policy"
	case e.Inferred():
		return "inferred"
	case op == OpAdd && e.Type.IsDecisionLike():
		return "decisions"
	case op == OpAdd:
		return "archive-adds"
	default:
		return "chore-notes"
	}
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Ask path: permission prompt first, pending queue second
// ---------------------------------------------------------------------------

// confirm fires the same approval surface tool approvals use, so there is one
// vocabulary for "may I" in the product rather than two.
func (g *Gate) confirm(ctx context.Context, req WriteRequest, op Op, bucket, rationale string, near []Entry) (bool, error) {
	if g.permissions == nil {
		return false, nil
	}
	if g.permissions.SkipRequests() {
		// An operator in yolo mode has already opted out of prompts; the dial still
		// governs what would have been proposed, so nothing is silently lost.
		return true, nil
	}
	if max := g.policy.MaxAsksPerTurn; max > 0 && g.asksThisTurn(ctx, req.SessionID) >= max {
		return false, nil
	}
	desc := fmt.Sprintf("Memory wants to %s: %s", op, orDefault(req.Entry.Summary, req.Entry.Body))
	if rationale != "" {
		desc += " — because " + rationale
	}
	granted, err := g.permissions.Request(ctx, permission.CreatePermissionRequest{
		SessionID:   req.SessionID,
		ToolCallID:  req.ToolCallID,
		ToolName:    ToolName,
		Description: desc,
		Action:      string(op),
		Params:      req.Entry,
		Path:        g.store.VaultDir(req.Entry.Scope),
	})
	if err != nil {
		return false, fmt.Errorf("memory confirmation failed: %w", err)
	}
	g.recordFeedback(ctx, bucket, granted)
	return granted, nil
}

// queueProposal parks a write as a deferred write awaiting a yes, which is the same
// machinery as the approval prompt with the answer postponed.
func (g *Gate) queueProposal(ctx context.Context, req WriteRequest, op Op, bucket, rationale string, e Entry) (int64, error) {
	payload, err := json.Marshal(e)
	if err != nil {
		return 0, fmt.Errorf("encode proposal: %w", err)
	}
	b := g.store.bankFor(e.Scope)
	var id int64
	rows, err := b.write.QueryContext(ctx,
		"INSERT INTO proposals(ts, session_id, op, bucket, rationale, payload) VALUES(?, ?, ?, ?, ?, ?) RETURNING id",
		g.store.now().UTC().Format("2006-01-02T15:04:05Z07:00"), req.SessionID, string(op), bucket, rationale, string(payload))
	if err != nil {
		return 0, fmt.Errorf("queue proposal: %w", err)
	}
	if rows.Next() {
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan proposal id: %w", err)
		}
	}
	rows.Close()
	return id, nil
}

// Pending lists the writes still awaiting a decision.
func (g *Gate) Pending(ctx context.Context) ([]Proposal, error) {
	var out []Proposal
	for _, b := range g.store.banks() {
		if b == nil {
			continue
		}
		rows, err := b.read.QueryContext(ctx, "SELECT id, ts, session_id, op, bucket, rationale, payload FROM proposals WHERE decided = 0 ORDER BY id")
		if err != nil {
			return nil, fmt.Errorf("list proposals: %w", err)
		}
		for rows.Next() {
			p, err := scanRow[Proposal](rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan proposal: %w", err)
			}
			out = append(out, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate proposals: %w", err)
		}
	}
	return out, nil
}

// Proposal is a deferred write.
type Proposal struct {
	ID        int64  `db:"id" json:"id"`
	TS        string `db:"ts" json:"ts"`
	SessionID string `db:"session_id" json:"session_id"`
	Op        string `db:"op" json:"op"`
	Bucket    string `db:"bucket" json:"bucket"`
	Rationale string `db:"rationale" json:"rationale"`
	Payload   string `db:"payload" json:"-"`
	Entry     Entry  `json:"entry"`
}

// Resolve answers a queued proposal: accept commits it, decline feeds the bucket's
// risk so the gate learns where the line is for this user and project.
func (g *Gate) Resolve(ctx context.Context, id int64, accept bool) (Outcome, error) {
	var (
		found   Proposal
		b       *bank
		decided bool
	)
	for _, candidate := range g.store.banks() {
		if candidate == nil {
			continue
		}
		rows, err := candidate.read.QueryContext(ctx, "SELECT id, ts, session_id, op, bucket, rationale, payload FROM proposals WHERE id = ? AND decided = 0", id)
		if err != nil {
			return Outcome{}, fmt.Errorf("load proposal: %w", err)
		}
		if rows.Next() {
			p, serr := scanRow[Proposal](rows)
			if serr != nil {
				rows.Close()
				return Outcome{}, fmt.Errorf("scan proposal: %w", serr)
			}
			found, b, decided = p, candidate, true
		}
		rows.Close()
		if decided {
			break
		}
	}
	if !decided {
		return Outcome{Status: StatusNoOp, Message: fmt.Sprintf("No pending proposal %d.", id)}, nil
	}
	if err := b.execWrite(ctx, "UPDATE proposals SET decided = 1 WHERE id = ?", id); err != nil {
		return Outcome{}, fmt.Errorf("settle proposal: %w", err)
	}
	g.recordFeedback(ctx, found.Bucket, accept)
	if !accept {
		return Outcome{Op: Op(found.Op), Status: StatusRefused, Message: fmt.Sprintf("Dropped proposed %s for %q.", found.Op, found.Rationale)}, nil
	}
	var e Entry
	if err := json.Unmarshal([]byte(found.Payload), &e); err != nil {
		return Outcome{}, fmt.Errorf("decode proposal payload: %w", err)
	}
	if e.Status == StatusPending {
		e.Status = StatusActive
	}
	near, err := g.near(ctx, e)
	if err != nil {
		return Outcome{}, err
	}
	return g.commit(ctx, WriteRequest{SessionID: found.SessionID, Primary: true, Entry: e}, Op(found.Op), e, near, found.Rationale)
}

func (g *Gate) recordFeedback(ctx context.Context, bucket string, approved bool) {
	if !g.policy.Adaptive || bucket == "" {
		return
	}
	b := g.store.bankFor(ScopeGlobal)
	lean, risk := 0.2, 0.0
	if !approved {
		lean, risk = 0.0, 0.25
	}
	if err := b.execWrite(ctx, `INSERT INTO buckets(bucket, auto_lean, risk, samples, ask) VALUES(?, ?, ?, 1, -1)
			ON CONFLICT(bucket) DO UPDATE SET
				auto_lean = MIN(1.0, buckets.auto_lean + ?),
				risk = MIN(1.0, buckets.risk + ?),
				samples = buckets.samples + 1`,
		bucket, lean, risk, lean, risk); err != nil {
		return
	}
	// A bucket that has been approved repeatedly with no corrections stops asking;
	// one that keeps being corrected starts asking. This is the "if the user had to
	// fix a fact" signal made principled.
	if err := b.execWrite(ctx, `UPDATE buckets SET ask = CASE
			WHEN risk > auto_lean THEN 1
			WHEN auto_lean > 0.5 AND risk < 0.25 THEN 0
			ELSE ask END WHERE bucket = ?`, bucket); err != nil {
		return
	}
}

// Bucket reads the learned state for one bucket.
func (g *Gate) Bucket(ctx context.Context, name string) (Bucket, bool, error) {
	b := g.store.bankFor(ScopeGlobal)
	rows, err := b.read.QueryContext(ctx, "SELECT bucket, ask, auto_lean, risk, samples FROM buckets WHERE bucket = ?", name)
	if err != nil {
		return Bucket{}, false, fmt.Errorf("read bucket: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return Bucket{}, false, nil
	}
	out, err := scanRow[Bucket](rows)
	if err != nil {
		return Bucket{}, false, fmt.Errorf("scan bucket: %w", err)
	}
	return out, true, nil
}

// logAsk is the explainability trail: every ask and every not-ask has to be able to
// answer "because <bucket>, <signal>, <rule>".
func (g *Gate) logAsk(ctx context.Context, sessionID string, op Op, bucket, decision, rationale, itemID string) error {
	b := g.store.bankFor(ScopeGlobal)
	if err := b.execWrite(ctx,
		"INSERT INTO asklog(ts, session_id, op, bucket, decision, rationale, item_id) VALUES(?, ?, ?, ?, ?, ?, ?)",
		g.store.now().UTC().Format("2006-01-02T15:04:05Z07:00"), sessionID, string(op), bucket, decision, rationale, itemID); err != nil {
		return fmt.Errorf("record ask decision: %w", err)
	}
	return nil
}

// AskLog is one explainability record.
type AskLog struct {
	ID        int64  `db:"id" json:"id"`
	TS        string `db:"ts" json:"ts"`
	SessionID string `db:"session_id" json:"session_id"`
	Op        string `db:"op" json:"op"`
	Bucket    string `db:"bucket" json:"bucket"`
	Decision  string `db:"decision" json:"decision"`
	Rationale string `db:"rationale" json:"rationale"`
	ItemID    string `db:"item_id" json:"item_id"`
}

// RecentAsks returns the tail of the decision trail for the review UI.
func (g *Gate) RecentAsks(ctx context.Context, limit int) ([]AskLog, error) {
	if limit <= 0 {
		limit = 20
	}
	b := g.store.bankFor(ScopeGlobal)
	rows, err := b.read.QueryContext(ctx, "SELECT id, ts, session_id, op, bucket, decision, rationale, item_id FROM asklog ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("read ask log: %w", err)
	}
	defer rows.Close()
	var out []AskLog
	for rows.Next() {
		a, err := scanRow[AskLog](rows)
		if err != nil {
			return nil, fmt.Errorf("scan ask log row: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ask log: %w", err)
	}
	return out, nil
}

// asksThisTurn throttles the nag: a fully cautious agent that interrupts on every
// merge is a parrot that cannot finish a task, which is a real failure mode.
func (g *Gate) asksThisTurn(ctx context.Context, sessionID string) int {
	v := ctx.Value(asksKey)
	n, ok := v.(int)
	if !ok || sessionID == "" {
		return 0
	}
	return n
}

type asksKeyType struct{}

var asksKey asksKeyType

// WithAskBudget stamps the per-turn ask allowance onto the context so the gate can
// throttle without holding session state itself.
func WithAskBudget(ctx context.Context, asks int) context.Context {
	return context.WithValue(ctx, asksKey, asks)
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
