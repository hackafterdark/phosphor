//go:build cgo
// +build cgo

// Package workspaceindex provides symbol extraction from source files.
package workspaceindex

import (
	"context"
	"fmt"
	"strings"

	"github.com/hackafterdark/phosphor/pkg/agent/parser"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// indexCodeSymbols extracts code symbols from a source file and inserts
// them into the workspace index store.
func (i *Indexer) indexCodeSymbols(ctx context.Context, relPath string, data []byte) error {
	lang := parser.DetectLanguage(relPath)
	if lang == "" {
		return nil
	}

	root := parser.Parse(data, lang)
	if root == nil {
		return nil
	}

	symbols := extractSymbols(root, data)

	for _, sym := range symbols {
		if err := i.store.InsertSymbol(ctx, relPath, sym.Name, sym.QualifiedName, sym.Signature, sym.Documentation); err != nil {
			return fmt.Errorf("insert symbol %s: %w", sym.Name, err)
		}
	}

	return nil
}

type symbolInfo struct {
	Name          string
	QualifiedName string
	Signature     string
	Documentation string
}

func extractSymbols(root *sitter.Node, source []byte) []symbolInfo {
	var symbols []symbolInfo
	cursor := root.Walk()
	defer cursor.Close()
	for _, child := range root.Children(cursor) {
		switch child.Kind() {
		case "function_declaration", "method_declaration":
			sym := extractFunctionSymbol(&child, source)
			if sym.Name != "" {
				symbols = append(symbols, sym)
			}
		case "type_declaration", "struct_type", "interface_declaration":
			sym := extractTypeSymbol(&child, source)
			if sym.Name != "" {
				symbols = append(symbols, sym)
			}
		}
	}
	return symbols
}

func extractFunctionSymbol(node *sitter.Node, source []byte) symbolInfo {
	nameNode := node.ChildByFieldName("name")
	name := ""
	if nameNode != nil {
		name = normalizeText(nameNode.Utf8Text(source))
	}

	qualifiedName := name
	container := findContainer(node)
	if container != nil {
		containerName := ""
		idNode := findIdentifier(container)
		if idNode != nil {
			containerName = normalizeText(idNode.Utf8Text(source))
		}
		if containerName != "" {
			qualifiedName = containerName + "." + name
		}
	}

	bodyNode := node.ChildByFieldName("body")
	signature := ""
	if bodyNode != nil {
		signature = firstNLines(string(source[node.StartByte():bodyNode.StartByte()]), 4)
	} else {
		signature = firstNLines(node.Utf8Text(source), 4)
	}

	doc := extractDoc(node, source)

	return symbolInfo{
		Name:          name,
		QualifiedName: qualifiedName,
		Signature:     signature,
		Documentation: doc,
	}
}

func extractTypeSymbol(node *sitter.Node, source []byte) symbolInfo {
	idNode := findIdentifier(node)
	name := ""
	if idNode != nil {
		name = normalizeText(idNode.Utf8Text(source))
	}
	return symbolInfo{
		Name:          name,
		QualifiedName: name,
		Signature:     firstNLines(node.Utf8Text(source), 4),
		Documentation: extractDoc(node, source),
	}
}

func findContainer(node *sitter.Node) *sitter.Node {
	parent := node.Parent()
	for p := parent; p != nil; p = p.Parent() {
		if p.Kind() == "type_declaration" || p.Kind() == "struct_type" {
			pCopy := *p
			return &pCopy
		}
	}
	return nil
}

func findIdentifier(node *sitter.Node) *sitter.Node {
	cursor := node.Walk()
	defer cursor.Close()
	for _, child := range node.Children(cursor) {
		if child.Kind() == "type_identifier" || child.Kind() == "identifier" {
			childCopy := child
			return &childCopy
		}
	}
	return nil
}

func extractDoc(node *sitter.Node, source []byte) string {
	parent := node.Parent()
	if parent == nil {
		return ""
	}
	cursor := parent.Walk()
	defer cursor.Close()
	children := parent.Children(cursor)
	for i, child := range children {
		if child == *node {
			if i > 0 {
				prev := children[i-1]
				if prev.Kind() == "line_comment" || prev.Kind() == "block_comment" {
					return cleanCommentText(prev.Utf8Text(source))
				}
			}
			break
		}
	}
	return ""
}

func cleanCommentText(text string) string {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"//", "/*", "*/", "* ", "*"} {
		text = strings.TrimPrefix(text, prefix)
	}
	return strings.TrimSpace(text)
}

func normalizeText(text string) string {
	return strings.TrimSpace(text)
}

func firstNLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
