package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

type ViewNodeParams struct {
	FilePath string `json:"file_path" description:"The path to the file to read"`
	NodeName string `json:"node_name" description:"The name of the function, struct, or class to view"`
}

const ViewNodeToolName = "view_node"

var viewNodeDescription = "View the implementation/definition of a specific function, struct, class, or interface in a file by name. Uses Tree-sitter AST parsing if available, falling back to smart indentation/brace matching if Tree-sitter is unavailable."

func NewViewNodeTool(workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ViewNodeToolName,
		viewNodeDescription,
		func(ctx context.Context, params ViewNodeParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool view_node")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", ViewNodeToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}
			if params.NodeName == "" {
				return fantasy.NewTextErrorResponse("node_name is required"), nil
			}

			filePath := filepathext.SmartJoin(workingDir, params.FilePath)

			// Enforce workspace bounds
			absWorkingDir, err := filepath.Abs(workingDir)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving working directory: %w", err)
			}
			absFilePath, err := filepath.Abs(filePath)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving file path: %w", err)
			}
			if !filepathext.IsInside(absFilePath, absWorkingDir) {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Security violation: path %s is outside workspace", absFilePath)), nil
			}
			filePath = absFilePath

			fileBytes, err := os.ReadFile(filePath)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Error reading file: %v", err)), nil
			}
			if !utf8.Valid(fileBytes) {
				return fantasy.NewTextErrorResponse("File content is not valid UTF-8"), nil
			}

			var nodeText string
			var startLine int
			var searchErr error

			if isSitterAvailable() {
				nodeText, startLine, searchErr = findNodeSitter(fileBytes, params.NodeName, filePath)
			} else {
				nodeText, startLine, searchErr = findNodeFallback(fileBytes, params.NodeName, filePath)
				if searchErr == nil {
					nodeText = "[Tree-sitter unavailable, showing regex-based fallback match]\n" + nodeText
				}
			}

			if searchErr != nil {
				return fantasy.NewTextErrorResponse(searchErr.Error()), nil
			}

			// Prepend line numbers to the matched node
			lines := strings.Split(nodeText, "\n")
			var numbered []string
			for i, line := range lines {
				if i == 0 && strings.HasPrefix(line, "[Tree-sitter") {
					numbered = append(numbered, line)
					continue
				}
				// Format with line number
				numbered = append(numbered, fmt.Sprintf("%d: %s", startLine+i, line))
			}
			output := strings.Join(numbered, "\n")

			return fantasy.NewTextResponse(output), nil
		},
	)
}

func findNodeFallback(code []byte, nodeName string, filePath string) (string, int, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	lines := strings.Split(string(code), "\n")
	startLine := -1
	var startIndent string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isMatch := false
		if strings.Contains(trimmed, nodeName) {
			if ext == ".go" {
				if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "type ") {
					isMatch = true
				}
			} else if ext == ".py" {
				if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "class ") {
					isMatch = true
				}
			} else if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
				if strings.Contains(trimmed, "function ") || strings.Contains(trimmed, "class ") || strings.Contains(trimmed, "interface ") || strings.Contains(trimmed, "type ") {
					isMatch = true
				}
			} else {
				if strings.Contains(trimmed, "func ") || strings.Contains(trimmed, "def ") || strings.Contains(trimmed, "class ") || strings.Contains(trimmed, "struct ") {
					isMatch = true
				}
			}
		}

		if isMatch {
			startLine = i
			for _, r := range line {
				if r == ' ' || r == '\t' {
					startIndent += string(r)
				} else {
					break
				}
			}
			break
		}
	}

	if startLine == -1 {
		return "", 0, fmt.Errorf("node %q not found in file (fallback mode)", nodeName)
	}

	var blockLines []string
	if ext == ".py" {
		blockLines = append(blockLines, lines[startLine])
		for i := startLine + 1; i < len(lines); i++ {
			line := lines[i]
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				blockLines = append(blockLines, line)
				continue
			}
			var indent string
			for _, r := range line {
				if r == ' ' || r == '\t' {
					indent += string(r)
				} else {
					break
				}
			}
			if len(indent) <= len(startIndent) {
				break
			}
			blockLines = append(blockLines, line)
		}
	} else {
		braceCount := 0
		hasSeenBrace := false
		for i := startLine; i < len(lines); i++ {
			line := lines[i]
			blockLines = append(blockLines, line)
			for _, r := range line {
				if r == '{' {
					braceCount++
					hasSeenBrace = true
				} else if r == '}' {
					braceCount--
				}
			}
			if hasSeenBrace && braceCount <= 0 {
				break
			}
			if len(blockLines) > 200 {
				break
			}
		}
	}

	return strings.Join(blockLines, "\n"), startLine + 1, nil
}
