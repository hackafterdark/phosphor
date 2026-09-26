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
	"runtime"

	"github.com/hackafterdark/phosphor/internal/cmd"
	_ "github.com/hackafterdark/phosphor/internal/dns"
	_ "github.com/joho/godotenv/autoload"
)

// blockProfileRate records a blocking stack for every 100µs a goroutine spends
// parked on a synchronization primitive. Dense enough to catch a stall that matters,
// sparse enough to leave a profiling run usable as an editor.
const blockProfileRate = 100_000

// mutexProfileFraction records every contended mutex acquisition. The contention
// profile is the one that names the holder of a hot lock rather than the goroutines
// queued behind it, so it is worth the overhead in a run that was started for it.
const mutexProfileFraction = 1

func main() {
	if os.Getenv("PHOSPHOR_PROFILE") != "" {
		// The block and contention profiles are off by default, which leaves the
		// /debug/pprof/block and /debug/pprof/mutex endpoints empty exactly when a
		// run is being diagnosed for a stall. A profiling run is the one case where
		// their cost is worth paying: block records a stack per nanosecond spent
		// blocked on a synchronization primitive, and the mutex profile records the
		// holder of a contended lock, which is what names the parking party rather
		// than the victim.
		runtime.SetBlockProfileRate(blockProfileRate)
		runtime.SetMutexProfileFraction(mutexProfileFraction)
		go func() {
			slog.Info("Serving pprof at localhost:6060")
			if httpErr := http.ListenAndServe("localhost:6060", nil); httpErr != nil {
				slog.Error("Failed to pprof listen", "error", httpErr)
			}
		}()
	}

	cmd.Execute()
}
