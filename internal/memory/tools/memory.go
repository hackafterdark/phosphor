// Package tools exposes the agent-facing memory tools: the in-conversation writer,
// the retrieval tool, and the progressive-disclosure reader.
package tools

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"text/template"
	"time"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/memory"
	memoryui "github.com/hackafterdark/phosphor/internal/memory/ui"
	agenttools "github.com/hackafterdark/phosphor/pkg/agent/tools"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"github.com/hackafterdark/phosphor/pkg/permission"
	"go.opentelemetry.io/otel/attribute"
)

// touchedSources are the entries a committed write reported as nearby, in the shape the
// provenance card wants. Only rows the gate already loaded go in: the card is worth its
// bytes because every column in it is a column the index holds, and an entry the write
// path did not read would have to be rendered with a trust and a status invented for it.
func touchedSources(outcome memory.Outcome) []memoryui.Source {
	const cap = 4
	if len(outcome.Related) == 0 {
		return nil
	}
	why := outcome.Rationale
	if why == "" {
		why = "near-duplicate the gate surfaced against this write"
	}
	n := min(len(outcome.Related), cap)
	return memoryui.FromEntries(outcome.Related[:n], memoryui.RoleActed, why)
}

//go:embed memory.md
var memoryDescriptionTmpl []byte

// ToolName is the exported name of the writer tool.
const ToolName = memory.ToolName

// Params is the argument set of the memory writer.
type Params struct {
	Op           string   `json:"op" jsonschema:"description=What to do: add | refine | supersede | retire | pin | unpin | note | confirm | ignore"`
	ID           string   `json:"id,omitempty" jsonschema:"description=Target entry id for refine, supersede, retire, pin, unpin and note"`
	Type         string   `json:"type,omitempty" jsonschema:"description=decision | constraint | requirement | open_question | reference | plan | preference | fact | pattern | environment | task | policy"`
	Summary      string   `json:"summary,omitempty" jsonschema:"description=One-line headline shown in listings and search results"`
	Body         string   `json:"body,omitempty" jsonschema:"description=The full statement to remember"`
	Thread       string   `json:"thread,omitempty" jsonschema:"description=Named topic that groups related entries so a later session can continue it"`
	Tags         []string `json:"tags,omitempty" jsonschema:"description=Canonical keywords used for lateral recall across threads"`
	Source       string   `json:"source,omitempty" jsonschema:"description=Provenance: session#id@start:end or file#anchor"`
	Pinned       bool     `json:"pinned,omitempty" jsonschema:"description=Exclude from Tier-A eviction; use for hard constraints"`
	Asserted     *bool    `json:"asserted,omitempty" jsonschema:"description=true when the user stated it or it is verifiable; omitted means inferred, which lands as a pending draft"`
	Supersedes   string   `json:"supersedes,omitempty" jsonschema:"description=Id of the entry this one retires"`
	FromDecision string   `json:"from_decision,omitempty" jsonschema:"description=Id of the decision that authorizes this entry"`
	Scope        string   `json:"scope,omitempty" jsonschema:"description=project | global (default project). Only global crosses projects"`
}

