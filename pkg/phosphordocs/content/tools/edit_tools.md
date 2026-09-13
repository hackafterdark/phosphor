# Edit and Multi-Edit Tools

Phosphor provides two powerful, resilient, and secure tools for modifying files in the workspace: **Edit** (`edit`) and **Multi-Edit** (`multiedit`). These tools go beyond simple text replacement by combining fuzzy matching, concurrent write safety, AST-aware syntax validation, LSP-driven diagnostic feedback, and security guardrails.

---

## 1. Core Architecture

Both tools operate on an **Atomic Read-Modify-Write (RMW)** model to prevent overwriting concurrent external modifications and to guarantee the assistant always operates on the latest file state.

```mermaid
graph TD
    A[Start Edit / Multi-Edit] --> B[Read Current File Content]
    B --> C[Compute Initial State Hash]
    C --> D[Apply Edit Operations via Fuzzy Matching]
    D --> E[Validate Syntax & Scan for Secrets]
    E -- Validation Fails --> F[Reject Edit & Return Error]
    E -- Validation Passes --> G[Perform Atomic Lock & Compare Disk State]
    G -- Disk State Matches Initial --> H[Write New Content to Disk]
    G -- Disk State Differs --> I[Silent Refresh: Re-read, Re-match, Retry]
    I --> D
    H --> J[Query LSP Diagnostics]
    J --> K[Format TUI Warning & Return Result]
```

---

## 2. Key Features

### 2.1. Atomic Read-Modify-Write (RMW) with Silent Refresh

To handle cases where a file is modified externally or by another process during an edit cycle, the tools perform the following:
1. **Initial Hash Check:** Stash the initial content string (`initialContent`) of the file when the tool is first called.
2. **Post-Application Check:** Immediately before writing back to disk, the tool re-reads the file from disk.
3. **Silent Refresh:** If the disk content differs from `initialContent`, the tool:
   - Invalidates the fuzzy match cache for the file.
   - Updates `initialContent` to the latest disk content.
   - Re-calculates the target edit locations against the fresh content.
   - Retries the operation (up to 5 attempts) before failing.

### 2.2. Fuzzy Matching & Cache Optimization

When a strict substring match (`strings.Index`) fails, the tool falls back to a **Fuzzy Line Correction** algorithm:
- It searches for the closest line matches using Levenshtein distance calculations.
- **Quote Normalization:** Normalizes single/double/backtick quotes during comparisons to prevent matching failures due to minor style differences.
- **Thread-Safe Cache:** To maintain high performance, fuzzy search results are cached in a thread-safe `map` (using `sync.RWMutex`) keyed by the file path and `oldString`. The cache is automatically invalidated when the file is modified or written to.

### 2.3. Semantic Safety (AST-Aware Syntax Verification)

To prevent the assistant from committing broken code, a pre-write syntax validation step is executed:
- **Tree-sitter Integration:** Parses the proposed file content into an Abstract Syntax Tree (AST).
- **AST Error Detection:** Traverses the AST looking for:
  - Explicit `ERROR` nodes.
  - Nodes where `IsMissing() == true` (e.g., unmatched parentheses, unclosed braces, or missing semicolons).
- **Graceful Degradation:** This feature is compiled conditionally using Go build tags (`sitter_cgo.go` and `sitter_nocgo.go`). If CGO is disabled or the file language is unsupported, the syntax check is gracefully skipped.

### 2.4. Security Guarding (Secret Detection)

Before writing any content to disk, the tools scan the new content using regular expressions to prevent accidental credential leakage:
- **AWS Keys:** Identifies AWS Access Key IDs and Secret Access Keys.
- **Private Keys:** Detects PEM-encoded private keys (e.g., RSA, EC, SSH).
- **Generic API Keys:** Catches high-entropy API keys and bearer tokens.
If a secret is detected, the edit is immediately rejected with a security warning.

### 2.5. Hashline-Aware Precision Verification & Security

To eliminate file state drift and prevent edit errors in large files, Phosphor implements an opt-in **Hashline Verification** protocol:
1. **Metadata Annotation (via `view`):** When calling the `view` tool with `use_hashline: true`, Phosphor prefixes every line of the output with its 1-indexed line number, a CRC32 checksum of the line (with normalized line endings), and a pipe separator:
   ```text
      102:a1b2c3d4|func someFunction() {
   ```
