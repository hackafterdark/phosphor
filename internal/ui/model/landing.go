package model

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/dustin/go-humanize"
	"github.com/hackafterdark/phosphor/internal/agent/tools/mcp"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/lsp"
	"github.com/hackafterdark/phosphor/internal/session"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/internal/workspace"
)

type loadRecentSessionsMsg []session.Session

// loadRecentSessions queries the workspace for recent sessions.
func (m *UI) loadRecentSessions() tea.Cmd {
	return func() tea.Msg {
		sessions, err := m.com.Workspace.ListSessions(context.Background())
		if err != nil {
			return loadRecentSessionsMsg(nil)
		}
		return loadRecentSessionsMsg(sessions)
	}
}

// selectedLargeModel returns the currently selected large language model
// from the agent coordinator, if one exists.
func (m *UI) selectedLargeModel() *workspace.AgentModel {
	if m.com.Workspace.AgentIsReady() {
		model := m.com.Workspace.AgentModel()
		return &model
	}
	return nil
}

// getLandingConfig returns the landing screen layout config from TUIOptions,
// falling back to the built-in defaults.
func (m *UI) getLandingConfig() config.LandingConfig {
	if m.com.Config().Options.TUI.Landing != nil {
		cfg := *m.com.Config().Options.TUI.Landing
		if cfg.MaxColumns == 0 {
			cfg.MaxColumns = config.DefaultLandingConfig().MaxColumns
		}
		if cfg.MinColWidth == 0 {
			cfg.MinColWidth = config.DefaultLandingConfig().MinColWidth
		}
		if cfg.Gap == 0 {
			cfg.Gap = config.DefaultLandingConfig().Gap
		}
		if cfg.Components == nil {
			cfg.Components = config.DefaultLandingConfig().Components
		}
		return cfg
	}
	return config.DefaultLandingConfig()
}

// landingView renders the landing page with config-driven layout.
// Components are filtered by visibility, sorted by order, and laid out
// responsively in columns.
func (m *UI) landingView() string {
	t := m.com.Styles
	width := m.layout.main.Dx()

	// Get the landing config.
	cfg := m.getLandingConfig()

	// Filter non-hidden components; array position determines display order.
	var components []config.LandingComponentConfig
	for _, comp := range cfg.Components {
		if !comp.Hidden {
			components = append(components, comp)
		}
	}

	// If no non-hidden components, use default layout.
	if len(components) == 0 {
		components = config.DefaultLandingConfig().Components
	}

	// Calculate column layout.
	gap := cfg.Gap
	if gap == 0 {
		gap = config.DefaultLandingConfig().Gap
	}
	maxCols := cfg.MaxColumns
	if maxCols == 0 {
		maxCols = config.DefaultLandingConfig().MaxColumns
	}

	// Determine how many columns can actually fit.
	var numCols int
	if maxCols >= 2 && width >= 80 {
		numCols = 2
	} else {
		numCols = 1
	}

	colWidth := width
	if numCols > 1 {
		colWidth = (width - gap) / numCols
	}

	// Render each component individually.
	var renderedComponents []string
	for _, comp := range components {
		rendered := m.renderLandingComponent(comp, colWidth)
		if rendered != "" {
			renderedComponents = append(renderedComponents, rendered)
		}
	}

	// If nothing to render, show empty state.
	if len(renderedComponents) == 0 {
		content := t.Files.EmptyMessage.Render("No components configured")
		return lipgloss.NewStyle().
			Width(width).
			Height(m.layout.main.Dy() - 1).
			PaddingTop(1).
			Render(content)
	}

	// Distribute components into columns.
	var columns [][]string
	for i := 0; i < numCols; i++ {
		columns = append(columns, nil)
	}
	for i, content := range renderedComponents {
		colIdx := i % numCols
		columns[colIdx] = append(columns[colIdx], content)
	}

	// Join columns.
	var renderedCols []string
	for _, col := range columns {
		if len(col) > 0 {
			colContent := joinWithVerticalGap(col, cfg.VerticalGap)
			colStyle := lipgloss.NewStyle().Width(colWidth)
			renderedCols = append(renderedCols, colStyle.Render(colContent))
		}
	}

	var content string
	if len(renderedCols) == 1 {
		content = renderedCols[0]
	} else if len(renderedCols) == 2 {
		content = lipgloss.JoinHorizontal(lipgloss.Top, renderedCols[0], strings.Repeat(" ", gap), renderedCols[1])
	} else {
		content = lipgloss.JoinVertical(lipgloss.Left, renderedCols...)
	}

	// Final wrapper with height.
	return lipgloss.NewStyle().
		Width(width).
		Height(m.layout.main.Dy() - 1).
		PaddingTop(1).
		Render(content)
}

// joinWithVerticalGap joins strings vertically with a configurable number of blank lines between them.
func joinWithVerticalGap(parts []string, gap int) string {
	if len(parts) == 0 {
		return ""
	}
	if gap == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	}

	var result []string
	for i, part := range parts {
		result = append(result, part)
		if i < len(parts)-1 {
			for range gap {
				result = append(result, "")
			}
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, result...)
}

// renderLandingComponent renders a single landing component by ID.
func (m *UI) renderLandingComponent(comp config.LandingComponentConfig, colWidth int) string {
	switch comp.ID {
	case "recent_sessions":
		return m.renderRecentSessions(comp, colWidth)
	case "quick_actions":
		return m.renderQuickActions(colWidth)
	case "active_project":
		return m.renderActiveProject(colWidth)
	case "active_llm":
		return m.renderActiveLLM(colWidth)
	case "capabilities":
		return m.renderCapabilities(colWidth)
	default:
		return ""
	}
}

