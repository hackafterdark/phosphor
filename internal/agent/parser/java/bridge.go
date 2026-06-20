// Package lang_java provides tree-sitter bindings for java.
package lang_java

/*
#cgo CFLAGS: -IF:/hackafterdark/phosphor/grammars/include -IF:/hackafterdark/phosphor/grammars

#include "tree_sitter/parser.h"

#include "java/src/parser.c"
*/
import "C"

import (
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// GetLanguage returns the tree-sitter language for java.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_java()))
}
