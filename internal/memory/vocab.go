package memory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The project vocabulary is the shared tag vocabulary the §2 tags table
// canonicalizes against (kind='vocab'). It is seeded from files that state what
// the project calls things — module names, task names, directory names, doc
// headings — so write-time keyword enrichment matches project reality rather
// than inventing terms per entry.

// vocabCap bounds the seeded vocabulary so a huge repository cannot balloon the
// tags table; the terms that matter for recall come from the highest-signal
// sources, which are scanned first.
const vocabCap = 512

// SeedVocabulary fills the project bank's vocabulary from the workspace and
// reports how many vocab terms the index now holds. Seeding is additive and
// idempotent: an already-seeded bank short-circuits on a single count query.
func (s *Store) SeedVocabulary(ctx context.Context, workspaceDir string) (int, error) {
	if workspaceDir == "" || s.project == nil {
		return 0, nil
	}
	b := s.project
	have, err := s.count(b, "SELECT COUNT(*) FROM tags WHERE kind = 'vocab'")
	if err != nil {
		return 0, err
	}
	if have > 0 {
		return have, nil
	}
	terms := ProjectVocabulary(workspaceDir)
	if len(terms) == 0 {
		return 0, nil
	}
	if err := s.tx(ctx, b, func(tx *sql.Tx) error {
		for _, term := range terms {
			if _, err := tx.ExecContext(ctx, "INSERT INTO tags(term, kind) VALUES(?, 'vocab') ON CONFLICT(term) DO NOTHING", term); err != nil {
				return fmt.Errorf("seed vocab term %q: %w", term, err)
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return s.count(b, "SELECT COUNT(*) FROM tags WHERE kind = 'vocab'")
}

// ProjectVocabulary collects the candidate terms for a workspace, highest-signal
// source first, already deduplicated and capped.
func ProjectVocabulary(workspaceDir string) []string {
	set := make(map[string]bool, 128)
	out := make([]string, 0, 128)
	add := func(term string) {
		term = normalizeVocabTerm(term)
		if term == "" || set[term] {
			return
		}
		set[term] = true
		out = append(out, term)
	}
	addGoModTerms(workspaceDir, add)
	addTaskfileTerms(workspaceDir, add)
	addDirTerms(workspaceDir, add)
	addDocTerms(workspaceDir, add)
	if len(out) > vocabCap {
		out = out[:vocabCap]
	}
	return out
}

func addGoModTerms(dir string, add func(string)) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return
	}
	inRequireBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		before, _ := strings.CutPrefix(line, "//")
		line = strings.TrimSpace(before)
		switch fields := strings.Fields(line); {
		case len(fields) == 0:
		case fields[0] == "module" && len(fields) > 1:
			add(moduleBase(fields[1]))
		case strings.HasPrefix(line, "require ("):
			inRequireBlock = true
		case inRequireBlock && fields[0] == ")":
			inRequireBlock = false
		case fields[0] == "require" && len(fields) > 1:
			add(moduleBase(fields[1]))
		case inRequireBlock:
			add(moduleBase(fields[0]))
		}
	}
}

func addTaskfileTerms(dir string, add func(string)) {
	data, err := os.ReadFile(filepath.Join(dir, "Taskfile"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if groups := taskQuotedRe.FindStringSubmatch(line); groups != nil {
			if len(groups) > 1 {
				add(groups[1])
			}
			continue
		}
		if groups := taskColumnRe.FindStringSubmatch(line); groups != nil && len(groups) > 1 {
			add(groups[1])
		}
	}
}

func addDirTerms(dir string, add func(string)) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		if skipVocabDirs[strings.ToLower(name)] {
			continue
		}
		if info, err := entry.Info(); err == nil && info.IsDir() {
			add(name)
		}
	}
}

func addDocTerms(dir string, add func(string)) {
	for _, name := range []string{"AGENTS.md", "AGENTS.local.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			heading := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
			for _, word := range Tokenize(heading, 0) {
				add(word)
			}
		}
	}
}

var (
	// taskQuotedRe catches - task: "name" and task: 'name' YAML forms.
	taskQuotedRe = regexp.MustCompile(`^\s*(?:-\s+)?task\s*:\s*['"]([^'"]+)['"]`)
	// taskColumnRe catches bare `name:` task columns, optionally quoted.
	taskColumnRe = regexp.MustCompile(`^\s*['"]?([A-Za-z_][A-Za-z0-9_.:-]+)['"]?\s*:(?:\s|$)`)
	// taskYAMLKeys are go-task vocabulary words that are structure, never targets.
	taskYAMLKeys = map[string]bool{
		"version": true, "vars": true, "env": true, "tasks": true, "cmds": true,
		"cmd": true, "desc": true, "deps": true, "platforms": true, "ignore_error": true,
		"silent": true, "sources": true, "generates": true, "status": true, "preconditions": true,
	}
	// skipVocabDirs are the top-level directories that never carry project vocabulary.
	skipVocabDirs = map[string]bool{
		"node_modules": true, "vendor": true, "dist": true, "bin": true,
		"testdata": true, "target": true,
	}
	// majorRe matches a module path's /vN major suffix segment.
	majorRe = regexp.MustCompile(`^v\d+$`)
)

// moduleBase reduces a module path to its meaningful last segment, dropping a
// /vN major suffix so github.com/tsawler/prose/v3 seeds "prose", not "v3".
func moduleBase(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if n := len(segs); n > 1 && majorRe.MatchString(segs[n-1]) {
		return segs[n-2]
	}
	return segs[len(segs)-1]
}

// normalizeVocabTerm keeps a term only when it is a clean lowercase word worth
// matching: long enough to carry meaning, made of word characters, not filler.
func normalizeVocabTerm(term string) string {
	term = strings.ToLower(strings.Trim(term, "'\"`.,;:!?·—-_[]{}"))
	if len(term) < 3 || commonStopwords[term] || taskYAMLKeys[term] {
		return ""
	}
	for i := 0; i < len(term); i++ {
		c := term[i]
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '_' && c != '-' && c != '.' && c != ':' {
			return ""
		}
	}
	return term
}
