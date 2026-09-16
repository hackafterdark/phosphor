package tools

import (
	"path/filepath"
	"strings"
)

// ScanMode is the context flag that steers how aggressively a content scan
// runs. It exists because a generic KEY=value or "apiKey":"value" detector is a
// false-positive magnet on source code (where such literals are overwhelmingly
// placeholders, fixtures and config keys) yet is load-bearing on env/config
// output (where it is often the only thing catching an opaque custom secret).
// Picking the mode from the read context lets each surface keep the precision
// it needs. This is the context-flagged scan API of the redaction plan.
type ScanMode int

const (
	// ScanFull runs the complete detector set: the high-precision checks plus
	// the generic/keyword-anchored family. Used for config/dotenv/log/command
	// output and for any context that is not recognised source code.
	ScanFull ScanMode = iota
	// ScanCodeFile runs the high-precision checks only (vendor prefixes, PEM,
	// JWT, auth headers, connection strings, URL userinfo) and drops the
	// generic family, which on code is FP noise. The write-path block still uses
	// ScanFull, so a real credential authored into a file is never let through.
	ScanCodeFile
)

// sourceCodeExts are the extensions whose files are treated as source code for
// the purposes of choosing a ScanMode. Reading one of these (or grepping inside
// one) uses ScanCodeFile; everything else, including .env/.json/.yaml/.ini/.toml
// config, uses ScanFull.
var sourceCodeExts = map[string]bool{
	".go": true, ".py": true, ".pyi": true, ".js": true, ".mjs": true, ".cjs": true,
	".jsx": true, ".ts": true, ".tsx": true, ".java": true, ".kt": true, ".kts": true,
	".scala": true, ".rb": true, ".php": true, ".c": true, ".h": true, ".cc": true,
	".cpp": true, ".cxx": true, ".hpp": true, ".hh": true, ".cs": true, ".rs": true,
	".swift": true, ".m": true, ".mm": true, ".sh": true, ".bash": true, ".zsh": true,
	".sql": true, ".html": true, ".htm": true, ".css": true, ".scss": true, ".sass": true,
	".less": true, ".vue": true, ".svelte": true, ".dart": true, ".ex": true, ".exs": true,
	".erl": true, ".hrl": true, ".hs": true, ".lua": true, ".pl": true, ".pm": true,
	".r": true, ".jl": true, ".groovy": true, ".gradle": true, ".ml": true, ".mli": true,
	".fs": true, ".fsx": true, ".vb": true, ".v": true, ".sv": true, ".clj": true,
	".cljs": true, ".elm": true, ".nim": true, ".zig": true, ".toml": false,
}

// scanModeForPath chooses the scan mode for a read of the given path coming from
// the named tool source. Source-code file paths select ScanCodeFile; everything
// else (config, dotenv, logs, and the path-less command/output surfaces that
// pass an empty path) selects ScanFull. It fails toward ScanFull, the
// higher-recall mode, whenever it is unsure.
func scanModeForPath(filePath, source string) ScanMode {
	if filePath == "" {
		// Path-less output (bash stdout, MCP result, job output): treat as config
		// output and run the full set.
		return ScanFull
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	if sourceCodeExts[ext] {
		return ScanCodeFile
	}
	return ScanFull
}
