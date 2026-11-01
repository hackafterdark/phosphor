//go:build windows

package shell

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"

	"github.com/hackafterdark/phosphor/pkg/sandbox"
	"golang.org/x/sys/windows"
	"mvdan.cc/sh/v3/interp"
)

// defaultKillTimeout matches mvdan's DefaultExecHandler default.
const defaultKillTimeout = 2 * time.Second

// isolateProcess detaches the child from the TUI's console. Windows'
// equivalent of the Unix Setsid session isolation: CREATE_NO_WINDOW
// gives the child a console with no window, so direct console writes
// (cygwin's early fatal-error path, for instance) land in a hidden
// buffer instead of bleeding onto the terminal screen. Without this,
// a child like cygwin ssh.exe painting "fatal error" text over the
// live TUI leaves ghost text that only a resize repaint erases. The
// exec handler always pipes stdio anyway, so the child never needs
// the visible console.
func isolateProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}

// processGroupExecHandler on Windows routes confined children through
// the platform tier: the Windows 11 AppContainer tier launches the
// child directly through confineSpawn (kernel-enforced workspace FS
// boundary), while older hosts stay on the restricted token (set
// inside confineProcess) with a containment Job Object after spawn.
// Unlike the Unix handler there is no process-group signal to target;
// cancellation kills the child directly, and the job's kill-on-close
// covers any descendants.
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
		child, refused := confineSpawn(&cmd, sbx, hc.Stderr)
		if refused != nil {
			return refused
		}
		if child != nil {
			// AppContainer tier: the kernel launched the child;
			// cancellation kills it directly and the job's
			// kill-on-close covers any descendants.
			stopf := context.AfterFunc(ctx, func() { _ = child.Kill() })
			defer stopf()
			_, err = child.Wait()
			return exitStatusFromError(ctx, hc.Stderr, err)
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
