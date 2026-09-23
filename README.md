# Phosphor

[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/hackafterdark/phosphor)

Phosphor is a terminal-based, hardened agentic runtime built in Go, designed for developers who demand full visibility, structural intelligence, and uncompromising security in their AI coding tools. While it's primarily focused on being a coding agent, it can be used for more.

It is also a research project and a reference. It is not a commercial project, and its success isn't measured by how many people use it. The goal is to raise awareness around agent security and to share ideas and strategies. I borrow freely from other projects when building features into Phosphor, and I encourage you to borrow from it in return: it takes the best of what I find, adds entirely new ideas and my own design input, tests it, and shares what works. Along the way I'll make decisions you may not agree with, and that's fine: this is an open-source project, so you're free to adapt it to your own tastes.

> **Important Note:** *To set expectations, this is a personal research project.* While the goal is to make it useful for others, it will primarily be driven by my own needs and preferences, and there is no formal support. There's some real experiments in here and they won't all be winners at all times. That said, it is my own day-to-day agent and is used for real work, so please don't let that stop you from trying it for yours. So yes, it absolutely works and I do hope you find it useful!

## Why Phosphor?

Phosphor originated from the need to run AI agents against local inference engines and a need to experiment with agents.

Some goals and areas of focus include:

1. **Hardened Security:** A defense-in-depth model that enforces filesystem bounds, command allow-lists, and environment variable filtering at the shell interpreter level.
2. **Structural Awareness:** Integration of tree-sitter to enable AST-aware editing and structural search, allowing agents to understand the shape of the code they modify.
3. **Observability:** Native OpenTelemetry instrumentation to turn autonomous agent behavior from a black box into an auditable flight recorder.

## Documentation

The full documentation index lives at [docs/INDEX.md](docs/INDEX.md), covering
configuration, tools, security hardening, and platform integration. Start there
to browse everything.

The documentation also ships inside the binary. Run Phosphor and just ask it a
question about itself, or ask it to list the docs; it searches the same corpus
from within the TUI.

Phosphor is built as a workbench for agentic experimentation. You can read our full mission and architectural approach here: [CORE_PHILOSOPHY](docs/CORE_PHILOSOPHY.md).

## Build Requirements

Phosphor relies on tree-sitter for structural code analysis. Because tree-sitter is a C library, a C compiler is required to build the project.

- OS: Linux, macOS, or Windows (with MSYS2/MinGW).
- Toolchain: Go 1.21+, GCC or Clang.
- CGO: Required for the full experience (enables CGO with `CGO_ENABLED=1`). Without it, the build still succeeds, but tree-sitter features (structural search, AST-aware editing) are excluded.

### Build Instructions

Easiest path, if you have the [Taskfile CLI](https://taskfile.dev) installed:

```
task build
```

This enables CGO, sets the required Go experiment flags, and regenerates the embedded docs corpus for you.

Or with plain Go tooling:

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

You can also run `go test` the same way.

## Credits

Phosphor is a fork of [Crush](https://github.com/charmbracelet/crush), the wonderful terminal AI coding agent from [Charm](https://charm.land). Many of its foundations come from their work, and I'm grateful for it.

## License

Phosphor is licensed under [FSL-1.1-MIT](https://github.com/hackafterdark/phosphor/raw/main/LICENSE.md).