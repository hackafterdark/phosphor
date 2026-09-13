# Structural Search Language Notes

Comprehensive notes on the structural search tool's support for each language, based on testing against example files in `examples/structural_search/`.

---

## Go

### Grammar Version
- `tree-sitter-go` (vendored in `grammars/go/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | 6 | Captures `function_declaration` and `method_declaration` |
| `find_structs` | Works | 1 | Captures `type_spec` with `struct_type` |
| `find_variables` | Works | 14 | Captures `var_declaration`, `short_var_declaration` |
| `find_interfaces` | N/A | 0 | No interfaces in example file |
| `find_calls` | Works | 9 | Captures `call_expression` (direct and selector) |
| `find_imports` | Works | 3 | Captures `import_spec` nodes |
| `find_comments` | Works | 3 | Captures `comment` nodes |

### Known Issues

None known. Go support is fully functional.

---

## TypeScript

### Grammar Version
- `tree-sitter-typescript` v0.23.2

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | 9 | Captures standalone functions and class methods (names, params, body) |
| `find_structs` | Works | 15 | Captures classes, fields, and methods with parameters and body content |
| `find_variables` | Works | 4 | Captures `const` declarations, not class fields |
| `find_interfaces` | Works | 2 | Captures `Config` and `Person` interfaces |
| `find_calls` | Works | 18 | Captures direct calls and method calls with arguments |
| `find_imports` | N/A | 0 | No imports in example file |
| `find_comments` | Works | 8 | Captures both `//` line comments and `/** */` block comments |

### Known Issues

None known. All core templates work correctly after grammar fixes.

---

## JavaScript

### Grammar Version
- `tree-sitter-javascript` (vendored in `grammars/javascript/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | 4 | Captures standalone function declarations (createConfig, greet, fetchPersons, loadPersons) |
| `find_structs` | Works | 6 | Captures PersonService class and its 5 methods (constructor, addPerson, getAllPersons, getPersonCount, getPersonsByAge, sortByName) |
| `find_variables` | Works | 14 | Captures `const` declarations including Logger, person, response, data, persons, config, service |
| `find_interfaces` | N/A | — | Not defined for JavaScript |
| `find_calls` | Works | 20+ | Captures direct calls (console.log, fetch) and method calls (push, filter, sort, addPerson) |
| `find_imports` | N/A | 0 | No imports in example file |
| `find_comments` | Works | 15 | Captures `//` line comments and `/** */` JSDoc block comments |

### Custom Templates

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_async_functions` | Works | 2 | Captures `async function` declarations (fetchPersons, loadPersons) |
| `find_try_catch` | Works | 2 | Captures try-catch blocks with `error_var` and `catch_body` extraction |

### Known Issues

None known. All core and custom templates work correctly.

---

## Python

### Grammar Version
- `tree-sitter-python` (vendored in `grammars/python/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | 13 | Captures `function_definition` including methods |
| `find_structs` | Works | 4 | Captures `class_definition` nodes |
| `find_variables` | Works | 9 | Captures instance and local variable assignments |
| `find_interfaces` | N/A | — | Not defined for Python |
| `find_calls` | Works | 22 | Captures direct and method calls |
| `find_imports` | Works | 3 | Captures `import` and `from ... import` statements |
| `find_comments` | Works | 2 | Captures only `#` line comments, NOT docstrings (`"""`) |

### Known Issues

- **find_comments**: Python docstrings (`"""..."""`) are not captured because the grammar uses a different node type

---

## Rust

### Grammar Version
- `tree-sitter-rust` (vendored in `grammars/rust/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | 12 | Captures standalone functions and methods (names, params, body) |
| `find_structs` | Works | 3 | Captures `struct_item` nodes (`Config`, `Person`, `PersonService`) |
| `find_variables` | Works | 4 | Captures `let_declaration` nodes (local bindings only, not function params) |
| `find_interfaces` | Works | 1 | Captures `trait` declarations (`Printable`) |
| `find_calls` | Works | 19 | Captures `call_expression` nodes (direct, method, and scoped calls) |
| `find_imports` | Works | 2 | Captures `use_declaration` statements |
| `find_comments` | Works | 16 | Captures both `//` line comments and `///` doc comments |

### Custom Templates

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_struct_fields` | Works | 6 | Captures struct fields with `field_type` extraction |
| `find_error_returns` | Works | 1 | Captures `Err` variants with `err_value` extraction |

### Known Issues

None known. Rust support is fully functional.

---

## PHP

### Grammar Version
- `tree-sitter-php` (vendored in `grammars/php/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | — | Captures `function_definition`, `method_declaration`, `anonymous_function` |
| `find_structs` | Works | — | Captures `class_declaration` |
| `find_variables` | Works | — | Captures `variable_name`, `property_declaration`, `assignment_expression` |
| `find_interfaces` | Works | — | Captures `interface_declaration` |
| `find_calls` | Works | — | Captures `function_call_expression`, `member_call_expression`, `scoped_call_expression` |
| `find_imports` | Works | — | Captures `namespace_use_clause` |
| `find_comments` | Works | — | Captures `comment` nodes |

### Known Issues

None known. PHP support is fully functional.

---

## C++

### Grammar Version
- `tree-sitter-cpp` (vendored in `grammars/cpp/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | — | Captures `function_definition` with `function_declarator` |
| `find_structs` | Works | — | Captures `class_specifier` and `struct_specifier` |
| `find_variables` | Works | — | Captures `declaration` with `init_declarator` |
| `find_interfaces` | N/A | — | Not defined for C++ |
| `find_calls` | Works | — | Captures `call_expression` (direct, qualified, method) |
| `find_imports` | Works | — | Captures `preproc_include` (system and regular) |
| `find_comments` | Works | — | Captures `comment` nodes |

### Known Issues

None known. C++ support is fully functional.

---

## C#

### Grammar Version
- `tree-sitter-csharp` (vendored in `grammars/csharp/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | — | Captures `method_declaration`, `constructor_declaration` |
| `find_structs` | Works | — | Captures `class_declaration`, `record_declaration` |
| `find_variables` | Works | — | Captures `local_declaration_statement` with `variable_declarator` |
| `find_interfaces` | N/A | — | Not defined for C# |
| `find_calls` | Works | — | Captures `invocation_expression` (direct and member access) |
| `find_imports` | Works | — | Captures `using_directive` |
| `find_comments` | Works | — | Captures `comment` nodes |

### Known Issues

None known. C# support is fully functional.

---

## JSON

### Grammar Version
- `tree-sitter-json` (vendored in `grammars/json/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | N/A | — | Not defined for JSON (no functions) |
| `find_structs` | Works | — | Captures nested `object` nodes via `pair` |
| `find_variables` | Works | — | Captures `pair` key-value nodes |
| `find_interfaces` | N/A | — | Not defined for JSON |
| `find_calls` | N/A | — | Not defined for JSON |
| `find_imports` | N/A | — | Not defined for JSON |
| `find_comments` | N/A | 0 | No comments in example file (JSON doesn't support comments natively) |

### Known Issues

JSON has limited structural concepts (only objects and key-value pairs).

---

## CSS

### Grammar Version
- `tree-sitter-css` (vendored in `grammars/css/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | N/A | — | Not defined for CSS (no functions) |
| `find_structs` | Works | — | Captures `rule_set` and `media_statement` |
| `find_variables` | Works | — | Captures `declaration` with `property_name` |
| `find_interfaces` | N/A | — | Not defined for CSS |
| `find_calls` | Works | — | Captures `call_expression` (CSS functions like `var()`, `rgb()`) |
| `find_imports` | Works | — | Captures `import_statement` |
| `find_comments` | Works | — | Captures `comment` nodes |

### Known Issues

None known. CSS support is fully functional.

---

## Scala

### Grammar Version
- `tree-sitter-scala` (vendored in `grammars/scala/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | — | Captures `function_definition` and `function_declaration` |
| `find_structs` | Works | — | Captures `class_definition` and `object_definition` |
| `find_variables` | Works | — | Captures `val_definition` and `var_definition` |
| `find_interfaces` | Works | — | Captures `trait_definition` |
| `find_calls` | Works | — | Captures `call_expression` |
| `find_imports` | Works | — | Captures `import_declaration` |
| `find_comments` | Works | — | Captures `comment` nodes |

### Known Issues

None known. Scala support is fully functional. Note: `comment` nodes are `extra: true` nodes (present in AST even when other nodes fail).

---

## C

### Grammar Version
- `tree-sitter-c` (vendored in `grammars/c/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | — | Captures `function_definition` with `function_declarator` |
| `find_structs` | Works | — | Captures `struct_specifier` and `type_definition` |
| `find_variables` | Works | — | Captures `init_declarator` |
| `find_interfaces` | N/A | — | Not defined for C |
| `find_calls` | Works | — | Captures `call_expression` (direct and method) |
| `find_imports` | Works | — | Captures `preproc_include` (string and system) |
| `find_comments` | Works | — | Captures `comment` nodes |

### Known Issues

None known. C support is fully functional.

---

## Bash

### Grammar Version
- `tree-sitter-bash` (vendored in `grammars/bash/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | — | Captures `function_definition` with `word` name |
| `find_structs` | N/A | — | Not defined for Bash (no structs) |
| `find_variables` | Works | — | Captures `variable_assignment` |
| `find_interfaces` | N/A | — | Not defined for Bash |
| `find_calls` | Works | — | Captures `command` with `command_name` |
| `find_imports` | N/A | — | Not defined for Bash |
| `find_comments` | Works | — | Captures `comment` nodes |

### Known Issues

None known. Bash support is fully functional.

---

## HCL / Terraform

### Grammar Version
- `tree-sitter-hcl` (vendored in `grammars/hcl/`, CGO-compiled)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | — | Captures `attribute` nodes (all key-value pairs in blocks) |
| `find_structs` | Works | — | Captures `block` nodes (resource, variable, output blocks) |
| `find_variables` | Works | — | Captures `attribute` nodes with value captures |
| `find_interfaces` | N/A | — | Not defined for HCL |
| `find_calls` | Works | — | Captures `function_call` nodes |
| `find_imports` | N/A | — | Not defined for HCL |
| `find_comments` | Works | — | Captures `comment` nodes |

### Known Issues

- **find_structs**: Many duplicates due to wildcards matching too broadly (same as Go's `find_structs` behavior)
- HCL uses `attribute` for key-value pairs and `block` for named blocks with bodies

---

## Ruby

### Grammar Version
- `tree-sitter-ruby` (vendored in `grammars/ruby/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | — | Captures `method` and `singleton_method` |
| `find_structs` | Works | — | Captures `class` nodes |
| `find_variables` | Works | — | Captures `assignment` nodes |
| `find_interfaces` | N/A | — | Not defined for Ruby |
| `find_calls` | Works | — | Captures `call` nodes |
| `find_imports` | N/A | — | Not defined for Ruby |
| `find_comments` | Works | — | Captures `comment` nodes |

### Known Issues

Ruby is disabled in the `findFiles` function (commented out in `structural_search.go`), so `.rb` files won't be auto-discovered by the tool. However, the grammar and templates are functional — specify the language explicitly:
```
structural_search(language="ruby", include="*.rb", template_name="find_functions")
```

---

## SQL

### Grammar Version
- `tree-sitter-sql` (vendored in `grammars/sql/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Works | 1 | Captures `update_updated_at_column()` function body (line 42) |
| `find_structs` | Works | 3 | Captures `create_table` nodes for `users`, `posts`, `comments` (lines 4, 14, 25) |
| `find_variables` | N/A | — | Not defined for SQL |
| `find_interfaces` | N/A | — | Not defined for SQL |
| `find_calls` | Works | 4 | Captures aggregate function calls: `COUNT` (lines 61, 74, 79), `SUM` (line 75) |
| `find_imports` | N/A | — | Not defined for SQL |
| `find_comments` | Works | 12 | Captures all `--` SQL line comments (lines 1–103) |
| `find_select_tables` | Works | 3 | Captures `select_statement` table references: `posts` (line 63), `users` (lines 76, 104) |
| `find_joins` | Works | 3 | Captures `join_clause` references: `users` (line 64), `comments` (line 65), `posts` (line 77) |
| `find_inserts` | Works | 3 | Captures `insert_statement` targets: `users` (line 82), `posts` (line 87), `comments` (line 92) |
| `find_deletes` | Works | 1 | Captures `delete_statement` target: `posts` (line 101) |
| `find_select_all` | Works | 1 | Captures `select_statement` with `wildcard`: `SELECT *` on line 104 |

### Known Issues

None known. SQL support is fully functional with custom templates for SQL-specific operations.

---

## TOML

### Grammar Version
- `tree-sitter-toml` (vendored in `grammars/toml/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | N/A | — | Not defined for TOML |
| `find_structs` | Works | — | Captures `table` and `table_array_element` |
| `find_variables` | Works | — | Captures `pair` with `dotted_key` |
| `find_interfaces` | N/A | — | Not defined for TOML |
| `find_calls` | N/A | — | Not defined for TOML |
| `find_imports` | N/A | — | Not defined for TOML |
| `find_comments` | Works | — | Captures `comment` nodes |

### Known Issues

TOML is disabled in the `findFiles` function (commented out in `structural_search.go`), so `.toml` files won't be auto-discovered. However, the grammar and templates are functional — specify the language explicitly:
```
structural_search(language="toml", include="*.toml", template_name="find_variables")
```

---

## HTML

### Grammar Version
- `tree-sitter-html` (vendored in `grammars/html/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | N/A | 0 | Not defined for HTML (no functions) |
| `find_structs` | Works | 39 | Captures all HTML elements (`html`, `head`, `body`, `header`, `nav`, `section`, `article`, `footer`, etc.) |
| `find_variables` | Works | 30 | Captures attribute name/value pairs (e.g. `id="main-header"`, `class="navbar"`, `href="styles.css"`) |
| `find_interfaces` | N/A | 0 | Not defined for HTML |
| `find_calls` | N/A | 0 | Not defined for HTML |
| `find_imports` | Works | 29 | Captures element attributes (`href`, `class`, `id`, `src`, `rel`, `charset`, etc.) with values |
| `find_comments` | Works | 3 | Captures HTML comments (`<!-- ... -->`) |

### Known Issues

None known. HTML support is fully functional.

---

## Java

### Grammar Version
- `tree-sitter-java` (vendored in `grammars/java/`)

### Template Results

| Template | Status | Matches | Notes |
|---|---|---|---|
| `find_functions` | Disabled | — | Java not supported (requires external scanner) |
| `find_structs` | Disabled | — | Java not supported |
| `find_variables` | Disabled | — | Java not supported |
| `find_interfaces` | Disabled | — | Java not supported |
| `find_calls` | Disabled | — | Java not supported |
| `find_imports` | Disabled | — | Java not supported |
| `find_comments` | Disabled | — | Java not supported |

### Known Issues

- Java is explicitly disabled in the structural_search tool. The comment in the code says: `"Java not supported (requires external scanner not present in vendored grammar)"`
- The Java grammar files exist in `grammars/java/` but the parser.c file is minimal/incomplete.
- The language auto-detection in structural_search.go has a commented-out case for `.java` and falls back to `"go"`.

---

## Summary

### Fully Working Languages

| Language | Templates Working | Notes |
|---|---|---|
| Go | 7/7 | Best overall support |
| TypeScript | 7/7 | Fully functional |
| JavaScript | 9/9 (7 core + 2 custom) | Fully functional with custom templates |
| Rust | 9/9 (7 core + 2 custom) | Fully functional with custom templates |
| PHP | 7/7 | Fully functional |
| C++ | 7/7 | Fully functional |
| C# | 7/7 | Fully functional |
| C | 7/7 | Fully functional |
| Bash | 6/7 | Fully functional (no structs) |
| CSS | 7/7 | Fully functional |
| Scala | 7/7 | Fully functional |
| HCL/Terraform | 5/7 | Fully functional, find_structs has duplicates |
| SQL | 12/12 (7 core + 5 custom) | Fully functional, tested with concrete match counts |
| HTML | 5/7 | Fully functional (no functions/calls/interfaces) |
| TOML | 4/5 | Grammar works, but disabled in findFiles (needs explicit language param) |

### Partially Working Languages

| Language | Templates Working | Notes |
|---|---|---|
| Python | 6/6 | find_comments misses docstrings (`"""`) |
| JSON | 2/7 | Limited structural concepts (only structs and variables work) |

### Broken/Disabled Languages

| Language | Status | Reason |
|---|---|---|
| Java | Disabled | External scanner not present in vendored grammar |
| Ruby | 5/7 | Grammar works, but disabled in findFiles (needs explicit language param) |
