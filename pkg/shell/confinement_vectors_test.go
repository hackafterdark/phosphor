package shell

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"

	"github.com/stretchr/testify/require"

	"github.com/hackafterdark/phosphor/internal/pathguard"
)

// TestJQBuiltinConfinesFileOperands proves the jq builtin bounds-checks the
// files it opens in-process (os.Open in readInputs) before touching them, even
// though jq is dispatched as a Phosphor builtin that short-circuits the exec
// middleware chain. The out-of-workspace operand must surface a confinement
// error; an in-workspace operand must not.
func TestJQBuiltinConfinesFileOperands(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := filepath.Join(filepath.Dir(filepath.Clean(os.TempDir())), "phosphor-jq-vector.json")
	require.NoError(t, os.WriteFile(outside, []byte(`{"a":1}`), 0o644))
	inside := filepath.Join(workspace, "data.json")
	require.NoError(t, os.WriteFile(inside, []byte(`{"a":1}`), 0o644))

	conf := newConfinement(workspace, nil, false)

	err := handleJQ(t.Context(), conf, workspace, []string{"jq", "-r", "-R", ".", outside}, nil, io.Discard, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")

	err = handleJQ(t.Context(), conf, workspace, []string{"jq", "-r", "-R", ".", inside}, nil, io.Discard, io.Discard)
	if err != nil {
		require.NotContains(t, err.Error(), "outside workspace", err.Error())
	}

	// A nil confinement (trusted hook runner) must not enforce the check.
	err = handleJQ(t.Context(), nil, workspace, []string{"jq", "-r", "-R", ".", outside}, nil, io.Discard, io.Discard)
	if err != nil {
		require.NotContains(t, err.Error(), "outside workspace", err.Error())
	}
}

// TestPathPrefixedDispatchConfinesScriptPath proves that executing a
// path-prefixed program whose argv[0] resolves outside the workspace (here via
// an inline-assignment indirection so the literal path never appears in the
// command text) is refused before probeFile/os.ReadFile open it. The same check
// that stops the read also stops the "source an out-of-workspace file" vector.
func TestPathPrefixedDispatchConfinesScriptPath(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := filepath.Join(filepath.Dir(filepath.Clean(os.TempDir())), "phosphor-dispatch-vector.sh")
	require.NoError(t, os.WriteFile(outside, []byte("#!/bin/sh\necho leak\n"), 0o755))

	// "$p" where p was assigned the out-of-workspace path in the same line.
	err := Run(t.Context(), RunOptions{
		Command:   "p=" + shellQuote(filepath.ToSlash(outside)) + `; "$p"`,
		Cwd:       workspace,
		Workspace: workspace,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")

	// A script that lives inside the workspace must not be confined away.
	inside := filepath.Join(workspace, "ok.sh")
	require.NoError(t, os.WriteFile(inside, []byte("echo hi\n"), 0o755))
	err = Run(t.Context(), RunOptions{
		Command:   "p=" + shellQuote(filepath.ToSlash(inside)) + `; "$p"`,
		Cwd:       workspace,
		Workspace: workspace,
	})
	if err != nil {
		require.NotContains(t, err.Error(), "outside workspace", err.Error())
	}
}

// TestCheckProgramBoundsRedirectOperands exercises the post-expansion AST walk
// directly. It is the layer that catches redirection operands the interpreter
// opens itself: their paths live only in the redirect word (never in any exec
// argv), so neither the argv-bounded pathConfinementHandler nor the static
// validator (which these Run-level tests bypass by calling checkProgram) sees
// them. Input redirects are proven inside/out-of-workspace after $VAR
// expansion, and command-substitution-derived targets are treated as
// unprovable.
func TestCheckProgramBoundsRedirectOperands(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := filepath.Join(filepath.Dir(filepath.Clean(os.TempDir())), "phosphor-ast-vector.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret\n"), 0o644))

	conf := newConfinement(workspace, nil, false)
	env := []string{"OUT=" + filepath.ToSlash(outside), "REL=relative.txt"}

	parse := func(command string) *syntax.File {
		file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
		require.NoError(t, err)
		return file
	}

	// Input redirect expanded from an env var pointing outside the workspace:
	// only the AST walk can see it (the command's argv is just "cat").
	for _, command := range []string{
		`cat < "$OUT"`,
		`cat < $OUT`,
		`jq -r . < "$OUT"`,
		". \"$OUT\"",
		"source \"$OUT\"",
		". $OUT",
	} {
		err := checkProgram(conf, workspace, env, parse(command))
		require.Error(t, err, command)
		require.Contains(t, err.Error(), "outside workspace", command)
	}

	// Input redirect whose target is produced by a command substitution we
	// refuse to execute is unprovable and therefore blocked.
	err := checkProgram(conf, workspace, env, parse(`cat < $(printf %s "$OUT")`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")

	// Output redirect expanded from an env var is bounds-checked too.
	err = checkProgram(conf, workspace, env, parse(`echo hi > "$OUT"`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")

	// Legitimate in-workspace operands, here-docs, unprovable writes, and
	// fd-duplication operands must all pass.
	for _, command := range []string{
		`cat < "$REL"`,
		`cat < relative.txt`,
		`echo hi > "$REL"`,
		`echo hi > out.txt`,
		`echo hi > $(printf %s out.txt)`, // unprovable write: allowed
		"cat <<EOF\nhello\nEOF",
		`cat <&0`,
		". \"$REL\"",
		"source \"$REL\"",
	} {
		err := checkProgram(conf, workspace, env, parse(command))
		if err != nil {
			require.NotContains(t, err.Error(), "outside workspace", command)
		}
	}

	// A disabled confinement (nil or empty workspace root) never enforces.
	require.NoError(t, checkProgram(nil, workspace, env, parse(`cat < "$OUT"`)))
	require.NoError(t, checkProgram(&pathguard.Confinement{}, workspace, env, parse(`cat < "$OUT"`)))
}

// TestRunConfinesExpandedRedirectOperands proves the walk is wired into the
// [Run] entrypoint: a command whose read redirect resolves outside the
// workspace via a runtime-expanded variable is refused before execution, even
// though the command argv ("cat") is innocuous and the static validator is not
// involved on this code path.
func TestRunConfinesExpandedRedirectOperands(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := filepath.Join(filepath.Dir(filepath.Clean(os.TempDir())), "phosphor-run-vector.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret\n"), 0o644))

	err := Run(t.Context(), RunOptions{
		Command:   `cat < "$LEAK"`,
		Cwd:       workspace,
		Workspace: workspace,
		Env:       []string{"LEAK=" + filepath.ToSlash(outside)},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")

	// A concrete out-of-workspace *write* via a redirect whose operand is
	// env-expanded (argv is just "echo") is refused by the confinement-aware
	// interp.OpenHandler installed on the runner.
	err = Run(t.Context(), RunOptions{
		Command:   "echo hi > \"$LEAK\"",
		Cwd:       workspace,
		Workspace: workspace,
		Env:       []string{"LEAK=" + filepath.ToSlash(outside)},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")

	// The same command reading an in-workspace file must not be confined away.
	inside := filepath.Join(workspace, "ok.txt")
	require.NoError(t, os.WriteFile(inside, []byte("ok\n"), 0o644))
	err = Run(t.Context(), RunOptions{
		Command:   `cat < "$LEAK"`,
		Cwd:       workspace,
		Workspace: workspace,
		Env:       []string{"LEAK=" + filepath.ToSlash(inside)},
	})
	if err != nil {
		require.NotContains(t, err.Error(), "outside workspace", err.Error())
	}
}

// TestRunConfinesSourcedScriptBody proves the AST walk also covers the body of a
// *sourced* in-workspace script. The static [pathguard.ValidateCommandPaths]
// only sees the top-level command string and [pathConfinementHandler] only sees
// exec argv, so a redirect inside a sourced script whose target resolves outside
// the workspace (either literally or via an exported env var) would otherwise be
// opened un-checked by the interpreter. This is the runShellSource wiring.
func TestRunConfinesSourcedScriptBody(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := filepath.Join(filepath.Dir(filepath.Clean(os.TempDir())), "phosphor-source-body.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret\n"), 0o644))

	// A sourced script whose body reads outside via an exported env var.
	envScript := filepath.Join(workspace, "leak-env.sh")
	require.NoError(t, os.WriteFile(envScript, []byte(`echo sourcing; cat < "$LEAK"`+"\n"), 0o644))
	err := Run(t.Context(), RunOptions{
		Command:   `. "$SCRIPT"`,
		Cwd:       workspace,
		Workspace: workspace,
		Env: []string{
			"SCRIPT=" + filepath.ToSlash(envScript),
			"LEAK=" + filepath.ToSlash(outside),
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")

	// Same, with the out-of-workspace path written *literally* into the sourced
	// script body — invisible to the static validator (not in the command string)
	// and to the argv handler (a redirect, not argv).
	litScript := filepath.Join(workspace, "leak-lit.sh")
	require.NoError(t, os.WriteFile(litScript, []byte("echo hi; cat < \""+filepath.ToSlash(outside)+"\"\n"), 0o644))
	err = Run(t.Context(), RunOptions{
		Command:   `. "$SCRIPT"`,
		Cwd:       workspace,
		Workspace: workspace,
		Env:       []string{"SCRIPT=" + filepath.ToSlash(litScript)},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")

	// A sourced script that only touches an in-workspace file must run normally.
	okScript := filepath.Join(workspace, "ok-body.sh")
	require.NoError(t, os.WriteFile(okScript, []byte("echo hi; cat < ok.txt\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "ok.txt"), []byte("ok\n"), 0o644))
	err = Run(t.Context(), RunOptions{
		Command:   `. "$SCRIPT"`,
		Cwd:       workspace,
		Workspace: workspace,
		Env:       []string{"SCRIPT=" + filepath.ToSlash(okScript)},
	})
	if err != nil {
		require.NotContains(t, err.Error(), "outside workspace", err.Error())
	}
}

// TestExecConfinesExpandedRedirectOperands exercises the same post-expansion
// walk through the *stateful* [Shell.Exec] code path (execCommon), proving the
// guard is wired into both execution surfaces and not just the stateless Run.
func TestExecConfinesExpandedRedirectOperands(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := filepath.Join(filepath.Dir(filepath.Clean(os.TempDir())), "phosphor-exec-vector.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret\n"), 0o644))

	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"SystemRoot=" + os.Getenv("SystemRoot"),
		"COMSPEC=" + os.Getenv("COMSPEC"),
	}

	newConfined := func(leak string) *Shell {
		return NewShell(&Options{
			WorkingDir: workspace,
			Workspace:  workspace,
			Logger:     noopLogger{},
			Env:        append([]string{"LEAK=" + filepath.ToSlash(leak)}, env...),
		})
	}

	_, _, err := newConfined(outside).Exec(t.Context(), `cat < "$LEAK"`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside workspace")

	inside := filepath.Join(workspace, "ok.txt")
	require.NoError(t, os.WriteFile(inside, []byte("ok\n"), 0o644))
	_, _, err = newConfined(inside).Exec(t.Context(), `cat < "$LEAK"`)
	if err != nil {
		require.NotContains(t, err.Error(), "outside workspace", err.Error())
	}
}

// Compile-time guard: newConfinement returns the policy type jq/dispatch expect.
var _ *pathguard.Confinement = newConfinement("x", nil, false)
