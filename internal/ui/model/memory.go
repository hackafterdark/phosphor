package model

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/hackafterdark/phosphor/internal/memory"
	memorytools "github.com/hackafterdark/phosphor/internal/memory/tools"
	memoryui "github.com/hackafterdark/phosphor/internal/memory/ui"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/dialog"
	"github.com/hackafterdark/phosphor/internal/ui/util"
	"github.com/hackafterdark/phosphor/internal/workspace"
)

// memoryCommandTimeout bounds every store read a slash command performs. The vault is
// local and the index is SQLite, so a command that takes seconds is a command that hit
// a contended lock; reporting slow beats making the TUI wait on it.
const memoryCommandTimeout = 8 * time.Second

// memoryFallbackState memoizes the on-demand shared store the TUI opens when the app's
// startup index is absent. The zero value is ready to use; mu guards the lazy open
// because the draw loop and command tea.Cmd closures reach it from different goroutines.
type memoryFallbackState struct {
	mu     sync.Mutex
	store  *memory.Store
	gaveUp bool
}

// handleMemorySlashCommand handles the "/memory" family: status, sources, fsck, budget
// and policy. Each subcommand reads or writes through the same store the agent's tools
// use, so what the user sees here and what the agent sees in a tool result cannot be two
// different truths about one vault.
func (m *UI) handleMemorySlashCommand(args []string) tea.Cmd {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	rest := args[min(len(args), 1):]
	switch sub {
	case "":
		return m.memoryStatusCommand()
	case "sources":
		return m.memorySourcesCommand(rest)
	case "fsck":
		return m.memoryFsckCommand()
	case "budget":
		return m.memoryBudgetCommand()
	case "policy":
		return m.memoryPolicyCommand(rest)
	case "pin", "unpin", "demote", "dispute", "retire", "cite":
		return m.memoryManageCommand(sub, rest)
	case "key":
		return m.memoryKeyCommand(rest)
	case "review":
		return m.memoryReviewCommand(rest)
	default:
		return util.ReportWarn(fmt.Sprintf(
			"Unknown /memory subcommand %q. Use review, sources, fsck, budget, policy, key, pin, unpin, demote, dispute, retire, or cite.", sub))
	}
}

// memoryStore resolves the app-owned index through the same concrete-type assertion the
// symbol index uses. When the app's long-lived index is absent it falls back to opening
// one on demand, exactly as the memory tools do per call, so a single failed startup open
// (the vault not yet created, a momentary WAL lock) cannot leave the whole TUI memory
// surface reporting "off" for the rest of the session while the tools still work. A nil
// return now means only that memory is genuinely off or this is a remote workspace with
// no local vault; each caller reports that in its own words rather than pretending an
// empty corpus.
func (m *UI) memoryStore() *memory.Store {
	aw, ok := m.com.Workspace.(*workspace.AppWorkspace)
	if !ok {
		return nil
	}
	if app := aw.App(); app != nil && app.MemoryIndex != nil {
		return app.MemoryIndex
	}
	if !memorytools.Enabled(aw.Store()) {
		return nil
	}
	return m.openMemoryFallback(aw)
}

// openMemoryFallback opens the shared derived index once and memoizes it. The open is
// guarded by a mutex because the draw loop and the tea.Cmd closures a command runs on
// reach it concurrently, and a failed open is latched so a broken vault is not reopened
// on every draw tick. The handle is registered on the app's cleanup so it is closed at
// shutdown rather than leaked for the life of the process.
func (m *UI) openMemoryFallback(aw *workspace.AppWorkspace) *memory.Store {
	m.memFallback.mu.Lock()
	defer m.memFallback.mu.Unlock()
	if m.memFallback.store != nil {
		return m.memFallback.store
	}
	if m.memFallback.gaveUp {
		return nil
	}
	dir := aw.WorkingDir()
	if err := memory.EnsureVault(dir); err != nil {
		m.memFallback.gaveUp = true
		return nil
	}
	store, err := memory.Open(memory.OpenOptions{
		WorkspaceDir: dir,
		Settings:     memorytools.Settings(aw.Store()),
		Shared:       true,
	})
	if err != nil {
		m.memFallback.gaveUp = true
		return nil
	}
	m.memFallback.store = store
	return store
}

// resetMemoryFallback clears the memoized fallback handle and the failed-open latch so
// the next memoryStore() call reopens against the current on-disk state. A key restore
// or rotate is exactly that moment: the seal's authority changed underneath a handle that
// either failed closed on a missing key or holds a now-superseded one, and the operator
// should get recall back without a process restart.
func (m *UI) resetMemoryFallback() {
	m.memFallback.mu.Lock()
	defer m.memFallback.mu.Unlock()
	if m.memFallback.store != nil {
		_ = m.memFallback.store.Close()
		m.memFallback.store = nil
	}
	m.memFallback.gaveUp = false
}

func (m *UI) openMemoryMaintenanceStore(aw *workspace.AppWorkspace) (*memory.Store, bool) {
	if !memorytools.Enabled(aw.Store()) {
		return nil, false
	}
	dir := aw.WorkingDir()
	if err := memory.EnsureVault(dir); err != nil {
		return nil, false
	}
	store, err := memory.Open(memory.OpenOptions{
		WorkspaceDir: dir,
		Settings:     memorytools.Settings(aw.Store()),
		Shared:       false,
		Maintenance:  true,
	})
	if err != nil {
		return nil, false
	}
	return store, true
}

