// Package lang_typescript provides tree-sitter bindings for typescript.
package lang_typescript

/*
#cgo CFLAGS: -I${SRCDIR}/../../../../grammars/include -I${SRCDIR}/../../../../grammars

#include "tree_sitter/parser.h"

#include "typescript/src/parser.c"
#undef TS_PUBLIC
#include "typescript/src/scanner.c"
*/
import "C"

import (
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for typescript.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_typescript()))
}
