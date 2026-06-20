// Package lang_json provides tree-sitter bindings for json.
package lang_json

/*
#cgo CFLAGS: -IF:/hackafterdark/phosphor/grammars/include -IF:/hackafterdark/phosphor/grammars

#include "tree_sitter/parser.h"

#include "json/src/parser.c"
*/
import "C"

import (
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for json.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_json()))
}
