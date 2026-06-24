package completions

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/hackafterdark/phosphor/internal/ui/list"
	"github.com/rivo/uniseg"
	"github.com/sahilm/fuzzy"
)

// FileCompletionValue represents a file path completion value.
type FileCompletionValue struct {
	Path string
}

// ResourceCompletionValue represents a MCP resource completion value.
type ResourceCompletionValue struct {
	MCPName  string
	URI      string
	Title    string
	MIMEType string
}

// SlashCommandValue represents a slash command completion value.
type SlashCommandValue struct {
	Name        string
	Description string
}

// CompletionItem represents an item in the completions list.
type CompletionItem struct {
	*list.Versioned

	text    string
	value   any
	match   fuzzy.Match
	focused bool
	cache   map[int]string

	// Styles
	normalStyle  lipgloss.Style
	focusedStyle lipgloss.Style
	matchStyle   lipgloss.Style
}

// NewCompletionItem creates a new completion item.
func NewCompletionItem(text string, value any, normalStyle, focusedStyle, matchStyle lipgloss.Style) *CompletionItem {
	return &CompletionItem{
		Versioned:    list.NewVersioned(),
		text:         text,
		value:        value,
		normalStyle:  normalStyle,
		focusedStyle: focusedStyle,
		matchStyle:   matchStyle,
	}
}

// Finished implements list.Item. Completion items render purely from
// (text, match, focus); any mutation (SetMatch / SetFocused) bumps
// Version() so the frozen cache entry invalidates on the next
// render. Marking them finished lets the F6 list memo skip the
// per-line work for the steady completions popup.
func (c *CompletionItem) Finished() bool {
	return true
}

// Text returns the display text of the item.
func (c *CompletionItem) Text() string {
	return c.text
}

// Value returns the value of the item.
func (c *CompletionItem) Value() any {
	return c.value
}

// Filter implements [list.FilterableItem].
func (c *CompletionItem) Filter() string {
	return c.text
}

// SetMatch implements [list.MatchSettable].
func (c *CompletionItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(c.match, m) {
		return
	}
	c.cache = nil
	c.match = m
	c.Bump()
}

// sameFuzzyMatch reports whether two fuzzy.Match values are
// observably equal. Because Match contains a slice (MatchedIndexes)
// it is not directly comparable with ==; we compare the scalar
// fields and then walk the indexes. SetMatch uses this to skip
// gratuitous version bumps when the same match is reapplied.
func sameFuzzyMatch(a, b fuzzy.Match) bool {
	return a.Str == b.Str &&
		a.Index == b.Index &&
		a.Score == b.Score &&
		slices.Equal(a.MatchedIndexes, b.MatchedIndexes)
}

// SetFocused implements [list.Focusable].
func (c *CompletionItem) SetFocused(focused bool) {
	if c.focused == focused {
		return
	}
	c.cache = nil
	c.focused = focused
	c.Bump()
}

// Render implements [list.Item].
func (c *CompletionItem) Render(width int) string {
	var desc string
	if cmd, ok := c.value.(SlashCommandValue); ok {
		desc = cmd.Description
	}
	return renderItem(
		c.normalStyle,
		c.focusedStyle,
		c.matchStyle,
		c.text,
		desc,
		c.focused,
		width,
		c.cache,
		&c.match,
	)
}

