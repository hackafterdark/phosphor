package phosphordocs

import (
	"path/filepath"
	"slices"
	"strings"
)

// excludedDirs lists docs-root-relative directory prefixes that are not
// user-facing help material and therefore never shipped in the binary. Dev-facing
// architecture notes and ADRs describe internal design decisions; shipping them
// would widen the searchable corpus with content that answers "why did we build it
// this way" rather than "how do I use it," degrading lookup precision.
var excludedDirs = []string{
	"adr/",
	"architecture/",
}

// excludedFiles lists individual docs-root-relative files that are not user-facing
// help material and are shipped neither by directory rule nor by extension.
var excludedFiles = []string{
	// Reserved landing-doc name; the generator writes content/README.md itself, so a
	// canonical doc of the same name is never copied into that slot.
	"README.md",
	// Human-facing sitemap linked from the repository README. The generator emits its
	// own content/README.md index for the in-app corpus, so shipping this too would
	// give agents two competing tables of contents to choose between.
	"INDEX.md",
	// Internal roadmap/forward-looking notes, not a description of shipped behavior.
	"hooks/FUTURE.md",
	// A QA harness description, useful to maintainers rather than to users asking
	// how to use the product.
	"security/SECURITY_VALIDATION_TEST_SUITE.md",
}

// IsIncluded reports whether the given docs-root-relative path belongs in the
// shipped help corpus. Only markdown files qualify, minus the explicit exclusions.
//
// The generator (gen/main.go) and the drift-guard test both consult this so the
// allow-list has exactly one source of truth.
func IsIncluded(relPath string) bool {
	rel := filepath.ToSlash(filepath.Clean(relPath))
	if !strings.HasSuffix(rel, ".md") {
		return false
	}
	for _, dir := range excludedDirs {
		if strings.HasPrefix(rel, dir) {
			return false
		}
	}
	for _, file := range excludedFiles {
		if rel == file {
			return false
		}
	}
	return true
}

// sortDocs orders docs by path so listings and the TOC are deterministic across
// platforms and filesystem walk order.
func sortDocs(docs []Doc) {
	slices.SortFunc(docs, func(a, b Doc) int {
		return strings.Compare(a.RelPath, b.RelPath)
	})
}
