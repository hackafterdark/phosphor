package dialog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/agent/parser"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/sahilm/fuzzy"
)

// LanguagesID is the identifier for the structural search languages dialog.
const LanguagesID = "languages"

const (
	languagesDialogMaxWidth  = 50
	languagesDialogMaxHeight = 14
)

// LanguageOption represents a single language entry in the dialog list.
type LanguageOption struct {
	*list.Versioned
	langID      string   // canonical language ID (e.g. "go", "typescript")
	DisplayName string   // human-readable name (e.g. "Go", "TypeScript")
	Extensions  []string // file extensions (e.g. ["*.go"])
	Templates   []string // available template names
	Checked     bool     // whether this language is enabled
	t           *styles.Styles
	m           fuzzy.Match
	cache       map[int]string
	focused     bool
}

// AllLanguageOptions returns every supported language with defaults, plus an
// "inherit from global" option as the first entry.
func AllLanguageOptions() []LanguageOption {
	var opts []LanguageOption
	opts = append(opts, LanguageOption{
		langID:      "__inherit_global__",
		DisplayName: "Inherit from global config",
		Extensions:  nil,
		Templates:   nil,
	})
	for _, lang := range parser.AllLanguageExtensions {
		opts = append(opts, LanguageOption{
			langID:      lang,
			DisplayName: parser.LanguageDisplayName(lang),
			Extensions:  parser.LanguageExtensions[lang],
			Templates:   parser.TemplateNames(lang),
		})
	}
	return opts
}

// Languages represents a multi-select checkbox dialog for structural search languages.
type Languages struct {
	com  *common.Common
	help help.Model
	list *list.FilterableList

	keyMap struct {
		Toggle   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}

	width int
}

var (
	_ Dialog   = (*Languages)(nil)
	_ ListItem = (*LanguageOption)(nil)
)

// NewLanguages creates a new languages dialog.
func NewLanguages(com *common.Common) *Languages {
	l := &Languages{
		com: com,
	}

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	l.help = h

	l.list = list.NewFilterableList()
	l.list.Focus()

	l.keyMap.Toggle = key.NewBinding(
		key.WithKeys("enter", "space", "ctrl+y"),
		key.WithHelp("enter/space", "toggle"),
	)
	l.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	l.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	l.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	l.keyMap.Close = CloseKey

	l.setItems()
	return l
}

// ID implements Dialog.
func (l *Languages) ID() string {
	return LanguagesID
}

// HandleMsg implements [Dialog].
func (l *Languages) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, l.keyMap.Close):
			return l.buildConfirmAction()
		case key.Matches(msg, l.keyMap.Previous):
			l.list.Focus()
			if l.list.IsSelectedFirst() {
				l.list.SelectLast()
				l.list.ScrollToBottom()
				break
			}
			l.list.SelectPrev()
			l.list.ScrollToSelected()
		case key.Matches(msg, l.keyMap.Next):
			l.list.Focus()
			if l.list.IsSelectedLast() {
				l.list.SelectFirst()
				l.list.ScrollToTop()
				break
			}
			l.list.SelectNext()
			l.list.ScrollToSelected()
		case key.Matches(msg, l.keyMap.Toggle):
			selectedItem := l.list.SelectedItem()
			if selectedItem == nil {
				break
			}
			langItem, ok := selectedItem.(*LanguageOption)
			if !ok {
				break
			}
			if langItem.langID == "__inherit_global__" {
				// Toggling inherit: uncheck all other languages.
				langItem.Checked = !langItem.Checked
				for _, item := range l.list.FilteredItems() {
					if other, ok := item.(*LanguageOption); ok && other.langID != "__inherit_global__" {
						other.Checked = false
						other.Bump()
					}
				}
			} else {
				// Toggling a language: uncheck inherit option.
				langItem.Checked = !langItem.Checked
				for _, item := range l.list.FilteredItems() {
					if other, ok := item.(*LanguageOption); ok && other.langID == "__inherit_global__" {
						other.Checked = false
						other.Bump()
					}
				}
			}
			langItem.Bump()
		}
	}
	return nil
}

