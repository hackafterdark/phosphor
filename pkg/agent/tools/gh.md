# GitHub operations on the repository pinned to the current workspace.

The `gh` tool is host-mediated: `gh` runs outside the sandbox with the
host's GitHub credential, but the model never sees the credential and can
only address the repository pinned to the workspace's `origin` remote. The
tool injects `--repo <owner/repo>` (derived from the approved origin pin)
into every call; `--repo`/`-R` cannot be supplied as arguments.

Allowed subcommands:

- `pr view` (read-only; `--comments`, `--json`)
- `pr create` (`--title`, `--body`, `--base`, `--label`, `--draft`, `--fill`)
- `pr comment` (`--body`, optional PR number)
- `pr checkout` (no flags; always requires explicit approval)
- `issue view` (read-only; `--comments`, `--json`)
- `issue comment` (`--body`, optional issue number)
- `release view` (read-only; `--latest`, `--json`)

Everything else — `gh api`, `gh auth`, `gh config`, `gh alias`,
`gh ssh-key`, `gh gpg-key`, `gh extension`, `gh codespace`, `gh repo`,
`gh secret`, `gh run`, and `gh workflow` — is unreachable.

Guarantees:

- Fail closed: the workspace must be an initialized git repository with an
  approved `origin` pin; uninitialized or unpinned workspaces cannot use
  this tool.
- `--body-file` and `--template` are rejected (host-file exfiltration);
  content must be inline via `--title`/`--body`.
- `--recurse-submodules` and `--force` are rejected on `pr checkout`.
- `gh` runs with `GH_PAGER=cat`, `GH_PROMPT_DISABLED=1`, `GH_EDITOR=false`,
  `GH_BROWSER=false`, `NO_COLOR=1`, `GH_SPINNER_DISABLED=1`, and the same
  git config neutralization the git tool applies (gh invokes git
  internally).
- All output is treated as untrusted external content: it is wrapped in a
  security boundary, defanged for control tokens, and passed through secret
  redaction before reaching the model.
