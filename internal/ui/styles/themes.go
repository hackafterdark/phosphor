package styles

import (
	"encoding/json"
	"image/color"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/hackafterdark/phosphor/internal/home"
	"gopkg.in/yaml.v3"
)

// ThemeLogoConfig holds logo-related settings that can be defined in theme files.
type ThemeLogoConfig struct {
	// AppTitle is the application title displayed in the logo.
	AppTitle string `json:"app_title,omitempty" yaml:"app_title,omitempty"`
	// FigletFont is the name of a FIGlet font to use for the logo.
	FigletFont string `json:"figlet_font,omitempty" yaml:"figlet_font,omitempty"`
	// FigletSolid renders the FIGlet logo using solid block characters.
	FigletSolid bool `json:"figlet_solid,omitempty" yaml:"figlet_solid,omitempty"`
	// SidebarLogoType controls the sidebar logo style: "figlet", "plain_text", or "hidden".
	SidebarLogoType string `json:"sidebar_logo_type,omitempty" yaml:"sidebar_logo_type,omitempty"`
	// SidebarFigletFont specifies a separate FIGlet font for the sidebar logo.
	SidebarFigletFont string `json:"sidebar_figlet_font,omitempty" yaml:"sidebar_figlet_font,omitempty"`
}

// ThemePalette represents the JSON/YAML structure for a custom color theme.
// All color fields are optional strings to allow partial overrides of a
// base theme.
type ThemePalette struct {
	Name              string  `json:"name" yaml:"name"`
	Primary           *string `json:"primary,omitempty" yaml:"primary,omitempty"`
	Secondary         *string `json:"secondary,omitempty" yaml:"secondary,omitempty"`
	Accent            *string `json:"accent,omitempty" yaml:"accent,omitempty"`
	Keyword           *string `json:"keyword,omitempty" yaml:"keyword,omitempty"`
	FgBase            *string `json:"fgBase,omitempty" yaml:"fgBase,omitempty"`
	BgBase            *string `json:"bgBase,omitempty" yaml:"bgBase,omitempty"`
	Separator         *string `json:"separator,omitempty" yaml:"separator,omitempty"`
	FgSubtle          *string `json:"fgSubtle,omitempty" yaml:"fgSubtle,omitempty"`
	FgMoreSubtle      *string `json:"fgMoreSubtle,omitempty" yaml:"fgMoreSubtle,omitempty"`
	FgMostSubtle      *string `json:"fgMostSubtle,omitempty" yaml:"fgMostSubtle,omitempty"`
	OnPrimary         *string `json:"onPrimary,omitempty" yaml:"onPrimary,omitempty"`
	BgMostVisible     *string `json:"bgMostVisible,omitempty" yaml:"bgMostVisible,omitempty"`
	BgLessVisible     *string `json:"bgLessVisible,omitempty" yaml:"bgLessVisible,omitempty"`
	BgLeastVisible    *string `json:"bgLeastVisible,omitempty" yaml:"bgLeastVisible,omitempty"`
	Destructive       *string `json:"destructive,omitempty" yaml:"destructive,omitempty"`
	Error             *string `json:"error,omitempty" yaml:"error,omitempty"`
	Warning           *string `json:"warning,omitempty" yaml:"warning,omitempty"`
	WarningSubtle     *string `json:"warningSubtle,omitempty" yaml:"warningSubtle,omitempty"`
	Denied            *string `json:"denied,omitempty" yaml:"denied,omitempty"`
	Busy              *string `json:"busy,omitempty" yaml:"busy,omitempty"`
	Info              *string `json:"info,omitempty" yaml:"info,omitempty"`
	InfoMoreSubtle    *string `json:"infoMoreSubtle,omitempty" yaml:"infoMoreSubtle,omitempty"`
	InfoMostSubtle    *string `json:"infoMostSubtle,omitempty" yaml:"infoMostSubtle,omitempty"`
	Success           *string `json:"success,omitempty" yaml:"success,omitempty"`
	SuccessMoreSubtle *string `json:"successMoreSubtle,omitempty" yaml:"successMoreSubtle,omitempty"`
	SuccessMostSubtle *string `json:"successMostSubtle,omitempty" yaml:"successMostSubtle,omitempty"`

	// Section title and divider line colors.
	SectionTitle     *string `json:"sectionTitle,omitempty" yaml:"sectionTitle,omitempty"`
	SectionLine      *string `json:"sectionLine,omitempty" yaml:"sectionLine,omitempty"`
	SectionSeparator *string `json:"sectionSeparator,omitempty" yaml:"sectionSeparator,omitempty"`

	// Logo configuration for the application logo and sidebar logo.
	Logo *ThemeLogoConfig `json:"logo,omitempty" yaml:"logo,omitempty"`

	// Dialog selected item override colors. These allow the command menu
	// and other list-based dialog items to use a different background/foreground
	// for the highlighted row without affecting the rest of the `primary` token.
	DialogSelectedBackground *string `json:"dialogSelectedBackground,omitempty" yaml:"dialogSelectedBackground,omitempty"`
	DialogSelectedForeground *string `json:"dialogSelectedForeground,omitempty" yaml:"dialogSelectedForeground,omitempty"`
	// Button override colors.
	ButtonFocusedBackground *string `json:"buttonFocusedBackground,omitempty" yaml:"buttonFocusedBackground,omitempty"`
	ButtonFocusedForeground *string `json:"buttonFocusedForeground,omitempty" yaml:"buttonFocusedForeground,omitempty"`
	ButtonBlurredBackground *string `json:"buttonBlurredBackground,omitempty" yaml:"buttonBlurredBackground,omitempty"`
	ButtonBlurredForeground *string `json:"buttonBlurredForeground,omitempty" yaml:"buttonBlurredForeground,omitempty"`
}

