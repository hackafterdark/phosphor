# Phosphor Core Philosophy

Phosphor is a terminal-based, hardened agentic runtime built in Go. It is a fork of Crush, architected for developers who demand full visibility, structural intelligence, and uncompromising security in their AI coding tools.

Phosphor is not a general-purpose chat interface. It is a coding agent that integrates deep structural intelligence (AST), filesystem sandboxing, and local observability directly into the agent's execution loop.

The Four Pillars of Phosphor

                    ┌─────────────────────────┐
                    │  PHOSPHOR CORE PILLARS  │
                    └────────────┬────────────┘
                                 │
           ┌────────────────┬────┴────────────────┬────────────────┐
           ▼                ▼                     ▼                ▼
    ┌──────────────┐ ┌──────────────┐      ┌──────────────┐ ┌──────────────┐
    │ Sovereignty  │ │  Security    │      │Observability │ │ Efficiency & │
    │ (Local-First)│ │ (Guardrails) │      │ (OTel/Logs)  │ │Effectiveness │
    └──────────────┘ └──────────────┘      └──────────────┘ └──────────────┘


## 1. Sovereignty

Phosphor respects your autonomy. You own your environment, your data, and your code.

**Local-First Runtime:** Optimized for local, open-weights models. Your code, prompts, and reasoning logs stay strictly on your machine.

**Zero Telemetry:** Entirely free of tracking and analytics.

**An Agentic Laboratory:** The Phosphor project values a lean, stable core over feature bloat. Phosphor is built to be a workbench; it is encouraged that you use it as a laboratory to experiment, learn, and prototype your own agentic ideas. Whether you want to borrow a component, test a new tool, or fork the runtime to build a highly specialized assistant, Phosphor provides the core, and you build the machine. The belief is that the most powerful agents are those tailored by the developer, and Phosphor is here to provide the solid foundation for you to make it your own.

## 2. Security (Hardened by Design)

Phosphor treats the workspace as a strict, high-integrity sandbox. Phosphor employs a Defense-in-Depth model:

**Workspace Confinement:** Filesystem tools use strict path resolution and validation (filepathext.IsInside) to enforce boundaries. Shell interpreters enforce CWD lockdowns to prevent directory traversal.

**Command Integrity:** A strict Slash Command registry partitions intent (System) from data (Chat). Unrecognized slash-prefixed inputs are rejected, preventing accidental command execution or prompt injection.

**Jail-Ready Architecture:** Phosphor is architected for containerized deployment, ensuring the agent operates within an ephemeral, restricted environment.

**Configuration-Driven Lockdown:** Security manifests (phosphor.json) allow for granular tool blacklisting, read-only modes, and strict network egress policies.

## 3. Observability

Autonomous agents cannot be black boxes. Phosphor provides a "flight recorder" for your agent's decision-making.

**OpenTelemetry (OTel):** Fully instrumented traces for agent turns, tool calls, and LLM reasoning.

**Local Auditing:** All telemetry is structured locally. You retain 100% control over your audit logs and debug traces.

**Structural Logging:** Deep internal logging captures the agent’s intent, providing a clear audit trail of every modification made to your workspace.

## 4. Efficiency & Effectiveness

Phosphor minimizes "LLM-work" through structural intelligence rather than brute-force token generation.

**AST-Aware Editing:** The edit and multiedit tools leverage Tree-sitter for real-time AST parsing. The agent validates syntax and checks for missing elements before writing to disk, ensuring the build is never broken by hallucinations.

**Structural Search:** Agents reason about the shape of your code (functions, structs, calls) rather than searching for raw text, leading to faster, more accurate context resolution.

**LSP Integration & Self-Healing:** Edits are validated against LSP diagnostics. If a change introduces an error, Phosphor captures the feedback and initiates a structured autonomous self-healing loop.