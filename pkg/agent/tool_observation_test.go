package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestTranslateToObservation(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		toolName string
		want     string
	}{
		{
			name:     "missing required parameter",
			err:      errors.New("missing required parameter: pattern"),
			toolName: "grep",
			want:     "Tool Observation:",
		},
		{
			name:     "invalid pattern",
			err:      errors.New("invalid pattern: [invalid regex"),
			toolName: "grep",
			want:     "Tool Observation:",
		},
		{
			name:     "invalid json",
			err:      errors.New("invalid json in tool call input"),
			toolName: "edit",
			want:     "Tool Observation:",
		},
		{
			name:     "malformed",
			err:      errors.New("malformed tool call output"),
			toolName: "bash",
			want:     "Tool Observation:",
		},
		{
			name:     "extra data",
			err:      errors.New("extra data in input"),
			toolName: "write",
			want:     "Tool Observation:",
		},
		{
			name:     "context overflow",
			err:      errors.New("context overflow: input too long"),
			toolName: "read",
			want:     "Tool Observation:",
		},
		{
			name:     "generic tool error",
			err:      errors.New("tool validation failed"),
			toolName: "ls",
			want:     "Tool Observation:",
		},
		{
			name:     "unknown error",
			err:      errors.New("some unexpected error occurred"),
			toolName: "cat",
			want:     "Tool Observation:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateToObservation(tt.err, tt.toolName)
			if !strings.Contains(got, tt.want) {
				t.Errorf("translateToObservation(%q, %q) = %q, want to contain %q",
					tt.err, tt.toolName, got, tt.want)
			}
		})
	}
}

func TestExtractParamName(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "parameter with colon",
			msg:  "missing required parameter: pattern",
			want: "pattern",
		},
		{
			name: "parameter with space",
			msg:  "missing required parameter input",
			want: "input",
		},
		{
			name: "no parameter",
			msg:  "some other error",
			want: "",
		},
		{
			name: "parameter with trailing text",
			msg:  "missing required parameter: file_path (required)",
			want: "file_path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractParamName(tt.msg)
			if got != tt.want {
				t.Errorf("extractParamName(%q) = %q, want %q", tt.msg, got, tt.want)
			}
		})
	}
}
