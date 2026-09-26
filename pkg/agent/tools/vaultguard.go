package tools

import (
	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/memory"
)

// vaultWriteGuard is the tool-only write fence for the memory vault. The vault is
// the highest-privilege injection surface in the product — its files feed the
// system prompt — so only the `memory` tool may mutate it, which is what lets
// schema, sanitization and index sync be guarantees rather than hopes. The generic
// edit/write/append/multiedit tools therefore refuse any target that resolves into
// a vault and point the caller at the tool that enforces those invariants.
func vaultWriteGuard(workingDir, absFilePath string) (fantasy.ToolResponse, bool) {
	if !memory.IsVaultPath(workingDir, absFilePath) {
		return fantasy.ToolResponse{}, false
	}
	msg := "The memory vault is written only through the `memory` tool, which enforces the entry " +
		"schema, secret and injection sanitization, and index sync. To record durable context use " +
		"`memory(op=add, ...)`; to recall it use `memory_search`. Direct edits to files under " +
		"`.phosphor/memory/` are refused."
	return fantasy.NewTextErrorResponse(msg), true
}
