Recall the decisions, constraints, requirements, references and plans that earlier turns chose to record,
so this session can continue work rather than re-litigate it.

Use it at the start of a continuation ("let's keep going with the memory design", "what did we decide about
the build tooling", "remember the prose package?") and any time a fact you need is the kind memory exists to
hold: a rationale, a ruled-out option, a requirement, a pointer. Code shape is not what this finds — use the
workspace index, the LSP or structural search for that.

Search by keywords or identifiers (BM25 ranked), by a named `thread` to stay inside one evolving topic, or by
`tags` for lateral recall across every thread that ever touched a thing (a tag query is deliberately not scoped
by thread). A vague "continue that" resolves best by matching a thread name you can already infer from context.

It returns the highest-ranked entries as a one-line summary plus a metadata badge and a body snippet, each
carrying its origin `source` and a `why` so you can judge the hit. A result without provenance is not returned
at all. A miss means "nothing was recorded about this yet", not "it is not true" — keep going and record it when
it lands. When a summary is not enough, pull the full body with memory_read by id.
