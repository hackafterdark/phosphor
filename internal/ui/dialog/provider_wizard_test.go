package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestWizard(t *testing.T) *ProviderWizard {
	t.Helper()
	s := styles.CharmtonePantera()
	return NewProviderWizard(&common.Common{Styles: &s})
}

func TestNormalizeBaseURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"ollama with /v1", "http://localhost:11434/v1", "http://localhost:11434/v1"},
		{"scheme and host only gets /v1", "http://localhost:8080", "http://localhost:8080/v1"},
		{"trailing slash trimmed", "http://localhost:8000/v1/", "http://localhost:8000/v1"},
		{"missing scheme gets http", "localhost:1234/v1", "http://localhost:1234/v1"},
		{"host:port without scheme", "192.168.1.10:8080", "http://192.168.1.10:8080/v1"},
		{"https preserved", "https://infer.example.com/v1", "https://infer.example.com/v1"},
		{"custom path preserved", "http://localhost:8000/api/openai/v1", "http://localhost:8000/api/openai/v1"},
		{"root path gets /v1", "http://localhost:8000/", "http://localhost:8000/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeBaseURL(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("empty is an error", func(t *testing.T) {
		t.Parallel()
		_, err := normalizeBaseURL("")
		require.Error(t, err)
	})

	t.Run("non-http scheme is an error", func(t *testing.T) {
		t.Parallel()
		_, err := normalizeBaseURL("ftp://localhost:21/v1")
		require.Error(t, err)
	})

	t.Run("no host is an error", func(t *testing.T) {
		t.Parallel()
		_, err := normalizeBaseURL("http://")
		require.Error(t, err)
	})
}

func TestProviderWizardAdvanceFlow(t *testing.T) {
	t.Parallel()

	w := newTestWizard(t)

	// Step 1: empty and invalid names are rejected, valid name advances.
	w.nameInput.SetValue("")
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepName, w.step)

	w.nameInput.SetValue("bad name.dots")
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepName, w.step)
	assert.NotEmpty(t, w.errText)

	w.nameInput.SetValue("local")
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepURL, w.step)
	assert.Equal(t, "local", w.providerID)

	// Step 2: invalid URL stays put, valid URL normalizes and advances.
	w.urlInput.SetValue("ftp://nope")
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepURL, w.step)

	w.urlInput.SetValue("192.168.1.10:8000")
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepKey, w.step)
	assert.Equal(t, "http://192.168.1.10:8000/v1", w.baseURL)

	// Step 3: key is optional, empty advances.
	w.keyInput.SetValue("")
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepModels, w.step)

	// Step 4: enter with no models errors; typing + enter accumulates;
	// enter on empty input moves to the context-window screen.
	w.modelInput.SetValue("")
	require.Nil(t, w.advance())
	assert.NotEmpty(t, w.errText)

	w.modelInput.SetValue("qwen3-32b")
	require.Nil(t, w.advance())
	w.modelInput.SetValue("llama-3.1-70b")
	require.Nil(t, w.advance())
	w.modelInput.SetValue("qwen3-32b") // duplicate ignored
	require.Nil(t, w.advance())
	assert.Equal(t, []string{"qwen3-32b", "llama-3.1-70b"}, w.models)

	// HandleMsg routes the empty-input enter to the context-window screen.
	w.errText = ""
	require.Nil(t, w.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}))
	assert.Equal(t, providerWizardStepContext, w.step)

	// Invalid context windows stay on the step.
	w.contextWInput.SetValue("abc")
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepContext, w.step)
	assert.NotEmpty(t, w.errText)

	// Empty context window skips the value and moves to attachments.
	w.contextWInput.SetValue("")
	w.errText = ""
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepAttach, w.step)

	// Enter on the boolean screen finishes with an explicit answer.
	action := w.advance()
	save, ok := action.(ActionSetupProvider)
	require.True(t, ok, "expected ActionSetupProvider, got %T", action)
	assert.Equal(t, "local", save.ProviderID)
	assert.Equal(t, "http://192.168.1.10:8000/v1", save.BaseURL)
	assert.Empty(t, save.APIKey)
	assert.Equal(t, []string{"qwen3-32b", "llama-3.1-70b"}, save.Models)
	assert.Zero(t, save.ContextW)
	require.NotNil(t, save.Attachments)
	assert.True(t, *save.Attachments)
}

