// Package saferegex provides a static ReDoS (Regular Expression
// Denial-of-Service) analyzer for regex patterns that arrive from operator
// or workspace configuration: custom secret rules, hook matchers, and log
// filters.
//
// Phosphor compiles every regex with the stdlib engine, which is RE2-based
// and therefore immune to catastrophic *backtracking*. A user-supplied
// pattern can still hurt, though: the classic nested-repetition and
// ambiguous-alternation shapes ((a+)+, (a|aa)+, (a|\w)+ ...) blow up the
// automaton's per-input cost polynomially, are the exact shapes a hostile
// workspace file would embed to DoS the scanner while it processes
// attacker-influenced bytes, and become outright fatal if such a pattern
// ever escapes to a backtracking engine (another tool, a future exporter).
// Check rejects those shapes statically so they fail at config-load time
// with a clear message instead of hanging a scan at run time.
//
// The analysis walks the regexp/syntax AST and rejects:
//
//   - unbounded quantifiers nested inside unbounded quantifiers,
//   - alternations under an unbounded quantifier where two branches can
//     match the same text (e.g. (a|\w)+) or one branch is an integer
//     repetition of another (e.g. (a|aa)+),
//   - repetition bounds beyond MaxRepeatBound,
//   - bounded-repetition expansions whose estimated automaton size exceeds
//     MaxRepeatExpansion,
//   - patterns larger than MaxPatternLen or with more than MaxASTNodes
//     syntax nodes.
//
// It is a conservative heuristic, not a prover: it under-approximates the
// dangerous set (a pattern it allows may still be slow), and it can reject
// exotic patterns that are technically safe. Legitimate secret-detection,
// path-matching, and tool-name-matcher patterns are shallow enough to pass.
package saferegex

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode"
)

const (
	// MaxPatternLen is the largest pattern length accepted.
	MaxPatternLen = 4096
	// MaxASTNodes is the largest number of syntax nodes accepted.
	MaxASTNodes = 20_000
	// MaxRepeatBound is the largest explicit {n,m} repetition bound accepted.
	// The Go parser itself refuses counted forms above 1000; this mirrors that
	// limit so the intent is explicit and survives an engine swap.
	MaxRepeatBound = 1000
	// MaxRepeatExpansion is the estimated automaton budget along a chain of
	// bounded repetitions (product of bounds and body lengths).
	MaxRepeatExpansion = 100_000

	// maxEnumeratedPaths caps the branch enumeration used for the ambiguity
	// test; a body that overflows it is left unjudged rather than rejected.
	maxEnumeratedPaths = 96
	// maxRepresentativeLen caps a single enumerated representative string.
	maxRepresentativeLen = 32
)

// Check statically analyses a user-supplied pattern and returns a non-nil
// error explaining the rejection when it matches a known ReDoS shape or
// exceeds the resource budgets. A nil error means no dangerous shape was
// found, not a proof of safety.
func Check(pattern string) error {
	if len(pattern) > MaxPatternLen {
		return fmt.Errorf("regex rejected (potential ReDoS): pattern is %d bytes, the maximum is %d", len(pattern), MaxPatternLen)
	}
	root, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return fmt.Errorf("invalid regex: %w", err)
	}
	a := &analyzer{}
	if err := a.walk(root, 0, 1); err != nil {
		return fmt.Errorf("regex rejected (potential ReDoS): %s (pattern %.80q)", err, pattern)
	}
	return nil
}

// IsSafe reports whether Check accepts the pattern.
func IsSafe(pattern string) bool {
	return Check(pattern) == nil
}

// Compile checks the pattern with Check and then compiles it with the
// stdlib engine.
func Compile(pattern string) (*regexp.Regexp, error) {
	if err := Check(pattern); err != nil {
		return nil, err
	}
	return regexp.Compile(pattern)
}

type analyzer struct {
	nodes int
}