// memorySidebarRefresh is how often the sidebar panel re-reads the store. The draw
// loop calls memoryInfo every frame it draws the sidebar, and re-running the corpus
// tally, the seal tally, and the transcript scan each time would make the cheapest
// provenance surface in the app the most expensive one.
const memorySidebarRefresh = 2 * time.Second

// memoryPanelSnapshot is the throttled cache behind the sidebar panel: both store
// tallies plus when they were read. A failed read is cached as failed so a contended
// vault is retried on the next tick rather than on every frame.
type memoryPanelSnapshot struct {
	at      time.Time
	stats   memory.Stats
	statsOK bool
	integ   memory.IntegrityStatus
	integOK bool
}

// memoryPanelStats returns the corpus and seal tallies, refreshed at most once per
// memorySidebarRefresh.
func (m *UI) memoryPanelStats(ctx context.Context, store *memory.Store) memoryPanelSnapshot {
	if time.Since(m.memoryPanel.at) < memorySidebarRefresh {
		return m.memoryPanel
	}
	snap := memoryPanelSnapshot{at: time.Now()}
	if stats, err := store.Report(ctx); err == nil {
		snap.stats, snap.statsOK = stats, true
	}
	if integ, err := store.ReportIntegrity(ctx); err == nil {
		snap.integ, snap.integOK = integ, true
	}
	m.memoryPanel = snap
	return snap
}

// memoryInfo renders the sidebar's Memory panel as a glanceable stat block: how full
// the always-injected window is, the shape of the corpus, how many memories this
// session leaned on, and a seal warning only when integrity is unhealthy. Which
// memories those are stays inspection rather than glance — the summaries and
// citations live in /memory sources and out of the TUI, not in a column this narrow.
// It returns empty when memory is off, which is what keeps the panel out of the
// sidebar without needing a second visibility setting.
func (m *UI) memoryInfo(width int) string {
	store := m.memoryStore()
	if store == nil {
		return ""
	}
	t := m.com.Styles
	ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
	defer cancel()

	snap := m.memoryPanelStats(ctx, store)
	recalled := len(m.sessionMemorySources(ctx))

	var parts []string
	parts = append(parts, common.Section(t, "Memory", width), "")
	// The stat lines carry the panel's substance, so they take the same one-step
	// brighter token the workspace search widget prints its counts with;
	// AdditionalText is the dim voice reserved for "…and N more" trailing notes.
	if snap.statsOK {
		line := "Inject " + formatInjectPair(snap.stats.Injected, snap.stats.InjectLimit)
		if bar := injectBar(snap.stats.Injected, snap.stats.InjectLimit, min(10, max(4, width-14))); bar != "" {
			line += " " + bar
		}
		parts = append(parts, t.ModelInfo.Provider.Render(line))
		parts = append(parts, t.ModelInfo.Provider.Render(fmt.Sprintf(
			"%d active · %d pending · %d recalled", snap.stats.Active, snap.stats.Pending, recalled)))
	} else if recalled > 0 {
		parts = append(parts, t.ModelInfo.Provider.Render(fmt.Sprintf("%d recalled", recalled)))
	}
	// The seal speaks only when it has something to warn about: a permanent "sealed"
	// badge is decoration, and an amber unsigned count or red quarantine is the part
	// a user can act on.
	if snap.integOK && snap.integ.Enabled {
		if !snap.integ.KeyPresent {
			parts = append(parts, t.LSP.ErrorDiagnostic.Render("integrity key missing"))
		} else if snap.integ.KeyJustMinted {
			parts = append(parts, t.LSP.WarningDiagnostic.Render("\u26a0 back up the key: /memory key show"))
		} else if snap.integ.Unsigned > 0 {
			parts = append(parts, t.LSP.WarningDiagnostic.Render(fmt.Sprintf("\u26a0 %d unsigned", snap.integ.Unsigned)))
		}
		if snap.integ.Quarantined > 0 {
			parts = append(parts, t.LSP.ErrorDiagnostic.Render(fmt.Sprintf("quarantined %d", snap.integ.Quarantined)))
		}
		if snap.integ.DroppedVerify > 0 {
			parts = append(parts, t.LSP.ErrorDiagnostic.Render(fmt.Sprintf("%d dropped at seal check", snap.integ.DroppedVerify)))
		}
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(parts, "\n"))
}

// formatInjectPair renders used and limit on one shared unit so the budget reads as
// one measure — "3.1/8 KB", not "3.1 KB/8192 B". A nonpositive limit means the
// window is unbounded, and then only the used side is worth printing.
func formatInjectPair(used, limit int) string {
	const (
		kb = 1024
		mb = kb * 1024
	)
	unit, scale := " B", 1.0
	switch {
	case used >= mb || limit >= mb:
		unit, scale = " MB", mb
	case used >= kb || limit >= kb:
		unit, scale = " KB", kb
	}
	trim := func(v int) string {
		s := strconv.FormatFloat(float64(v)/scale, 'f', 1, 64)
		return strings.TrimSuffix(s, ".0")
	}
	if limit <= 0 {
		return trim(used) + unit
	}
	return trim(used) + "/" + trim(limit) + unit
}

