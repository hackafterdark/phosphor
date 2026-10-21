# CLI Reference

This page describes the `phosphor` command-line interface. For command-specific
details, see the linked docs or run `phosphor <command> --help`.

## Global flags

These flags apply to the root command and are inherited by subcommands.

| Flag | Short | Description |
| --- | --- | --- |
| `--cwd <dir>` | `-c` | Set the current working directory. |
| `--data-dir <dir>` | `-D` | Use a custom Phosphor data directory. |
| `--debug` | `-d` | Enable debug logging. |
| `--host <host>` | `-H` | Connect to a specific Phosphor server host. |
| `--profile <name>` | | Select an active prompt/governance profile. |
| `--trust` | `-T` | Trust the workspace and load repo-local tools/MCP configuration. |
| `--yolo` | `-y` | Automatically accept all permissions. |
| `--session <id>` | `-s` | Continue a previous session by ID. |
| `--continue` | `-C` | Continue the most recent session. |
| `--help` | `-h` | Show help. |

`--session` and `--continue` are mutually exclusive.

## Commands

| Command | Description |
| --- | --- |
| `phosphor` | Start the TUI and HTTP API (default). |
| `phosphor run [prompt...]` | Run a single non-interactive prompt. |
| `phosphor server` | Start the HTTP API server. |
| `phosphor acp` | Start the Agent Client Protocol service. |
| `phosphor login [platform]` | Authenticate with a provider. |
| `phosphor logout [platform]` | Log out from a provider. |
| `phosphor update-providers [path-or-url]` | Update provider metadata. |
| `phosphor index [directory]` | Build the workspace FTS5 index. |
| `phosphor session` | List and manage sessions. |
| `phosphor projects` | List known project directories. |
| `phosphor logs` | View Phosphor logs. |
| `phosphor stats` | Show token and cost usage statistics. |
| `phosphor memorygraph` | Run the memory graph viewer. |
| `phosphor cron` | Run the cron service for scheduled jobs. |
| `phosphor job` | Manage scheduled jobs. |
| `phosphor bindings` | List all keybindings. |
| `phosphor dirs` | Show config and data directories. |
| `phosphor schema` | Generate JSON schema for configuration. |

## `phosphor run`

`phosphor run` sends a single prompt to the agent and exits. It supports
piping input from stdin.

| Flag | Short | Description |
| --- | --- | --- |
| `--quiet` | `-q` | Hide the spinner. |
| `--verbose` | `-v` | Show logs. |
| `--model <model>` | `-m` | Model to use (`model` or `provider/model`). |
| `--small-model <model>` | | Small model to use. |
| `--session <id>` | `-s` | Continue a previous session by ID. |
| `--continue` | `-C` | Continue the most recent session. |

## `phosphor server`

| Flag | Short | Description |
| --- | --- | --- |
| `--host <host>` | `-H` | Server host (TCP or Unix socket). |
| `--openai-host <host>` | | OpenAI-compatible API host. |
| `--openai-port <port>` | | OpenAI-compatible API port. |
| `--openai-enabled <bool>` | | Enable the OpenAI API service (default: true). |
| `--workspace <dir>` | | Workspace directory path. |

## `phosphor login`

| Flag | Short | Description |
| --- | --- | --- |
| `--force` | `-f` | Force re-authentication even if already logged in. |

## `phosphor logout`

| Flag | Short | Description |
| --- | --- | --- |
| `--force` | `-f` | Skip the logout confirmation prompt. |

## `phosphor update-providers`

| Flag | Description |
| --- | --- |
| `--source <source>` | Provider source to update (`catwalk` or `hyper`, default: `catwalk`). |

## `phosphor index`

No subcommand-specific flags. Builds the workspace FTS5 index for the given
directory (defaults to the current directory).

## `phosphor session`

| Subcommand | Description |
| --- | --- |
| `phosphor session list` | List all sessions. |
| `phosphor session show <id>` | Show session details. |
| `phosphor session last` | Show the most recent session. |
| `phosphor session delete <id>` | Delete a session. |
| `phosphor session rename <id> <title>` | Rename a session. |
| `phosphor session pin <id>` | Pin a session against bulk deletion. |
| `phosphor session unpin <id>` | Unpin a session. |
| `phosphor session prune-sessions` | Bulk delete old sessions. |
| `phosphor session prune <session-id>` | Prune old messages from a stateless session. |
| `phosphor session list-stateless` | List stateless sessions. |

Common output flags:

| Flag | Description |
| --- | --- |
| `--json` | Output in JSON format. |
| `--before <duration>` | Only affect sessions older than the duration. |
| `--dry-run` | Show what would be deleted without deleting. |

## `phosphor projects`

| Flag | Description |
| --- | --- |
| `--json` | Output as JSON. |

## `phosphor logs`

| Flag | Short | Description |
| --- | --- | --- |
| `--follow` | `-f` | Follow log output. |
| `--tail <n>` | `-t` | Show only the last N lines (default: 1000). |

## `phosphor memorygraph`

| Flag | Description |
| --- | --- |
| `--report <file>` | Write a self-contained snapshot report HTML file and exit. |

See [Memory Graph Viewer](memory/MEMORYGRAPH.md) for details.

## `phosphor cron`

Runs the cron service for scheduled jobs. See
[Scheduled Jobs](platform/SCHEDULED_JOBS.md) for job file format and management.

## `phosphor job`

| Subcommand | Description |
| --- | --- |
| `phosphor job add <name>` | Create a new job. |
| `phosphor job list` | List all jobs. |
| `phosphor job remove <name>` | Remove a job. |
| `phosphor job edit <name>` | Edit a job. |

## `phosphor acp`

| Flag | Description |
| --- | --- |
| `--workspace <dir>` | Workspace directory (defaults to current directory). |

## `phosphor bindings`

Lists all available keybindings.

## `phosphor dirs`

Shows the config and data directories used by Phosphor.

## `phosphor schema`

Generates the JSON schema for `phosphor.json`.