package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func TestVerifySyntax(t *testing.T) {
	// Valid Go
	err := verifySyntax("", `package main
func main() {}`, "main.go")
	require.NoError(t, err)

	// Invalid Go - should fail if sitter is available
	err = verifySyntax("", `package main
func main(`, "main.go")
	if isSitterAvailable() {
		require.Error(t, err)
	} else {
		require.NoError(t, err)
	}

	// Invalid syntax in txt file should always succeed
	err = verifySyntax("", `package main
func main(`, "main.txt")
	require.NoError(t, err)
}

func TestVerifySyntaxDifferential_PassPreExistingError(t *testing.T) {
	if !isSitterAvailable() {
		t.Skip("tree-sitter requires cgo")
	}
	// A file that already fails to parse (grammar gap) must accept a valid
	// edit that adds no new errors.
	pre := "template <typename T> struct S { T x; };\ntemplate struct S<float>;"
	post := "template <typename T> struct S { T x; };\ntemplate struct S<float>;\n// comment"
	require.NoError(t, verifySyntax(pre, post, "solver.cpp"))
}

func TestVerifySyntaxDifferential_RejectNewError(t *testing.T) {
	if !isSitterAvailable() {
		t.Skip("tree-sitter requires cgo")
	}
	pre := "template <typename T> struct S { T x; };\ntemplate struct S<float>;"
	post := "template <typename T> struct S { T x; };\ntemplate struct S<float>;\nint main( {"
	err := verifySyntax(pre, post, "solver.cpp")
	require.Error(t, err)
	// The delta (1 new) and the pre-existing count (1) must both be stated,
	// and the post-edit locations reported.
	require.Contains(t, err.Error(), "1 new (1 pre-existing)")
	require.Contains(t, err.Error(), "post-edit locations:")
}

func TestVerifySyntaxNewFile_RejectsSyntaxErrors(t *testing.T) {
	if !isSitterAvailable() {
		t.Skip("tree-sitter requires cgo")
	}
	err := verifySyntax("", "package main\nfunc main(", "main.go")
	require.Error(t, err)
	require.Contains(t, err.Error(), "AST contains 1 error(s)")
	require.Contains(t, err.Error(), "locations: 2:11")
}

func TestViewNodeTool(t *testing.T) {
	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// Create a test file
	code := `package main

import "fmt"

type Config struct {
	Port int
}

func GetConfig() *Config {
	return &Config{Port: 8080}
}
`
	filePath := filepath.Join(workingDir, "main.go")
	err := os.WriteFile(filePath, []byte(code), 0o644)
	require.NoError(t, err)

	tool := NewViewNodeTool(workingDir)

	// View GetConfig
	input, err := json.Marshal(ViewNodeParams{
		FilePath: "main.go",
		NodeName: "GetConfig",
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  ViewNodeToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "func GetConfig")
	require.Contains(t, resp.Content, "Port: 8080")

	// View Config struct
	inputStruct, err := json.Marshal(ViewNodeParams{
		FilePath: "main.go",
		NodeName: "Config",
	})
	require.NoError(t, err)

	respStruct, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  ViewNodeToolName,
		Input: string(inputStruct),
	})
	require.NoError(t, err)
	require.False(t, respStruct.IsError)
	require.Contains(t, respStruct.Content, "Config struct")
}

func TestViewTool_Summarize(t *testing.T) {
	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	code := `package main

import "fmt"

type Config struct {
	Port int
}

func GetConfig() *Config {
	return &Config{Port: 8080}
}

func helper() {}
`
	filePath := filepath.Join(workingDir, "main.go")
	err := os.WriteFile(filePath, []byte(code), 0o644)
	require.NoError(t, err)

	tool := NewViewTool(nil, &mockPermissionService{}, mockFileTrackerService{}, nil, workingDir, nil)

	input, err := json.Marshal(ViewParams{
		FilePath:  "main.go",
		Summarize: true,
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  ViewToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "<file_outline>")
	require.Contains(t, resp.Content, "Config")
	require.Contains(t, resp.Content, "GetConfig")
	// Private function helper should not be in the outline if it's Go (since it's unexported)
	if isSitterAvailable() {
		require.NotContains(t, resp.Content, "helper")
	}
}