// ThemeForProvider returns the Styles associated with the given provider
// ID. Unknown or empty provider IDs yield the default Charmtone Pantera
// theme.
func ThemeForProvider(providerID string) Styles {
	switch providerID {
	case "hyper":
		return HyperphosphorObsidiana()
	default:
		return CharmtonePantera()
	}
}

// Theme returns the Styles configured by the user via themeOption,
// falling back to provider-based default themes if empty or invalid.
func Theme(themeOption string, providerID string, workingDir string) Styles {
	themeOption = strings.TrimSpace(themeOption)
	if themeOption == "" {
		return ThemeForProvider(providerID)
	}

	// Check built-in themes first.
	switch strings.ToLower(themeOption) {
	case "pantera", "charmtone":
		return CharmtonePantera()
	case "obsidiana", "hyper":
		return HyperphosphorObsidiana()
	}

	// Try to resolve file path.
	path := findThemePath(themeOption, workingDir)
	if path == "" {
		slog.Warn("Theme file not found, falling back to default theme", "theme", themeOption)
		return ThemeForProvider(providerID)
	}

	palette, err := loadThemeFile(path)
	if err != nil {
		slog.Warn("Failed to load theme file, falling back to default theme", "path", path, "error", err)
		return ThemeForProvider(providerID)
	}

	// Start with the default provider-based theme as base.
	baseOpts := defaultQuickStyleOpts(providerID)

	// Apply overrides.
	opts := quickStyleOpts{
		primary:           parseColor(palette.Primary, baseOpts.primary),
		secondary:         parseColor(palette.Secondary, baseOpts.secondary),
		accent:            parseColor(palette.Accent, baseOpts.accent),
		keyword:           parseColor(palette.Keyword, baseOpts.keyword),
		fgBase:            parseColor(palette.FgBase, baseOpts.fgBase),
		bgBase:            parseColor(palette.BgBase, baseOpts.bgBase),
		separator:         parseColor(palette.Separator, baseOpts.separator),
		fgSubtle:          parseColor(palette.FgSubtle, baseOpts.fgSubtle),
		fgMoreSubtle:      parseColor(palette.FgMoreSubtle, baseOpts.fgMoreSubtle),
		fgMostSubtle:      parseColor(palette.FgMostSubtle, baseOpts.fgMostSubtle),
		onPrimary:         parseColor(palette.OnPrimary, baseOpts.onPrimary),
		bgMostVisible:     parseColor(palette.BgMostVisible, baseOpts.bgMostVisible),
		bgLessVisible:     parseColor(palette.BgLessVisible, baseOpts.bgLessVisible),
		bgLeastVisible:    parseColor(palette.BgLeastVisible, baseOpts.bgLeastVisible),
		destructive:       parseColor(palette.Destructive, baseOpts.destructive),
		error:             parseColor(palette.Error, baseOpts.error),
		warning:           parseColor(palette.Warning, baseOpts.warning),
		warningSubtle:     parseColor(palette.WarningSubtle, baseOpts.warningSubtle),
		denied:            parseColor(palette.Denied, baseOpts.denied),
		busy:              parseColor(palette.Busy, baseOpts.busy),
		info:              parseColor(palette.Info, baseOpts.info),
		infoMoreSubtle:    parseColor(palette.InfoMoreSubtle, baseOpts.infoMoreSubtle),
		infoMostSubtle:    parseColor(palette.InfoMostSubtle, baseOpts.infoMostSubtle),
		success:           parseColor(palette.Success, baseOpts.success),
		successMoreSubtle: parseColor(palette.SuccessMoreSubtle, baseOpts.successMoreSubtle),
		successMostSubtle: parseColor(palette.SuccessMostSubtle, baseOpts.successMostSubtle),

		sectionTitle: parseColor(palette.SectionTitle, baseOpts.sectionTitle),
		sectionLine:  parseColor(palette.SectionLine, baseOpts.sectionLine),

		sectionSeparator: baseOpts.sectionSeparator,
	}

	s := quickStyle(opts)

	if palette.SectionSeparator != nil {
		s.SectionSeparator = *palette.SectionSeparator
	}

	if palette.DialogSelectedBackground != nil {
		s.Dialog.SelectedItem = s.Dialog.SelectedItem.Background(parseColor(palette.DialogSelectedBackground, baseOpts.primary))
	}
	if palette.DialogSelectedForeground != nil {
		s.Dialog.SelectedItem = s.Dialog.SelectedItem.Foreground(parseColor(palette.DialogSelectedForeground, baseOpts.onPrimary))
	}

	if palette.ButtonFocusedBackground != nil {
		s.Button.Focused = s.Button.Focused.Background(parseColor(palette.ButtonFocusedBackground, baseOpts.secondary))
	}
	if palette.ButtonFocusedForeground != nil {
		s.Button.Focused = s.Button.Focused.Foreground(parseColor(palette.ButtonFocusedForeground, baseOpts.onPrimary))
	}
	if palette.ButtonBlurredBackground != nil {
		s.Button.Blurred = s.Button.Blurred.Background(parseColor(palette.ButtonBlurredBackground, baseOpts.bgLessVisible))
	}
	if palette.ButtonBlurredForeground != nil {
		s.Button.Blurred = s.Button.Blurred.Foreground(parseColor(palette.ButtonBlurredForeground, baseOpts.fgBase))
	}

	// Apply logo configuration from theme if present.
	if palette.Logo != nil {
		if palette.Logo.AppTitle != "" {
			s.LogoConfig.AppTitle = palette.Logo.AppTitle
		}
		if palette.Logo.FigletFont != "" {
			s.LogoConfig.FigletFont = palette.Logo.FigletFont
		}
		s.LogoConfig.FigletSolid = palette.Logo.FigletSolid
		if palette.Logo.SidebarLogoType != "" {
			s.LogoConfig.SidebarLogoType = palette.Logo.SidebarLogoType
		}
		if palette.Logo.SidebarFigletFont != "" {
			s.LogoConfig.SidebarFigletFont = palette.Logo.SidebarFigletFont
		}
	}

	return s
}

