package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/hackafterdark/phosphor/pkg/session"
	"github.com/spf13/cobra"
)

var (
	sessionPinCmd = &cobra.Command{
		Use:   "pin <id>",
		Short: "Pin a session to protect it from bulk deletion",
		Long:  "Mark a session as pinned. Pinned sessions are excluded from session pruning.",
		Args:  cobra.ExactArgs(1),
		RunE:  runSessionPin,
	}

	sessionUnpinCmd = &cobra.Command{
		Use:   "unpin <id>",
		Short: "Unpin a session",
		Long:  "Remove the pinned status from a session.",
		Args:  cobra.ExactArgs(1),
		RunE:  runSessionUnpin,
	}

	sessionPruneSessionsCmd = &cobra.Command{
		Use:   "prune-sessions",
		Short: "Bulk delete old sessions",
		Long: `Delete all non-pinned sessions last updated before the specified time.

This command bulk-deletes sessions that haven't been updated since the given
cutoff time. Pinned sessions are always excluded from deletion.

Examples:
  phosphor session prune-sessions --before 30d       # Delete sessions older than 30 days
  phosphor session prune-sessions --before 2024-01-01T00:00:00Z  # Delete sessions before a specific date
  phosphor session prune-sessions --before 7d --dry-run  # Preview what would be deleted
  phosphor session prune-sessions --before 7d --json     # JSON output for automation
`,
		RunE: runSessionPruneSessions,
	}

	sessionPruneSessionsBefore string
	sessionPruneSessionsJSON   bool
	sessionPruneSessionsDry    bool
)

func init() {
	sessionPinCmd.Flags().BoolVar(&sessionShowJSON, "json", false, "output in JSON format")
	sessionUnpinCmd.Flags().BoolVar(&sessionShowJSON, "json", false, "output in JSON format")
	sessionPruneSessionsCmd.Flags().StringVar(&sessionPruneSessionsBefore, "before", "",
		"Delete sessions last updated before this date (RFC3339 or relative: '24h', '7d', '30d')")
	sessionPruneSessionsCmd.MarkFlagRequired("before")
	sessionPruneSessionsCmd.Flags().BoolVar(&sessionPruneSessionsJSON, "json", false, "output in JSON format")
	sessionPruneSessionsCmd.Flags().BoolVar(&sessionPruneSessionsDry, "dry-run", false,
		"Show what would be deleted without actually deleting")
	sessionCmd.AddCommand(sessionPinCmd)
	sessionCmd.AddCommand(sessionUnpinCmd)
	sessionCmd.AddCommand(sessionPruneSessionsCmd)
}

func runSessionPin(cmd *cobra.Command, args []string) error {
	ctx, svc, cleanup, err := sessionSetup(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	sess, err := resolveSessionID(ctx, svc.sessions, args[0])
	if err != nil {
		return err
	}

	if err := svc.sessions.Pin(ctx, sess.ID); err != nil {
		return fmt.Errorf("failed to pin session: %w", err)
	}

	if sessionShowJSON {
		return outputSessionMutationJSON(cmd.OutOrStdout(), sessionMutationResult{
			ID:     session.HashID(sess.ID),
			UUID:   sess.ID,
			Title:  sess.Title,
			Pinned: true,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Pinned session %s (%s)\n", session.HashID(sess.ID)[:7], sess.Title)
	return nil
}

func runSessionUnpin(cmd *cobra.Command, args []string) error {
	ctx, svc, cleanup, err := sessionSetup(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	sess, err := resolveSessionID(ctx, svc.sessions, args[0])
	if err != nil {
		return err
	}

	if err := svc.sessions.Unpin(ctx, sess.ID); err != nil {
		return fmt.Errorf("failed to unpin session: %w", err)
	}

	if sessionShowJSON {
		return outputSessionMutationJSON(cmd.OutOrStdout(), sessionMutationResult{
			ID:     session.HashID(sess.ID),
			UUID:   sess.ID,
			Title:  sess.Title,
			Pinned: false,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Unpinned session %s (%s)\n", session.HashID(sess.ID)[:7], sess.Title)
	return nil
}

func runSessionPruneSessions(cmd *cobra.Command, _ []string) error {
	if sessionPruneSessionsBefore == "" {
		return fmt.Errorf("--before flag is required")
	}

	ctx, svc, cleanup, err := sessionSetup(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	cutoff, err := parseCutoff(sessionPruneSessionsBefore)
	if err != nil {
		return fmt.Errorf("invalid --before value: %w", err)
	}

	out := cmd.OutOrStdout()

	if sessionPruneSessionsDry {
		sessions, err := svc.sessions.ListPrunableSessions(ctx, cutoff)
		if err != nil {
			return err
		}

		if sessionPruneSessionsJSON {
			result := map[string]any{
				"dry_run":            true,
				"session_count":      len(sessions),
				"cutoff":             cutoff.Format(time.RFC3339),
				"sessions_to_delete": formatSessionsJSON(sessions),
			}
			return outputJSON(out, result)
		}

		if len(sessions) == 0 {
			fmt.Fprintln(out, "No sessions match the criteria.")
			return nil
		}

		hashStyle := lipgloss.NewStyle().Foreground(charmtone.Malibu)
		dateStyle := lipgloss.NewStyle().Foreground(charmtone.Damson)
		fmt.Fprintln(out, "Dry run: would delete the following sessions:")
		for _, s := range sessions {
			hash := session.HashID(s.ID)[:7]
			date := time.Unix(s.UpdatedAt, 0).Format(time.RFC3339)
			title := s.Title
			fmt.Fprintf(out, "  %s %s %s\n", hashStyle.Render(hash), dateStyle.Render(date), title)
		}
		return nil
	}

	count, err := svc.sessions.BulkDeleteSessions(ctx, cutoff)
	if err != nil {
		return err
	}

	if sessionPruneSessionsJSON {
		return outputJSON(out, map[string]any{
			"session_count": count,
			"cutoff":        cutoff.Format(time.RFC3339),
		})
	}

	fmt.Fprintf(out, "Deleted %d sessions last updated before %s\n", count, cutoff.Format(time.RFC3339))
	return nil
}

func formatSessionsJSON(sessions []session.Session) []map[string]any {
	result := make([]map[string]any, len(sessions))
	for i, s := range sessions {
		result[i] = map[string]any{
			"id":            session.HashID(s.ID),
			"uuid":          s.ID,
			"title":         s.Title,
			"updated_at":    time.Unix(s.UpdatedAt, 0).Format(time.RFC3339),
			"is_pinned":     s.IsPinned,
			"message_count": s.MessageCount,
		}
	}
	return result
}

func outputSessionMutationJSON(out interface{ Write([]byte) (int, error) }, r sessionMutationResult) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

func outputJSON(out interface{ Write([]byte) (int, error) }, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	out.Write(data)
	out.Write([]byte("\n"))
	return nil
}
