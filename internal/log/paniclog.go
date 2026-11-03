package log

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
)

// panicLogMu serializes hookPanicLog calls; panicHookInstalled records that
// crash output is already mirrored so repeated Setup calls never chain hooks.
var (
	panicLogMu         sync.Mutex
	panicHookInstalled bool
)

// hookPanicLog mirrors unhandled panics and runtime fatal errors (including
// the full goroutine dump) into <logFile-directory>/phosphor-panic.log. The
// Go runtime writes this output directly to stderr, bypassing slog, so it is
// otherwise lost when the terminal scrollback scrolls away. debug.SetCrashOutput
// writes the crash report to the mirror file in addition to stderr, capturing
// panic, runtime fault, and fatal-error output alike. Returns false if the
// mirror cannot be set up.
func hookPanicLog(logFile string) bool {
	panicLogMu.Lock()
	defer panicLogMu.Unlock()
	if panicHookInstalled {
		return true
	}

	panicFile := filepath.Join(filepath.Dir(logFile), "phosphor-panic.log")
	if err := os.MkdirAll(filepath.Dir(panicFile), 0o700); err != nil {
		return false
	}
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

	panicHookInstalled = true
	return true
}

// PanicLogFile returns the path of the panic mirror file next to logFile.
func PanicLogFile(logFile string) string {
	return filepath.Join(filepath.Dir(logFile), "phosphor-panic.log")
}
