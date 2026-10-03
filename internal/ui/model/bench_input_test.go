package model

import (
	"context"
	"fmt"
	"image"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/memory"
	memoryui "github.com/hackafterdark/phosphor/internal/memory/ui"
	"github.com/hackafterdark/phosphor/internal/ui/attachments"
	"github.com/hackafterdark/phosphor/internal/ui/chat"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/completions"
	"github.com/hackafterdark/phosphor/internal/ui/dialog"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	uistyles "github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/internal/workspace"
	"github.com/hackafterdark/phosphor/internal/workspaceindex"
	"github.com/hackafterdark/phosphor/pkg/agent/tools/mcp"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/csync"
	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/session"
)

type benchItem struct {
	*list.Versioned
	text string
}

func (b *benchItem) Render(width int) string { return b.text }
func (b *benchItem) Finished() bool          { return true }

type benchWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (w *benchWorkspace) Config() *config.Config                           { return w.cfg }
func (w *benchWorkspace) WorkingDir() string                               { return "." }
func (w *benchWorkspace) AgentIsReady() bool                               { return true }
func (w *benchWorkspace) AgentIsBusy() bool                                { return false }
func (w *benchWorkspace) PermissionSkipRequests() bool                     { return false }
func (w *benchWorkspace) AgentQueuedPrompts(string) int                    { return 0 }
func (w *benchWorkspace) AgentModel() workspace.AgentModel                 { return workspace.AgentModel{} }
func (w *benchWorkspace) LSPGetStates() map[string]workspace.LSPClientInfo { return nil }
func (w *benchWorkspace) MCPGetStates() map[string]mcp.ClientInfo          { return nil }

func benchUI(tb testing.TB) *UI {
	tb.Helper()
	ws := &benchWorkspace{cfg: &config.Config{
		Options:   &config.Options{TUI: &config.TUIOptions{}},
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Agents:    map[string]config.Agent{config.AgentSystem: {Model: config.SelectedModelTypeLarge}},
	}}
	styles := uistyles.Theme("", "", ".")
	com := &common.Common{Workspace: ws, Styles: &styles}

	ta := textarea.New()
	ta.SetStyles(com.Styles.Editor.Textarea)
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.SetVirtualCursor(false)
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 10
	ta.Focus()

	ch := NewChat(com)
	items := make([]list.Item, 0, 100)
	for i := 0; i < 100; i++ {
		items = append(items, &benchItem{Versioned: list.NewVersioned(), text: fmt.Sprintf("message %d some fairly long rendered content for the chat list item", i)})
	}
	ch.list.SetItems(items...)

	ui := &UI{
		com:         com,
		dialog:      dialog.NewOverlay(),
		keyMap:      DefaultKeyMap(),
		textarea:    ta,
		chat:        ch,
		completions: completions.New(com.Styles.Completions.Normal, com.Styles.Completions.Focused, com.Styles.Completions.Match),
		attachments: attachments.New(
			attachments.NewRenderer(
				com.Styles.Attachments.Normal,
				com.Styles.Attachments.Deleting,
				com.Styles.Attachments.Image,
				com.Styles.Attachments.Text,
				com.Styles.Attachments.Skill,
			),
			attachments.Keymap{},
		),
		focus:   uiFocusEditor,
		state:   uiChat,
		session: &session.Session{ID: "bench"},
		width:   120,
		height:  40,
	}
	ui.status = NewStatus(com, ui)
	ui.header = newHeader(com)
	ui.setEditorPrompt()
	ui.updateLayoutAndSize()
	return ui
}

func BenchmarkUIKeyPressUpdate(b *testing.B) {
	ui := benchUI(b)
	msg := tea.KeyPressMsg{Code: 'a', Text: "a"}
	start := time.Now()
	for i := 0; i < b.N; i++ {
		ui.Update(msg)
	}
	elapsed := time.Since(start)
	b.ReportMetric(float64(elapsed.Nanoseconds())/float64(b.N), "ns/op")
}

func BenchmarkUIView(b *testing.B) {
	ui := benchUI(b)
	ui.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	start := time.Now()
	for i := 0; i < b.N; i++ {
		_ = ui.View()
	}
	elapsed := time.Since(start)
	b.ReportMetric(float64(elapsed.Nanoseconds())/float64(b.N), "ns/op")
}

func TestBenchSanity(t *testing.T) {
	ui := benchUI(t)
	ui.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	v := ui.View()
	t.Logf("view len=%d cursor=%+v", len(v.Content), v.Cursor)
	m := message.Message{ID: "1", Role: message.User}
	_ = m
}

