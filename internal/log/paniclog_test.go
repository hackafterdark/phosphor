package log

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPanicHookCapturesPanic runs a subprocess that initializes logging and
// then panics. The Go runtime writes the panic header and goroutine dump to
// stderr; the crash-output hook must mirror it into phosphor-panic.log next
// to the log file so the crash survives the terminal scrollback.
func TestPanicHookCapturesPanic(t *testing.T) {
	if os.Getenv("PHOSPHOR_PANIC_TEST_CHILD") == "1" {
		logFile := filepath.Join(os.Getenv("PHOSPHOR_PANIC_TEST_DIR"), "logs", "phosphor.log")
		Setup(logFile, false)
		panic("boom from panic hook test")
	}

	dir := t.TempDir()
	t.Setenv("PHOSPHOR_PANIC_TEST_CHILD", "1")
	t.Setenv("PHOSPHOR_PANIC_TEST_DIR", dir)

	cmd := exec.Command(os.Args[0], "-test.run", "TestPanicHookCapturesPanic")
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	t.Log("child output:", string(out))
	require.Error(t, err, "child process should exit non-zero from the panic")

	panicLog := PanicLogFile(filepath.Join(dir, "logs", "phosphor.log"))
	data, err := os.ReadFile(panicLog)
	require.NoError(t, err, "panic mirror file must exist")
	require.Contains(t, string(data), "panic: boom from panic hook test")
	require.Contains(t, string(data), "goroutine", "goroutine dump must be captured")
}

// TestPanicHookCapturesRuntimeFault verifies that runtime faults (slice
// bounds) — the same class of fault that bypasses recover() — are mirrored
// into the panic log.
func TestPanicHookCapturesRuntimeFault(t *testing.T) {
	if os.Getenv("PHOSPHOR_PANIC_TEST_CHILD") == "1" {
		logFile := filepath.Join(os.Getenv("PHOSPHOR_PANIC_TEST_DIR"), "logs", "phosphor.log")
		Setup(logFile, false)
		s := []int{1, 2, 3}
		_ = s[2:10]
		return
	}

	dir := t.TempDir()
	t.Setenv("PHOSPHOR_PANIC_TEST_CHILD", "1")
	t.Setenv("PHOSPHOR_PANIC_TEST_DIR", dir)

	cmd := exec.Command(os.Args[0], "-test.run", "TestPanicHookCapturesRuntimeFault")
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	t.Log("child output:", string(out))
	require.Error(t, err, "child process should exit non-zero from the fault")

	panicLog := PanicLogFile(filepath.Join(dir, "logs", "phosphor.log"))
	data, err := os.ReadFile(panicLog)
	require.NoError(t, err, "panic mirror file must exist")
	require.Contains(t, string(data), "slice bounds out of range")
}
