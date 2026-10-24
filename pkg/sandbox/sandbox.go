// Package sandbox provides kernel-enforced sandboxing of child processes
// using native OS primitives (Landlock+seccomp on Linux, Seatbelt on macOS,
// restricted tokens on Windows). See _PLANS/OS_SANDBOXING_PACKAGES.md.
package sandbox

import "os/exec"

// NetworkMode selects kernel-enforced network posture for a confined child.
type NetworkMode int

const (
	// NetworkAllow keeps today's behavior: no kernel network policy.
	NetworkAllow NetworkMode = iota
	// NetworkDeny blocks all egress from the child.
	NetworkDeny
	// NetworkProxyOnly allows egress only to the loopback egress broker on
	// ProxyPort. The proxy env stays cooperative-client UX; the kernel
	// policy is the enforcement (invariant 6).
	NetworkProxyOnly
)

// Spec is the OS-agnostic sandbox policy. It is derived from config and
// pathguard.Confinement so kernel policy and in-process policy can never
// diverge silently.
type Spec struct {
	// WorkspaceRoot is always readable and writable.
	WorkspaceRoot string
	// ReadOnlyRoots are additional paths the child may read and execute.
	ReadOnlyRoots []string
	// ReadWriteRoots are additional paths the child may read and write.
	ReadWriteRoots []string
	// ReadOnlyFiles and ReadWriteFiles carry typed file rules for
	// well-known exceptions (e.g. /dev/null must be writable or most
	// tools break; ~/.gitconfig must be readable or git fails fatally).
	ReadOnlyFiles  []string
	ReadWriteFiles []string
	// Network selects the kernel network posture. Zero value is
	// NetworkUnrestricted, which keeps today's behavior.
	Network NetworkMode
	// ProxyPort is the broker port when Network is NetworkProxyOnly.
	ProxyPort int
	// Limits carries optional kernel resource caps. Zero values mean
	// "no limit"; limits are opt-in and validated at Confine time.
	Limits ResourceLimits
}

// Isolator applies a Spec to a child process so the child and all of its
// descendants run confined by the kernel.
type Isolator interface {
	// Confine mutates cmd so the child runs confined. Either enforcement
	// attaches or an error is returned — never a partial sandbox.
	Confine(cmd *exec.Cmd, spec Spec) error
	// Available reports whether the primitives exist on this host, with
	// human-readable detail for phosphor_info and slog reporting.
	Available() (ok bool, detail string)
}

// NoSandbox is the zero isolator: enforcement is disabled.
type NoSandbox struct{}

func (NoSandbox) Confine(cmd *exec.Cmd, spec Spec) error { return nil }

func (NoSandbox) Available() (bool, string) { return false, "disabled" }
