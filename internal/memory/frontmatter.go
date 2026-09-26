package memory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// notesHeading is the dedicated human region inside an entry body. The agent
// appends below its own marker inside this region and never rewrites what a
// human wrote above it.
const notesHeading = "## Notes"

const (
	maxVaultPathLength = 240
	maxFileNameLength  = 96
	minFileNameLength  = 16
	maxIDThreadLength  = 24
	maxIDSummaryLength = 48
	idHashSuffixLength = 8
)

// ParseError marks a file whose frontmatter could not be parsed. The indexer
// quarantines such files instead of failing, because a git conflict marker or a
// plugin-written YAML defect must not be able to take the whole index down.
type ParseError struct {
	Path string
	Err  error
}

func (e *ParseError) Error() string { return fmt.Sprintf("parse frontmatter %s: %v", e.Path, e.Err) }

func (e *ParseError) Unwrap() error { return e.Err }

// ParseEntry decodes a vault file into an Entry. A file whose frontmatter is
// unparseable returns a *ParseError so the caller can quarantine it rather than
// index a corrupt row or inject garbage into the system prompt.
func ParseEntry(path string) (Entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, fmt.Errorf("read entry: %w", err)
	}
	entry, err := ParseEntryBytes(path, raw)
	if err != nil {
		return Entry{}, err
	}
	entry.Path = filepath.ToSlash(path)
	entry.ContentHash = HashBytes(raw)
	return entry, nil
}

// ParseEntryBytes decodes the bytes of a vault file. Splitting on the leading
// `---` fence keeps the body byte-exact, which is what makes provenance
// citations stable.
func ParseEntryBytes(path string, raw []byte) (Entry, error) {
	var entry Entry
	text := string(raw)
	text = strings.TrimPrefix(text, "")
	if !strings.HasPrefix(text, "---") {
		return Entry{}, &ParseError{Path: path, Err: fmt.Errorf("missing frontmatter fence")}
	}
	rest := strings.TrimLeft(strings.TrimPrefix(text, "---"), "\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Entry{}, &ParseError{Path: path, Err: fmt.Errorf("unterminated frontmatter block")}
	}
	head := rest[:end]
	body := strings.TrimLeft(rest[end+len("\n---"):], "-\r\n")

	if err := yaml.Unmarshal([]byte(head), &entry); err != nil {
		return Entry{}, &ParseError{Path: path, Err: err}
	}
	// A human can delete the fence while editing in Obsidian; that is drift, not
	// a fatal condition, so the file is still readable but never injected.
	if LooksLikeConflict(head) {
		return Entry{}, &ParseError{Path: path, Err: fmt.Errorf("merge-conflict markers in frontmatter")}
	}

	entry.Body, entry.Notes = splitNotes(body)
	if entry.ID == "" {
		return Entry{}, &ParseError{Path: path, Err: fmt.Errorf("entry has no id")}
	}
	entry.Type = NormalType(string(entry.Type))
	if entry.Status == "" {
		entry.Status = StatusActive
	}
	if entry.Owner == "" {
		entry.Owner = OwnerAgent
	}
	if entry.Trust == 0 {
		entry.Trust = 0.5
	}
	return entry, nil
}

// splitNotes separates the agent-authored body from the human `## Notes`
// region so a region merge can keep both voices instead of truncating one.
func splitNotes(body string) (string, string) {
	idx := strings.Index(body, "\n"+notesHeading)
	if idx < 0 {
		if !strings.HasPrefix(strings.TrimSpace(body), notesHeading) {
			return strings.TrimSpace(body), ""
		}
		return "", strings.TrimSpace(body)
	}
	return strings.TrimSpace(body[:idx]), strings.TrimSpace(body[idx+len("\n"+notesHeading):])
}

