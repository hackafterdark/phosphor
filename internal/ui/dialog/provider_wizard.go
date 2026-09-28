package dialog

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/pkg/config"
)

// ProviderWizardID is the identifier for the provider setup wizard dialog.
const ProviderWizardID = "provider_wizard"

// providerWizardStep tracks the position in the wizard flow.
type providerWizardStep int

const (
	providerWizardStepName providerWizardStep = iota
	providerWizardStepURL
	providerWizardStepKey
	providerWizardStepModels
)

// providerIDPattern is the accepted character set for a provider id. Dots
// are deliberately excluded because config writes use dotted key paths
// (providers.<id>.base_url).
var providerIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ActionSetupProvider is returned by the wizard when the user finishes
// the flow. The caller is responsible for persisting the provider to the
// global config and closing the dialog.
type ActionSetupProvider struct {
	ProviderID string
	BaseURL    string
	APIKey     string
	Models     []string
}

// ProviderWizard is a small linear wizard that collects an
// OpenAI-compatible endpoint (e.g. llama.cpp, vLLM, Ollama, LM Studio),
// an optional API key, and a list of model ids, then hands everything to
// the UI model to write to the global config as a custom provider.
type ProviderWizard struct {
	com   *common.Common
	width int
	step  providerWizardStep

	providerID string
	baseURL    string
	apiKey     string
	models     []string

	errText string

	nameInput  textinput.Model
	urlInput   textinput.Model
	keyInput   textinput.Model
	modelInput textinput.Model

	help   help.Model
	keyMap struct {
		Submit key.Binding
		Remove key.Binding
		Close  key.Binding
	}
}

var _ Dialog = (*ProviderWizard)(nil)

// NewProviderWizard creates a new provider setup wizard dialog.
func NewProviderWizard(com *common.Common) *ProviderWizard {
	m := &ProviderWizard{}
	m.com = com
	m.width = 60

	innerWidth := m.width - com.Styles.Dialog.View.GetHorizontalFrameSize() - 2 // (1) cursor padding

	m.nameInput = newWizardInput(com, innerWidth, "e.g. ollama")
	m.urlInput = newWizardInput(com, innerWidth, "http://localhost:11434/v1")
	m.keyInput = newWizardInput(com, innerWidth, "leave empty if the server doesn't need one")
	m.modelInput = newWizardInput(com, innerWidth, "e.g. qwen3-32b")
	m.nameInput.Focus()

	m.help = help.New()
	m.help.Styles = com.Styles.DialogHelpStyles()

	m.keyMap.Submit = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "next"),
	)
	m.keyMap.Remove = key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "remove last model"),
	)
	m.keyMap.Close = CloseKey

	return m
}

func newWizardInput(com *common.Common, width int, placeholder string) textinput.Model {
	input := textinput.New()
	input.SetVirtualCursor(false)
	input.Placeholder = placeholder
	input.SetStyles(com.Styles.TextInput)
	input.Prompt = "> "
	input.SetWidth(max(0, width-com.Styles.Dialog.InputPrompt.GetHorizontalFrameSize()-1))
	return input
}

// ID implements Dialog.
func (m *ProviderWizard) ID() string {
	return ProviderWizardID
}

// HandleMsg implements [Dialog].
func (m *ProviderWizard) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, m.keyMap.Remove):
			if m.step == providerWizardStepModels && len(m.models) > 0 {
				m.models = m.models[:len(m.models)-1]
			}
			return nil
		case key.Matches(msg, m.keyMap.Submit):
			return m.advance()
		default:
			// Backspace on an empty input steps back a screen.
			input := m.currentInput()
			if msg.String() == "backspace" && input.Value() == "" && m.step > providerWizardStepName {
				m.step--
				m.errText = ""
				m.focusStep()
				return nil
			}
			var cmd tea.Cmd
			*input, cmd = input.Update(msg)
			m.errText = ""
			if cmd != nil {
				return ActionCmd{cmd}
			}
		}
	case tea.PasteMsg:
		input := m.currentInput()
		var cmd tea.Cmd
		*input, cmd = input.Update(msg)
		if cmd != nil {
			return ActionCmd{cmd}
		}
	}
	return nil
}

func (m *ProviderWizard) currentInput() *textinput.Model {
	switch m.step {
	case providerWizardStepName:
		return &m.nameInput
	case providerWizardStepURL:
		return &m.urlInput
	case providerWizardStepKey:
		return &m.keyInput
	default:
		return &m.modelInput
	}
}

func (m *ProviderWizard) focusStep() {
	m.nameInput.Blur()
	m.urlInput.Blur()
	m.keyInput.Blur()
	m.modelInput.Blur()
	m.currentInput().Focus()
}

