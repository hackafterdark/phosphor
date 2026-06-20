// Package lang_python provides tree-sitter bindings for python.
package lang_python

/*
#cgo CFLAGS: -IF:/hackafterdark/phosphor/grammars/include -IF:/hackafterdark/phosphor/grammars

#include "tree_sitter/parser.h"

#include "python/src/parser.c"
#include "python/src/scanner.c"
*/
import "C"

import (
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for python.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_python()))
}
