package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
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

  "services": { "memory-graph": { "host": "127.0.0.1", "port": 8644 } }`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := ResolveCwd(cmd)
			if err != nil {
				return fmt.Errorf("failed to resolve working directory: %w", err)
			}
			server.SetMemoryGraphWorkspace(cwd)

			host, port := memoryGraphHostPort(cwd)
			addr := fmt.Sprintf("%s:%d", host, port)

			// Bind before printing the URL so a port conflict fails fast
			// instead of leaving the user on a dead page.
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("failed to listen on %s: %w", addr, err)
			}

			srv := &http.Server{Handler: server.NewMemoryGraphHandler()}

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

	return cmd
}

// memoryGraphHostPort resolves the viewer bind address from the memory-graph
// service entry in phosphor.json, falling back to the defaults when the
// config is missing or unreadable.
func memoryGraphHostPort(cwd string) (string, int) {
	host, port := "127.0.0.1", memoryGraphDefaultPort
	store, err := config.Init(cwd, "", false)
	if err != nil {
		return host, port
	}
	if entry, ok := store.Config().Services["memory-graph"]; ok {
		if entry.Host != "" {
			host = entry.Host
		}
		if entry.Port != 0 {
			port = entry.Port
		}
	}
	return host, port
}
