package model

import (
	"testing"

	"github.com/hackafterdark/phosphor/internal/ui/dialog"
	"github.com/hackafterdark/phosphor/pkg/permission"
	"github.com/stretchr/testify/require"
)

// newTestUIForPermissions builds a UI with a chat, dialog overlay, and
// common context sufficient to exercise handlePermissionNotification.
func newTestUIForPermissions() *UI {
	u := newTestUI()
	u.dialog = dialog.NewOverlay()
	return u
}

func TestHandlePermissionNotification_RemoteGrantClosesDialog(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-1",
		ToolCallID: "tool-call-X",
		ToolName:   "bash",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))
	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID))

	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-X",
		Granted:    true,
		RequestID:  "perm-1",
	})

	require.False(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"granted notification should close matching permissions dialog")
}

func TestHandlePermissionNotification_RemoteDenyClosesDialog(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-2",
		ToolCallID: "tool-call-Y",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))

	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-Y",
		Denied:     true,
		RequestID:  "perm-2",
	})

	require.False(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"denied notification should close matching permissions dialog")
}

func TestHandlePermissionNotification_InitialPendingDoesNotClose(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-3",
		ToolCallID: "tool-call-Z",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))

	// The initial notification published by permission.Request is
	// neither granted nor denied; it must not dismiss the dialog.
	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-Z",
	})

	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"initial pending notification must not close the dialog")
}

func TestHandlePermissionNotification_DifferentToolCallIDDoesNotClose(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-4",
		ToolCallID: "tool-call-A",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))

	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-B",
		Granted:    true,
	})

	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"notification for unrelated tool call must not close the dialog")
}

// The escalation race: a bash command raises the execute-permission request
// and then, for the same tool call, a sandbox escalation request. When the
// execute grant is resolved (or auto-approved), its terminal notification
// must not close the escalation dialog — otherwise the user cannot answer
// the escalation prompt and the tool hangs until the run is cancelled.
func TestHandlePermissionNotification_SameToolCallIDDifferentRequest(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	escalation := permission.PermissionRequest{
		ID:         "perm-escalation",
		ToolCallID: "tool-call-shared",
		ToolName:   "bash",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, escalation))

	// Terminal notification for the execute request of the same tool call.
	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-shared",
		Granted:    true,
		RequestID:  "perm-execute",
	})

	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"a different request's resolution must not close the escalation dialog")

	// Shortcut grants (auto-approve) carry no request ID at all and must
	// never close the dialog either.
	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-shared",
		Granted:    true,
	})

	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"shortcut grant without request ID must not close the dialog")

	// The escalation request's own resolution still closes it.
	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-shared",
		Denied:     true,
		RequestID:  "perm-escalation",
	})

	require.False(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"the escalation request's own resolution must close the dialog")
}
