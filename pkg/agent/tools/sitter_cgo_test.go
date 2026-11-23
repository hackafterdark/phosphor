//go:build cgo

package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/agent/parser"
	"github.com/stretchr/testify/require"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// countASTErrorsNaive walks the whole tree including nested error nodes. It
// exists only to contrast with collectASTErrors in tests.
func countASTErrorsNaive(code, lang string) int {
	root := parser.Parse([]byte(code), lang)
	var count func(n *sitter.Node) int
	count = func(n *sitter.Node) int {
		if n == nil {
			return 0
		}
		c := 0
		if n.IsError() || n.IsMissing() || n.Kind() == "ERROR" {
			c++
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			c += count(n.Child(i))
		}
		return c
	}
	return count(root)
}

func TestCollectASTErrors_NestedErrorsNotDoubleCounted(t *testing.T) {
	// Each declaration is broken; tree-sitter nests the downstream damage
	// inside the first ERROR subtree. Recursion stops at ERROR boundaries, so
	// the collector reports one top-level failure, not one per nested node.
	code := "package main\n"
	for i := 0; i < 12; i++ {
		code += fmt.Sprintf("func b%d( {\n", i)
	}
	errs := collectASTErrors(code, "go")
	naive := countASTErrorsNaive(code, "go")
	require.Len(t, errs, 1)
	require.Greater(t, naive, len(errs))
}

func TestFormatASTErrorLocations_CapsAtTen(t *testing.T) {
	errs := make([]astError, 12)
	for i := range errs {
		errs[i] = astError{line: i + 1, col: 1}
	}
	out := formatASTErrorLocations(errs)
	require.Contains(t, out, "(+2 more)")
	require.Equal(t, 10, strings.Count(out, ":"))
}
