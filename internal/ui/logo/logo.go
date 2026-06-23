package logo

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/hackafterdark/phosphor/internal/ui/styles"
)

// Opts are the options for rendering the application logo.
type Opts struct {
	FieldColor   color.Color
	TitleColorA  color.Color
	TitleColorB  color.Color
	CharmColor   color.Color
	VersionColor color.Color
	Width        int
	Hyper        bool

	// AppTitle overrides the default "Phosphor" title text.
	AppTitle string
	// FigletFont specifies the FIGlet font to use. Empty means use the default.
	FigletFont string
	// FigletSolid renders the logo with solid block characters instead of raw font characters.
	FigletSolid bool

	Unstable bool

	// SidebarLogoPlain forces the sidebar logo to use plain text instead of FIGlet.
	SidebarLogoPlain bool
	// SidebarLogoHidden hides the sidebar logo entirely.
	SidebarLogoHidden bool
	// SidebarFigletFont specifies the FIGlet font for the sidebar logo.
	SidebarFigletFont string
}

// Render renders the logo. Set compact to true for the narrow sidebar version.
func Render(base lipgloss.Style, version string, compact bool, o Opts) string {
	title := o.AppTitle
	if title == "" {
		title = "Phosphor"
	}

	// Compact version: app title with diagonal decorations.
	if compact {
		field := fg(o.FieldColor, strings.Repeat("╱", o.Width))
		return strings.Join([]string{field, field, "", field, ""}, "\n")
	}

	// Wide version: render figlet text with gradient.
	return renderWideLogo(title, base, o.TitleColorA, o.TitleColorB, o.FigletFont, o.FigletSolid)
}

func renderWideLogo(title string, base lipgloss.Style, colorA, colorB color.Color, fontName string, figletSolid bool) string {
	titleText, width, _, _ := FigletText(title, fontName, figletSolid)
	lines := strings.Split(strings.TrimRight(titleText, "\n"), "\n")
	for i, line := range lines {
		currentWidth := lipgloss.Width(line)
		if currentWidth < width {
			lines[i] = line + strings.Repeat(" ", width-currentWidth)
		}
	}
	paddedText := strings.Join(lines, "\n")
	return applyGradient(paddedText, base, colorA, colorB)
}

// LogoHeight returns the visual height of the wide logo.
func LogoHeight(appTitle, figletFont string) int {
	title := appTitle
	if title == "" {
		title = "Phosphor"
	}
	_, _, height, err := FigletText(title, figletFont, false)
	if err != nil {
		return 5
	}
	return height
}

// SmallRender renders a small logo suitable for the sidebar.
// If SidebarLogoHidden is true, it returns an empty string.
// If SidebarLogoPlain is true, or if the figlet text does not fit within
// the given width, it falls back to a single-line plain-text title with
// gradient styling.
// maxHeight limits the number of lines returned; if the figlet exceeds
// this height, plain text is used instead.
func SmallRender(t *styles.Styles, width, maxHeight int, o Opts) string {
	// If hidden, return empty string so the sidebar skips the logo.
	if o.SidebarLogoHidden {
		return ""
	}

	name := o.AppTitle
	if name == "" {
		name = "Phosphor"
	}

	// If plain text is explicitly requested, skip figlet rendering.
	if o.SidebarLogoPlain {
		return applyGradient(name, t.Logo.GradCanvas, t.Logo.SmallGradFromColor, t.Logo.SmallGradToColor)
	}

	// Use the configured sidebar figlet font (default "Pagga").
	fontName := o.SidebarFigletFont
	if fontName == "" {
		fontName = "Pagga"
	}

	// Render using figlet font.
	titleText, _, _, _ := FigletText(name, fontName, false)
	lines := strings.Split(strings.TrimRight(titleText, "\n"), "\n")

	// Check if the figlet text exceeds the width or height constraints.
	for _, line := range lines {
		if lipgloss.Width(line) > width {
			return applyGradient(name, t.Logo.GradCanvas, t.Logo.SmallGradFromColor, t.Logo.SmallGradToColor)
		}
	}
	if len(lines) > maxHeight {
		return applyGradient(name, t.Logo.GradCanvas, t.Logo.SmallGradFromColor, t.Logo.SmallGradToColor)
	}

	titleText = applyGradient(titleText, t.Logo.GradCanvas, t.Logo.SmallGradFromColor, t.Logo.SmallGradToColor)
	return titleText
}

func fg(c color.Color, s string) string {
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

func applyGradient(text string, base lipgloss.Style, colorA, colorB color.Color) string {
	result := new(strings.Builder)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}
		result.WriteString(styles.ApplyForegroundGrad(base, line, colorA, colorB))
	}
	return result.String()
}
