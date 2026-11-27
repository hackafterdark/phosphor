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
		// Setup skips the panic mirror inside test binaries (the open
		// mirror file would wedge t.TempDir cleanup on Windows), so
		// install it explicitly for the crash-path check.
		require.True(t, hookPanicLog(logFile, os.Getenv("PHOSPHOR_PANIC_TEST_DIR")), "panic mirror must install")
		Setup(logFile, os.Getenv("PHOSPHOR_PANIC_TEST_DIR"), false)
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
		require.True(t, hookPanicLog(logFile, os.Getenv("PHOSPHOR_PANIC_TEST_DIR")), "panic mirror must install")
		Setup(logFile, os.Getenv("PHOSPHOR_PANIC_TEST_DIR"), false)
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

// TestPanicHookFollowsReconfiguredLogDir verifies that after Reset then
// Setup with a second log directory, crash output lands in the new
// directory's mirror file, not the first one.
func TestPanicHookFollowsReconfiguredLogDir(t *testing.T) {
	if os.Getenv("PHOSPHOR_PANIC_TEST_CHILD") == "1" {
		childDir := os.Getenv("PHOSPHOR_PANIC_TEST_DIR")
		first := filepath.Join(childDir, "first", "phosphor.log")
		second := filepath.Join(childDir, "second", "phosphor.log")
		require.True(t, hookPanicLog(first, childDir), "first panic mirror must install")
		Reset()
		require.True(t, hookPanicLog(second, childDir), "second panic mirror must install")
		Setup(second, childDir, false)
		panic("boom from reconfigured panic hook test")
	}

	dir := t.TempDir()
	t.Setenv("PHOSPHOR_PANIC_TEST_CHILD", "1")
	t.Setenv("PHOSPHOR_PANIC_TEST_DIR", dir)

	cmd := exec.Command(os.Args[0], "-test.run", "TestPanicHookFollowsReconfiguredLogDir")
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	t.Log("child output:", string(out))
	require.Error(t, err, "child process should exit non-zero from the panic")

	firstPanic := PanicLogFile(filepath.Join(dir, "first", "phosphor.log"))
	firstData, err := os.ReadFile(firstPanic)
	require.NoError(t, err, "first mirror file exists once opened")
	require.Empty(t, firstData, "crash output must follow the re-pointed mirror")

	secondPanic := PanicLogFile(filepath.Join(dir, "second", "phosphor.log"))
	data, err := os.ReadFile(secondPanic)
	require.NoError(t, err, "second mirror file must exist")
	require.Contains(t, string(data), "boom from reconfigured panic hook test")
}

// TestPanicHookRefusesEscapedLogDir verifies a planted symlink at the log
// directory cannot redirect the crash mirror outside the workspace: the
// mirror is disabled instead (fail closed).
func TestPanicHookRefusesEscapedLogDir(t *testing.T) {
	outside := t.TempDir()
	work := t.TempDir()

	linkedDir := filepath.Join(work, "logs")
	require.NoError(t, os.Symlink(outside, linkedDir))
	// The mirror path itself must exist so the containment check, not the
	// file-level symlink check, is what refuses the install.
	require.NoError(t, os.WriteFile(filepath.Join(outside, "phosphor-panic.log"), nil, 0o600))

	t.Chdir(work)
	require.False(t, hookPanicLog(filepath.Join(linkedDir, "phosphor.log"), work),
		"a log directory resolving outside the workspace must refuse the panic mirror")
}
