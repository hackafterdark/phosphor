package pathguard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// failingCommand is the real commit invocation that was refused by the
// static validator: a drive root appears inside heredoc data.
const failingCommand = `git commit -m "$(cat <<'EOF'
fix: block mid-command cd escapes from the workspace sandbox

cd is executed inside the shell interpreter and bypassed every path
check, so compound commands like "cd C:/ && cat Windows/win.ini" read
and wrote files outside the workspace while the post-command cwd reset
hid the violation.

ஃ Generated with Phosphor

Assisted-by: Phosphor:CYBER-FROST-3.8-EXL3-SAGE-3.87bpw
EOF
)" && git status`

func TestHeredocBodyIsData(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateCommandPaths(failingCommand, t.TempDir()))

	// A real operand on the same line as the introducer is still flagged.
	err := ValidateCommandPaths("cat <<EOF > /etc/evil.txt\nbody\nEOF", t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Security violation")

	// Whitespace between the introducer and the delimiter is POSIX-legal and
	// must mask the body too.
	require.NoError(t, ValidateCommandPaths("cat << EOF\nC:/drive root text\nEOF", t.TempDir()))

	// Stacked heredocs: bodies stream in source order.
	stacked := "cat <<A <<B\nC:/one\nA\nC:/two\nB\n"
	require.NoError(t, ValidateCommandPaths(stacked, t.TempDir()))

	// Bare-delimiter introducer with tabs (<<-).
	dashy := "cat <<-EOF\n\t../../x\n\tEOF\n"
	require.NoError(t, ValidateCommandPaths(dashy, t.TempDir()))

	// Expanded delimiter: unmatchable, body falls back to token scanning.
	expanded := "cat <<$DELIM\nplain words only\nEOF\n"
	require.NoError(t, ValidateCommandPaths(expanded, t.TempDir()))

	// Herestrings carry no body.
	require.NoError(t, ValidateCommandPaths("grep x <<< C:/data", t.TempDir()))
}

func TestHeredocBodyNotRewritten(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	body := "Added C:/ support\n"
	cmd := "cat <<EOF > out.txt\n" + body + "EOF\n"
	got := CorrectCommandPaths(cmd, workspace)
	require.True(t, strings.Contains(got, body), "heredoc body must survive rewriting byte-for-byte:\n%s", got)
}
