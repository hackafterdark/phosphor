package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/hackafterdark/phosphor/internal/app"
	"github.com/hackafterdark/phosphor/internal/config"
	phosphorlog "github.com/hackafterdark/phosphor/internal/log"
	"github.com/hackafterdark/phosphor/internal/platform"
	"github.com/hackafterdark/phosphor/internal/platform/cron"
	"github.com/hackafterdark/phosphor/internal/platform/httpapi"
	"github.com/hackafterdark/phosphor/internal/server"
	"github.com/spf13/cobra"
)

var serverHost string
var openaiHost string
var openaiPort int
var openaiEnabled bool
var workspacePath string

func init() {
	serverCmd.Flags().StringVarP(&serverHost, "host", "H", server.DefaultHost(), "Server host (TCP or Unix socket)")
	serverCmd.Flags().StringVar(&openaiHost, "openai-host", "", "OpenAI API host (TCP)")
	serverCmd.Flags().IntVar(&openaiPort, "openai-port", 0, "OpenAI API port")
	serverCmd.Flags().BoolVar(&openaiEnabled, "openai-enabled", true, "Enable the OpenAI API service")
	serverCmd.Flags().StringVar(&workspacePath, "workspace", "", "Workspace directory path (defaults to current working directory)")
	rootCmd.AddCommand(serverCmd)
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the Phosphor server",
	RunE: func(cmd *cobra.Command, _ []string) error {
		dataDir, err := cmd.Flags().GetString("data-dir")
		if err != nil {
			return fmt.Errorf("failed to get data directory: %v", err)
		}
		debug, err := cmd.Flags().GetBool("debug")
		if err != nil {
			return fmt.Errorf("failed to get debug flag: %v", err)
		}

		cfg, err := config.Load(config.GlobalWorkspaceDir(), dataDir, debug)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %v", err)
		}

		// Apply CLI overrides to openai-api configuration
		openaiEntry := cfg.Config().Services["openai-api"]
		if cmd.Flags().Changed("openai-host") {
			openaiEntry.Host = openaiHost
		}
		if cmd.Flags().Changed("openai-port") {
			openaiEntry.Port = openaiPort
		}
		if cmd.Flags().Changed("openai-enabled") {
			openaiEntry.Enabled = openaiEnabled
		}
		cfg.Config().Services["openai-api"] = openaiEntry

		var hostURL *url.URL
		if cmd.Flags().Changed("host") {
			var err error
			hostURL, err = server.ParseHostURL(serverHost)
			if err != nil {
				return fmt.Errorf("invalid server host: %v", err)
			}
		} else if entry, ok := cfg.Config().Services["http-api"]; ok && (entry.Host != "" || entry.Port != 0) {
			h := entry.Host
			if h == "" {
				h = "127.0.0.1"
			}
			hostURL = &url.URL{
				Scheme: "tcp",
				Host:   fmt.Sprintf("%s:%d", h, entry.Port),
			}
		} else {
			var err error
			hostURL, err = server.ParseHostURL(serverHost)
			if err != nil {
				return fmt.Errorf("invalid server host: %v", err)
			}
		}

		logFile := filepath.Join(config.GlobalCacheDir(), "server-"+safeHostName(hostURL), "phosphor.log")

		if term.IsTerminal(os.Stderr.Fd()) {
			phosphorlog.SetupWithConfig(logFile, nil, debug, os.Stderr)
		} else {
			phosphorlog.SetupWithConfig(logFile, nil, debug)
		}

		// Separate logger for HTTP server request logs, written to
		// <workspace>/.phosphor/logs/phosphor-server.log so that HTTP
		// request/info/error logs stay out of the main phosphor.log.
		cwd, _ := os.Getwd()
		httpLogFile := filepath.Join(cwd, ".phosphor", "logs", "phosphor-server.log")
		var httpLogLevel slog.Level = slog.LevelInfo
		if cfg.Config().Logging != nil {
			switch cfg.Config().Logging.Level {
			case "debug":
				httpLogLevel = slog.LevelDebug
			case "off":
				httpLogLevel = slog.LevelError // effectively disabled
			}
		}
		var httpLogger *slog.Logger
		if err := os.MkdirAll(filepath.Dir(httpLogFile), 0o700); err == nil {
			if f, err := os.OpenFile(httpLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				httpLogger = slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{
					Level:     httpLogLevel,
					AddSource: true,
				}))
				// Don't close the file — let it stay open for the process lifetime
				_ = f
			} else {
				fmt.Fprintf(os.Stderr, "Warning: failed to open HTTP log file %s: %v\n", httpLogFile, err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Warning: failed to create HTTP log directory: %v\n", err)
		}
		if httpLogger == nil {
			fmt.Fprintf(os.Stderr, "Warning: HTTP logger not initialized, logs will go to phosphor.log\n")
		}

		gov := platform.NewGovernance(cfg.Config())
		reg := platform.NewRegistry(slog.Default(), gov)

		// Determine workspace path
		var wsDir string
		if workspacePath != "" {
			wsDir = workspacePath
		} else {
			var err error
			wsDir, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get working directory: %v", err)
			}
		}
		slog.Info("Using workspace directory", "path", wsDir)

		var httpSrvLogger *slog.Logger = httpLogger
		srv := httpapi.NewService(cfg, hostURL.Scheme, hostURL.Host, wsDir, httpSrvLogger)

		if err := gov.Check(srv); err != nil {
			if httpLogger != nil {
				httpLogger.Error("Governance check failed", "error", err)
			}
			return fmt.Errorf("governance check failed: %w", err)
		}

		reg.Register(srv)

		// Instantiate and register the cron service.
		cronSrv := cron.NewServiceWithProvider(func() *app.App {
			backend := srv.Backend()
			if backend == nil {
				return nil
			}
			workspaces := backend.ListWorkspaces()
			if len(workspaces) == 0 {
				return nil
			}
			ws, err := backend.GetWorkspace(workspaces[0].ID)
			if err != nil {
				return nil
			}
			return ws.App
		}, cfg, slog.Default())

		if err := gov.Check(cronSrv); err != nil {
			if httpLogger != nil {
				httpLogger.Error("Governance check failed for cron service", "error", err)
			}
			return fmt.Errorf("governance check failed for cron service: %w", err)
		}

		reg.Register(cronSrv)

		// Start all registered services
		if err := reg.StartAll(cmd.Context()); err != nil {
			if httpLogger != nil {
				httpLogger.Error("Failed to start services", "error", err)
			}
			return fmt.Errorf("failed to start services: %w", err)
		}

		sigch := make(chan os.Signal, 1)
		sigs := []os.Signal{os.Interrupt}
		sigs = append(sigs, addSignals(sigs)...)
		signal.Notify(sigch, sigs...)

		// Print startup summary to stderr so the user sees it in the terminal.
		openaiCfg := srv.Describe()
		fmt.Fprintf(os.Stderr, "\n%s\n", openaiCfg)
		fmt.Fprintf(os.Stderr, "  Press Ctrl+C to stop.\n\n")
		fmt.Fprintf(os.Stderr, "  Logs: %s\n", httpLogFile)

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
