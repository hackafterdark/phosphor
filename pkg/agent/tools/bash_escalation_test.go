package tools

import (
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestIsSandboxDenial(t *testing.T) {
	t.Parallel()

	// The kernel tier marks its refusal explicitly.
	require.True(t, isSandboxDenial("phosphor: sandbox refused exec: platform sandbox unavailable", 126, true))

	// The in-process path guard reports out-of-workspace operands.
	require.True(t, isSandboxDenial("Security violation: operand \"/etc/shadow\" is outside workspace", 1, true))

	// Exit 126 alone (sandbox active) counts: the wrapper refuses before
	// the child runs, so stderr text may be absent.
	require.True(t, isSandboxDenial("", 126, true))

	// Sandbox off: no output is ever attributed to the sandbox.
	require.False(t, isSandboxDenial("outside workspace", 1, false))
	require.False(t, isSandboxDenial("", 126, false))

	// Ordinary failures are not sandbox denials — an EACCES from the
	// command itself must not route through the escalation prompt.
	require.False(t, isSandboxDenial("gcc: error: permission denied", 1, true))
}

func TestAppendSandboxGuidance(t *testing.T) {
	t.Parallel()

	guided := appendSandboxGuidance("denied: outside workspace", 1, true, false)
	require.Contains(t, guided, sandboxDenialSentinel)

	// Guidance is never stacked on a repeated denial.
	again := appendSandboxGuidance(guided, 1, true, false)
	require.Equal(t, guided, again)

	// No guidance for successful or non-denial output.
	require.Equal(t, "ok", appendSandboxGuidance("ok", 0, true, false))
	require.Equal(t, "boom", appendSandboxGuidance("boom", 1, true, false))

	// An escalated run is unsandboxed: nothing to escalate to.
	require.Equal(t, "denied: outside workspace",
		appendSandboxGuidance("denied: outside workspace", 1, true, true))
}

func TestSandboxDenialRequested(t *testing.T) {
	t.Parallel()

	require.True(t, sandboxDenialRequested(BashParams{Sandbox: "escalate"}))
	require.True(t, sandboxDenialRequested(BashParams{Sandbox: "ESCALATE"}))
	require.False(t, sandboxDenialRequested(BashParams{}))
	require.False(t, sandboxDenialRequested(BashParams{Sandbox: "workspace"}))
}

func TestBashDescriptionSandboxGuidanceGated(t *testing.T) {
	t.Parallel()

	// The escalation contract is only advertised when the sandbox is on.
	off := bashDescription("/ws", config.ToolBash{SandboxMode: "off"}, nil, "test-model")
	require.NotContains(t, off, "sandbox=\"escalate\"")

	on := bashDescription("/ws", config.ToolBash{SandboxMode: "workspace"}, nil, "test-model")
	require.Contains(t, on, "sandbox=\"escalate\"")
	require.True(t, strings.Contains(on, "never request escalation preemptively"))
}
