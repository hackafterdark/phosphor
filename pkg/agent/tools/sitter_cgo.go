//go:build cgo

package tools

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hackafterdark/phosphor/pkg/agent/parser"
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
	// .json is deliberately absent: the JSON grammar rejects comments and
	// other valid JSONC dialects (deno.json, tsconfig.json), so strict AST
	// validation produced false rejections on legitimate edits. JSON stays
	// validated by real parsers downstream, not by this gate.
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

// verifySyntax rejects an edit only when it adds tree-sitter parse errors
// relative to the pre-edit content. Pre-existing ERROR/MISSING nodes from
// grammar gaps (e.g. C++20 corner cases the grammars cannot express) must not
// block otherwise valid edits.
//
// Tradeoff: a net-zero swap — fixing one parse error while introducing
// another — passes under count-diffing; it passes silently, with no
// message surfaced for these edits.
func verifySyntax(oldContent, newContent, filePath string) error {
	lang := detectLanguageStrict(filePath)
	if lang == "" {
		return nil
	}
	postErrors := collectASTErrors(newContent, lang)
	if len(postErrors) == 0 {
		return nil
	}
	preErrors := 0
	if oldContent != "" {
		preErrors = len(collectASTErrors(oldContent, lang))
	}
	if len(postErrors) <= preErrors {
		return nil
	}
	locations := formatASTErrorLocations(postErrors)
	if oldContent == "" {
		return fmt.Errorf("syntax error: AST contains %d error(s); locations: %s", len(postErrors), locations)
	}
	return fmt.Errorf("syntax error: edit introduced %d new (%d pre-existing) AST error(s); post-edit locations: %s", len(postErrors)-preErrors, preErrors, locations)
}

// astError is the 1-based line:col position of a tree-sitter parse failure.
type astError struct {
	line int
	col  int
}

// collectASTErrors returns the positions of every top-level parse failure in
// code. Recursion stops at an ERROR node: tree-sitter frequently nests
// secondary ERROR or MISSING nodes inside a parent ERROR subtree, and skipping
// those children keeps one broken expression from inflating the error count.
func collectASTErrors(code, lang string) []astError {
	root := parser.Parse([]byte(code), lang)
	if root == nil {
		return nil
	}
	var errs []astError
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.IsError() || n.IsMissing() || n.Kind() == "ERROR" {
			start := n.StartPosition()
			errs = append(errs, astError{line: int(start.Row) + 1, col: int(start.Column) + 1})
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			visit(n.Child(i))
		}
	}
	visit(root)
	return errs
}

// formatASTErrorLocations renders at most maxReportedASTErrors locations so a
// heavily broken file cannot bloat the tool output.
func formatASTErrorLocations(errs []astError) string {
	const maxReportedASTErrors = 10
	shown := errs
	if len(shown) > maxReportedASTErrors {
		shown = shown[:maxReportedASTErrors]
	}
	parts := make([]string, 0, len(shown))
	for _, e := range shown {
		parts = append(parts, fmt.Sprintf("%d:%d", e.line, e.col))
	}
	out := strings.Join(parts, ", ")
	if extra := len(errs) - maxReportedASTErrors; extra > 0 {
		out += fmt.Sprintf(" (+%d more)", extra)
	}
	return out
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
