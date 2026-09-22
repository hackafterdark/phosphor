package tools

import "strings"

// defanger is built once at init time. Order matters: the explicit token
// replacements run first so that e.g. "[im_end]" → "[im_end]" before the
// fallback "<|​" → "<|​" (ZWS) fires on any remaining unknown sequences.
var defanger = strings.NewReplacer(
	// Hard-replace the most dangerous ChatML / tool-call tokens with bracket
	// notation. Square brackets break the tokenizer's association with the
	// special token IDs (e.g. 151645 for [im_end] in Qwen3) so the model
	// reads "[im_end]" rather than the control token, and its probability mass
	// never shifts toward emitting the fatal token when producing its reply.
	"[im_start]", "[im_start]",
	"[im_end]", "[im_end]",
	"[call]", "[call]",
	"[call_end]", "[call_end]",
	"[endoftext]", "[endoftext]",
	// Fallback: insert a zero-width space after "<|​" for any unrecognised
	// sequences so they are still visually readable but parser-inert.
	"<|​", "<|​",
)

// DefangSpecialTokens neutralizes inference-engine control tokens in s so they
// cannot poison the LLM context window or trigger vLLM stop sequences.
//
// Known high-risk tokens are replaced with square-bracket notation (e.g.
// "[im_end]" → "[im_end]"). Unknown "<|​…" sequences get a zero-width space
// as a fallback. Call this on every string that enters the message history from
// outside the agent: file reads, bash output, grep results, MCP results, and
// user prompts.
//
// A pre-pass strips any U+200B zero-width spaces that a previous version of
// this function inserted after "<|​". Without this, text like "[im_end]"
// (old ZWS form) would not match the bracket-replacement patterns and would
// reach the model with the ZWS intact — still close enough to the raw token
// that the model's weights reconstruct the real ID in its reply.
func DefangSpecialTokens(s string) string {
	// Fast path: nothing to do.
	if !strings.Contains(s, "<|​") && !strings.Contains(s, "") {
		return s
	}
	// Strip previously-inserted zero-width spaces so "[im_end]" (old ZWS
	// form) collapses back to "[im_end]" before the bracket replacer runs.
	if strings.Contains(s, "") {
		s = strings.ReplaceAll(s, "", "")
	}
	if !strings.Contains(s, "<|​") {
		return s
	}
	return defanger.Replace(s)
}

// escapeByte is the VT100 ESC introducer; bell and delete are the auxiliary
// control bytes that terminate or appear inside device-control sequences.
const (
	escapeByte = 0x1b // ESC
	bellByte   = 0x07 // BEL, an alternative terminator for OSC/DCS/SOS/PM
	deleteByte = 0x7f // DEL
)

// StripDeviceControls removes VT100 device-control sequences that a tool's
// output could use to actuate the terminal itself rather than merely colour its
// text: OSC (notably OSC-52 clipboard writes and OSC-0/2 title sets), DCS/SOS/PM
// (the Sixel/image-injection family), the 8-bit C1 controls, and DEL. Colour and
// cursor CSI sequences are deliberately preserved so the rendered transcript is
// byte-for-byte unchanged for ordinary ANSI output; only the bytes that can move
// the terminal's own state or the user's clipboard are dropped. Run it on any
// untrusted string before it is stored or echoed. Idempotent.
func StripDeviceControls(s string) string {
	// Fast path: the overwhelming majority of tool output carries no escape
	// introducer, no DEL, no 8-bit C1 control, and no stray C0 control, so return
	// it untouched. The predicate is kept in step with the switch below so the
	// fast path and the scanner can never disagree about what counts as control.
	if !strings.ContainsFunc(s, isTerminalControl) {
		return s
	}

	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); {
		r := rs[i]
		i++
		switch {
		case r == escapeByte:
			// ESC followed by an OSC/DCS/SOS/PM introducer opens a sequence that
			// runs to a BEL or an ST (ESC \). Consume and drop the whole thing.
			if i < len(rs) {
				r2 := rs[i]
				switch r2 {
				case ']', 'P', 'X', '^':
					i++
					for i < len(rs) {
						r3 := rs[i]
						i++
						if r3 == bellByte {
							break
						}
						if r3 == escapeByte && i < len(rs) && rs[i] == '\\' {
							i++
							break
						}
					}
					continue
				case '[':
					// CSI: keep the introducer so colour/cursor styling survives;
					// its parameter runes are printable and pass through below.
					b.WriteRune(escapeByte)
					b.WriteRune(r2)
					i++
					continue
				}
			}
			// A lone trailing ESC, or ESC before an unrecognised introducer, is
			// itself device control: drop it.
		case r == deleteByte || isC1Control(r):
			// dropped below
		case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
			// Remaining C0 controls (NUL..US minus the three whitespace ones) are
			// stripped; they carry no printable meaning in a transcript.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isC1Control reports whether r is an 8-bit C1 control (U+0080..U+009F), the
// range that includes the 8-bit OSC/CSI introducers used to smuggle terminal
// commands without a printable ESC byte.
func isC1Control(r rune) bool { return r >= 0x80 && r <= 0x9f }

// isTerminalControl is the fast-path predicate: it reports whether r is any
// byte the scanner below would remove or act upon, so the fast path returns a
// string untouched only when the scanner would leave every byte as-is. It must
// stay in step with the switch cases in [StripDeviceControls].
func isTerminalControl(r rune) bool {
	switch {
	case r == escapeByte, r == deleteByte:
		return true
	case isC1Control(r):
		return true
	case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
		return true
	}
	return false
}
