// Package dialog provides the workspace (FTS5) index management dialog.
package dialog

import (
	"context"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/internal/workspaceindex"
	"github.com/hackafterdark/phosphor/pkg/config"
)

// WorkspaceIndexID is the identifier for the workspace index management dialog.
const WorkspaceIndexID = "workspace_index"

// workspaceIndexPollInterval is how often the dialog re-reads the index
// progress while a full build is running, so the percentage advances live.
const workspaceIndexPollInterval = 400 * time.Millisecond

// workspaceIndexIdleInterval is the gentler poll cadence used while no build is
// running, so externally triggered rebuilds still surface without hammering
// the store while the dialog simply sits open.
const workspaceIndexIdleInterval = 1500 * time.Millisecond

// ActionToggleWorkspaceFullTextEnabled toggles the FTS5 index on/off.
type ActionToggleWorkspaceFullTextEnabled struct{}

// ActionToggleWorkspaceFullTextAutoIndex toggles the auto-update watcher.
type ActionToggleWorkspaceFullTextAutoIndex struct{}

// ActionUpdateWorkspaceIndex requests an incremental rescan: changed files are
// re-indexed and a reconcile pass prunes anything deleted or newly ignored.
type ActionUpdateWorkspaceIndex struct{}

// ActionRebuildWorkspaceIndex requests a full rebuild: the index is cleared
// and re-walked from scratch under one lock, so no stale rows can survive.
type ActionRebuildWorkspaceIndex struct{}

// ActionClearWorkspaceIndex clears all rows from the FTS5 index.
type ActionClearWorkspaceIndex struct{}

// workspaceIndexRefreshMsg asks the dialog to re-read index progress from the
// store. It is emitted by a [tea.Cmd] so the blocking read stays out of
// HandleMsg.
type workspaceIndexRefreshMsg struct{}

// workspaceIndexProgressMsg carries progress loaded by a [tea.Cmd] back into
// the dialog state.
type workspaceIndexProgressMsg struct {
	progress *workspaceindex.IndexProgress
	err      error
}

// workspaceIndexMode is a small state machine so the destructive Rebuild and
// Clear rows require an explicit confirmation before their action fires,
// mirroring how the sessions dialog gates deletions.
type workspaceIndexMode int

const (
	workspaceIndexModeNormal workspaceIndexMode = iota
	workspaceIndexModeConfirmRebuild
	workspaceIndexModeConfirmClear
)

// WorkspaceIndexItem is a selectable action row in the workspace index dialog.
type WorkspaceIndexItem struct {
	*list.Versioned
	Title   string
	ID      string
	filter  string
	Action  Action
	t       *styles.Styles
	focused bool
}

// Render returns the rendered representation of the item.
func (i *WorkspaceIndexItem) Render(width int) string {
	if i.focused {
		return i.t.Dialog.SelectedItem.Render(i.Title)
	}
	return i.t.Dialog.NormalItem.Render(i.Title)
}

// Version returns the version of the item.
func (i *WorkspaceIndexItem) Version() uint64 {
	return i.Versioned.Version()
}

// Finished reports that the item is in a terminal state.
func (i *WorkspaceIndexItem) Finished() bool {
	return false
}

// Filter returns the filter string for the item.
func (i *WorkspaceIndexItem) Filter() string {
	return i.filter
}

// SetFocused updates the focus state and bumps the version.
func (i *WorkspaceIndexItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.focused = focused
	i.Bump()
}

// setTitle updates the item label and bumps its version so the list cache
// re-renders it.
func (i *WorkspaceIndexItem) setTitle(title string) {
	if i.Title == title {
		return
	}
	i.Title = title
	i.Bump()
}