// walk descends the AST. starDepth counts unbounded quantifiers on the
// current root-to-node path; expansion is the estimated state budget
// produced by the bounded repetitions on that path.
func (a *analyzer) walk(n *syntax.Regexp, starDepth, expansion int) error {
	a.nodes++
	if a.nodes > MaxASTNodes {
		return fmt.Errorf("pattern is too complex (more than %d syntax nodes)", MaxASTNodes)
	}

	switch n.Op {
	case syntax.OpStar, syntax.OpPlus:
		if err := a.checkRepetition(n, starDepth); err != nil {
			return err
		}
		return a.walk(n.Sub[0], starDepth+1, expansion)

	case syntax.OpRepeat:
		if n.Min > MaxRepeatBound || n.Max > MaxRepeatBound {
			return fmt.Errorf("repetition bound %d exceeds the maximum of %d", max(n.Min, n.Max), MaxRepeatBound)
		}
		if n.Max < 0 { // {n,} is an unbounded quantifier.
			if err := a.checkRepetition(n, starDepth); err != nil {
				return err
			}
			return a.walk(n.Sub[0], starDepth+1, expansion)
		}
		mult := max(n.Max, 1)
		if childLen := maxLen(n.Sub[0]); childLen >= 0 {
			mult *= childLen
		}
		if expansion*mult > MaxRepeatExpansion {
			return fmt.Errorf("bounded repetitions expand to more than %d automaton states", MaxRepeatExpansion)
		}
		return a.walk(n.Sub[0], starDepth, expansion*mult)
	}

	for _, child := range n.Sub {
		if err := a.walk(child, starDepth, expansion); err != nil {
			return err
		}
	}
	return nil
}

// checkRepetition validates one unbounded-quantifier node sitting at
// starDepth on the current path.
func (a *analyzer) checkRepetition(n *syntax.Regexp, starDepth int) error {
	if starDepth >= 1 {
		return fmt.Errorf(
			"unbounded quantifier %q wraps a sub-expression that itself contains an unbounded quantifier (the classic nested-repetition shape, e.g. %q)",
			n.Op.String(), "(a+)+",
		)
	}
	return checkAmbiguousBody(n)
}

// checkAmbiguousBody rejects an unbounded repetition whose body is an
// alternation with branches that overlap: two branches matching the same
// text ((a|\w)+) or one branch matching an integer repeat of another
// ((a|aa)+). Both give the matcher exponentially many parsings of one input
// on a backtracking engine and an oversized automaton on RE2.
func checkAmbiguousBody(rep *syntax.Regexp) error {
	body := rep.Sub[0]
	if !hasAlternate(body) {
		return nil
	}
	reps, capped := enumerate(body)
	if capped {
		return nil // Over the enumeration budget: leave it unjudged, no false rejects.
	}
	for _, first := range reps {
		for _, second := range reps {
			if first.node == second.node || first.text == "" || second.text == "" {
				continue
			}
			switch {
			case isClassNode(first.node) && isClassNode(second.node):
				// Two classes that share a rune can both match that rune.
				if classesIntersect(first.node, second.node) {
					return ambiguous(rep)
				}
			case isClassNode(first.node):
				// A class that matches a whole literal branch matches it too.
				if literalInClass(second.text, first.node) {
					return ambiguous(rep)
				}
			case isClassNode(second.node):
				if literalInClass(first.text, second.node) {
					return ambiguous(rep)
				}
			default:
				fold := (first.node.Flags|second.node.Flags)&syntax.FoldCase != 0
				equal := func(a, b string) bool { return a == b }
				if fold {
					equal = strings.EqualFold
				}
				if equal(first.text, second.text) {
					return ambiguous(rep)
				}
				long, short := second, first
				if len(short.text) > len(long.text) {
					long, short = first, second
				}
				if len(long.text) > len(short.text) && len(long.text)%len(short.text) == 0 &&
					equal(long.text, strings.Repeat(short.text, len(long.text)/len(short.text))) {
					return ambiguous(rep)
				}
			}
		}
	}
	return nil
}

func ambiguous(rep *syntax.Regexp) error {
	return fmt.Errorf(
		"alternation under the unbounded quantifier %q has branches that can match the same text (e.g. %q)",
		rep.Op.String(), `(a|\w)+`,
	)
}

func isClassNode(n *syntax.Regexp) bool {
	switch n.Op {
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return true
	}
	return false
}

func classMatchRune(n *syntax.Regexp, r rune) bool {
	if n.Op == syntax.OpAnyChar {
		return true
	}
	if n.Op == syntax.OpAnyCharNotNL {
		return r != '\n'
	}
	for i := 0; i+1 < len(n.Rune); i += 2 {
		if n.Rune[i] <= r && r <= n.Rune[i+1] {
			return true
		}
	}
	// The i flag makes classes case-insensitive at match time, so mirror
	// that here when judging overlap.
	if n.Flags&syntax.FoldCase != 0 {
		for _, alt := range []rune{unicode.ToLower(r), unicode.ToUpper(r)} {
			if alt == r {
				continue
			}
			for i := 0; i+1 < len(n.Rune); i += 2 {
				if n.Rune[i] <= alt && alt <= n.Rune[i+1] {
					return true
				}
			}
		}
	}
	return false
}

