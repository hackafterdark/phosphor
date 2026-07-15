// Package lang_toml provides tree-sitter bindings for toml.
package lang_toml

/*
#cgo CFLAGS: -I${SRCDIR}/../../../../grammars/include -I${SRCDIR}/../../../../grammars

#include "tree_sitter/parser.h"

#include "toml/src/parser.c"
#undef TS_PUBLIC
#include "toml/src/scanner.c"
*/
import "C"

import (
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for toml.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_toml()))
}
