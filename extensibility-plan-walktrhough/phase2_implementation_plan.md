# Implementation Plan - Platform Extensibility Phase 2

We will wrap the existing monolithic HTTP API server as a registered `platform.Service` (`http-api`), enforce security governance on it, and migrate the `phosphor server` CLI command to use the registry.

## User Review Required

> [!IMPORTANT]
> ### 1. CLI Override & Configuration Defaults
> By default, the `http-api` service will be defined as enabled in the default configuration.
> If the user sets `"enabled": false` in their `phosphor.json` config under `services.http-api`, the registry will block the service from starting, and the `phosphor server` command will fail/warn clean.
> If the user overrides the host using the CLI flag `-H` / `--host`, it will take precedence over any `host` or `port` configuration specified in `phosphor.json`.

## Proposed Changes

---

### config

#### [MODIFY] [load.go](file:///f:/hackafterdark/phosphor/internal/config/load.go)
- Set default configuration for `"http-api"` service in `setDefaults` so that it is enabled by default.

---

### platform

#### [NEW] [service.go](file:///f:/hackafterdark/phosphor/internal/platform/httpapi/service.go)
- Implement `httpapi.Service` struct satisfying the `platform.Service` interface.
- Provide `NewService(cfgStore *config.ConfigStore, scheme, host string, logger *slog.Logger) *Service`.
- Implement `Start` to launch the HTTP server asynchronously in a goroutine.
- Implement `Stop` to invoke `Shutdown` on the underlying `server.Server`.

#### [NEW] [service_test.go](file:///f:/hackafterdark/phosphor/internal/platform/httpapi/service_test.go)
- Unit test to verify `Name()`, `Start()`, and `Stop()` lifecycle of the HTTP API service.

#### [MODIFY] [governance.go](file:///f:/hackafterdark/phosphor/internal/platform/governance.go)
- Add validation for `http-api` port configuration (checking that `port >= 0 && port <= 65535`).

#### [MODIFY] [governance_test.go](file:///f:/hackafterdark/phosphor/internal/platform/governance_test.go)
- Add tests validating governance policy checks with invalid `http-api` ports.

---

### cmd

#### [MODIFY] [server.go](file:///f:/hackafterdark/phosphor/internal/cmd/server.go)
- Instantiate `platform.NewRegistry` and register the `http-api` service (if enabled).
- Start the server using `registry.StartAll(ctx)`.
- Use the registry's lifecycle manager for cleanup on signal shutdown.

## Verification Plan

### Automated Tests
- Run `go test ./internal/platform/...` to verify registry, governance, and HTTP API service tests.
- Run `go test ./...` to verify no regressions across other commands, CLI schema, or agent services.

### Manual Verification
- Start the server via `go run . server` and check if it binds correctly.
- Test disabling the service via `"services": { "http-api": { "enabled": false } }` in `phosphor.json` and ensure it fails gracefully.
