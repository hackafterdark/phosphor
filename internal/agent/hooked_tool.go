package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/agent/tools"
	"github.com/hackafterdark/phosphor/internal/hooks"
	"github.com/hackafterdark/phosphor/internal/permission"
	"github.com/tidwall/sjson"
)

// hookedTool wraps a fantasy.AgentTool to run PreToolUse hooks and/or fiduciary
// security policy checks before delegating to the inner tool.
type hookedTool struct {
	inner         fantasy.AgentTool
	runner        *hooks.Runner
	activeProfile string
}

func newHookedTool(inner fantasy.AgentTool, runner *hooks.Runner, activeProfile string) *hookedTool {
	return &hookedTool{
		inner:         inner,
		runner:        runner,
		activeProfile: activeProfile,
	}
}

// wrapToolsWithHooks returns a tool slice with each entry wrapped in a
// hookedTool. Returns the original slice unchanged when runner is nil, activeProfile
// is not "fiduciary", or when isSubAgent is true (unless fiduciary profile is active).
func wrapToolsWithHooks(tools []fantasy.AgentTool, runner *hooks.Runner, isSubAgent bool, activeProfile string) []fantasy.AgentTool {
	if runner == nil && activeProfile != "fiduciary" {
		return tools
	}
	if isSubAgent && activeProfile != "fiduciary" {
		return tools
	}
	out := make([]fantasy.AgentTool, len(tools))
	for i, tool := range tools {
		out[i] = newHookedTool(tool, runner, activeProfile)
	}
	return out
}

func (h *hookedTool) Info() fantasy.ToolInfo {
	return h.inner.Info()
}

func (h *hookedTool) ProviderOptions() fantasy.ProviderOptions {
	return h.inner.ProviderOptions()
}

func (h *hookedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	h.inner.SetProviderOptions(opts)
}

var restrictedCmds = []string{
	"chown", "chmod", "passwd", "sudo", "useradd", "usermod", "userdel",
	"groupadd", "groupmod", "groupdel", "systemctl", "service", "iptables", "ufw",
}

func isFiduciaryViolation(cmd string) (bool, string) {
	lowerCmd := strings.ToLower(cmd)

	// Check restricted paths
	restrictedPaths := []string{"/etc", "/var", "~/.ssh", ".ssh"}
	for _, p := range restrictedPaths {
		if strings.Contains(lowerCmd, p) {
			return true, fmt.Sprintf("Fiduciary Policy Violation: Restricted Path %q", p)
		}
		winP := strings.ReplaceAll(p, "/", "\\")
		if strings.Contains(lowerCmd, winP) {
			return true, fmt.Sprintf("Fiduciary Policy Violation: Restricted Path %q", winP)
		}
	}

	// Check restricted commands
	for _, c := range restrictedCmds {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(c) + `\b`)
		if re.MatchString(lowerCmd) {
			return true, fmt.Sprintf("Fiduciary Policy Violation: Restricted Command %q", c)
		}
	}

	return false, ""
}

func (h *hookedTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	// 1. Perform Fiduciary Security Guardrails first
	if h.activeProfile == "fiduciary" {
		toolName := strings.ToLower(call.Name)
		if toolName == "bash" || toolName == "cmd" || toolName == "powershell" || toolName == "shell" {
			var args struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal([]byte(call.Input), &args); err == nil && args.Command != "" {
				if violated, reason := isFiduciaryViolation(args.Command); violated {
					resp := fantasy.NewTextErrorResponse(reason)
					resp.StopTurn = true
					return resp, nil
				}
			}
		}
	}

	// If no hook runner is configured, bypass hook execution.
	if h.runner == nil {
		return h.inner.Run(ctx, call)
	}

	sessionID := tools.GetSessionFromContext(ctx)
	result, err := h.runner.Run(ctx, hooks.EventPreToolUse, sessionID, call.Name, call.Input)
	if err != nil {
		slog.Warn("Hook execution error, proceeding with tool call",
			"tool", call.Name, "error", err)
	}

	if result.Decision == hooks.DecisionDeny || result.Halt {
		reason := fmt.Sprintf("Tool call blocked by hook. Reason: %s", result.Reason)
		if result.Halt {
			reason = fmt.Sprintf("Turn halted by hook. Reason: %s", result.Reason)
		}
		resp := fantasy.NewTextErrorResponse(reason)
		// Halt ends the whole turn; a plain deny only blocks this tool
		// call so the model can see the error and try something else.
		resp.StopTurn = result.Halt
		resp.Metadata = hookMetadataJSON(result)
		return resp, nil
	}

	if result.UpdatedInput != "" {
		call.Input = result.UpdatedInput
	}

	// An explicit allow from a hook pre-approves the permission prompt for
	// this tool call. Deny is already handled above; silence falls through
	// to the normal permission flow.
	if result.Decision == hooks.DecisionAllow {
		ctx = permission.WithHookApproval(ctx, call.ID)
	}

	resp, err := h.inner.Run(ctx, call)
	if err != nil {
		return resp, err
	}

	if result.Context != "" {
		if resp.Content != "" {
			resp.Content += "\n"
		}
		resp.Content += result.Context
	}

	resp.Metadata = mergeHookMetadata(resp.Metadata, result)
	return resp, nil
}

// buildHookMetadata creates a HookMetadata from an AggregateResult.
func buildHookMetadata(result hooks.AggregateResult) hooks.HookMetadata {
	return hooks.HookMetadata{
		HookCount:    result.HookCount,
		Decision:     result.Decision.String(),
		Halt:         result.Halt,
		Reason:       result.Reason,
		InputRewrite: result.UpdatedInput != "",
		Hooks:        result.Hooks,
	}
}

// hookMetadataJSON builds a JSON string containing only the hook metadata.
func hookMetadataJSON(result hooks.AggregateResult) string {
	meta := buildHookMetadata(result)
	data, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return `{"hook":` + string(data) + `}`
}

// mergeHookMetadata injects hook metadata into existing tool metadata.
func mergeHookMetadata(existing string, result hooks.AggregateResult) string {
	if result.HookCount == 0 {
		return existing
	}
	meta := buildHookMetadata(result)
	data, err := json.Marshal(meta)
	if err != nil {
		return existing
	}
	if existing == "" {
		existing = "{}"
	}
	merged, err := sjson.SetRaw(existing, "hook", string(data))
	if err != nil {
		return existing
	}
	return merged
}
