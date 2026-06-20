package tools

import (
	"testing"

	"github.com/hackafterdark/phosphor/internal/agent/parser"
)

func TestExecuteStructuralSearch_ListTemplates(t *testing.T) {
	// Ensure registry is loaded
	parser.Registry.Reload(t.TempDir())

	tests := []struct {
		name     string
		action   string
		language string
		wantOK   bool
	}{
		{
			name:     "list go templates",
			action:   "list_templates",
			language: "go",
			wantOK:   true,
		},
		{
			name:     "list rust templates",
			action:   "list_templates",
			language: "rust",
			wantOK:   true,
		},
		{
			name:     "list templates defaults to go",
			action:   "list_templates",
			language: "",
			wantOK:   true,
		},
		{
			name:     "list unknown language",
			action:   "list_templates",
			language: "fortran",
			wantOK:   true,
		},
		{
			name:     "normal search still works",
			action:   "search",
			language: "go",
			wantOK:   true,
		},
		{
			name:     "default action is search",
			action:   "",
			language: "go",
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := StructuralSearchParams{
				Action:   tt.action,
				Language: tt.language,
			}
			_, err := executeStructuralSearch(nil, t.TempDir(), params)
			if (err == nil) != tt.wantOK {
				t.Errorf("executeStructuralSearch() error = %v, wantOK %v", err, tt.wantOK)
			}
		})
	}
}

func TestFormatTemplateList(t *testing.T) {
	templates := []AvailableTemplateInfo{
		{ID: "find_functions", Description: "Discover all functions"},
		{ID: "find_structs", Description: "Find structures"},
	}
	result := formatTemplateList(templates)
	if len(result) == 0 {
		t.Fatal("expected non-empty output")
	}
	if result[0] != 'A' {
		t.Errorf("expected output to start with 'A', got %q", result[0])
	}
}
