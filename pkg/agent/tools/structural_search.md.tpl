Search source code using tree-sitter AST queries. This is the "sniper rifle" for finding code by syntax structure — use it before grep for finding code patterns by their AST shape.

**TOOL FUNNEL PROTOCOL:**
1. **Try** `structural_search` **first** for finding syntax structures (functions, tables, classes, etc.).
2. **Use** `list_templates` **action** to discover what templates are available for the target language before searching.
3. **Fallback to** `grep` **if** the pattern is too complex or the file is too large to parse.
4. **LSP tools** (references, diagnostics) **only** for cross-file symbol resolution or type information.

**IMPORTANT:** Templates are **language-specific**. Not every language has the same templates. Always check available templates for your target language before searching.

**Available Languages and Templates:**
{{ range .LanguageTemplates }}- **{{ .Language }}** (`{{ range $i, $e := .Extensions }}{{ if $i }}, {{ end }}{{ $e }}{{ end }}`) — {{ range $i, $t := .Templates }}{{ if $i }}, {{ end }}{{ $t }}{{ end }}
{{ end }}

**Notes:**
- Templates are language-specific. An S-expression for Go won't work for Python.
- If `language` is not specified, it is auto-detected from the `include` pattern or file extensions.
- Python comments are `extra: true` nodes (like Go) — they appear in the AST when queried.
