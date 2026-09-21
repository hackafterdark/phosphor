Scan a file or directory inside the workspace for leaked credentials using the same gitleaks detector that guards writes and read-path redaction. It reports each finding with the secret masked (vendor prefix plus the final characters only) and the surrounding source line redacted, so the raw credential is never included in the tool output.

Set scan_history to true to audit git commit history (the diffs of all commits reachable from HEAD) instead of the working tree; if the path is not inside a git repository it falls back to scanning the working tree.

File discovery respects .gitignore and .phosphorignore, plus any .gitleaksignore file found in the scanned directory or the workspace root. Binary files are skipped.
