## Roadmap: Phosphor Engine "Audit Registry" Implementation

### Phase 1: Define the Query Schema (`internal/agent/parser/templates.go`)

Before writing audit logic, standardize how queries are stored. You need a `QueryRegistry` struct that maps an `AuditType` to a `Template` (S-Expression).

- **Registry Structure:**

  Go
  ```
  type AuditRegistry struct {
      Queries map[string]AuditTemplate
  }
  type AuditTemplate struct {
      Name        string
      Language    string
      Pattern     string // S-expression
      Description string
      Severity    string // Critical, High, Medium, Info
  }
  
  ```
- **Goal:** Allow the agent to query the registry by `Severity` or `AuditType` (e.g., "Give me all Critical security queries for Go").

### Phase 2: Implement Audit Templates

Map your conceptual audit needs to Tree-sitter S-expressions.

- **Security Templates (**`AuditSecurity`**):**
  - **Insecure Crypto:** Match calls to outdated hash functions (e.g., MD5/SHA1).
  - **Insecure Command Execution:** Match execution functions (`exec.Command`, `os.Exec`) that take unsanitized input.
  - **Path Traversal:** Match file open/read calls using concatenated strings.
- **Complexity Templates (**`AuditComplexity`**):**
  - **"God Function" Detection:** Match `function_declaration` nodes and compute body length.
  - **Deep Nesting:** Match `if` or `for` blocks nested $&gt; 3$ levels deep.
- **Documentation Templates (**`AuditDocumentation`**):**
  - **Missing Docs:** Match `function_declaration` where the *preceding* sibling is NOT a `comment` or `doc_comment` node.

### Phase 3: Build the "Audit Runner"

Create an engine-side service that automates the execution of these templates across a codebase.

1. **Iterative Execution:** Create a `RunAudit(codebasePath, auditLevel)` function.
2. **Report Aggregation:** The engine should collect `Match` results from all enabled templates and output them as a structured JSON/Markdown report.
3. **Threshold Logic:** Implement logic to ignore "Info" level issues if a user only asks for "Critical" security findings.

### Phase 4: Integration with `phosphor` CLI

- **Command:** `phosphor audit --level=critical --target=./my-project`
- **Output:** A clean, actionable Markdown table showing:
  - `Issue` | `File` | `Line` | `Severity` | `Recommendation`

### Implementation Strategy (The "Architect" Workflow)

1. **Step A: Verification First.** Finish your manual verification of the remaining languages (`tree-sitter-html`, `tree-sitter-sql`, etc.).
2. **Step B: Registry Initialization.** Initialize the `QueryRegistry` map in `internal/agent/parser/templates.go`.
3. **Step C: Template "Hardening."** Start with one `AuditSecurity` query (e.g., "Insecure Hardcoded Secrets") and verify the `structural_search` tool returns the correct AST nodes for it.
4. **Step D: Automate.** Wire the Registry into the `phosphor` CLI so you can call `phosphor audit` without manually writing search queries.

**Developer Tip:** Since you are building this for *yourself*, focus on the **Severity** field first. By labeling queries as `Critical` vs `Info`, you create a "Signal-to-Noise" filter that makes the tool usable immediately rather than waiting for it to be "perfectly accurate" for every language.

---

Asking about making it configurable/extensible...but this is like ast-grep really. Here's what agent brainstorming had to say:

Yes, this is not just possible—it is the **standard architecture for production-grade static analysis tools** (like Semgrep, ESLint, or SonarQube).

By moving your queries into configuration files, you shift from "hardcoded logic" to a **"Rules Engine"** model. This allows you to add new security checks, complexity audits, or stylistic rules without ever recompiling your Go binary.

Here is how you design that extensibility into your existing architecture:

### 1. The Design: "Query-as-Data"

Instead of hardcoding S-expressions in `templates.go`, define a YAML or JSON schema that your engine loads at runtime.

**Proposed** `rules/security.yaml`**:**

YAML

```
rules:
  - id: insecure-crypto-md5
    language: go
    severity: critical
    pattern: |
      (call_expression
        function: (selector_expression
          operand: (identifier) @pkg
          field: (field_identifier) @method)
        arguments: (_))
    # Engine logic: match @pkg == "crypto/md5"
    message: "MD5 is cryptographically broken. Use SHA-256 or better."

```

### 2. Implementation Strategy

To make your engine "pull" from these files, you need to add a **Loader Layer**:

1. **Registry Initialization:** At startup, `phosphor` scans a `/rules` directory in the project root.
2. **Dynamic Parsing:** Your Go engine uses `gopkg.in/yaml.v3` to parse these files into your `AuditRegistry` struct.
3. **Template Injection:** The engine injects these dynamic patterns into the `sitter.Query` object at runtime.
  - *Pro-tip:* Since Tree-sitter queries are just strings, you can literally pass the YAML `pattern` field directly to `sitter.NewQuery(lang, pattern)`.

### 3. Benefits of this Approach

- **User-Defined Rules:** You can distribute your tool to other developers, and they can drop their own `.yaml` files into a `.phosphor/rules` folder to add custom project-specific security checks.
- **Hot-Reloading:** You could build a "watch" mode where your engine reloads the YAML files if they change, allowing for instant feedback while iterating on a rule.
- **Separation of Concerns:** Your Go code manages the *performance* of the AST traversal; the YAML files manage the *policy* of what constitutes a "vulnerability."

### Immediate Action Plan

Since you just finished stabilizing the core engine:

1. **Define your Schema:** Create a `struct` that maps exactly to your YAML/JSON rule format.
2. **Write the Loader:** Add a function to your engine that iterates `os.ReadDir("./rules")` and unmarshals them into your `AuditRegistry`.
3. **Refactor:** Once the loader is working, move one or two of your "hardcoded" templates into a YAML file and delete them from `templates.go`. If it still works, you’ve successfully decoupled your engine from its rules.

**This is the ultimate goal.** Once you do this, `Phosphor` stops being a hardcoded parser and becomes a **pluggable analysis platform**. Would you like me to help you draft the Go code for that YAML-to-Registry loader?