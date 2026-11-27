package log

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
)

// panicLogMu serializes hookPanicLog calls; panicLogPath records the
// mirror file currently installed so repeated Setup calls for the same
// directory never chain hooks, and a Reset-then-Setup with a new log
// directory re-points the mirror to follow the active log path.
var (
	panicLogMu   sync.Mutex
	panicLogPath string
)

// hookPanicLog mirrors unhandled panics and runtime fatal errors (including
// the full goroutine dump) into <logFile-directory>/phosphor-panic.log. The
// Go runtime writes this output directly to stderr, bypassing slog, so it is
// otherwise lost when the terminal scrollback scrolls away. debug.SetCrashOutput
// writes the crash report to the mirror file in addition to stderr, capturing
// panic, runtime fault, and fatal-error output alike. Calling SetCrashOutput
// again overrides any earlier call, so a reconfigured log directory re-points
// the mirror. Returns false if the mirror cannot be set up.
func hookPanicLog(logFile string) bool {
	panicLogMu.Lock()
	defer panicLogMu.Unlock()

	// The panic mirror sits next to the configured log file. logFile is
	// derived from Options.DataDirectory, which config hardening clamps to
	// the trusted floor, so no workspace-supplied value reaches this path;
	// the symlink check below additionally refuses a planted redirect
	// target.
	// codeql[go/path-injection]
	panicFile := filepath.Join(filepath.Dir(logFile), "phosphor-panic.log")
	if panicLogPath == panicFile {
		return true
	}
	// codeql[go/path-injection] panicFile is floor-clamped per the comment
	// above; no workspace-supplied component reaches this path.
	if err := os.MkdirAll(filepath.Dir(panicFile), 0o700); err != nil {
		return false
	}
	// Refuse a planted symlink at the mirror path: the crash log is
	// opened on the crash path itself, and an attacker-controlled
	// target there would redirect crash output outside the log dir.
	// codeql[go/path-injection] same floor-clamped panicFile; the Lstat
	// itself is the planted-symlink check for this path.
	if fi, err := os.Lstat(panicFile); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return false
	}
	// codeql[go/path-injection] same floor-clamped panicFile, opened only
	// after the symlink check above.
	f, err := os.OpenFile(panicFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		f.Close()
		return false
	}
	// SetCrashOutput duplicates the descriptor; closing f is safe.
	f.Close()

	panicLogPath = panicFile
	return true
}

// PanicLogFile returns the path of the panic mirror file next to logFile.
func PanicLogFile(logFile string) string {
	// codeql[go/path-injection] same floor-clamped logFile as hookPanicLog;
	// no workspace-supplied component reaches this path.
	return filepath.Join(filepath.Dir(logFile), "phosphor-panic.log")
}
