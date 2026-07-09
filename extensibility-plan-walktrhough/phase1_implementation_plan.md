# Implementation Plan - Platform Extensibility Phase 1

This plan details the implementation of Phase 1 of the Phosphor Platform Extensibility project. The goal of Phase 1 is to define the transport-agnostic `Service` and `Registry` types, implement a configuration-driven governance layer, and update the configuration structures in `internal/config/config.go` without changing any existing execution logic.

## Suggestions & Open Questions

> [!IMPORTANT]
> ### 1. Unified Configuration Loading (Suggestion)
> The original plan proposed a separate `internal/platform/config.go` with its own `Load()` function. We suggest instead defining the `Services` and `Security` configuration fields directly inside the main `config.Config` struct in `internal/config/config.go`.
> 
> This approach allows the `phosphor.json` config parsing, global vs workspace merging, environment variable expansion, and JSON validation to occur automatically during the existing `config.Load` call.
> 
> *Do you agree with integrating the `services` and `security` configuration definitions directly into the existing `config.Config` struct?*

> [!IMPORTANT]
> ### 2. Registry Lifecycle Blocking Behavior (Question)
> In the plan, the `Service.Start(ctx)` interface is defined as:
> `Start(ctx context.Context) error // Blocks until Stop is called or context is cancelled.`
> 
> If `Start` blocks, then `Registry.StartAll` running sequentially in a single loop will block forever on the first active service (like an HTTP server or a Discord polling bot).
> 
> We have two choices to resolve this:
> - **Option A (Non-blocking Start):** The `Start` method returns immediately after initializing the listener/connection, spawning its own run loop inside a separate goroutine.
> - **Option B (Asynchronous Registry):** The `Start` method blocks, and the `Registry.StartAll` loop spawns a separate goroutine for each service's `Start` method.
> 
> *Which lifecycle pattern do you prefer? (We recommend Option A for simple service control, or Option B if we want the registry to manage the worker goroutines).*

> [!NOTE]
> ### 3. Egress Security Policies (Suggestion)
> In the governance layer, the plan suggests:
> ```go
> case "http-api":
>     if !g.config.Security.AllowedEgress.HTTP {
>         return fmt.Errorf("HTTP egress blocked by security policy")
>     }
> ```
> Since the `http-api` service acts as an ingress server (incoming HTTP connections), checking it against `AllowedEgress` might be slightly confusing in name. Egress typically means outgoing connections (e.g., the agent calling out to Discord or Slack). We suggest naming this structure `AllowedTransports` or keeping the term but documenting clearly that ingress services might also be gated here if they involve outbound callbacks or simply to lock down the interface.

---

## Proposed Changes

### Configuration Layer

#### [MODIFY] [config.go](file:///f:/hackafterdark/phosphor/internal/config/config.go)
- Add new configuration structs:
  ```go
  type ServiceEntry struct {
      Enabled bool           `json:"enabled" jsonschema:"description=Whether the service is enabled"`
      Port    int            `json:"port,omitempty" jsonschema:"description=Port number for network services"`
      Host    string         `json:"host,omitempty" jsonschema:"description=Host address to bind to"`
      Auth    AuthConfig     `json:"auth,omitempty" jsonschema:"description=Authentication configuration"`
  }

  type AuthConfig struct {
      Type string `json:"type" jsonschema:"description=Auth type: none or bearer,enum=none,enum=bearer"`
      Key  string `json:"key,omitempty" jsonschema:"description=Bearer token or key"`
  }

  type AllowedEgressConfig struct {
      HTTP     bool `json:"http,omitempty" jsonschema:"description=Allow HTTP API service egress"`
      Discord  bool `json:"discord,omitempty" jsonschema:"description=Allow Discord bot egress"`
      Slack    bool `json:"slack,omitempty" jsonschema:"description=Allow Slack bot egress"`
      Telegram bool `json:"telegram,omitempty" jsonschema:"description=Allow Telegram bot egress"`
  }

  type SecurityConfig struct {
      AllowedEgress AllowedEgressConfig `json:"allowed_egress,omitempty" jsonschema:"description=Permitted outbound platforms"`
      ToolBlacklist []string            `json:"tool_blacklist,omitempty" jsonschema:"description=List of tools to block"`
      ReadOnly      bool                `json:"read_only,omitempty" jsonschema:"description=Force read-only mode"`
  }
  ```
- Add the `Services` and `Security` fields to the main `Config` struct:
  ```go
  Services map[string]ServiceEntry `json:"services,omitempty" jsonschema:"description=Configuration for platform services"`
  Security *SecurityConfig          `json:"security,omitempty" jsonschema:"description=Security manifest and governance options"`
  ```
- Update `setDefaults` in `internal/config/config.go` to initialize these structures with sensible defaults (e.g. TUI enabled by default, others disabled).

---

### Platform Package (New)

#### [NEW] [service.go](file:///f:/hackafterdark/phosphor/internal/platform/service.go)
- Define the `Service` interface:
  ```go
  package platform

  import "context"

  type Service interface {
      Name() string
      Start(ctx context.Context) error
      Stop(ctx context.Context) error
      Describe() string
  }
  ```
- Define `AgentService` interface:
  ```go
  type AgentService interface {
      Service
      Connect(ctx context.Context) error
      SetPromptHandler(handler PromptHandler)
      SendPrompt(ctx context.Context, req PromptRequest) error
  }
  ```
- Define supporting types (`PromptHandler`, `PromptEvent`, `EventType`, `PromptRequest`, `PromptMode`, `Recipient`, `Image`) as outlined in the platform extensibility plan.

#### [NEW] [registry.go](file:///f:/hackafterdark/phosphor/internal/platform/registry.go)
- Implement `Registry` struct to manage the lifecycle of registered services:
  ```go
  package platform

  import (
      "context"
      "log/slog"
  )

  type Registry struct {
      services []Service
      logger   *slog.Logger
      gov      *Governance
  }
  ```
- Implement registry methods:
  - `NewRegistry(logger *slog.Logger, gov *Governance) *Registry`
  - `Register(s Service)`
  - `StartAll(ctx context.Context) error`
  - `StopAll(ctx context.Context) error`

#### [NEW] [governance.go](file:///f:/hackafterdark/phosphor/internal/platform/governance.go)
- Implement `Governance` struct to validate and authorize service execution:
  ```go
  package platform

  import (
      "fmt"
      "github.com/hackafterdark/phosphor/internal/config"
  )

  type Governance struct {
      cfg *config.Config
  }

  func NewGovernance(cfg *config.Config) *Governance {
      return &Governance{cfg: cfg}
  }

  func (g *Governance) Check(s Service) error {
      // 1. Check if service is enabled in config
      // 2. Check egress configuration for remote/network platforms
      // 3. Validate auth options if required
  }
  ```

---

### Verification Plan

#### Automated Tests
- Run `go test ./internal/config/...` to ensure updated config structure parses correctly.
- Add unit tests in `internal/platform/registry_test.go` and `internal/platform/governance_test.go` to test:
  - Registering services.
  - Starting and stopping services in order.
  - Governance checks blocking unauthorized egress or disabled services.
- Verify JSON schema tests still pass via `go test ./internal/cmd -run TestSchema`.
