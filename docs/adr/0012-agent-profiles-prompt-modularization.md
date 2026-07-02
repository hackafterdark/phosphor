# ADR: Agent Profiles & Prompt Modularization

- **Status:** Accepted
- **Date:** 2026-07-02
- **Authors:** Phosphor Team
- **Superseded By:** —

## Context

Originally, Phosphor inherited a monolithic prompt structure from Crush. The main agent prompt was called "coder", implying a narrow coding focus, and was stored as a single, large Go template. The sub-agent prompt ("task"), used for background context-gathering, was a much smaller, separate template. As Phosphor evolved, we introduced new governance layers to the main agent's prompt—such as explicit `interaction_gating` (not present in Crush) and grouped/enhanced coding protocols—to enforce safety and architectural boundaries. 

However, this monolithic and separate design had several limitations:
1. **Lack of Customization**: Users could not easily customize the main agent's behavior (e.g., creating a security-hardened agent or a non-coding assistant) without replacing the entire system prompt template.
2. **Sub-Agent Token Inefficiency**: In expanding the system's capabilities, some of these complex rules (like decision-making Heuristics, custom styling, or coding protocols) were kept in or added to the sub-agent prompt, despite the sub-agent having only read-only tools and executing programmatically.
3. **Rigid Architecture**: The "coder" and "task" designations locked the agent into specific roles rather than establishing a general-purpose system/sub-agent pattern.

## Decision

We resolved to rename the core agent components, introduce a modular profile system, and optimize the sub-agent prompt architecture.

1. **Rename to General-Purpose "System" Agent**: 
   * Renamed the core agent from `AgentCoder` to `AgentSystem` (and the master template from `coder.md.tpl` to `system.md.tpl`) to reflect a generic and flexible primary assistant.
2. **Introduce Cascading Profiles**:
   * Split the system prompt into five governance sections: `rules`, `style`, `workflow`, `interaction_gating`, and `decision_making`.
   * Added the concept of **Profiles** (e.g., `default`, `fiduciary`) where each section is a separate template partial that can be individually overridden or disabled using a magic word (`# DISABLE_SECTION`).
   * Configured a cascading resolution order: Workspace overrides (`.phosphor/profiles/`) -> Global overrides (`~/.phosphor/profiles/`) -> Embedded defaults (built into the binary).
3. **Streamline Sub-Agent ("Task") Prompt**:
   * Rewrote `task.md.tpl` to remove sections not applicable to read-only programmatic execution (such as `decision_making`, `coding_protocol`, `interaction_gating`, and custom styles).
   * Kept `<critical_rules>` (fiduciary rules) and project/user context files so the sub-agent remains safe and project-aware.
   * Defined a hardcoded, highly concise communication rule to prevent conversational filler in sub-agent responses.
4. **Implement a Tool Funnel Protocol for Sub-Agents**:
   * Added a strict instruction funnel in `task.md.tpl` instructing the sub-agent to prioritize `structural_search` (AST-based querying using tree-sitter) before falling back to unstructured keyword `grep` or standard file listings.
   * Registered `structural_search` as an allowed read-only tool for sub-agent sessions in `internal/config/config.go`.

## Rationale / Why This Approach

* **Generalization**: Renaming to "System" frees the agent from being categorized solely as a "coder", letting users build writing assistants, doc-searchers, or diagnostic agents.
* **Token Efficiency**: Stripping writing protocols and interactive gates from the sub-agent drastically reduces prompt tokens, resulting in faster and cheaper LLM requests for parallel background search operations.
* **Security & Defense-in-Depth**:
  * The sub-agent retains critical safety rules and path validation parameters.
  * Governance partials are rendered using least-privilege view structs, preventing sensitive config fields from leaking into custom templates.
  * The circuit-breaker pattern ensures that any template parsing or execution failures fail-fast and prevent agent startup, enforcing governance.

## Consequences

* Master templates are resolved and initialized as `system.md.tpl` (main agent) and `task.md.tpl` (sub-agent).
* Configuration models and TUI command-line parameters reflect the new `AgentSystem` names.
* Tests in `internal/config/load_test.go` and `internal/agent/prompt/` have been updated and verify that:
  * The sub-agent has correct read-only tools including `structural_search`.
  * The prompt builder executes properly under different profile configurations.
* Documentation in `docs/SYSTEM_PROMPT.md` and `docs/structural_search/README.md` is updated to detail these changes.
