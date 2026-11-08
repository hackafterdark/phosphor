package dialog

import (
	"cmp"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
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
	providerWizardStepContext
	providerWizardStepAttach
)

// providerWizardStepCount is the total number of wizard screens, used in
// the "Step X of N" footer.
const providerWizardStepCount = int(providerWizardStepAttach) + 1

// providerWizardHeight is the fixed content height of the dialog.
// Padding every step to this height keeps the dialog from resizing as
// the collected values appear.
const providerWizardHeight = 10

// providerIDPattern is the accepted character set for a provider id. Dots
// are deliberately excluded because config writes use dotted key paths
// (providers.<id>.base_url).
var providerIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ActionSetupProvider is returned by the wizard when the user finishes
// the flow. The caller is responsible for persisting the provider to the
// global config and closing the dialog.
type ActionSetupProvider struct {
	ProviderID  string
	BaseURL     string
	APIKey      string
	Models      []string
	ContextW    int64
	Attachments *bool
}

// boolOption identifies one choice on the boolean capabilities screen.
type boolOption int

const (
	boolOptionYes boolOption = iota
	boolOptionNo
)

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
	contextW   int64
	attach     *bool
	attachSel  boolOption

	errText string

	nameInput     textinput.Model
	urlInput      textinput.Model
	keyInput      textinput.Model
	modelInput    textinput.Model
	contextWInput textinput.Model

	help   help.Model
	keyMap struct {
		Submit key.Binding
		Remove key.Binding
		Prev   key.Binding
		Next   key.Binding
		Close  key.Binding
	}
}

var _ Dialog = (*ProviderWizard)(nil)

// NewProviderWizard creates a new provider setup wizard dialog. It always
// renders as a framed, centered dialog so it stands apart from whatever
// dialog (e.g. the onboarding provider picker) sits behind it.
func NewProviderWizard(com *common.Common) *ProviderWizard {
	m := &ProviderWizard{}
	m.com = com
	m.width = 70

	innerWidth := m.width - com.Styles.Dialog.View.GetHorizontalFrameSize() - 2 // (1) cursor padding

	m.nameInput = newWizardInput(com, innerWidth, "e.g. ollama")
	m.urlInput = newWizardInput(com, innerWidth, "http://localhost:11434/v1")
	m.keyInput = newWizardInput(com, innerWidth, "leave empty if the server doesn't need one")
	m.modelInput = newWizardInput(com, innerWidth, "e.g. qwen3-32b")
	m.contextWInput = newWizardInput(com, innerWidth, "e.g. 32768 (leave empty to skip)")
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
	m.keyMap.Prev = key.NewBinding(
		key.WithKeys("left", "h"),
		key.WithHelp("←", "previous"),
	)
	m.keyMap.Next = key.NewBinding(
		key.WithKeys("right", "l"),
		key.WithHelp("→", "next"),
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
			if m.step == providerWizardStepAttach {
				return m.advance()
			}
			// On the model-ID screen an empty input means "done adding
			// models": move to the context-window screen. With no models
			// collected yet, fall through to advance() so the required
			// list error surfaces instead of silently skipping.
			if m.step == providerWizardStepModels && strings.TrimSpace(m.modelInput.Value()) == "" && len(m.models) > 0 {
				m.step = providerWizardStepContext
				m.errText = ""
				m.focusStep()
				return nil
			}
			return m.advance()
		case m.step == providerWizardStepAttach &&
			(key.Matches(msg, m.keyMap.Prev) || key.Matches(msg, m.keyMap.Next)):
			if key.Matches(msg, m.keyMap.Prev) {
				m.attachSel = boolOptionYes
			} else {
				m.attachSel = boolOptionNo
			}
			return nil
		default:
			// Backspace on an empty input steps back a screen.
			if m.step == providerWizardStepAttach {
				if msg.String() == "backspace" && m.step > providerWizardStepName {
					m.step--
					m.errText = ""
					m.focusStep()
					return nil
				}
				return nil
			}
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
		if m.step == providerWizardStepAttach {
			return nil
		}
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
	case providerWizardStepModels:
		return &m.modelInput
	case providerWizardStepContext:
		return &m.contextWInput
	default:
		return nil
	}
}

