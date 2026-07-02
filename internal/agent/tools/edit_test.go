package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func TestPreserveQuoteStyle(t *testing.T) {
	tests := []struct {
		name        string
		matchedText string
		newString   string
		expected    string
	}{
		{
			name:        "Convert double to single quotes",
			matchedText: "let x = 'hello';",
			newString:   `let x = "world";`,
			expected:    `let x = 'world';`,
		},
		{
			name:        "Convert single to double quotes",
			matchedText: `let x = "hello";`,
			newString:   "let x = 'world';",
			expected:    `let x = "world";`,
		},
		{
			name:        "Convert double to backticks",
			matchedText: "let x = `hello`;",
			newString:   `let x = "world";`,
			expected:    "let x = `world`;",
		},
		{
			name:        "No quotes in matched, keep new",
			matchedText: "let x = 123;",
			newString:   `let x = "world";`,
			expected:    `let x = "world";`,
		},
		{
			name:        "Escaped quotes conversion",
			matchedText: "let x = 'hello \\'friend\\'';",
			newString:   `let x = "world \"friend\"";`,
			expected:    "let x = 'world \\'friend\\'';",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := preserveQuoteStyle(tt.matchedText, tt.newString)
			require.Equal(t, tt.expected, actual)
		})
	}
}

func TestApplyEditWithFuzzy(t *testing.T) {
	content := `package main

import "fmt"

func main() {
	fmt.Println("Hello, world!")
}`

	// 1. Exact match
	res, err := applyEditWithFuzzy(nil, "main.go", content, `fmt.Println("Hello, world!")`, `fmt.Println("Hello, Go!")`, false)
	require.NoError(t, err)
	require.Contains(t, res, `fmt.Println("Hello, Go!")`)

	// 2. Fuzzy match with different whitespace/indentation
	res, err = applyEditWithFuzzy(nil, "main.go", content, `  fmt.Println( "Hello, world!" )`, `fmt.Println("Hello, Go!")`, false)
	require.NoError(t, err)
	require.Contains(t, res, `fmt.Println("Hello, Go!")`)

	// 3. Fuzzy match with quote preservation
	res, err = applyEditWithFuzzy(nil, "main.go", content, `fmt.Println('Hello, world!')`, `fmt.Println("Hello, Go!")`, false)
	require.NoError(t, err)
	// Output should have double quotes because the original/matched text has double quotes
	require.Contains(t, res, `fmt.Println("Hello, Go!")`)

	// 4. Reject match below threshold
	_, err = applyEditWithFuzzy(nil, "main.go", content, `fmt.Println("Goodbye, world!")`, `fmt.Println("Hello, Go!")`, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "below threshold")
}

type mockFileTrackerForEdit struct{}

func (m mockFileTrackerForEdit) RecordRead(ctx context.Context, sessionID, path string) {}
func (m mockFileTrackerForEdit) LastReadTime(ctx context.Context, sessionID, path string) time.Time {
	return time.Now()
}
func (m mockFileTrackerForEdit) ListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	return nil, nil
}

func TestEditToolWithHashline(t *testing.T) {
	t.Parallel()
	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "test.txt")
	err := os.WriteFile(filePath, []byte("line A\nline B\nline C"), 0o644)
	require.NoError(t, err)

	// Compute hash of "line B"
	hashB := crc32.ChecksumIEEE([]byte("line B"))
	taggedOldString := fmt.Sprintf("     2:%08x|line B", hashB)

	tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerForEdit{}, workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// 1. Successful replacement using hashliness
	input, err := json.Marshal(EditParams{
		FilePath:    filePath,
		OldString:   taggedOldString,
		NewString:   "line B modified",
		UseHashline: true,
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  EditToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, "expected successful edit but got error: %s", resp.Content)

	// Verify content was updated on disk
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "line A\nline B modified\nline C", string(data))

	// Verify snippet is returned in metadata with correct line numbers and hashes
	var meta EditResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.NotEmpty(t, meta.Snippet)
	// Should contain line 2 with its new hash
	newHashB := crc32.ChecksumIEEE([]byte("line B modified"))
	expectedSnippetLine := fmt.Sprintf("     2:%08x|line B modified", newHashB)
	require.Contains(t, meta.Snippet, expectedSnippetLine)
}

func TestEditToolHashMismatch(t *testing.T) {
	t.Parallel()
	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "test.txt")
	err := os.WriteFile(filePath, []byte("line A\nline B\nline C"), 0o644)
	require.NoError(t, err)

	// Mismatching hash
	taggedOldString := "     2:badabada|line B"

	tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerForEdit{}, workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	input, err := json.Marshal(EditParams{
		FilePath:  filePath,
		OldString: taggedOldString,
		NewString: "line B modified",
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  EditToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Hash verification failed")
}

func TestEditToolAnchorPoisoning(t *testing.T) {
	t.Parallel()
	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "test.txt")
	err := os.WriteFile(filePath, []byte("line A\nline B\nline C"), 0o644)
	require.NoError(t, err)

	tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerForEdit{}, workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	input, err := json.Marshal(EditParams{
		FilePath:  filePath,
		OldString: "line B",
		NewString: "     2:abcdef12|poisoned line B",
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  EditToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Security violation: raw hashline tags")
}