// renderRecentSessions renders the "Recent Sessions" component.
func (m *UI) renderRecentSessions(comp config.LandingComponentConfig, colWidth int) string {
	t := m.com.Styles

	maxItems := comp.MaxItems
	if maxItems == 0 {
		maxItems = 5
	}

	var parts []string
	parts = append(parts, common.Section(t, "Recent Sessions", colWidth))

	if len(m.recentSessions) == 0 {
		parts = append(parts, t.Files.EmptyMessage.Render("None"))
	} else {
		limit := maxItems
		if len(m.recentSessions) < limit {
			limit = len(m.recentSessions)
		}
		for _, s := range m.recentSessions[:limit] {
			title := s.Title
			if title == "" {
				title = "Untitled Session"
			}
			info := humanize.Time(time.Unix(s.UpdatedAt, 0))

			// Truncate title so the line fits within colWidth.
			infoWidth := lipgloss.Width(info)
			maxTitleWidth := colWidth - infoWidth - 4
			if maxTitleWidth < 10 {
				maxTitleWidth = 10
			}
			truncatedTitle := ansi.Truncate(title, maxTitleWidth, "…")
			titleFormatted := t.Files.Path.Render(truncatedTitle)
			infoFormatted := t.Dialog.Sessions.InfoBlurred.Render(info)

			gapSize := max(0, colWidth-lipgloss.Width(titleFormatted)-lipgloss.Width(infoFormatted)-2)
			line := fmt.Sprintf("• %s%s%s", titleFormatted, strings.Repeat(" ", gapSize), infoFormatted)
			parts = append(parts, line)
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderQuickActions renders the "Quick Actions" component.
func (m *UI) renderQuickActions(colWidth int) string {
	t := m.com.Styles

	type action struct {
		key  string
		desc string
	}
	actions := []action{
		{"ctrl+n", "New Session"},
		{"ctrl+s", "Sessions List"},
		{"ctrl+l", "Select Model"},
		{"ctrl+p", "Commands Menu"},
		{"ctrl+c", "Quit"},
	}

	var parts []string
	parts = append(parts, common.Section(t, "Quick Actions", colWidth))
	for _, act := range actions {
		keyFormatted := t.Header.Keystroke.Render(act.key)
		descFormatted := t.Header.KeystrokeTip.Render("  " + act.desc)
		parts = append(parts, fmt.Sprintf("%s%s", keyFormatted, descFormatted))
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderActiveProject renders the "Active Project" component.
func (m *UI) renderActiveProject(colWidth int) string {
	t := m.com.Styles

	var parts []string
	parts = append(parts, common.Section(t, "Active Project", colWidth))
	cwd := common.PrettyPath(t, m.com.Workspace.WorkingDir(), colWidth)
	parts = append(parts, cwd)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderActiveLLM renders the "Active LLM" component.
func (m *UI) renderActiveLLM(colWidth int) string {
	t := m.com.Styles

	var parts []string
	parts = append(parts, common.Section(t, "Active LLM", colWidth))
	modelInfo := m.modelInfo(colWidth)
	if modelInfo == "" {
		modelInfo = t.Files.EmptyMessage.Render("None")
	}
	parts = append(parts, modelInfo)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderCapabilities renders the "Capabilities" component.
func (m *UI) renderCapabilities(colWidth int) string {
	t := m.com.Styles

	var parts []string
	parts = append(parts, common.Section(t, "Capabilities", colWidth))

	// Active LSPs list.
	var activeLSPs []string
	for _, state := range m.lspStates {
		if state.State == lsp.StateReady {
			activeLSPs = append(activeLSPs, state.Name)
		}
	}
	slices.Sort(activeLSPs)
	lspText := "(None)"
	if len(activeLSPs) > 0 {
		lspText = strings.Join(activeLSPs, ", ")
	}

	// Active MCPs list.
	var activeMCPs []string
	for _, state := range m.mcpStates {
		if state.State == mcp.StateConnected {
			title := state.Name
			if state.Name == config.DockerMCPName {
				title = "Docker MCP"
			}
			activeMCPs = append(activeMCPs, title)
		}
	}
	slices.Sort(activeMCPs)
	mcpText := "(None)"
	if len(activeMCPs) > 0 {
		mcpText = strings.Join(activeMCPs, ", ")
	}

	// Active Skills list.
	var activeSkills []string
	for _, item := range m.skillStatusItems() {
		activeSkills = append(activeSkills, item.name)
	}
	skillsText := "(None)"
	if len(activeSkills) > 0 {
		skillsText = strings.Join(activeSkills, ", ")
	}

	capabilitiesStyle := lipgloss.NewStyle().Width(colWidth)
	parts = append(parts,
		capabilitiesStyle.Render(fmt.Sprintf("%s %s", t.Header.Keystroke.Render("LSPs:  "), t.Header.KeystrokeTip.Render(lspText))),
		capabilitiesStyle.Render(fmt.Sprintf("%s %s", t.Header.Keystroke.Render("MCPs:  "), t.Header.KeystrokeTip.Render(mcpText))),
		capabilitiesStyle.Render(fmt.Sprintf("%s %s", t.Header.Keystroke.Render("Skills:"), t.Header.KeystrokeTip.Render(skillsText))),
	)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
