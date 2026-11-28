# Mediated `git` and `gh` Tools

Phosphor exposes two dedicated agent tools, `git` and `gh`, that perform
Git and GitHub operations **host-side with full credential access while
the model stays blind to secrets**. They exist so unattended (YOLO) and
headless sessions can do real Git/GitHub work without ever exposing the
host's GitHub OAuth token, SSH keys, or credential files to the sandbox or
to the model.

The `bash` tool's `sandbox="escalate"` path remains the fallback for
everything else, but it unsandboxes the whole command; the mediated tools
are bounded by construction and preferred wherever they cover the
operation.

## Enabling and disabling

Both tools are **enabled by default** whenever the workspace is trusted
for repo tooling; `gh` additionally requires the `gh` binary on `PATH`.
Their descriptions appear in the system prompt only while enabled. To
turn either one off, list it in `options.disabled_tools` in
`phosphor.json`:

```json
{
  "options": {
    "disabled_tools": ["git", "gh"]
  }
}
```

Disabling a tool removes it from the agent's tool list and its
description from the system prompt entirely; the model cannot invoke or
even reference it.

## Steering: unsupported vs. blocked

Because terminal-trained models have a reflexive pull toward 'bash', the
preference for the mediated tools is stated in three places: the system
prompt (conditionally, only while the tools are enabled), the 'bash'
tool description (which also drops its own raw 'git'/'gh' workflow
guidance when the mediated tools are present), and the tool errors
themselves:

- **Unsupported** operations (outside the allowlist, e.g. 'git clone',
  'gh api') return an error that explicitly steers to a *explained*
  escalated-'bash' request instead of a silent retry.
- **Policy-blocked** operations (secret gate, unpinned origin, path
  outside the workspace) return an error that explicitly forbids
  retrying through 'bash' or escalation — escalation would bypass the
  guardrail that just fired.

## Why mediated tools

- `~/.config/gh/hosts.yml` and `~/.ssh` hold live credentials with
  account-persistence-capable scopes. Sandboxed `gh`/`git push` cannot
  reach them, and unsandboxed execution exposes them.
- The mediated tools run host-side (like hooks) and mediate every
  invocation through strict validation, so arbitrary commands, remote
  URLs, and credential access are unreachable by construction.

## Repo pinning

- Both tools operate only on the repository detected in the current
  workspace, pinned to its `origin` remote.
- An existing `origin` remote is seeded at startup, so already-pinned
  repositories never see a redundant pin prompt.
- The pin is explicit for changes: the first use of a new or changed
  `origin` URL (e.g. after `git init`, or because `.git/config` is
  writable by the file tools) requires an approval prompt (even in YOLO
  mode), and any later change to `origin` re-triggers the pin approval.
- `gh` injects `--repo <owner/repo>` (derived from the pinned origin)
  into every call; `--repo`/`-R` cannot be supplied as arguments.
- Uninitialized or unpinned workspaces fail closed for all remote
  operations and all `gh` commands.
- Linked worktrees (`.git` pointer files, e.g. under orchestrators like
  Orca) are supported out of the box; `git worktree` management itself is
  unreachable, so an agent inside a worktree cannot touch sibling
  worktrees or the main checkout.

## Approval tiers

| Tier | Behavior | Examples |
|------|----------|----------|
| Read-only | No prompt | `git status`, `git log`, `gh pr view`, `gh issue view` |
| Local | Normal permission policy; auto-approved in YOLO | `git add`, `git commit`, `git branch`, `gh pr create`, `gh pr comment` |
| Gated | Explicit approval always (YOLO, allowlists, and hook pre-approvals cannot grant); denied in headless runs | `git push` to the default branch, pushes touching `.github/workflows/`, `git pull`, `git merge`, `git rebase`, `git reset --hard`, `git clean`, `gh pr checkout` |

## Push policy

The `tools.git.push_policy` option selects how `git push` is approved:

```json
{
  "tools": {
    "git": {
      "push_policy": "feature_branches"
    }
  }
}
```

- `"prompt"` (default): every push requires interactive approval and
  fails closed in headless/scheduled runs.
- `\"feature_branches\"`: YOLO and headless/scheduled runs may push any
  non-default branch without prompting, provided `origin` was pinned at
  startup. (`git fetch` and `gh pr create` remain local-tier operations
  that the normal permission policy handles.)

Pushes to the default branch, diffs touching `.github/workflows/`, force
pushes, and secret-gate hits stay gated or blocked regardless of the
policy — there is no policy that allows unattended default-branch
pushes. The setting is operator-floored: a workspace `phosphor.json`
can never widen `push_policy` past the value configured globally, only
tighten it.

Force pushes (`--force`, `-f`, `--force-with-lease`) are unreachable.
`git clone` is not supported; greenfield workspaces may `git init`
(workspace-root scoped) to unlock local operations.

## Secret hygiene

- **Commit gate:** the staged diff is scanned with gitleaks before
  `git commit`; a finding blocks the commit with a redacted error naming
  the rule and file.
- **Push gate:** outgoing commits (`git log -p` against the push base)
  are scanned before `git push`.
- **GitHub write gate:** `--title`/`--body` content of `gh pr create`,
  `gh pr comment`, and `gh issue comment` is scanned before execution.
- `gh` output (PR bodies, issue comments) is arbitrary user-authored
  text: it is redacted, defanged for control tokens, and wrapped as
  untrusted external content before reaching the model.

## OTel trace trailer

When tracing is actively configured and the trace is sampled, `git
commit` messages carry a `Phosphor-Trace: <trace-id>` trailer for
correlation with agent telemetry. No trailer is added when tracing is
disabled.

## Fine-grained PAT override

Host-side `gh` inherits `gh auth login` state, which often carries broad
scopes. For defense in depth, configure a repo-scoped fine-grained
personal access token instead:

```json
{
  "tools": {
    "gh": {
      "token_env": "PHOSPHOR_GH_TOKEN"
    }
  }
}
```

When `tools.gh.token_env` is set and the environment variable is
populated, its value is injected as `GH_TOKEN` into the host-side `gh`
subprocess and `GITHUB_TOKEN` is stripped from that subprocess, so the
override unambiguously selects the credential. The token value lives in
the environment, never in the config file, and is never logged or
returned to the model.

The bash tool's environment is allowlisted, so the token variable is
automatically invisible to sandboxed and escalated shell commands —
unless you deliberately list it in `tools.bash.allowed_env`, which you
should not do for a credential variable.

Recommended PAT permissions for the target repository: `contents: write`
and `pull_requests: write` — explicitly **no** `workflows` and **no**
admin permissions.

## Configuration

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `tools.gh.token_env` | string | `""` | Environment variable holding a repo-scoped fine-grained PAT injected as `GH_TOKEN` into host-side `gh` calls. |

Both tools are registered only for trusted workspaces (repo tooling
allowed); the `gh` tool additionally requires the `gh` CLI on `PATH`.
