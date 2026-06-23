# ADR: Remove PostHog Telemetry

## Status

Accepted

## Context

Phosphor inherited PostHog telemetry from Crush, the project Phosphor was forked from. The `internal/event/` package used the `posthog-go` library to send telemetry data to `https://data.charm.land`. This included app lifecycle events, session events, token usage metrics, and error reporting. The data was sent without requiring explicit user configuration — it was enabled by default and could only be disabled via environment variables or config flags.

The telemetry system also depended on `github.com/denisbrodbeck/machineid` to generate a machine-level distinct ID for tracking.

## Decision

Remove the PostHog telemetry system entirely from the codebase.

### Why This Approach

1. **Alignment with project goals**: Phosphor aims to be private, secure, and lean. Sending telemetry to a remote party contradicts these goals, regardless of what data is actually transmitted. The absence of prompt or session data in the telemetry doesn't eliminate the fundamental concern: data leaving the user's machine to a third-party destination.

2. **No "phone home"**: There should be no telemetry that "phones home" without explicit user configuration. The only telemetry that should be collected is the OpenTelemetry tracing, which requires users to configure their own endpoint. This gives users full control over where their data goes.

3. **Reduced dependencies**: Removing PostHog eliminates unnecessary dependencies (`posthog-go`, `machineid`), reducing the attack surface, build size, and maintenance burden.

4. **User trust**: Even if sensitive information isn't being sent, having telemetry code that reaches out to a third-party server creates cognitive overhead for users trying to understand what their agent is doing. Removing it eliminates this concern entirely.

### Alternatives Considered

- **None**: There is no reasonable alternative here. If we're going to collect telemetry, we should do it in a way that's transparent, configurable, and belongs to the end-user, which is what OpenTelemetry provides. PostHog is not the right tool for the job.

### Consequences

- The `internal/event/` package and `internal/agent/event.go` have been removed, eliminating all PostHog telemetry code.
- The `posthog-go` and `machineid` dependencies have been removed from `go.mod`.
- All references to event tracking (AppInitialized, AppExited, SessionCreated, SessionDeleted, PromptSent, PromptResponded, TokensUsed, StatsViewed, etc.) have been removed from the codebase.
- The `shouldEnableMetrics` function and `x-phosphor-id` header generation have been removed.
- Users who want telemetry should configure OpenTelemetry tracing, which sends data to a user-configured endpoint.