// buildConfirmAction collects all checked language IDs and returns the
// ActionSetStructuralSearchLanguages to write them to config. If "inherit from
// global" is the only selection, Languages will be nil to signal deletion.
func (l *Languages) buildConfirmAction() ActionSetStructuralSearchLanguages {
	inheritChecked := false
	langs := make([]string, 0)
	for _, item := range l.list.FilteredItems() {
		langItem, ok := item.(*LanguageOption)
		if !ok {
			continue
		}
		if langItem.langID == "__inherit_global__" {
			inheritChecked = langItem.Checked
			continue
		}
		if langItem.Checked {
			langs = append(langs, langItem.langID)
		}
	}
	if inheritChecked && len(langs) == 0 {
		langs = nil // nil signals deletion of the key
	}
	return ActionSetStructuralSearchLanguages{
		Languages: langs,
	}
}

// Cursor returns the cursor for the dialog.
func (l *Languages) Cursor() *tea.Cursor {
	return nil
}

// Draw implements [Dialog].
func (l *Languages) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := l.com.Styles
	width := max(0, min(languagesDialogMaxWidth, area.Dx()))
	height := max(0, min(languagesDialogMaxHeight, area.Dy()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()

	l.list.SetSize(innerWidth, height-heightOffset)
	l.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)

	// Show global config state and selection/inherit status in title info.
	globalLangs := l.globalLanguageSet()
	checkedCount := 0
	inheritChecked := false
	for _, item := range l.list.FilteredItems() {
		langItem, ok := item.(*LanguageOption)
		if !ok {
			continue
		}
		if langItem.langID == "__inherit_global__" {
			inheritChecked = langItem.Checked
			continue
		}
		if langItem.Checked {
			checkedCount++
		}
	}
	if inheritChecked {
		rc.Subtitle = "inherit from global"
	} else if len(globalLangs) == 0 {
		rc.Subtitle = fmt.Sprintf("%d selected (no global filter)", checkedCount)
	} else {
		rc.Subtitle = fmt.Sprintf("%d selected | global: %s", checkedCount, strings.Join(globalLangs, ", "))
	}
	rc.Title = "Structural Search Languages"
	rc.Gap = 1

	visibleCount := len(l.list.FilteredItems())
	if l.list.Height() >= visibleCount {
		l.list.ScrollToTop()
	} else {
		l.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(l.list.Height()).Render(l.list.Render())
	rc.AddPart(listView)
	rc.Help = l.help.View(l)

	view := rc.Render()

	cur := l.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (l *Languages) ShortHelp() []key.Binding {
	return []key.Binding{
		l.keyMap.Toggle,
		l.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (l *Languages) FullHelp() [][]key.Binding {
	m := [][]key.Binding{}
	slice := []key.Binding{
		l.keyMap.Toggle,
		l.keyMap.Next,
		l.keyMap.Previous,
		l.keyMap.Close,
	}
	for i := 0; i < len(slice); i += 4 {
		end := min(i+4, len(slice))
		m = append(m, slice[i:end])
	}
	return m
}

func (l *Languages) setItems() {
	// Read only the workspace config to determine checked state.
	// Global config is shown in title info but doesn't pre-check items.
	workspaceLangs := l.workspaceLanguageSet()
	inheritFromGlobal := l.inheritsFromGlobal()

	items := make([]list.FilterableItem, 0, len(AllLanguageOptions()))
	for _, opt := range AllLanguageOptions() {
		var checked bool
		if opt.langID == "__inherit_global__" {
			checked = inheritFromGlobal
		} else {
			checked = workspaceLangs[opt.langID]
		}
		item := &LanguageOption{
			Versioned:   list.NewVersioned(),
			langID:      opt.langID,
			DisplayName: opt.DisplayName,
			Extensions:  opt.Extensions,
			Templates:   opt.Templates,
			Checked:     checked,
			t:           l.com.Styles,
		}
		items = append(items, item)
	}

	l.list.SetItems(items...)
	l.list.SetSelected(0)
	l.list.ScrollToTop()
}

// workspaceLanguageSet returns a set of language IDs from the workspace config only.
func (l *Languages) workspaceLanguageSet() map[string]bool {
	workspacePath := l.workspaceConfigPath()
	data, err := os.ReadFile(workspacePath)
	if err != nil {
		return nil
	}
	var cfg struct {
		Options struct {
			Agent struct {
				StructuralSearchLanguages []string `json:"structural_search_languages"`
			} `json:"agent"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	result := make(map[string]bool)
	for _, lang := range cfg.Options.Agent.StructuralSearchLanguages {
		result[lang] = true
	}
	return result
}

// inheritsFromGlobal returns true if the workspace config has no
// structural_search_languages key (meaning it inherits from global).
func (l *Languages) inheritsFromGlobal() bool {
	workspacePath := l.workspaceConfigPath()
	data, err := os.ReadFile(workspacePath)
	if err != nil {
		return true // no workspace config = inheriting
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return true
	}
	optionsRaw, ok := raw["options"]
	if !ok {
		return true
	}
	var options struct {
		Agent json.RawMessage `json:"agent"`
	}
	if err := json.Unmarshal(optionsRaw, &options); err != nil {
		return true
	}
	if options.Agent == nil {
		return true
	}
	var agent struct {
		Langs json.RawMessage `json:"structural_search_languages"`
	}
	if err := json.Unmarshal(options.Agent, &agent); err != nil {
		return true
	}
	return agent.Langs == nil
}

// workspaceConfigPath returns the path to the workspace phosphor.json file.
func (l *Languages) workspaceConfigPath() string {
	workingDir := l.com.Workspace.WorkingDir()
	return filepath.Join(workingDir, ".phosphor", "phosphor.json")
}

// currentLanguageSet returns a set of currently enabled language IDs.
// An empty set means "all languages" (no filter).
func (l *Languages) currentLanguageSet() map[string]bool {
	cfg := l.com.Config()
	result := make(map[string]bool)
	if cfg == nil || cfg.Options == nil || cfg.Options.Agent == nil {
		return result
	}
	for _, lang := range cfg.Options.Agent.StructuralSearchLanguages {
		result[lang] = true
	}
	return result
}

// globalLanguageSet returns a sorted list of language IDs from the global config.
func (l *Languages) globalLanguageSet() []string {
	globalPath := config.GlobalConfigData()
	data, err := os.ReadFile(globalPath)
	if err != nil {
		return nil
	}
	var cfg struct {
		Options struct {
			Agent struct {
				StructuralSearchLanguages []string `json:"structural_search_languages"`
			} `json:"agent"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	langs := cfg.Options.Agent.StructuralSearchLanguages
	if len(langs) == 0 {
		return nil
	}
	sorted := make([]string, len(langs))
	copy(sorted, langs)
	slices.Sort(sorted)
	return sorted
}

// Filter returns the filter value for the language item.
func (lo *LanguageOption) Filter() string {
	return lo.DisplayName
}

// ID implements ListItem.
func (lo *LanguageOption) ID() string {
	return lo.langID
}

// SetFocused sets the focus state of the language item.
func (lo *LanguageOption) SetFocused(focused bool) {
	if lo.focused == focused {
		return
	}
	lo.cache = nil
	lo.focused = focused
	if lo.Versioned != nil {
		lo.Bump()
	}
}

// SetMatch sets the fuzzy match for the language item.
func (lo *LanguageOption) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(lo.m, m) {
		return
	}
	lo.cache = nil
	lo.m = m
	if lo.Versioned != nil {
		lo.Bump()
	}
}

// Finished implements list.Item. Language items are render-stable outside of
// explicit SetFocused / SetMatch / Checked changes.
func (lo *LanguageOption) Finished() bool {
	return true
}

// Render returns the string representation of the language item.
func (lo *LanguageOption) Render(width int) string {
	st := ListItemStyles{
		ItemBlurred:     lo.t.Dialog.NormalItem,
		ItemFocused:     lo.t.Dialog.SelectedItem,
		InfoTextBlurred: lo.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: lo.t.Dialog.ListItem.InfoFocused,
	}

	// Inherit option has no extensions info.
	var info string
	if lo.langID == "__inherit_global__" {
		info = ""
	} else {
		info = strings.Join(lo.Extensions, ", ")
	}

	// Checkbox indicator styled with lipgloss.
	var checkbox string
	if lo.Checked {
		checkbox = lo.t.Dialog.CheckboxChecked.Render("[✓]")
	} else {
		checkbox = lo.t.Dialog.CheckboxUnchecked.Render("[ ]")
	}

	title := checkbox + " " + lo.DisplayName

	return renderItem(st, title, info, lo.focused, width, lo.cache, &lo.m)
}
