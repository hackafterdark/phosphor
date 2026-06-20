//go:build !cgo

package agent

// structuralSearchAvailable is false when CGO is disabled (tree-sitter unavailable).
var structuralSearchAvailable = false
