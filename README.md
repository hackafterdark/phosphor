# Phosphor

Phosphor is a terminal-based, hardened agentic runtime built in Go. It is a fork of the wonderful [Crush](https://github.com/charmbracelet/crush) agent, designed for developers who demand full visibility, structural intelligence, and uncompromising security in their AI coding tools.

Phosphor is not a general-purpose chat interface. It is a hardened foundation for agentic execution that treats the filesystem as a high-integrity sandbox and code as a structural graph rather than raw text.

> **Important Note:** _This is a personal project._ While the goal is to make it useful for others, it will primarily be driven by my own needs and preferences. It is not recommended for production use and there is no support and no warranties expressed or implied...But I do hope you find it useful!

## Why Phosphor?

Phosphor originated from the need to run AI agents against local inference engines and a need to experiment with agents. While Crush provided a great project, there were some obviously incompatible changes with the fork.

There were some new features added (namely the tree-sitter integration) and some features removed (like the bang command). The changes were incompatible with the original project so a fork became the only reasonable way to pursue these changes.

Additional goals and areas of focus include:

1. **Hardened Security:** A defense-in-depth model that enforces filesystem bounds, command allow-lists, and environment variable filtering at the shell interpreter level.
2. **Structural Awareness:** Integration of tree-sitter to enable AST-aware editing and structural search, allowing agents to understand the shape of the code they modify.
3. **Observability:** Native OpenTelemetry instrumentation to turn autonomous agent behavior from a black box into an auditable flight recorder.

## Core Philosophy

Phosphor is built as a workbench for agentic experimentation. You can read our full mission and architectural approach here: [CORE_PHILOSOPHY](docs/CORE_PHILOSOPHY.md).

## Project State

Phosphor is in its early stages. It is stable enough for daily development workflows, but the API, CLI flags, and internal architecture are subject to change as the system matures.

This project is built in the open. While it serves as my primary workbench, I encourage others to use it, learn from it, or fork it to adapt it to their own needs. Phosphor values a lean, stable core over feature bloat; if your needs diverge from our goals, forking is the preferred path.

## Build Requirements

Phosphor relies on tree-sitter for structural code analysis. Because tree-sitter is a C library, a C compiler is required to build the project.

- OS: Linux, macOS, or Windows (with MSYS2/MinGW).
- Toolchain: Go 1.21+, GCC or Clang.
- CGO: Must be enabled (CGO_ENABLED=1).

### Build Instructions

```
# Ensure CGO is enabled
export CGO_ENABLED=1

# Build the binary
go build -o phosphor .
```

Note for Windows users: Ensure your C compiler (e.g., MSYS2 GCC) is in your %PATH% before running the build command. You can also specify the path to GCC in one line with something like this for example:

```
$env:CGO_ENABLED="1"; $env:GOTOOLCHAIN="auto"; $env:PATH="C:/msys64/ucrt64/bin;" + $env:Path; go build -o phosphor.exe .
```

You can also run `go test` the same way. Note that if you do not have CGO enabled things should still work, but tree-sitter functionality will be excluded.

## License

Phosphor is licensed under [FSL-1.1-MIT](https://github.com/hackafterdark/phosphor/raw/main/LICENSE.md).