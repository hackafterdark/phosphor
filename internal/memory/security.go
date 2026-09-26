package memory

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/hackafterdark/phosphor/pkg/security/externalcontent"
	gl "github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/report"
)

// Memory is the highest-privilege injection surface in the product: its content
// is replayed into the system prompt of every future session, so a poisoned entry
// persists until somebody notices it. Everything that enters or leaves the vault
// therefore passes the same gates that web and MCP content pass, plus a secret
// scan on the way in.

// detectorAPI is the slice of the gitleaks detector this package needs, kept as an
// interface so the construction cost stays behind one lazy value and tests can stub
// it.
type detectorAPI interface {
	DetectString(content string) []report.Finding
}

var detector = sync.OnceValues(newDetector)

func newDetector() (detectorAPI, error) {
	d, err := gl.NewDetectorDefaultConfig()
	if err != nil {
		return nil, err
	}
	return d, nil
}

// sanitize defangs ChatML-class control tokens and homoglyph smuggling. It is
// idempotent and inert on clean text, and it runs on every read from disk as well
// as every write, because humans, Obsidian plugins, and vault sync all write these
// files too.
func sanitize(s string) string {
	if s == "" {
		return s
	}
	return externalcontent.Sanitize(s)
}

// Sanitize is the exported form for the prompt-injection path, where the block is
// assembled from rows that may have been re-read from disk.
func Sanitize(s string) string { return sanitize(s) }

// sanitizedStamp is the §9 audit stamp: the digest of the defanged projection of
// the entry text, which is what the render path injects after sanitize over
// oneLine. Stamping the *projection* rather than the raw bytes is the point — the
// invariant fsck checks is about what would reach the model, and folding the
// defanger into the hash means a file that still carries a live control token
// can never match the stamp its row claims.
func sanitizedStamp(body, notes string) string {
	return HashString(sanitize(strings.TrimSpace(body)) + "\x00" + sanitize(strings.TrimSpace(notes)))
}

// maxSourceBytes bounds the provenance string. It rides into the injected
// ⟨…⟩ badge and the provenance card as one short line, so it is capped well
// below any plausible citation rather than sized to a novel.
const maxSourceBytes = 240

// ValidateSource is the write-time gate for the provenance string. Source is a
// system-owned field (§3), but it is model-supplied on the tool path and a
// human can hand-edit it in Obsidian, and it renders verbatim into the
// system-prompt badge — so the same defang-and-grammar check the body passes
// must cover it too. It returns the defanged, single-line form the vault should
// store, or an error naming why the value is unusable.
func ValidateSource(s string) (string, error) {
	// Fields collapses all whitespace runs, which is also what removes the
	// newlines a hand-edited YAML block scalar could smuggle into the badge.
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "", nil
	}
	if len(s) > maxSourceBytes {
		return "", fmt.Errorf("memory provenance is too long (%d bytes; the limit is %d)", len(s), maxSourceBytes)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("memory provenance was rejected: it contains control character %U", r)
		}
	}
	if hits := injectionPattern.FindAllString(sanitize(s), -1); len(hits) > 0 {
		return "", fmt.Errorf("memory provenance was rejected: it still contained a model control token (%s) after defanging", firstOf(hits))
	}
	return sanitize(s), nil
}

// CheckContent is the write-time gate: control tokens and credentials must not be
// able to enter the vault. The matched secret is never echoed back, only its rule
// identity, so a blocked write cannot leak the credential into the transcript.
func CheckContent(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if hits := injectionPattern.FindAllString(sanitize(text), -1); len(hits) > 0 {
		return fmt.Errorf("memory entry rejected: it still contained a model control token (%s) after defanging", firstOf(hits))
	}
	d, err := detector()
	if err != nil {
		// The detector parses a tested embedded ruleset and only fails if that parse
		// fails. Fail closed here rather than let an unscanned credential become a
		// permanent part of the system prompt.
		return fmt.Errorf("memory entry rejected: the credential scanner is unavailable (%v)", err)
	}
	findings := d.DetectString(text)
	if len(findings) == 0 {
		return nil
	}
	f := findings[0]
	label := strings.TrimSpace(f.Description)
	if label == "" {
		label = f.RuleID
	}
	msg := fmt.Sprintf("memory entry rejected: it looks like it contains a credential (%s", f.RuleID)
	if label != "" {
		msg += ": " + label
	}
	if f.StartLine > 0 {
		msg += fmt.Sprintf(" near line %d", f.StartLine)
	}
	if len(findings) > 1 {
		msg += fmt.Sprintf(", %d findings total", len(findings))
	}
	return fmt.Errorf("%s). Memory feeds the system prompt of every later session, so credentials cannot be stored here.", msg)
}

// injectionPattern is the strict threat grammar for text that is about to become
// a permanent part of the system turn: role-forging, tool-call forgery, and
// instruction-override phrasings.
var injectionPattern = regexp.MustCompile(
	`(?i)(?:` +
		`<\|[^|>]{0,40}\|>` + // leftover control tokens
		`|\[im_(?:start|end)\]` +
		`|</?\s*(?:system|assistant|user)\s*>` +
		`|\[\s*(?:call|call_end|endoftext)\s*\]` +
		`|you\s+(?:are|now)\s+(?:no\s+longer\s+)?(?:not\s+)?(?:a\s+)?(?:helpful\s+)?(?:ai|assistant|model)` +
		`|ignore\s+(?:all\s+|any\s+|the\s+|previous\s+|prior\s+|above\s+)?(?:previous\s+)?(?:instructions?|rules?|prompts?)` +
		`|disregard\s+(?:the\s+|all\s+)?(?:prior|previous|above)\s+instructions?` +
		`|new\s+system\s+prompt` +
		`|developer\s+mode` +
		`)`,
)

func firstOf(hits []string) string {
	if len(hits) == 0 {
		return ""
	}
	return strings.TrimSpace(hits[0])
}
