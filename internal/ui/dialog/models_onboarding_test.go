package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/csync"
	"github.com/stretchr/testify/require"
)

func TestOnboardingCustomProviderEntry(t *testing.T) {
	t.Parallel()

	t.Run("shown at top of list during onboarding", func(t *testing.T) {
		t.Parallel()

		m := newTestModels(t, true)
		items := visibleSelectable(m)
		require.NotEmpty(t, items)
		first, ok := items[0].(*ModelItem)
		require.True(t, ok)
		require.Equal(t, customProviderItemID, first.prov.ID)
	})

	t.Run("selecting it opens the provider wizard", func(t *testing.T) {
		t.Parallel()

		m := newTestModels(t, true)
		m.list.SelectFirst()
		action := m.HandleMsg(selectKeyMsg())
		open, ok := action.(ActionOpenDialog)
		require.True(t, ok, "expected ActionOpenDialog, got %T", action)
		require.Equal(t, ProviderWizardID, open.DialogID)
	})

	t.Run("not shown outside onboarding", func(t *testing.T) {
		t.Parallel()

		m := newTestModels(t, false)
		for _, item := range visibleSelectable(m) {
			mi, ok := item.(*ModelItem)
			if !ok {
				continue
			}
			require.NotEqual(t, customProviderItemID, mi.prov.ID)
		}
	})

	t.Run("real models are unaffected", func(t *testing.T) {
		t.Parallel()

		m := newTestModels(t, true)
		var found bool
		for _, item := range visibleSelectable(m) {
			mi, ok := item.(*ModelItem)
			if ok && mi.prov.ID != customProviderItemID {
				found = true
				break
			}
		}
		require.True(t, found, "built-in providers must still be listed")
	})
}

func newTestModels(t *testing.T, isOnboarding bool) *Models {
	t.Helper()

	originalUseMock := config.UseMockProviders
	config.UseMockProviders = true
	t.Cleanup(func() {
		config.UseMockProviders = originalUseMock
		config.ResetProviders()
	})
	config.ResetProviders()

	s := styles.CharmtonePantera()
	cfg := &config.Config{
		Options:   &config.Options{DisableProviderAutoUpdate: true},
		Providers: csync.NewMap[string, config.ProviderConfig](),
	}
	com := &common.Common{
		Styles:    &s,
		Workspace: &mockWorkspace{cfg: cfg},
	}
	m, err := NewModels(com, isOnboarding)
	require.NoError(t, err)
	return m
}

func visibleSelectable(m *Models) []list.Item {
	items := m.list.VisibleItems()
	var out []list.Item
	for _, item := range items {
		if _, ok := item.(*ModelItem); ok {
			out = append(out, item)
		}
	}
	return out
}

func selectKeyMsg() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyEnter}
}
