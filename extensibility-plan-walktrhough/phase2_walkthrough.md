# Walkthrough - Platform Extensibility Phase 2

We have successfully migrated the monolithic HTTP API server into the modular platform service registry (`internal/platform`) as `HttpApiService`, enforced security governance (port range and enablement), and updated the CLI command to initialize, register, and run the service via the registry.

In addition, we integrated `github.com/labstack/echo/v5` inside `internal/platform/httpapi` to host the OpenAI-compatible API surface. The framework choice is fully isolated inside this package.

## Changes Made

### httpapi Service Wrapper (Dual Servers, One Wrapper)
- **[NEW] [service.go](file:///f:/hackafterdark/phosphor/internal/platform/httpapi/service.go)**:
  - Implements the `platform.Service` interface for the HTTP API server.
  - Spawns the stdlib Management API server (from `internal/server`) on the configured port.
  - Spawns the Echo-powered OpenAI-compatible API using a standard `http.Server` with the Echo instance as the handler.
  - Resolves configuration for the OpenAI API via the `"openai-api"` service entry (defaulting to host `127.0.0.1` and port `8643`).
  - Monitors the parent context for cancellation and uses an idempotent `sync.Once` wrapper to stop both servers gracefully.
- **[NEW] [service_test.go](file:///f:/hackafterdark/phosphor/internal/platform/httpapi/service_test.go)**: Verifies starting and stopping the dual-server setup cleanly.

### Config and CLI Integration
- **[MODIFY] [load.go](file:///f:/hackafterdark/phosphor/internal/config/load.go)**: Configures default settings (enabled state, host, and port) for both the `http-api` and `openai-api` services in `phosphor.json`.
- **[MODIFY] [server.go](file:///f:/hackafterdark/phosphor/internal/cmd/server.go)**: Added CLI flags (`--openai-host`, `--openai-port`, `--openai-enabled`) and integrated them as overrides into the loaded configuration.

### Security Governance
- **[MODIFY] [governance.go](file:///f:/hackafterdark/phosphor/internal/platform/governance.go)**: Added check to validate that both the HTTP API service port and the OpenAI API service port reside in a valid TCP range (`0` to `65535`).
- **[MODIFY] [governance_test.go](file:///f:/hackafterdark/phosphor/internal/platform/governance_test.go)**: Added test cases verifying invalid ports are rejected for both services.

## Validation Results

- **Unit Tests**:
  - `go test -v ./internal/platform/...` passed.
  - `go test -v ./internal/platform/httpapi/...` passed.
- **E2E & Integration**:
  - Entire test suite `go test ./...` passed without regressions.