func classesIntersect(a, b *syntax.Regexp) bool {
	for i := 0; i+1 < len(a.Rune); i += 2 {
		for r := a.Rune[i]; r <= a.Rune[i+1]; r++ {
			if classMatchRune(b, r) {
				return true
			}
		}
	}
	return a.Op == syntax.OpAnyChar || b.Op == syntax.OpAnyChar ||
		a.Op == syntax.OpAnyCharNotNL || b.Op == syntax.OpAnyCharNotNL
}

func literalInClass(text string, class *syntax.Regexp) bool {
	for _, r := range text {
		if !classMatchRune(class, r) {
			return false
		}
	}
	return true
}

func hasAlternate(n *syntax.Regexp) bool {
	if n.Op == syntax.OpAlternate {
		return true
	}
	return slices.ContainsFunc(n.Sub, hasAlternate)
}

type textRep struct {
	text string
	node *syntax.Regexp
}

// enumerate builds up to maxEnumeratedPaths representative strings the
// expression can match, one per distinct branch, tagging the leaf node each
// string came from. The second return reports a budget overflow.
func enumerate(n *syntax.Regexp) ([]textRep, bool) {
	return enumerateInto(n, nil)
}

func enumerateInto(n *syntax.Regexp, out []textRep) ([]textRep, bool) {
	switch n.Op {
	case syntax.OpNoMatch:
		return out, false
	case syntax.OpLiteral:
		return appendTexts(out, n, string(n.Rune))
	case syntax.OpCharClass:
		return appendTexts(out, n, classRepresentative(n))
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return appendTexts(out, n, "a")
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return appendTexts(out, n, "")
	case syntax.OpQuest, syntax.OpStar, syntax.OpPlus, syntax.OpRepeat, syntax.OpCapture:
		return enumerateInto(n.Sub[0], out)
	case syntax.OpAlternate:
		var capped bool
		for _, child := range n.Sub {
			var ok bool
			out, ok = enumerateInto(child, out)
			capped = capped || ok
		}
		return out, capped
	case syntax.OpConcat:
		current := []textRep{{text: "", node: n}}
		var capped bool
		for _, child := range n.Sub {
			left, ok := enumerateInto(child, nil)
			capped = capped || ok
			var next []textRep
			for _, prefix := range current {
				for _, suffix := range left {
					next, ok = appendTexts(next, suffix.node, prefix.text+suffix.text)
					capped = capped || ok
				}
			}
			current = next
		}
		out = append(out, current...)
		return out, capped || len(out) >= maxEnumeratedPaths
	}
	// An unknown node with children cannot be enumerated; report it as
	// over-budget so the ambiguity test abstains.
	return out, true
}

func appendTexts(out []textRep, node *syntax.Regexp, text string) ([]textRep, bool) {
	if len(text) > maxRepresentativeLen || len(out) >= maxEnumeratedPaths {
		return out, true
	}
	return append(out, textRep{text: text, node: node}), false
}

// classRepresentative returns one character the class matches, so branch
// comparisons see real overlap (e.g. \d and a literal 0 collide). Class
// runes are stored as from-to range pairs.
func classRepresentative(n *syntax.Regexp) string {
	if len(n.Rune) >= 2 {
		return string(n.Rune[0])
	}
	return "a"
}

// maxLen estimates the longest string the expression matches, or -1 when
// unbounded.
func maxLen(n *syntax.Regexp) int {
	switch n.Op {
	case syntax.OpNoMatch:
		return 0
	case syntax.OpLiteral:
		return len(string(n.Rune))
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return 1
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return 0
	case syntax.OpQuest:
		return maxLen(n.Sub[0])
	case syntax.OpStar, syntax.OpPlus:
		return -1
	case syntax.OpCapture:
		return maxLen(n.Sub[0])
	case syntax.OpRepeat:
		if n.Max < 0 {
			return -1
		}
		child := maxLen(n.Sub[0])
		if child < 0 {
			return -1
		}
		if n.Max > 0 && child > MaxRepeatExpansion/n.Max {
			return MaxRepeatExpansion + 1
		}
		return n.Max * child
	case syntax.OpConcat:
		total := 0
		for _, child := range n.Sub {
			l := maxLen(child)
			if l < 0 {
				return -1
			}
			total += l
		}
		return total
	case syntax.OpAlternate:
		best := 0
		for _, child := range n.Sub {
			l := maxLen(child)
			if l < 0 {
				return -1
			}
			best = max(best, l)
		}
		return best
	}
	if len(n.Sub) == 0 {
		return 0
	}
	return -1
}
