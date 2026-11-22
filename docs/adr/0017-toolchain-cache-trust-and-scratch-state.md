# 17. Toolchain Cache Trust Model and Scratch State for Confined Builds

- **Status:** Accepted
- **Date:** 2026-11-22
- **Authors:** Phosphor Team
- **Superseded By:** —

## Context

Kernel sandboxing (ADR-0016) confines bash-tool children to the workspace
plus a small set of writable roots. Real development workflows break that
boundary constantly: `go build` downloads modules and toolchains, `cargo
build` takes cache locks, `gradle` writes daemon logs, `npm`/`pip`/`uv`
fetch packages. Each toolchain hides its writable state under the user's
home directory, and each home directory also contains state that must never
be reachable from a confined child:

- `bin/` directories on `PATH` (`GOPATH/bin`, `~/.cargo/bin`,
  `~/.local/bin`) — writable means a confined child can plant a binary the
  host executes later.
- Persistent hook and config files (`~/.gradle/init.d`,
  `~/.cargo/config.toml`) — writable means arbitrary code executes on
  every future host build.
- Credentials and integrity state (`~/.cargo/credentials.toml`,
  `~/.m2/settings.xml`, `~/.gradle/gradle.properties`,
  `$XDG_DATA_HOME/uv/credentials`, `GOPATH/pkg/sumdb`) — readable means
  credential theft; writable means poisoned checksum or registry state.

A concrete failure drove the pattern: the host GOPATH is denied for
persistence reasons, but Go legitimately needs to write
`$GOPATH/pkg/sumdb` for checksum-DB lookups and verified toolchain
downloads — so routine `go build`/`go test` wedged inside the sandbox on
any repo pinned to a newer toolchain.

## Decision

Adopt a uniform, per-toolchain trust model for the writable set, enforced
in one place (`pkg/sandbox/spec.go`) so the kernel spec and the in-process
pathguard can never disagree:

1. **Never grant a toolchain's top-level home directory** (`~/go`,
   `~/.cargo`, `~/.gradle`, `~/.m2`, `~/.bun`, `~/.local/share/uv`).
2. **Grant only leaf cache directories**, resolved exactly the way the
   tool itself resolves them (its canonical override env var first, then
   the default path): `pkg/mod`, `registry/`, `git/`, `repository/`,
   `caches/`, `wrapper/`, `daemon/`, `native/`, `install/cache`,
   `pnpm/store`, `~/.cache/{pip,uv,ccache}`, `~/.nuget/packages`. The
   whole list is gated behind `tools.bash.trust_toolchain_caches`
   (default off), because a writable cache is trusted input for every
   future host build.
3. **Keep the three dangerous classes denied**, both by scoping the grants
   and by explicit deny-read entries (`CredentialDenyPaths`): `bin/`
   executable dirs, persistent hook/config files, and credential/integrity
   state. Single-file writable rules are the only exception, and only for
   lock files with no code content (Cargo's `.package-cache` /
   `.package-cache-mutate`), pre-created by Phosphor before the spec is
   built because kernel file rules (Landlock) need the file to exist.
4. **Redirect tool-forced scratch state instead of widening grants.** When
   a tool needs writable state that has no safe leaf to grant (Go's
   `pkg/sumdb` sibling, toolchain verification), create a per-process
   scratch directory in the OS temp dir via `os.MkdirTemp` and pin the
   tool's cache variables to the host values (e.g. `GOPATH=<scratch>` with
   `GOMODCACHE`/`GOCACHE` pinned). The host caches stay shared; the
   host's dangerous sibling paths stay denied. This machinery is added
   lazily — only when a real workflow hits friction, never pre-built per
   language.

## Rationale / Why This Approach

- **The home directory is the wrong granularity.** Every affected
  ecosystem mixes heavy caches, executable `PATH` dirs, credential stores,
  and code-execution config in one home directory; only leaf grants can
  separate the useful state from the dangerous state.
- **One resolution function, two enforcement layers.** Resolving each
  cache path from the tool's own override variable keeps `DefaultReadWriteDirs`,
  `LanguageCacheDirs`, and the pathguard on a single list — drift between
  kernel and in-process enforcement is the failure mode that produces
  confusing half-denials.
- **Scratch redirection preserves both properties the home grant cannot**:
  shared host caches (no multi-GB duplication) and denied host dangerous
  paths (no persistence, no credential exposure). `os.MkdirTemp` gives
  unpredictable names, so concurrent Phosphor sessions cannot collide when
  one cleans up.
- **Lazy adoption keeps the surface small.** Pre-emptive scratch plumbing
  for every ecosystem would grow the writable set without evidence of
  need; the trust policy is uniform, but scratch machinery lands only on
  demonstrated friction.

## Consequences

- Confined `go build`/`go test`/toolchain resolution work out of the box
  even when the host GOPATH is unwritable, with host caches shared and no
  duplicated downloads.
- Opt-in toolchain trust (`trust_toolchain_caches`) covers npm, pip, uv,
  ccache, cargo, maven, gradle, bun, pnpm, and NuGet with cargo's dual
  cache locks and gradle's daemon/native dirs functional inside the
  sandbox.
- The writable set is larger than a workspace-only policy: each granted
  cache is acknowledged trusted input, which is why the flag is opt-in and
  why `trusted_extra_roots` stays guard-railed against roots that would
  defeat the model.
- Scratch cleanup is process-lifetime and defensive: the scratch dir is
  created lazily, removed at shutdown only if it was ever created, and
  `os.MkdirTemp` orphans are reaped by the OS temp cleanup.
- The model is stated per-toolchain going forward: any new ecosystem added
  to `LanguageCacheDirs` must pass the same three-class check (cache leaf
  grantable; `bin/`, hook/config, and credentials denied).