// Serialize renders an entry back to vault-file bytes. Field order is stable so
// a rewrite that changes one value produces a one-line git diff.
func Serialize(e Entry) ([]byte, error) {
	// System-owned numbers and timestamps are written out explicitly rather than
	// relying on yaml's zero handling, so an unset field stays absent instead of
	// appearing as `phosphor.trust: 0`.
	type file struct {
		ID           string   `yaml:"id"`
		Type         Type     `yaml:"type"`
		Thread       string   `yaml:"thread,omitempty"`
		Summary      string   `yaml:"summary,omitempty"`
		Title        string   `yaml:"title,omitempty"`
		Tags         []string `yaml:"tags,omitempty"`
		Links        []string `yaml:"links,omitempty"`
		Source       string   `yaml:"source,omitempty"`
		Expires      string   `yaml:"expires,omitempty"`
		Status       Status   `yaml:"phosphor.status"`
		Trust        float64  `yaml:"phosphor.trust"`
		Pinned       bool     `yaml:"phosphor.pinned,omitempty"`
		Owner        Owner    `yaml:"phosphor.owner"`
		Asserted     *bool    `yaml:"phosphor.asserted,omitempty"`
		RecallCount  int      `yaml:"phosphor.recall_count,omitempty"`
		HelpfulCount int      `yaml:"phosphor.helpful_count,omitempty"`
		Created      string   `yaml:"phosphor.created,omitempty"`
		Updated      string   `yaml:"phosphor.updated,omitempty"`
		LastUsed     string   `yaml:"phosphor.last_used,omitempty"`
		Supersedes   string   `yaml:"phosphor.supersedes,omitempty"`
		FromDecision string   `yaml:"phosphor.from_decision,omitempty"`
		Mac          string   `yaml:"phosphor.mac,omitempty"`
		Sanitized    string   `yaml:"phosphor.sanitized,omitempty"`
	}
	f := file{
		ID:           e.ID,
		Type:         e.Type,
		Thread:       e.Thread,
		Summary:      e.Summary,
		Title:        e.Title,
		Tags:         e.Tags,
		Links:        e.Links,
		Source:       e.Source,
		Expires:      e.Expires,
		Status:       e.Status,
		Trust:        e.Trust,
		Pinned:       e.Pinned,
		Owner:        e.Owner,
		Asserted:     e.Asserted,
		RecallCount:  e.RecallCount,
		HelpfulCount: e.HelpfulCount,
		Created:      e.Created,
		Updated:      e.Updated,
		LastUsed:     e.LastUsed,
		Supersedes:   e.Supersedes,
		FromDecision: e.FromDecision,
		Mac:          e.Mac,
		Sanitized:    e.Sanitized,
	}
	head, err := yaml.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(strings.TrimRight(string(head), "\n"))
	sb.WriteString("\n---\n\n")
	if b := strings.TrimSpace(e.Body); b != "" {
		sb.WriteString(b)
		sb.WriteString("\n")
	}
	if n := strings.TrimSpace(e.Notes); n != "" {
		sb.WriteString("\n" + notesHeading + "\n")
		sb.WriteString(n)
		sb.WriteString("\n")
	}
	return []byte(sb.String()), nil
}

func EntryPath(vaultDir, id string) string {
	entriesDir := filepath.Join(vaultDir, EntriesDirName)
	full := rawSlug(id)
	limit := maxFileNameLength
	available := maxVaultPathLength - len(entriesDir) - 1 - len(".md")
	if len(full) > limit || len(full) > available {
		if available < limit {
			limit = available
		}
		if limit < minFileNameLength {
			limit = minFileNameLength
		}
	}

	slug := boundedSlug(full, limit)
	if slug == "" {
		slug = "entry"
	}
	return filepath.Join(entriesDir, slug+".md")
}

// WriteEntry atomically writes an entry file. The write is a whole-file
// replacement of a file this tool owns, and the previous bytes are kept as a
// `.bak` when the existing file will not round-trip, which is what makes an
// agent rewrite safe next to a human editing the same vault in Obsidian.
func WriteEntry(path string, e Entry) error {
	out, err := Serialize(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create entries dir: %w", err)
	}
	if existing, readErr := os.ReadFile(path); readErr == nil {
		round, rerr := ParseEntryBytes(path, existing)
		if rerr != nil || (round.Body != strings.TrimSpace(e.Body) && round.Notes != strings.TrimSpace(e.Notes)) {
			backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
			if werr := os.WriteFile(backup, existing, 0o644); werr == nil {
				slog.Warn("Memory entry did not round-trip; preserved previous bytes",
					"path", filepath.ToSlash(path), "backup", filepath.ToSlash(backup))
			}
		}
	}
	// Stage under a uniquely named temp file in the vault root rather than next
	// to the target: entries live under a long slug, and the extra characters
	// would push the name over the per-component limit on Windows.
	stageDir := filepath.Dir(path)
	tmp, err := os.CreateTemp(stageDir, "mem-*.tmp")
	if err != nil {
		return fmt.Errorf("create staging file: %w", err)
	}
	stage := tmp.Name()
	_, werr := tmp.Write(out)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(stage)
		return fmt.Errorf("write entry: %v", errors.Join(werr, cerr))
	}
	if err := os.Rename(stage, path); err != nil {
		_ = os.Remove(stage)
		return fmt.Errorf("rename entry: %w", err)
	}
	return nil
}

