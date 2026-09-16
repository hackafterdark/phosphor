package saferegex

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// classic catastrophic-backtracking shapes must all be rejected.
func TestCheck_RejectsNestedRepetition(t *testing.T) {
	t.Parallel()

	unsafe := []string{
		`(a+)+`,
		`(a*)*`,
		`(a+)*`,
		`(a*)+`,
		`((a+)+)*`,
		`(x+x+)*b?`,
		`([A-Za-z]+)*[A-Za-z]+=([A-Za-z]+)*`,
		`(.*)*.*?`,
		`(.*)(.*)+`,
		`(?:[A-Za-z]+[\.\-]?)*[A-Za-z]+`, // SQL identifier idiom: nested unbounded loop.
		`([A-Za-z0-9]+[-_.]?)+`,          // same family, separated loop is still unbounded-inside-unbounded.
		`(?:a|aa|b?)*`,
	}
	for _, pattern := range unsafe {
		err := Check(pattern)
		require.Error(t, err, "expected rejection for %q", pattern)
		require.Contains(t, err.Error(), "ReDoS", "expected a ReDoS message for %q", pattern)
	}
}

// ambiguous alternations under an unbounded quantifier are the second
// classic family: exponential parsings on a backtracker.
func TestCheck_RejectsAmbiguousAlternation(t *testing.T) {
	t.Parallel()

	unsafe := []string{
		`(a|aa)+`,
		`(?:a|aa)+`,
		`(a|aa|b)+`,
		`(a|aa|aaa)*`,
		`(a|\w\w)+`,
		`(\w\w|\d)+`,
		`(w|ww)+`,
		`(ab|abab)+`,
		`(?:https?|httpshttps)+`,
		`(?:[0-9]{2}|[0-9])+`,
		`(?i)(abc|ABCabc)+`,
	}
	for _, pattern := range unsafe {
		err := Check(pattern)
		require.Error(t, err, "expected rejection for %q", pattern)
		require.Contains(t, err.Error(), "ReDoS", "expected a ReDoS message for %q", pattern)
	}
}

// patterns that only differ by case must be judged fold-sensitively:
// without the i flag the branches are disjoint and legitimate.
// the fold comparison in the ambiguity test must follow the i flag:
// multi-character branches only collide case-insensitively when the flag
// is actually in effect at that position. (Fold-equivalent single-rune
// branches are merged away by the parser itself, so no alternation
// survives there.)
func TestCheck_CaseFoldIsFlagScoped(t *testing.T) {
	t.Parallel()

	require.NoError(t, Check(`(abc|ABCabc)+`))
	require.Error(t, Check(`(?i)(abc|ABCabc)+`))
	require.NoError(t, Check(`(?i)(ab|AB)+`))
}

// the shapes that must pass: everything shipped-style in secret rules,
// allowlists, and tool-name matchers.
func TestCheck_AcceptsLegitimatesPatterns(t *testing.T) {
	t.Parallel()

	safe := []string{
		``,
		`^bash$`,
		`(^|/)docs/.*\.md$`,
		`\.env`,
		`[A-Za-z0-9/+=]{40}`,
		`AKIA[0-9A-F]{16}`,
		`(?i)api[_-]?key\s*[:=]\s*['"][A-Za-z0-9_\-]{20,}['"]`,
		`PHOSPHOR_TEST_TOKEN[=\s]+["']?([A-Z0-9]{16})["']?`,
		`\bacme-(?:prod|stg)-([a-f0-9]{32})\b`,
		`\b\d{3}-\d{2}-\d{4}\b`,
		`\b(?:\d[ \-]?){13,23}\d\b`,
		`(?:https?|ftp)+`,
		`(a|b)+`,
		`(ab|cd)*`,
		`(a+)b+`,
		`(a*){5}`,
		`(a+){5,9}`,
		`(a|ab|abc)`,                      // ambiguous but not under a quantifier.
		`(a|aa)`,                          // ditto.
		`(?:foo|bar|_)+`,                  // overlapping-looking but disjoint reps.
		`(a|\w)+`,                         // the parser folds the subsumed branch; a single class remains.
		`(\d|\w)+`,                        // ditto.
		`[A-Za-z0-9._/-]+@[\-A-Za-z0-9]*`, // email-ish.
		`[A-Za-z]+[\.\-]?[A-Za-z]+`,       // sibling quantifiers, no nesting.
		`(?:(?:dev|stg)-[0-9a-f]{8}\.[0-9a-f]{32})`,
		`(?i)-----BEGIN [A-Z ]+ PRIVATE KEY-----`,
		`\b(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\b`,
		`\b(?:(?:[0-9A-Fa-f]{1,4}:){7}[0-9A-Fa-f]{1,4}|(?:[0-9A-Fa-f]{1,4}:){1,6}:[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{1,4}){0,6}|::(?:[0-9A-Fa-f]{1,4}:){0,6}[0-9A-Fa-f]{1,4})\b`,
	}
	for _, pattern := range safe {
		require.NoError(t, Check(pattern), "expected acceptance for %q", pattern)
	}
}

// a pattern whose enumeration budget overflows is left unjudged rather
// than rejected: breadth must never turn into false positives.
func TestCheck_AbstainsOnOverbudgetEnumeration(t *testing.T) {
	t.Parallel()

	branches := make([]string, 0, 120)
	for i := range 120 {
		branches = append(branches, string(rune('a'+i)))
	}
	pattern := `(?:` + strings.Join(branches, "|") + `)+`
	require.NoError(t, Check(pattern))
}

func TestCheck_RejectsResourceAbuses(t *testing.T) {
	t.Parallel()

	require.Error(t, Check(strings.Repeat("(a)", 2000)), "oversized pattern")
	require.Error(t, Check(`(ab){1001}`), "oversized repetition bound")
	require.Error(t, Check(`(?:a{30}){30}{30}`), "nested bounded expansion")
}

func TestCheck_RejectsInvalidSyntax(t *testing.T) {
	t.Parallel()

	err := Check(`(a+`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid regex")
}

func TestCompile(t *testing.T) {
	t.Parallel()

	re, err := Compile(`PHOSPHOR_[A-Z0-9]{8}`)
	require.NoError(t, err)
	require.True(t, re.MatchString("x PHOSPHOR_ABCDEFGH y"))

	require.NotPanics(t, func() {
		re, err := Compile(`(a+)+`)
		require.Error(t, err)
		require.Nil(t, re)
	})

	require.True(t, IsSafe(`foo`))
	require.False(t, IsSafe(`(a*)*`))
}