// injectBar is the fill meter beside the injection budget, drawn with the block
// pieces the filepicker already uses for its histogram. The rounding is integer
// arithmetic so the draw path stays free of float drift, and a zero or negative
// limit renders no bar at all rather than an empty one that reads as full.
func injectBar(used, limit, width int) string {
	if limit <= 0 || width <= 0 {
		return ""
	}
	filled := (min(max(used, 0), limit)*2*width + limit) / (2 * limit)
	return strings.Repeat("▓", filled) + strings.Repeat("░", width-filled)
}

// sessionMemorySources refreshes the cached list of what the current session
// touched, at most once per memorySidebarRefresh and always when the session
// changed underneath the cache.
func (m *UI) sessionMemorySources(ctx context.Context) []memoryui.Source {
	sessionID := ""
	if m.hasSession() {
		sessionID = m.session.ID
	}
	if sessionID == m.memorySidebarSession && time.Since(m.memorySidebarAt) < memorySidebarRefresh {
		return m.memorySidebar
	}
	m.memorySidebarSession = sessionID
	m.memorySidebarAt = time.Now()
	m.memorySidebar = nil
	if sessionID != "" {
		if msgs, err := m.com.Workspace.ListMessages(ctx, sessionID); err == nil {
			m.memorySidebar = memoryui.SourcesFromMessages(msgs)
		}
	}
	return m.memorySidebar
}

// memoryManageCommand is the manage-from-the-badge half of the provenance surfaces:
// every id the pill, the card, or the sidebar panel shows is an id these commands act
// on by name. Pin and unpin move an entry across the Tier A line, demote and dispute
// pull it back below the automatic floor, retire tombstones it, and cite prints the
// one-line provenance again for a reader who lost it.
func (m *UI) memoryManageCommand(action string, args []string) tea.Cmd {
	ids := make([]string, 0, len(args))
	seen := map[string]bool{}
	for _, a := range args {
		if a != "" && !seen[a] {
			seen[a] = true
			ids = append(ids, a)
		}
	}
	if len(ids) == 0 {
		return util.ReportWarn("Usage: /memory " + action + " <id...> (the ids are on every memory card, pill, and sidebar row).")
	}
	return func() tea.Msg {
		store := m.memoryStore()
		if store == nil {
			return util.NewInfoMsg("Memory is off: there is nothing to " + action + ".")
		}
		ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
		defer cancel()
		entries, err := store.Get(ctx, ids...)
		if err != nil {
			return util.ReportError(fmt.Errorf("memory %s: %w", action, err))()
		}
		found := make(map[string]memory.Entry, len(entries))
		for _, e := range entries {
			found[e.ID] = e
		}
		if action == "cite" {
			var sb strings.Builder
			for _, id := range ids {
				e, ok := found[id]
				if !ok {
					fmt.Fprintf(&sb, "%s: not found\n", id)
					continue
				}
				sb.WriteString(memoryui.FromEntry(e, memoryui.RoleRecalled, "cited by id").Citation())
				sb.WriteString("\n")
			}
			return util.NewInfoMsg(strings.TrimSpace(sb.String()))
		}
		var done []string
		for _, id := range ids {
			e, ok := found[id]
			if !ok {
				continue
			}
			if err := m.applyMemoryAction(ctx, store, action, e); err != nil {
				return util.ReportError(fmt.Errorf("memory %s %s: %w", action, id, err))()
			}
			done = append(done, id)
		}
		if len(done) == 0 {
			return util.NewInfoMsg(fmt.Sprintf("None of %s name a memory entry.", strings.Join(ids, ", ")))
		}
		return util.NewInfoMsg(fmt.Sprintf("%s: %s.", action, strings.Join(done, ", ")))
	}
}

func (m *UI) applyMemoryAction(ctx context.Context, store *memory.Store, action string, e memory.Entry) error {
	switch action {
	case "pin":
		return store.SetPinned(ctx, e.Scope, e.ID, true)
	case "unpin":
		return store.SetPinned(ctx, e.Scope, e.ID, false)
	case "demote":
		if err := store.SetPinned(ctx, e.Scope, e.ID, false); err != nil {
			return err
		}
		return store.SetTrust(ctx, e.Scope, e.ID, 0.2)
	case "dispute":
		if err := store.SetTrust(ctx, e.Scope, e.ID, 0.2); err != nil {
			return err
		}
		return store.AddNote(ctx, e.Scope, e.ID, "disputed by user; verify before relying on it")
	case "retire":
		return store.SetStatus(ctx, e.Scope, e.ID, memory.StatusRetired, "")
	default:
		return fmt.Errorf("unsupported action %q", action)
	}
}

