# Security Hardening

This page is the single entry point for hardening Phosphor. It states the
project's security philosophy, shows how the individual controls fit
together, and gives a recommended baseline configuration.

## The Philosophy

Phosphor is built on four pillars — sovereignty, security as guardrails,
observability, and efficiency — and its security posture follows from them:

- **Sovereignty:** everything runs on your machine. No telemetry, no cloud
  round-trips for local work, and your code and logs stay local.
- **Hardened by default:** the safe behavior is the default behavior.
  Destructive and escape vectors are denied unless you explicitly opt in.
- **Defense in depth:** no single check is trusted to hold. String-level
  guards, tool-layer policy, and kernel enforcement all evaluate the same
  command independently.
- **Consent vs. enforcement:** permission prompts are consent checkpoints,
  not the security boundary. You can batch or delegate consent (session
  grants, allow-lists, YOLO mode); you cannot delegate containment. The
  kernel sandbox, workspace confinement, and escalation checks enforce
  regardless of any prompt-level grant. See
  [Permission Prompts](CONFIGURATION.md#permission-prompts).

## Where the Industry Stands

Terminal coding agents have converged on a similar shape: a chat loop,
shell/file tools, and a permission prompt in front of mutating actions.
The differences are in what happens *behind* the prompt:

| Common industry pattern | Typical behavior | Phosphor |
|---|---|---|
| Consent as the boundary | The prompt is the security control; once consent is given (or skipped), arbitrary commands run | Consent and enforcement are separate layers; enforcement never trusts consent |
| Shell isolation | Host shell invoked directly; agent sees the full environment | Environment allowlist, credential-bearing values scrubbed, interpreter inline-exec blocked |
| Filesystem bounds | Prompt-level warnings or none | Five tool-layer bounds layers plus optional kernel-enforced confinement (Landlock/Seatbelt/restricted token) |
| Escape hatches | Host shells, remote hosts, and nested interpreters reachable | Escape-hatch binaries and inline `sh -c` forms are gated while sandboxed; escalation is per-command and always prompts |
| Network egress | Fully open or fully blocked | Optional policy: host allow-lists, private-IP denial, proxy-aware |
| Secrets | None or CI-scanner bolt-ons | Layered redaction: file reads, tool output, JSON keys, learned secrets, and a last-resort wire mask |
| Auditability | Chat transcript | Local OpenTelemetry traces and structured logs of every tool call |

Phosphor's position is deliberate: **an agent's blast radius should be
bounded by the operating system, not by the agent's own judgment.**
Prompts can be phished, injected, or worn down by a tired user; kernel
confinement cannot.

## The Control Stack

From the outside in:

1. **Trust gate** — repo tooling (MCP servers, hooks, custom tasks) runs
   only after an explicit trust decision per workspace.
2. **Consent layer** — permission prompts, session grants,
   `permissions.allowed_tools`, `permissions.allow_commands`.
   See [Permission Prompts](CONFIGURATION.md#permission-prompts).
3. **Tool-layer guards** — workspace path confinement
   ([WORKSPACE_HARDENING.md](WORKSPACE_HARDENING.md)), environment
   allowlist and command deny list
   ([ENVIRONMENT_HARDENING.md](ENVIRONMENT_HARDENING.md)), network egress
   policy ([NETWORK_EGRESS_HARDENING.md](NETWORK_EGRESS_HARDENING.md)).
4. **Kernel sandbox** — `tools.bash.sandbox_mode: workspace|strict`
   confines bash children and stdio MCP servers with native OS primitives
   (Landlock, Seatbelt, and on Windows restricted token/AppContainer +
   Job Object). Denials surface to the agent; `sandbox="escalate"`
   retries always prompt, per command.
5. **Secret protection** — redaction before anything reaches the model or
   the wire ([SECRETS_PROTECTION.md](SECRETS_PROTECTION.md)).
6. **Observability** — local audit trail of every decision
   ([observability/OPEN_TELEMETRY.md](../observability/OPEN_TELEMETRY.md),
   [observability/LOGGING.md](../observability/LOGGING.md)).

## Recommended Baseline

For unattended or high-risk environments:

1. **Trust only what you audit.** Review MCP servers, hooks, and custom
   tasks in a workspace before granting it; the trust decision gates repo
   tooling and is persisted in `trusted_workspaces.json`.
2. **Constrain the shell and network:**

```json
{
  "$schema": "https://github.com/hackafterdark/phosphor/blob/main/schema.json",
  "tools": {
    "bash": {
      "sandbox_mode": "workspace",
      "sandbox_limits": { "max_procs": 256, "max_file_mb": 4096 },
      "network": { "enabled": true, "host_allowlist": ["proxy.internal"] }
    }
  },
  "permissions": {
    "allowed_tools": ["view", "grep", "glob", "ls"],
    "allow_commands": ["git", "go build", "go test", "go vet"]
  }
}
```

Notes:

- `sandbox_mode: workspace` degrades visibly (with a warning) when kernel
  enforcement is unavailable; `strict` refuses to exec instead.
- `allow_commands` is argv-verified and deny-by-default: compound
  commands, substitutions, and subshells still prompt.
- Session grants stay argument-agnostic by design; prefer explicit
  `allow_commands` entries in shared or CI environments.

## Verifying the Controls

The security controls are pinned by tests across the shell, sandbox,
permission, and secret-protection packages. Run them after any change to
the guard stack:

```bash
go test ./pkg/shell/ ./pkg/sandbox/ ./pkg/permission/ ./pkg/security/...
```

For the interactive validation walkthrough, see
[SECURITY_VALIDATION_TEST_SUITE.md](SECURITY_VALIDATION_TEST_SUITE.md)
(maintainer-facing).

## Further Reading

- [security/CONFIGURATION.md](CONFIGURATION.md) — every control and its config path.
- [ADR-0016](../adr/0016-os-level-sandboxing.md) — why kernel sandboxing, and why native primitives.
- [CORE_PHILOSOPHY.md](../CORE_PHILOSOPHY.md) — the project pillars.