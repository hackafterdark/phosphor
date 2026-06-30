# ADR-001: Tree-Sitter Multi-Language Structural Search

- **Status:** Accepted
- **Date:** 2026-06-14
- **Authors:** Phosphor Team
- **Superseded By:** —

## Context

Phosphor's AI agents need to locate code by syntax structure (functions, structs, calls, imports) rather than plain text search. The existing `grep` tool works for literal text patterns but cannot understand code structure, handle AST-aware queries, or navigate complex nested syntax.

We needed a syntax-aware search tool that:

1. Finds code elements by their AST node types (e.g., "find all function declarations")
2. Returns structured captures with positions and text
3. Works across multiple programming languages
4. Integrates seamlessly with Phosphor's agent tool funnel
5. Does not require external binaries (self-contained via CGO)

## Decision

We implemented `structural_search` — a tree-sitter-based code search tool that uses S-expression queries against parsed ASTs. The implementation evolved from a Go-only tool to a **19-language** multi-language structural search engine.

### Core Architecture

```
structural_search tool
├── tools/structural_search.go    — Tool definition, params, execution
├── tools/structural_search.md.tpl — Tool description template
├── parser/parser.go              — Language detection, AST parsing, querying
└── parser/templates.go           — Language-specific S-expression templates
```

### Key Design Decisions

#### 1. Tree-Sitter Over Alternatives

**Chosen:** tree-sitter (C library via CGO bindings)
**Rejected:** AST parsing in pure Go (no mature multi-language parsers), LSP-only (requires language server running), regex-based parsing (fragile for nested syntax)

**Rationale:**
- Tree-sitter is the most mature incremental parser with 15+ language grammars
- Provides structured S-expression query language for precise AST matching
- Self-contained C sources — no external binaries needed
- Fast enough for multi-file directory scanning

#### 2. Language Auto-Detection

The `language` parameter in `StructuralSearchParams` is **optional**. If omitted, the tool auto-detects the language:

- From the `include` pattern (e.g., `"*.ts"` → `"typescript"`)
- From file extensions when walking directories
- Falls back to `"go"` if nothing matches

This preserves existing Go-only workflows while enabling multi-language searches.

#### 3. Language-Specific Templates

Templates are stored in a nested map: `map[language][templateName]SExpression`. Each language has its own set of S-expressions because tree-sitter node types differ significantly between languages.

The agent uses **human-readable template names** (`find_functions`, `find_structs`, `find_variables`, `find_interfaces`, `find_calls`, `find_imports`, `find_comments`) rather than raw S-expressions.

#### 4. Per-File Language Detection

Even when a target language is specified, each file is parsed with its own detected language via `parser.DetectLanguage(file)`. This handles mixed-language directories correctly.

#### 5. Tool Funnel Integration

The agent uses `structural_search` as the "sniper rifle" (precise, syntax-aware) and falls back to `grep` as the "nuclear option" for unknown text patterns:

```
TOOL FUNNEL PROTOCOL:
1. Try `structural_search` first for finding functions, structs, variables, interfaces, calls, imports, or comments by syntax structure.
2. Fallback to `grep` if the pattern is too complex or the file is too large to parse.
3. Use LSP tools for cross-file symbol resolution or type information.
```

#### 6. CGO Requirement

All tree-sitter Go bindings use CGO (C compilation). The project builds with `CGO_ENABLED=0` for releases, but development builds require a C compiler.

**CI/CD:** `.github/workflows/build.yml` includes `CGO_ENABLED: 1` in build and test steps. Windows runners install UCRT64 MinGW64 via `mingw-w64` package.

## Consequences

### Positive

