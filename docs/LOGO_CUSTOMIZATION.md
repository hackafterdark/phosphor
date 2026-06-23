# Logo Customization

Logo branding is configured within **theme YAML files** under the `logo` block. This keeps visual identity (colors, fonts, branding) bundled together per theme.

See [Theme System](themes/README.md) for how themes work and how they are resolved.

## Configuration

Logo settings go in your theme file (e.g. `.phosphor/themes/mytheme.yaml`). The `logo` block controls branding for both the startup screen and the sidebar:

```yaml
logo:
  app_title: "Phosphor"
  figlet_font: "Pagga"
  figlet_solid: false
  sidebar_logo_type: "figlet"
  sidebar_figlet_font: "Pagga"
```

### `logo.app_title`

Sets the text displayed as the logo on the startup screen and in the sidebar.

```yaml
logo:
  app_title: "PHOSPHOR"
```

### `logo.sidebar_logo_type`

Controls how the logo appears in the sidebar. Accepts three values:

- **`figlet`** (default): Renders the logo using the specified FIGlet font, falling back to plain text if the sidebar is too narrow.
- **`plain_text`**: Always displays the logo as a single line of plain text (e.g., "Phosphor").
- **`hidden`**: Hides the logo entirely in the sidebar.

```yaml
logo:
  sidebar_logo_type: "plain_text"
```

### `logo.sidebar_figlet_font`

Specifies a separate FIGlet font for the sidebar logo. If omitted, the sidebar uses the same font as the startup screen (`logo.figlet_font`). Note: If the logo does not fit into the sidebar due to the size of the figlet font, it will automatically fall back to plain text.

```yaml
logo:
  sidebar_logo_type: "figlet"
  sidebar_figlet_font: "Doom"
```

### `logo.app_title`

Sets the text displayed as the logo on the startup screen and in the sidebar.

```yaml
logo:
  app_title: "PHOSPHOR"
```

### `logo.figlet_font`

Specifies which Figlet font to use when rendering the startup screen logo. Accepts either:

- A **built-in font name** (see list below)
- An **absolute path** to a `.flf` file for dynamic loading

```yaml
logo:
  figlet_font: "Doom"
```

### `logo.figlet_solid`

A boolean setting that controls how internal characters are rendered in FIGlet fonts.

- `true`: Renders internal glyph characters using solid block characters (`█`). This creates a solid, modern, filled appearance that works exceptionally well with shadow or block-style fonts.
- `false` (default): Renders the font's original glyph characters (e.g., `@` in the `Fraktur` font). This preserves the traditional ASCII style intended by the font author.

```yaml
logo:
  figlet_font: "Poison"
  figlet_solid: true
```

## Included Fonts

The following Figlet fonts are bundled with Phosphor:

| Font Name | Description |
|---|---|
| `ANSI_Compact` | Compact ASCII art style |
| `Big_Money-ne` | Bold, money-bag style letters |
| `BlurVision_ASCII` | Blurred, retro ASCII look |
| `Digital` | Clean, digital-clock style letters |
| `Doom` | Blocky, DOOM-inspired lettering |
| `Fraktur` | Decorative, gothic/Fraktur style letters |
| `Pagga` | Clean, pagoda-style block letters |
| `Poison` | Sleek, venomous font styling |

## Previewing Fonts

You can preview how different Figlet fonts look before applying them:

- **Online preview tool:** [patorjk.com/software/taag](https://patorjk.com/software/taag/) — type your text and browse through available fonts live in the browser.

## Installing Custom Fonts

To use a font not included by default:

1. Browse and download `.flf` font files from the [figlet.js repository](https://github.com/patorjk/figlet.js).
2. Save the `.flf` file to any location on your system.
3. Set `figlet_font` to the absolute path of the file in your theme YAML.

Example:

```yaml
logo:
  app_title: "PHOSPHOR"
  figlet_font: "C:/Users/yourname/fonts/Slant.flf"
```

Restart Phosphor to see the changes take effect.