func (m *UI) memoryStatusCommand() tea.Cmd {
	return func() tea.Msg {
		store := m.memoryStore()
		if store == nil {
			return util.NewInfoMsg("Memory is off: no vault is open in this session. Set memory.enabled in phosphor.json to turn it on.")
		}
		ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
		defer cancel()
		stats, err := store.Report(ctx)
		if err != nil {
			return util.ReportError(fmt.Errorf("memory status: %w", err))()
		}
		var sb strings.Builder
		sb.WriteString("Memory (builtin provider)\n")
		fmt.Fprintf(&sb, "Corpus: %d total, %d active, %d hot, %d pending, %d retired, %d quarantined\n",
			stats.Total, stats.Active, stats.Hot, stats.Pending, stats.Retired, stats.Quarantined)
		fmt.Fprintf(&sb, "Injected window: %d/%d bytes\n", stats.Injected, stats.InjectLimit)
		fmt.Fprintf(&sb, "Vault: %s\n", store.VaultDir(memory.ScopeProject))
		if integ, err := store.ReportIntegrity(ctx); err == nil {
			if !integ.Enabled {
				sb.WriteString("Tamper seal: off (memory.integrity=false; the index accepts rows it cannot verify).\n")
			} else {
				if integ.KeyPresent {
					fmt.Fprintf(&sb, "Tamper seal: on; %d/%d entries sealed, key %s\n",
						integ.Signed, integ.Total, integ.KeyFingerprint)
				} else {
					sb.WriteString(fmt.Sprintf("Tamper seal: on; %d/%d entries sealed. Signing key MISSING — every entry fails closed until it is restored (/memory key status shows the recovery path).\n",
						integ.Signed, integ.Total))
				}
				if integ.KeyJustMinted {
					sb.WriteString("The signing key was created this session — take your backup now: /memory key show, store it offline; losing it locks the corpus out of recall.\n")
				}
				if integ.Unsigned > 0 || integ.Quarantined > 0 {
					fmt.Fprintf(&sb, "Needs attention: %d unsigned, %d quarantined (run /memory fsck to resync).\n",
						integ.Unsigned, integ.Quarantined)
				}
				if integ.DroppedVerify > 0 {
					fmt.Fprintf(&sb, "%d entries were held out of recall this session because their seal failed at read time \u2014 tampering or a changed key; inspect before trusting what remains.\n",
						integ.DroppedVerify)
				}
			}
		}
		if threads, err := store.ActiveThreads(ctx, 6); err == nil && len(threads) > 0 {
			names := make([]string, 0, len(threads))
			for _, t := range threads {
				names = append(names, fmt.Sprintf("%s (%d)", t.Headline, t.Entries))
			}
			fmt.Fprintf(&sb, "Active threads: %s\n", strings.Join(names, ", "))
		}
		if stats.Quarantined > 0 {
			sb.WriteString("Quarantined entries are held out of recall; run /memory fsck to resync.")
		}
		return util.NewInfoMsg(sb.String())
	}
}

// memorySourcesCommand answers "where did this come from" at two granularities: with a
// query it is a search over the whole corpus, and without one it is the provenance of
// the current session, read back out of the stored tool results so the list is the
// turns' own record rather than a second account of them.
func (m *UI) memorySourcesCommand(args []string) tea.Cmd {
	if !m.hasSession() && len(args) == 0 {
		return util.ReportWarn("Start a session first, or pass a query: /memory sources <query>.")
	}
	return func() tea.Msg {
		store := m.memoryStore()
		if store == nil {
			return util.NewInfoMsg("Memory is off: nothing has been recalled or written in this workspace.")
		}
		ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
		defer cancel()
		if query := strings.Join(args, " "); query != "" {
			hits, err := store.Search(ctx, memory.SearchQuery{Query: query, Limit: 10})
			if err != nil {
				return util.ReportError(fmt.Errorf("memory sources: %w", err))()
			}
			if len(hits) == 0 {
				return util.NewInfoMsg(fmt.Sprintf("No memories match %q.", query))
			}
			return util.NewInfoMsg(memoryui.Card(len(hits), memoryui.FromHits(hits, memoryui.RoleRecalled)))
		}
		msgs, err := m.com.Workspace.ListMessages(ctx, m.session.ID)
		if err != nil {
			return util.ReportError(fmt.Errorf("memory sources: %w", err))()
		}
		sources := memoryui.SourcesFromMessages(msgs)
		if len(sources) == 0 {
			threads, terr := store.ActiveThreads(ctx, 10)
			if terr == nil && len(threads) > 0 {
				names := make([]string, 0, len(threads))
				for _, t := range threads {
					names = append(names, fmt.Sprintf("%s (%d entries, last %s)", t.Headline, t.Entries, t.Touched))
				}
				return util.NewInfoMsg("This session has not touched any memories yet.\nActive threads: " + strings.Join(names, ", "))
			}
			return util.NewInfoMsg("This session has not touched any memories yet.")
		}
		return util.NewInfoMsg(memoryui.CardLabeled("This session touched ", len(sources), sources))
	}
}

// memoryFsckCommand reconciles the index against the vault and audits the
// sanitization posture. The markdown is the source of truth, so a full rescan
// is safe at any time and is the repair for every symptom where the panels and
// the recall disagree with the files. The audit half is what makes spec §9's
// invariant checkable: every indexed row must carry a sanitization stamp that
// recomputes from the defanged bytes of the file it was derived from.
func (m *UI) memoryFsckCommand() tea.Cmd {
	return func() tea.Msg {
		store := m.memoryStore()
		if store == nil {
			return util.NewInfoMsg("Memory is off: there is no index to check.")
		}
		ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
		defer cancel()
		audit, err := store.Fsck(ctx)
		if err != nil {
			return util.ReportError(fmt.Errorf("memory fsck: %w", err))()
		}
		stats, err := store.Report(ctx)
		if err != nil {
			return util.ReportError(fmt.Errorf("memory fsck: %w", err))()
		}
		msg := fmt.Sprintf(
			"Index reconciled from the vault: %d active, %d pending, %d retired, %d quarantined.",
			stats.Active, stats.Pending, stats.Retired, stats.Quarantined,
		)
		if audit.Drifted > 0 {
			msg += fmt.Sprintf(" %d files had been edited outside the gate and were re-indexed.", audit.Drifted)
		}
		if audit.Restamped > 0 {
			msg += fmt.Sprintf(" %d rows carried a stale sanitization stamp and were re-witnessed.", audit.Restamped)
		}
		if audit.BadSource > 0 {
			msg += fmt.Sprintf(" %d provenance strings failed validation and were repaired.", audit.BadSource)
		}
		if audit.Unstamped > 0 {
			msg += fmt.Sprintf(" WARNING: %d rows still carry no sanitization stamp.", audit.Unstamped)
		} else {
			msg += fmt.Sprintf(" Sanitization audit clean: all %d scanned entries stamp to their defanged bytes.", audit.Scanned)
		}
		if retries := store.BusyRetries(memory.ScopeProject); retries > 0 {
			msg += fmt.Sprintf(" %d contended writes were retried rather than lost.", retries)
		}
		return util.NewInfoMsg(msg)
	}
}