- **Syntax-aware search:** Finds code by AST structure, not text patterns
- **Multi-language:** 19 languages supported (Go, TypeScript, JavaScript, Python, SQL, Rust, Java, C#, PHP, C++, C, Bash, HCL, Ruby, JSON, HTML, CSS, TOML, Scala)
- **Structured output:** Returns captures with file path, line/column positions, and matched text
- **Template abstraction:** Agent uses human-readable names, not raw S-expressions
- **Auto-detection:** Language auto-detected from file extensions — no manual configuration needed
- **Mixed-language support:** Each file parsed with its own grammar
- **Parallel with grep:** Not a replacement — agent chooses based on search pattern type
- **AST caching:** Parsed ASTs cached per-file to avoid re-parsing

### Negative

- **CGO required:** Tree-sitter is a C library, so CGO must be enabled. This excludes Android 32-bit and other CGO-disabled targets.
- **Build complexity:** Requires a C compiler (MSYS2 UCRT64 GCC on Windows, GCC/Clang on Linux/macOS)
- **Binary size:** CGO dependencies increase binary size
- **Go version:** `go.mod` requires `go >= 1.26.4`

### Neutral

- **S-expression learning curve:** Users must learn tree-sitter's S-expression syntax for custom queries
- **Template maintenance:** Each language requires its own set of S-expressions per template
- **Grammar versioning:** Tree-sitter grammars evolve — queries may break across versions

## Supported Languages

| Language | Extensions |
|---|---|
| Go | `.go` |
| TypeScript | `.ts`, `.tsx` |
| JavaScript | `.js`, `.jsx` |
| Python | `.py` |
| SQL | `.sql` |
| Rust | `.rs` |
| Java | `.java` |
| C# | `.cs` |
| PHP | `.php` |
| C++ | `.cpp`, `.cc`, `.cxx`, `.hpp`, `.hxx` |
| C | `.c`, `.h` |
| Bash | `.sh` |
| HCL | `.hcl` |
| Ruby | `.rb` |
| JSON | `.json` |
| HTML | `.html`, `.htm` |
| CSS | `.css` |
| TOML | `.toml` |
| Scala | `.scala`, `.sbt` |

## Dependencies

```
github.com/tree-sitter/go-tree-sitter v0.25.0
github.com/tree-sitter/tree-sitter-go v0.25.0
github.com/tree-sitter/tree-sitter-typescript v0.23.2
github.com/tree-sitter/tree-sitter-javascript v0.25.0
github.com/tree-sitter/tree-sitter-python v0.25.0
github.com/DerekStride/tree-sitter-sql v0.3.11
github.com/tree-sitter/tree-sitter-rust v0.24.2
github.com/tree-sitter/tree-sitter-java v0.23.5
github.com/tree-sitter/tree-sitter-c-sharp v0.23.5
github.com/tree-sitter/tree-sitter-php v0.24.2
github.com/tree-sitter/tree-sitter-cpp v0.23.4
github.com/tree-sitter/tree-sitter-c v0.24.2
github.com/tree-sitter/tree-sitter-bash v0.25.1
github.com/tree-sitter-grammars/tree-sitter-hcl v1.2.0
github.com/tree-sitter/tree-sitter-ruby v0.23.1
github.com/tree-sitter/tree-sitter-json v0.24.8
github.com/tree-sitter/tree-sitter-html v0.23.2
github.com/tree-sitter/tree-sitter-css v0.23.2
github.com/tree-sitter-grammars/tree-sitter-toml v0.7.0
github.com/tree-sitter/tree-sitter-scala v0.23.2
```

All grammar bindings require CGO (C compiler) since they embed the tree-sitter C parser source.

## Implementation Details

### Phase 1: Go-Only Implementation

- Added `github.com/tree-sitter/go-tree-sitter` and `github.com/tree-sitter/tree-sitter-go` to `go.mod`
- Updated `CGO_ENABLED: 0` → `1` in `Taskfile.yaml`, `.goreleaser.yml`, `.github/workflows/build.yml`
- Created `internal/agent/parser/parser.go` with `Parse()`, `Query()`, helper functions
- Created `internal/agent/tools/structural_search.go` with tool implementation
- Created `internal/agent/parser/templates.go` with 7 Go query templates
- Registered in `coordinator.go` and `common_test.go`
- Added `<tool_funnel>` section to `coder.md.tpl`

### Phase 2: Multi-Language Extension

- Refactored `templates.go` from `map[string]string` → `map[string]map[string]string` (language → template name → S-expression)
- Added `Language` type with constants for all 19 languages
- Added `GetLanguage(lang string) *sitter.Language` — maps language name to tree-sitter grammar pointer
- Added `DetectLanguage(filePath string) string` — maps file extensions to language names
- Added `SupportedLanguages()` — returns list of 19 supported language names
- Updated `Parse(code []byte, lang string)` — accepts language parameter, sets parser grammar dynamically
- Renamed `findGoFiles` → `findFiles` with language-aware extension filtering
- Added `Language` field to `StructuralSearchParams`
- Updated `executeStructuralSearch` to auto-detect language and use per-file parsing

### Phase 3: Additional Languages

- **SQL**: `github.com/DerekStride/tree-sitter-sql v0.3.11` — `create_function`, `create_table`, `select`, `insert`, `delete`
- **Rust**: `github.com/tree-sitter/tree-sitter-rust v0.24.2` — `function_item`, `struct_item`, `let_declaration`, `use_declaration`
- **Java**: `github.com/tree-sitter/tree-sitter-java v0.23.5` — `method_declaration`, `class_declaration`, `interface_declaration`, `method_invocation`
- **C#**: `github.com/tree-sitter/tree-sitter-c-sharp v0.23.5` — `method_declaration`, `class_declaration`, `invocation_expression`, `using_directive`
- **PHP**: `github.com/tree-sitter/tree-sitter-php v0.24.2` — `function_definition`, `class_declaration`, `variable_name`, `function_call_expression`
- **C++**: `github.com/tree-sitter/tree-sitter-cpp v0.23.4` — `function_definition`, `class_specifier`, `preproc_include`, `call_expression`
- **C**: `github.com/tree-sitter/tree-sitter-c v0.24.2` — `function_definition`, `struct_specifier`, `call_expression`, `preproc_include`
- **Bash**: `github.com/tree-sitter/tree-sitter-bash v0.25.1` — `function_definition`, `variable_assignment`, `command`
- **HCL**: `github.com/tree-sitter-grammars/tree-sitter-hcl v1.2.0` — `block`, `attribute`, `identifier`, `function_call`
- **Ruby**: `github.com/tree-sitter/tree-sitter-ruby v0.23.1` — `method`, `class`, `module`, `call`, `assignment`
- **JSON**: `github.com/tree-sitter/tree-sitter-json v0.24.8` — `object`, `pair`, `string` (nested objects, key-value pairs)
- **HTML**: `github.com/tree-sitter/tree-sitter-html v0.23.2` — `element`, `tag_name`, `attribute` (elements, attributes, script/style imports)
- **CSS**: `github.com/tree-sitter/tree-sitter-css v0.23.2` — `rule_set`, `selector_list`, `custom_property` (rule sets, custom properties, @import)
- **TOML**: `github.com/tree-sitter-grammars/tree-sitter-toml v0.7.0` — `table`, `pair`, `key` (tables, key-value pairs)
- **Scala**: `github.com/tree-sitter/tree-sitter-scala v0.23.2` — `class_definition`, `object_definition`, `function_definition`, `trait_definition` (classes, objects, functions, traits, method calls, imports)

## File Locations

```
internal/
  agent/
    parser/
      parser.go          # Tree-sitter parsing/querying API, language detection
      templates.go       # Language-specific S-expression templates (19 languages)
    tools/
      structural_search.go      # Tool implementation with multi-language support
      structural_search.md.tpl  # Tool description template with language list
    coordinator.go              # Tool registration (line 698)
    common_test.go              # Test setup registration (line 177)
templates/
  coder.md.tpl                  # Tool funnel protocol section
```

## Tree-Sitter API Notes

- `go-tree-sitter` v0.25.0 API used
- `Node.Utf8Text(source []byte)` — get node text from source
- `Query.CaptureNames()` — get all capture names by index
- `QueryCursor.Matches(query, node, text)` — execute query, returns `QueryMatches`
- `QueryMatches.Next()` — returns `*QueryMatch` (nil when exhausted)
- `QueryCapture.Node` — struct value (not pointer), use `&cap.Node` for methods

## Adding New Languages

To add support for a new language:

1. **Install the tree-sitter package**: `go get github.com/tree-sitter/tree-sitter-<lang>`
2. **Update `parser.go`**:
   - Add a `Language` constant
   - Add a case to `GetLanguage()` returning the grammar pointer
   - Add an extension → language mapping in `DetectLanguage()`
3. **Update `templates.go`**:
   - Add a new top-level key in `Templates` for the language
   - Add S-expression templates (see templates for reference)
4. **Update `structural_search.go`**:
   - Add the language's extensions to `findFiles()`
5. **Update `structural_search.md.tpl`**:
   - Add the language to the supported languages list

## Notes by Language

### Function Detection

| Language | Node Type | Parameters | Body |
|---|---|---|---|
| Go | `function_declaration` | `parameter_list` | `block` |
| Go | `method_declaration` | `parameter_list` | `block` |
| TypeScript | `function_declaration` | `formal_parameters` | `statement_block` |
| TypeScript | `arrow_function` | `formal_parameters` | `statement_block` / `expression` |
| TypeScript | `function_expression` | `formal_parameters` | `statement_block` |
| JavaScript | `function_declaration` | `formal_parameters` | `statement_block` |
| JavaScript | `arrow_function` | `formal_parameters` | `statement_block` / `expression` |
| JavaScript | `function_expression` | `formal_parameters` | `statement_block` |
| Python | `function_definition` | `parameters` | `block` |
| SQL | `create_function` | `function_arguments` | `function_body` |
| Rust | `function_item` | `formal_parameter_list` | `block` |
| Java | `method_declaration` | `formal_parameters` | `block` |
| C# | `method_declaration` | `formal_parameter_list` | `block` |
| PHP | `function_definition` / `method_declaration` | `formal_parameters` | `compound_statement` / `declaration_list` |
| C++ | `function_definition` | `parameter_list` | `compound_statement` |
| C | `function_definition` | `parameters` | `compound_statement` |
| Bash | `function_definition` | `formal_parameters` | `compound_statement` |
| HCL | `block` | `block_label` | `body` |
| Ruby | `method` / `singleton_method` | `method_parameters` | `body_statement` |
| Scala | `function_definition` | `formal_parameter_list` | `block` |

### Class/Struct Detection

| Language | Node Type | Name Field | Body |
|---|---|---|---|
| Go | `type_spec` → `struct_type` | `type_identifier` | `struct_field_declaration_list` |
| TypeScript | `class_declaration` | `type_identifier` | `class_body` |
| TypeScript | `type_alias_declaration` | `type_identifier` | `type` |
| JavaScript | `class_declaration` | `identifier` | `class_body` |
| JavaScript | `class` (expression) | `identifier` (optional) | `class_body` |
| Python | `class_definition` | `identifier` | `block` |
| SQL | `create_table` | `object_reference.name` | `column_definitions` |
| Rust | `struct_item` | `type_identifier` | `field_declaration_list` |
| Java | `class_declaration` | `identifier` | `class_body` |
| C# | `class_declaration` | `identifier` | `declaration_list` |
| PHP | `class_declaration` | `name` | `declaration_list` |
| C++ | `class_specifier` / `struct_specifier` | `type_identifier` | `field_declaration_list` |
| C | `struct_specifier` | `type_identifier` | `struct_body` |
| HCL | `block` | `identifier` / `block_label` | `body` |
| Ruby | `class` / `module` | `constant` / `scope_resolution` | `body_statement` |
| JSON | `object` → `pair` | `string` (key) | `object` (nested value) |
| HTML | `element` | `tag_name` | `element_children` |
| CSS | `rule_set` | `selector_list` | `block` |
| TOML | `table` | `key` | `array` |
| Scala | `class_definition` / `object_definition` | `identifier` | `class_body` / `template_body` |

### Call Detection

| Language | Node Type | Function Field | Arguments Field |
|---|---|---|---|
| Go | `call_expression` | `identifier` / `selector_expression` | `argument_list` |
| TypeScript | `call_expression` | `identifier` / `member_expression` | `arguments` |
| JavaScript | `call_expression` | `identifier` / `member_expression` | `arguments` |
| Python | `call` | `identifier` / `attribute` | `arguments` |
| Rust | `call_expression` | `identifier` / `field_expression` | `arguments` |
| Java | `method_invocation` | `identifier` | `formal_arguments` |
| C# | `invocation_expression` | `member` / `identifier` | `argument_list` |
| PHP | `function_call_expression` / `member_call_expression` / `scoped_call_expression` | `name` | `arguments` |
| C++ | `call_expression` | `identifier` / `field_expression` | `argument_list` |
| C | `call_expression` | `identifier` / `field_expression` | `arguments` |
| Bash | `command` | `word` | `arguments` |
| HCL | `function_call` | `identifier` | `arguments` |
| Ruby | `call` | `identifier` | `argument_list` |
| Scala | `call_expression` | `identifier` | `argument_list` |

### Import Detection

| Language | Node Type | Module Path | Import Name |
|---|---|---|---|
| Go | `import_declaration` | `interpreted_string_literal` | `import_spec.name` |
| TypeScript | `import_statement` → `import_clause` → `named_imports` | `source` (string) | `import_specifier.name` |
| JavaScript | `import_statement` → `import_clause` → `named_imports` | `source` (string) | `import_specifier.name` |
| Python | `import_statement` | `(dotted_name)` | `aliased_import.name` |
| Python | `import_from_statement` | `module_name` (dotted_name) | `aliased_import.name` |
| Rust | `use_declaration` | `scoped_identifier.path` | `scoped_identifier.name` / `identifier` |
| Java | `import_declaration` | `scoped_name` | `scoped_name.name` |
| C# | `using_directive` | `qualified_name` / `identifier` | `name` |
| PHP | `namespace_use_declaration` / `use_declaration` | `qualified_name` | `name` (alias) |
| C++ | `preproc_include` | `string_literal` / `system_lib_string` | — |
| HTML | `element` (script/link) | `tag_name` | `attribute_name` (src/href) |
| CSS | `import_statement` | `string` | — |
| TOML | — | — | — |
| Scala | `import_declaration` | `scoped_identifier` | `identifier` |

## Language-Specific Notes

- **Python**: Comments are `extra: true` nodes (like Go) — they appear in the AST when queried.
- **JavaScript**: No interface construct; `find_interfaces` template is empty. CommonJS `require()` is just a `call_expression` with function name `"require"` — not a separate node type.
- **TypeScript**: `import_require_clause` (`const x = require('...')`) captured separately from ES module imports.
- **SQL**: No variables, interfaces, or function calls — those templates are empty. Uses `object_reference` for identifiers. Supports `create_view`, `create_procedure`, `create_index`, `create_type` — but only `create_function` and `create_table` have templates. SQL-specific templates: `find_select_tables`, `find_joins`, `find_inserts`, `find_deletes`, `find_select_all`. PostgreSQL `schema.table` syntax produces `(table_name (identifier) (identifier))`.
- **PHP**: Uses `function_definition` (top-level) and `method_declaration` (class methods), both with `formal_parameters`. Uses `name` (not `identifier`) for function/method/class names. Uses `declaration_list` for class/trait/interface bodies, `compound_statement` for function bodies. Uses `variable_name` (with `$` prefix) for variable identifiers. Has three call types: `function_call_expression` (bare), `member_call_expression` (`$obj->method()`), `scoped_call_expression` (`Class::method()`). Uses `namespace_use_declaration` and `use_declaration` for imports. Has `trait_declaration` and `enum_declaration` alongside `class_declaration`. Supports `anonymous_function` and `arrow_function` (short `fn()` syntax). Comments captured via `comment` node type (covers `//`, `/* */`, and `#` styles).
- **C++**: Uses `function_definition` for both top-level functions and methods, with `function_declarator` containing the name. Uses `class_specifier` for `class` and `struct_specifier` for `struct`. Uses `field_expression` for member access (`obj.method()` or `obj->method()`). Uses `compound_statement` for code blocks. Uses `preproc_include` for `#include` directives, with `string_literal` for `"file.h"` and `system_lib_string` for `<stdio.h>`. Uses `identifier` for variable names, `init_declarator` for declarations with initialization. Has `namespace_definition` and `qualified_identifier` for namespaced code. Supports `template_function`, `template_method`, and `template_argument_list` for templated code. Comments captured via `comment` node type (covers `//` and `/* */` styles).
- **C**: Uses `function_definition` for functions, with `function_declarator` containing the name and `parameters`. Uses `struct_specifier` for struct definitions, with `struct_body` for the body. Uses `call_expression` for function calls, with `identifier` or `field_expression` for the function name. Uses `compound_statement` for code blocks. Uses `preproc_include` for `#include` directives, with `string_literal` for `"file.h"` and `system_lib_string` for `<stdio.h>`. Uses `identifier` for variable names, `init_declarator` for declarations with initialization. Comments captured via `comment` node type (covers `//` and `/* */` styles). No interfaces or imports — those templates are empty.
- **Bash**: Uses `function_definition` for functions, with `identifier` for the name and `formal_parameters` for parameters. Uses `compound_statement` for function bodies and code blocks. Uses `variable_assignment` for variable assignments, with `identifier` for variable names. Uses `command` for command execution, with `word` for the command name and `arguments` for arguments. Comments captured via `comment` node type (covers `#` style). No classes, structs, interfaces, or imports — those templates are empty.
- **HCL**: Uses `block` for Terraform resources, variables, outputs, etc., with `type` and `labels` fields. Uses `attribute` for key-value pairs (e.g., `name = "value"`). Uses `identifier` for block types and attribute names. Uses `body` for the nested content inside blocks. Supports `function_call` for built-in functions (e.g., `length()`, `concat()`). Comments captured via `comment` node type (covers `#`, `//`, and `/* */` styles). No interfaces or imports — those templates are empty.
- **Ruby**: Uses `method` for instance methods and `singleton_method` for class/instance methods defined on specific objects. Uses `class` and `module` for class/module definitions, with `constant` or `scope_resolution` for names. Uses `body_statement` for method/class/module bodies. Uses `call` for method calls, with `identifier` for method name and `argument_list` for arguments. Uses `assignment` for variable assignments, with `identifier` for variable names. No interfaces or imports — those templates are empty. Comments captured via `comment` node type (covers `#` and `=begin ... =end` styles).
- **Rust**: No interfaces (traits are captured via `find_interfaces` → `trait_item`). Uses `use_declaration` for imports, with `scoped_identifier` for `use foo::bar` and bare `identifier` for `use bar`. Has both `line_comment` (`//`) and `block_comment` (`/* */`) — both captured by `find_comments`. Uses `function_item` (not `function_declaration`), `let_declaration` (not `var_declaration`), `use_declaration` (not `import_declaration`). Has `enum_item`, `trait_item`, and `impl_item` — only `trait_item` has a template (for `find_interfaces`).
- **Java**: Uses `method_invocation` (not `call_expression`) for method calls, with `formal_arguments` (not `arguments`). Has `interface_declaration` and `enum_declaration` — only `interface_declaration` has a template (for `find_interfaces`). Uses `scoped_name` for import paths (e.g., `java.util.List`).
- **C#**: Uses `invocation_expression` (not `call_expression`) for method calls, with `argument_list` (not `arguments`). Uses `using_directive` (not `import_declaration`) for imports. Has `enum_declaration`, `field_declaration`, `variable_declarator`, and `lambda_expression`. Comments captured via `comment` node type.
- **JSON**: No functions, interfaces, calls, or imports — those templates are empty. `find_structs` matches nested objects: an `object` containing a `pair` with a `string` key and `object` value. `find_variables` matches key-value pairs: a `pair` with a `string` key and any value. Comments captured via `comment` node type (JSONC format).
- **HTML**: No functions, interfaces, or calls — those templates are empty. `find_structs` matches elements with tag names and children via `element` → `tag_name` → `element_children`. `find_variables` matches attributes: an `attribute` with `attribute_name` and `attribute_value`. `find_imports` matches elements with src/href attributes (e.g., `<script src="...">`, `<link href="...">`). Comments captured via `comment` node type (covers `<!-- -->` style).
- **CSS**: No functions, interfaces, or calls — those templates are empty. `find_structs` matches rule sets: a `rule_set` with a `selector_list` and `block` body. `find_variables` matches CSS custom properties: a `custom_property` with `property_name` and `value`. `find_imports` matches `@import` statements: an `import_statement` with a `string` value. Comments captured via `comment` node type (covers `/* */` style).
- **TOML**: No functions, interfaces, or calls — those templates are empty. `find_structs` matches tables with arrays: a `table` with a `key` name and `array` value. `find_variables` matches key-value pairs: a `pair` with a `key` name and any value. No imports — that template is empty. Comments captured via `comment` node type (covers `#` style).
- **Scala**: Has both `class_definition` and `object_definition` for classes and companion objects. `find_structs` captures both `class_definition` (with `class_body`) and `object_definition` (with `template_body`). `find_interfaces` captures `trait_definition` (with `template_body`). `find_calls` captures `call_expression` with `identifier` function name and `argument_list` arguments. `find_imports` captures `import_declaration` with `scoped_identifier` paths. Has `function_definition` for methods, with `formal_parameter_list` for parameters and `block` for body. Has `case_class`, `enum`, `extension`, and `given` constructs — not yet templated. Comments captured via `comment` node type (covers `//` and `/* */` styles).

## Pre-existing Issues

- **Go version mismatch**: `go.mod` requires `go >= 1.26.4`, but the development environment may have an older version.
- **CGO build constraints**: All tree-sitter bindings require CGO, which may not be available in all environments.

## References

- [RFC: Structural Search with Tree-Sitter](.agents/docs/rfc/structural-search-with-tree-sitter.md)
- [Implementation Notes: TREE_SITTER.md](TREE_SITTER.md)
- [Update Log: TREE_SITTER_UPDATES.md](TREE_SITTER_UPDATES.md)