// BenchmarkSidebarStoreReads measures the throttled synchronous reads the chat
// sidebar performs inside the draw path: workspace-search progress (per ~1s)
// and the memory panel tallies (per ~2s), against the real on-disk stores.
func BenchmarkSidebarStoreReads(b *testing.B) {
	ctx := context.Background()
	idx, err := workspaceindex.NewStore(".")
	if err != nil {
		b.Fatal(err)
	}
	mem, err := memory.Open(memory.OpenOptions{WorkspaceDir: ".", Shared: true})
	if err != nil {
		b.Fatal(err)
	}
	start := time.Now()
	for i := 0; i < b.N; i++ {
		if _, err := idx.GetProgress(ctx); err != nil {
			b.Fatal(err)
		}
		if _, err := mem.Report(ctx); err != nil {
			b.Fatal(err)
		}
		if _, err := mem.ReportIntegrity(ctx); err != nil {
			b.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	b.ReportMetric(float64(elapsed.Nanoseconds())/float64(b.N), "ns/op")
}

// BenchmarkTextareaUpdateOnly isolates the bubbles textarea cost per keystroke.
func BenchmarkTextareaUpdateOnly(b *testing.B) {
	ta := textarea.New()
	ta.SetStyles(uistyles.Theme("", "", ".").Editor.Textarea)
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 10
	ta.Focus()
	ta.SetValue("hello world this is a bench prompt")
	msg := tea.KeyPressMsg{Code: 'x', Text: "x"}
	start := time.Now()
	for i := 0; i < b.N; i++ {
		ta, _ = ta.Update(msg)
	}
	b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
}

// BenchmarkChatRenderOnly isolates the cached chat list render.
func BenchmarkChatRenderOnly(b *testing.B) {
	ui := benchUI(b)
	start := time.Now()
	for i := 0; i < b.N; i++ {
		_ = ui.chat.list.Render()
	}
	b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
}

// BenchmarkSidebarDraw isolates the sidebar section rebuild plus draw.
func BenchmarkSidebarDraw(b *testing.B) {
	ui := benchUI(b)
	scr := uv.NewScreenBuffer(ui.width, ui.height)
	area := image.Rect(0, 0, 34, ui.height)
	start := time.Now()
	for i := 0; i < b.N; i++ {
		ui.drawSidebar(scr, area)
	}
	b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
}

// BenchmarkEditorViewOnly isolates the editor view render.
func BenchmarkEditorViewOnly(b *testing.B) {
	ui := benchUI(b)
	start := time.Now()
	for i := 0; i < b.N; i++ {
		_ = ui.renderEditorView(80)
	}
	b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
}

// BenchmarkMemoryInfoFull measures the per-draw memory widget cost with fresh
// caches: the two store tallies, the integrity tally, and the transcript scan.
func BenchmarkMemoryInfoFull(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	if err := memory.EnsureVault(dir); err != nil {
		b.Fatal(err)
	}
	mem, err := memory.Open(memory.OpenOptions{WorkspaceDir: dir, Shared: true})
	if err != nil {
		b.Fatal(err)
	}
	defer mem.Close()
	// Pre-seed a few entries so the tallies are non-trivial.
	seedCtx := context.Background()
	for i := 0; i < 5; i++ {
		_ = mem.Put(seedCtx, memory.Entry{
			ID:      fmt.Sprintf("bench-%d", i),
			Type:    memory.TypeDecision,
			Summary: "bench decision",
			Body:    "body text for the bench entry",
		})
	}
	start := time.Now()
	for i := 0; i < b.N; i++ {
		_, _ = mem.Report(ctx)
		_, _ = mem.ReportIntegrity(ctx)
	}
	b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
}

// BenchmarkRealListMessages measures the per-refresh reads against the real
// on-disk session database: ListMessages (all messages of this session) plus
// the memory sources scan over them.
func BenchmarkRealListMessages(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	conn, err := db.Connect(ctx, dir)
	if err != nil {
		b.Fatal(err)
	}
	q := db.New(conn)
	if _, err := conn.ExecContext(ctx, `INSERT INTO sessions (id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at, todos, current_tokens, is_stateless, is_pinned) VALUES ("bench", "bench", 1, 0, 0, 0, 1, 1, "[]", 0, 0, 0)`); err != nil {
		b.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO messages (id, session_id, role, parts, created_at, updated_at, is_summary_message) VALUES ("m1", "bench", "user", "[]", 1, 1, 0)`); err != nil {
		b.Fatal(err)
	}
	id := "bench"
	start := time.Now()
	for i := 0; i < b.N; i++ {
		msgs, err := q.ListMessagesBySession(ctx, id)
		if err != nil {
			b.Fatal(err)
		}
		converted := make([]message.Message, 0, len(msgs))
		for _, m := range msgs {
			converted = append(converted, message.Message{
				ID:        m.ID,
				SessionID: m.SessionID,
				Role:      message.MessageRole(m.Role),
				CreatedAt: m.CreatedAt,
			})
		}
		_ = memoryui.SourcesFromMessages(converted)
	}
	b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
	db.Release(dir)
}

// BenchmarkSidebarSections attributes the per-frame sidebar cost across the
// default components, with the draw-path caches warm.
func BenchmarkSidebarSections(b *testing.B) {
	ui := benchUI(b)
	for _, id := range []string{"logo", "session_title", "working_dir", "active_llm", "goal", "files", "lsps", "mcps", "skills", "memory"} {
		cfg := config.SidebarComponentConfig{ID: id}
		b.Run(id, func(b *testing.B) {
			start := time.Now()
			for range b.N {
				_ = ui.renderSidebarComponent(cfg, 34)
			}
			b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
		})
	}
}

// BenchmarkFrameWithoutSidebar compares a full frame with the sidebar drawn
// against the same frame in compact mode (sidebar skipped), attributing the
// difference to the sidebar rebuild alone.
func BenchmarkFrameWithoutSidebar(b *testing.B) {
	full := benchUI(b)
	compact := benchUI(b)
	compact.isCompact = true
	fullStart := time.Now()
	for range b.N {
		_ = full.View()
	}
	fullNs := time.Since(fullStart).Nanoseconds()
	compactStart := time.Now()
	for range b.N {
		_ = compact.View()
	}
	compactNs := time.Since(compactStart).Nanoseconds()
	b.ReportMetric(float64(fullNs)/float64(b.N), "full_ns/op")
	b.ReportMetric(float64(compactNs)/float64(b.N), "compact_ns/op")
}

// BenchmarkStyledStringDecode isolates the ultraviolet decode cost of the
// per-frame NewStyledString allocations for the three biggest blocks.
func BenchmarkStyledStringDecode(b *testing.B) {
	ui := benchUI(b)
	sidebar := ui.renderSidebarComponent(config.SidebarComponentConfig{ID: "memory"}, 34)
	_ = ui.renderSidebarComponent(config.SidebarComponentConfig{ID: "logo"}, 34)
	scr := uv.NewScreenBuffer(ui.width, ui.height)
	area := image.Rect(0, 0, 34, ui.height)
	start := time.Now()
	for range b.N {
		uv.NewStyledString(sidebar).Draw(scr, area)
	}
	b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
}

// BenchmarkRealStoreReads times the two throttled synchronous read groups the
// chat sidebar performs inside the draw path against this workspace's real
// on-disk databases (609 MB index, live memory vault), as the app sees them.
func BenchmarkRealStoreReads(b *testing.B) {
	ctx := context.Background()
	// go test runs with the package directory (internal/ui/model) as cwd, so the
	// workspace data directory is three levels up.
	const dataDir = "../../../.phosphor"
	idx, err := workspaceindex.NewStore(dataDir)
	if err != nil {
		b.Fatal(err)
	}
	mem, err := memory.Open(memory.OpenOptions{WorkspaceDir: "../../..", Shared: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Run("progress", func(b *testing.B) {
		start := time.Now()
		for range b.N {
			if _, err := idx.GetProgress(ctx); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
	})
	b.Run("tallies", func(b *testing.B) {
		start := time.Now()
		for range b.N {
			if _, err := mem.Report(ctx); err != nil {
				b.Fatal(err)
			}
			if _, err := mem.ReportIntegrity(ctx); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
	})
}

// BenchmarkRealSessionTranscript times the transcript read plus the memory
// sources scan for the largest real session in this workspace's database,
// which is what the memory sidebar panel refresh pays every two seconds.
func BenchmarkRealSessionTranscript(b *testing.B) {
	ctx := context.Background()
	conn, err := db.Connect(ctx, "../../../.phosphor")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Release("../../../.phosphor")
	q := db.New(conn)
	sessions, err := q.ListSessions(ctx)
	if err != nil {
		b.Fatal(err)
	}
	if len(sessions) == 0 {
		b.Skip("no sessions in the local database")
	}
	biggest := sessions[0]
	for _, s := range sessions {
		if s.MessageCount > biggest.MessageCount {
			biggest = s
		}
	}
	b.ReportMetric(float64(biggest.MessageCount), "messages")
	id := biggest.ID
	start := time.Now()
	for range b.N {
		msgs, err := q.ListMessagesBySession(ctx, id)
		if err != nil {
			b.Fatal(err)
		}
		converted := make([]message.Message, 0, len(msgs))
		for _, m := range msgs {
			converted = append(converted, message.Message{
				ID:        m.ID,
				SessionID: m.SessionID,
				Role:      message.MessageRole(m.Role),
				CreatedAt: m.CreatedAt,
			})
		}
		_ = memoryui.SourcesFromMessages(converted)
	}
	b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
}

// BenchmarkRealSourcesScan splits the transcript refresh: the sources scan over
// an already-decoded transcript, which is what the draw path pays once the
// query itself is hoisted out.
func BenchmarkRealSourcesScan(b *testing.B) {
	ctx := context.Background()
	conn, err := db.Connect(ctx, "../../../.phosphor")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Release("../../../.phosphor")
	q := db.New(conn)
	id := "84ce36bf-8fa1-4085-a616-84ad8011279d"
	rows, err := q.ListMessagesBySession(ctx, id)
	if err != nil {
		b.Fatal(err)
	}
	msgs := make([]message.Message, 0, len(rows))
	for _, r := range rows {
		var m message.Message
		m.ID = r.ID
		m.SessionID = r.SessionID
		m.Role = message.MessageRole(r.Role)
		m.CreatedAt = r.CreatedAt
		msgs = append(msgs, m)
	}
	start := time.Now()
	for range b.N {
		_ = memoryui.SourcesFromMessages(msgs)
	}
	b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
}

// BenchmarkRealisticKeystroke builds the UI from this workspace's real session
// transcript plus the real memory vault and times one keystroke cycle (Update
// plus View), which is what the terminal waits on per character.
func BenchmarkRealisticKeystroke(b *testing.B) {
	ctx := context.Background()
	conn, err := db.Connect(ctx, "../../../.phosphor")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Release("../../../.phosphor")
	svc := message.NewService(db.New(conn))
	msgs, err := svc.List(ctx, "84ce36bf-8fa1-4085-a616-84ad8011279d")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(len(msgs)), "messages")

	ui := benchUI(b)
	ui.allSessionMessages = msgs
	for i := range msgs {
		for _, item := range chat.ExtractMessageItems(ui.com.Styles, &msgs[i], nil) {
			ui.chat.AppendMessages(item)
		}
	}
	ui.chat.SetSize(ui.layout.main.Dx(), ui.layout.main.Dy())

	key := tea.KeyPressMsg{Code: 'a', Text: "a"}
	start := time.Now()
	for range b.N {
		ui.Update(key)
		_ = ui.View()
	}
	b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
}

// BenchmarkRealisticRefreshFrame times a steady frame now that the memory
// panel and index progress refresh off the render path entirely (see
// fetchMemoryPanelCmd/fetchIndexProgressCmd): View never clears or refetches
// either cache, so this should track BenchmarkRealisticKeystroke rather than
// spiking on the tick boundary the old inline-refresh design used to hit.
func BenchmarkRealisticRefreshFrame(b *testing.B) {
	ctx := context.Background()
	conn, err := db.Connect(ctx, "../../../.phosphor")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Release("../../../.phosphor")
	mem, err := memory.Open(memory.OpenOptions{WorkspaceDir: "../..", Shared: true})
	if err != nil {
		b.Fatal(err)
	}
	defer mem.Close()
	svc := message.NewService(db.New(conn))
	msgs, err := svc.List(ctx, "84ce36bf-8fa1-4085-a616-84ad8011279d")
	if err != nil {
		b.Fatal(err)
	}
	ui := benchUI(b)
	ui.allSessionMessages = msgs
	for i := range msgs {
		for _, item := range chat.ExtractMessageItems(ui.com.Styles, &msgs[i], nil) {
			ui.chat.AppendMessages(item)
		}
	}
	ui.chat.SetSize(ui.layout.main.Dx(), ui.layout.main.Dy())
	_ = ui.View()
	start := time.Now()
	for range b.N {
		_ = ui.View()
	}
	b.ReportMetric(float64(time.Since(start).Nanoseconds())/float64(b.N), "ns/op")
}