// ReadEntry loads an entry from the vault and stamps its scope.
func ReadEntry(path string, scope Scope) (Entry, error) {
	e, err := ParseEntry(path)
	if err != nil {
		return Entry{}, err
	}
	e.Scope = scope
	return e, nil
}

// HashBytes is the content hash used for change detection, mirroring the way
// the workspace index decides whether a file needs re-indexing.
func HashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// HashString hashes a string.
func HashString(s string) string { return HashBytes([]byte(s)) }

// LooksLikeConflict detects the merge markers that make a frontmatter block
// both invalid YAML and dangerous to inject.
func LooksLikeConflict(head string) bool {
	for _, marker := range []string{"<<<<<<<", "=======", ">>>>>>>"} {
		if strings.HasPrefix(strings.TrimSpace(head), marker) ||
			strings.Contains(head, "\n"+marker) ||
			strings.Contains(head, marker+"\n") {
			return true
		}
	}
	return false
}

// NewID mints an entry id. Identity is this value and never the file path, so
// a rename or move in Obsidian cannot break links or provenance.
func NewID(thread, summary string) string {
	base := rawSlug(strings.TrimSpace(thread))
	if base == "" {
		base = "entry"
	}
	base = boundedSlug(base, maxIDThreadLength)

	slug := rawSlug(strings.TrimSpace(summary))
	if slug == "" {
		slug = randomSlugToken()
	}
	slug = boundedSlug(slug, maxIDSummaryLength)
	return fmt.Sprintf("%s-%s-%s", time.Now().UTC().Format("2006-01-02"), base, slug)
}

// weakDisplayWords are the words a display label must not end on after a cut:
// prepositions and articles read as truncation, so the cut steps back over them
// until the label ends on a word that carries meaning.
var weakDisplayWords = map[string]bool{
	"the": true, "a": true, "an": true, "in": true, "of": true, "to": true,
	"on": true, "at": true, "by": true, "for": true, "with": true, "and": true,
	"or": true, "is": true, "are": true,
}

// deriveTitle mints the human display label from a summary: the leading
// clause, cut at a word boundary so the label reads whole. It is only ever
// called when an entry is written without a title, which keeps the generated
// label from overwriting one a person set in Obsidian.
func deriveTitle(summary string) string {
	words := strings.Fields(strings.TrimSpace(summary))
	if len(words) == 0 {
		return ""
	}
	if len(words) > 8 {
		words = words[:8]
	}
	label := strings.Join(words, " ")
	const maxBytes = 48
	if len(label) > maxBytes {
		if i := strings.LastIndexByte(label[:maxBytes], ' '); i > 0 {
			label = label[:i]
		} else {
			label = label[:maxBytes]
			for len(label) > 0 && !utf8.Valid([]byte(label)) {
				label = label[:len(label)-1]
			}
		}
	}
	for {
		tail := label
		if i := strings.LastIndexByte(label, ' '); i >= 0 {
			if i == 0 {
				break
			}
			tail = label[i+1:]
			if !weakDisplayWords[strings.ToLower(tail)] {
				break
			}
			label = label[:i]
			continue
		}
		break
	}
	return strings.TrimRight(label, " ,;:.!?")
}

// Slug turns arbitrary text into a bounded filename-safe token.
func Slug(s string) string {
	return boundedSlug(rawSlug(s), maxFileNameLength)
}

func rawSlug(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	prevDash := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
			prevDash = false
		case !prevDash:
			sb.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(sb.String(), "-")
}

func boundedSlug(slug string, max int) string {
	return boundedSlugWithHash(slug, max, HashString(slug)[:idHashSuffixLength])
}

