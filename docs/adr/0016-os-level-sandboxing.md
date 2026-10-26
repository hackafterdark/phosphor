# 16. OS-Level Sandboxing as a Defense-in-Depth Layer

- **Status:** Accepted
- **Date:** 2026-10-26
- **Authors:** Phosphor Team
- **Superseded By:** —

## Context

Phosphor's bash tool has always guarded execution: the pathguard confines
file access to the workspace, command blocklists and AST checks reject
dangerous commands, and blocked operations are reported back to the agent.
These guards genuinely stop the common cases — an agent cannot casually read
or write outside the workspace today.

But the guards are *cooperative*: they parse the command before running it,
so they are only as strong as their parsing. A determined or prompt-injected
payload can hide its intent from string-level checks — for example,
`wsl sh -c '<payload>'` carries the payload inside a quoted argument the
argv guards never parse, and it then executes inside the WSL2 VM kernel,
outside the host's reach. String matching can also only reason about argv,
not about what a program actually does once running (a `curl` can be given
any destination; a Python script can open any socket).

A kernel-enforced boundary closes exactly this class of gap: the OS, not
Phosphor's parser, decides what the child process may touch.

## Decision

Add kernel-level sandboxing of bash-tool child processes as an
**additional, complementary layer** on top of the existing in-process
guards, selected by `tools.bash.sandbox_mode` (`off` / `workspace` /
`strict`), enforced with each platform's native primitive:

- **Linux**: Landlock (filesystem) + seccomp (syscall denylist)
- **macOS**: Seatbelt (`sandbox-exec`)
- **Windows**: restricted token + low integrity level + Job Object

The sandboxing code is **built directly into the Phosphor binary** — no
external helper binary, container runtime, or daemon. The binary re-execs
itself in a hidden bootstrap mode that attaches the kernel restrictions
before handing off to the real command.

Denials are surfaced to the agent and the user, and the agent may request
one unsandboxed retry (`sandbox="escalate"`) which **always requires an
explicit per-command user approval** — no allowlist, YOLO mode, or session
cache can grant it.

## Rationale / Why This Approach

- **The kernel boundary catches what string-level guards cannot.** The
  in-process guards stay as the first line (they give fast, clear feedback
  to the agent); the kernel layer makes any bypass of them non-effective
  rather than merely unlikely.
- **In-binary enforcement keeps Phosphor a single, dependency-free
  executable.** An external sandbox helper would be another binary on
  `PATH` that could be missing, stale, or tampered with; the Phosphor
  executable enforcing its own policy is the one component whose integrity
  the policy can rely on.
- **Native OS primitives over external sandboxes.** Landlock and restricted
  tokens are kernel features — nothing to install — and give the same
  guarantees on a fresh machine as on a prepared one. bubblewrap was
  rejected as an external runtime dependency; container-based isolation was
  rejected as changing the product's deployment model.
- **cBPF over libseccomp-golang on a preference basis.** Phosphor does use
  CGO (the vendored tree-sitter grammars require it), so cgo is available —
  but it complicates cross-compilation, and Go's standard library covers
  the seccomp syscall filter directly, so the pure-Go path was preferred
  when a good alternative existed.
- **The escalation flow is modeled on Codex CLI.** OpenAI's Codex CLI
  solves the same usability problem (legitimate commands occasionally need
  to run unsandboxed) with a per-command user prompt, and its behavior is
  well understood and accepted by users. We adopted the same pattern
  deliberately: deny first, surface the denial, and let the user decide
  per command.

## Consequences

**Positive**

- Prompt-injected or malicious repo content cannot use the bash tool to
  read outside the allowed filesystem set, write outside the workspace, or
  (with the network tier) egress except through the egress broker — even
  if it defeats every string-level guard.
- No operator setup: the capability works on stock OS installs, and where
  the platform cannot enforce it, Phosphor says so visibly instead of
  pretending.
- User-approved escalations keep legitimate workflows working while
  remaining explicit, logged, and observable.

**Negative / trade-offs**

- Kernel feature requirements: Landlock needs a recent Linux kernel; where
  unavailable, `workspace` mode degrades to the in-process guards with a
  visible warning, and `strict` mode refuses to run.
- Seatbelt profiles are tied to macOS internals and need re-validation on
  major macOS releases.
- Some host-adjacent binaries (`wsl`, `cmd`, `powershell`, inline
  `sh -c`) cannot be sandboxed on the host side and are gated by
  deny-by-default + escalation instead — intentional friction on those
  paths.
- Not every spawned process is confined yet (LSP servers, bundled rg/git
  helpers); this is tracked as a documented gap, consistent with the
  existing "What Is NOT Constrained" stance in the workspace-hardening
  docs.