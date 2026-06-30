package tools

import (
	"testing"

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
