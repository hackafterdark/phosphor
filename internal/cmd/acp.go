package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/hackafterdark/phosphor/internal/backend"
	"github.com/hackafterdark/phosphor/internal/client"
	"github.com/hackafterdark/phosphor/internal/config"
	phosphorlog "github.com/hackafterdark/phosphor/internal/log"
	"github.com/hackafterdark/phosphor/internal/platform"
	"github.com/hackafterdark/phosphor/internal/platform/acp"
	"github.com/hackafterdark/phosphor/internal/server"
	"github.com/spf13/cobra"
)

var acpWorkspace string

func init() {
	acpCmd.Flags().StringVar(&acpWorkspace, "workspace", "", "Workspace directory (defaults to current directory)")
	rootCmd.AddCommand(acpCmd)
}

var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Start the ACP (Agent Client Protocol) v1 server over stdio",
	RunE: func(cmd *cobra.Command, _ []string) error {
		dataDir, err := cmd.Flags().GetString("data-dir")
		if err != nil {
			return fmt.Errorf("failed to get data directory: %v", err)
		}

		debug, err := cmd.Flags().GetBool("debug")
		if err != nil {
			return fmt.Errorf("failed to get debug flag: %v", err)
		}

		// Determine the workspace directory.
		workingDir := acpWorkspace
		if workingDir == "" {
			workingDir, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get working directory: %v", err)
			}
		}

		cfg, err := config.Load(workingDir, dataDir, debug)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %v", err)
		}

		// Set up logging.
		logFile := filepath.Join(config.GlobalCacheDir(), "acp", "phosphor.log")
		phosphorlog.SetupWithConfig(logFile, nil, debug, os.Stderr)

		// Shut down any running server to avoid database lock conflicts.
		if err := stopRunningServer(); err != nil {
			slog.Warn("Failed to stop running server", "error", err)
		}

		// Create the backend and governance layer.
		gov := platform.NewGovernance(cfg.Config())
		reg := platform.NewRegistry(slog.Default(), gov)

		backendCtx, backendCancel := context.WithCancel(cmd.Context())
		defer backendCancel()

		bk := backend.New(backendCtx, cfg, nil)

		// Create and register the ACP service.
		acpSrv := acp.NewService(bk, cfg, slog.Default())

		if err := gov.Check(acpSrv); err != nil {
			return fmt.Errorf("governance check failed: %w", err)
		}

		reg.Register(acpSrv)

		slog.Info("Starting ACP service...")

		if err := reg.StartAll(cmd.Context()); err != nil {
			return fmt.Errorf("failed to start services: %w", err)
		}

		sigch := make(chan os.Signal, 1)
		sigs := []os.Signal{os.Interrupt}
		sigs = append(sigs, addSignals(sigs)...)
		signal.Notify(sigch, sigs...)

		<-sigch
		slog.Info("Received interrupt signal...")

		ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
		defer cancel()

		slog.Info("Shutting down...")

		if err := reg.StopAll(ctx); err != nil {
			slog.Error("Failed to shutdown services", "error", err)
			return fmt.Errorf("failed to shutdown services: %v", err)
		}

		return nil
	},
}

// stopRunningServer sends a shutdown command to any running phosphor server
// via the /v1/control endpoint. This prevents database lock conflicts when
// ACP mode starts its own backend.
func stopRunningServer() error {
	hostURL, err := server.ParseHostURL(server.DefaultHost())
	if err != nil {
		return fmt.Errorf("failed to parse host URL: %w", err)
	}

	// On Unix, check if the socket file exists first to avoid unnecessary
	// dialing/timeouts. Windows named pipes cannot be stat'ed, so we skip
	// the stat check for non-unix schemes.
	if hostURL.Scheme == "unix" {
		if _, statErr := os.Stat(hostURL.Host); statErr != nil {
			if os.IsNotExist(statErr) {
				return nil // no server running
			}
			return statErr
		}
	}

	// Create a client and send shutdown.
	c, err := client.NewClient("", hostURL.Scheme, hostURL.Host)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := c.ShutdownServer(ctx); err != nil {
		// If the server is not running or we fail to connect, ignore the
		// error. We want to be lenient here since the server not running is
		// the goal.
		if isConnectionError(err) {
			return nil
		}
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	// Wait briefly for server to shut down.
	time.Sleep(500 * time.Millisecond)
	return nil
}

// isConnectionError reports whether the error represents a failure to
// connect to a non-running server socket or named pipe.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "refused") ||
		strings.Contains(errStr, "cannot find the file specified") ||
		strings.Contains(errStr, "no such file or directory") ||
		strings.Contains(errStr, "dial")
}
