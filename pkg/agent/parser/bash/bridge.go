// Package lang_bash provides tree-sitter bindings for bash.
package lang_bash

/*
#cgo CFLAGS: -I${SRCDIR}/../../../../grammars/include -I${SRCDIR}/../../../../grammars

#include "tree_sitter/parser.h"

#include "bash/src/parser.c"
#undef TS_PUBLIC
#include "bash/src/scanner.c"
*/
import "C"

import (
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for bash.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_bash()))
}
