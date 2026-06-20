package jsonmerge

import (
	"encoding/json"
	"testing"
)

func TestMerge(t *testing.T) {
	tests := []struct {
		name     string
		inputs   [][]byte
		expected map[string]any
	}{
		{
			name:     "empty inputs",
			inputs:   [][]byte{},
			expected: map[string]any{},
		},
		{
			name:     "single input",
			inputs:   [][]byte{[]byte(`{"a":1}`)},
			expected: map[string]any{"a": float64(1)},
		},
		{
			name:     "overwrite primitive",
			inputs:   [][]byte{[]byte(`{"a":1}`), []byte(`{"a":2}`)},
			expected: map[string]any{"a": float64(2)},
		},
		{
			name: "merge nested objects",
			inputs: [][]byte{
				[]byte(`{"a":{"b":1,"c":2}}`),
				[]byte(`{"a":{"c":3,"d":4}}`),
			},
			expected: map[string]any{
				"a": map[string]any{
					"b": float64(1),
					"c": float64(3),
					"d": float64(4),
				},
			},
		},
		{
			name:     "replace array",
			inputs:   [][]byte{[]byte(`{"a":[1,2]}`), []byte(`{"a":[3,4]}`)},
			expected: map[string]any{"a": []any{float64(3), float64(4)}},
		},
		{
			name:     "empty objects",
			inputs:   [][]byte{[]byte(`{}`), []byte(`{}`), []byte(`{}`)},
			expected: map[string]any{},
		},
		{
			name: "config merge pattern",
			inputs: [][]byte{
				[]byte(`{"thinking":"enabled","timeout":30}`),
				[]byte(`{"timeout":60,"retries":3}`),
				[]byte(`{"retries":5,"verbose":true}`),
			},
			expected: map[string]any{
				"thinking": "enabled",
				"timeout":  float64(60),
				"retries":  float64(5),
				"verbose":  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Merge(tt.inputs...)
			if err != nil {
				t.Fatalf("Merge() error = %v", err)
			}

			var got map[string]any
			if err := json.Unmarshal(result, &got); err != nil {
				t.Fatalf("Failed to unmarshal result: %v", err)
			}

			// Compare by re-marshaling to avoid float64 vs int issues
			gotJSON, _ := json.Marshal(got)
			expJSON, _ := json.Marshal(tt.expected)
			if string(gotJSON) != string(expJSON) {
				t.Errorf("Merge() = %s, want %s", gotJSON, expJSON)
			}
		})
	}
}

func TestMergeProviderOptions(t *testing.T) {
	// Simulates the coordinator.go pattern: catwalk opts + provider opts + model opts
	inputs := [][]byte{
		[]byte(`{"reasoning_effort":"high","max_tokens":4096}`),
		[]byte(`{"max_tokens":8192,"temperature":0.7}`),
		[]byte(`{"temperature":0.9}`),
	}

	result, err := Merge(inputs...)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if got["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high", got["reasoning_effort"])
	}
	if got["max_tokens"] != float64(8192) {
		t.Errorf("max_tokens = %v, want 8192", got["max_tokens"])
	}
	if got["temperature"] != float64(0.9) {
		t.Errorf("temperature = %v, want 0.9", got["temperature"])
	}
}
