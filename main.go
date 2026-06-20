// Package main is the entry point for the Phosphor CLI.
//
//	@title			Phosphor API
//	@version		1.0
//	@description	Phosphor is a terminal-based AI coding assistant. This API is served over a Unix socket (or Windows named pipe) and provides programmatic access to workspaces, sessions, agents, LSP, MCP, and more.
//	@contact.name	HackAfterDark
//	@contact.url	https://hackafterdark.com
//	@license.name	MIT
//	@license.url	https://github.com/hackafterdark/phosphor/blob/main/LICENSE
//	@BasePath		/v1
package main

import (
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"

	"github.com/hackafterdark/phosphor/internal/cmd"
	_ "github.com/hackafterdark/phosphor/internal/dns"
	_ "github.com/joho/godotenv/autoload"
)

func main() {
	if os.Getenv("PHOSPHOR_PROFILE") != "" {
		go func() {
			slog.Info("Serving pprof at localhost:6060")
			if httpErr := http.ListenAndServe("localhost:6060", nil); httpErr != nil {
				slog.Error("Failed to pprof listen", "error", httpErr)
			}
		}()
	}

	cmd.Execute()
}
