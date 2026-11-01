//go:build !windows

package sandbox

import "context"

// CleanupContainerGrants is a no-op outside Windows: the Unix isolators
// carry confinement in the exec.Cmd itself and no host DACLs are
// touched, so there is nothing to revoke.
func CleanupContainerGrants(context.Context) error { return nil }
