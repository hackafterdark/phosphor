"""Mitmproxy addon: benchmark anti-cheat egress filter.

Blocks reward-seeking lookups (reference solutions, grader tests, live
issue tracker) while leaving package registries and the model API open.
Domains are matched as suffixes, so github.com covers gist.github.com and
codeload.github.com.

Usage:

    mitmdump --mode regular --listen-host 0.0.0.0 --listen-port 8080 \
        -s benchmarks/deepswe1.1/tools/mitm_blocklist.py

The addon logs every blocked request and answers with a crisp 403 so the
agent gets an immediate, unambiguous refusal instead of a hang.
"""

from mitmproxy import http

BLOCKED_DOMAINS = (
    # Reference solutions, upstream fixes, and grader context.
    "github.com",
    "githubusercontent.com",  # raw, media, objects subdomains
    "gitlab.com",
    "bitbucket.org",
    "sourcegraph.com",
    "grep.app",
    "searchcode.com",
    # Live upstream state that leaks the intended change.
    "api.github.com",
    "bugs.debian.org",
    "issues.apache.org",
)

# Explicitly allowed: package/toolchain installs and model endpoints stay
# open. Kept for documentation; the filter is block-based, so anything
# outside BLOCKED_DOMAINS passes through.
ALLOWED_DOMAINS = (
    "ollama.com",
    "openrouter.net",
    "pypi.org",
    "files.pythonhosted.org",
    "registry.npmjs.org",
    "proxy.golang.org",
    "go.dev",
    "crates.io",
    "static.crates.io",
    "index.crates.io",
)


def _blocked(host: str) -> bool:
    host = host.lower().rstrip(".")
    return any(host == d or host.endswith("." + d) for d in BLOCKED_DOMAINS)


def request(flow):
    host = flow.request.pretty_host
    if _blocked(host):
        flow.response = http.Response.make(
            403,
            b"Blocked by benchmark egress filter: fetching upstream "
            b"solutions or issue trackers is not allowed during evaluation.",
            {"Content-Type": "text/plain"},
        )
        print(f"[benchmark-egress] blocked {flow.request.pretty_url}")