// NewMemoryTool builds the writer. primary is false for subagents: only the primary
// agent context may commit memories, so a subagent's throwaway goal or a cron prompt
// cannot contaminate the shared vault.
func NewMemoryTool(cfg *config.ConfigStore, permissions permission.Service, workingDir string, primary bool, policy memory.Policy) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolName,
		renderMemoryDescription(),
		func(ctx context.Context, params Params, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool "+ToolName)
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", ToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)

			sessionID := agenttools.GetSessionFromContext(ctx)
			store, err := openStore(cfg, workingDir)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to open the memory vault: %v", err)), nil
			}
			defer store.Close()

			if _, err := store.SyncIfStale(ctx); err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to refresh the memory index: %v", err)), nil
			}

			op := memory.Op(strings.ToLower(strings.TrimSpace(params.Op)))
			if op == "" {
				op = memory.OpAdd
			}
			if op == memory.OpAdd && params.Type == "" {
				// A bare "remember this" is a fact; anything the model calls a decision
				// says so explicitly, which keeps the taxonomy honest.
				op = memory.OpAdd
			}

			entry := memory.Entry{
				ID:           strings.TrimSpace(params.ID),
				Type:         memory.NormalType(params.Type),
				Summary:      strings.TrimSpace(params.Summary),
				Body:         params.Body,
				Thread:       strings.TrimSpace(params.Thread),
				Tags:         params.Tags,
				Source:       strings.TrimSpace(params.Source),
				Pinned:       params.Pinned,
				Asserted:     params.Asserted,
				Supersedes:   strings.TrimSpace(params.Supersedes),
				FromDecision: strings.TrimSpace(params.FromDecision),
				Owner:        memory.OwnerAgent,
			}
			if params.Source == "" && sessionID != "" {
				entry.Source = fmt.Sprintf("session#%s", sessionID)
			}
			entry.Scope = memory.ScopeProject
			if strings.EqualFold(params.Scope, string(memory.ScopeGlobal)) {
				entry.Scope = memory.ScopeGlobal
			}

			gate := memory.NewGate(store, permissions, policy)
			budget, hasBudget := memory.BudgetFromContext(ctx)
			req := memory.WriteRequest{
				SessionID:  sessionID,
				ToolCallID: call.ID,
				Primary:    primary,
				LowBudget:  hasBudget && budget.BelowRateLimitFloor(),
				Entry:      entry,
			}

			outcome, err := gate.Record(ctx, req, op)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Memory write failed: %v", err)), nil
			}
			if outcome.Status == memory.StatusRefused {
				return fantasy.NewTextErrorResponse(outcome.Message), nil
			}
			if outcome.Status == memory.StatusPendingWrite {
				return fantasy.NewTextResponse(fmt.Sprintf("%s\n\nPending proposal id: %d\nDecide later with: memory(op=confirm, id=%d) or the /memory view.",
					outcome.Message, outcome.Proposal, outcome.Proposal)), nil
			}

			memory.RecordWrite()
			var sb strings.Builder
			sb.WriteString(outcome.Message)
			if outcome.Rationale != "" {
				fmt.Fprintf(&sb, "\nWhy: %s", outcome.Rationale)
			}
			fmt.Fprintf(&sb, "\nInjected window: %d/%d bytes. Corpus: %d active, %d pending, %d retired.",
				outcome.Stats.Injected, outcome.Stats.InjectLimit, outcome.Stats.Active, outcome.Stats.Pending, outcome.Stats.Retired)
			if outcome.Stats.Quarantined > 0 {
				fmt.Fprintf(&sb, " %d quarantined", outcome.Stats.Quarantined)
			}
			sb.WriteString(".")
			// The near-duplicates the gate surfaced ride as a provenance card rather than as a bare
			// id list: the agent reads it as "these are the entries you already have that are close
			// to that", and the TUI re-reads the same bytes later out of the stored result to draw
			// the turn's memory pill, so one rendering serves both.
			if touched := touchedSources(outcome); len(touched) > 0 {
				fmt.Fprintf(&sb, "\n%s\n%s", memoryui.Header("Touched ", len(touched)), memoryui.Rows(touched))
			}
			return fantasy.NewTextResponse(sb.String()), nil
		},
	)
}

// openStore opens the derived index over the project and global vaults. Like the
// workspace index it is opened per call rather than held by the coordinator: it is
// cheap, WAL-backed, and the rescan is content-hash gated.
func openStore(cfg *config.ConfigStore, workingDir string) (*memory.Store, error) {
	dir := workingDir
	if dir == "" && cfg != nil {
		dir = cfg.WorkingDir()
	}
	if dir != "" {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("workspace directory %q is not readable", dir)
		}
	}
	if err := memory.EnsureVault(dir); err != nil {
		return nil, err
	}
	return memory.Open(memory.OpenOptions{WorkspaceDir: dir, Settings: Settings(cfg), Shared: true})
}

// Settings resolves the store tuning from phosphor.json, falling back to the
// shipped defaults. It is exported so the app wiring opens the same index the tools
// do, with the same byte and age caps.
func Settings(cfg *config.ConfigStore) memory.Settings {
	s := memory.DefaultSettings()
	m := memoryConfig(cfg)
	if m == nil {
		return s
	}
	if m.MaxInjectBytes > 0 {
		s.MaxInjectBytes = memory.ClampInjectBytes(m.MaxInjectBytes)
	}
	if m.MaxUnusedDays > 0 {
		s.MaxUnusedDays = m.MaxUnusedDays
	}
	if m.PromoteThreshold > 0 {
		s.PromoteThreshold = m.PromoteThreshold
	}
	if m.AutoPromote != nil {
		s.AutoPromote = m.AutoPromote
	}
	if m.AutoPromoteMinTrust > 0 {
		s.AutoPromoteMinTrust = m.AutoPromoteMinTrust
	}
	if m.AutoPromoteSharePct > 0 {
		s.AutoPromoteSharePct = m.AutoPromoteSharePct
	}
	if m.WriteRetries > 0 {
		s.WriteRetries = m.WriteRetries
	}
	s.ThreadHintMaxLines = m.ThreadHintLinesOr(-1)
	if m.Prose != nil {
		s.EnableProse = m.Prose
	}
	if m.Integrity != nil {
		s.Integrity = m.Integrity
	}
	return s
}

