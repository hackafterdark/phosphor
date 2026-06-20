//go:build cgo

package agent

// structuralSearchAvailable is true when CGO is enabled (required for tree-sitter).
var structuralSearchAvailable = true
