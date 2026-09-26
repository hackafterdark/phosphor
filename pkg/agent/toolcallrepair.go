package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"charm.land/fantasy"
)

// repairToolCall deterministically fixes tool calls the model emitted with
// inputs that do not match the tool's parameter schema. LLMs routinely
// stringify integers ("offset": "64"), spell booleans as "true", wrap arrays
// or objects in a JSON string, or hallucinate extra keys. Fantasy's decoder
// rejects all of these outright, which burns a full turn on a repairable
// mistake. The repair is schema-driven and purely mechanical: nothing is
// invented, values are only converted to the type the schema already
// declares. When the input cannot be reconciled with the schema at all the
// original call is returned untouched so the normal error path still runs.
func repairToolCall(_ context.Context, opts fantasy.ToolCallRepairOptions) (*fantasy.ToolCallContent, error) {
	call := opts.OriginalToolCall
	input := strings.TrimSpace(call.Input)
	if input == "" {
		input = "{}"
	}

	var tool fantasy.AgentTool
	for _, t := range opts.AvailableTools {
		if t.Info().Name == call.ToolName {
			tool = t
			break
		}
	}
	if tool == nil {
		return nil, nil
	}

	candidates := []string{input}
	if sanitized := sanitizeJSONInput(input); sanitized != input {
		candidates = append(candidates, sanitized)
	}

	var props map[string]any
	if p, ok := tool.Info().Parameters["properties"].(map[string]any); ok {
		props = p
	} else if tool.Info().Parameters != nil {
		props = tool.Info().Parameters
	}
	for _, candidate := range candidates {
		decoded, err := decodeLoose(candidate)
		if err != nil {
			continue
		}
		repaired, changed := coerceObject(decoded, props)
		if !changed {
			// Nothing to fix; let fantasy retry the original decode.
			continue
		}
		out, err := json.Marshal(repaired)
		if err != nil {
			continue
		}
		fixed := call
		fixed.Input = string(out)
		fixed.Invalid = false
		fixed.ValidationError = nil
		return &fixed, nil
	}
	return nil, nil
}

// decodeLoose parses a JSON object while keeping numbers as json.Number so
// coercions round-trip without float64 precision damage (large ids, offsets).
func decodeLoose(s string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("not a json object")
	}
	return m, nil
}

func coerceObject(obj map[string]any, props map[string]any) (map[string]any, bool) {
	changed := false
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		canon, schema, known := lookupProp(props, k)
		if !known && props != nil {
			// Free-form maps are schema'd with a "*" wildcard property;
			// keys match it instead of being treated as unknown.
			if wildcard, ok := props["*"]; ok {
				schema = wildcard
			} else if len(props) > 0 {
				// An unknown key guarantees a validation failure downstream;
				// dropping it gives the tool a chance to run instead.
				changed = true
				continue
			}
		}
		if known && canon != k {
			changed = true
		}
		nv, did := coerceValue(v, schema)
		if did {
			changed = true
		}
		out[canon] = nv
	}
	return out, changed
}

// lookupProp matches a schema property to a key the model sent, tolerating
// casing and separator drift (maxResults vs max_results) so a near-miss is
// repaired into the canonical name instead of rejected.
func lookupProp(props map[string]any, key string) (string, any, bool) {
	if props == nil {
		return key, nil, false
	}
	if s, ok := props[key]; ok {
		return key, s, true
	}
	norm := normalizeKey(key)
	for name, s := range props {
		if normalizeKey(name) == norm {
			return name, s, true
		}
	}
	return key, nil, false
}

func normalizeKey(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		if r == '_' || r == '-' || r == ' ' {
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func schemaType(schema any) string {
	m, ok := schema.(map[string]any)
	if !ok {
		return ""
	}
	t, _ := m["type"].(string)
	return t
}

func coerceValue(v any, schema any) (any, bool) {
	switch expect := schemaType(schema); expect {
	case "integer", "number":
		return coerceNumber(v, expect == "integer")
	case "boolean":
		return coerceBool(v)
	case "string":
		return coerceString(v)
	case "array":
		return coerceArray(v, schema)
	case "object":
		if obj, ok := v.(map[string]any); ok {
			props, _ := schema.(map[string]any)["properties"].(map[string]any)
			return coerceObject(obj, props)
		}
		// The model often stringifies nested objects; try to parse them.
		if s, ok := v.(string); ok {
			if obj, err := decodeLoose(s); err == nil {
				props, _ := schema.(map[string]any)["properties"].(map[string]any)
				inner, _ := coerceObject(obj, props)
				return inner, true
			}
		}
	}
	return v, false
}

func coerceNumber(v any, wantInt bool) (any, bool) {
	switch val := v.(type) {
	case string:
		s := strings.TrimSpace(val)
		if wantInt {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return json.Number(strconv.FormatInt(n, 10)), true
			}
			return v, false
		}
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return json.Number(s), true
		}
		return v, false
	case json.Number:
		if wantInt && strings.ContainsAny(string(val), ".eE") {
			if f, err := strconv.ParseFloat(string(val), 64); err == nil && f == float64(int64(f)) {
				return json.Number(strconv.FormatInt(int64(f), 10)), true
			}
			return v, false
		}
		return v, false
	case float64, int64, int:
		// UseNumber avoids these for objects, but nested arrays decoded
		// elsewhere can still land here.
		if wantInt {
			if f, ok := toFloat(v); ok && f == float64(int64(f)) {
				return json.Number(strconv.FormatInt(int64(f), 10)), true
			}
			return v, false
		}
		if f, ok := toFloat(v); ok {
			return json.Number(strconv.FormatFloat(f, 'g', -1, 0)), true
		}
		return v, false
	case bool:
		return v, false
	case nil:
		return v, false
	}
	return v, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

func coerceBool(v any) (any, bool) {
	if s, ok := v.(string); ok {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return v, false
}

func coerceString(v any) (any, bool) {
	switch val := v.(type) {
	case json.Number:
		return string(val), true
	case float64:
		return strconv.FormatFloat(val, 'g', -1, 0), true
	case int64:
		return strconv.FormatInt(int64(val), 10), true
	case int:
		return strconv.Itoa(val), true
	case bool:
		if val {
			return "true", true
		}
		return "false", true
	}
	return v, false
}

func coerceArray(v any, schema any) (any, bool) {
	m, _ := schema.(map[string]any)
	items, hasItems := m["items"]
	if arr, ok := v.([]any); ok {
		if !hasItems {
			return v, false
		}
		out := make([]any, len(arr))
		changed := false
		for i, el := range arr {
			nv, did := coerceValue(el, items)
			if did {
				changed = true
			}
			out[i] = nv
		}
		return out, changed
	}
	// A stringified array ("tags": "[\"a\",\"b\"]") is common with some
	// providers; reparse and coerce it when it is valid JSON.
	if s, ok := v.(string); ok {
		trimmed := strings.TrimSpace(s)
		if strings.HasPrefix(trimmed, "[") {
			dec := json.NewDecoder(strings.NewReader(trimmed))
			dec.UseNumber()
			var arr []any
			if err := dec.Decode(&arr); err == nil {
				if !hasItems {
					return arr, true
				}
				out := make([]any, len(arr))
				for i, el := range arr {
					out[i], _ = coerceValue(el, items)
				}
				return out, true
			}
		}
	}
	return v, false
}