func memoryConfig(cfg *config.ConfigStore) *config.Memory {
	if cfg == nil {
		return nil
	}
	c := cfg.Config()
	if c == nil {
		return nil
	}
	return c.Memory
}

// Enabled reports whether the memory tools should be offered to the model. The
// config owns the on-by-default tri-state and the "off"/"none" provider escape, so
// the resolver lives there; a nil block means the shipped default (on).
func Enabled(cfg *config.ConfigStore) bool {
	if cfg == nil {
		return true
	}
	c := cfg.Config()
	if c == nil {
		return true
	}
	return c.Memory.EnabledOrAuto()
}

// Policy resolves the write-gate policy from phosphor.json, falling back to the
// shipped defaults.
func Policy(cfg *config.ConfigStore) memory.Policy {
	return policyFrom(memoryConfig(cfg))
}

// PolicyFromConfig is Policy's twin for a caller holding a bare Config rather
// than the store it usually hangs off, which is how the TUI reaches the same
// gate policy the tools resolve. A nil config or memory block is the shipped
// default.
func PolicyFromConfig(c *config.Config) memory.Policy {
	if c == nil {
		return memory.DefaultPolicy()
	}
	return policyFrom(c.Memory)
}

func policyFrom(m *config.Memory) memory.Policy {
	p := memory.DefaultPolicy()
	if m == nil {
		return p
	}
	if m.Ask != "" {
		p.AskMode = memory.AskMode(strings.ToLower(m.Ask))
	}
	if m.Adaptive != nil {
		p.Adaptive = *m.Adaptive
	}
	if m.MinSamples > 0 {
		p.MinSamples = m.MinSamples
	}
	if m.Confirm != nil {
		p.Confirm = m.Confirm
	}
	if m.Ignore != nil {
		p.Ignore = m.Ignore
	}
	if m.Untunable != nil {
		p.Untunable = m.Untunable
	}
	if m.RateLimitFloorPct > 0 {
		p.RateLimitFloorPct = m.RateLimitFloorPct
	}
	p.AllowNonPrimaryWrites = m.AllowNonPrimaryWrites != nil && *m.AllowNonPrimaryWrites
	return p
}

func renderMemoryDescription() string {
	tmpl, err := template.New("memory").Parse(string(memoryDescriptionTmpl))
	if err != nil {
		return "Record durable decisions, constraints, requirements, references and preferences so later sessions can continue this work."
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, nil); err != nil {
		return string(memoryDescriptionTmpl)
	}
	return sb.String()
}

// systemBlockTimeout bounds the whole Tier-A render. Every step of it waits on the
// same single-connection index the vault watcher may be writing, and degrading to
// "no memory this turn" is far kinder than the prompt build stalling on a contended
// SQLite lock for the length of the busy timeout.
const systemBlockTimeout = 5 * time.Second

// SystemBlock renders the always-injected Tier-A window for the system prompt: the
// hot working set plus the optional names-only continuation hint, byte-capped and
// sanitized. It is a pure function of (vault, settings), which is what lets those
// bytes ride the provider prefix cache instead of being re-paid every turn. Any
// failure yields an empty block so a broken vault degrades to "no memory this
// session" rather than failing the prompt build.
func SystemBlock(ctx context.Context, cfg *config.ConfigStore, workingDir string) string {
	ctx, span := otel.StartSpan(ctx, "memory.system_block")
	defer span.End()
	if !Enabled(cfg) {
		return ""
	}
	store, err := openStore(cfg, workingDir)
	if err != nil {
		slog.Warn("Failed to open memory vault for the system block; continuing without it", "error", err)
		return ""
	}
	defer store.Close()

	renderCtx, cancelRender := context.WithTimeout(ctx, systemBlockTimeout)
	defer cancelRender()

	if _, err := store.SyncIfStale(renderCtx); err != nil {
		if renderCtx.Err() != nil {
			slog.Warn("Timed out refreshing the memory index; continuing without the memory block", "error", renderCtx.Err())
		} else {
			slog.Warn("Failed to refresh the memory index for the system block", "error", err)
		}
		return ""
	}
	if err := store.RefreshLifecycle(renderCtx); err != nil {
		slog.Warn("Failed to refresh memory lifecycle for the system block", "error", err)
	}
	block, err := store.TierABlock(renderCtx)
	if err != nil {
		slog.Warn("Failed to render the memory system block", "error", err)
		return ""
	}
	span.SetAttributes(attribute.Int("phosphor.memory.block_bytes", len(block)))
	return memory.Sanitize(block)
}

