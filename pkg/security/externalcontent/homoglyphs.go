package externalcontent

import "strings"

// zeroWidthRunes is the set of invisible/control Unicode code points that
// attackers inject to break ASCII pattern-matching while keeping text visually
// identical. They are silently dropped during normalization.
var zeroWidthRunes = map[rune]bool{
	0x200B: true, // ZERO WIDTH SPACE
	0x200C: true, // ZERO WIDTH NON-JOINER
	0x200D: true, // ZERO WIDTH JOINER
	0xFEFF: true, // ZERO WIDTH NO-BREAK SPACE (BOM)
	0x2060: true, // WORD JOINER
	0x00AD: true, // SOFT HYPHEN
	0x180E: true, // MONGOLIAN VOWEL SEPARATOR
	0x034F: true, // COMBINING GRAPHEME JOINER
}

// homoglyphMap maps Unicode code points that visually resemble the ASCII
// punctuation used in chat-template tokens (<, >, |) to their ASCII
// equivalents.
var homoglyphMap = map[rune]rune{
	// Fullwidth vertical line — used raw in DeepSeek special tokens
	0xFF5C: '|', // FULLWIDTH VERTICAL LINE ｜

	// Angle-bracket lookalikes
	0x2039: '<', // SINGLE LEFT-POINTING ANGLE QUOTATION MARK ‹
	0x203A: '>', // SINGLE RIGHT-POINTING ANGLE QUOTATION MARK ›
	0x276E: '<', // HEAVY LEFT-POINTING ANGLE QUOTATION MARK ORNAMENT ❮
	0x276F: '>', // HEAVY RIGHT-POINTING ANGLE QUOTATION MARK ORNAMENT ❯
	0x27E8: '<', // MATHEMATICAL LEFT ANGLE BRACKET ⟨
	0x27E9: '>', // MATHEMATICAL RIGHT ANGLE BRACKET ⟩
	0x3008: '<', // CJK LEFT ANGLE BRACKET 〈
	0x3009: '>', // CJK RIGHT ANGLE BRACKET 〉
	0x00AB: '<', // LEFT-POINTING DOUBLE ANGLE QUOTATION MARK «
	0x00BB: '>', // RIGHT-POINTING DOUBLE ANGLE QUOTATION MARK »
	0xFF62: '<', // HALFWIDTH LEFT CORNER BRACKET ｢
	0xFF63: '>', // HALFWIDTH RIGHT CORNER BRACKET ｣

	// Pipe lookalikes
	0x01C0: '|', // LATIN LETTER DENTAL CLICK ǀ
	0x2502: '|', // BOX DRAWINGS LIGHT VERTICAL │
	0x2758: '|', // LIGHT VERTICAL BAR ❘
	0x2759: '|', // MEDIUM VERTICAL BAR ❙
	0x275A: '|', // HEAVY VERTICAL BAR ❚

	// SentencePiece word-boundary marker — appears in DeepSeek token names
	0x2581: '_', // LOWER ONE EIGHTH BLOCK ▁
}

// foldHomoglyphs normalizes Unicode homoglyphs and invisible characters in s
// so that subsequent pattern-matching against ASCII chat-template tokens is
// reliable.  A single rune-by-rune pass:
//
//   - Drops every rune in zeroWidthRunes (invisible chars injected to split tokens).
//   - Applies homoglyphMap substitutions (lookalike angle brackets, pipes, etc.).
//   - Maps the entire fullwidth ASCII block U+FF01–U+FF5E to U+0021–U+007E by
//     subtracting 0xFEE0, covering attacks like "＜|im_end|＞" where the attacker
//     substitutes U+FF1C (FULLWIDTH LESS-THAN SIGN) for the ASCII "<".
//
// The function is a no-op on pure-ASCII input (fast path avoids allocation).
func foldHomoglyphs(s string) string {
	if s == "" {
		return s
	}

	// Fast path: no non-ASCII bytes means no homoglyphs.
	allASCII := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			allASCII = false
			break
		}
	}
	// Also fast-path the common case where the only non-ASCII content is an
	// existing ZWS left by the old token_defang.go pass.  That ZWS is 0xE2
	// 0x80 0x8B (U+200B in UTF-8); catching it here lets Sanitize strip old
	// ZWS-polluted history even when no other homoglyphs are present.
	if allASCII {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		// Drop invisible / zero-width characters.
		if zeroWidthRunes[r] {
			continue
		}
		// Explicit homoglyph substitution.
		if replacement, ok := homoglyphMap[r]; ok {
			b.WriteRune(replacement)
			continue
		}
		// Fullwidth ASCII block: subtract 0xFEE0 to get the regular ASCII rune.
		// Covers ＜ (FF1C→<), ＞ (FF1E→>), ／ (FF0F→/) and all others.
		if r >= 0xFF01 && r <= 0xFF5E {
			b.WriteRune(r - 0xFEE0)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
