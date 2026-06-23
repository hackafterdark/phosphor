# ADR: Configurable TUI Themes, Layout, and Logo Branding

## Status

Accepted

## Context

Phosphor is designed as a highly personalizable agent — something users can fork, configure, and make their own. Before these changes, the TUI had a single hardcoded color scheme (the "Charmtone" theme) and a fixed layout for the landing screen and sidebar. Visual identity (logo text, FIGlet font, sidebar branding) was also scattered across config options rather than bundled into a coherent visual identity. This made it difficult for users to create distinct "flavors" without touching Go source files.

The motivation was threefold:

1. **Theming** — Allow users to define complete color palettes in external YAML/JSON files, with support for workspace-level and global themes that can be shared across teams via Git.
2. **Layout** — Give users control over which components appear on the landing screen and sidebar, their display order, and visibility.
3. **Logo Branding** — Consolidate visual identity (app title, FIGlet fonts, sidebar logo style) into the theme system so that branding travels with the palette.

## Decision

We introduced three interconnected customization systems:

### 1. Theme System with External Files

Users define color palettes in YAML or JSON files placed in `.phosphor/themes/` or `themes/`. A tiered lookup strategy resolves theme names:

1. Direct path (if the setting contains path separators).
2. Workspace-local directories (`.phosphor/themes/`, `themes/`).
3. Global user config (`~/.config/phosphor/themes/` or `$PHOSPHOR_GLOBAL_CONFIG/themes/`).

Themes define a comprehensive set of palette keys covering brand colors, foreground/background defaults, subtle typography, panel backgrounds, and status indicators. Themes can also define a `logo` block for visual branding.

### 2. Configurable UI Layout

Landing screen and sidebar components are controlled via `options.tui.landing` and `options.tui.sidebar` in `phosphor.json`. Components are defined as an array where position determines display order. Each component supports `hidden` and `max_items` properties. The landing screen is responsive — it adapts to terminal width via `max_columns`, `gap`, and `vertical_gap` settings.

### 3. Logo Branding within Themes

Logo settings moved from `options.tui` into the theme `logo` block. This keeps visual identity bundled with the color palette. Settings include `app_title`, `figlet_font`, `figlet_solid`, `sidebar_logo_type`, and `sidebar_figlet_font`. The system ships with 8 built-in FIGlet fonts and supports custom `.flf` files via absolute path.

### Why This Approach

1. **Low barrier to entry**: Themes use simple YAML/JSON files — no Go code required for basic customization.
2. **Composable**: Layout config lives in `phosphor.json` (structural), while themes live in YAML files (visual). Together they let users build complete "flavors."
3. **Team-friendly**: Workspace-local themes can be checked into Git, allowing shared visual identity across development teams.
4. **Backward compatible**: All settings have sensible defaults. Existing users see zero change until they opt in.
5. **Extensible for forks**: The palette key model is explicit and documented — forkers can add new keys or override existing ones without touching the core styling engine.
6. **No runtime restart needed for layouts**: Layout changes only require a restart, which is acceptable for a mostly-static TUI.

### Alternatives Considered

- **Programmatic theme API (Go plugins)**: Overkill for a TUI agent — most users prefer declarative config over compiled plugins.
- **Single monolithic config file**: Bundling themes inside `phosphor.json` makes files verbose. Separate YAML files are easier to maintain and share.
- **CSS custom properties (e.g., `--color-primary`)**: More familiar to web developers, but less natural for terminal users. Semantic key names (`primary`, `bgBase`, `success`) are more intuitive.

### Consequences

- Users can create and share complete visual themes without modifying Go source files.
- Workspace teams can share themes via Git by checking in `.phosphor/themes/` or `themes/` directories.
- The styling system has a two-tier architecture: `quickStyle.go` builds a token-driven base, and theme files provide concrete color values and overrides.
- Layout changes require a restart of Phosphor to take effect.
- Adding new palette keys requires updating both the theme YAML schema and the `Styles` struct.

### Files Changed

| File | Change |
|---|---|
| `internal/ui/styles/themes.go` | Theme loading, palette resolution, and override system |
| `internal/ui/styles/quickstyle.go` | Token-driven base theme builder |
| `internal/config/config.go` | Added `TUIOptions` fields for theme, landing, and sidebar config |
| `internal/ui/model/ui.go` | Wired theme and layout config into UI initialization |
| `docs/THEMES.md` | User-facing theme system documentation |
| `docs/UI_LAYOUT_CONFIG.md` | User-facing layout configuration documentation |
| `docs/LOGO_CUSTOMIZATION.md` | User-facing logo branding documentation |
