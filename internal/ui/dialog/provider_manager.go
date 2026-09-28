package dialog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/pkg/config"
)

// ProviderManagerID is the identifier for the custom provider management
// dialog.
const ProviderManagerID = "provider_manager"

// providerManagerMode tracks which view of the manager is active.
type providerManagerMode int

const (
	pmModeList providerManagerMode = iota
	pmModeDetail
	pmModeModels
	pmModeModelFields
)

// ActionDeleteProvider asks the UI to remove a custom provider from the
// config files. Source is the highest-precedence file the entry was found
// in and Sources every file carrying a piece of it; if any is a file the
// app cannot write, the UI warns the user to remove it by hand.
type ActionDeleteProvider struct {
	ProviderID string
	Scope      config.Scope
	Source     string
	Sources    []string
}

// ActionUpdateProviderField asks the UI to write a single provider-level
// field. Key is relative to the provider object (e.g. "base_url"). A nil
// Value means the field should be removed.
type ActionUpdateProviderField struct {
	ProviderID string
	Scope      config.Scope
	Key        string
	Value      any
}

// ActionUpdateProviderModels asks the UI to replace the entire models array
// of a provider. The map values preserve every key the user wrote, so
// round-tripping never drops fields the UI does not render.
type ActionUpdateProviderModels struct {
	ProviderID string
	Scope      config.Scope
	Models     []map[string]any
}

// pmEntry is one user-defined provider parsed from the raw config files.
// An entry may be split across several files (e.g. a hand-edited global
// file plus the app-managed data config the dialogs write to), so raw is
// the per-key merge of all of them and sources records every file carrying
// a piece of the entry.
type pmEntry struct {
	id      string
	scope   config.Scope
	source  string
	sources []string
	raw     map[string]any
}

// pmFieldKind drives how a field is edited and validated.
type pmFieldKind int

const (
	pmFieldText pmFieldKind = iota
	pmFieldSecret
	pmFieldURL
	pmFieldInt
	pmFieldFloat
	pmFieldBool
	pmFieldCycle
	pmFieldList
	pmFieldJSON
)

// pmField describes one editable field of a provider or model entry.
type pmField struct {
	label    string
	key      string // dotted path within the provider/model object
	kind     pmFieldKind
	options  []string // for cycle fields
	required bool
}

// providerFields are the provider-level rows in the detail view.
var providerFields = []pmField{
	{label: "display name", key: "name", kind: pmFieldText},
	{label: "base URL", key: "base_url", kind: pmFieldURL, required: true},
	{label: "api key", key: "api_key", kind: pmFieldSecret},
	{label: "type", key: "type", kind: pmFieldCycle, options: []string{
		"openai", "openai-compat", "anthropic", "gemini", "azure", "vertexai",
	}},
	{label: "tool call format", key: "tool_call_format", kind: pmFieldCycle, options: []string{"native", "xml"}},
	{label: "discover models", key: "discover_models", kind: pmFieldBool},
	{label: "disabled", key: "disable", kind: pmFieldBool},
}

// modelFields are the model-level rows. They mirror the JSON tags of
// [catwalk.Model] and [catwalk.ModelOptions], which are the only keys a
// models[] entry can carry.
var modelFields = []pmField{
	{label: "id", key: "id", kind: pmFieldText, required: true},
	{label: "name", key: "name", kind: pmFieldText},
	{label: "context window", key: "context_window", kind: pmFieldInt},
	{label: "max tokens", key: "default_max_tokens", kind: pmFieldInt},
	{label: "can reason", key: "can_reason", kind: pmFieldBool},
	{label: "supports attachments", key: "supports_attachments", kind: pmFieldBool},
	{label: "reasoning levels", key: "reasoning_levels", kind: pmFieldList},
	{label: "default reasoning effort", key: "default_reasoning_effort", kind: pmFieldText},
	{label: "cost per 1M in", key: "cost_per_1m_in", kind: pmFieldFloat},
	{label: "cost per 1M out", key: "cost_per_1m_out", kind: pmFieldFloat},
	{label: "cost per 1M cached in", key: "cost_per_1m_in_cached", kind: pmFieldFloat},
	{label: "cost per 1M cached out", key: "cost_per_1m_out_cached", kind: pmFieldFloat},
	{label: "temperature", key: "options.temperature", kind: pmFieldFloat},
	{label: "top_p", key: "options.top_p", kind: pmFieldFloat},
	{label: "top_k", key: "options.top_k", kind: pmFieldInt},
	{label: "frequency penalty", key: "options.frequency_penalty", kind: pmFieldFloat},
	{label: "presence penalty", key: "options.presence_penalty", kind: pmFieldFloat},
	{label: "stop sequences", key: "options.provider_options.stop", kind: pmFieldList},
	{label: "provider options (JSON)", key: "options.provider_options", kind: pmFieldJSON},
}

