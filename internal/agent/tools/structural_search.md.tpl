Search source code using tree-sitter AST queries. This is the "sniper rifle" for finding code by syntax structure — use it before grep for finding code patterns by their AST shape.

**TOOL FUNNEL PROTOCOL:**
1. **Try** `structural_search` **first** for finding syntax structures (functions, tables, classes, etc.).
2. **Use** `list_templates` **action** to discover what templates are available for the target language before searching.
3. **Fallback to** `grep` **if** the pattern is too complex or the file is too large to parse.
4. **LSP tools** (references, diagnostics) **only** for cross-file symbol resolution or type information.

**IMPORTANT:** Templates are **language-specific**. Not every language has the same templates. Always check available templates for your target language before searching.

**Supported Languages:**
- **Go** (`*.go`) — `parameter_list`, `block`, `field_identifier`
- **C** (`*.c`, `*.h`) — `function_declarator`, `compound_statement`, `struct_specifier`, `call_expression`
- **Bash** (`*.sh`) — `function_definition`, `variable_assignment`, `command`, `comment`
- **C++** (`*.cpp`, `*.cc`, `*.cxx`, `*.hpp`, `*.hxx`) — `function_declarator`, `compound_statement`, `class_specifier`, `field_expression`
- **C#** (`*.cs`) — `method_declaration`, `class_declaration`, `invocation_expression`, `using_directive`
- **HCL** (`*.hcl`) — `block`, `attribute`, `identifier`, `body`
- **Ruby** (`*.rb`) — `method`, `class`, `module`, `call`, `assignment`, `comment`
- **TypeScript** (`*.ts`, `*.tsx`) — `formal_parameters`, `statement_block`, `property_identifier`
- **JavaScript** (`*.js`, `*.jsx`) — `formal_parameters`, `statement_block`, `property_identifier`
- **Python** (`*.py`) — `parameters`, `block`, `call`
- **SQL** (`*.sql`) — `create_function`, `create_table`, `select`, `insert`, `update`, `delete`
- **Rust** (`*.rs`) — `function_item`, `struct_item`, `enum_item`, `let_declaration`, `use_declaration`
- **Java** (`*.java`) — `method_declaration`, `class_declaration`, `interface_declaration`, `variable_declaration`, `method_invocation`
- **PHP** (`*.php`) — `function_definition`, `class_declaration`, `interface_declaration`, `variable_name`, `function_call_expression`, `namespace_use_declaration`
- **JSON** (`*.json`) — `object`, `pair`, `string`, `comment`
- **HTML** (`*.html`, `*.htm`) — `element`, `tag_name`, `attribute`, `comment`
- **CSS** (`*.css`) — `rule_set`, `selector_list`, `block`, `custom_property`, `import_statement`
- **TOML** (`*.toml`) — `table`, `pair`, `key`, `comment`
- **Scala** (`*.scala`, `*.sbt`) — `class_definition`, `object_definition`, `function_definition`, `import_declaration`

**Notes:**
- Templates are language-specific. An S-expression for Go won't work for Python.
- If `language` is not specified, it is auto-detected from the `include` pattern or file extensions.
- Python comments are `extra: true` nodes (like Go) — they appear in the AST when queried.

**Available templates by language:**
{{ range .LanguageTemplates }}- **{{ .Language }}**: {{ range $i, $t := .Templates }}{{ if $i }}, {{ end }}{{ $t }}{{ end }}
{{ end }}
