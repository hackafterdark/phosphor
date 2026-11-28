Run git commands on the repository in the current workspace.

The `git` tool is host-mediated: arguments are validated against a strict
subcommand and flag allowlist, hostile repo-local git config is neutralized
on every invocation, and operations are pinned to the repository's `origin`
remote. The agent can never target another repository, force-push, or read
credentials directly.

Allowed subcommands: status, log, diff, show, branch, remote, tag, rev-parse,
add, commit, checkout, stash, fetch, pull, push, merge, rebase, reset, clean,
and init.

Greenfield workspaces: if the workspace is not yet a git repository, only
`git init` is allowed (scoped strictly to the workspace root; `--bare`,
`--separate-git-dir`, `--template`, and path arguments are rejected).
Initializing unlocks local operations; `push`, `pull`, and `fetch` stay
disabled until the user configures an `origin` remote, and the first use of
a new or changed `origin` URL always requires explicit user approval (the
repository pin). `git clone` is not supported.

Argument contract:
- `subcommand` is the git subcommand to run; `args` carries its flags and
  positional arguments as a JSON array (no shell syntax — no chaining,
  substitution, or redirection is possible). `args` must contain only
  flags/arguments and must never repeat the subcommand: run status with
  short output as `{`subcommand`: `status`, `args`: [`--short`]}`, never
  `{`subcommand`: `status`, `args`: [`status`]}`.
- For `diff` and `log`, positional arguments are revisions; to restrict the
  command to file paths, pass `--` and then the paths.
- `add` takes file paths only; `checkout` without `-b` takes file paths only
  after `--`. `checkout -b <branch>` creates and switches to a new branch.
- `push`, `pull`, and `fetch` accept at most the remote name `origin` plus
  refspecs; remote URLs and arbitrary remote names are rejected.

Approval tiers:
- Read-only commands (status, log, diff, show, branch listing, remote, tag
  listing, rev-parse, fetch dry-run) run without a prompt.
- Local commands (add, commit, branch creation, checkout -b, stash, tag,
  fetch, init) follow the normal permission policy and auto-approve in yolo
  mode.
- High-impact commands (push to the default branch, pushes touching
  .github/workflows/, merge, rebase, pull, reset, clean, destructive
  checkout) always require explicit approval, even in yolo mode, and are
  denied in headless runs.

Push approval policy (`tools.git.push_policy`, default `prompt`):
- `prompt`: every `push` requires interactive approval; headless runs
  are denied.
- `feature_branches`: pushes to non-default branches are approved
  automatically (YOLO and headless/scheduled runs included), provided
  `origin` was pinned at startup. Pushes to the default branch and
  pushes touching `.github/workflows/` stay gated in both modes, and
  force pushes and secret-gate hits stay blocked.

Force pushes (`--force`, `-f`, `--force-with-lease`) are not supported.