// ProviderManager lists user-defined providers and lets the user add,
// edit, and delete them, down to the per-model option level. Reads come
// from the raw config files (so unknown-but-valid keys survive), writes go
// out as actions handled by the UI model.
type ProviderManager struct {
	com   *common.Common
	width int

	mode    providerManagerMode
	entries []pmEntry

	selList  int
	selField int
	selModel int
	selMF    int

	providerID string // provider targeted by detail/models/modelFields
	pending    string // provider id awaiting delete confirmation
	modelIdx   int    // model targeted by modelFields; -1 when adding a new one
	adding     bool   // editor is creating a new model
	confirm    bool   // waiting on a delete confirmation

	editing   bool
	editField pmField

	input   textinput.Model
	errText string

	help   help.Model
	keyMap struct {
		CursorUp   key.Binding
		CursorDown key.Binding
		Select     key.Binding
		Remove     key.Binding
		Back       key.Binding
		Close      key.Binding
	}
}

var _ Dialog = (*ProviderManager)(nil)

// NewProviderManager creates the custom provider management dialog and
// loads the current set of user-defined providers.
func NewProviderManager(com *common.Common) *ProviderManager {
	m := &ProviderManager{}
	m.com = com
	m.width = 76
	m.modelIdx = -1

	m.input = newWizardInput(com, m.width-com.Styles.Dialog.View.GetHorizontalFrameSize()-6, "")

	m.help = help.New()
	m.help.Styles = com.Styles.DialogHelpStyles()

	m.keyMap.CursorUp = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑", "up"))
	m.keyMap.CursorDown = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "down"))
	m.keyMap.Select = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select"))
	m.keyMap.Remove = key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "delete"))
	m.keyMap.Back = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))
	m.keyMap.Close = CloseKey

	m.load()
	return m
}

// ID implements [Dialog].
func (m *ProviderManager) ID() string { return ProviderManagerID }

// load (re)reads user-defined providers from the global, data, and
// workspace config files. A provider counts as user-defined only when a
// raw entry exists in one of those files, which cleanly excludes merged
// built-in catalog providers.
func (m *ProviderManager) load() {
	type fileSrc struct {
		scope config.Scope
		path  string
	}
	files := []fileSrc{
		{config.ScopeGlobal, config.GlobalConfig()},
		{config.ScopeGlobal, config.GlobalConfigData()},
	}
	if m.com.Workspace != nil {
		files = append(files, fileSrc{
			config.ScopeWorkspace,
			filepath.Join(m.com.Workspace.WorkingDir(), ".phosphor", "phosphor.json"),
		})
	}

	byID := map[string]pmEntry{}
	for _, f := range files {
		for id, raw := range readRawProviders(f.path) {
			existing, seen := byID[id]
			if !seen {
				byID[id] = pmEntry{
					id:      id,
					scope:   f.scope,
					source:  f.path,
					sources: []string{f.path},
					raw:     raw,
				}
				continue
			}
			// The same provider can live in several config files: the
			// hand-edited global file and the app-managed data config the
			// dialogs write single keys into. Merge per key (higher-
			// precedence files win per key) instead of letting the last
			// file shadow the whole entry, which would hide values that
			// only exist in the user's own file.
			for k, v := range raw {
				existing.raw[k] = v
			}
			existing.source = f.path
			existing.scope = f.scope
			existing.sources = append(existing.sources, f.path)
			byID[id] = existing
		}
	}

	m.entries = make([]pmEntry, 0, len(byID))
	for _, e := range byID {
		m.entries = append(m.entries, e)
	}
	sort.Slice(m.entries, func(i, j int) bool { return m.entries[i].id < m.entries[j].id })

	m.clampSel()
}

