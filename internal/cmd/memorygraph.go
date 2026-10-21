package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hackafterdark/phosphor/internal/server"
	"github.com/hackafterdark/phosphor/pkg/config"
)

// memoryGraphDefaultPort is the standalone viewer port. 8643 is the
// openai-api/mermaid service; the viewer rides its own port so it works
// regardless of whether that service is enabled.
const memoryGraphDefaultPort = 8644

// NewMemoryGraphCommand returns a new command for running the memory graph
// viewer as a standalone service.
func NewMemoryGraphCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memorygraph",
		Short: "Run the memory graph viewer server",
		Long: `Start the memory graph viewer to browse the phosphor memory vault.

The viewer runs in the foreground and serves the constellation view and its
live data endpoint until interrupted by a signal (Ctrl+C). Host and port can
be overridden with the memory-graph service entry in phosphor.json:

  "services": { "memory-graph": { "enabled": true, "host": "127.0.0.1", "port": 8644 } }

The viewer serves raw memory entries to any client that can reach the
port, so only loopback hosts are accepted. If the service entry exists
with "enabled": false (or omitted), the command refuses to start.

With --report FILE the current graph is frozen into a single self-contained
HTML file that opens offline with no server, and the command exits.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := ResolveCwd(cmd)
			if err != nil {
				return fmt.Errorf("failed to resolve working directory: %w", err)
			}
			server.SetMemoryGraphWorkspace(cwd)

			if report, _ := cmd.Flags().GetString("report"); report != "" {
				return writeMemoryGraphReport(cmd.Context(), report)
			}

			host, port, err := memoryGraphHostPort(cwd)
			if err != nil {
				return err
			}
			if !isLoopbackHost(host) {
				return fmt.Errorf(
					"memory-graph host %q is not a loopback address; the viewer serves raw memory entries with no authentication, so only 127.0.0.1, ::1, or localhost may be bound",
					host)
			}
			addr := net.JoinHostPort(host, strconv.Itoa(port))

			// Bind before printing the URL so a port conflict fails fast
			// instead of leaving the user on a dead page.
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("failed to listen on %s: %w", addr, err)
			}

			srv := &http.Server{
				Handler:           server.NewMemoryGraphHandler(),
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       30 * time.Second,
				WriteTimeout:      30 * time.Second,
				IdleTimeout:       120 * time.Second,
			}

			errCh := make(chan error, 1)
			go func() { errCh <- srv.Serve(ln) }()

			fmt.Println("Phosphor Memory Graph Viewer")
			fmt.Println("============================")
			fmt.Printf("Open http://%s/view.html\n", ln.Addr())
			fmt.Println("\nPress Ctrl+C to stop.")

			sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			select {
			case <-sigCtx.Done():
				slog.Info("Received shutdown signal")
			case err := <-errCh:
				return fmt.Errorf("memory graph server failed: %w", err)
			}

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("failed to stop memory graph server: %w", err)
			}
			slog.Info("Memory graph viewer stopped")
			return nil
		},
	}

	cmd.Flags().String("report", "", "Write a self-contained snapshot report HTML file and exit")

	return cmd
}

// writeMemoryGraphReport freezes the current memory graph into a single
// self-contained HTML file that opens offline with no server or polling.
func writeMemoryGraphReport(ctx context.Context, path string) error {
	dbPath := server.MemoryGraphDBPath()
	if dbPath == "" {
		return fmt.Errorf("no memory vault found")
	}
	page, err := server.RenderMemoryGraphReport(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("failed to render memory graph report: %w", err)
	}
	if err := os.WriteFile(path, page, 0o600); err != nil {
		return fmt.Errorf("failed to write report: %w", err)
	}
	// os.WriteFile keeps the mode of a pre-existing file; force the
	// snapshot private even when the destination already existed.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("failed to restrict report permissions: %w", err)
	}
	fmt.Printf("wrote snapshot report: %s\n", path)
	return nil
}

// memoryGraphHostPort resolves the viewer bind address from the memory-graph
// service entry in phosphor.json, falling back to the defaults when the
// config is missing or unreadable.
func memoryGraphHostPort(cwd string) (string, int, error) {
	host, port := "127.0.0.1", memoryGraphDefaultPort
	store, err := config.Init(cwd, "", false)
	if err != nil {
		return host, port, nil
	}
	if entry, ok := store.Config().Services["memory-graph"]; ok {
		if !entry.Enabled {
			return host, port, fmt.Errorf(
				"memory-graph service is disabled in the config; set \"enabled\": true under services.memory-graph in phosphor.json to run the viewer")
		}
		if entry.Host != "" {
			host = entry.Host
		}
		if entry.Port != 0 {
			port = entry.Port
		}
	}
	return host, port, nil
}

// isLoopbackHost reports whether host is a loopback address or localhost.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
