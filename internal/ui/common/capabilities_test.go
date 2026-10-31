package common

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

// kittyQueryMarker is the parameter payload of the Kitty graphics capability
// probe. Terminals without graphics support can't consume the DCS sequence
// and leak its payload onto the screen as literal text, so the probe must
// never be sent to them.
const kittyQueryMarker = "i=31,s=1,v=1,a=q,t=d,f=24"

// xtversionQueryMarker is the XTVERSION query, supported by all terminals
// that capability probing targets.
const xtversionQueryMarker = "\x1b[>q"

func queryPayload(t *testing.T, env uv.Environ) string {
	t.Helper()
	msg := QueryCmd(env)()
	switch m := msg.(type) {
	case string:
		return m
	case tea.RawMsg:
		s, ok := m.Msg.(string)
		require.True(t, ok, "raw message payload is not a string: %T", m.Msg)
		return s
	default:
		t.Fatalf("unexpected query message type %T", msg)
		return ""
	}
}

func TestQueryCmd_SkipsKittyProbeOnAlacritty(t *testing.T) {
	t.Parallel()

	payload := queryPayload(t, uv.Environ{"TERM=alacritty"})
	require.NotContains(t, payload, kittyQueryMarker)
	// The XTVERSION query is still sent, it is supported by Alacritty.
	require.Contains(t, payload, xtversionQueryMarker)
}

func TestQueryCmd_SendsKittyProbeOnSupportedTerminals(t *testing.T) {
	t.Parallel()

	for _, term := range []string{"kitty", "xterm-ghostty", "rio", "wezterm"} {
		t.Run(term, func(t *testing.T) {
			t.Parallel()
			payload := queryPayload(t, uv.Environ{"TERM=" + term})
			require.Contains(t, payload, kittyQueryMarker)
		})
	}
}

func TestQueryCmd_SkipsKittyProbeUnderTmux(t *testing.T) {
	t.Parallel()

	payload := queryPayload(t, uv.Environ{
		"TERM=xterm-ghostty",
		"TMUX=/tmp/tmux-1000/default,1234,0",
	})
	require.NotContains(t, payload, kittyQueryMarker)
}

func TestQueryCmd_SkipsKittyProbeWhenTermUnset(t *testing.T) {
	t.Parallel()

	payload := queryPayload(t, uv.Environ{})
	require.NotContains(t, payload, kittyQueryMarker)
	// Capability queries unrelated to graphics still run.
	require.Contains(t, payload, xtversionQueryMarker)
}

func TestSupportsKittyGraphicsQuery_Reasons(t *testing.T) {
	t.Parallel()

	ok, reason := supportsKittyGraphicsQuery(uv.Environ{"TERM=alacritty"})
	require.False(t, ok)
	require.Equal(t, "unsupported terminal", reason)

	ok, reason = supportsKittyGraphicsQuery(uv.Environ{
		"TERM=kitty",
		"TMUX=/tmp/tmux-1000/default,1234,0",
	})
	require.False(t, ok)
	require.Equal(t, "tmux", reason)

	ok, reason = supportsKittyGraphicsQuery(uv.Environ{"TERM=xterm-ghostty"})
	require.True(t, ok)
	require.Empty(t, reason)
}

func TestQueryCmd_GarbleNeverAppears(t *testing.T) {
	t.Parallel()

	// The on-screen garble is the base64 payload of the probe leaking as
	// literal text; assert the encoded parameters are absent for every
	// terminal that does not support the graphics protocol.
	for _, term := range []string{"alacritty", "xterm-256color", "dumb", ""} {
		payload := queryPayload(t, uv.Environ{"TERM=" + term})
		require.False(t, strings.Contains(payload, kittyQueryMarker),
			"TERM=%q payload must not contain the Kitty graphics probe", term)
	}
}