func TestProviderWizardCapabilities(t *testing.T) {
	t.Parallel()

	w := newTestWizard(t)
	w.step = providerWizardStepContext
	w.models = []string{"qwen3-32b"}

	w.contextWInput.SetValue("32768")
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepAttach, w.step)
	assert.Equal(t, int64(32768), w.contextW)

	// Invalid numbers stay on the step.
	w.step = providerWizardStepContext
	w.contextWInput.SetValue("32768 maybe")
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepContext, w.step)
	assert.NotEmpty(t, w.errText)

	w.contextWInput.SetValue("0")
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepContext, w.step)
	assert.NotEmpty(t, w.errText)

	// Empty input skips the value entirely, and clears any value
	// entered on a previous pass through the step.
	w.errText = ""
	w.contextW = 32768
	w.contextWInput.SetValue("")
	require.Nil(t, w.advance())
	assert.Equal(t, providerWizardStepAttach, w.step)
	assert.Zero(t, w.contextW)
	assert.Zero(t, w.attach)
}

func TestProviderWizardAttachmentToggle(t *testing.T) {
	t.Parallel()

	w := newTestWizard(t)
	w.step = providerWizardStepAttach
	w.models = []string{"qwen3-32b"}

	// The selector defaults to yes; arrows set an explicit boolean.
	require.Nil(t, w.HandleMsg(tea.KeyPressMsg{Code: tea.KeyRight}))
	assert.Equal(t, boolOptionNo, w.attachSel)
	require.Nil(t, w.HandleMsg(tea.KeyPressMsg{Code: tea.KeyLeft}))
	assert.Equal(t, boolOptionYes, w.attachSel)

	action := w.advance()
	save, ok := action.(ActionSetupProvider)
	require.True(t, ok, "expected ActionSetupProvider, got %T", action)
	require.NotNil(t, save.Attachments)
	assert.True(t, *save.Attachments)

	// Free-form typing on the boolean screen is ignored entirely.
	w.HandleMsg(tea.KeyPressMsg{Code: 'x', Text: "x"})
	assert.Equal(t, boolOptionYes, w.attachSel)
	assert.Equal(t, providerWizardStepAttach, w.step)
}

func TestProviderWizardKeys(t *testing.T) {
	t.Parallel()

	w := newTestWizard(t)
	w.step = providerWizardStepModels
	w.focusStep()

	// Typing a model id and pressing enter appends it to the list.
	w.modelInput.SetValue("qwen3-32b")
	require.Nil(t, w.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}))
	assert.Equal(t, []string{"qwen3-32b"}, w.models)

	// ctrl+d removes the last model.
	require.Nil(t, w.HandleMsg(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}))
	assert.Empty(t, w.models)

	// Enter on empty input with no models is an error, not a save.
	require.Nil(t, w.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}))
	assert.NotEmpty(t, w.errText)

	// Re-adding a model and pressing enter on empty input moves to the
	// capabilities screens; enter on the context-window screen (empty)
	// skips it, and enter on the boolean screen finishes the wizard.
	w.modelInput.SetValue("qwen3-32b")
	require.Nil(t, w.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}))
	require.Nil(t, w.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}))
	assert.Equal(t, providerWizardStepContext, w.step)
	require.Nil(t, w.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}))
	assert.Equal(t, providerWizardStepAttach, w.step)
	_, ok := w.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}).(ActionSetupProvider)
	require.True(t, ok, "expected ActionSetupProvider on finish")

	// Backspace on the boolean screen steps back to the context step.
	require.Nil(t, w.HandleMsg(tea.KeyPressMsg{Code: tea.KeyBackspace}))
	assert.Equal(t, providerWizardStepContext, w.step)

	// Escape closes the wizard.
	assert.Equal(t, ActionClose{}, w.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}
