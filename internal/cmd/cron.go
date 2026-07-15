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
	"github.com/hackafterdark/phosphor/internal/projects"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/hackafterdark/phosphor/pkg/skills"
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
			// Resolve working directory
			debug, _ := cmd.Flags().GetBool("debug")
			dataDir, _ := cmd.Flags().GetString("data-dir")

			cwd, err := ResolveCwd(cmd)
			if err != nil {
				return fmt.Errorf("failed to resolve working directory: %w", err)
			}

			// Initialize config store
			store, err := config.Init(cwd, dataDir, debug)
			if err != nil {
				return fmt.Errorf("failed to initialize config: %w", err)
			}

			cfg := store.Config()

			// Ensure data directory exists
			if err := os.MkdirAll(cfg.Options.DataDirectory, 0o700); err != nil {
				return fmt.Errorf("failed to create data directory: %w", err)
			}

			// Register project
			if err := projects.Register(cwd, cfg.Options.DataDirectory); err != nil {
				slog.Warn("Failed to register project", "error", err)
			}

			// Connect database
			ctx := cmd.Context()
			conn, err := db.Connect(ctx, cfg.Options.DataDirectory)
			if err != nil {
				return fmt.Errorf("failed to connect database: %w", err)
			}

			// Discover skills
			discoveryCfg := localSkillsDiscoveryConfig(store)
			allSkills, activeSkills, skillStates := skills.DiscoverFromConfig(discoveryCfg)
			skillsMgr := skills.NewManager(
				allSkills, activeSkills, skillStates,
				skills.WithWorkingDir(discoveryCfg.WorkingDir),
			)

			// Create app
			appInst, err := app.New(ctx, conn, store, skillsMgr)
			if err != nil {
				_ = conn.Close()
				return fmt.Errorf("failed to create app: %w", err)
			}

			// Create the cron service
			cronService := cron.NewService(appInst, store, slog.Default())

			if err := cronService.Start(ctx); err != nil {
				return fmt.Errorf("failed to start cron service: %w", err)
			}

			// Print startup information
			jobs := cronService.GetScheduledJobs()
			fmt.Println("Phosphor Cron Service")
			fmt.Println("====================")
			if len(jobs) == 0 {
				fmt.Println("No scheduled jobs found.")
			} else {
				fmt.Printf("Scheduled %d job(s):\n\n", len(jobs))
				for i, job := range jobs {
					title := job.Name
					if title == "" {
						title = job.Name
					}
					fmt.Printf("  %d. %s — %s\n", i+1, title, job.Schedule)
				}
			}
			fmt.Println("\nPress Ctrl+C to stop.")

			// Wait for interrupt signal
			sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			<-sigCtx.Done()
			slog.Info("Received shutdown signal")

			if err := cronService.Stop(ctx); err != nil {
				return fmt.Errorf("failed to stop cron service: %w", err)
			}
			slog.Info("Cron service stopped")

			return nil
		},
	}

	return cmd
}
