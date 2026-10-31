package common

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	xstrings "github.com/charmbracelet/x/exp/strings"
	"go.opentelemetry.io/otel/attribute"

	"github.com/hackafterdark/phosphor/internal/ui/notification"
	"github.com/hackafterdark/phosphor/pkg/otel"
)

// Capabilities define different terminal capabilities supported.
type Capabilities struct {
	// Profile is the terminal color profile used to determine how colors are
	// rendered.
	Profile colorprofile.Profile
	// Columns is the number of character columns in the terminal.
	Columns int
	// Rows is the number of character rows in the terminal.
	Rows int
	// PixelX is the width of the terminal in pixels.
	PixelX int
	// PixelY is the height of the terminal in pixels.
	PixelY int
	// KittyGraphics indicates whether the terminal supports the Kitty graphics
	// protocol.
	KittyGraphics bool
	// SixelGraphics indicates whether the terminal supports Sixel graphics.
	SixelGraphics bool
	// Env is the terminal environment variables.
	Env uv.Environ
	// TerminalVersion is the terminal version string.
	TerminalVersion string
	// ReportFocusEvents indicates whether the terminal supports focus events.
	ReportFocusEvents bool
	// OSC99Notifications indicates whether the terminal supports OSC 99 notifications.
	OSC99Notifications bool
}

// Update updates the capabilities based on the given message.
func (c *Capabilities) Update(msg any) {
	switch m := msg.(type) {
	case tea.EnvMsg:
		c.Env = uv.Environ(m)
	case tea.ColorProfileMsg:
		c.Profile = m.Profile
	case tea.WindowSizeMsg:
		c.Columns = m.Width
		c.Rows = m.Height
	case uv.PixelSizeEvent:
		c.PixelX = m.Width
		c.PixelY = m.Height
	case uv.KittyGraphicsEvent:
		c.KittyGraphics = true
	case uv.PrimaryDeviceAttributesEvent:
		if slices.Contains(m, 4) {
			c.SixelGraphics = true
		}
	case tea.TerminalVersionMsg:
		c.TerminalVersion = m.Name
	case tea.ModeReportMsg:
		switch m.Mode {
		case ansi.ModeFocusEvent:
			c.ReportFocusEvents = modeSupported(m.Value)
		}
	case uv.UnknownOscEvent:
		if notification.DetectOSC99Support(string(m)) {
			c.OSC99Notifications = true
		}
	}
}

// QueryCmd returns a [tea.Cmd] that queries the terminal for different
// capabilities.
func QueryCmd(env uv.Environ) tea.Cmd {
	var sb strings.Builder
	sb.WriteString(ansi.RequestPrimaryDeviceAttributes)
	sb.WriteString(ansi.QueryModifyOtherKeys)
	sb.WriteString(ansi.RequestModeFocusEvent)
	sb.WriteString(notification.OSC99QuerySequence())

	// Queries that should only be sent to "smart" normal terminals.
	if shouldQueryCapabilities(env) {
		sb.WriteString(ansi.RequestNameVersion)
		sb.WriteString(ansi.WindowOp(14)) // Window size in pixels
	}

	// The Kitty graphics probe is queried separately because terminals
	// listed in kittyTerminals (e.g. Alacritty) answer XTVERSION but do
	// not implement the graphics protocol. Sending the probe to such a
	// terminal leaks its query text onto the screen as literal garble.
	if supports, reason := supportsKittyGraphicsQuery(env); supports {
		kittyReq := ansi.KittyGraphics([]byte("AAAA"), "i=31", "s=1", "v=1", "a=q", "t=d", "f=24")
		if _, isTmux := env.LookupEnv("TMUX"); isTmux {
			kittyReq = ansi.TmuxPassthrough(kittyReq)
		}
		sb.WriteString(kittyReq)
	} else {
		termType := env.Getenv("TERM")
		slog.Debug("Skipped Kitty graphics capability probe",
			"term", termType, "reason", reason)
		_, span := otel.StartSpan(context.Background(), "terminal.capability_probe")
		span.SetAttributes(
			attribute.String("terminal.term", termType),
			attribute.Bool("terminal.kitty_graphics_probed", false),
			attribute.String("terminal.skip_reason", reason),
		)
		span.End()
	}

	return tea.Raw(sb.String())
}

// SupportsTrueColor returns true if the terminal supports true color.
func (c Capabilities) SupportsTrueColor() bool {
	return c.Profile == colorprofile.TrueColor
}

// SupportsKittyGraphics returns true if the terminal supports Kitty graphics.
func (c Capabilities) SupportsKittyGraphics() bool {
	return c.KittyGraphics
}

// SupportsSixelGraphics returns true if the terminal supports Sixel graphics.
func (c Capabilities) SupportsSixelGraphics() bool {
	return c.SixelGraphics
}

// CellSize returns the size of a single terminal cell in pixels.
func (c Capabilities) CellSize() (width, height int) {
	if c.Columns == 0 || c.Rows == 0 {
		return 0, 0
	}
	return c.PixelX / c.Columns, c.PixelY / c.Rows
}

func modeSupported(v ansi.ModeSetting) bool {
	return v.IsSet() || v.IsReset()
}

// kittyTerminals defines terminals supporting querying capabilities.
var kittyTerminals = []string{"alacritty", "ghostty", "kitty", "rio", "wezterm"}

// kittyGraphicsTerminals defines terminals known to implement the Kitty
// graphics protocol. Alacritty is deliberately absent: it answers XTVERSION
// but ignores the graphics protocol, and its parser leaks the base64 payload
// of unsupported DCS sequences onto the screen.
var kittyGraphicsTerminals = []string{"kitty", "ghostty", "rio", "wezterm"}

// supportsKittyGraphicsQuery reports whether the Kitty graphics probe can be
// sent safely. When it returns false, the second return value explains why.
func supportsKittyGraphicsQuery(env uv.Environ) (bool, string) {
	if _, isTmux := env.LookupEnv("TMUX"); isTmux {
		// tmux does not forward Kitty graphics queries reliably; the
		// passthrough wrapper reaches the outer terminal, which may not
		// support the protocol at all.
		return false, "tmux"
	}
	termType := env.Getenv("TERM")
	if !xstrings.ContainsAnyOf(termType, kittyGraphicsTerminals...) {
		return false, "unsupported terminal"
	}
	return true, ""
}

func shouldQueryCapabilities(env uv.Environ) bool {
	const osVendorTypeApple = "Apple"
	termType := env.Getenv("TERM")
	termProg, okTermProg := env.LookupEnv("TERM_PROGRAM")
	_, okSSHTTY := env.LookupEnv("SSH_TTY")
	if okTermProg && strings.Contains(termProg, osVendorTypeApple) {
		return false
	}
	return (!okTermProg && !okSSHTTY) ||
		(!strings.Contains(termProg, osVendorTypeApple) && !okSSHTTY) ||
		// Terminals that do support XTVERSION.
		xstrings.ContainsAnyOf(termType, kittyTerminals...)
}
