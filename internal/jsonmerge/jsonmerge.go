// Package jsonmerge provides a simple deep-merge function for JSON objects.
// It merges multiple JSON objects into one, with later values overwriting earlier
// ones. Nested maps are merged recursively; arrays and primitives are replaced.
package jsonmerge

import (
	"encoding/json"
	"fmt"
)

// Merge merges multiple JSON byte slices into a single JSON object.
// Later values overwrite earlier ones for conflicting keys.
// Nested objects are merged recursively. Arrays and primitives are replaced.
// Returns the merged JSON bytes.
func Merge(data ...[]byte) ([]byte, error) {
	var result map[string]any

	for i, b := range data {
		if len(b) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("jsonmerge: failed to parse input %d: %w", i, err)
		}
		if result == nil {
			result = m
		} else {
			deepMerge(result, m)
		}
	}

	if result == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(result)
}

// deepMerge merges src into dst recursively.
// Nested maps are merged; all other values are replaced.
func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if existing, ok := dst[k]; !ok {
			dst[k] = v
		} else {
			dst[k] = mergeTwo(existing, v)
		}
	}
}

// mergeTwo merges two values. If both are maps, they are merged recursively.
// Otherwise, the second value wins.
func mergeTwo(a, b any) any {
	aMap, aOk := a.(map[string]any)
	bMap, bOk := b.(map[string]any)

	if !aOk || !bOk {
		return b
	}

	merged := make(map[string]any, len(aMap))
	for k, v := range aMap {
		merged[k] = v
	}

	for k, v := range bMap {
		if existing, ok := merged[k]; ok {
			if existingMap, ok := existing.(map[string]any); ok {
				if vMap, ok := v.(map[string]any); ok {
					merged[k] = mergeTwo(existingMap, vMap)
					continue
				}
			}
		}
		merged[k] = v
	}

	return merged
}
