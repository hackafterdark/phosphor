package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/message"
)

// Provider is the swappable boundary for memory, the same lifecycle seam Hermes'
// MemoryProvider, Codex's MemoriesBackend, and oh-my-pi's MemoryBackend each
// converged on independently. Naming it now is what makes a second backend a config
// choice later instead of a refactor: the tool, the gate, and the UI only ever talk
// to this.
//
// The first-party vault in this package is implementation #1. A third-party store is
// either another in-process implementation selected by `memory.provider` or an MCP
// server surfacing the same three tools; neither would require touching the tools or
// the gate again.
type Provider interface {
	// Name identifies the implementation for logs and the /memory view.
	Name() string
	// Available reports whether the backend can serve right now. A false here makes
	// the agent behave exactly as if memory were switched off.
	Available(ctx context.Context) bool
	// Init prepares per-session state. It must never panic a turn.
	Init(ctx context.Context, sessionID string) error
	// SystemPromptBlock renders the always-injected Tier A block, byte-capped.
	SystemPromptBlock(ctx context.Context) string
	// Recall answers a retrieval request.
	Recall(ctx context.Context, q SearchQuery) ([]Hit, error)
	// Record runs the classify-plus-gate write pipeline.
	Record(ctx context.Context, req WriteRequest, op Op) (Outcome, error)
	// Lifecycle runs the promote/demote/retire pass.
	Lifecycle(ctx context.Context) error
	// OnTurnStart is called before the model is consulted for a turn.
	OnTurnStart(ctx context.Context, info TurnInfo) error
	// OnSessionEnd is called once a session's run is complete; it may propose pending
	// distillates but must never inject anything by itself.
	OnSessionEnd(ctx context.Context, sessionID string, msgs []message.Message) error
	// OnPreCompress returns text to splice into a compaction summary so durable
	// memories survive summarization instead of being summarized away.
	OnPreCompress(ctx context.Context, sessionID string, msgs []message.Message) string
	// Tools returns the agent-facing tools this backend contributes.
	Tools(ctx context.Context) []fantasy.AgentTool
	// Close releases resources.
	Close() error
}

// TurnInfo is the per-turn context the provider is told about: whose turn it is and
// whether that context may write.
type TurnInfo struct {
	SessionID   string
	Text        string
	Primary     bool
	WroteMemory bool
	Suppressed  bool
}

// builtin is the first-party markdown-backed provider.
type builtin struct {
	store  *Store
	gate   *Gate
	policy Policy
	ready  bool
	cause  error
}

// NewBuiltin wraps a store and its gate as a Provider.
func NewBuiltin(store *Store, gate *Gate) Provider {
	return &builtin{store: store, gate: gate, policy: gate.Policy(), ready: true}
}

func (b *builtin) Name() string { return "builtin" }

func (b *builtin) Available(ctx context.Context) bool {
	return b != nil && b.ready && b.store != nil
}

func (b *builtin) Init(ctx context.Context, sessionID string) error {
	if b == nil || !b.ready {
		return nil
	}
	if _, err := b.store.SnapshotFor(ctx, sessionID); err != nil {
		// A broken index degrades to "no memory this session", never to a failed turn.
		slog.Warn("Memory unavailable for session; continuing without it", "session", sessionID, "error", err)
		return nil
	}
	return nil
}

func (b *builtin) SystemPromptBlock(ctx context.Context) string {
	if !b.Available(ctx) {
		return ""
	}
	block, err := b.store.TierABlock(ctx)
	if err != nil {
		slog.Warn("Failed to render memory block", "error", err)
		return ""
	}
	return Sanitize(block)
}

