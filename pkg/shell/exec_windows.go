//go:build windows

package shell

import (
	"os/exec"
	"time"

	"mvdan.cc/sh/v3/interp"
)

// defaultKillTimeout matches mvdan's DefaultExecHandler default.
const defaultKillTimeout = 2 * time.Second

// isolateProcess is a no-op on Windows. Session isolation via Setsid is a
// Unix-only concept; Windows uses CREATE_NEW_PROCESS_GROUP which mvdan's
// default handler already handles adequately.
func isolateProcess(_ *exec.Cmd) {}

// processGroupExecHandler returns interp.DefaultExecHandler on Windows.
// The sandbox policy is accepted for signature parity; kernel confinement
// on Windows arrives with the phase-3 backend and is a no-op until then.
func processGroupExecHandler(killTimeout time.Duration, _ *sandboxPolicy) interp.ExecHandlerFunc {
	return interp.DefaultExecHandler(killTimeout)
}
