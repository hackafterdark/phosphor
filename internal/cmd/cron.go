package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/hackafterdark/phosphor/internal/app"
	"github.com/hackafterdark/phosphor/internal/platform/cron"
)

// NewCronCommand returns a new command for running the cron service.
func NewCronCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Run the cron service for scheduled jobs",
		Long: `Start the cron service to run scheduled jobs.
The cron service runs in the foreground and will continue to run
until interrupted by a signal (Ctrl+C).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create the app with nil parameters to use defaults
			app, err := app.New(context.Background(), nil, nil, nil)
			if err != nil {
				return fmt.Errorf("failed to create app: %w", err)
			}

			// Create the cron service
			cronService := cron.NewService(app, app.Store(), slog.Default())

			// Start the cron service in a goroutine
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if err := cronService.Start(ctx); err != nil {
				return fmt.Errorf("failed to start cron service: %w", err)
			}
			slog.Info("Cron service started")

			// Wait for interrupt signal
			<-ctx.Done()
			slog.Info("Received shutdown signal")

			// Stop the cron service
			if err := cronService.Stop(ctx); err != nil {
				return fmt.Errorf("failed to stop cron service: %w", err)
			}
			slog.Info("Cron service stopped")

			return nil
		},
	}

	return cmd
}