# 16. OS-Level Sandboxing Built Into the Binary

- **Status:** Accepted
- **Date:** 2026-10-26
- **Authors:** Phosphor Team
- **Superseded By:** —

## Context

Phosphor's bash tool historically guarded execution with *advisory, in-process*
checks: string blocklists, AST analysis, and argv confinement (`pathguard`).
These are cooperative — they stop accidents, not adversaries. A prompt-injected
agent or malicious repo content can drive the shell to read secrets
(`~/.ssh`, browser profiles, cloud credentials), exfiltrate over raw sockets
(the string blockers only see argv, not `curl`'s destination or a Python
socket), or damage the host (writes outside the workspace, cron/persistence,
fork bombs, disk fill). The honest failure statement was "we didn't see a
bypass"; the guarantee a kernel boundary provides is "a bypass cannot work",
regardless of how clever the injected command is.

The design space also had a deployment dimension: sandboxing could be delegated
to an external helper (bubblewrap/sandbox daemon), a container runtime
(Docker/Podman), or WSL2 on Windows. Each of those reintroduces a runtime
dependency the binary cannot assume — an external helper may be missing,
stale, or attacker-swappable on `PATH`; containers require a daemon and change
the user story from "terminal agent" to "container platform".

## Decision

Enforce kernel-level confinement of bash-tool child processes (and stdio MCP
servers) **directly inside the Phosphor binary**, on every platform, selected
by `tools.bash.sandbox_mode` (`off` / `workspace` / `strict`):

- **Linux**: Landlock (via `github.com/landlock-lsm/go-landlock`, pure Go,
  unprivileged, no user namespaces) + seccomp cBPF via `SysProcAttr`, with
  RLIMIT_NPROC/RLIMIT_FSIZE resource limits. Bootstrap is a **re-exec of
  Phosphor itself** in a hidden `phosphor sandbox-exec` mode that attaches
  Landlock/seccomp/rlimits before execing the real command; the policy is
  delivered over a **non-inheritable fd**, never argv or env.
- **macOS**: the Seatbelt profile compiled by Phosphor, applied through
  `/usr/bin/sandbox-exec` (the same primitive Claude Code uses), with
  resource limits riding a `--rlimit-exec` re-exec wrapper.
- **Windows**: restricted token + low integrity level + per-command Job
  Object (kill-on-close, active-process and per-process memory caps),
  hand-rolled on `golang.org/x/sys/windows`. This matches — and on older
  Windows exceeds — Codex CLI's unelevated posture; the admin-required
  elevated tier was deliberately skipped.

The sandbox is a **policy ceiling, never a permission gate**: the agent cannot
choose or relax its own policy (config does), approving a command never implies
de-escalation, and the only way out is the explicit per-command user grant
(`sandbox="escalate"` through the permission service's `BypassShortcuts`
path — YOLO mode, allowlists, hook pre-approvals, and session caches can
never grant it).

## Rationale / Why This Approach

- **The binary is the only trusted code in the picture.** Confinement depends
  on the enforcement mechanism being untouchable by the sandboxed child. A
  helper binary on `PATH` is attacker-influenceable; the Phosphor executable
  that re-execs itself in bootstrap mode is signed-by-install and versioned
  with the policy it enforces.
- **Zero runtime dependencies preserves the single-binary distribution
  model.** Landlock and restricted tokens are kernel features, not software
  installs; `sandbox-exec` ships with macOS. Nothing to `apt install`, no
  daemon to race, no container engine — the security posture is the same on a
  fresh machine as on a prepared one.
- **Rejected: bubblewrap.** External binary dependency, degraded behavior
  inside containers, and namespace semantics we only need for network
  enforcement on kernels <6.7 (parked, revivable).
- **Rejected: libseccomp-golang.** cgo dependency; Phosphor builds
  `CGO_ENABLED=0`, and raw cBPF via `SysProcAttr.SeccompFilter` covers the
  syscall denylist without it. Other evaluated helpers (`criyle/go-sandbox`,
  hollis-labs) were unmaintained or unavailable.
- **Re-exec self over ptrace/cgo bootstrap.** One uniform helper path for all
  three primitives, no ptrace race windows, and the policy fd is read before
  any user-influenced data is touched (Invariant 3).
- **Fail-loud posture (Invariant 1).** `strict` refuses to exec when
  enforcement cannot attach; `workspace` degrades to the in-process guards
  with a warning the model sees in the tool result. Silent weakening is the
  failure mode this whole feature exists to eliminate.
- **Escalation UX is Codex-proven and kept verbatim in spirit**: denials are
  marked `sandbox-denial:` in tool output, the agent may retry once with
  `sandbox="escalate"`, and every escalation is an explicitly approved,
  warn-logged, span-traced user decision.

## Implementation

| Piece | Home |
|---|---|
| Policy spec (`Spec`, `SpecForLimits`, `CredentialDenyPaths`) | `pkg/sandbox/spec.go` |
| Linux isolator (Landlock + seccomp bootstrap) | `pkg/sandbox/isolator_linux.go`, `pkg/sandbox/enforce_linux.go` |
| macOS Seatbelt profile + wrapper | `pkg/sandbox/isolator_darwin.go`, `pkg/sandbox/seatbelt_profile.go` |
| Windows restricted token + Job Object | `pkg/sandbox/isolator_windows.go`, `pkg/sandbox/contain_windows.go` |
| Resource limits | `pkg/sandbox/limits.go` |
| Shell integration (confine + degraded warning) | `pkg/shell/sandbox.go`, `pkg/shell/exec_unix.go`, `pkg/shell/exec_windows.go` |
| Escalation grant + denial guidance | `pkg/agent/tools/bash.go` |
| Escape-hatch gating (wsl/cmd/powershell/inline `sh -c`) | `pkg/sandbox/escape.go`, `pkg/shell/sandbox.go` |

The six security invariants (fail loud; no new secrets on disk; non-inheritable
policy fd; kernel boundary is the authority, pathguard pre-checks are UX;
hooks stay unsandboxed; egress fails closed) are pinned by vector tests in
`pkg/shell/sandbox_vector_test.go` and `pkg/shell/sandbox_vector_windows_test.go`.

## Consequences

**Positive**

- Prompt-injected shell commands cannot read outside the allowed filesystem
  set, cannot write outside the workspace (plus opted-in toolchain caches),
  and — in `ProxyOnly` network tier — cannot egress except through the
  egress broker.
- The security guarantee holds on stock OS installs across Linux, macOS, and
  Windows with no operator setup; capability detection degrades visibly,
  never silently.
- Denials, escalations, and the active mode are observable
  (`phosphor.security.sandbox_mode`, `sandbox_escalated`,
  `sandbox_escalation_denied`, warn-level escalation logs, `[sandbox]`
  report in `phosphor_info`).

**Negative / trade-offs**

- Per-platform kernel surface: Landlock needs kernel ≥5.13 (ABI-gated
  features), Seatbelt profiles need re-validation each macOS major release
  (quarterly regression budget), and Windows POSIX rlimits cannot be
  enforced — specs requesting them are refused rather than silently
  promised.
- Legitimate host-adjacent workflows (wsl/cmd/powershell/inline shells) now
  require an explicit user escalation; this is intentional friction.
- v1 leaves documented gaps: LSP servers, bundled rg/git helper spawns, and
  TUI shell commands are not yet confined; DNS-tunnel exfil closes with the
  v2 broker shim. These mirror the `WORKSPACE_HARDENING.md` "What Is NOT
  Constrained" stance and are tracked in the sandboxing plan.