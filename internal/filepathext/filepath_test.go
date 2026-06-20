package filepathext

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSearchPath(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Create a subdirectory to simulate a nested path.
	subDir := filepath.Join(tmpDir, "src")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatalf("Mkdir(%q) error = %v", subDir, err)
	}

	deepDir := filepath.Join(subDir, "internal")
	if err := os.Mkdir(deepDir, 0o755); err != nil {
		t.Fatalf("Mkdir(%q) error = %v", deepDir, err)
	}

	// Create a separate directory for "absolute" path test (outside tmpDir).
	otherDir := filepath.Join(t.TempDir(), "other")
	if err := os.Mkdir(otherDir, 0o755); err != nil {
		t.Fatalf("Mkdir(%q) error = %v", otherDir, err)
	}

	tests := []struct {
		name       string
		workingDir string
		searchPath string
		want       string
	}{
		{
			name:       "empty search path returns working dir",
			workingDir: tmpDir,
			searchPath: "",
			want:       tmpDir,
		},
		{
			name:       "relative path joins with working dir",
			workingDir: tmpDir,
			searchPath: "src",
			want:       subDir,
		},
		{
			name:       "relative path with ./ prefix",
			workingDir: tmpDir,
			searchPath: "./src",
			want:       subDir,
		},
		{
			name:       "relative path with nested dirs",
			workingDir: tmpDir,
			searchPath: "src/internal",
			want:       deepDir,
		},
		{
			name:       "absolute path returns as-is",
			workingDir: tmpDir,
			searchPath: otherDir,
			want:       otherDir,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolveSearchPath(tc.workingDir, tc.searchPath)
			if err != nil {
				t.Fatalf("ResolveSearchPath(%q, %q) error = %v", tc.workingDir, tc.searchPath, err)
			}

			if got != tc.want {
				t.Errorf("ResolveSearchPath(%q, %q) = %q, want %q", tc.workingDir, tc.searchPath, got, tc.want)
			}
		})
	}
}