// CharmtonePantera returns the Charmtone dark theme. It's the default style
// for the UI.
func CharmtonePantera() Styles {
	return quickStyle(panteraOpts())
}

// HyperphosphorObsidiana returns the Hyperphosphor dark theme.
func HyperphosphorObsidiana() Styles {
	return quickStyle(obsidianaOpts())
}

func defaultQuickStyleOpts(providerID string) quickStyleOpts {
	if providerID == "hyper" {
		return obsidianaOpts()
	}
	return panteraOpts()
}

func panteraOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   charmtone.Charple,
		secondary: charmtone.Dolly,
		accent:    charmtone.Bok,
		keyword:   charmtone.Blush,

		fgBase:       charmtone.Sash,
		fgMoreSubtle: charmtone.Squid,
		fgSubtle:     charmtone.Smoke,
		fgMostSubtle: charmtone.Oyster,

		onPrimary: charmtone.Butter,

		bgBase:         charmtone.Pepper,
		bgLeastVisible: charmtone.BBQ,
		bgLessVisible:  charmtone.Char,
		bgMostVisible:  charmtone.Iron,

		separator: charmtone.Char,

		destructive:       charmtone.Coral,
		error:             charmtone.Sriracha,
		warningSubtle:     charmtone.Zest,
		warning:           charmtone.Mustard,
		denied:            charmtone.Tang,
		busy:              charmtone.Citron,
		info:              charmtone.Malibu,
		infoMoreSubtle:    charmtone.Sardine,
		infoMostSubtle:    charmtone.Damson,
		success:           charmtone.Julep,
		successMoreSubtle: charmtone.Bok,
		successMostSubtle: charmtone.Guac,

		sectionTitle:     charmtone.Oyster,
		sectionLine:      charmtone.Char,
		sectionSeparator: SectionSeparator,
	}
}

func obsidianaOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   charmtone.Charple,
		secondary: charmtone.Dolly,
		accent:    charmtone.Bok,

		fgBase:       charmtone.Sash,
		fgMoreSubtle: charmtone.Squid,
		fgSubtle:     charmtone.Smoke,
		fgMostSubtle: charmtone.Oyster,

		onPrimary: charmtone.Butter,

		bgBase:         charmtone.Pepper,
		bgLeastVisible: charmtone.BBQ,
		bgLessVisible:  charmtone.Char,
		bgMostVisible:  charmtone.Iron,

		separator: charmtone.Char,

		destructive:       charmtone.Coral,
		error:             charmtone.Sriracha,
		warningSubtle:     charmtone.Zest,
		warning:           charmtone.Mustard,
		denied:            charmtone.Tang,
		busy:              charmtone.Citron,
		info:              charmtone.Malibu,
		infoMoreSubtle:    charmtone.Sardine,
		infoMostSubtle:    charmtone.Damson,
		success:           charmtone.Julep,
		successMoreSubtle: charmtone.Bok,
		successMostSubtle: charmtone.Guac,

		sectionTitle:     charmtone.Oyster,
		sectionLine:      charmtone.Char,
		sectionSeparator: SectionSeparator,
	}
}

func globalThemesDir() string {
	if phosphorGlobal := os.Getenv("PHOSPHOR_GLOBAL_CONFIG"); phosphorGlobal != "" {
		return filepath.Join(phosphorGlobal, "themes")
	}
	return filepath.Join(home.Config(), "phosphor", "themes")
}

func findThemePath(themeOption string, workingDir string) string {
	if themeOption == "" {
		return ""
	}

	// 1. If it looks like a path (absolute, starts with `./`, `../`, or has path separators).
	if filepath.IsAbs(themeOption) ||
		strings.HasPrefix(themeOption, "./") ||
		strings.HasPrefix(themeOption, "../") ||
		strings.Contains(themeOption, string(filepath.Separator)) ||
		strings.Contains(themeOption, "/") {
		if !filepath.IsAbs(themeOption) && workingDir != "" {
			return filepath.Join(workingDir, themeOption)
		}
		return themeOption
	}

	// 2. Otherwise search candidate directories.
	var candidates []string
	extensions := []string{".yaml", ".yml", ".json"}

	if workingDir != "" {
		localPhosphorThemesDir := filepath.Join(workingDir, ".phosphor", "themes")
		for _, ext := range extensions {
			candidates = append(candidates, filepath.Join(localPhosphorThemesDir, themeOption+ext))
		}
		localThemesDir := filepath.Join(workingDir, "themes")
		for _, ext := range extensions {
			candidates = append(candidates, filepath.Join(localThemesDir, themeOption+ext))
		}
	}

	globalDir := globalThemesDir()
	for _, ext := range extensions {
		candidates = append(candidates, filepath.Join(globalDir, themeOption+ext))
	}

	// Also support the exact name in case it includes extension.
	if workingDir != "" {
		candidates = append(candidates, filepath.Join(workingDir, ".phosphor", "themes", themeOption))
		candidates = append(candidates, filepath.Join(workingDir, "themes", themeOption))
	}
	candidates = append(candidates, filepath.Join(globalDir, themeOption))

	// Return first that exists.
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return ""
}

func loadThemeFile(path string) (ThemePalette, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ThemePalette{}, err
	}

	var p ThemePalette
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		if err := json.Unmarshal(data, &p); err != nil {
			return ThemePalette{}, err
		}
	} else {
		if err := yaml.Unmarshal(data, &p); err != nil {
			return ThemePalette{}, err
		}
	}
	return p, nil
}

func parseColor(s *string, def color.Color) color.Color {
	if s == nil || *s == "" {
		return def
	}
	return lipgloss.Color(*s)
}
