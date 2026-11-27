package config

import "testing"

func ptrInt(v int) *int { return &v }

func TestParseToolOutputMode(t *testing.T) {
	t.Parallel()

	if got := ParseToolOutputMode("collapsed"); got != ToolOutputCollapsed {
		t.Fatalf("ParseToolOutputMode(collapsed) = %q, want collapsed", got)
	}
	if got := ParseToolOutputMode("bogus"); got != DefaultToolOutputMode {
		t.Fatalf("ParseToolOutputMode(bogus) = %q, want default %q", got, DefaultToolOutputMode)
	}
}

func TestToolOutputModeFor(t *testing.T) {
	t.Parallel()

	var nilOutput *ToolOutput
	if got := nilOutput.ModeFor("bash"); got != DefaultToolOutputMode {
		t.Fatalf("nil ToolOutput.ModeFor = %q, want default %q", got, DefaultToolOutputMode)
	}

	out := &ToolOutput{}
	if got := out.ModeFor("bash"); got != DefaultToolOutputMode {
		t.Fatalf("empty ToolOutput.ModeFor = %q, want default %q", got, DefaultToolOutputMode)
	}

	out.Default = ToolOutputFull
	if got := out.ModeFor("bash"); got != ToolOutputFull {
		t.Fatalf("default override: ModeFor = %q, want full", got)
	}

	out.PerTool = map[string]ToolOutputMode{"bash": ToolOutputCollapsed}
	if got := out.ModeFor("bash"); got != ToolOutputCollapsed {
		t.Fatalf("per-tool override: ModeFor(bash) = %q, want collapsed", got)
	}
	if got := out.ModeFor("edit"); got != ToolOutputFull {
		t.Fatalf("per-tool override: ModeFor(edit) = %q, want full", got)
	}

	// Empty override values fall back to the default.
	out.PerTool["grep"] = ""
	if got := out.ModeFor("grep"); got != ToolOutputFull {
		t.Fatalf("empty override: ModeFor(grep) = %q, want full", got)
	}
}

func TestToolOutputLineLimit(t *testing.T) {
	t.Parallel()

	var nilOutput *ToolOutput
	if got := nilOutput.LineLimit(); got != 0 {
		t.Fatalf("nil ToolOutput.LineLimit = %d, want 0", got)
	}

	out := &ToolOutput{}
	if got := out.LineLimit(); got != 0 {
		t.Fatalf("unset LineLimit = %d, want 0", got)
	}

	out.Limit = ptrInt(25)
	if got := out.LineLimit(); got != 25 {
		t.Fatalf("LineLimit = %d, want 25", got)
	}

	// Non-positive limits are ignored so the UI default applies.
	out.Limit = ptrInt(0)
	if got := out.LineLimit(); got != 0 {
		t.Fatalf("zero LineLimit = %d, want 0", got)
	}
	out.Limit = ptrInt(-5)
	if got := out.LineLimit(); got != 0 {
		t.Fatalf("negative LineLimit = %d, want 0", got)
	}
}