// WorkspaceIndex manages the FTS5 symbol/document workspace index. It shows
// the live build status and offers enable, auto-update, rebuild, and clear
// actions. It is deliberately independent of the vector-embeddings index.
type WorkspaceIndex struct {
	com   *common.Common
	store *workspaceindex.Store
	list  *list.List

	items struct {
		enable  *WorkspaceIndexItem
		auto    *WorkspaceIndexItem
		update  *WorkspaceIndexItem
		rebuild *WorkspaceIndexItem
		clear   *WorkspaceIndexItem
	}

	mode workspaceIndexMode

	progress    *workspaceindex.IndexProgress
	progressErr error

	windowWidth, windowHeight int
	dialogWidth, dialogHeight int

	keyMap struct {
		Select,
		Up,
		Down,
		Confirm,
		Cancel,
		Close key.Binding
	}
}

var _ Dialog = (*WorkspaceIndex)(nil)

// NewWorkspaceIndex creates a new workspace index management dialog. The store
// is the shared [workspaceindex.Store] owned by the app; it may be nil when
// indexing is not configured, in which case the dialog still renders and lets
// the user enable the feature.
func NewWorkspaceIndex(com *common.Common, store *workspaceindex.Store) *WorkspaceIndex {
	d := &WorkspaceIndex{com: com, store: store}
	d.list = list.NewList()

	d.items.enable = &WorkspaceIndexItem{Versioned: list.NewVersioned(), ID: "enable", Title: "Enable Workspace Index", filter: "enable disable indexing full text fts", Action: ActionToggleWorkspaceFullTextEnabled{}, t: com.Styles}
	d.items.auto = &WorkspaceIndexItem{Versioned: list.NewVersioned(), ID: "auto", Title: "Auto-Update: OFF", filter: "auto update watcher live", Action: ActionToggleWorkspaceFullTextAutoIndex{}, t: com.Styles}
	d.items.update = &WorkspaceIndexItem{Versioned: list.NewVersioned(), ID: "update", Title: "Update Index", filter: "update rescan changed files sync incremental", Action: ActionUpdateWorkspaceIndex{}, t: com.Styles}
	d.items.rebuild = &WorkspaceIndexItem{Versioned: list.NewVersioned(), ID: "rebuild", Title: "Rebuild Index (clear + full re-index)", filter: "rebuild build reindex scan clear from scratch", Action: ActionRebuildWorkspaceIndex{}, t: com.Styles}
	d.items.clear = &WorkspaceIndexItem{Versioned: list.NewVersioned(), ID: "clear", Title: "Clear Index", filter: "clear delete wipe reset empty", Action: ActionClearWorkspaceIndex{}, t: com.Styles}

	d.list.SetItems(d.items.enable, d.items.auto, d.items.update, d.items.rebuild, d.items.clear)
	d.list.RegisterRenderCallback(func(idx, selectedIdx int, item list.Item) list.Item {
		if wi, ok := item.(*WorkspaceIndexItem); ok {
			wi.SetFocused(idx == selectedIdx)
		}
		return item
	})
	d.list.SetSelected(0)

	d.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "select"),
	)
	d.keyMap.Up = key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "prev"))
	d.keyMap.Down = key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "next"))
	d.keyMap.Confirm = key.NewBinding(key.WithKeys("y", "enter"), key.WithHelp("y", "confirm"))
	d.keyMap.Cancel = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "cancel"))
	d.keyMap.Close = CloseKey

	d.syncLabels()
	return d
}

// ID returns the dialog identifier.
func (d *WorkspaceIndex) ID() string {
	return WorkspaceIndexID
}

// InitialCmd primes a one-shot progress refresh shortly after the dialog opens.
func (d *WorkspaceIndex) InitialCmd() tea.Cmd {
	return tea.Tick(workspaceIndexPollInterval, func(time.Time) tea.Msg {
		return workspaceIndexRefreshMsg{}
	})
}

// syncLabels refreshes the action labels from the current configuration state.
func (d *WorkspaceIndex) syncLabels() {
	wi := fullTextConfig(d.com)
	enabled := wi != nil && wi.Enabled
	if enabled {
		d.items.enable.setTitle("Disable Workspace Index")
	} else {
		d.items.enable.setTitle("Enable Workspace Index")
	}
	if enabled && wi.AutoIndexEnabled() {
		d.items.auto.setTitle("Auto-Update: ON")
	} else {
		d.items.auto.setTitle("Auto-Update: OFF")
	}
}