func (b *builtin) Recall(ctx context.Context, q SearchQuery) ([]Hit, error) {
	if !b.Available(ctx) {
		return nil, nil
	}
	if _, err := b.store.SyncIfStale(ctx); err != nil {
		return nil, err
	}
	if err := b.store.RefreshLifecycle(ctx); err != nil {
		return nil, err
	}
	hits, err := b.store.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	// Nothing without provenance: a row that cannot cite its origin is not returned,
	// which turns context bleed from a bug to guard against into "the system declined
	// to load unlabeled context".
	out := hits[:0]
	for _, h := range hits {
		if h.Status == StatusPending && !h.Pinned {
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

func (b *builtin) Record(ctx context.Context, req WriteRequest, op Op) (Outcome, error) {
	if !b.Available(ctx) {
		return Outcome{Status: StatusRefused, Message: "Memory backend is off."}, nil
	}
	if op == OpAdd || op == "" {
		return b.gate.Add(ctx, req)
	}
	return b.gate.Apply(ctx, req, op)
}

func (b *builtin) Lifecycle(ctx context.Context) error {
	if !b.Available(ctx) {
		return nil
	}
	return b.store.RefreshLifecycle(ctx)
}

func (b *builtin) OnTurnStart(ctx context.Context, info TurnInfo) error { return nil }

func (b *builtin) OnSessionEnd(ctx context.Context, sessionID string, msgs []message.Message) error {
	return nil
}

// OnPreCompress splices the pinned window into whatever becomes the compaction
// summary, so a decision that mattered survives the summarizer rewriting the
// transcript. The result is re-capped so survival cannot re-inflate the window it
// was produced to shrink.
func (b *builtin) OnPreCompress(ctx context.Context, sessionID string, msgs []message.Message) string {
	if !b.Available(ctx) {
		return ""
	}
	block, err := b.store.TierABlock(ctx)
	if err != nil || strings.TrimSpace(block) == "" {
		return ""
	}
	return CapBlock(block, b.store.Settings().MaxInjectBytes)
}

func (b *builtin) Tools(ctx context.Context) []fantasy.AgentTool { return nil }

func (b *builtin) Close() error {
	if b == nil || b.store == nil {
		return nil
	}
	return b.store.Close()
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// Factory builds a provider by name. One active provider at a time, and a load
// failure falls back to off rather than panicking the agent loop: a misconfigured
// memory backend must not be able to break a turn.
type Factory func(ctx context.Context, opts ProviderOptions) (Provider, error)

// ProviderOptions is everything a factory may need to stand a backend up.
type ProviderOptions struct {
	WorkspaceDir string
	SessionID    string
	Settings     Settings
	Policy       Policy
	Permissions  PermissionRequester
	Provider     string
	EnableWrites bool
}

// PermissionRequester is the narrow slice of the permission service a backend needs,
// kept as an interface so an out-of-tree backend can be tested without the whole
// approval stack.
type PermissionRequester interface {
	Request(ctx context.Context, opts PermissionRequestSpec) (bool, error)
	SkipRequests() bool
}

// PermissionRequestSpec mirrors permission.CreatePermissionRequest without importing
// it here, so third-party backends stay decoupled from the approval stack.
type PermissionRequestSpec struct {
	SessionID   string
	ToolCallID  string
	ToolName    string
	Description string
	Action      string
	Path        string
	Params      any
}

var registry = struct {
	sync.RWMutex
	factories map[string]Factory
}{factories: map[string]Factory{}}

// Register makes a backend selectable by name.
func Register(name string, factory Factory) {
	registry.Lock()
	defer registry.Unlock()
	registry.factories[strings.ToLower(name)] = factory
}

// Providers lists the registered backend names.
func Providers() []string {
	registry.RLock()
	defer registry.RUnlock()
	out := make([]string, 0, len(registry.factories))
	for name := range registry.factories {
		out = append(out, name)
	}
	return out
}

// OpenProvider resolves the configured backend. "off" and any unknown or failing
// name resolve to a null provider, so the rest of the agent never branches on
// memory being broken.
func OpenProvider(ctx context.Context, opts ProviderOptions) Provider {
	if opts.Provider == "" || strings.EqualFold(opts.Provider, "off") || strings.EqualFold(opts.Provider, "none") {
		return nullProvider{}
	}
	registry.RLock()
	factory, ok := registry.factories[strings.ToLower(opts.Provider)]
	registry.RUnlock()
	if !ok {
		slog.Warn("Unknown memory provider configured; running with memory off",
			"provider", opts.Provider, "known", strings.Join(Providers(), ","))
		return nullProvider{}
	}
	p, err := factory(ctx, opts)
	if err != nil {
		slog.Warn("Memory provider failed to start; running with memory off", "provider", opts.Provider, "error", err)
		return nullProvider{}
	}
	if p == nil || !p.Available(ctx) {
		slog.Warn("Memory provider reported unavailable; running with memory off", "provider", opts.Provider)
		return nullProvider{}
	}
	return p
}

// nullProvider is what "off" and every failure resolve to. It satisfies the seam so
// no caller has to nil-check.
type nullProvider struct{}

func (nullProvider) Name() string                                       { return "off" }
func (nullProvider) Available(context.Context) bool                     { return false }
func (nullProvider) Init(context.Context, string) error                 { return nil }
func (nullProvider) SystemPromptBlock(context.Context) string           { return "" }
func (nullProvider) Recall(context.Context, SearchQuery) ([]Hit, error) { return nil, nil }
func (p nullProvider) Record(context.Context, WriteRequest, Op) (Outcome, error) {
	return Outcome{Status: StatusRefused, Message: "Memory is switched off."}, nil
}
func (nullProvider) Lifecycle(context.Context) error                               { return nil }
func (nullProvider) OnTurnStart(context.Context, TurnInfo) error                   { return nil }
func (nullProvider) OnSessionEnd(context.Context, string, []message.Message) error { return nil }
func (nullProvider) OnPreCompress(context.Context, string, []message.Message) string {
	return ""
}
func (nullProvider) Tools(context.Context) []fantasy.AgentTool { return nil }
func (nullProvider) Close() error                              { return nil }

// Default opens the first-party backend, which is the only one that ships.
func Default(ctx context.Context, opts ProviderOptions) (Provider, error) {
	settings := opts.Settings
	if settings.MaxInjectBytes <= 0 {
		settings = DefaultSettings()
	}
	store, err := Open(OpenOptions{WorkspaceDir: opts.WorkspaceDir, Settings: settings})
	if err != nil {
		return nil, fmt.Errorf("open memory vault: %w", err)
	}
	gate := NewGate(store, nil, opts.Policy)
	return NewBuiltin(store, gate), nil
}

func init() {
	Register("builtin", Default)
}
