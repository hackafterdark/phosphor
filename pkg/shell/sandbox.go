package shell

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/hackafterdark/phosphor/pkg/egress"
	"github.com/hackafterdark/phosphor/pkg/sandbox"
	"mvdan.cc/sh/v3/interp"
)

// SandboxDegradedWarning is surfaced in tool output when kernel sandboxing
// was requested but the host could not apply the full policy. The kernel
// boundary may still cover some tiers (e.g. filesystem but not network);
// the warning makes the weaker posture visible to the model and the user
// instead of degrading silently.
const SandboxDegradedWarning = "kernel sandbox partially unavailable on this host; " +
	"the requested policy could not be fully attached (sandbox_mode=workspace). " +
	"Set sandbox_mode=strict to refuse exec when the sandbox cannot attach."

// sandboxPolicy carries the kernel-sandbox decision for one shell instance.
// A nil policy means sandboxing is off; trusted surfaces (hooks, value
// expansion) keep their unrestricted behavior.
type sandboxPolicy struct {
	mode string
	spec sandbox.Spec
	// degraded is set the first time confinement finds the primitives
	// unavailable in workspace mode; callers surface SandboxDegradedWarning
	// once the command output is reported.
	degraded bool
}

// newSandboxPolicy builds the policy for a shell. Returns nil when
// sandboxing is off, invalid, or no workspace is configured (hooks run
// unsandboxed by invariant).
func newSandboxPolicy(mode, workspace string, extraRoots []string, limits sandbox.ResourceLimits) *sandboxPolicy {
	if !sandbox.ModeEnabled(mode) || workspace == "" {
		return nil
	}
	p := &sandboxPolicy{
		mode: mode,
		spec: sandbox.SpecForLimits(workspace, extraRoots, limits),
	}
	// ProxyOnly network tier: when the egress broker is routing
	// subprocesses, the kernel policy makes it the only egress path
	// (invariant 6). The proxy env injected into the child stays
	// cooperative-client UX, never enforcement.
	if port, ok := egress.RoutedBrokerPort(); ok {
		p.spec.Network = sandbox.NetworkProxyOnly
		p.spec.ProxyPort = port
	}
	// Availability is static for the host's kernel, so the degraded
	// posture is known up front and can be surfaced to the model even
	// before the first exec.
	if ok, _ := sandbox.Available(); !ok && strings.EqualFold(mode, "workspace") {
		p.degraded = true
	}
	return p
}

// SandboxRefusalMarker prefixes the stderr line written when the kernel
// sandbox refuses an exec (strict mode, exit status 126). The bash tool
// recognizes this marker to distinguish sandbox denials from ordinary
// command failures.
const SandboxRefusalMarker = "phosphor: sandbox refused exec"

// confineProcess wraps cmd's argv through the kernel sandbox before exec.
// Returns a non-nil interp error (exit status 126) when strict mode refuses
// the exec because enforcement cannot attach; workspace mode degrades to
// in-process guards and records the fact for the caller to surface.
func confineProcess(cmd *exec.Cmd, sbx *sandboxPolicy, stderr io.Writer) error {
	if sbx == nil {
		return nil
	}
	degraded, err := sandbox.Confine(cmd, sbx.mode, sbx.spec)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", SandboxRefusalMarker, err)
		return interp.ExitStatus(126)
	}
	if degraded && !sbx.degraded {
		sbx.degraded = true
		slog.Warn("Kernel sandbox unavailable; running with in-process guards only",
			"mode", sbx.mode, "workspace", sbx.spec.WorkspaceRoot)
	}
	return nil
}

// escapeHatchHandler returns middleware that denies host escape-hatch
// binaries (wsl, cmd, powershell) and inline shell execution (sh -c etc.)
// while the sandbox is active. Kernel confinement cannot reach a payload
// that crosses kernel boundaries (wsl) or hides inside a quoted inline
// argument, so the control is deny-by-default: the refusal carries the
// sandbox refusal marker and exit status 126, which the bash tool
// recognizes as a sandbox denial and pairs with the escalation guidance.
// An escalated run (sandbox policy nil) skips the gate entirely — the user
// grant for an unsandboxed run is the only way through.
func escapeHatchHandler(sbx *sandboxPolicy) func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		return func(ctx context.Context, args []string) error {
			if sbx == nil || len(args) == 0 {
				return next(ctx, args)
			}
			if reason := sandbox.EscapeHatchReason(args); reason != "" {
				hc := interp.HandlerCtx(ctx)
				fmt.Fprintf(hc.Stderr, "%s: %s\n", SandboxRefusalMarker, reason)
				slog.Warn("Escape-hatch command denied by sandbox",
					"argv0", args[0], "reason", reason, "mode", sbx.mode)
				return interp.ExitStatus(126)
			}
			return next(ctx, args)
		}
	}
}
