package dialog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pmEnter() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }

func pmDown() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyDown} }

func pmEsc() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEscape} }

func pmCtrlD() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 0x64, Mod: tea.ModCtrl} } // `d`

const managerFixture = `{
  "providers": {
    "gx10-vllm": {
      "name": "GB10-vllm",
      "base_url": "http://gx10-f4d4:8888/v1",
      "type": "openai-compat",
      "api_key": "sk-secret123456",
      "models": [
        {
          "id": "qwen3-coder",
          "name": "Qwen3 Coder",
          "context_window": 131072,
          "supports_attachments": true,
          "options": {
            "temperature": 0.7,
            "provider_options": { "repetition_penalty": 1.05 }
          }
        }
      ]
    },
    "ollama": {
      "base_url": "http://localhost:11434/v1",
      "models": [{ "id": "llama3", "name": "llama3" }]
    }
  }
}`

// newTestManager points both global config paths at temp dirs so the
// dialog loads only fixture providers, never the real user config.
func newTestManager(t *testing.T, content string) *ProviderManager {
	t.Helper()
	globalDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("PHOSPHOR_GLOBAL_CONFIG", globalDir)
	t.Setenv("PHOSPHOR_GLOBAL_DATA", dataDir)
	if content != "" {
		require.NoError(t, os.WriteFile(filepath.Join(globalDir, "phosphor.json"), []byte(content), 0o600))
	}
	s := styles.CharmtonePantera()
	return NewProviderManager(&common.Common{Styles: &s})
}

func TestProviderManagerLoadsOnlyRawProviders(t *testing.T) {
	m := newTestManager(t, managerFixture)

	require.Len(t, m.entries, 2)
	assert.Equal(t, "gx10-vllm", m.entries[0].id)
	assert.Equal(t, "ollama", m.entries[1].id)
	assert.Equal(t, config.ScopeGlobal, m.entries[0].scope)

	models := m.entries[0].raw["models"]
	require.NotNil(t, models, "raw models array must survive the load")
}

func TestProviderManagerEmptyState(t *testing.T) {
	m := newTestManager(t, `{}`)
	assert.Empty(t, m.entries)
}

func TestProviderManagerOpenAddFromList(t *testing.T) {
	m := newTestManager(t, managerFixture)
	assert.Equal(t, 0, m.selList) // add item pinned first

	action := m.HandleMsg(pmEnter())
	open, ok := action.(ActionOpenDialog)
	require.True(t, ok, "enter on row 0 must open the wizard, got %T", action)
	assert.Equal(t, ProviderWizardID, open.DialogID)
}

func TestProviderManagerNavigationAndDetail(t *testing.T) {
	m := newTestManager(t, managerFixture)

	// Down onto the first provider and open detail.
	m.HandleMsg(pmDown())
	require.Equal(t, 1, m.selList)
	m.HandleMsg(pmEnter())
	assert.Equal(t, pmModeDetail, m.mode)
	assert.Equal(t, "gx10-vllm", m.providerID)

	// Back returns to the list; back again closes.
	m.HandleMsg(pmEsc())
	assert.Equal(t, pmModeList, m.mode)
	m.HandleMsg(pmEsc())
	assert.Equal(t, ActionClose{}, m.HandleMsg(pmEsc()))
}

func TestProviderManagerToggleBool(t *testing.T) {
	m := newTestManager(t, managerFixture)
	m.providerID = "ollama"
	m.mode = pmModeDetail
	m.selField = 6 // disabled

	action := m.HandleMsg(pmEnter())
	upd, ok := action.(ActionUpdateProviderField)
	require.True(t, ok, "got %T", action)
	assert.Equal(t, "ollama", upd.ProviderID)
	assert.Equal(t, "disable", upd.Key)
	assert.Equal(t, true, upd.Value)

	// Flipping again emits false.
	action = m.HandleMsg(pmEnter())
	upd, ok = action.(ActionUpdateProviderField)
	require.True(t, ok)
	assert.Equal(t, false, upd.Value)
}