func renderItem(
	normalStyle, focusedStyle, matchStyle lipgloss.Style,
	text string,
	desc string,
	focused bool,
	width int,
	cache map[int]string,
	match *fuzzy.Match,
) string {
	if cache == nil {
		cache = make(map[int]string)
	}

	cached, ok := cache[width]
	if ok {
		return cached
	}

	// Select base style.
	style := normalStyle
	matchStyle = matchStyle.Background(style.GetBackground())
	if focused {
		style = focusedStyle
		matchStyle = matchStyle.Background(style.GetBackground())
	}

	innerWidth := width - 2 // Account for padding
	var content string

	if desc == "" {
		// Truncate if needed.
		if ansi.StringWidth(text) > innerWidth {
			text = ansi.Truncate(text, innerWidth, "…")
		}

		// Render full-width text with background.
		content = style.Padding(0, 1).Width(width).Render(text)

		// Apply match highlighting using StyleRanges.
		if len(match.MatchedIndexes) > 0 {
			var ranges []lipgloss.Range
			for _, rng := range matchedRanges(match.MatchedIndexes) {
				start, stop := bytePosToVisibleCharPos(text, rng)
				// Offset by 1 for the padding space.
				ranges = append(ranges, lipgloss.NewRange(start+1, stop+2, matchStyle))
			}
			content = lipgloss.StyleRanges(content, ranges...)
		}
	} else {
		// Render dual-column: "name  description"
		nameWidth := ansi.StringWidth(text)
		
		if nameWidth+2 >= innerWidth {
			// Name takes up all or almost all space. Just render truncated name.
			truncatedName := ansi.Truncate(text, innerWidth, "…")
			content = style.Padding(0, 1).Width(width).Render(truncatedName)
		} else {
			descMaxWidth := innerWidth - nameWidth - 2
			truncatedDesc := desc
			if ansi.StringWidth(desc) > descMaxWidth {
				truncatedDesc = ansi.Truncate(desc, descMaxWidth, "…")
			}

			// Style the name and highlight any fuzzy matches.
			nameStyle := lipgloss.NewStyle().Foreground(style.GetForeground()).Background(style.GetBackground())
			nameStr := nameStyle.Render(text)

			if len(match.MatchedIndexes) > 0 {
				var ranges []lipgloss.Range
				for _, rng := range matchedRanges(match.MatchedIndexes) {
					start, stop := bytePosToVisibleCharPos(text, rng)
					ranges = append(ranges, lipgloss.NewRange(start, stop, matchStyle))
				}
				nameStr = lipgloss.StyleRanges(nameStr, ranges...)
			}

			// Style the separator.
			sepStyle := lipgloss.NewStyle().Background(style.GetBackground())
			sepStr := sepStyle.Render("  ")

			// Style the description with faint (dimmed) coloring.
			descStyle := lipgloss.NewStyle().Faint(true).Foreground(style.GetForeground()).Background(style.GetBackground())
			descStr := descStyle.Render(truncatedDesc)

			// Pad right side to fill exactly innerWidth.
			usedWidth := nameWidth + 2 + ansi.StringWidth(truncatedDesc)
			remaining := innerWidth - usedWidth
			var rightPadStr string
			if remaining > 0 {
				rightPadStr = lipgloss.NewStyle().Background(style.GetBackground()).Render(strings.Repeat(" ", remaining))
			}

			// Add left and right padding spaces (totaling width).
			leftPadStr := lipgloss.NewStyle().Background(style.GetBackground()).Render(" ")
			rightPadStr2 := lipgloss.NewStyle().Background(style.GetBackground()).Render(" ")

			content = leftPadStr + nameStr + sepStr + descStr + rightPadStr + rightPadStr2
		}
	}

	cache[width] = content
	return content
}

// matchedRanges converts a list of match indexes into contiguous ranges.
func matchedRanges(in []int) [][2]int {
	if len(in) == 0 {
		return [][2]int{}
	}
	current := [2]int{in[0], in[0]}
	if len(in) == 1 {
		return [][2]int{current}
	}
	var out [][2]int
	for i := 1; i < len(in); i++ {
		if in[i] == current[1]+1 {
			current[1] = in[i]
		} else {
			out = append(out, current)
			current = [2]int{in[i], in[i]}
		}
	}
	out = append(out, current)
	return out
}

// bytePosToVisibleCharPos converts byte positions to visible character positions.
func bytePosToVisibleCharPos(str string, rng [2]int) (int, int) {
	bytePos, byteStart, byteStop := 0, rng[0], rng[1]
	pos, start, stop := 0, 0, 0
	gr := uniseg.NewGraphemes(str)
	for byteStart > bytePos {
		if !gr.Next() {
			break
		}
		bytePos += len(gr.Str())
		pos += max(1, gr.Width())
	}
	start = pos
	for byteStop > bytePos {
		if !gr.Next() {
			break
		}
		bytePos += len(gr.Str())
		pos += max(1, gr.Width())
	}
	stop = pos
	return start, stop
}

// Ensure CompletionItem implements the required interfaces.
var (
	_ list.Item           = (*CompletionItem)(nil)
	_ list.FilterableItem = (*CompletionItem)(nil)
	_ list.MatchSettable  = (*CompletionItem)(nil)
	_ list.Focusable      = (*CompletionItem)(nil)
)
