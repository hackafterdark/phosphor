"""Phosphor agent adapter for Pier (DeepSWE v1.1).

Register with:

    pier run --agent-import-path agents.phosphor:PhosphorAgent ...

The agent itself is minimal: it uploads the host-built Phosphor binary into
the environment, injects the arm config, runs `phosphor run` headlessly, and
commits the result so the v1.1 verifier's `[[verifier.collect]]` hook can
extract `git diff <base> HEAD`. Token usage is pulled from the Phosphor
session database and reported through the Pier AgentContext.
"""

import asyncio
import shlex
import sqlite3
import tempfile
from pathlib import Path
from typing import Any

from pier.agents.base import BaseAgent
from pier.environments.base import BaseEnvironment
from pier.models.agent.context import AgentContext

_REMOTE_BINARY = "/usr/local/bin/phosphor"
_REMOTE_CONFIG = "/tmp/phosphor-arm"
_REMOTE_PROMPT = "/tmp/phosphor-prompt.txt"
_REPO_DIR = "/app"


class PhosphorAgent(BaseAgent):
    """Runs the Phosphor CLI headlessly inside a Pier environment."""

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        # binary: host path to the Phosphor executable (required).
        # config: host path to a phosphor.json arm config (optional; when
        #         omitted the image/env must already resolve a provider).
        self.binary_path = Path(kwargs.pop("binary", "")).resolve()
        if not self.binary_path.is_file():
            raise ValueError(f"binary kwarg must point at a built phosphor binary: {self.binary_path}")
        config = kwargs.pop("config", None)
        self.config_path = Path(config).resolve() if config else None
        if self.config_path is not None and not self.config_path.is_file():
            raise ValueError(f"config kwarg must point at a phosphor.json file: {self.config_path}")
        self._lsp_status: tuple[int, str] | None = None
        super().__init__(*args, **kwargs)

    @staticmethod
    def name() -> str:
        return "phosphor"

    def version(self) -> str | None:
        return None

    async def setup(self, environment: BaseEnvironment) -> None:
        await environment.upload_file(self.binary_path, _REMOTE_BINARY)
        await environment.exec(f"chmod +x {_REMOTE_BINARY}", timeout_sec=60)

        if self.config_path is not None:
            await environment.upload_dir(self.config_path.parent, _REMOTE_CONFIG)
            await environment.exec(
                f"mv {_REMOTE_CONFIG}/{self.config_path.name} {_REMOTE_CONFIG}/phosphor.json",
                timeout_sec=60,
            )

        # The verifier container is disposable, but git still demands an
        # identity before it will record the agent's commit.
        await environment.exec(
            "git config --global --add safe.directory " + _REPO_DIR
            + " && git config --global user.email phosphor@bench.local"
            " && git config --global user.name phosphor",
            timeout_sec=60,
        )

        # Phosphor auto-starts LSP servers whose binaries are on PATH and
        # whose root markers (go.mod, Cargo.toml, package.json) are in the
        # workspace. ECR task images ship the toolchain but not the
        # language servers, so install them here — gated on the repo
        # markers so each trial only pays for what it needs.
        await self._install_language_servers(environment)

    async def _install_language_servers(
        self, environment: BaseEnvironment
    ) -> None:
        """Best-effort LSP setup; a failure degrades to no-LSP, never a failed trial."""
        script = (
            "cd /app || exit 0; "
            # Go: gopls (skip when the Go toolchain is too old to build it).
            "if test -f go.mod && ! command -v gopls >/dev/null; then "
            "go install golang.org/x/tools/gopls@latest >/dev/null 2>&1; "
            "mv -n \"$(go env GOPATH)\"/bin/gopls /usr/local/bin/ 2>/dev/null || true; fi; "
            # Node/TypeScript: typescript + typescript-language-server.
            "if { test -f package.json || ls *.ts >/dev/null 2>&1 "
            "|| find . -maxdepth 3 -name '*.ts' -print -quit | grep -q .; } "
            "&& ! command -v typescript-language-server >/dev/null; then "
            "npm install -g typescript typescript-language-server >/dev/null 2>&1; fi; "
            # Python: pyright pulls its own node runtime via npm if present,
            # else the pip wheel ships it.
            "if { test -f pyproject.toml || test -f setup.py || test -f setup.cfg "
            "|| find . -maxdepth 2 -name '*.py' -print -quit | grep -q .; } "
            "&& ! command -v pyright-langserver >/dev/null; then "
            "pip install --quiet --disable-pip-version-check pyright 2>&1 "
            "| tail -1 || true; fi; "
            # Rust: rust-analyzer prebuilt binary, cargo build as fallback.
            # rustup images ship a rust-analyzer shim whose component may be
            # uninstalled, so probe with --version instead of command -v.
            "if test -f Cargo.toml && ! rust-analyzer --version >/dev/null 2>&1; then "
            "if command -v wget >/dev/null || command -v curl >/dev/null; then "
            "wget -qO /tmp/ra.gz "
            "https://github.com/rust-lang/rust-analyzer/releases/latest/download/rust-analyzer-x86_64-unknown-linux-gnu.gz "
            "|| curl -fsSL -o /tmp/ra.gz "
            "https://github.com/rust-lang/rust-analyzer/releases/latest/download/rust-analyzer-x86_64-unknown-linux-gnu.gz; "
            "gunzip -c /tmp/ra.gz > /usr/local/bin/rust-analyzer "
            "&& chmod +x /usr/local/bin/rust-analyzer "
            # Remove the dead rustup shim so PATH lookup finds the binary.
            "&& { rm -f \"$(command -v rust-analyzer)\" 2>/dev/null || true; }; "
            "else cargo install rust-analyzer --locked >/dev/null 2>&1; fi; fi; "
            "true"
        )
        result = await environment.exec(
            f"sh -c {shlex.quote(script)}", timeout_sec=900
        )
        self._lsp_status = (result.return_code, (result.stderr or "")[-500:])

    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        env: dict[str, str] = {}
        if self.config_path is not None:
            # PHOSPHOR_GLOBAL_CONFIG points at the config *directory*; the
            # CLI appends phosphor.json itself.
            env["PHOSPHOR_GLOBAL_CONFIG"] = _REMOTE_CONFIG
            env["PHOSPHOR_GLOBAL_DATA"] = "/tmp/phosphor-data"
            env["PHOSPHOR_CACHE_DIR"] = "/tmp/phosphor-cache"
        env["PHOSPHOR_DISABLE_PROVIDER_AUTO_UPDATE"] = "1"
        # Pier forwards anything passed via --ae into extra_env; copy the
        # provider credentials the arm config expects (e.g. OLLAMA_API_KEY).
        env.update(getattr(self, "extra_env", None) or {})

        # Feed the instruction through a file + stdin, never argv: markdown
        # rules in the problem statement otherwise arrive as CLI flags.
        with tempfile.TemporaryDirectory() as tmp:
            prompt_file = Path(tmp) / "prompt.txt"
            prompt_file.write_text(instruction)
            await environment.upload_file(prompt_file, _REMOTE_PROMPT)

        model_arg = f" --model {self.model_name}" if self.model_name else ""
        # Stream combined stdout/stderr to /logs/agent/phosphor.log so
        # hung or timed-out trials leave evidence behind; pipefail keeps
        # phosphor's exit code (not tee's) as the command result.
        inner = (
            f"{_REMOTE_BINARY} --cwd {_REPO_DIR} --yolo run --quiet{model_arg}"
            f" < {_REMOTE_PROMPT}"
        )
        command = (
            "bash -c \"mkdir -p /logs/agent && set -o pipefail && "
            f"{inner} 2>&1 | tee /logs/agent/phosphor.log\""
        )
        result = await environment.exec(
            command,
            cwd=_REPO_DIR,
            env=env,
            timeout_sec=None,
        )
        context.metadata = {
            "agent_return_code": result.return_code,
            "agent_stdout_tail": (result.stdout or "")[-2000:],
            "agent_stderr_tail": (result.stderr or "")[-2000:],
            "lsp_setup": self._lsp_status,
        }

        # v1.1 grading extracts `git diff base HEAD`: commit whatever the
        # agent changed, then leave the tree clean for the collect hook.
        await environment.exec(
            f"cd {_REPO_DIR} && git add -A"
            " && (git commit -m 'phosphor agent changes' || true)"
            " && git log --oneline -1",
            timeout_sec=120,
        )

        await self._record_token_usage(environment, context, env)

    async def _record_token_usage(
        self,
        environment: BaseEnvironment,
        context: AgentContext,
        env: dict[str, str],
    ) -> None:
        # The session database lives in the workspace .phosphor dir;
        # PHOSPHOR_GLOBAL_DATA only relocates global data, not this file.
        with tempfile.TemporaryDirectory() as tmp:
            local_db = Path(tmp) / "phosphor.db"
            try:
                await environment.download_file(
                    f"{_REPO_DIR}/.phosphor/phosphor.db", local_db
                )
            except Exception:  # noqa: BLE001 - usage accounting must never fail a trial
                return
            for _ in range(10):
                if local_db.exists():
                    break
                await asyncio.sleep(0.5)
            if not local_db.exists():
                return
            try:
                conn = sqlite3.connect(local_db)
                row = conn.execute(
                    "select coalesce(sum(prompt_tokens), 0),"
                    " coalesce(sum(completion_tokens), 0), count(*)"
                    " from token_usage"
                ).fetchone()
                conn.close()
            except sqlite3.Error:
                return
        context.n_input_tokens = int(row[0])
        context.n_output_tokens = int(row[1])
        if context.metadata is None:
            context.metadata = {}
        context.metadata["model_calls"] = int(row[2])
