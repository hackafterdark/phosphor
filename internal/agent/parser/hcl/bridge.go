// Package lang_hcl provides tree-sitter bindings for hcl.
package lang_hcl

/*
#cgo CFLAGS: -IF:/hackafterdark/phosphor/grammars/include -IF:/hackafterdark/phosphor/grammars

#include "tree_sitter/parser.h"

#include "hcl/src/parser.c"
#include "hcl/src/scanner.c"
*/
import "C"

import (
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for hcl.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_hcl()))
}