// pendingAction returns the action gated behind the current confirmation,
// if any.
func (d *WorkspaceIndex) pendingAction() Action {
	switch d.mode {
	case workspaceIndexModeConfirmRebuild:
		return ActionRebuildWorkspaceIndex{}
	case workspaceIndexModeConfirmClear:
		return ActionClearWorkspaceIndex{}
	default:
		return nil
	}
}

// HandleMsg processes messages and returns actions.
func (d *WorkspaceIndex) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.windowWidth = msg.Width
		d.windowHeight = msg.Height
		return nil
	case workspaceIndexRefreshMsg:
		// The blocking store read lives in a Cmd, not here.
		return ActionCmd{Cmd: d.fetchProgressCmd()}
	case workspaceIndexProgressMsg:
		d.progress = msg.progress
		d.progressErr = msg.err
		// Always keep a self-rescheduling poll alive while the dialog is
		// open so externally triggered builds (Rebuild) show live progress.
		// Poll fast during a build, gently while idle.
		interval := workspaceIndexIdleInterval
		if msg.progress != nil && msg.progress.Status == workspaceindex.IndexStatusIndexing {
			interval = workspaceIndexPollInterval
		}
		return ActionCmd{Cmd: tea.Tick(interval, func(time.Time) tea.Msg {
			return workspaceIndexRefreshMsg{}
		})}

	case tea.KeyPressMsg:
		if d.mode != workspaceIndexModeNormal {
			switch {
			case key.Matches(msg, d.keyMap.Confirm), key.Matches(msg, d.keyMap.Select):
				action := d.pendingAction()
				d.mode = workspaceIndexModeNormal
				return action
			case key.Matches(msg, d.keyMap.Cancel), key.Matches(msg, d.keyMap.Close):
				d.mode = workspaceIndexModeNormal
				return nil
			default:
				// Any other key abandons the pending confirmation and is
				// handled as ordinary navigation just below.
				d.mode = workspaceIndexModeNormal
			}
		}
		switch {
		case key.Matches(msg, d.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, d.keyMap.Up):
			d.list.Focus()
			if d.list.IsSelectedFirst() {
				d.list.SelectLast()
			} else {
				d.list.SelectPrev()
			}
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Down):
			d.list.Focus()
			if d.list.IsSelectedLast() {
				d.list.SelectFirst()
			} else {
				d.list.SelectNext()
			}
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Select):
			if item, ok := d.list.SelectedItem().(*WorkspaceIndexItem); ok && item != nil {
				switch item.ID {
				case "rebuild":
					d.mode = workspaceIndexModeConfirmRebuild
					return nil
				case "clear":
					d.mode = workspaceIndexModeConfirmClear
					return nil
				default:
					return item.Action
				}
			}
		}
	}
	return nil
}

// fetchProgressCmd reads index progress off the store off the UI thread.
func (d *WorkspaceIndex) fetchProgressCmd() tea.Cmd {
	if d.store == nil {
		return nil
	}
	store := d.store
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		progress, err := store.GetProgress(ctx)
		return workspaceIndexProgressMsg{progress: progress, err: err}
	}
}

