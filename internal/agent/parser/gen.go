//go:build ignore

// gen.go generates bridge_<lang>.go files for each language sub-package.
// Run with: go run gen.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var langs = []struct {
	name       string
	goPackage  string
	funcName   string
	hasScanner bool
}{
	{"go", "lang_go", "tree_sitter_go", false},
	{"typescript", "lang_typescript", "tree_sitter_typescript", true},
	{"javascript", "lang_javascript", "tree_sitter_javascript", true},
	{"python", "lang_python", "tree_sitter_python", true},
	{"rust", "lang_rust", "tree_sitter_rust", true},
	// {"java", "lang_java", "tree_sitter_java", false} — Java not supported (requires external scanner)
	{"csharp", "lang_csharp", "tree_sitter_c_sharp", true},
	{"php", "lang_php", "tree_sitter_php", true},
	{"cpp", "lang_cpp", "tree_sitter_cpp", true},
	{"c", "lang_c", "tree_sitter_c", false},
	{"bash", "lang_bash", "tree_sitter_bash", true},
	{"hcl", "lang_hcl", "tree_sitter_hcl", true},
	{"ruby", "lang_ruby", "tree_sitter_ruby", true},
	{"json", "lang_json", "tree_sitter_json", false},
	{"html", "lang_html", "tree_sitter_html", true},
	{"css", "lang_css", "tree_sitter_css", true},
	{"toml", "lang_toml", "tree_sitter_toml", true},
	{"scala", "lang_scala", "tree_sitter_scala", true},
}

func main() {
	base := "F:/hackafterdark/phosphor/internal/agent/parser/"
	for _, lang := range langs {
		dir := filepath.Join(base, lang.name)
		os.MkdirAll(dir, 0o755)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("// Package %s provides tree-sitter bindings for %s.\n", lang.goPackage, lang.name))
		sb.WriteString(fmt.Sprintf("package %s\n\n", lang.goPackage))
		sb.WriteString("/*\n")
		sb.WriteString("#cgo CFLAGS: -IF:/hackafterdark/phosphor/grammars/include -IF:/hackafterdark/phosphor/grammars\n\n")
		sb.WriteString("#include \"tree_sitter/parser.h\"\n\n")
		sb.WriteString(fmt.Sprintf("#include \"%s/src/parser.c\"\n", lang.name))
		if lang.hasScanner {
			sb.WriteString(fmt.Sprintf("#include \"%s/src/scanner.c\"\n", lang.name))
		}
		sb.WriteString("*/\n")
		sb.WriteString("import \"C\"\n\n")
		sb.WriteString("import (\n")
		sb.WriteString("\t\"unsafe\"\n\n")
		sb.WriteString("\tsitter \"github.com/tree-sitter/go-tree-sitter\"\n")
		sb.WriteString(")\n\n")
		sb.WriteString("// GetLanguage returns the tree-sitter language for " + lang.name + ".\n")
		sb.WriteString("func GetLanguage() *sitter.Language {\n")
		sb.WriteString("\treturn sitter.NewLanguage(unsafe.Pointer(C." + lang.funcName + "()))\n")
		sb.WriteString("}\n")

		err := os.WriteFile(filepath.Join(dir, "bridge.go"), []byte(sb.String()), 0o644)
		if err != nil {
			panic(err)
		}
		fmt.Printf("Generated %s/bridge.go\n", lang.name)
	}
}
