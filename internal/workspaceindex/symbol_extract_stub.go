//go:build !cgo

// Package workspaceindex provides symbol extraction from source files.
//
// Code-symbol extraction relies on tree-sitter, which requires CGO. When the
// binary is built with CGO_ENABLED=0 the tree-sitter-backed implementation in
// symbol_extract.go is excluded, so this stub keeps the package buildable and
// makes code-symbol indexing a graceful no-op. The rest of the workspace index
// (document/full-text indexing) still works without CGO; only the
// symbol-specific part is skipped.
package workspaceindex

import "context"

// indexCodeSymbols is a no-op when CGO is disabled. Without tree-sitter there
// is no AST to walk, so code files are simply omitted from the symbol index
// rather than failing the surrounding indexing operation.
func (i *Indexer) indexCodeSymbols(ctx context.Context, relPath string, data []byte) error {
	return nil
}
