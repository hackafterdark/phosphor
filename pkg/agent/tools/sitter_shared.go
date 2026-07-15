package tools

import (
	"fmt"
	"path/filepath"
	"strings"
)

func generateOutlineFallback(code []byte, filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	lines := strings.Split(string(code), "\n")
	var outline []string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		isSignature := false
		name := ""

		switch ext {
		case ".go":
			if strings.HasPrefix(trimmed, "func ") {
				parts := strings.Fields(trimmed)
				if len(parts) > 1 {
					fnName := parts[1]
					if strings.HasPrefix(fnName, "(") {
						idx := strings.Index(trimmed, ")")
						if idx != -1 {
							afterReceiver := strings.TrimSpace(trimmed[idx+1:])
							methodParts := strings.Fields(afterReceiver)
							if len(methodParts) > 0 {
								fnName = methodParts[0]
							}
						}
					}
					if idx := strings.Index(fnName, "("); idx != -1 {
						fnName = fnName[:idx]
					}
					if len(fnName) > 0 && fnName[0] >= 'A' && fnName[0] <= 'Z' {
						isSignature = true
					}
				}
			} else if strings.HasPrefix(trimmed, "type ") {
				parts := strings.Fields(trimmed)
				if len(parts) > 1 {
					typeName := parts[1]
					if len(typeName) > 0 && typeName[0] >= 'A' && typeName[0] <= 'Z' {
						isSignature = true
					}
				}
			}
		case ".py":
			if strings.HasPrefix(trimmed, "def ") {
				parts := strings.Fields(trimmed)
				if len(parts) > 1 {
					name = parts[1]
					if idx := strings.Index(name, "("); idx != -1 {
						name = name[:idx]
					}
					if !strings.HasPrefix(name, "_") {
						isSignature = true
					}
				}
			} else if strings.HasPrefix(trimmed, "class ") {
				parts := strings.Fields(trimmed)
				if len(parts) > 1 {
					name = parts[1]
					if idx := strings.Index(name, "("); idx != -1 {
						name = name[:idx]
					}
					if idx := strings.Index(name, ":"); idx != -1 {
						name = name[:idx]
					}
					if !strings.HasPrefix(name, "_") {
						isSignature = true
					}
				}
			}
		case ".ts", ".tsx", ".js", ".jsx":
			if strings.HasPrefix(trimmed, "export ") {
				isSignature = true
			}
		default:
			if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "class ") || strings.HasPrefix(trimmed, "struct ") {
				isSignature = true
			}
		}

		if isSignature {
			display := trimmed
			display = strings.TrimRight(display, " {:")
			outline = append(outline, fmt.Sprintf("Line %d: %s", i+1, display))
		}
	}

	if len(outline) == 0 {
		return "No public signatures found (fallback mode).", nil
	}
	return strings.Join(outline, "\n"), nil
}
