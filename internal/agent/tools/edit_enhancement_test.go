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

func TestCheckSecrets(t *testing.T) {
	// Test AWS Secret Key
	err := checkSecrets(`aws_secret_access_key = "abc123XYZ/foo/bar/baz/123456789012345678"`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "AWS secret access key")

	// Test Private Key
	err = checkSecrets("-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0y...\n-----END RSA PRIVATE KEY-----")
	require.Error(t, err)
	require.Contains(t, err.Error(), "private key")

	// Test Generic API Key
	err = checkSecrets(`api_key = "AIzaSyD-12345678901234567890"`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "API key")

	// Test safe content
	err = checkSecrets(`const myVar = "hello world"`)
	require.NoError(t, err)
}

func TestVerifySyntax(t *testing.T) {
	// Valid Go
	err := verifySyntax(`package main
func main() {}`, "main.go")
	require.NoError(t, err)

	// Invalid Go - should fail if sitter is available
	err = verifySyntax(`package main
func main(`, "main.go")
	if isSitterAvailable() {
		require.Error(t, err)
	} else {
		require.NoError(t, err)
	}

	// Invalid syntax in txt file should always succeed
	err = verifySyntax(`package main
func main(`, "main.txt")
	require.NoError(t, err)
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

	tool := NewViewTool(nil, &mockPermissionService{}, mockFileTrackerService{}, nil, workingDir)

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
