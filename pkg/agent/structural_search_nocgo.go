//go:build !cgo

package agent

// StructuralSearchAvailable is false when CGO is disabled (tree-sitter unavailable).
var StructuralSearchAvailable = false
