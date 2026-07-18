# Agent Tools Overview

Phosphor provides a comprehensive suite of agent tools organized by function. Each tool enables the AI agent to interact with the workspace, execute commands, and access external resources.

## File Operations

| Tool | Description |
|------|-------------|
| `view` | Read file content with line numbers, offset, and limit parameters. Supports image rendering and file summarization. |
| `edit` | Perform find-and-replace edits on a single file with exact string matching. |
| `write` | Create or overwrite a file with new content. Auto-creates parent directories. |
| `append` | Append content to the end of a file; creates the file if it doesn't exist. |
| `multiedit` | Apply multiple find-and-replace edits to a single file in one operation. |
| `ls` | List files and directories as a tree structure. |
| `glob` | Find files by name/pattern using glob syntax. |
| `grep` | Search file contents using regex or literal text patterns. |
| `view_node` | View the implementation of a specific function, struct, class, or interface by name using AST parsing. |

## Shell Execution

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands with background execution support, output truncation, and path validation. |

## Background Job Management

| Tool | Description |
|------|-------------|
| `job_output` | Retrieve stdout/stderr from a background shell by ID, with optional blocking wait. |
| `job_kill` | Terminate a background shell process by ID. |

## Web & Network

| Tool | Description |
|------|-------------|
| `fetch` | Fetch raw content from a URL as text, markdown, or HTML (max 100KB). |
| `download` | Download a URL directly to a local file with streaming and timeout support. |
| `agentic_fetch` | Fetch a URL or search the web using an AI sub-agent that can extract, summarize, and answer questions. |
| `web_fetch` | Fetch URL content for sub-agents (lightweight, no AI processing). |
| `web_search` | Search the web via DuckDuckGo Lite with randomized headers. |
| `sourcegraph` | Search code across public GitHub repositories via Sourcegraph API. |

## Code Intelligence (LSP)

| Tool | Description |
|------|-------------|
| `lsp_diagnostics` | Get LSP errors, warnings, and hints for a file or the whole project. |
| `lsp_references` | Find all references to a symbol by name using LSP. |
| `lsp_restart` | Restart one or all LSP clients by name. |

## Structural Search

| Tool | Description |
|------|-------------|
| `structural_search` | Search source code using tree-sitter AST queries. Supports multiple languages (Go, TypeScript, Python, Rust, etc.) with pre-built templates for finding functions, structs, variables, interfaces, and more. |

## MCP (Model Context Protocol)

| Tool | Description |
|------|-------------|
| `mcp_*` | Dynamically loaded tools from configured MCP servers. |
| `read_mcp_resource` | Read a specific resource from an MCP server by URI. |
| `list_mcp_resources` | List all resources available from an MCP server. |

## Phosphor System Tools

| Tool | Description |
|------|-------------|
| `phosphor_info` | Get Phosphor's current runtime state: active model, provider, LSP/MCP status, skills, hooks, permissions, and disabled tools. |
| `phosphor_logs` | Read Phosphor's internal application logs with configurable line count. |
| `reload_queries` | Reload custom query capabilities from the workspace `.phosphor/queries` directory. |

## Task Management

| Tool | Description |
|------|-------------|
| `update_goal` | Update the status of the current goal (supports "complete" status). |
| `todos` | Manage a structured task list with pending, in_progress, and completed states. |

## Quick Reference

```
Total built-in tools: 30

By category:
  File Operations:      9 tools (view, edit, write, append, multiedit, ls, glob, grep, view_node)
  Shell:                1 tool  (bash)
  Background Jobs:      2 tools (job_output, job_kill)
  Web & Network:        6 tools (fetch, download, agentic_fetch, web_fetch, web_search, sourcegraph)
  Code Intelligence:    3 tools (lsp_diagnostics, lsp_references, lsp_restart)
  Structural Search:    1 tool  (structural_search)
  MCP:                  3 tools (mcp_*, read_mcp_resource, list_mcp_resources)
  System:               3 tools (phosphor_info, phosphor_logs, reload_queries)
  Task Management:      2 tools (update_goal, todos)
```

## Deep Dives

- [Edit Tools](./edit_tools.md) — In-depth documentation for `edit`, `multiedit`, `write`, and `append`.
- [Structural Search](../structural_search/) — Templates, languages, and usage patterns for `structural_search`.
- [MCP Tools](../hooks/) — Information about MCP server integration and dynamically loaded tools.

## Design Principles

All tools share common patterns:

1. **Workspace containment** — All file paths are validated against the workspace root.
2. **Permission gating** — Tools that modify files or execute commands request user permission.
3. **Telemetry** — Every tool call is traced with OpenTelemetry spans.
4. **Response metadata** — Tools return structured metadata alongside text responses for downstream processing.
5. **Security** — Network tools include allow-lists, IP blocking, and secret redaction.