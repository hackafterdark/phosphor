// Package phosphordocs serves the user-facing Phosphor documentation that is
// compiled into the binary. It gives the agent a version-matched help corpus
// it can look up from any workspace, without the docs needing to exist on the
// user's disk and without spending any system-prompt tokens until a lookup
// actually runs.
//
// The corpus is a curated copy of the canonical `docs/` tree (see gen/main.go
// for the generator and allowlist.go for what is shipped). The canonical tree
// stays the single source of truth; the embedded copy is a build artifact and a
// drift-guard test fails if the two diverge.
package phosphordocs

import (
	"embed"
	"io/fs"
	"path/filepath"
	"strings"
)

// DocsPrefix is the virtual path prefix for embedded documentation files. The
// View tool resolves paths with this prefix from the embedded filesystem rather
// than from disk, mirroring how builtin skills are served via
// skills.BuiltinPrefix.
const DocsPrefix = "phosphor://docs/"

//go:generate go run github.com/hackafterdark/phosphor/pkg/phosphordocs/gen

// Version records which build of Phosphor these embedded docs were compiled from.
// Because the corpus is embedded at build time it always describes this exact
// binary; the value is stamped via -ldflags at release time (see the Taskfile
// docs:embed/build tasks) and defaults to "devel" otherwise. Surfacing it lets the
// agent tell a user on an old build that the guidance matches their build rather
// than the latest release.
//
// Run `go generate ./pkg/phosphordocs` (or `task docs:embed`) to refresh content/.
var Version = "devel"

// contentRoot is the directory name inside the embedded filesystem that holds
// the shipped documentation.
const contentRoot = "content"

//go:embed content
var contentFS embed.FS

// DocsFS returns the embedded documentation filesystem.
func DocsFS() embed.FS {
	return contentFS
}

// IsDocsPath reports whether the given path refers to an embedded doc.
func IsDocsPath(path string) bool {
	return strings.HasPrefix(path, DocsPrefix)
}

// RelFromVirtual converts a virtual path such as
// "phosphor://docs/commands/GOAL.md" into its path relative to the docs root,
// e.g. "commands/GOAL.md". Bare forms like "commands/GOAL.md" pass through
// unchanged so callers may accept either shape. Always forward-slash so addressing
// is stable across platforms regardless of the host path separator.
func RelFromVirtual(path string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimPrefix(path, DocsPrefix)))
}

// ToVirtual renders a docs-relative path as its canonical virtual form.
func ToVirtual(rel string) string {
	return DocsPrefix + filepath.ToSlash(filepath.Clean(rel))
}

// embeddedPath maps a virtual or bare path to its location inside the embedded
// FS (i.e. prefixed with contentRoot).
func embeddedPath(path string) string {
	return contentRoot + "/" + filepath.ToSlash(filepath.Clean(RelFromVirtual(path)))
}

// ReadFile returns the raw bytes of an embedded doc, addressing it by either its
// virtual path ("phosphor://docs/<rel>") or its docs-relative path ("<rel>").
// The second return is false when no such doc is shipped.
func ReadFile(path string) ([]byte, bool) {
	data, err := fs.ReadFile(contentFS, embeddedPath(path))
	if err != nil {
		return nil, false
	}
	return data, true
}

// Exists reports whether an embedded doc is present at the given path.
func Exists(path string) bool {
	_, ok := ReadFile(path)
	return ok
}

// Doc describes a single shipped documentation file.
type Doc struct {
	// RelPath is the path relative to the docs root, e.g. "commands/GOAL.md".
	RelPath string
	// VirtualPath is the canonical addressing form, e.g.
	// "phosphor://docs/commands/GOAL.md".
	VirtualPath string
	// Title is the first markdown heading, falling back to the base name.
	Title string
}

// List returns every shipped doc, sorted by path.
func List() []Doc {
	var docs []Doc
	fs.WalkDir(contentFS, contentRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(contentRoot, filepath.FromSlash(p))
		rel = filepath.ToSlash(rel)
		docs = append(docs, Doc{
			RelPath:     rel,
			VirtualPath: ToVirtual(rel),
		})
		return nil
	})
	sortDocs(docs)
	for i := range docs {
		docs[i].Title = titleOf(docs[i].RelPath)
	}
	return docs
}

// titleOf derives a human title from the file's first markdown H1, falling back
// to its base name without the extension.
func titleOf(rel string) string {
	data, ok := ReadFile(rel)
	if !ok {
		return filepath.Base(rel)
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			if title := strings.TrimSpace(trimmed[2:]); title != "" {
				return title
			}
		}
	}
	return strings.TrimSuffix(filepath.Base(rel), ".md")
}