func (m *UI) memoryBudgetCommand() tea.Cmd {
	return func() tea.Msg {
		store := m.memoryStore()
		if store == nil {
			return util.NewInfoMsg("Memory is off: nothing is occupying the context window.")
		}
		ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
		defer cancel()
		stats, err := store.Report(ctx)
		if err != nil {
			return util.ReportError(fmt.Errorf("memory budget: %w", err))()
		}
		share := 0
		if stats.InjectLimit > 0 {
			share = stats.Injected * 100 / stats.InjectLimit
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Injected window: %d/%d bytes (%d%% of the cap).\n", stats.Injected, stats.InjectLimit, share)
		if stats.InjectLimitRequested > stats.InjectLimit {
			fmt.Fprintf(&sb, "Configured budget %d bytes was clamped to the %d byte ceiling.\n",
				stats.InjectLimitRequested, memory.MaxInjectBytesCeiling)
		}
		fmt.Fprintf(&sb, "Corpus behind it: %d active of %d total entries, %d tags.\n", stats.Active, stats.Total, stats.Tags)
		if len(stats.ByType) > 0 {
			keys := make([]string, 0, len(stats.ByType))
			for t := range stats.ByType {
				keys = append(keys, t)
			}
			slices.Sort(keys)
			lines := make([]string, 0, len(keys))
			for _, t := range keys {
				lines = append(lines, fmt.Sprintf("%s=%d", t, stats.ByType[t]))
			}
			fmt.Fprintf(&sb, "By type: %s\n", strings.Join(lines, ", "))
		}
		if foot, ok := m.memoryTurnFootprint(); ok {
			fmt.Fprintf(&sb, "Last write in this session: %d/%d bytes injected, corpus %d/%d/%d active/pending/retired.\n",
				foot.Injected, foot.InjectLimit, foot.Active, foot.Pending, foot.Retired)
		}
		fmt.Fprintf(&sb, "Vault: %s", store.VaultDir(memory.ScopeProject))
		return util.NewInfoMsg(sb.String())
	}
}

// memoryTurnFootprint reads the last write's budget report back out of the stored tool
// results instead of caching it on the model, so the number shown is the one the agent
// was shown at the time, even if this UI instance never saw the write happen.
func (m *UI) memoryTurnFootprint() (memoryui.Footprint, bool) {
	if !m.hasSession() {
		return memoryui.Footprint{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
	defer cancel()
	msgs, err := m.com.Workspace.ListMessages(ctx, m.session.ID)
	if err != nil {
		return memoryui.Footprint{}, false
	}
	return memoryui.TurnFootprint(msgs)
}

// memoryPolicyCommand is the human's half of standing instructions. With no argument it
// lists what is on file; with one it records a type:policy entry through the store,
// which is where §5 says bespoke rules live: the same bytes the agent reads at turn
// start, authored this time by the user and owned as human, so the write path never
// overwrites them as a guess.
func (m *UI) memoryPolicyCommand(args []string) tea.Cmd {
	rule := strings.TrimSpace(strings.Join(args, " "))
	if rule == "" {
		return func() tea.Msg {
			store := m.memoryStore()
			if store == nil {
				return util.NewInfoMsg("Memory is off: no policy entries can be read.")
			}
			ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
			defer cancel()
			hits, err := store.Search(ctx, memory.SearchQuery{Types: []memory.Type{memory.TypePolicy}, Limit: 20})
			if err != nil {
				return util.ReportError(fmt.Errorf("memory policy: %w", err))()
			}
			if len(hits) == 0 {
				return util.NewInfoMsg("No policy entries yet. Add one with: /memory policy <rule>")
			}
			return util.NewInfoMsg(memoryui.Card(len(hits), memoryui.FromHits(hits, memoryui.RoleInjected)))
		}
	}
	return func() tea.Msg {
		store := m.memoryStore()
		if store == nil {
			return util.ReportError(fmt.Errorf("memory is off: enable memory.enabled before recording policy"))()
		}
		ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
		defer cancel()
		asserted := true
		e := memory.Entry{
			ID:       memory.NewID("policy", rule),
			Thread:   "policy",
			Type:     memory.TypePolicy,
			Summary:  rule,
			Tags:     memory.Keywords(rule, 6),
			Source:   "user@slash",
			Trust:    0.8,
			Status:   memory.StatusActive,
			Owner:    memory.OwnerHuman,
			Asserted: &asserted,
		}
		e.Normalized(time.Now().UTC())
		if err := store.Put(ctx, e); err != nil {
			return util.ReportError(fmt.Errorf("memory policy: %w", err))()
		}
		return util.NewInfoMsg(fmt.Sprintf("Policy recorded: %s (%s)", rule, e.ID))
	}
}

// memoryKeyCommand is the operator-facing control for the tamper seal:
// `/memory key` reports whether the seal is on and how much of the corpus is
// sealed, `/memory key show` prints the signing key as its BIP39 backup phrase,
// and `/memory key rotate confirm` mints a fresh key and re-seals every entry
// under it. Rotation is destructive to any previously exported backup phrase, so
// it asks for the literal word confirm rather than acting on bare intent.
func (m *UI) memoryKeyCommand(args []string) tea.Cmd {
	action := "status"
	if len(args) > 0 {
		action = strings.ToLower(args[0])
	}
	return func() tea.Msg {
		// When the seal is on but its signing key is gone, the ordinary open fails
		// closed, which would otherwise lock the operator out of the very command
		// that recovers the key. Fall back to a maintenance handle that opens over a
		// keyless sealed vault so status/restore/rotate stay reachable.
		store := m.memoryStore()
		maintenance := false
		if store == nil {
			if aw, ok := m.com.Workspace.(*workspace.AppWorkspace); ok {
				var reachable bool
				store, reachable = m.openMemoryMaintenanceStore(aw)
				maintenance = reachable
			}
		}
		if store == nil {
			return util.NewInfoMsg("Memory is off: there is no signing key to inspect.")
		}
		if maintenance {
			defer func() { _ = store.Close() }()
		}
		ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
		defer cancel()
		switch action {
		case "", "status":
			st, err := store.ReportIntegrity(ctx)
			if err != nil {
				return util.ReportError(fmt.Errorf("memory integrity status: %w", err))()
			}
			var sb strings.Builder
			if st.Enabled {
				sb.WriteString("Memory tamper seal: on\n")
			} else {
				sb.WriteString("Memory tamper seal: off (set memory.integrity in phosphor.json to turn it on)\n")
			}
			if st.KeyPresent {
				if st.KeyFingerprint != "" {
					fmt.Fprintf(&sb, "Signing key: present (fingerprint %s)\n", st.KeyFingerprint)
				} else {
					sb.WriteString("Signing key: present\n")
				}
				if st.KeyJustMinted {
					sb.WriteString("The key was created this session — store the phrase offline: /memory key show\n")
				}
			} else if st.Enabled {
				sb.WriteString("Signing key: MISSING (entries fail closed until it is restored)\n")
			}
			fmt.Fprintf(&sb, "Corpus: %d total, %d sealed, %d unsigned, %d quarantined\n",
				st.Total, st.Signed, st.Unsigned, st.Quarantined)
			if st.Enabled && !st.KeyPresent {
				sb.WriteString("Restore a backed-up key with: /memory key restore <phrase>")
			}
			return util.NewInfoMsg(strings.TrimRight(sb.String(), "\n"))
		case "show":
			phrase, err := memory.IntegrityKeyMnemonic()
			if err != nil {
				return util.ReportError(fmt.Errorf("read signing key: %w", err))()
			}
			return util.NewInfoMsg(
				"Memory signing key (BIP39). Store this offline like any other secret:\n\n" + phrase)
		case "restore":
			if len(args) < 2 {
				return util.ReportWarn("Usage: /memory key restore <bip39-phrase>")()
			}
			if _, err := memory.RestoreIntegrityKeyFromMnemonic(strings.Join(args[1:], " ")); err != nil {
				return util.ReportError(fmt.Errorf("restore signing key: %w", err))()
			}
			// Adopt the restored phrase in the live handle and clear the failed-open
			// latch, so recall returns under the original key without a process restart.
			if err := store.ReloadIntegrityKey(ctx); err != nil {
				return util.ReportError(fmt.Errorf("reload signing key: %w", err))()
			}
			m.resetMemoryFallback()
			return util.NewInfoMsg("Signing key restored from the phrase; memory recall is active again.")
		case "rotate":
			if len(args) < 2 || strings.ToLower(args[1]) != "confirm" {
				return util.ReportWarn("Rotating the key re-seals every entry under a new one and invalidates any " +
					"previously exported backup phrase. Run /memory key rotate confirm to proceed.")()
			}
			n, err := store.RotateIntegrityKey(ctx)
			if err != nil {
				return util.ReportError(fmt.Errorf("rotate signing key: %w", err))()
			}
			m.resetMemoryFallback()
			return util.NewInfoMsg(fmt.Sprintf(
				"Rotated the signing key and re-sealed %d %s. Export the new backup with /memory key show.",
				n, memoryPlural(n, "entry", "entries")))
		default:
			return util.ReportWarn("Usage: /memory key [status|show|restore <phrase>|rotate confirm]")()
		}
	}
}

// memoryPlural picks the count-appropriate noun for a report line.
func memoryPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// reviewPageSize bounds one /memory review listing. It is a console decision
// surface, not a pager: the corpus belongs to /memory sources and the vault,
// and a person deciding should see the queue that fits on a screen.
const reviewPageSize = 25

// memoryReviewCommand is the human's decision surface for the two rooms where
// memory parks things that wait on a person: unconfirmed drafts (status
// pending, inert in every read path until somebody answers for them) and
// queued proposals (writes the gate declined to commit unattended). The bare
// form opens the review dialog, which is where the reading and deciding
// happen: it gives an entry the screen real estate a list row cannot and keeps
// the queue out of the conversation's display history. The decision sub-
// commands stay as one-line direct actions so the surface remains scriptable
// from the editor too. Drafts move through the gate's managed ops, which is
// the same system-owned lane the agent's memory(op=confirm) rides, and
// proposals through Resolve, which feeds the bucket's risk learning when one
// is declined. Drafts are worth deciding deliberately because nothing else
// will: the aging pass only touches rows that have been used, and a draft is
// never recalled, so an unreviewed one would sit pending forever.
func (m *UI) memoryReviewCommand(args []string) tea.Cmd {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	rest := args[min(len(args), 1):]
	switch sub {
	case "":
		return m.openMemoryReviewDialog()
	case "confirm", "ignore", "retire":
	default:
		return util.ReportWarn(fmt.Sprintf(
			"Unknown /memory review subcommand %q. Open the queue with a bare /memory review, or decide with confirm, ignore, or retire.", sub))
	}
	ids := make([]string, 0, len(rest))
	seen := map[string]bool{}
	for _, a := range rest {
		if a != "" && !seen[a] {
			seen[a] = true
			ids = append(ids, a)
		}
	}
	if len(ids) == 0 {
		return util.ReportWarn("Usage: /memory review confirm|ignore|retire <id...> (the ids come from the /memory review dialog).")
	}
	return func() tea.Msg {
		store := m.memoryStore()
		if store == nil {
			return util.NewInfoMsg("Memory is off: nothing is waiting on you.")
		}
		ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
		defer cancel()
		gate := memory.NewGate(store, nil, memorytools.PolicyFromConfig(m.com.Config()))
		sessionID := ""
		if m.hasSession() {
			sessionID = m.session.ID
		}
		var decided, missed []string
		for _, id := range ids {
			ok, err := reviewDecideOne(ctx, store, gate, sessionID, id, sub)
			if err != nil {
				return util.ReportError(fmt.Errorf("memory review %s %s: %w", sub, id, err))()
			}
			if !ok {
				missed = append(missed, id)
				continue
			}
			decided = append(decided, id)
		}
		if len(decided) == 0 {
			return util.NewInfoMsg(fmt.Sprintf(
				"None of %s named something waiting on you. Nothing was changed; a bare /memory review lists what is.",
				strings.Join(ids, ", ")))
		}
		msg := fmt.Sprintf("%s: %s.", sub, strings.Join(decided, ", "))
		if len(missed) > 0 {
			msg += fmt.Sprintf(" Not waiting on you: %s.", strings.Join(missed, ", "))
		}
		return util.NewInfoMsg(msg)
	}
}

// reviewDecideOne answers one waiting-room id: a numeric id is a proposal,
// where confirm commits the queued bytes and ignore or retire both decline
// it, which is the answer that teaches the bucket where this user's line is.
// Anything else has to name a pending draft, and moves through the gate's
// managed lane. The bool reports whether the id still named something
// waiting: an entry decided in another room since the listing came back is
// reported as a miss rather than double-decided.
func reviewDecideOne(ctx context.Context, store *memory.Store, gate *memory.Gate, sessionID, id, sub string) (bool, error) {
	if n, err := strconv.ParseInt(id, 10, 64); err == nil {
		out, err := gate.Resolve(ctx, int64(n), sub == "confirm")
		if err != nil {
			return false, err
		}
		return out.Status != memory.StatusNoOp, nil
	}
	scope, ok := reviewPendingScope(ctx, store, id)
	if !ok {
		return false, nil
	}
	op := memory.OpConfirm
	switch sub {
	case "ignore":
		op = memory.OpIgnore
	case "retire":
		op = memory.OpRetire
	}
	out, err := gate.Apply(ctx, memory.WriteRequest{
		SessionID: sessionID,
		Primary:   true,
		Entry:     memory.Entry{ID: id, Scope: scope},
	}, op)
	if err != nil {
		return false, err
	}
	return out.Status != memory.StatusRefused, nil
}

// reviewPendingScope resolves the bank a draft lives in from the pending
// listing, which doubles as the confirmation that the id names a draft at
// all: ids naming live entries belong to the pin/demote/dispute/retire
// family, not to review.
func reviewPendingScope(ctx context.Context, store *memory.Store, id string) (memory.Scope, bool) {
	list, err := store.ListPending(ctx, reviewPageSize)
	if err != nil {
		return "", false
	}
	for _, e := range list {
		if e.ID == id {
			return e.Scope, true
		}
	}
	return "", false
}

// openMemoryReviewDialog puts the review queue on the screen. Opening is
// cheap and empty until the load command lands, so the dialog never renders
// a half-read queue as if it were the truth.
func (m *UI) openMemoryReviewDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.MemoryReviewID) {
		m.dialog.BringToFront(dialog.MemoryReviewID)
		return m.loadMemoryReviewData()
	}
	m.dialog.OpenDialog(dialog.NewMemoryReview(m.com))
	return m.loadMemoryReviewData()
}

// loadMemoryReviewData reads the two waiting rooms off the vault and hands
// the dialog its snapshot. The read rides a tea.Cmd so a contended index can
// never block the update loop; the dialog only stores what arrives.
func (m *UI) loadMemoryReviewData() tea.Cmd {
	return func() tea.Msg {
		store := m.memoryStore()
		if store == nil {
			return dialog.MemoryReviewLoadedMsg{Note: "Memory is off: nothing is waiting on you."}
		}
		ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
		defer cancel()
		gate := memory.NewGate(store, nil, memorytools.PolicyFromConfig(m.com.Config()))
		pending, err := store.ListPending(ctx, reviewPageSize)
		if err != nil {
			return dialog.MemoryReviewLoadedMsg{Err: fmt.Errorf("memory review: %w", err)}
		}
		proposals, err := gate.Pending(ctx)
		if err != nil {
			return dialog.MemoryReviewLoadedMsg{Err: fmt.Errorf("memory review: %w", err)}
		}
		return dialog.MemoryReviewLoadedMsg{Items: memoryReviewItems(pending, proposals)}
	}
}

// decideMemoryReview answers one item from the dialog and re-sends the
// queue, so a decision that was refused upstream reappears rather than
// vanishing on an optimistic remove. The result line rides in the message's
// Result field, which the dialog flashes briefly under its own timer; the
// subtitle keeps counting what is left, and deciding never writes to the
// conversation surface.
func (m *UI) decideMemoryReview(item dialog.MemoryReviewItem, decision dialog.ReviewDecision) tea.Cmd {
	return func() tea.Msg {
		store := m.memoryStore()
		if store == nil {
			return dialog.MemoryReviewLoadedMsg{Note: "Memory is off: nothing is waiting on you."}
		}
		ctx, cancel := context.WithTimeout(context.Background(), memoryCommandTimeout)
		defer cancel()
		gate := memory.NewGate(store, nil, memorytools.PolicyFromConfig(m.com.Config()))
		sessionID := ""
		if m.hasSession() {
			sessionID = m.session.ID
		}
		ok, err := reviewDecideOne(ctx, store, gate, sessionID, item.ID, string(decision))
		if err != nil {
			return dialog.MemoryReviewLoadedMsg{Err: fmt.Errorf("memory review %s %s: %w", decision, item.ID, err)}
		}
		pending, err := store.ListPending(ctx, reviewPageSize)
		if err != nil {
			return dialog.MemoryReviewLoadedMsg{Err: fmt.Errorf("memory review: %w", err)}
		}
		proposals, err := gate.Pending(ctx)
		if err != nil {
			return dialog.MemoryReviewLoadedMsg{Err: fmt.Errorf("memory review: %w", err)}
		}
		result := fmt.Sprintf("%s: %s.", decision, item.ID)
		if !ok {
			result = fmt.Sprintf("%s: %s was already answered elsewhere; nothing changed.", decision, item.ID)
		}
		return dialog.MemoryReviewLoadedMsg{Items: memoryReviewItems(pending, proposals), Result: result}
	}
}

// memoryReviewItems converts the two waiting rooms into the dialog's
// presentation form. The list row stays deliberately narrow — identity, a
// one-line summary, the reason it waits — because the full reading is the
// detail view's job, not the row's.
func memoryReviewItems(pending []memory.Entry, proposals []memory.Proposal) []dialog.MemoryReviewItem {
	items := make([]dialog.MemoryReviewItem, 0, len(pending)+len(proposals))
	for _, e := range pending {
		label := fmt.Sprintf("%s [%s]", e.ID, e.Type)
		if e.Thread != "" {
			label += fmt.Sprintf(" in %q", e.Thread)
		}
		items = append(items, dialog.MemoryReviewItem{
			Kind:    "draft",
			ID:      e.ID,
			Label:   label,
			Summary: reviewOneLine(e.Summary, 96),
			Why:     reviewWhy(e),
			Detail:  reviewDetail(e.Summary, reviewWhy(e), e.Body),
		})
	}
	for _, p := range proposals {
		summary := p.Rationale
		var e memory.Entry
		if err := json.Unmarshal([]byte(p.Payload), &e); err == nil && e.Summary != "" {
			summary = e.Summary
		}
		items = append(items, dialog.MemoryReviewItem{
			Kind:    "proposal",
			ID:      strconv.FormatInt(p.ID, 10),
			Label:   fmt.Sprintf("#%d %s [%s]", p.ID, p.Op, p.Bucket),
			Summary: reviewOneLine(summary, 96),
			Why:     "queued by the gate",
			Detail:  reviewDetail(summary, "bucket "+p.Bucket, e.Body),
		})
	}
	return items
}

// reviewDetail composes the detail view's reading: what it asserts, why it
// waits, and the full body that never fits a list row.
func reviewDetail(summary, why, body string) string {
	var sb strings.Builder
	if summary != "" {
		fmt.Fprintf(&sb, "%s\n", strings.TrimSpace(summary))
	}
	if why != "" {
		fmt.Fprintf(&sb, "why: %s\n", why)
	}
	if body = strings.TrimSpace(body); body != "" {
		sb.WriteString("\n" + body)
	}
	return strings.TrimSpace(sb.String())
}

// reviewWhy is the one-line answer to "why is this waiting": an unconfirmed
// inference is its own reason, and anything else carries whatever provenance
// the writer recorded.
func reviewWhy(e memory.Entry) string {
	if e.Inferred() {
		return "unconfirmed inference"
	}
	if e.Source != "" {
		return "from " + e.Source
	}
	return ""
}

// reviewOneLine collapses a stored field to one tidy line at most n runes.
func reviewOneLine(s string, n int) string {
	line := strings.Join(strings.Fields(s), " ")
	if r := []rune(line); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return line
}
