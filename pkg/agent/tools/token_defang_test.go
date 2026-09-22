package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStripDeviceControls proves the VT100 device-control sequences that can
// actuate the terminal (OSC clipboard/title writes, the DCS/Sixel family, the
// 8-bit C1 controls, DEL, stray C0) are removed while ordinary colour and
// cursor CSI output is preserved byte-for-byte.
func TestStripDeviceControls(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "clean text untouched",
			in:   "go test ./... ok  pkg/egress 1.11s",
			want: "go test ./... ok  pkg/egress 1.11s",
		},
		{
			name: "OSC-52 clipboard write dropped",
			in:   "\x1b]52;;c3RjT78=\x07echo hi",
			want: "echo hi",
		},
		{
			name: "OSC terminated by ST dropped",
			in:   "\x1b]0;evil title\x1b\\keep",
			want: "keep",
		},
		{
			name: "DCS/Sixel family dropped",
			in:   "pre\x1bP0;q=256#G01256\x1b\\post",
			want: "prepost",
		},
		{
			name: "CSI colour preserved verbatim",
			in:   "\x1b[31mred\x1b[0m done",
			want: "\x1b[31mred\x1b[0m done",
		},
		{
			name: "DEL removed",
			in:   "a\x7fb",
			want: "ab",
		},
		{
			name: "8-bit C1 removed",
			in:   "a" + string([]rune{0x9c}) + "b",
			want: "ab",
		},
		{
			name: "lone ESC before unknown introducer dropped, text kept",
			in:   "x\x1bYtail",
			want: "xYtail",
		},
		{
			name: "whitespace controls preserved, other C0 stripped",
			in:   "a\tb\nc\r\td\x00\x01",
			want: "a\tb\nc\r\td",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, StripDeviceControls(tc.in))
		})
	}
}

// TestStripDeviceControls_Idempotent guards that running the strip twice is a
// no-op relative to once, so a value can never be re-weaponized downstream.
func TestStripDeviceControls_Idempotent(t *testing.T) {
	t.Parallel()

	in := "build \x1b]52;;payload\x07\x1b[1;32mcompiled\x1b[0m\x7f"
	once := StripDeviceControls(in)
	twice := StripDeviceControls(once)
	require.Equal(t, once, twice)
	require.NotContains(t, once, "\x1b]")
	require.NotContains(t, once, "\x7f")
	require.Contains(t, once, "compiled")
}

// TestStripDeviceControls_NoOsc52Survives is the concrete OSC-52 regression:
// attacker-controlled bytes must not be able to plant a clipboard payload.
func TestStripDeviceControls_NoOsc52Survives(t *testing.T) {
	t.Parallel()

	payload := "attacker-payload"
	out := StripDeviceControls("log line\n\x1b]52;0;" + payload + "\x07\nnext")
	require.NotContains(t, out, payload)
	require.False(t, strings.Contains(out, "\x1b]52"))
}

// TestStripDeviceControls_RawC1ByteDoesNotSurvive is the raw-byte regression: a
// bare 0x9D byte is invalid UTF-8, so the rune-based fast-path predicate never
// sees the C1 value and would return the input untouched, handing the terminal a
// live 8-bit control introducer. Invalid UTF-8 must therefore take the scanner
// path, where the byte collapses to an inert replacement character.
func TestStripDeviceControls_RawC1ByteDoesNotSurvive(t *testing.T) {
	t.Parallel()

	in := string([]byte{'a', 0x9d, 'b'})
	out := StripDeviceControls(in)
	require.NotContains(t, out, string([]byte{0x9d}))
	require.Equal(t, "a\uFFFDb", out)

	// Idempotent: the replacement character is plain printable text.
	require.Equal(t, out, StripDeviceControls(out))
}