func (m *ProviderWizard) focusStep() {
	m.nameInput.Blur()
	m.urlInput.Blur()
	m.keyInput.Blur()
	m.modelInput.Blur()
	m.contextWInput.Blur()
	if input := m.currentInput(); input != nil {
		input.Focus()
	}
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
		m.step = providerWizardStepContext
		m.focusStep()
	case providerWizardStepContext:
		raw := strings.TrimSpace(m.contextWInput.Value())
		if raw != "" {
			contextW, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || contextW <= 0 {
				m.errText = "Context window must be a positive number of tokens (e.g. 32768)."
				return nil
			}
			m.contextW = contextW
		}
		m.step = providerWizardStepAttach
		m.focusStep()
	case providerWizardStepAttach:
		value := m.attachSel == boolOptionYes
		m.attach = &value
		return ActionSetupProvider{
			ProviderID:  m.providerID,
			BaseURL:     m.baseURL,
			APIKey:      m.apiKey,
			Models:      slices.Clone(m.models),
			ContextW:    m.contextW,
			Attachments: m.attach,
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
		textStyle.Render(fmt.Sprintf("Step %d of %d · written to %s", m.step+1, providerWizardStepCount, config.GlobalConfigData())),
		"",
		helpStyle.Render(m.help.View(m)),
	)
	// Pad short steps so the dialog never changes size mid-flow.
	for len(lines) < providerWizardHeight {
		lines = append(lines, "")
	}

	view := dialogStyle.Render(strings.Join(lines, "\n"))
	cur := m.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// stepLines renders the running summary of everything collected so far
// so the user always sees the full picture, including the model list.
// Fields whose value is not known yet hold a placeholder so the dialog
// keeps the same height on every step.
func (m *ProviderWizard) stepLines() []string {
	t := m.com.Styles
	models := "(pending)"
	if len(m.models) > 0 {
		models = strings.Join(m.models, ", ")
	}
	contextW := "(skipped)"
	if m.contextW > 0 {
		contextW = strconv.FormatInt(m.contextW, 10)
	}
	attach := "(pending)"
	if m.attach != nil {
		attach = fmt.Sprintf("%t", *m.attach)
	}
	return []string{
		t.Dialog.SecondaryText.Render("provider:     " + cmp.Or(m.providerID, "(pending)")),
		t.Dialog.SecondaryText.Render("url:          " + cmp.Or(m.baseURL, "(pending)")),
		t.Dialog.SecondaryText.Render("api key:      " + cmp.Or(m.apiKey, "(none)")),
		t.Dialog.SecondaryText.Render("models:       " + models),
		t.Dialog.SecondaryText.Render("context:      " + contextW),
		t.Dialog.SecondaryText.Render("attachments:  " + attach),
	}
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
	case providerWizardStepContext:
		title = textStyle.Render("Set the ") + accentStyle.Render("Context Window") + textStyle.Render(" (optional).")
	case providerWizardStepAttach:
		title = textStyle.Render("Does it support ") + accentStyle.Render("Attachments") + textStyle.Render("?")
	}

	headerOffset := titleStyle.GetHorizontalFrameSize() + dialogStyle.GetHorizontalFrameSize()
	return common.DialogTitle(t, titleStyle.Render(title), m.width-headerOffset, t.Dialog.TitleGradFromColor, t.Dialog.TitleGradToColor)
}

// inputView renders the current input, or the yes/no selector on the
// boolean capabilities screen.
func (m *ProviderWizard) inputView() string {
	if m.step == providerWizardStepAttach {
		return m.attachView()
	}
	return m.currentInput().View()
}

// attachView renders the yes/no selector with radio markers so the
// answer is always a valid boolean.
func (m *ProviderWizard) attachView() string {
	t := m.com.Styles
	yesRadio, noRadio := t.Radio.Off, t.Radio.Off
	if m.attachSel == boolOptionYes {
		yesRadio = t.Radio.On
	} else {
		noRadio = t.Radio.On
	}
	return fmt.Sprintf("%s %s  %s %s",
		yesRadio.Render(), t.Radio.Label.Render("Yes"),
		noRadio.Render(), t.Radio.Label.Render("No"),
	)
}

// Cursor returns the cursor position relative to the dialog. The
// boolean screen has no text input, so it shows no cursor.
func (m *ProviderWizard) Cursor() *tea.Cursor {
	input := m.currentInput()
	if input == nil {
		return nil
	}
	return InputCursor(m.com.Styles, input.Cursor())
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
			key.WithHelp("enter", "add model / continue"),
		)
		bindings = append(bindings, submit, m.keyMap.Remove)
	case providerWizardStepAttach:
		bindings = append(bindings, m.keyMap.Prev, m.keyMap.Next, m.keyMap.Submit)
	default:
		bindings = append(bindings, m.keyMap.Submit)
	}
	bindings = append(bindings, m.keyMap.Close)
	return bindings
}
