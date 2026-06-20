//go:build cgo

package parser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"strings"

	lang_javascript "github.com/hackafterdark/phosphor/internal/agent/parser/javascript"
	lang_typescript "github.com/hackafterdark/phosphor/internal/agent/parser/typescript"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

func TestRegistryDefaults(t *testing.T) {
	r := NewQueryRegistry()
	q, ok := r.GetTemplate("go", "find_functions")
	if !ok {
		t.Fatal("expected find_functions query to be present by default")
	}
	if q == "" {
		t.Fatal("expected non-empty default query")
	}
}

func TestRegistryOverrides(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "phosphor-query-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	queriesDir := filepath.Join(tempDir, ".phosphor", "queries")
	if err := os.MkdirAll(queriesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Write single query overriding default.
	overrideYaml := `
id: find_functions
description: "Custom go function finder"
language: go
query: "(function_declaration) @func"
guidance: "Custom guidance"
`
	if err := os.WriteFile(filepath.Join(queriesDir, "override.yaml"), []byte(overrideYaml), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Write list of queries containing a new one.
	newListYaml := `
- id: find_custom_thing
  description: "Custom search"
  language: go
  query: "(type_declaration) @type"
- id: find_comments
  description: "Custom comments"
  language: go
  query: "(comment) @comment"
`
	if err := os.WriteFile(filepath.Join(queriesDir, "custom.yaml"), []byte(newListYaml), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewQueryRegistry()
	if err := r.Reload(tempDir); err != nil {
		t.Fatal(err)
	}

	// Verify override.
	q, ok := r.GetTemplate("go", "find_functions")
	if !ok {
		t.Fatal("expected find_functions to be present")
	}
	if q != "(function_declaration) @func" {
		t.Errorf("expected overridden query, got %q", q)
	}

	cap, ok := r.GetCapability("go", "find_functions")
	if !ok {
		t.Fatal("expected capability to exist")
	}
	if cap.Description != "Custom go function finder" {
		t.Errorf("expected overridden description, got %q", cap.Description)
	}
	if cap.Guidance != "Custom guidance" {
		t.Errorf("expected overridden guidance, got %q", cap.Guidance)
	}

	// Verify new query from list.
	qCustom, ok := r.GetTemplate("go", "find_custom_thing")
	if !ok {
		t.Fatal("expected new query to be loaded")
	}
	if qCustom != "(type_declaration) @type" {
		t.Errorf("expected new query, got %q", qCustom)
	}

	// Verify list query override.
	qComment, ok := r.GetTemplate("go", "find_comments")
	if !ok {
		t.Fatal("expected find_comments to exist")
	}
	if qComment != "(comment) @comment" {
		t.Errorf("expected overridden comments, got %q", qComment)
	}
}

func formatNode(node *sitter.Node, code []byte, indent string) string {
	res := ""
	if node.IsNamed() {
		res += fmt.Sprintf("%s%s [%d-%d] -> %q\n", indent, node.Kind(), node.StartByte(), node.EndByte(), node.Utf8Text(code))
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		res += formatNode(node.Child(i), code, indent+"  ")
	}
	return res
}

func hasErrorNode(node *sitter.Node) bool {
	if node.Kind() == "ERROR" {
		return true
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		if hasErrorNode(node.Child(i)) {
			return true
		}
	}
	return false
}

func TestFindTSFunctions(t *testing.T) {
	parser := sitter.NewParser()
	lang := lang_typescript.GetLanguage()
	t.Logf("TS ABI Version: %d", lang.AbiVersion())
	parser.SetLanguage(lang)

	code := []byte("const x = `hello`;")
	tree := parser.ParseCtx(context.Background(), code, nil)
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
	defer tree.Close()

	if hasErrorNode(tree.RootNode()) {
		ast := formatNode(tree.RootNode(), code, "")
		t.Errorf("AST contains ERROR node:\n%s", ast)
	}
}

func TestFindGoImportsAndInterfaces(t *testing.T) {
	parser := sitter.NewParser()
	lang := GetLanguage("go")
	parser.SetLanguage(lang)

	// 1. Test imports
	codeImports := []byte(`package main
import (
	"fmt"
	l "log"
)
`)
	treeImports := parser.ParseCtx(context.Background(), codeImports, nil)
	if treeImports == nil {
		t.Fatal("expected non-nil tree")
	}
	defer treeImports.Close()

	importMatches, err := Query(treeImports.RootNode(), codeImports, "go", "find_imports")
	if err != nil {
		t.Fatalf("find_imports failed: %v", err)
	}
	if len(importMatches) != 2 {
		t.Errorf("expected 2 import matches, got %d", len(importMatches))
	}

	// 2. Test interfaces
	codeInterfaces := []byte(`package main
type Reader interface {
	Read(p []byte) (n int, err error)
	Close() error
}
`)
	treeInterfaces := parser.ParseCtx(context.Background(), codeInterfaces, nil)
	if treeInterfaces == nil {
		t.Fatal("expected non-nil tree")
	}
	defer treeInterfaces.Close()

	interfaceMatches, err := Query(treeInterfaces.RootNode(), codeInterfaces, "go", "find_interfaces")
	if err != nil {
		t.Fatalf("find_interfaces failed: %v", err)
	}
	if len(interfaceMatches) != 3 {
		t.Errorf("expected 3 interface matches, got %d", len(interfaceMatches))
	}
}

func TestFindRustImportsAndInterfaces(t *testing.T) {
	parser := sitter.NewParser()
	lang := GetLanguage("rust")
	parser.SetLanguage(lang)

	// Test imports
	codeImports := []byte(`use std::io; use std::collections::HashMap;`)
	treeImports := parser.ParseCtx(context.Background(), codeImports, nil)
	if treeImports == nil {
		t.Fatal("expected non-nil tree")
	}
	defer treeImports.Close()

	importMatches, err := Query(treeImports.RootNode(), codeImports, "rust", "find_imports")
	if err != nil {
		t.Fatalf("find_imports failed: %v", err)
	}
	if len(importMatches) != 2 {
		t.Errorf("expected 2 import matches, got %d", len(importMatches))
	}

	// Test interfaces (traits)
	codeInterfaces := []byte(`trait Reader { fn read(&mut self) -> Result<usize>; }`)
	treeInterfaces := parser.ParseCtx(context.Background(), codeInterfaces, nil)
	if treeInterfaces == nil {
		t.Fatal("expected non-nil tree")
	}
	defer treeInterfaces.Close()

	interfaceMatches, err := Query(treeInterfaces.RootNode(), codeInterfaces, "rust", "find_interfaces")
	if err != nil {
		t.Fatalf("find_interfaces failed: %v", err)
	}
	if len(interfaceMatches) != 1 {
		t.Errorf("expected 1 interface match, got %d", len(interfaceMatches))
	}
}

func TestFindPHPImportsAndInterfaces(t *testing.T) {
	parser := sitter.NewParser()
	lang := GetLanguage("php")
	parser.SetLanguage(lang)

	// Test imports
	codeImports := []byte(`<?php
use App\Models\User;
use App\Models\Post as MyPost;
`)
	treeImports := parser.ParseCtx(context.Background(), codeImports, nil)
	if treeImports == nil {
		t.Fatal("expected non-nil tree")
	}
	defer treeImports.Close()

	importMatches, err := Query(treeImports.RootNode(), codeImports, "php", "find_imports")
	if err != nil {
		t.Fatalf("find_imports failed: %v", err)
	}
	if len(importMatches) != 2 {
		t.Errorf("expected 2 import matches, got %d", len(importMatches))
	}
}

func TestFindJSFunctions(t *testing.T) {
	parser := sitter.NewParser()
	lang := lang_javascript.GetLanguage()
	parser.SetLanguage(lang)

	code, err := os.ReadFile("../../../examples/structural_search/example.js")
	if err != nil {
		t.Fatal(err)
	}
	tree := parser.ParseCtx(context.Background(), code, nil)
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
	defer tree.Close()

	if hasErrorNode(tree.RootNode()) {
		ast := formatNode(tree.RootNode(), code, "")
		t.Errorf("AST contains ERROR node:\n%s", ast)
	}

	astStr := formatNode(tree.RootNode(), code, "")
	os.WriteFile("debug_js_ast.txt", []byte(astStr), 0o644)

	// Verify structural_search query 'find_structs' execution.
	structMatches, err := Query(tree.RootNode(), code, "javascript", "find_structs")
	if err != nil {
		t.Fatalf("find_structs query failed: %v", err)
	}
	if len(structMatches) != 6 {
		t.Errorf("expected 6 matches for find_structs (representing class methods), got %d", len(structMatches))
	}

	// Verify structural_search query 'find_functions' execution.
	funcMatches, err := Query(tree.RootNode(), code, "javascript", "find_functions")
	if err != nil {
		t.Fatalf("find_functions query failed: %v", err)
	}
	if len(funcMatches) != 4 {
		t.Errorf("expected 4 matches for find_functions (representing standalone functions), got %d", len(funcMatches))
	}

	// Verify custom async query matches correctly.
	asyncPattern := `
(function_declaration
  "async"
  name: (identifier) @function_name)
`
	asyncMatches, err := Query(tree.RootNode(), code, "javascript", asyncPattern)
	if err != nil {
		t.Fatalf("async query failed: %v", err)
	}

	var matchedNames []string
	for _, m := range asyncMatches {
		for _, cap := range m.Captures {
			if cap.Capture == "function_name" {
				matchedNames = append(matchedNames, cap.Text)
			}
		}
	}

	if len(matchedNames) != 2 {
		t.Errorf("expected 2 async function matches, got %d: %v", len(matchedNames), matchedNames)
	}
	if len(matchedNames) >= 2 {
		if matchedNames[0] != "fetchPersons" || matchedNames[1] != "loadPersons" {
			t.Errorf("expected fetchPersons and loadPersons, got %v", matchedNames)
		}
	}
}

func TestRustCustomQueries(t *testing.T) {
	parser := sitter.NewParser()
	lang := GetLanguage("rust")
	parser.SetLanguage(lang)

	code := []byte(`
struct Point {
    x: i32,
    y: i32,
}

fn check() -> Result<(), String> {
    if true {
        return Err("error message".to_string());
    }
    Ok(())
}
`)

	tree := parser.ParseCtx(context.Background(), code, nil)
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
	defer tree.Close()

	// 1. Test find_struct_fields query
	fieldsQuery := `
(field_declaration
  name: (field_identifier) @field_name
  type: (_) @field_type)
`
	fieldMatches, err := Query(tree.RootNode(), code, "rust", fieldsQuery)
	if err != nil {
		t.Fatalf("find_struct_fields query failed: %v", err)
	}
	if len(fieldMatches) != 2 {
		t.Errorf("expected 2 field matches, got %d", len(fieldMatches))
	}

	// 2. Test find_error_returns query
	errQuery := `
(call_expression
  function: (identifier) @func_name (#eq? @func_name "Err")
  arguments: (arguments (_) @err_value))
`
	errMatches, err := Query(tree.RootNode(), code, "rust", errQuery)
	if err != nil {
		t.Fatalf("find_error_returns query failed: %v", err)
	}
	if len(errMatches) != 1 {
		t.Errorf("expected 1 error return match, got %d", len(errMatches))
	}
}

func TestFindHTMLTemplates(t *testing.T) {
	parser := sitter.NewParser()
	lang := GetLanguage("html")
	parser.SetLanguage(lang)

	// Since we are running in the package directory, example.html is in ../../../examples/structural_search/example.html
	path := filepath.Join("..", "..", "..", "examples", "structural_search", "example.html")
	code, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read example.html: %v", err)
	}

	tree := parser.ParseCtx(context.Background(), code, nil)
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
	defer tree.Close()

	var lines []string
	var printNode func(*sitter.Node, int, string)
	printNode = func(n *sitter.Node, depth int, indent string) {
		if depth > 4 || len(lines) > 100 {
			return
		}
		lines = append(lines, fmt.Sprintf("%s%s (%d-%d): %q", indent, n.Kind(), n.StartByte(), n.EndByte(), n.Utf8Text(code)))
		for i := uint(0); i < n.ChildCount(); i++ {
			printNode(n.Child(i), depth+1, indent+"  ")
		}
	}
	printNode(tree.RootNode(), 0, "")
	t.Logf("HTML AST DUMP:\n%s", strings.Join(lines, "\n"))

	// 1. Test find_structs (should capture element / self_closing_tag)
	structsQuery := Templates["html"]["find_structs"]
	structMatches, err := Query(tree.RootNode(), code, "html", structsQuery)
	if err != nil {
		t.Fatalf("find_structs query failed: %v", err)
	}
	if len(structMatches) == 0 {
		t.Errorf("expected at least some structs/elements, got 0")
	}

	// 2. Test find_variables (should capture attributes)
	varsQuery := Templates["html"]["find_variables"]
	varMatches, err := Query(tree.RootNode(), code, "html", varsQuery)
	if err != nil {
		t.Fatalf("find_variables query failed: %v", err)
	}
	if len(varMatches) == 0 {
		t.Errorf("expected at least some attributes/variables, got 0")
	}

	// 3. Test find_imports (should capture links / scripts)
	importsQuery := Templates["html"]["find_imports"]
	importMatches, err := Query(tree.RootNode(), code, "html", importsQuery)
	if err != nil {
		t.Fatalf("find_imports query failed: %v", err)
	}
	if len(importMatches) == 0 {
		t.Errorf("expected at least some imports/links/scripts, got 0")
	}

	// 4. Test find_comments (should capture comments)
	commentsQuery := Templates["html"]["find_comments"]
	commentMatches, err := Query(tree.RootNode(), code, "html", commentsQuery)
	if err != nil {
		t.Fatalf("find_comments query failed: %v", err)
	}
	if len(commentMatches) == 0 {
		t.Errorf("expected at least some comments, got 0")
	}
}

func TestFindSQLTemplates(t *testing.T) {
	parser := sitter.NewParser()
	lang := GetLanguage("sql")
	parser.SetLanguage(lang)

	path := filepath.Join("..", "..", "..", "examples", "structural_search", "example.sql")
	code, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read example.sql: %v", err)
	}

	tree := parser.ParseCtx(context.Background(), code, nil)
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
	defer tree.Close()
	if hasErrorNode(tree.RootNode()) {
		ast := formatNode(tree.RootNode(), code, "")
		t.Logf("SQL AST contains ERROR node:\n%s", ast)
	}

	// 1. Test find_structs (should capture create_table)
	structsQuery := Templates["sql"]["find_structs"]
	structMatches, err := Query(tree.RootNode(), code, "sql", structsQuery)
	if err != nil {
		t.Fatalf("find_structs query failed: %v", err)
	}
	if len(structMatches) == 0 {
		t.Errorf("expected at least some structs/create_table, got 0")
	}

	// 2. Test find_select_tables
	selectQuery := Templates["sql"]["find_select_tables"]
	selectMatches, err := Query(tree.RootNode(), code, "sql", selectQuery)
	if err != nil {
		t.Fatalf("find_select_tables query failed: %v", err)
	}
	if len(selectMatches) == 0 {
		t.Errorf("expected at least some select tables, got 0")
	}

	// 3. Test find_comments
	commentsQuery := Templates["sql"]["find_comments"]
	commentMatches, err := Query(tree.RootNode(), code, "sql", commentsQuery)
	if err != nil {
		t.Fatalf("find_comments query failed: %v", err)
	}
	if len(commentMatches) == 0 {
		t.Errorf("expected at least some comments, got 0")
	}
}

func TestAllQueriesCompile(t *testing.T) {
	for langName, templates := range Templates {
		t.Run(langName, func(t *testing.T) {
			if !isVendoredLanguage(langName) {
				t.Skipf("Language %s is not natively supported/vendored in this repository", langName)
			}
			lang := GetLanguage(langName)
			if lang == nil {
				t.Skipf("Language %s not supported/loaded", langName)
			}
			for name, pattern := range templates {
				if pattern == "" {
					continue
				}
				t.Run(name, func(t *testing.T) {
					q, err := sitter.NewQuery(lang, pattern)
					if err != nil {
						t.Fatalf("Failed to compile query %s for %s: %v\nPattern:\n%s", name, langName, err, pattern)
					}
					q.Close()
				})
			}
		})
	}
}

func isVendoredLanguage(langName string) bool {
	switch langName {
	case "go", "cpp", "c", "bash", "hcl", "csharp", "typescript", "javascript", "python", "php", "rust", "json", "html", "css", "scala", "sql":
		return true
	default:
		return false
	}
}