2. **Automatic Detection:** The edit tools (`edit` and `multiedit`) automatically detect the presence of hashline prefixes in the `old_string` parameter.
3. **Verification and Decoupling:**
   - **Drift Checking:** The tool parses the checksums and line numbers from the hashline tags, then reads the current disk file to verify that the lines targeted by the edit still match the expected CRC32 checksums exactly. If a mismatch is detected, the edit is aborted immediately.
   - **Tag Stripping:** Once verified, the hashline metadata is stripped from `old_string` before fuzzy matching is applied, ensuring that only clean code is used for the replacement logic.
4. **RMW Loop Re-Verification:** If a concurrent file modification is detected during the RMW silent refresh, the retry loop re-reads the updated file from disk and re-validates the operation's hashline tags against the updated content before attempting to apply the patch.
5. **Anchor Poisoning Guardrail:** To prevent the assistant from accidentally writing raw hashline prefixes back into the source code, Phosphor inspects the replacement `new_string` content. Any attempt to write raw hashline tags triggers a strict **Security Violation** error, blocking the change.
6. **Verified Snippet Generation:** On successful application of a hashline-aware edit, the tool computes the exact line numbers and new CRC32 checksums of the modified region, returning an enriched hashline-tagged `Snippet` in the response metadata. This allows the assistant to update its context with the new hashes without needing a full-file read.

---

## 3. Synergy of Edit Tool Strategies

Phosphor's edit tools are built on a multi-tiered safety model. By combining all strategies, they achieve a high degree of correctness and efficiency:
* **Fuzzy Matching** allows the assistant to successfully apply edits even if simple whitespace or quote styles differ.
* **Hashline Verification** anchors the fuzzy matcher to the exact lines the assistant intends to modify, acting as a lock to prevent matching the wrong instance in highly repetitive files.
* **Atomic RMW** ensures that concurrent changes made by the developer or another process are never blindly overwritten.
* **AST & Secret Validation** prevents syntactically broken code or sensitive credentials from ever touching the disk.

---

## 4. Developer Feedback Loop

### 4.1. LSP Diagnostic-Driven Feedback

The edit tools are tightly integrated with the workspace's Language Server Protocol (LSP) manager:
1. **Before Edit:** Retrieve the list of diagnostics and count the number of error-level diagnostics.
2. **After Edit:** Notify the LSPs of the change, wait for the compilation diagnostics to update, and recount the errors.
3. **Diagnostic Diffing:** Compare the pre- and post-edit diagnostics. Any newly introduced diagnostics are stored in the response metadata (`new_diagnostics`).
4. **Self-Healing Prompt Header:** If the error count increases, a warning header is prepended to the tool output returned to the assistant:
   ```text
   WARNING: ACTION REQUIRED: Your last edit introduced N new error(s). Please prioritize fixing these newly introduced diagnostics.
   ```
   This forces the assistant to immediately focus on fixing its own mistakes before proceeding with other tasks.

### 4.2. TUI Warning Banners

The Terminal User Interface (TUI) parses the `new_diagnostics` metadata field and renders a prominent warning banner directly above the diff view:

```text
 ⚠️  ACTION REQUIRED  This edit introduced 1 new diagnostic error(s):
  • main.go:12:3: undefined: foo
```

This ensures that human operators are immediately aware of any compilation or syntax issues introduced by the assistant.

---

## 5. Configuration and Parameters

### `view` Parameters (Hashline Generation)
| Parameter | Type | Description |
| :--- | :--- | :--- |
| `file_path` | `string` | The absolute path to the file to view. |
| `start_line` | `integer` | (Optional) The starting line number to view. |
| `end_line` | `integer` | (Optional) The ending line number to view. |
| `use_hashline` | `boolean` | (Optional) If `true`, returns lines prefixed with line numbers and CRC32 checksums. |

### `edit` Parameters
| Parameter | Type | Description |
| :--- | :--- | :--- |
| `file_path` | `string` | The absolute path to the file to modify. |
| `old_string` | `string` | The exact block of text to replace (optionally hashline-tagged). |
| `new_string` | `string` | The replacement text (must not contain raw hashline tags). |
| `replace_all` | `boolean` | (Optional) If `true`, replaces all occurrences of `old_string`. |

### `multiedit` Parameters
| Parameter | Type | Description |
| :--- | :--- | :--- |
| `file_path` | `string` | The absolute path to the file to modify. |
| `edits` | `array` | An array of sequential edit operations, each containing `old_string` (optionally hashline-tagged), `new_string` (must not contain raw hashline tags), and `replace_all`. |
