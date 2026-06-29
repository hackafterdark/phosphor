package tools

import (
	"context"
	"encoding/json"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/hackafterdark/phosphor/internal/shell"
	"github.com/stretchr/testify/require"
)

// TestFuzzing_IsInsideHandlesGarbagePaths verifies that IsInside does not
// panic when given random, malformed, or edge-case paths. Security guards
// must fail-closed (return false on error) rather than fail-open (crashing
// or returning true).
func TestFuzzing_IsInsideHandlesGarbagePaths(t *testing.T) {
	t.Parallel()

	// Generate a valid workspace path for the test.
	tmpDir := t.TempDir()
	absWorkspace, err := filepath.Abs(tmpDir)
	require.NoError(t, err)

	// Generate random garbage paths to feed into IsInside.
	garbagePaths := generateGarbagePaths(100)

	for _, path := range garbagePaths {
		t.Run(path, func(t *testing.T) {
			// IsInside should never panic, regardless of input.
			require.NotPanics(t, func() {
				result := filepathext.IsInside(path, absWorkspace)
				// Result can be true or false - we just verify it doesn't crash.
				_ = result
			})
		})
	}
}

// TestFuzzing_IsInsideHandlesEmptyAndSpecialPaths verifies that IsInside
// handles empty strings, null bytes, and other special characters gracefully.
func TestFuzzing_IsInsideHandlesEmptyAndSpecialPaths(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	absWorkspace, err := filepath.Abs(tmpDir)
	require.NoError(t, err)

	specialPaths := []string{
		"",                         // Empty string
		"\x00",                     // Null byte
		"\x00\x00",                 // Multiple null bytes
		"\\",                       // Just a backslash
		"/",                        // Just a forward slash
		"\\\\",                     // Multiple backslashes
		"////",                     // Multiple forward slashes
		"../",                      // Parent with trailing slash
		"./",                       // Current with trailing slash
		"....",                     // Four dots (not a valid traversal)
		strings.Repeat(".", 100),   // Many dots
		strings.Repeat("\x00", 50), // Many null bytes
		"a\x00b",                   // Null byte in middle
		"hello world",              // Space in path
		"hello\tworld",             // Tab in path
		"hello\nworld",             // Newline in path
		"hello\rworld",             // Carriage return in path
	}

	for _, path := range specialPaths {
		t.Run(path, func(t *testing.T) {
			require.NotPanics(t, func() {
				result := filepathext.IsInside(path, absWorkspace)
				_ = result
			})
		})
	}
}

// TestFuzzing_BlockFuncHandlesGarbageArgs verifies that BlockFunc does not
// panic when given random garbage arguments. This tests the shell command
// blocking logic's robustness.
func TestFuzzing_BlockFuncHandlesGarbageArgs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	shellInstance := newShellForFuzzTest(tmpDir)

	garbageCommands := generateGarbageCommands(100)

	for _, cmd := range garbageCommands {
		t.Run(cmd, func(t *testing.T) {
			// Exec should never panic, regardless of input.
			require.NotPanics(t, func() {
				_, _, _ = shellInstance.Exec(t.Context(), cmd)
			})
		})
	}
}

// TestFuzzing_BlockFuncHandlesEmptyAndSpecialCommands verifies that BlockFunc
// handles empty strings, null bytes, and other special characters gracefully.
func TestFuzzing_BlockFuncHandlesEmptyAndSpecialCommands(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	shellInstance := newShellForFuzzTest(tmpDir)

	specialCommands := []string{
		"",                           // Empty command
		"\x00",                       // Null byte
		"\x00\x00",                   // Multiple null bytes
		"   ",                        // Just spaces
		"\t\t",                       // Just tabs
		"\n\n",                       // Just newlines
		"cmd \x00 arg",               // Null byte in middle
		"cmd\narg",                   // Newline in middle
		"cmd\rarg",                   // Carriage return in middle
		strings.Repeat("a", 10000),   // Very long command
		strings.Repeat("\x00", 1000), // Very long null bytes
	}

	for _, cmd := range specialCommands {
		t.Run(cmd, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, _, _ = shellInstance.Exec(t.Context(), cmd)
			})
		})
	}
}

// TestFuzzing_WriteToolHandlesGarbagePaths verifies that the write tool does
// not panic when given random garbage file paths. This is a higher-level test
// that exercises the full tool pipeline including SmartJoin and IsInside.
func TestFuzzing_WriteToolHandlesGarbagePaths(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := newTestContext()

	tool := NewWriteTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	garbagePaths := generateGarbagePaths(50)

	for _, path := range garbagePaths {
		t.Run(path, func(t *testing.T) {
			input, err := json.Marshal(WriteParams{FilePath: path, Content: "test"})
			require.NoError(t, err)

			// Tool.Run should never panic, regardless of input.
			require.NotPanics(t, func() {
				_, _ = tool.Run(ctx, fantasy.ToolCall{
					ID:    "fuzz-test",
					Name:  WriteToolName,
					Input: string(input),
				})
			})
		})
	}
}

// --- Helper functions ---

// newShellForFuzzTest creates a shell with all block funcs for fuzz testing.
func newShellForFuzzTest(workingDir string) *shell.Shell {
	return shell.NewShell(&shell.Options{
		WorkingDir: workingDir,
		BlockFuncs: []shell.BlockFunc{
			shell.CommandsBlocker([]string{"curl", "wget", "ssh", "sudo"}),
			shell.ArgumentsBlocker("go", []string{"run"}, []string{"."}),
			shell.ArgumentsBlocker("go", []string{"build"}, []string{"."}),
		},
	})
}

// newTestContext creates a test context with session ID.
func newTestContext() context.Context {
	return context.WithValue(context.Background(), SessionIDContextKey, "fuzz-test-session")
}

// generateGarbagePaths generates a slice of random, malformed paths for fuzzing.
func generateGarbagePaths(count int) []string {
	paths := make([]string, 0, count)

	characters := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()_+-=[]{}|;':\",./<>?`~ \t\n\r"

	for i := 0; i < count; i++ {
		length := rand.Intn(200) + 1 // 1 to 200 characters
		path := make([]byte, length)
		for j := range path {
			path[j] = characters[rand.Intn(len(characters))]
		}
		paths = append(paths, string(path))
	}

	return paths
}

// generateGarbageCommands generates a slice of random, malformed commands for fuzzing.
func generateGarbageCommands(count int) []string {
	commands := make([]string, 0, count)

	characters := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()_+-=[]{}|;':\",./<>?`~ \t\n\r"

	for i := 0; i < count; i++ {
		length := rand.Intn(500) + 1 // 1 to 500 characters
		cmd := make([]byte, length)
		for j := range cmd {
			cmd[j] = characters[rand.Intn(len(characters))]
		}
		commands = append(commands, string(cmd))
	}

	return commands
}
