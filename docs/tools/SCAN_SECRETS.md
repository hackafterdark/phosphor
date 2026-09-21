# Scan Secrets

## Description

`scan_secrets` audits a file or directory in the workspace for leaked credentials. It reuses the same gitleaks detector that guards every write and redacts tool output, so it reports findings with the same rules the write gate uses.

The tool is built around one hard invariant: **the raw secret is never included in the output.** Every finding is reported with the credential masked and the surrounding source line redacted, so asking the agent to run a security audit cannot itself echo a credential back into the conversation.

## Usage

### Parameters

| Parameter | Type | Required | Description |
|---|---|---|---|
| `path` | string | No | Relative path to a directory or a specific file to scan. Defaults to the current working directory. |
| `scan_history` | bool | No | When `true` and the path is inside a git repository, scan git commit history (the diffs of all commits reachable from `HEAD`) instead of the working tree. Falls back to the working tree when the path is not in a repository. Defaults to `false`. |

### Example

Scan a directory in the working tree:

```
Tool: scan_secrets
Parameters:
  path: "config"
```

Audit git commit history for the whole workspace:

```
Tool: scan_secrets
Parameters:
  path: "."
  scan_history: true
```

### Output

When secrets are found, the report lists each one with a masked value and a redacted line preview:

```
Found 2 potential secret(s):
1. [github-pat] GitHub Personal Access Token
   File: config/keys.go:7
   Masked: ghp_••••••••SG
   Line: const Token = "<redacted:gitleaks:github-pat>"
2. [aws-access-token] AWS Access Key ID
   File: .env:3
   Masked: AKIA••••••••GP
   Line: AWS_ACCESS_KEY_ID="<redacted:gitleaks:aws-access-token>"
```

When nothing is found, the tool returns the canonical zero-finding sentence naming the scanned path:

```
No unignored secrets detected in config.
```

For a history scan with no findings, the message is qualified with `git history of`:

```
No unignored secrets detected in git history of .
```

The structured response also carries metadata: `number_of_findings`, `files_scanned`, and `scan_history`.

## How It Works

1. The target path is resolved relative to the workspace root and rejected if it falls outside the workspace.
2. The process-wide gitleaks detector is reused (it is built once and shared with the write gate and read-path redaction), so the same ~700 rules plus the Aho-Corasick keyword prefilter apply.
3. For a working-tree scan, the target is walked gitignore-aware: files matched by `.gitignore`, `.phosphorignore`, or a `.gitleaksignore` file are skipped, as are binary files. Each remaining file is fed to the detector.
4. For a history scan, `git log --patch` is run (bounded to the most recent commits) and the detector is run over the unified diff. The file and line attribution for each finding are reconstructed from the nearest preceding `+++ b/<path>` diff header.
5. Every finding is masked and redacted before it is rendered, and a final pass scrubs the raw match bytes from the whole report as belt-and-suspenders protection.

## Masking and Redaction

- **Masked value:** the leading vendor prefix (e.g. `ghp_`, `sk_live_`, `AKIA`) and the final two characters are kept; everything between them is replaced with `••••••••`. The value can be recognised but never reconstructed.
- **Line preview:** the matched secret bytes are replaced in place with the read-path redaction sentinel `<redacted:gitleaks:<rule-id>>`, the line is passed through `DefangSpecialTokens` to neutralise inference control tokens, and long lines are truncated.
- **Residual scrub:** before returning, any remaining occurrence of a reported raw match or captured secret is replaced with its sentinel, so no path (including a rule description) can leak the credential.

## Ignore Files

Discovery respects, in addition to the built-in ignore set:

- `.gitignore` and `.phosphorignore`, honored hierarchically the same way the `grep`/`ls` tools honor them.
- `.gitleaksignore`, read from both the scanned directory and the workspace root. It uses gitignore pattern syntax (one pattern per line, `#` for comments) for suppressing known-true-positive fixtures or generated files that should not be reported.

## Requirements

- None for a working-tree scan — the detector ships embedded in the binary.
- A git repository on PATH for `scan_history` (the tool falls back to a working-tree scan when the path is not inside a repository).

## Tips

- Point it at the sensitive areas first (`.env` files, config directories, secret stores) before scanning the whole tree.
- Use `scan_history: true` when a credential may have been committed and later removed — the working tree will look clean while history still carries it.
- Add committed test fixtures to `.gitleaksignore` so the audit reports only genuine exposures.
- High-entropy custom keys in source may be reported by the generic detector family; when one is a known placeholder, ignore the file rather than trusting the report blindly.
