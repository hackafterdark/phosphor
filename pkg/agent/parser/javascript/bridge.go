// Package lang_javascript provides tree-sitter bindings for javascript.
package lang_javascript

/*
#cgo CFLAGS: -I${SRCDIR}/../../../../grammars/include -I${SRCDIR}/../../../../grammars

#include "tree_sitter/parser.h"

#include "javascript/src/parser.c"
#undef TS_PUBLIC
#include "javascript/src/scanner.c"
*/
import "C"

import (
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for javascript.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_javascript()))
}