func TestProviderManagerCycleToolCallFormat(t *testing.T) {
	m := newTestManager(t, managerFixture)
	m.providerID = "ollama"
	m.mode = pmModeDetail
	m.selField = 4 // tool call format

	action := m.HandleMsg(pmEnter())
	upd, ok := action.(ActionUpdateProviderField)
	require.True(t, ok, "got %T", action)
	assert.Equal(t, "tool_call_format", upd.Key)
	assert.Equal(t, "native", upd.Value, "unset starts the cycle at the first option")

	action = m.HandleMsg(pmEnter())
	upd, ok = action.(ActionUpdateProviderField)
	require.True(t, ok)
	assert.Equal(t, "xml", upd.Value)
}

func TestProviderManagerEditBaseURL(t *testing.T) {
	m := newTestManager(t, managerFixture)
	m.providerID = "ollama"
	m.mode = pmModeDetail
	m.selField = 1 // base URL

	m.HandleMsg(pmEnter()) // open editor
	require.True(t, m.editing)

	m.input.SetValue("localhost:9999")
	action := m.HandleMsg(pmEnter())
	upd, ok := action.(ActionUpdateProviderField)
	require.True(t, ok, "got %T", action)
	assert.Equal(t, "base_url", upd.Key)
	assert.Equal(t, "http://localhost:9999/v1", upd.Value, "editor must normalize the URL")
	assert.False(t, m.editing)

	// Local state reflects the edit for the next redraw.
	assert.Equal(t, "http://localhost:9999/v1", m.currentEntry().raw["base_url"])
}

func TestProviderManagerClearField(t *testing.T) {
	m := newTestManager(t, managerFixture)
	m.providerID = "gx10-vllm"
	m.mode = pmModeDetail
	m.selField = 2 // api key

	m.HandleMsg(pmEnter())
	m.input.SetValue("")
	action := m.HandleMsg(pmEnter())
	upd, ok := action.(ActionUpdateProviderField)
	require.True(t, ok, "got %T", action)
	assert.Nil(t, upd.Value, "empty input must request field removal")
	_, exists := m.currentEntry().raw["api_key"]
	assert.False(t, exists)
}

func TestProviderManagerAddModel(t *testing.T) {
	m := newTestManager(t, managerFixture)
	m.providerID = "ollama"
	m.mode = pmModeDetail
	m.selField = len(providerFields) // models… row

	m.HandleMsg(pmEnter()) // into models view
	assert.Equal(t, pmModeModels, m.mode)

	m.HandleMsg(pmEnter()) // row 0: add model
	require.True(t, m.editing)
	m.input.SetValue("mistral")
	action := m.HandleMsg(pmEnter())
	upd, ok := action.(ActionUpdateProviderModels)
	require.True(t, ok, "got %T", action)
	require.Len(t, upd.Models, 2)
	assert.Equal(t, "mistral", upd.Models[1]["id"])

	// Editor jumps straight into the new model's field list.
	assert.Equal(t, pmModeModelFields, m.mode)
	assert.Equal(t, 1, m.modelIdx)
}

func TestProviderManagerEditNestedOption(t *testing.T) {
	m := newTestManager(t, managerFixture)
	m.providerID = "gx10-vllm"
	m.mode = pmModeModelFields
	m.modelIdx = 0
	m.selMF = 12 // options.temperature

	m.HandleMsg(pmEnter())
	m.input.SetValue("0.2")
	action := m.HandleMsg(pmEnter())
	upd, ok := action.(ActionUpdateProviderModels)
	require.True(t, ok, "got %T", action)

	opts, ok := upd.Models[0]["options"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 0.2, opts["temperature"])

	po, ok := opts["provider_options"].(map[string]any)
	require.True(t, ok, "untouched provider_options must survive the edit")
	assert.Equal(t, 1.05, po["repetition_penalty"])

	// The bad-input path keeps the editor open with an error.
	m.HandleMsg(pmEnter())
	m.input.SetValue("hot")
	second := m.HandleMsg(pmEnter())
	assert.Nil(t, second)
	assert.True(t, m.editing)
	assert.NotEmpty(t, m.errText)
}

