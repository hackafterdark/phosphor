package tools

import (
	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/filepathext"
)

// configWriteGuard is the tool-only write fence for workspace/project config
// files. The config (phosphor.json at the workspace root or under .phosphor/)
// defines the security posture itself — floors, egress policy, sandbox mode,
// provider endpoints — so it is writable only through direct user action:
// editing the file by hand or the in-app configuration UI. Every agent write
// tool (write/edit/multiedit/append/download) funnels through this guard;
// reads are unaffected. This closes the prompt-injection path where hostile
// repo/tool content steers the agent into disarming its own controls.
//
// It complements (does not replace) the config-floor hardening in
// pkg/config/hardening.go: the floor clamps what a *loaded* workspace config
// may weaken; this guard stops the agent from *creating* such a config in the
// first place.
func configWriteGuard(absFilePath, absWorkingDir string) (fantasy.ToolResponse, bool) {
	if err := filepathext.ValidateConfigWrite(absFilePath, absWorkingDir); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), true
	}
	return fantasy.ToolResponse{}, false
}
