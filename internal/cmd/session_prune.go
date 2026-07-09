package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/hackafterdark/phosphor/internal/session"
	"github.com/spf13/cobra"
)

var (
	sessionPruneCmd = &cobra.Command{
		Use:   "prune <session-id>",
		Short: "Prune old messages from a stateless session",
		Long:  `Remove messages older than --before from a stateless session.`,
		Args:  cobra.ExactArgs(1),
		RunE:  runSessionPrune,
	}
	sessionPruneBefore string
	sessionPruneJSON   bool
	sessionPruneDryRun bool
)

var (
	sessionListStatelessCmd = &cobra.Command{
		Use:     "list-stateless",
		Short:   "List all stateless sessions, optionally filtered by service",
		Long:    `List all stateless sessions. Use --service to filter by service origin (e.g. openai-api, acp).`,
		RunE:    runSessionListStateless,
		Aliases: []string{"ls-stateless"},
	}
	sessionListStatelessService string
	sessionListStatelessJSON    bool
)

func init() {
	sessionPruneCmd.Flags().StringVar(&sessionPruneBefore, "before", "",
		"Prune messages before this date (RFC3339 or relative: '24h', '7d')")
	sessionPruneCmd.Flags().BoolVar(&sessionPruneJSON, "json", false, "output in JSON format")
	sessionPruneCmd.Flags().BoolVar(&sessionPruneDryRun, "dry-run", false,
		"Show what would be pruned without deleting")
	sessionCmd.AddCommand(sessionPruneCmd)

	sessionListStatelessCmd.Flags().StringVarP(&sessionListStatelessService, "service", "s", "",
		"Filter by service (e.g., 'openai-api', 'acp')")
	sessionListStatelessCmd.Flags().BoolVar(&sessionListStatelessJSON, "json", false, "output in JSON format")
	sessionCmd.AddCommand(sessionListStatelessCmd)
}

func parseCutoff(s string) (time.Time, error) {
	// Try relative duration first.
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d), nil
	}
	// Try RFC3339.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid duration or timestamp: %s", s)
}

func runSessionPrune(cmd *cobra.Command, args []string) error {
	if sessionPruneBefore == "" {
		return fmt.Errorf("--before flag is required")
	}

	ctx, svc, cleanup, err := sessionSetup(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	sess, err := resolveSessionID(ctx, svc.sessions, args[0])
	if err != nil {
		return err
	}

	if !sess.IsStateless {
		return fmt.Errorf("session %s is not stateless", session.HashID(sess.ID)[:7])
	}

	cutoff, err := parseCutoff(sessionPruneBefore)
	if err != nil {
		return fmt.Errorf("invalid --before value: %w", err)
	}

	out := cmd.OutOrStdout()

	if sessionPruneDryRun {
		count, err := svc.sessions.CountPrunableMessages(ctx, sess.ID, cutoff)
		if err != nil {
			return err
		}

		if sessionPruneJSON {
			return outputSessionPruneJSON(out, map[string]any{
				"dry_run":              true,
				"session_id":           session.HashID(sess.ID),
				"session_title":        sess.Title,
				"service":              sess.Service,
				"messages_would_prune": count,
				"cutoff":               cutoff.Format(time.RFC3339),
			})
		}

		fmt.Fprintf(out, "Dry run: would prune %d messages from session %s (%s)\n",
			count, session.HashID(sess.ID)[:12], sess.Service)
		return nil
	}

	count, err := svc.sessions.PruneMessages(ctx, sess.ID, cutoff)
	if err != nil {
		return err
	}

	if sessionPruneJSON {
		return outputSessionPruneJSON(out, map[string]any{
			"session_id":        session.HashID(sess.ID),
			"session_title":     sess.Title,
			"service":           sess.Service,
			"messages_pruned":   count,
			"cutoff":            cutoff.Format(time.RFC3339),
		})
	}

	fmt.Fprintf(out, "Pruned %d messages from session %s (%s)\n",
		count, session.HashID(sess.ID)[:12], sess.Service)
	return nil
}

func runSessionListStateless(cmd *cobra.Command, _ []string) error {
	ctx, svc, cleanup, err := sessionSetup(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	sessions, err := svc.sessions.ListStatelessSessions(ctx, sessionListStatelessService)
	if err != nil {
		return fmt.Errorf("failed to list stateless sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No stateless sessions found.")
		return nil
	}

	out := cmd.OutOrStdout()

	if sessionListStatelessJSON {
		type statelessSessionJSON struct {
			ID           string `json:"id"`
			UUID         string `json:"uuid"`
			Title        string `json:"title"`
			Service      string `json:"service"`
			MessageCount int64  `json:"message_count"`
			CreatedAt    string `json:"created_at"`
			UpdatedAt    string `json:"updated_at"`
		}

		output := make([]statelessSessionJSON, len(sessions))
		for i, s := range sessions {
			output[i] = statelessSessionJSON{
				ID:           session.HashID(s.ID),
				UUID:         s.ID,
				Title:        s.Title,
				Service:      s.Service,
				MessageCount: s.MessageCount,
				CreatedAt:    time.Unix(s.CreatedAt, 0).Format(time.RFC3339),
				UpdatedAt:    time.Unix(s.UpdatedAt, 0).Format(time.RFC3339),
			}
		}
		enc := json.NewEncoder(out)
		enc.SetEscapeHTML(false)
		return enc.Encode(output)
	}

	// Human-readable output.
	hashStyle := lipgloss.NewStyle().Foreground(charmtone.Malibu)
	dateStyle := lipgloss.NewStyle().Foreground(charmtone.Damson)
	serviceStyle := lipgloss.NewStyle().Foreground(charmtone.Charple).Bold(true)

	for _, s := range sessions {
		hash := session.HashID(s.ID)[:7]
		date := time.Unix(s.CreatedAt, 0).Format(time.RFC3339)
		service := serviceStyle.Render(s.Service)
		fmt.Fprintf(out, "%s %s [%s] %s (messages: %d)\n",
			hashStyle.Render(hash), dateStyle.Render(date), service, s.Title, s.MessageCount)
	}

	return nil
}

// outputSessionPruneJSON writes a JSON object to w.
func outputSessionPruneJSON(w interface{ Write([]byte) (int, error) }, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}
