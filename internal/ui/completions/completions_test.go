package completions

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

func TestFilterPrefersExactBasenameStem(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetItems([]FileCompletionValue{
		{Path: "internal/ui/chat/search.go"},
		{Path: "internal/ui/chat/user.go"},
	}, nil)

	c.Filter("user")

	filtered := c.filtered
	require.NotEmpty(t, filtered)
	first, ok := filtered[0].(*CompletionItem)
	require.True(t, ok)
	require.Equal(t, "internal/ui/chat/user.go", first.Text())
	require.NotEmpty(t, first.match.MatchedIndexes)
}

func TestFilterPrefersBasenamePrefix(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetItems([]FileCompletionValue{
		{Path: "internal/ui/chat/mcp.go"},
		{Path: "internal/ui/model/chat.go"},
	}, nil)

	c.Filter("chat.g")

	filtered := c.filtered
	require.NotEmpty(t, filtered)
	first, ok := filtered[0].(*CompletionItem)
	require.True(t, ok)
	require.Equal(t, "internal/ui/model/chat.go", first.Text())
	require.NotEmpty(t, first.match.MatchedIndexes)
}

func TestNamePriorityTier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		query    string
		wantTier int
	}{
		{
			name:     "exact stem",
			path:     "internal/ui/chat/user.go",
			query:    "user",
			wantTier: tierExactName,
		},
		{
			name:     "basename prefix",
			path:     "internal/ui/model/chat.go",
			query:    "chat.g",
			wantTier: tierPrefixName,
		},
		{
			name:     "path segment exact",
			path:     "internal/ui/chat/mcp.go",
			query:    "chat",
			wantTier: tierPathSegment,
		},
		{
			name:     "fallback",
			path:     "internal/ui/chat/search.go",
			query:    "user",
			wantTier: tierFallback,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := namePriorityTier(tt.path, tt.query)
			require.Equal(t, tt.wantTier, got)
		})
	}
}

func TestFilterPrefersPathSegmentExact(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetItems([]FileCompletionValue{
		{Path: "internal/ui/model/xychat.go"},
		{Path: "internal/ui/chat/mcp.go"},
	}, nil)

	c.Filter("chat")

	filtered := c.filtered
	require.NotEmpty(t, filtered)
	first, ok := filtered[0].(*CompletionItem)
	require.True(t, ok)
	require.Equal(t, "internal/ui/chat/mcp.go", first.Text())
}
func TestSelectCurrentWithSlashCommand(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetSlashCommands([]SlashCommandValue{
		{Name: "help"},
		{Name: "theme"},
		{Name: "models"},
	})

	c.Filter("h")
	msg := c.selectCurrent(false)

	require.NotNil(t, msg)
	selection, ok := msg.(SelectionMsg[SlashCommandValue])
	require.True(t, ok)
	require.Equal(t, "help", selection.Value.Name)
	require.False(t, selection.KeepOpen)
}

func TestFilterSelectedCompletionsMatchVisibleItems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		paths []string
	}{
		{
			name:  "ambiguous basename",
			query: "chat",
			paths: []string{
				"internal/ui/chat/chat.go",
				"internal/ui/chat/mcp.go",
				"internal/ui/model/chat.go",
				"internal/ui/model/xychat.go",
			},
		},
		{
			name:  "ambiguous single character",
			query: "m",
			paths: []string{
				"internal/ui/chat/mcp.go",
				"docs/chat.md",
				"README.md",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
			values := make([]FileCompletionValue, len(tt.paths))
			for i, path := range tt.paths {
				values[i] = FileCompletionValue{Path: path}
			}
			c.SetItems(values, nil)
			c.Filter(tt.query)

			require.NotEmpty(t, c.filtered)
			for i, item := range c.filtered {
				c.list.SetSelected(i)
				require.Equal(t, completionText(item), completionText(c.list.ItemAt(i)))
			}

			c.list.SetSelected(0)
			first, ok := c.list.ItemAt(0).(*CompletionItem)
			require.True(t, ok)
			want, ok := first.Value().(FileCompletionValue)
			require.True(t, ok)

			msg := c.selectCurrent(false)
			require.NotNil(t, msg)
			got, ok := msg.(SelectionMsg[FileCompletionValue])
			require.True(t, ok)
			require.Equal(t, want, got.Value)
		})
	}
}

func completionText(item any) string {
	completion, ok := item.(*CompletionItem)
	if !ok {
		return "<unknown>"
	}
	return completion.Text()
}
