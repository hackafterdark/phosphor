// Package lang_rust provides tree-sitter bindings for rust.
package lang_rust

/*
#cgo CFLAGS: -I${SRCDIR}/../../../../grammars/include -I${SRCDIR}/../../../../grammars

#include "tree_sitter/parser.h"

#include "rust/src/parser.c"
#undef TS_PUBLIC
#include "rust/src/scanner.c"
*/
import "C"

import (
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for rust.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_rust()))
}