func TestProviderManagerRemoveModel(t *testing.T) {
	m := newTestManager(t, managerFixture)
	m.providerID = "ollama"
	m.mode = pmModeModels
	m.selModel = 1 // llama3

	ctrlD := tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	action := m.HandleMsg(ctrlD)
	upd, ok := action.(ActionUpdateProviderModels)
	require.True(t, ok, "got %T", action)
	assert.Empty(t, upd.Models)
}

func TestProviderManagerDeleteProvider(t *testing.T) {
	m := newTestManager(t, managerFixture)
	m.selList = 1 // gx10-vllm

	m.HandleMsg(pmCtrlD())
	require.True(t, m.confirm)

	action := m.HandleMsg(pmEnter())
	del, ok := action.(ActionDeleteProvider)
	require.True(t, ok, "got %T", action)
	assert.Equal(t, "gx10-vllm", del.ProviderID)
	assert.Equal(t, config.ScopeGlobal, del.Scope)
	require.Len(t, m.entries, 1)
	assert.Equal(t, "ollama", m.entries[0].id)
}

func TestProviderManagerParseFieldValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		field pmField
		input string
		want  any
		err   bool
	}{
		{"int ok", pmField{label: "context window", kind: pmFieldInt}, "131072", int64(131072), false},
		{"int underscores ok", pmField{label: "n", kind: pmFieldInt}, "1_000", int64(1000), false},
		{"int bad", pmField{label: "n", kind: pmFieldInt}, "abc", nil, true},
		{"float ok", pmField{label: "temp", kind: pmFieldFloat}, "0.7", 0.7, false},
		{"float bad", pmField{label: "temp", kind: pmFieldFloat}, "hot", nil, true},
		{"list ok", pmField{label: "stop", kind: pmFieldList}, "a, b ,,c", []string{"a", "b", "c"}, false},
		{"json ok", pmField{label: "opts", kind: pmFieldJSON}, `{"stop":["x"]}`, map[string]any{"stop": []any{"x"}}, false},
		{"json array rejected", pmField{label: "opts", kind: pmFieldJSON}, `[1]`, nil, true},
		{"json invalid", pmField{label: "opts", kind: pmFieldJSON}, `{`, nil, true},
		{"clear optional", pmField{label: "name", kind: pmFieldText}, "", nil, false},
		{"clear required", pmField{label: "id", kind: pmFieldText, required: true}, "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseFieldValue(tc.field, tc.input, map[string]any{})
			if tc.err {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestProviderManagerNestedHelpers(t *testing.T) {
	t.Parallel()

	obj := map[string]any{}
	setNested(obj, "options.provider_options.stop", []string{"x"})
	v, ok := getNested(obj, "options.provider_options.stop")
	require.True(t, ok)
	assert.Equal(t, []string{"x"}, v)

	applyNested(obj, "options.provider_options.stop", nil)
	_, ok = getNested(obj, "options.provider_options")
	assert.False(t, ok, "empty intermediate maps must be pruned")
	_, ok = getNested(obj, "options")
	assert.False(t, ok, "pruning must cascade upward")
}

func TestProviderManagerEditorPrefillsExistingValues(t *testing.T) {
	m := newTestManager(t, managerFixture)
	m.providerID = "gx10-vllm"
	m.mode = pmModeDetail
	m.selField = 1 // base URL

	m.HandleMsg(pmEnter())
	require.True(t, m.editing)
	assert.Equal(t, "http://gx10-f4d4:8888/v1", m.input.Value(),
		"editor must show the existing value so it can be edited in place")

	// Secrets show the raw value in the editor, unlike the masked list row.
	m.editing = false
	m.selField = 2 // api key
	m.HandleMsg(pmEnter())
	require.True(t, m.editing)
	assert.Equal(t, "sk-secret123456", m.input.Value())

	// Model-level fields prefill too.
	m.editing = false
	m.mode = pmModeModelFields
	m.modelIdx = 0
	m.selMF = 12 // options.temperature
	m.HandleMsg(pmEnter())
	require.True(t, m.editing)
	assert.Equal(t, "0.7", m.input.Value())

	// Adding a brand-new model still starts with an empty editor.
	m.editing = false
	m.mode = pmModeModels
	m.selModel = 0
	m.HandleMsg(pmEnter())
	require.True(t, m.editing)
	assert.Equal(t, "", m.input.Value(), "adding a model must start with an empty editor")
}

func TestProviderManagerEditorCursorSitsOnInputRow(t *testing.T) {
	m := newTestManager(t, managerFixture)
	m.providerID = "gx10-vllm"
	m.mode = pmModeDetail
	m.selField = 1 // base URL

	m.HandleMsg(pmEnter())
	require.True(t, m.editing)

	scr := uv.NewScreenBuffer(120, 60)
	cur := m.Draw(scr, uv.Rect(0, 0, 120, 60))
	require.NotNil(t, cur, "editing must expose a terminal cursor")

	value := "http://gx10-f4d4:8888/v1"
	row := pmScreenRow(scr, cur.Y)
	assert.Contains(t, row, "base URL: "+value,
		"the cursor row must be the row that displays the typed value")

	cell := scr.CellAt(cur.X-1, cur.Y)
	require.NotNil(t, cell)
	assert.Equal(t, "1", cell.Content,
		"the cursor must sit right after the last character of the value")
}

func pmScreenRow(scr uv.ScreenBuffer, y int) string {
	var b strings.Builder
	for x := 0; x < scr.Bounds().Dx(); x++ {
		if c := scr.CellAt(x, y); c != nil {
			b.WriteString(c.Content)
		}
	}
	return b.String()
}

func TestProviderManagerViewsKeepConstantSize(t *testing.T) {
	m := newTestManager(t, managerFixture)
	area := uv.Rect(0, 0, 120, 60)

	frameRows := func(mode providerManagerMode, edit bool) (int, int, int, int) {
		m.mode = mode
		m.providerID = "gx10-vllm"
		m.modelIdx = 0
		m.editing = false
		m.adding = false
		m.errText = ""
		m.input.SetValue("")
		if edit {
			if mode == pmModeModelFields {
				m.selMF = 2 // context window (an editable field)
				m.HandleMsg(pmEnter())
			} else {
				m.selField = 1 // base URL (an editable field)
				m.HandleMsg(pmEnter())
			}
			require.True(t, m.editing, "enter must open the field editor")
		}
		scr := uv.NewScreenBuffer(120, 60)
		m.Draw(scr, area)
		first, last, minCol, maxCol := -1, -1, 1<<30, -1
		for y := 0; y < scr.Bounds().Dy(); y++ {
			for x := 0; x < scr.Bounds().Dx(); x++ {
				if c := scr.CellAt(x, y); c != nil && c.Content == "│" {
					if first < 0 {
						first = y
					}
					last = y
					minCol = min(minCol, x)
					maxCol = max(maxCol, x)
				}
			}
		}
		return first, last, minCol, maxCol
	}

	lf, ll, lc, lr := frameRows(pmModeList, false)
	require.NotEqual(t, -1, lf, "list view must render a framed dialog")
	df, dl, dc, dr := frameRows(pmModeDetail, false)
	mf, ml, mc, mr := frameRows(pmModeModels, false)
	ff, fl, fc, fr := frameRows(pmModeModelFields, false)
	dfe, dle, dce, dre := frameRows(pmModeDetail, true)
	ffe, fle, fce, fre := frameRows(pmModeModelFields, true)

	want := []int{lf, ll, lc, lr}
	assert.Equal(t, want, []int{df, dl, dc, dr}, "detail form must match the list dialog box")
	assert.Equal(t, want, []int{mf, ml, mc, mr}, "models view must match the list dialog box")
	assert.Equal(t, want, []int{ff, fl, fc, fr}, "model fields view must match the list dialog box")
	assert.Equal(t, want, []int{dfe, dle, dce, dre}, "field editor must not resize the dialog")
	assert.Equal(t, want, []int{ffe, fle, fce, fre}, "model field editor must not resize the dialog")
}

func TestProviderManagerListScrollsWithinFixedHeight(t *testing.T) {
	m := newTestManager(t, managerFixture)
	for i := 0; i < 30; i++ {
		m.entries = append(m.entries, pmEntry{
			id:  fmt.Sprintf("p%02d", i),
			raw: map[string]any{},
		})
	}
	m.selList = 25

	out := m.renderList(6)
	assert.Equal(t, 6, lipgloss.Height(out), "list must never exceed the shared body height")
	assert.Contains(t, out, "p24", "the selected entry must stay in the scrolling window")
	assert.NotContains(t, out, "p00", "entries far above the selection must scroll out")
}

func TestProviderManagerWindowHelper(t *testing.T) {
	start, end := pmWindow(30, 10, 25)
	assert.Equal(t, 20, start, "window clamps at the end of the list")
	assert.Equal(t, 30, end)

	start, end = pmWindow(5, 10, 3)
	assert.Equal(t, 0, start, "short lists render in full")
	assert.Equal(t, 5, end)

	start, end = pmWindow(30, 10, 0)
	assert.Equal(t, 0, start, "selection at the top pins the window")
	assert.Equal(t, 10, end)
}

func TestProviderManagerTitleSpacing(t *testing.T) {
	m := newTestManager(t, managerFixture)
	scr := uv.NewScreenBuffer(120, 60)
	m.Draw(scr, uv.Rect(0, 0, 120, 60))

	titleRow := -1
	for y := 0; y < scr.Bounds().Dy(); y++ {
		if strings.Contains(pmScreenRow(scr, y), "Custom Providers") {
			titleRow = y
			break
		}
	}
	require.NotEqual(t, -1, titleRow, "title must render")

	blank := strings.Trim(pmScreenRow(scr, titleRow+1), " │")
	assert.Empty(t, blank, "a blank row must separate the title bar from the first text line")

	subRow := pmScreenRow(scr, titleRow+2)
	assert.Contains(t, subRow, "configured", "the subtitle must follow the blank spacer row")
}

func TestProviderManagerMergesShadowedEntries(t *testing.T) {
	globalDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("PHOSPHOR_GLOBAL_CONFIG", globalDir)
	t.Setenv("PHOSPHOR_GLOBAL_DATA", dataDir)
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "phosphor.json"), []byte(managerFixture), 0o600))
	// A dialog write landed a sparse same-id entry in the app-managed
	// data config; it must not hide the hand-edited values.
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "phosphor.json"), []byte(
		`{"providers":{"gx10-vllm":{"disable":true}}}`), 0o600))

	s := styles.CharmtonePantera()
	m := NewProviderManager(&common.Common{Styles: &s})
	require.Len(t, m.entries, 2)

	var e *pmEntry
	for i := range m.entries {
		if m.entries[i].id == "gx10-vllm" {
			e = &m.entries[i]
		}
	}
	require.NotNil(t, e)

	url, ok := getNested(e.raw, "base_url")
	require.True(t, ok, "values only present in the hand-edited config must survive the merge")
	assert.Equal(t, "http://gx10-f4d4:8888/v1", url)
	dis, ok := getNested(e.raw, "disable")
	require.True(t, ok)
	assert.Equal(t, true, dis, "higher-precedence data-dir keys still win")
	models := modelsFromRaw(e.raw)
	require.Len(t, models, 1, "models from the hand-edited file must survive")
	assert.Equal(t, []string{config.GlobalConfig(), config.GlobalConfigData()}, e.sources)
	assert.Equal(t, config.GlobalConfigData(), e.source)
}