// advance validates the current step and moves to the next, or emits the
// save action once at least one model exists on the final step.
func (m *ProviderWizard) advance() Action {
	switch m.step {
	case providerWizardStepName:
		id := strings.TrimSpace(m.nameInput.Value())
		if id == "" {
			m.errText = "Provider name can't be empty."
			return nil
		}
		if !providerIDPattern.MatchString(id) {
			m.errText = "Use only letters, numbers, dashes, and underscores (no dots or spaces)."
			return nil
		}
		if m.providerExists(id) {
			m.errText = fmt.Sprintf("A provider named %q already exists in your config. Pick another name.", id)
			return nil
		}
		m.providerID = id
		m.step = providerWizardStepURL
		m.focusStep()
	case providerWizardStepURL:
		normalized, err := normalizeBaseURL(strings.TrimSpace(m.urlInput.Value()))
		if err != nil {
			m.errText = err.Error()
			return nil
		}
		m.baseURL = normalized
		m.step = providerWizardStepKey
		m.focusStep()
	case providerWizardStepKey:
		m.apiKey = strings.TrimSpace(m.keyInput.Value())
		m.step = providerWizardStepModels
		m.focusStep()
	case providerWizardStepModels:
		value := strings.TrimSpace(m.modelInput.Value())
		if value != "" {
			if !slices.Contains(m.models, value) {
				m.models = append(m.models, value)
			}
			m.modelInput.SetValue("")
			return nil
		}
		if len(m.models) == 0 {
			m.errText = "Add at least one model id (press enter after typing it)."
			return nil
		}
		return ActionSetupProvider{
			ProviderID: m.providerID,
			BaseURL:    m.baseURL,
			APIKey:     m.apiKey,
			Models:     slices.Clone(m.models),
		}
	}
	return nil
}

// providerExists reports whether a provider with this id is already
// configured, so the wizard never silently clobbers an existing entry.
func (m *ProviderWizard) providerExists(id string) bool {
	cfg := m.com.Config()
	if cfg == nil || cfg.Providers == nil {
		return false
	}
	_, exists := cfg.Providers.Get(id)
	return exists
}

// normalizeBaseURL coerces user input into a usable OpenAI-compatible
// endpoint: it adds a missing scheme and appends /v1 when no path was
// given, matching the convention every supported server follows.
func normalizeBaseURL(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("URL can't be empty (e.g. http://localhost:11434/v1)")
	}
	candidate := raw
	if !strings.Contains(candidate, "://") {
		candidate = "http://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("not a valid URL: %s", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("URL scheme must be http or https")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/v1"
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

// Draw implements [Dialog].
func (m *ProviderWizard) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles

	dialogStyle := t.Dialog.View.Width(m.width)
	inputStyle := t.Dialog.InputPrompt
	textStyle := t.Dialog.SecondaryText
	helpStyle := t.Dialog.HelpView
	helpStyle = helpStyle.Width(m.width - dialogStyle.GetHorizontalFrameSize())

	lines := []string{
		m.headerView(),
		inputStyle.Render(m.inputView()),
	}
	if m.errText != "" {
		lines = append(lines, t.Dialog.TitleError.Render(m.errText))
	}
	lines = append(lines, m.stepLines()...)
	lines = append(lines,
		textStyle.Render(fmt.Sprintf("Step %d of 4 · written to %s", m.step+1, config.GlobalConfigData())),
		"",
		helpStyle.Render(m.help.View(m)),
	)

	view := dialogStyle.Render(strings.Join(lines, "\n"))
	cur := m.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// stepLines renders the running summary of everything collected so far so
// the user always sees the full picture, including the model list.
func (m *ProviderWizard) stepLines() []string {
	t := m.com.Styles
	var lines []string
	if m.providerID != "" {
		lines = append(lines, t.Dialog.SecondaryText.Render("provider: "+m.providerID))
	}
	if m.baseURL != "" {
		lines = append(lines, t.Dialog.SecondaryText.Render("url:      "+m.baseURL))
	}
	if m.step == providerWizardStepModels {
		if len(m.models) == 0 {
			lines = append(lines, t.Dialog.SecondaryText.Render("models:   (none yet)"))
		} else {
			lines = append(lines, t.Dialog.SecondaryText.Render("models:   "+strings.Join(m.models, ", ")))
		}
	}
	return lines
}

func (m *ProviderWizard) headerView() string {
	t := m.com.Styles
	titleStyle := t.Dialog.Title
	dialogStyle := t.Dialog.View.Width(m.width)
	textStyle := t.Dialog.PrimaryText
	accentStyle := t.Dialog.TitleAccent

	var title string
	switch m.step {
	case providerWizardStepName:
		title = textStyle.Render("Name your ") + accentStyle.Render("Provider") + textStyle.Render(".")
	case providerWizardStepURL:
		title = textStyle.Render("Enter the ") + accentStyle.Render("Base URL") + textStyle.Render(".")
	case providerWizardStepKey:
		title = textStyle.Render("Enter the ") + accentStyle.Render("API Key") + textStyle.Render(" (optional).")
	case providerWizardStepModels:
		title = textStyle.Render("Add ") + accentStyle.Render("Model IDs") + textStyle.Render(".")
	}

	headerOffset := titleStyle.GetHorizontalFrameSize() + dialogStyle.GetHorizontalFrameSize()
	return common.DialogTitle(t, titleStyle.Render(title), m.width-headerOffset, t.Dialog.TitleGradFromColor, t.Dialog.TitleGradToColor)
}

func (m *ProviderWizard) inputView() string {
	return m.currentInput().View()
}

// Cursor returns the cursor position relative to the dialog.
func (m *ProviderWizard) Cursor() *tea.Cursor {
	return InputCursor(m.com.Styles, m.currentInput().Cursor())
}

// FullHelp returns the full help view.
func (m *ProviderWizard) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.ShortHelp()}
}

// ShortHelp returns the short help view.
func (m *ProviderWizard) ShortHelp() []key.Binding {
	bindings := make([]key.Binding, 0, 3)
	switch m.step {
	case providerWizardStepModels:
		submit := key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "add model / finish"),
		)
		bindings = append(bindings, submit, m.keyMap.Remove)
	default:
		bindings = append(bindings, m.keyMap.Submit)
	}
	bindings = append(bindings, m.keyMap.Close)
	return bindings
}
