package styles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThemeBuiltin(t *testing.T) {
	// Empty theme option should fallback to default theme for provider.
	stylesDefault := Theme("", "openai", "")
	stylesPantera := CharmtonePantera()
	if hex(stylesDefault.Background) == nil || hex(stylesPantera.Background) == nil ||
		*hex(stylesDefault.Background) != *hex(stylesPantera.Background) {
		t.Errorf("expected default theme to match Charmtone Pantera")
	}

	// "pantera" / "charmtone" name check.
	stylesNamedPantera := Theme("pantera", "openai", "")
	if hex(stylesNamedPantera.Background) == nil ||
		*hex(stylesNamedPantera.Background) != *hex(stylesPantera.Background) {
		t.Errorf("expected 'pantera' named theme to match Charmtone Pantera")
	}

	// "obsidiana" / "hyper" name check.
	stylesNamedObsidiana := Theme("obsidiana", "openai", "")
	stylesObsidiana := HyperphosphorObsidiana()
	if hex(stylesNamedObsidiana.Background) == nil || hex(stylesObsidiana.Background) == nil ||
		*hex(stylesNamedObsidiana.Background) != *hex(stylesObsidiana.Background) {
		t.Errorf("expected 'obsidiana' named theme to match Hyperphosphor Obsidiana")
	}
}

func TestThemeFromFile(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Write YAML theme.
	yamlContent := `
name: Custom Test Theme
primary: "#FF0000"
bgBase: "#0000FF"
`
	yamlPath := filepath.Join(tempDir, "test_theme.yaml")
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write test theme: %v", err)
	}

	themeStyles := Theme(yamlPath, "openai", "")

	// Check custom background color.
	hexStr := hex(themeStyles.Background)
	if hexStr == nil || *hexStr != "#0000ff" {
		t.Errorf("expected background color #0000ff, got %v", hexStr)
	}

	// Check custom primary color.
	primaryHex := hex(themeStyles.WorkingGradFromColor)
	if primaryHex == nil || *primaryHex != "#ff0000" {
		t.Errorf("expected primary color #ff0000, got %v", primaryHex)
	}

	// 2. Write JSON theme.
	jsonContent := `{"name": "Custom JSON Theme", "primary": "#00FF00"}`
	jsonPath := filepath.Join(tempDir, "test_theme.json")
	if err := os.WriteFile(jsonPath, []byte(jsonContent), 0o644); err != nil {
		t.Fatalf("failed to write test theme: %v", err)
	}

	themeStylesJSON := Theme(jsonPath, "openai", "")
	primaryHexJSON := hex(themeStylesJSON.WorkingGradFromColor)
	if primaryHexJSON == nil || *primaryHexJSON != "#00ff00" {
		t.Errorf("expected primary color #00ff00, got %v", primaryHexJSON)
	}
}

func TestThemeLookup(t *testing.T) {
	tempDir := t.TempDir()

	// Create a mock local workspace directory structure.
	wsDir := filepath.Join(tempDir, "workspace")
	if err := os.MkdirAll(filepath.Join(wsDir, ".phosphor", "themes"), 0o755); err != nil {
		t.Fatalf("failed to create dirs: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(wsDir, "themes"), 0o755); err != nil {
		t.Fatalf("failed to create dirs: %v", err)
	}

	// Create a mock global themes directory structure.
	globalDir := filepath.Join(tempDir, "global")
	if err := os.MkdirAll(filepath.Join(globalDir, "themes"), 0o755); err != nil {
		t.Fatalf("failed to create dirs: %v", err)
	}

	// Set environment variable for global config path.
	t.Setenv("PHOSPHOR_GLOBAL_CONFIG", globalDir)

	// Write themes in different places.
	// 1. Global theme.
	globalThemeContent := `
name: Global Theme
primary: "#000001"
`
	if err := os.WriteFile(filepath.Join(globalDir, "themes", "mytheme.yaml"), []byte(globalThemeContent), 0o644); err != nil {
		t.Fatalf("failed to write global theme: %v", err)
	}

	// 2. Local themes/ theme.
	localThemeContent := `
name: Local Themes Theme
primary: "#000002"
`
	if err := os.WriteFile(filepath.Join(wsDir, "themes", "mytheme.yaml"), []byte(localThemeContent), 0o644); err != nil {
		t.Fatalf("failed to write local theme: %v", err)
	}

	// 3. Local .phosphor/themes/ theme.
	dotPhosphorThemeContent := `
name: Dot Phosphor Theme
primary: "#000003"
`
	dotPhosphorThemePath := filepath.Join(wsDir, ".phosphor", "themes", "mytheme.yaml")
	if err := os.WriteFile(dotPhosphorThemePath, []byte(dotPhosphorThemeContent), 0o644); err != nil {
		t.Fatalf("failed to write dot phosphor theme: %v", err)
	}

	// 1. Should load from .phosphor/themes first (highest priority).
	styles1 := Theme("mytheme", "openai", wsDir)
	hex1 := hex(styles1.WorkingGradFromColor)
	if hex1 == nil || *hex1 != "#000003" {
		t.Errorf("expected primary #000003, got %v", hex1)
	}

	// 2. Remove .phosphor/themes theme, should load from themes/ (medium priority).
	if err := os.Remove(dotPhosphorThemePath); err != nil {
		t.Fatalf("failed to remove: %v", err)
	}
	styles2 := Theme("mytheme", "openai", wsDir)
	hex2 := hex(styles2.WorkingGradFromColor)
	if hex2 == nil || *hex2 != "#000002" {
		t.Errorf("expected primary #000002, got %v", hex2)
	}

	// 3. Remove themes/ theme, should load from global config (lowest priority).
	if err := os.Remove(filepath.Join(wsDir, "themes", "mytheme.yaml")); err != nil {
		t.Fatalf("failed to remove: %v", err)
	}
	styles3 := Theme("mytheme", "openai", wsDir)
	hex3 := hex(styles3.WorkingGradFromColor)
	if hex3 == nil || *hex3 != "#000001" {
		t.Errorf("expected primary #000001, got %v", hex3)
	}
}