// readRawProviders parses the providers object out of a config file
// without going through the merge pipeline, preserving every key the user
// wrote.
func readRawProviders(path string) map[string]map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		Providers map[string]json.RawMessage `json:"providers"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return nil
	}
	out := map[string]map[string]any{}
	for id, raw := range cfg.Providers {
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}
		out[id] = obj
	}
	return out
}

func (m *ProviderManager) clampSel() {
	m.selList = min(m.selList, max(0, len(m.entries)))
	m.selField = min(m.selField, max(0, len(providerFields)+2))
	m.selMF = min(m.selMF, max(0, len(modelFields)))
	if n := len(m.currentModels()) + 1; n > 0 {
		m.selModel = min(m.selModel, n-1)
	}
}

// currentEntry returns the entry targeted by the detail-family views.
func (m *ProviderManager) currentEntry() *pmEntry {
	for i := range m.entries {
		if m.entries[i].id == m.providerID {
			return &m.entries[i]
		}
	}
	return nil
}

// currentModels returns the live models array of the targeted provider.
func (m *ProviderManager) currentModels() []map[string]any {
	e := m.currentEntry()
	if e == nil {
		return nil
	}
	return modelsFromRaw(e.raw)
}

func modelsFromRaw(raw map[string]any) []map[string]any {
	arr, _ := raw["models"].([]any)
	models := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if obj, ok := item.(map[string]any); ok {
			models = append(models, obj)
		}
	}
	return models
}

// HandleMsg implements [Dialog].
func (m *ProviderManager) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.editing {
			return m.handleEditKey(msg)
		}
		switch m.mode {
		case pmModeList:
			return m.handleListKey(msg)
		case pmModeDetail:
			return m.handleDetailKey(msg)
		case pmModeModels:
			return m.handleModelsKey(msg)
		case pmModeModelFields:
			return m.handleModelFieldsKey(msg)
		}
	case tea.PasteMsg:
		if m.editing {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}
	}
	return nil
}

func (m *ProviderManager) handleListKey(msg tea.KeyPressMsg) Action {
	if m.confirm {
		switch msg.String() {
		case "y", "enter":
			idx := slices.IndexFunc(m.entries, func(e pmEntry) bool { return e.id == m.pending })
			m.confirm = false
			m.pending = ""
			if idx < 0 {
				return nil
			}
			e := m.entries[idx]
			m.entries = slices.Delete(m.entries, idx, idx+1)
			m.clampSel()
			return ActionDeleteProvider{
				ProviderID: e.id,
				Scope:      e.scope,
				Source:     e.source,
				Sources:    e.sources,
			}
		default:
			m.confirm = false
			return nil
		}
	}

	switch {
	case key.Matches(msg, m.keyMap.Close):
		return ActionClose{}
	case key.Matches(msg, m.keyMap.CursorUp):
		m.selList = max(0, m.selList-1)
	case key.Matches(msg, m.keyMap.CursorDown):
		m.selList = min(len(m.entries), m.selList+1)
	case key.Matches(msg, m.keyMap.Remove):
		if e := m.entryAt(m.selList); e != nil {
			m.pending = e.id
			m.confirm = true
		}
	case key.Matches(msg, m.keyMap.Select):
		if m.selList == 0 {
			return ActionOpenDialog{DialogID: ProviderWizardID}
		}
		if e := m.entryAt(m.selList); e != nil {
			m.providerID = e.id
			m.modelIdx = -1
			m.selField = 0
			m.mode = pmModeDetail
			m.load() // refresh from disk when re-entering the list
		}
	}
	return nil
}

// entryAt maps a list row (row 0 is the pinned add item) to an entry.
func (m *ProviderManager) entryAt(row int) *pmEntry {
	if row <= 0 || row > len(m.entries) {
		return nil
	}
	return &m.entries[row-1]
}

func (m *ProviderManager) handleDetailKey(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, m.keyMap.Back):
		m.mode = pmModeList
		m.load()
	case key.Matches(msg, m.keyMap.CursorUp):
		m.selField = max(0, m.selField-1)
	case key.Matches(msg, m.keyMap.CursorDown):
		m.selField = min(len(providerFields)+1, m.selField+1)
	case key.Matches(msg, m.keyMap.Select):
		return m.activateDetailRow()
	}
	return nil
}

func (m *ProviderManager) activateDetailRow() Action {
	e := m.currentEntry()
	if e == nil {
		m.mode = pmModeList
		return nil
	}
	if m.selField == len(providerFields) { // models submenu
		m.selModel = 0
		m.mode = pmModeModels
		return nil
	}
	if m.selField == len(providerFields)+1 { // delete provider
		m.pending = m.providerID
		m.confirm = true
		m.mode = pmModeList
		return nil
	}
	f := providerFields[m.selField]
	switch f.kind {
	case pmFieldBool:
		cur, _ := getNested(e.raw, f.key)
		next := true
		if b, ok := cur.(bool); ok {
			next = !b
		}
		setNested(e.raw, f.key, next)
		return ActionUpdateProviderField{ProviderID: e.id, Scope: e.scope, Key: f.key, Value: next}
	case pmFieldCycle:
		cur, _ := getNested(e.raw, f.key)
		curStr, _ := cur.(string)
		idx := slices.Index(f.options, curStr)
		next := f.options[(idx+1)%len(f.options)]
		setNested(e.raw, f.key, next)
		return ActionUpdateProviderField{ProviderID: e.id, Scope: e.scope, Key: f.key, Value: next}
	default:
		m.openEditor(f, false)
	}
	return nil
}

func (m *ProviderManager) handleModelsKey(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, m.keyMap.Back):
		m.mode = pmModeDetail
	case key.Matches(msg, m.keyMap.CursorUp):
		m.selModel = max(0, m.selModel-1)
	case key.Matches(msg, m.keyMap.CursorDown):
		m.selModel = min(len(m.currentModels()), m.selModel+1)
	case key.Matches(msg, m.keyMap.Select):
		if m.selModel == 0 {
			m.openEditor(modelFields[0], true) // add new model: prompt for id
			return nil
		}
		m.modelIdx = m.selModel - 1
		m.selMF = 0
		m.mode = pmModeModelFields
	case key.Matches(msg, m.keyMap.Remove):
		if m.selModel == 0 {
			return nil
		}
		models := m.currentModels()
		idx := m.selModel - 1
		if idx >= len(models) {
			return nil
		}
		e := m.currentEntry()
		cloned := cloneModels(models)
		cloned = slices.Delete(cloned, idx, idx+1)
		e.raw["models"] = toAnySlice(cloned)
		m.clampSel()
		return ActionUpdateProviderModels{ProviderID: e.id, Scope: e.scope, Models: cloned}
	}
	return nil
}

func (m *ProviderManager) handleModelFieldsKey(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, m.keyMap.Back):
		m.modelIdx = -1
		m.mode = pmModeModels
	case key.Matches(msg, m.keyMap.CursorUp):
		m.selMF = max(0, m.selMF-1)
	case key.Matches(msg, m.keyMap.CursorDown):
		m.selMF = min(len(modelFields)-1, m.selMF+1)
	case key.Matches(msg, m.keyMap.Select):
		models := m.currentModels()
		if m.modelIdx < 0 || m.modelIdx >= len(models) {
			return nil
		}
		f := modelFields[m.selMF]
		model := models[m.modelIdx]
		switch f.kind {
		case pmFieldBool:
			cur, _ := getNested(model, f.key)
			next := true
			if b, ok := cur.(bool); ok {
				next = !b
			}
			setNested(model, f.key, next)
			return m.commitModels()
		case pmFieldCycle:
			cur, _ := getNested(model, f.key)
			curStr, _ := cur.(string)
			idx := max(slices.Index(f.options, curStr), -1)
			next := f.options[(idx+1)%len(f.options)]
			setNested(model, f.key, next)
			return m.commitModels()
		default:
			m.openEditor(f, false)
		}
	}
	return nil
}

// commitModels emits a whole-array replace for the targeted provider's
// models, working on a deep clone so the action snapshot is stable.
func (m *ProviderManager) commitModels() Action {
	e := m.currentEntry()
	if e == nil {
		return nil
	}
	cloned := cloneModels(m.currentModels())
	e.raw["models"] = toAnySlice(cloned)
	return ActionUpdateProviderModels{ProviderID: e.id, Scope: e.scope, Models: cloned}
}

func (m *ProviderManager) openEditor(f pmField, adding bool) {
	m.editing = true
	m.adding = adding
	m.editField = f
	m.errText = ""
	initial := ""
	if !adding {
		if target := m.editTarget(); target != nil {
			// Show the real value in the editor, even for secrets, so the
			// user can edit it in place with backspace/arrow keys.
			editable := f
			editable.kind = pmFieldText
			initial = displayValue(target, editable)
		}
	}
	m.input.SetValue(initial)
	m.input.Placeholder = editorPlaceholder(f)
	m.input.Prompt = f.label + ": "
	// Size the value area so prompt + value + InputPrompt margins never
	// exceed the dialog interior; otherwise lipgloss widens the whole box
	// and the dialog resizes when entering edit mode.
	promptW := lipgloss.Width(m.input.Prompt)
	m.input.SetWidth(m.editorValueWidth(promptW))
	m.input.Focus()
}

// editorValueWidth returns the textinput value-area width that keeps the
// full input row inside the dialog interior for a prompt of the given
// width.
func (m *ProviderManager) editorValueWidth(promptWidth int) int {
	inner := m.bodyWidth()
	frame := m.com.Styles.Dialog.InputPrompt.GetHorizontalFrameSize()
	return max(8, inner-1-frame-promptWidth)
}

// editTarget returns the raw object (provider or model) whose field is
// currently being edited, or nil when it no longer exists.
func (m *ProviderManager) editTarget() map[string]any {
	e := m.currentEntry()
	if e == nil {
		return nil
	}
	if m.mode == pmModeModelFields {
		models := m.currentModels()
		if m.modelIdx < 0 || m.modelIdx >= len(models) {
			return nil
		}
		return models[m.modelIdx]
	}
	return e.raw
}

func editorPlaceholder(f pmField) string {
	switch f.kind {
	case pmFieldURL:
		return "http://localhost:11434/v1"
	case pmFieldInt:
		return "e.g. 131072 (empty to clear)"
	case pmFieldFloat:
		return "e.g. 0.7 (empty to clear)"
	case pmFieldList:
		return "comma separated (empty to clear)"
	case pmFieldJSON:
		return `{"key": "value"} (empty to clear)`
	default:
		return "(empty to clear)"
	}
}

func (m *ProviderManager) handleEditKey(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, m.keyMap.Select):
		return m.commitEdit()
	case key.Matches(msg, m.keyMap.Back):
		m.editing = false
		m.errText = ""
		return nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.errText = ""
		if cmd != nil {
			return ActionCmd{cmd}
		}
	}
	return nil
}

// commitEdit validates the editor value, applies it locally, and emits the
// corresponding write action.
func (m *ProviderManager) commitEdit() Action {
	f := m.editField
	raw := strings.TrimSpace(m.input.Value())

	e := m.currentEntry()
	if e == nil {
		m.editing = false
		return nil
	}

	// Creating a new model: the id field drives the whole add flow.
	if m.adding {
		if raw == "" {
			m.errText = "Model id can't be empty."
			return nil
		}
		if slices.ContainsFunc(m.currentModels(), func(md map[string]any) bool {
			id, _ := md["id"].(string)
			return id == raw
		}) {
			m.errText = fmt.Sprintf("Model %q already exists.", raw)
			return nil
		}
		models := append(cloneModels(m.currentModels()), map[string]any{"id": raw, "name": raw})
		e.raw["models"] = toAnySlice(models)
		m.adding = false
		m.editing = false
		m.modelIdx = len(models) - 1
		m.selModel = m.modelIdx + 1
		m.selMF = 0
		m.mode = pmModeModelFields
		return ActionUpdateProviderModels{ProviderID: e.id, Scope: e.scope, Models: models}
	}

	target := m.editTarget()
	if target == nil {
		m.editing = false
		return nil
	}

	parsed, err := parseFieldValue(f, raw, target)
	if err != nil {
		m.errText = err.Error()
		return nil
	}

	applyNested(target, f.key, parsed)

	var action Action
	if m.mode == pmModeModelFields {
		action = m.commitModels()
	} else {
		action = ActionUpdateProviderField{ProviderID: e.id, Scope: e.scope, Key: f.key, Value: parsed}
	}
	m.editing = false
	return action
}

// parseFieldValue converts editor text to the JSON value for a field.
// Empty input yields nil, meaning "remove this key".
func parseFieldValue(f pmField, raw string, target map[string]any) (any, error) {
	if raw == "" {
		if f.required {
			return nil, fmt.Errorf("%s can't be empty.", f.label)
		}
		return nil, nil
	}
	switch f.kind {
	case pmFieldInt:
		v, err := strconv.ParseInt(strings.ReplaceAll(raw, "_", ""), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a whole number.", f.label)
		}
		return v, nil
	case pmFieldFloat:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a number.", f.label)
		}
		return v, nil
	case pmFieldURL:
		v, err := normalizeBaseURL(raw)
		if err != nil {
			return nil, err
		}
		return v, nil
	case pmFieldList:
		var out []string
		for part := range strings.SplitSeq(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
		if len(out) == 0 && f.required {
			return nil, fmt.Errorf("%s can't be empty.", f.label)
		}
		return out, nil
	case pmFieldJSON:
		if !json.Valid([]byte(raw)) {
			return nil, fmt.Errorf("not valid JSON.")
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			return nil, fmt.Errorf("must be a JSON object, e.g. {\"key\": \"value\"}.")
		}
		return obj, nil
	case pmFieldBool:
		switch strings.ToLower(raw) {
		case "true", "yes", "y", "on":
			return true, nil
		case "false", "no", "n", "off":
			return false, nil
		}
		return nil, fmt.Errorf("%s must be true or false.", f.label)
	default:
		return raw, nil
	}
}

// ---- dotted-path helpers over the raw JSON maps -------------------------

func getNested(obj map[string]any, path string) (any, bool) {
	var cur any = obj
	for seg := range strings.SplitSeq(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func setNested(obj map[string]any, path string, value any) {
	segs := strings.Split(path, ".")
	var cur any = obj
	for _, seg := range segs[:len(segs)-1] {
		next, _ := cur.(map[string]any)[seg]
		m, ok := next.(map[string]any)
		if !ok {
			m = map[string]any{}
			cur.(map[string]any)[seg] = m
		}
		cur = m
	}
	cur.(map[string]any)[segs[len(segs)-1]] = value
}

// applyNested sets or deletes a key, pruning intermediate maps that
// become empty so the saved JSON stays clean.
func applyNested(obj map[string]any, path string, value any) {
	if value != nil {
		setNested(obj, path, value)
		return
	}
	segs := strings.Split(path, ".")
	var chain []map[string]any
	var cur any = obj
	for _, seg := range segs[:len(segs)-1] {
		m, ok := cur.(map[string]any)
		if !ok {
			return
		}
		next, ok := m[seg].(map[string]any)
		if !ok {
			return
		}
		chain = append(chain, m)
		cur = next
	}
	last, ok := cur.(map[string]any)
	if !ok {
		return
	}
	delete(last, segs[len(segs)-1])
	for i := len(chain) - 1; i >= 0; i-- {
		child, ok := chain[i][segs[i]].(map[string]any)
		if !ok || len(child) > 0 {
			break
		}
		delete(chain[i], segs[i])
	}
}

func cloneModels(models []map[string]any) []map[string]any {
	if len(models) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(models))
	for _, md := range models {
		blob, err := json.Marshal(md)
		if err != nil {
			continue
		}
		var cp map[string]any
		if json.Unmarshal(blob, &cp) != nil {
			continue
		}
		out = append(out, cp)
	}
	return out
}

func toAnySlice(models []map[string]any) []any {
	out := make([]any, 0, len(models))
	for _, md := range models {
		out = append(out, md)
	}
	return out
}

// ---- rendering ----------------------------------------------------------

func (m *ProviderManager) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles
	rc := NewRenderContext(t, m.width)
	rc.Gap = 1
	rc.TitleGap = 1

	switch m.mode {
	case pmModeList:
		rc.Title = "Custom Providers"
		rc.Subtitle = fmt.Sprintf("%d configured · enter adds · %s",
			len(m.entries), config.GlobalConfig())
	case pmModeDetail:
		rc.Title = "Provider: " + m.providerID
		rc.Subtitle = "enter edits, toggles flip in place"
	case pmModeModels:
		rc.Title = "Models: " + m.providerID
		rc.Subtitle = "first entry adds a model · ctrl+d removes one"
	case pmModeModelFields:
		models := m.currentModels()
		name := "?"
		if m.modelIdx >= 0 && m.modelIdx < len(models) {
			id, _ := models[m.modelIdx]["id"].(string)
			name = id
		}
		rc.Title = "Model: " + name
		rc.Subtitle = "keys mirror catwalk.Model; empty clears a field"
	}
	helpView := m.help.View(m)
	innerW := m.width - rc.ViewStyle.GetHorizontalFrameSize()
	// The subtitle renders through SecondaryText (which adds horizontal
	// padding), so its usable width is smaller than the dialog interior.
	rc.Subtitle = ansi.Truncate(
		rc.Subtitle,
		max(0, innerW-1-t.Dialog.SecondaryText.GetHorizontalFrameSize()),
		"…")
	rc.Help = helpView
	bodyH := m.bodyHeight(rc, area)
	rc.Help = ""

	switch m.mode {
	case pmModeList:
		rc.AddPart(m.renderList(bodyH))
	case pmModeDetail:
		rc.AddPart(m.renderFields(bodyH))
	case pmModeModels:
		rc.AddPart(m.renderModels(bodyH))
	case pmModeModelFields:
		rc.AddPart(m.renderModelFields(bodyH))
	}

	if m.errText != "" {
		rc.AddPart(ansi.Truncate(
			t.Dialog.TitleError.Render(m.errText),
			max(0, m.bodyWidth()-1), "…"))
	}

	var cur *tea.Cursor
	if m.editing {
		// The editor is the last content part, so measure the rendered
		// lines above it instead of assuming a fixed offset under the
		// title (which is what InputCursor assumes).
		dialogStyle := t.Dialog.View
		above := lipgloss.Height(rc.Render()) -
			dialogStyle.GetMarginBottom() -
			dialogStyle.GetBorderBottomSize() -
			dialogStyle.GetPaddingBottom()
		gap := 0
		if len(rc.Parts) > 0 {
			gap = rc.Gap
		}
		rc.AddPart(t.Dialog.InputPrompt.Render(m.input.View()))
		cur = m.editorCursor(above + gap)
	}

	rc.Help = helpView
	view := rc.Render()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// bodyHeight returns the content height shared by every manager view so
// switching views never resizes the dialog. It targets the tallest static
// view and shrinks (for all views at once) when the terminal is small;
// lists that overflow scroll around the selection instead of growing.
// The editor and error rows are carved out of that same budget, so opening
// a field editor never changes the overall dialog height.
func (m *ProviderManager) bodyHeight(rc *RenderContext, area uv.Rectangle) int {
	skel := *rc
	skel.Parts = nil
	body := min(len(modelFields), area.Dy()-lipgloss.Height(skel.Render()))
	if m.errText != "" {
		body -= lipgloss.Height(m.com.Styles.Dialog.TitleError.Render(m.errText)) + rc.Gap
	}
	if m.editing {
		body -= lipgloss.Height(m.com.Styles.Dialog.InputPrompt.Render(m.input.View())) + rc.Gap
	}
	return max(1, body)
}

// pmWindow returns a [start, end) range of at most h rows out of total,
// keeping sel visible and roughly centered.
func pmWindow(total, h, sel int) (int, int) {
	if h <= 0 {
		return 0, 0
	}
	if total <= h {
		return 0, total
	}
	if sel < 0 {
		sel = 0
	}
	start := max(0, min(sel-h/2, total-h))
	return start, start + h
}

// padToHeight pads content with blank rows, or drops overflow rows, so the
// rendered part is exactly h lines tall. Each line is truncated to width so
// styled content never soft-wraps and breaks the fixed dialog height.
func padToHeight(s string, h, width int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, max(0, width-1), "…")
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func (m *ProviderManager) bodyWidth() int {
	return m.width - m.com.Styles.Dialog.View.GetHorizontalFrameSize()
}

func (m *ProviderManager) renderList(h int) string {
	t := m.com.Styles
	if m.confirm {
		target := m.pending
		if target == "" {
			target = "?"
		}
		return padToHeight(t.Dialog.TitleError.Render(
			fmt.Sprintf("Delete provider %q? (y to confirm, any other key to cancel)", target)), h, m.bodyWidth())
	}
	addLabel := "+ Add custom provider…"
	var lines []string
	if m.selList == 0 {
		lines = append(lines, "▸ "+t.Dialog.TitleAccent.Render(addLabel))
	} else {
		lines = append(lines, "  "+t.Dialog.SecondaryText.Render(addLabel))
	}
	if len(m.entries) == 0 {
		lines = append(lines, "  "+t.Dialog.SecondaryText.Render("(no custom providers yet)"))
		return padToHeight(strings.Join(lines, "\n"), h, m.bodyWidth())
	}
	start, end := pmWindow(len(m.entries), h-1, m.selList-1)
	for i := start; i < end; i++ {
		e := m.entries[i]
		selected := m.selList == i+1
		marker, style := "  ", t.Dialog.SecondaryText
		if selected {
			marker, style = "▸ ", t.Dialog.PrimaryText
		}
		count := len(modelsFromRaw(e.raw))
		url, _ := getNested(e.raw, "base_url")
		urlStr, _ := url.(string)
		tag := "global"
		if e.scope == config.ScopeWorkspace {
			tag = "workspace"
		}
		line := fmt.Sprintf("%-24s %-42s %2d models [%s]",
			ansi.Truncate(e.id, 24, "…"), ansi.Truncate(urlStr, 42, "…"), count, tag)
		lines = append(lines, marker+style.Render(line))
	}
	return padToHeight(strings.Join(lines, "\n"), h, m.bodyWidth())
}

func (m *ProviderManager) renderFields(h int) string {
	t := m.com.Styles
	e := m.currentEntry()
	if e == nil {
		return padToHeight(t.Dialog.SecondaryText.Render("(provider is gone)"), h, m.bodyWidth())
	}
	var lines []string
	for i, f := range providerFields {
		sel := m.selField == i
		val := displayValue(e.raw, f)
		lines = append(lines, fieldLine(t, sel, f.label, val))
	}
	count := len(modelsFromRaw(e.raw))
	lines = append(lines, fieldLine(t, m.selField == len(providerFields), "models…", fmt.Sprintf("%d", count)))
	lines = append(lines, fieldLine(t, m.selField == len(providerFields)+1, "delete provider", ""))
	return padToHeight(strings.Join(lines, "\n"), h, m.bodyWidth())
}

func (m *ProviderManager) renderModels(h int) string {
	t := m.com.Styles
	var lines []string
	if m.selModel == 0 {
		lines = append(lines, "▸ "+t.Dialog.TitleAccent.Render("+ Add model…"))
	} else {
		lines = append(lines, "  "+t.Dialog.SecondaryText.Render("+ Add model…"))
	}
	models := m.currentModels()
	start, end := pmWindow(len(models), h-1, m.selModel-1)
	for i := start; i < end; i++ {
		md := models[i]
		selected := m.selModel == i+1
		marker, style := "  ", t.Dialog.SecondaryText
		if selected {
			marker, style = "▸ ", t.Dialog.PrimaryText
		}
		id, _ := md["id"].(string)
		cw, _ := getNested(md, "context_window")
		suffix := ""
		if v, ok := cw.(float64); ok && v > 0 {
			suffix = fmt.Sprintf("  (%s ctx)", strconv.FormatInt(int64(v), 10))
		}
		lines = append(lines, marker+style.Render(ansi.Truncate(id, 60, "…")+suffix))
	}
	return padToHeight(strings.Join(lines, "\n"), h, m.bodyWidth())
}

func (m *ProviderManager) renderModelFields(h int) string {
	t := m.com.Styles
	models := m.currentModels()
	if m.modelIdx < 0 || m.modelIdx >= len(models) {
		return padToHeight(t.Dialog.SecondaryText.Render("(model is gone)"), h, m.bodyWidth())
	}
	model := models[m.modelIdx]
	var lines []string
	start, end := pmWindow(len(modelFields), h, m.selMF)
	for i := start; i < end; i++ {
		f := modelFields[i]
		val := displayValue(model, f)
		lines = append(lines, fieldLine(t, m.selMF == i, f.label, val))
	}
	return padToHeight(strings.Join(lines, "\n"), h, m.bodyWidth())
}

func fieldLine(t *styles.Styles, sel bool, label, value string) string {
	marker, labelStyle, valueStyle := "  ", t.Dialog.SecondaryText, t.Dialog.TitleAccent
	if sel {
		marker, labelStyle, valueStyle = "▸ ", t.Dialog.PrimaryText, t.Dialog.TitleAccent
	}
	if value == "" {
		return marker + labelStyle.Render(label)
	}
	return marker + labelStyle.Render(fmt.Sprintf("%-26s", label)) + valueStyle.Render(ansi.Truncate(value, 40, "…"))
}

// displayValue formats the current value of a field for rendering.
func displayValue(obj map[string]any, f pmField) string {
	v, ok := getNested(obj, f.key)
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case bool:
		if val {
			return "true"
		}
		return "false"
	case string:
		if f.kind == pmFieldSecret && len(val) > 8 && !strings.HasPrefix(val, "$") {
			return "••••" + val[len(val)-4:]
		}
		return val
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'g', -1, 64)
	case []any:
		parts := make([]string, 0, len(val))
		for _, item := range val {
			s, _ := item.(string)
			parts = append(parts, s)
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		blob, err := json.Marshal(val)
		if err != nil {
			return ""
		}
		return string(blob)
	}
	return fmt.Sprintf("%v", v)
}

// editorCursor positions the terminal cursor inside the editor input row.
// above is the measured number of rendered lines that appear before the
// input part, including the dialog's top frame.
func (m *ProviderManager) editorCursor(above int) *tea.Cursor {
	cur := m.input.Cursor()
	if cur == nil {
		return nil
	}
	t := m.com.Styles
	dialogStyle := t.Dialog.View
	inputStyle := t.Dialog.InputPrompt
	cur.X += dialogStyle.GetMarginLeft() +
		dialogStyle.GetBorderLeftSize() +
		dialogStyle.GetPaddingLeft() +
		inputStyle.GetMarginLeft() +
		inputStyle.GetBorderLeftSize() +
		inputStyle.GetPaddingLeft()
	cur.Y += above +
		inputStyle.GetMarginTop() +
		inputStyle.GetBorderTopSize() +
		inputStyle.GetPaddingTop()
	return cur
}

// FullHelp implements the help.Item interface.
func (m *ProviderManager) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

// ShortHelp implements the help.Item interface.
func (m *ProviderManager) ShortHelp() []key.Binding {
	if m.editing {
		return []key.Binding{m.keyMap.Select, m.keyMap.Back}
	}
	switch m.mode {
	case pmModeList:
		return []key.Binding{m.keyMap.CursorUp, m.keyMap.CursorDown, m.keyMap.Select, m.keyMap.Remove, m.keyMap.Close}
	case pmModeModels:
		return []key.Binding{m.keyMap.CursorUp, m.keyMap.CursorDown, m.keyMap.Select, m.keyMap.Remove, m.keyMap.Back}
	default:
		return []key.Binding{m.keyMap.CursorUp, m.keyMap.CursorDown, m.keyMap.Select, m.keyMap.Back}
	}
}
