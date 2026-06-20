// Package lang_typescript provides tree-sitter bindings for typescript.
package lang_typescript

/*
#cgo CFLAGS: -IF:/hackafterdark/phosphor/grammars/include -IF:/hackafterdark/phosphor/grammars

#include "tree_sitter/parser.h"

#include "typescript/src/parser.c"
#include "typescript/src/scanner.c"

int get_external_lex_state_3852() {
	return ts_lex_modes[3852].external_lex_state;
}
int get_sizeof_TSLexMode() {
	return sizeof(TSLexMode);
}
int get_sizeof_TSLexerMode() {
	return sizeof(TSLexerMode);
}
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

func GetExternalLexState3852() int {
	return int(C.get_external_lex_state_3852())
}

func GetSizeofTSLexMode() int {
	return int(C.get_sizeof_TSLexMode())
}

func GetSizeofTSLexerMode() int {
	return int(C.get_sizeof_TSLexerMode())
}