func boundedSlugWithHash(slug string, max int, hash string) string {
	if slug == "" || len(slug) <= max {
		return slug
	}
	if max <= 0 {
		return ""
	}
	if max <= idHashSuffixLength {
		if len(hash) < max {
			return hash
		}
		return hash[:max]
	}

	cut := max - idHashSuffixLength - 1
	prefix := slug[:cut]
	if i := strings.LastIndexByte(prefix, '-'); i > 0 {
		prefix = prefix[:i]
	}
	prefix = strings.Trim(prefix, "-")
	if prefix == "" {
		return hash
	}
	return prefix + "-" + hash
}

func randomSlugToken() string {
	buf := make([]byte, idHashSuffixLength/2)
	if _, err := rand.Read(buf); err != nil {
		return HashString(time.Now().String())[:idHashSuffixLength]
	}
	return hex.EncodeToString(buf)
}

// CanonicalTag is the deterministic normalizer for tags: case fold, strip
// punctuation, and a rules-based singularize. It only ever folds an *exact*
// match. Fuzzy similarity is never auto-merged here, because silently fusing
// `task` and `mask` would corrupt the tag graph and the provenance that hangs
// off it.
func CanonicalTag(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	t = strings.Map(func(r rune) rune {
		switch r {
		case '_', ' ', '/', '-':
			return '-'
		}
		return r
	}, t)
	for strings.Contains(t, "--") {
		t = strings.ReplaceAll(t, "--", "-")
	}
	t = strings.Trim(t, "-")
	t = singularize(t)
	if len(t) > 48 {
		t = t[:48]
	}
	return t
}

// singularize applies the handful of spelling rules that are safe to apply
// without a dictionary. Irregular plurals are deliberately left alone.
func singularize(word string) string {
	switch {
	case word == "", word == "is", word == "was", word == "has":
		return word
	case strings.HasSuffix(word, "ies") && len(word) > 4:
		return strings.TrimSuffix(word, "ies") + "y"
	case strings.HasSuffix(word, "ses"), strings.HasSuffix(word, "xes"), strings.HasSuffix(word, "zes"),
		strings.HasSuffix(word, "ches"), strings.HasSuffix(word, "shes"):
		return strings.TrimSuffix(word, "es")
	case strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") && !strings.HasSuffix(word, "us") && len(word) > 3:
		return strings.TrimSuffix(word, "s")
	}
	return word
}

// commonStopwords is the filler the keyword paths drop so BM25 scoring and tag
// lists are not diluted by grammar words. Shared by Tokenize and the vocabulary
// seeder so the two never disagree about what is noise.
var commonStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "that": true, "with": true, "this": true,
	"from": true, "they": true, "their": true, "them": true, "then": true, "than": true,
	"when": true, "where": true, "which": true, "while": true, "what": true, "about": true,
	"into": true, "its": true, "are": true, "was": true, "were": true, "have": true,
	"has": true, "had": true, "not": true, "but": true, "you": true, "your": true,
	"should": true, "would": true, "could": true, "because": true, "does": true, "did": true,
	"use": true, "using": true, "used": true, "we": true, "our": true, "will": true,
}

// Tokenize splits text into lowercase keyword candidates. It is the cheap
// stand-in for the prose/tokenize path: no model, no dependency, deterministic.
// Stopwords are trimmed so BM25 scoring is not diluted by filler.
func Tokenize(text string, limit int) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' && r != '.'
	})
	stop := commonStopwords
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		w := strings.ToLower(strings.Trim(f, "-.'\""))
		if len(w) < 3 || stop[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// TokenOverlap is the Jaccard-style similarity used for dedup at write time:
// two entries that share most of their keywords are probably the same fact.
func TokenOverlap(a, b string) float64 {
	as, bs := Tokenize(a, 0), Tokenize(b, 0)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	set := make(map[string]bool, len(as))
	for _, t := range as {
		set[t] = true
	}
	shared := 0
	total := make(map[string]bool, len(as)+len(bs))
	for _, t := range as {
		total[t] = true
	}
	for _, t := range bs {
		total[t] = true
		if set[t] {
			shared++
		}
	}
	return float64(shared) / float64(len(total))
}