// DistillEnabled reports whether the off-by-default distillation path is on. It
// lives here rather than in the store so the agent asks the same resolver the
// config owns, and so "off unless explicitly set" stays a single decision.
func DistillEnabled(cfg *config.ConfigStore) bool {
	if cfg == nil {
		return false
	}
	c := cfg.Config()
	if c == nil {
		return false
	}
	return c.Memory.DistillEnabled()
}

// PreCompressBlock renders the durable window to splice into a compaction summary
// so it survives summarization, mirroring SystemBlock's degrade-don't-fail posture:
// any failure yields an empty string and the compaction simply carries no memory
// rather than stalling on a contended index. The block is capped so survival cannot
// re-inflate the window the compaction was run to shrink.
func PreCompressBlock(ctx context.Context, cfg *config.ConfigStore, workingDir string) string {
	ctx, span := otel.StartSpan(ctx, "memory.pre_compress_block")
	defer span.End()
	if !Enabled(cfg) {
		return ""
	}
	store, err := openStore(cfg, workingDir)
	if err != nil {
		slog.Warn("Failed to open memory vault for the compaction splice; continuing without it", "error", err)
		return ""
	}
	defer store.Close()

	renderCtx, cancelRender := context.WithTimeout(ctx, systemBlockTimeout)
	defer cancelRender()

	if _, err := store.SyncIfStale(renderCtx); err != nil {
		slog.Warn("Failed to refresh the memory index for the compaction splice", "error", err)
		return ""
	}
	if err := store.RefreshLifecycle(renderCtx); err != nil {
		slog.Warn("Failed to refresh memory lifecycle for the compaction splice", "error", err)
	}
	block, err := store.TierABlock(renderCtx)
	if err != nil {
		slog.Warn("Failed to render the memory block for the compaction splice", "error", err)
		return ""
	}
	span.SetAttributes(attribute.Int("phosphor.memory.block_bytes", len(block)))
	return memory.CapBlock(memory.Sanitize(block), Settings(cfg).MaxInjectBytes)
}

// ProposeDistillates pushes the candidate drafts the summarizer surfaced through the
// write-approval gate. It is the distillation write path and is emphatic about the
// two promises the feature is allowed to make: candidates land as pending drafts,
// never committed, because they were not confirmed by a human or the agent's own
// explicit write; and below the rate-limit floor they are dropped rather than
// queued, because queueing them would spend the very token budget the floor guards.
// It returns how many drafts were parked.
func ProposeDistillates(
	ctx context.Context,
	cfg *config.ConfigStore,
	permissions permission.Service,
	workingDir, sessionID string,
	cands []memory.Candidate,
	budget memory.Budget,
) int {
	_, span := otel.StartSpan(ctx, "memory.distill.propose")
	defer span.End()
	span.SetAttributes(
		attribute.Int("phosphor.memory.candidates", len(cands)),
		attribute.String("phosphor.memory.session", sessionID),
		attribute.Int64("phosphor.memory.budget_used", budget.Used),
		attribute.Float64("phosphor.memory.rate_floor_pct", budget.FloorPct),
	)
	if len(cands) == 0 || !Enabled(cfg) {
		return 0
	}
	if budget.BelowRateLimitFloor() {
		return 0
	}
	store, err := openStore(cfg, workingDir)
	if err != nil {
		slog.Warn("Failed to open memory vault to propose distillates; skipping distillation", "error", err)
		return 0
	}
	defer store.Close()
	if _, err := store.SyncIfStale(ctx); err != nil {
		slog.Warn("Failed to refresh the memory index to propose distillates; skipping distillation", "error", err)
		return 0
	}

	gate := memory.NewGate(store, permissions, Policy(cfg))
	low := budget.BelowRateLimitFloor()
	proposed := 0
	for _, c := range cands {
		entry := memory.Entry{
			Type:    c.Type,
			Summary: c.Summary,
			Body:    c.Body,
			Owner:   memory.OwnerAgent,
			Scope:   memory.ScopeProject,
		}
		if sessionID != "" {
			entry.Source = "session#" + sessionID + " (distilled)"
		}
		out, err := gate.Add(ctx, memory.WriteRequest{
			SessionID:       sessionID,
			Primary:         true,
			LowBudget:       low,
			SkipBudgetCheck: true,
			Entry:           entry,
		})
		if err != nil {
			slog.Warn("Failed to propose a distillate", "summary", c.Summary, "error", err)
			continue
		}
		if out.Status == memory.StatusPendingWrite || out.Status == memory.StatusCommitted {
			proposed++
		}
	}
	return proposed
}
