#!/usr/bin/env python3
"""Build graph.json (or a self-contained snapshot report) from the phosphor
memory SQLite store. Standalone; no phosphor imports.

Usage:
  python build_graph.py [db_path]                # writes graph.json next to view.html
  python build_graph.py [db_path] --report FILE  # writes a portable one-file report
  python build_graph.py [db_path] --serve        # graph.json + live viewer over HTTP
"""
import argparse
import json
import os
import sqlite3
import sys
from collections import Counter
from datetime import datetime, timezone

HERE = os.path.dirname(os.path.abspath(__file__))
VIEW_HTML = os.path.join(HERE, "view.html")


def find_db():
    home = os.path.expanduser("~")
    rel = os.path.join(".phosphor", "memory", ".phosphor-index", "memory.db")
    candidates = []
    # walk up from the current directory for a project vault
    d = os.getcwd()
    while True:
        candidates.append(os.path.join(d, rel))
        parent = os.path.dirname(d)
        if parent == d:
            break
        d = parent
    candidates += [
        os.path.join(home, ".local", "share", "phosphor", rel),
        os.path.join(home, ".phosphor", rel),
        os.path.join(home, ".config", "phosphor", rel),
    ]
    for c in candidates:
        if os.path.exists(c):
            return c
    raise SystemExit("no memory.db found; pass the path as an argument")


def build(db):
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row

    nodes = []
    ids = set()
    edges = []

    # canonical tags per entry, for clustering
    tags_of = {}
    for r in con.execute(
        "SELECT et.entry_id AS e, t.term AS term FROM entry_tags et "
        "JOIN tags t ON t.id = et.tag_id"
    ):
        tags_of.setdefault(r["e"], []).append(r["term"])

    for r in con.execute(
        "SELECT id, type, status, thread, trust, recall_count, hot_score, "
        "created_ts, updated_ts, supersedes, from_decision, summary, body FROM entries"
    ):
        if r["id"] in ids:
            continue
        ids.add(r["id"])
        nodes.append(
            {
                "id": r["id"],
                "type": r["type"] or "unknown",
                "status": r["status"] or "active",
                "thread": r["thread"] or "",
                "tags": tags_of.get(r["id"], []),
                "summary": (r["summary"] or "")[:120],
                "body": (r["body"] or "")[:4000],
                "trust": r["trust"] or 0,
                "recall": r["recall_count"] or 0,
                "hot": r["hot_score"] or 0,
                "created": r["created_ts"] or "",
                "updated": r["updated_ts"] or "",
            }
        )
        if r["supersedes"]:
            edges.append({"source": r["id"], "target": r["supersedes"], "kind": "supersedes"})
        if r["from_decision"]:
            edges.append({"source": r["id"], "target": r["from_decision"], "kind": "decision"})

    threads = Counter(n["thread"] for n in nodes if n["thread"])
    # tag nodes: entries cluster around their first tag, threads around their own node
    tag_counts = Counter(t for n in nodes for t in n["tags"])
    for t, c in tag_counts.most_common(12):
        tid = f"tag:{t}"
        if tid not in ids:
            ids.add(tid)
            nodes.append(
                {
                    "id": tid,
                    "type": "tag",
                    "status": "group",
                    "thread": "",
                    "tags": [],
                    "summary": t,
                    "trust": 0,
                    "recall": c,
                    "hot": c,
                    "created": "",
                    "updated": "",
                }
            )

    for t, c in threads.items():
        tid = f"thread:{t}"
        if tid not in ids:
            ids.add(tid)
            nodes.append(
                {
                    "id": tid,
                    "type": "thread",
                    "status": "group",
                    "thread": t,
                    "tags": [],
                    "summary": t,
                    "trust": 0,
                    "recall": c,
                    "hot": c,
                    "created": "",
                    "updated": "",
                }
            )
        for n in nodes:
            if n["type"] == "entry" or (n["type"] not in ("thread", "tag")):
                if n["thread"] == t:
                    edges.append({"source": n["id"], "target": tid, "kind": "thread"})

    for n in nodes:
        if n["type"] in ("thread", "tag"):
            continue
        for t in n["tags"][:3]:
            tid = f"tag:{t}"
            if tid in ids:
                edges.append({"source": n["id"], "target": tid, "kind": "tag"})

    # verdict tallies for the VERDICTS panel
    verdicts = Counter(n["type"] for n in nodes if n["type"] != "thread")

    # arrivals: created_ts bucketed by day for the ARRIVALS dot grid
    arrivals = Counter(
        (n["created"] or "")[:10] for n in nodes if n["type"] != "thread" and n["created"]
    )

    return {
        "generated": datetime.now(timezone.utc).isoformat(),
        "db": db,
        "nodes": nodes,
        "edges": edges,
        "verdicts": dict(verdicts),
        "arrivals": dict(arrivals),
    }


def write_report(graph, path):
    """Freeze the current graph into a single self-contained HTML report."""
    with open(VIEW_HTML, encoding="utf-8") as f:
        html = f.read()
    payload = json.dumps(graph).replace("</", "<\\/")
    head = (
        "<script>window.__PHOSPHOR_SNAPSHOT__ = true;\n"
        "window.__PHOSPHOR_GRAPH__ = " + payload + ";</script>\n</head>"
    )
    if "</head>" not in html:
        raise SystemExit("view.html has no </head>; cannot embed snapshot payload")
    html = html.replace("</head>", head, 1)
    with open(path, "w", encoding="utf-8") as f:
        f.write(html)
    print(f"wrote snapshot report: {path} "
          f"({len(graph['nodes'])} nodes, {len(graph['edges'])} edges)")


def serve():
    """Serve the live viewer; the page polls graph.json and self-refreshes."""
    import http.server
    import socketserver

    class Handler(http.server.SimpleHTTPRequestHandler):
        def __init__(self, *args, **kwargs):
            super().__init__(*args, directory=HERE, **kwargs)

        def end_headers(self):
            # never cache graph.json so the poller sees fresh data
            self.send_header("Cache-Control", "no-store")
            super().end_headers()

    with socketserver.TCPServer(("127.0.0.1", 8642), Handler) as httpd:
        print("serving live viewer at http://127.0.0.1:8642/view.html (ctrl-c to stop)")
        httpd.serve_forever()


def main():
    ap = argparse.ArgumentParser(description="phosphor memory graph builder")
    ap.add_argument("db", nargs="?", help="path to memory.db (auto-discovered if omitted)")
    ap.add_argument("--report", metavar="FILE",
                    help="write a portable single-file snapshot report instead of graph.json")
    ap.add_argument("--serve", action="store_true",
                    help="rebuild graph.json and serve the live viewer on 127.0.0.1:8642")
    args = ap.parse_args()

    db = args.db or find_db()
    graph = build(db)

    if args.report:
        write_report(graph, args.report)
        return

    out = os.path.join(HERE, "graph.json")
    with open(out, "w") as f:
        json.dump(graph, f, indent=1)
    print(f"wrote graph.json: {len(graph['nodes'])} nodes, {len(graph['edges'])} edges")

    if args.serve:
        serve()


if __name__ == "__main__":
    main()