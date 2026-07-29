package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hackafterdark/phosphor/internal/backend"
	"github.com/hackafterdark/phosphor/internal/client"
	"github.com/hackafterdark/phosphor/internal/platform"
	"github.com/hackafterdark/phosphor/internal/platform/acp"
	"github.com/hackafterdark/phosphor/internal/server"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/spf13/cobra"
)

var acpWorkspace string

func init() {
	acpCmd.Flags().StringVar(&acpWorkspace, "workspace", "", "Workspace directory (defaults to current directory)")
	rootCmd.AddCommand(acpCmd)
}

var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Start the Agent Client Protocol service",
	Long:  "Start the Agent Client Protocol (ACP) service over standard input/output.",
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

		// Configure logging to all standard log directories:
		// 1. DataDirectory/logs/phosphor-acp.log (matches phosphor.log location)
		// 2. workingDir/.phosphor/logs/phosphor-acp.log
		// 3. GlobalCacheDir/phosphor-acp.log (fallback)
		logPaths := []string{
			filepath.Join(cfg.Config().Options.DataDirectory, "logs", "phosphor-acp.log"),
			filepath.Join(workingDir, ".phosphor", "logs", "phosphor-acp.log"),
			filepath.Join(config.GlobalCacheDir(), "phosphor-acp.log"),
		}

		// Deduplicate log paths
		seenPaths := make(map[string]bool)
		var uniqueLogPaths []string
		for _, p := range logPaths {
			abs, err := filepath.Abs(p)
			if err != nil {
				abs = p
			}
			if !seenPaths[abs] {
				seenPaths[abs] = true
				uniqueLogPaths = append(uniqueLogPaths, abs)
			}
		}

		var logWriters []io.Writer
		for _, p := range uniqueLogPaths {
			if err := os.MkdirAll(filepath.Dir(p), 0755); err == nil {
				if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
					logWriters = append(logWriters, f)
				}
			}
		}

		var acpHandler slog.Handler
		if len(logWriters) > 0 {
			acpHandler = slog.NewJSONHandler(io.MultiWriter(logWriters...), &slog.HandlerOptions{
				Level:     slog.LevelDebug,
				AddSource: true,
			})
		} else {
			acpHandler = slog.NewTextHandler(io.Discard, nil)
		}

		acpLogger := slog.New(acpHandler)
		slog.SetDefault(acpLogger)

		acpLogger.Info("ACP logger initialized", "workspace", workingDir, "logPaths", uniqueLogPaths)

		// Shut down any running server to avoid database lock conflicts.
		if err := stopRunningServer(); err != nil {
			acpLogger.Warn("Failed to stop running server", "error", err)
		}

		// Create the backend and governance layer.
		gov := platform.NewGovernance(cfg.Config())
		reg := platform.NewRegistry(acpLogger, gov)

		backendCtx, backendCancel := context.WithCancel(cmd.Context())
		defer backendCancel()

		bk := backend.New(backendCtx, cfg, nil)

		// Create and register the ACP service.
		acpSrv := acp.NewService(bk, cfg, acpLogger)

		if err := gov.Check(acpSrv); err != nil {
			return fmt.Errorf("governance check failed: %w", err)
		}

		reg.Register(acpSrv)

		slog.Info("Starting ACP service...")

		if err := reg.StartAll(cmd.Context()); err != nil {
			return fmt.Errorf("failed to start services: %w", err)
		}

		acpLogger.Info("ACP service loop completed, shutting down")

		ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
		defer cancel()

		if err := reg.StopAll(ctx); err != nil {
			acpLogger.Error("Failed to shutdown services", "error", err)
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
