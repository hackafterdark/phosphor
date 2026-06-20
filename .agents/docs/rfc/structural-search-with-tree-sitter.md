### Phase 1: Dependency &amp; Grammar Setup

Add the core Go bindings and the C-based grammar libraries.

1. **Dependencies:** Add to your `go.mod`:

  Bash
  ```
  go get github.com/tree-sitter/go-tree-sitter
  go get github.com/tree-sitter/tree-sitter-go
  
  ```
2. **Grammar Management:** Create a directory `internal/agent/parser/` to hold your grammar-specific logic. Since you want to be "zero-dependency" (as in, no external binaries), statically link the C sources or use the pure-Go implementations if available for your target languages.

### Phase 2: The Structural Search Tool

Implement the `StructuralSearch` struct to replace your grep-based navigation.

Go

```
// internal/agent/tools/structural_search.go
package tools

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/tree-sitter/tree-sitter-go"
)

type StructuralSearchTool struct{}

func (t *StructuralSearchTool) Execute(code []byte, querySExpr string) ([]Match, error) {
    parser := sitter.NewParser()
    parser.SetLanguage(tree_sitter_go.GetLanguage())

    tree := parser.Parse(code, nil)
    query, _ := sitter.NewQuery([]byte(querySExpr), tree_sitter_go.GetLanguage())
    
    cursor := sitter.NewQueryCursor()
    cursor.Exec(query, tree.RootNode())

    var results []Match
    for {
        match, ok := cursor.NextMatch()
        if !ok { break }
        // Map match.Captures to JSON structure...
    }
    return results, nil
}

```

### Phase 3: The "Query Template" Registry

Don't make the LLM write S-expressions. Create an abstraction layer in `internal/agent/parser/templates.go`.

- **Registry:** Create a `map[string]string` where keys are human-readable (e.g., `"find_functions"`) and values are the S-expression patterns.
- **Agent Exposure:** Update your `ToolRegistry` so the agent calls `structural_search` with a `template_name` parameter.

### Phase 4: Integration into the Agent Funnel

Update `coder.md.tpl` to force the agent into the "Native Structural Search" mindset.

> **TOOL FUNNEL PROTOCOL:**
>
> 1. **Try** `structural_search` **first:** Use this for finding functions, structs, or variable declarations. It is more precise than `grep`.
> 2. **Fallback to** `grep`**:** Use only if the syntax pattern is too complex or the file is too large to parse.
> 3. **LSP:** Only use for "Find References" or cross-file symbol resolution.



