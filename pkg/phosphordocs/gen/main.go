// Command gen regenerates the embedded documentation corpus under content/ from the
// canonical `docs/` tree at the repository root. The canonical tree is the single
// source of truth; the embedded copy is a build artifact.
//
// Run it (from anywhere) with:
//
//	go generate ./pkg/phosphordocs
//	# or
//	go run ./pkg/phosphordocs/gen
//
// The set of copied files is decided by phosphordocs.IsIncluded so the allow-list
// has exactly one definition shared by the generator and the runtime.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hackafterdark/phosphor/pkg/phosphordocs"
)

type entry struct {
	Rel   string
	Virt  string
	Title string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "phosphordocs/gen:", err)
		os.Exit(1)
	}
}

func run() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	srcDir := filepath.Join(repoRoot, "docs")
	dstDir := filepath.Join(repoRoot, "pkg", "phosphordocs", "content")

	if _, err := os.Stat(srcDir); err != nil {
		return fmt.Errorf("canonical docs dir not found at %s: %w", srcDir, err)
	}

	// Rebuild from empty so removed or renamed docs do not linger in the corpus.
	if err := os.RemoveAll(dstDir); err != nil {
		return fmt.Errorf("clear content dir: %w", err)
	}

	var (
		copied, skipped int
		entries         []entry
	)
	err = filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !phosphordocs.IsIncluded(rel) {
			skipped++
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out := filepath.Join(dstDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		entries = append(entries, entry{
			Rel:   rel,
			Virt:  phosphordocs.ToVirtual(rel),
			Title: headingOf(data, rel),
		})
		copied++
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk docs: %w", err)
	}
	if copied == 0 {
		return fmt.Errorf("copied zero docs; refusing to leave the corpus empty")
	}

	slices.SortFunc(entries, func(a, b entry) int {
		return strings.Compare(a.Rel, b.Rel)
	})
	if err := writeIndex(dstDir, entries); err != nil {
		return err
	}

	fmt.Printf("phosphordocs: copied %d doc(s), skipped %d into %s\n",
		copied, skipped, filepath.ToSlash(dstDir))
	return nil
}

// writeIndex emits a generated README.md that lists every shipped doc with its
// virtual path. It is the corpus landing page: the agent can read it to discover
// what is available before opening a specific file.
func writeIndex(dstDir string, entries []entry) error {
	var sb strings.Builder
	sb.WriteString("# Phosphor Documentation\n\n")
	sb.WriteString("This is the corpus of user-facing Phosphor docs compiled into the binary.\n")
	sb.WriteString("Search it with the `phosphor_docs` tool, or open a file below with the\n")
	sb.WriteString("`view` tool using its virtual path.\n\n")
	sb.WriteString("## Index\n\n")
	for _, e := range entries {
		fmt.Fprintf(&sb, "- [%s](%s)\n", e.Title, e.Virt)
	}

	out := filepath.Join(dstDir, "README.md")
	return os.WriteFile(out, []byte(sb.String()), 0o644)
}

// headingOf returns the first markdown H1 of a doc, falling back to its base name.
func headingOf(data []byte, rel string) string {
	for _, line := range strings.Split(string(data), "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "# ") {
			if title := strings.TrimSpace(trimmed[2:]); title != "" {
				return title
			}
		}
	}
	return strings.TrimSuffix(filepath.Base(rel), ".md")
}

// findRepoRoot walks up from the working directory until it finds a go.mod.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}