// Draw renders the dialog.
func (d *WorkspaceIndex) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	d.dialogWidth = max(0, min(64, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	d.dialogHeight = max(0, min(20, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))

	d.syncLabels()

	rc := NewRenderContext(t, d.dialogWidth)
	rc.Title = "Workspace Index"

	contentWidth := d.dialogWidth - t.Dialog.View.GetHorizontalFrameSize()

	// Status panel.
	for _, line := range d.statusLines(contentWidth) {
		rc.AddPart(line)
	}
	rc.AddPart("")

	// Action list. The list gets whatever content height remains after the
	// status block.
	listHeight := max(1, d.dialogHeight-t.Dialog.View.GetVerticalFrameSize()-t.Dialog.Title.GetVerticalFrameSize()-len(d.statusLines(contentWidth))-1)
	d.list.SetSize(max(0, contentWidth-t.Dialog.View.GetHorizontalFrameSize()), listHeight)
	rc.AddPart(d.list.Render())

	rc.Help = "↑↓ navigate · enter select · esc close"
	DrawCenter(scr, area, rc.Render())
	return nil
}

// statusLines renders the read-only status block shown above the actions.
func (d *WorkspaceIndex) statusLines(width int) []string {
	t := d.com.Styles
	secondary := t.Dialog.SecondaryText
	var lines []string

	if d.mode != workspaceIndexModeNormal {
		prompt := "Clear the index and leave it empty until you rebuild it?"
		if d.mode == workspaceIndexModeConfirmRebuild {
			prompt = "Clear the index and rebuild it from scratch?"
		}
		return []string{
			secondary.Render("⚠  " + prompt),
			secondary.Render("   y confirm  ·  n / esc cancel"),
		}
	}

	wi := fullTextConfig(d.com)
	if wi == nil || !wi.Enabled {
		return append(lines,
			secondary.Render("Workspace search indexing is off."),
			secondary.Render("Enable it to build the FTS5 symbol & doc index."),
		)
	}

	if d.store == nil {
		return append(lines, secondary.Render("Index store unavailable."))
	}
	if d.progressErr != nil {
		return append(lines, secondary.Render("Status unavailable: "+d.progressErr.Error()))
	}
	if d.progress == nil {
		return append(lines, secondary.Render("Loading index status…"))
	}

	p := d.progress
	switch p.Status {
	case workspaceindex.IndexStatusIndexing:
		lines = append(lines, secondary.Render("Building index… "+abbrevNumber(p.FilesIndexed)+"/"+abbrevNumber(p.TotalFiles)))
		if p.CurrentFile != "" {
			lines = append(lines, secondary.Render("  "+truncMiddle(p.CurrentFile, max(4, width-2))))
		}
	case workspaceindex.IndexStatusError:
		lines = append(lines, secondary.Render("Last build errored. Try Rebuild."))
	}

	if p.Status != workspaceindex.IndexStatusIndexing {
		lines = append(lines, secondary.Render("Files "+abbrevNumber(p.FilesIndexed)+"  Symbols "+abbrevNumber(p.SymbolsIndexed)+"  Docs "+abbrevNumber(p.DocsIndexed)))
	}

	// Build freshness only. The Auto-Update row below already carries its own
	// on/off state, so repeating it here was pure duplication.
	var chip string
	switch {
	case p.Stale:
		chip = "partial - run Rebuild"
	case p.UpdatedSinceBuild > 0 && !p.LastBuilt.IsZero():
		chip = "built " + relTimeAgo(p.LastBuilt) + " - changes pending"
	case !p.LastBuilt.IsZero():
		chip = "built " + relTimeAgo(p.LastBuilt)
	default:
		chip = "never built"
	}
	lines = append(lines, secondary.Render("  ["+chip+"]"))
	return lines
}

// fullTextConfig returns the FTS5 config block, or nil when unset.
func fullTextConfig(com *common.Common) *config.FullTextIndex {
	cfg := com.Config()
	if cfg == nil || cfg.WorkspaceSearch == nil {
		return nil
	}
	return cfg.WorkspaceSearch.FullText
}

// abbrevNumber renders an integer compactly (1200 -> 1.2k).
func abbrevNumber(n int) string {
	switch {
	case n >= 1_000_000:
		return trimZero(strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64)) + "m"
	case n >= 1_000:
		return trimZero(strconv.FormatFloat(float64(n)/1_000, 'f', 1, 64)) + "k"
	default:
		return strconv.Itoa(n)
	}
}

// trimZero drops a trailing ".0" from a fixed-point string.
func trimZero(s string) string {
	return strings.TrimSuffix(s, ".0")
}

// truncMiddle elides the middle of s so it fits within n runes.
func truncMiddle(s string, n int) string {
	r := []rune(s)
	if len(r) <= n || n <= 1 {
		return s
	}
	if n <= 3 {
		return string(r[:n-1]) + "…"
	}
	head := (n - 1) / 2
	tail := (n - 1) - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// relTimeAgo renders a coarse "x ago" string for a timestamp.
func relTimeAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}
}
