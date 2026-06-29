//go:build cgo

package tools

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hackafterdark/phosphor/internal/agent/parser"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

func isSitterAvailable() bool {
	return true
}

func detectLanguageStrict(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".cpp", ".cc", ".cxx", ".hpp", ".hxx":
		return "cpp"
	case ".c", ".h":
		return "c"
	case ".sh":
		return "bash"
	case ".hcl", ".tf":
		return "hcl"
	case ".json":
		return "json"
	case ".html":
		return "html"
	case ".css":
		return "css"
	case ".toml":
		return "toml"
	case ".scala", ".sbt":
		return "scala"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".php":
		return "php"
	case ".sql":
		return "sql"
	case ".rs":
		return "rust"
	case ".cs":
		return "csharp"
	default:
		return ""
	}
}

func verifySyntax(newString string, filePath string) error {
	lang := detectLanguageStrict(filePath)
	if lang == "" {
		return nil
	}
	root := parser.Parse([]byte(newString), lang)
	if root == nil {
		return nil
	}
	if hasASTError(root) {
		return fmt.Errorf("syntax error: AST contains ERROR or missing node")
	}
	return nil
}

func hasASTError(n *sitter.Node) bool {
	if n == nil {
		return false
	}
	if n.IsError() || n.IsMissing() || n.Kind() == "ERROR" {
		return true
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if hasASTError(n.Child(i)) {
			return true
		}
	}
	return false
}

func generateOutline(code []byte, filePath string) (string, error) {
	lang := detectLanguageStrict(filePath)
	if lang == "" {
		return "", fmt.Errorf("unsupported language for outline")
	}
	root := parser.Parse(code, lang)
	if root == nil {
		return "", fmt.Errorf("failed to parse file")
	}

	var outline []string
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		if n == nil {
			return
		}
		kind := n.Kind()
		isSignature := false
		name := ""

		switch lang {
		case "go":
			if kind == "function_declaration" || kind == "method_declaration" {
				isSignature = true
				for i := uint(0); i < n.ChildCount(); i++ {
					child := n.Child(i)
					if child.Kind() == "identifier" || child.Kind() == "field_identifier" {
						name = child.Utf8Text(code)
						break
					}
				}
				if name != "" && len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
					isSignature = true
				} else {
					isSignature = false
				}
			} else if kind == "type_spec" {
				for i := uint(0); i < n.ChildCount(); i++ {
					child := n.Child(i)
					if child.Kind() == "type_identifier" {
						name = child.Utf8Text(code)
						break
					}
				}
				if name != "" && len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
					isSignature = true
				}
			}
		case "python":
			if kind == "function_definition" || kind == "class_definition" {
				for i := uint(0); i < n.ChildCount(); i++ {
					child := n.Child(i)
					if child.Kind() == "identifier" {
						name = child.Utf8Text(code)
						break
					}
				}
				if name != "" && !strings.HasPrefix(name, "_") {
					isSignature = true
				}
			}
		case "typescript", "javascript":
			if kind == "function_declaration" || kind == "class_declaration" || kind == "interface_declaration" || kind == "type_alias_declaration" {
				isExported := false
				parent := n.Parent()
				if parent != nil && parent.Kind() == "export_statement" {
					isExported = true
				}
				if isExported {
					isSignature = true
				}
			}
		default:
			if strings.Contains(kind, "function") || strings.Contains(kind, "method") || strings.Contains(kind, "class") || strings.Contains(kind, "struct") {
				isSignature = true
			}
		}

		if isSignature {
			text := n.Utf8Text(code)
			firstLine := text
			if idx := strings.Index(text, "\n"); idx != -1 {
				firstLine = text[:idx]
			}
			firstLine = strings.TrimRight(firstLine, " {:")
			outline = append(outline, fmt.Sprintf("Line %d: %s", n.StartPosition().Row+1, firstLine))
			return
		}

		for i := uint(0); i < n.ChildCount(); i++ {
			visit(n.Child(i))
		}
	}

	visit(root)
	if len(outline) == 0 {
		return "No public signatures found.", nil
	}
	return strings.Join(outline, "\n"), nil
}

func findNodeSitter(code []byte, nodeName string, filePath string) (string, int, error) {
	lang := detectLanguageStrict(filePath)
	if lang == "" {
		return "", 0, fmt.Errorf("unsupported language for node search")
	}
	root := parser.Parse(code, lang)
	if root == nil {
		return "", 0, fmt.Errorf("failed to parse file")
	}

	var targetNode *sitter.Node
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		if n == nil || targetNode != nil {
			return
		}
		kind := n.Kind()
		isCandidate := false
		name := ""

		switch lang {
		case "go":
			if kind == "function_declaration" || kind == "method_declaration" {
				isCandidate = true
				for i := uint(0); i < n.ChildCount(); i++ {
					child := n.Child(i)
					if child.Kind() == "identifier" || child.Kind() == "field_identifier" {
						name = child.Utf8Text(code)
						break
					}
				}
			} else if kind == "type_spec" {
				isCandidate = true
				for i := uint(0); i < n.ChildCount(); i++ {
					child := n.Child(i)
					if child.Kind() == "type_identifier" {
						name = child.Utf8Text(code)
						break
					}
				}
			}
		case "python":
			if kind == "function_definition" || kind == "class_definition" {
				isCandidate = true
				for i := uint(0); i < n.ChildCount(); i++ {
					child := n.Child(i)
					if child.Kind() == "identifier" {
						name = child.Utf8Text(code)
						break
					}
				}
			}
		case "typescript", "javascript":
			if kind == "function_declaration" || kind == "class_declaration" || kind == "interface_declaration" || kind == "type_alias_declaration" || kind == "method_definition" {
				isCandidate = true
				for i := uint(0); i < n.ChildCount(); i++ {
					child := n.Child(i)
					if child.Kind() == "identifier" || child.Kind() == "property_identifier" {
						name = child.Utf8Text(code)
						break
					}
				}
			}
		default:
			if strings.Contains(kind, "function") || strings.Contains(kind, "method") || strings.Contains(kind, "class") || strings.Contains(kind, "struct") {
				isCandidate = true
				for i := uint(0); i < n.ChildCount(); i++ {
					child := n.Child(i)
					if child.Kind() == "identifier" {
						name = child.Utf8Text(code)
						break
					}
				}
			}
		}

		if isCandidate && name == nodeName {
			targetNode = n
			return
		}

		for i := uint(0); i < n.ChildCount(); i++ {
			visit(n.Child(i))
		}
	}

	visit(root)
	if targetNode == nil {
		return "", 0, fmt.Errorf("node %q not found in AST", nodeName)
	}

	return targetNode.Utf8Text(code), int(targetNode.StartPosition().Row) + 1, nil
}
