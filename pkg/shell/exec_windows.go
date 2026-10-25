//go:build windows

package shell

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/hackafterdark/phosphor/pkg/sandbox"
	"mvdan.cc/sh/v3/interp"
)

// defaultKillTimeout matches mvdan's DefaultExecHandler default.
const defaultKillTimeout = 2 * time.Second

// isolateProcess is a no-op on Windows. Session isolation via Setsid is a
// Unix-only concept; Windows uses CREATE_NEW_PROCESS_GROUP which mvdan's
// default handler already handles adequately.
func isolateProcess(_ *exec.Cmd) {}

// processGroupExecHandler on Windows confines the child through the
// restricted token (set inside confineProcess) and assigns it to a
// containment Job Object after spawn. Unlike the Unix handler there is
// no process-group signal to target; cancellation kills the child
// directly, and the job's kill-on-close covers any descendants.
//
// v1 has no kernel-enforced workspace FS boundary on Windows (see
// pkg/sandbox/isolator_windows.go); sandbox.Confine reports that
// honestly and workspace mode degrades with a visible warning.
func processGroupExecHandler(killTimeout time.Duration, sbx *sandboxPolicy) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		path, err := interp.LookPathDir(hc.Dir, hc.Env, args[0])
		if err != nil {
			fmt.Fprintln(hc.Stderr, err)
			return interp.ExitStatus(127)
		}

		cmd := exec.Cmd{
			Path:   path,
			Args:   args,
			Env:    execEnvList(hc.Env),
			Dir:    hc.Dir,
			Stdin:  hc.Stdin,
			Stdout: hc.Stdout,
			Stderr: hc.Stderr,
		}
		isolateProcess(&cmd)
		if refused := confineProcess(&cmd, sbx, hc.Stderr); refused != nil {
			return refused
		}

		err = cmd.Start()
		if err == nil {
			// Kill-on-close is the Windows-side cleanup for every
			// exec (Unix parity); the resource caps attach only
			// when a sandbox policy is active.
			release, cerr := sandbox.ContainProcess(&cmd, sbx != nil)
			if cerr != nil {
				fmt.Fprintf(hc.Stderr, "phosphor: containment failed: %v\n", cerr)
			}
			if release != nil {
				defer release()
			}
			stopf := context.AfterFunc(ctx, func() {
				// Windows has no polite signal for arbitrary
				// processes; the job's kill-on-close covers the
				// descendants.
				_ = cmd.Process.Kill()
			})
			defer stopf()

			err = cmd.Wait()
		}

		return exitStatusFromError(ctx, hc.Stderr, err)
	}
}

// exitStatusFromError translates an exec error into an interp exit
// status. Windows has no signal semantics; the exit code is the whole
// story (128+n on cancel stays mvdan's convention via the default
// handler path).
func exitStatusFromError(ctx context.Context, stderr io.Writer, err error) error {
	if err == nil {
		return nil
	}
	switch err := err.(type) {
	case *exec.ExitError:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return interp.ExitStatus(uint8(err.ExitCode()))
	case *exec.Error:
		fmt.Fprintf(stderr, "%v\n", err)
		return interp.ExitStatus(127)
	default:
		return err
	}
}
