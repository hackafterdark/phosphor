Record and maintain durable memory: the decisions, constraints, requirements, references and
preferences that a later session would need in order to continue this work.

Memory is how continuity of *thinking* survives across sessions. Code shape is already covered by
the workspace index, the LSP and structural search — never store where a symbol lives. What only
memory can hold is the "because": why a choice was made, what was ruled out and on what grounds,
what the requirement actually is, and where a plan currently stands.

Reach for this tool the moment any of these appears in the conversation. The phrasings below are the
trigger — when you notice one, write the entry then and there, in the same turn, before you move on:

- "decided", "we'll go with", "let's go with", "settled on", "we're using"
- "rule out", "ruled out", "no longer use", "instead of", "rather than", "switched to", "we dropped"
- "the requirement is", "hard rule", "the constraint is", "we must never", "always use"
- "remember that", "note that", "keep in mind", "for future reference"
- "phase 1", "milestone 2", "the plan now is", "next up is", "still open is"
- a preference or convention the user corrects you about, or states outright

Typing of entry to use:

- `decision` — a choice made between options, with the reason and the rejected alternative
- `constraint` — a rule that narrows the solution space (the "no embeddings, that's hard" kind)
- `requirement` — what the thing has to do or satisfy, stated by the user
- `open_question` — something still undecided that a later turn must resolve
- `reference` — a pointer worth keeping: a library you evaluated, a doc, a runbook, a prior finding
- `plan` — where the work currently stands and what is next
- `preference` / `fact` / `pattern` / `environment` / `task` — the ordinary kinds
- `policy` — a standing instruction the user gave you about how to behave ("ask me before touching
  the build setup"); capture these here rather than only honouring them in the moment

How to write it well:

- One atomic fact per call. Do not store a paragraph of history; store the assertion.
- `summary` is a single line a stranger could act on. `body` carries the reason and the tradeoff.
- Set `thread` to a stable topic name so "continue the memory design work" resolves later. Reuse the
  existing thread name when the conversation is continuing something rather than starting something.
- Set `tags` to the recurring names (projects, tools, identifiers). Tags are how unrelated threads
  that touch the same thing find each other, so reuse spellings that already exist.
- `asserted: true` only when the user said it or you verified it from a file or tool output. An
  inference you have not confirmed is still worth recording, but leave `asserted` unset: it lands as a
  pending draft that cannot enter the always-injected window until it is confirmed. Never promote a
  guess into a remembered fact.
- Never store a credential, a token, or a private key. Writes containing one are refused.
- When something previously recorded turns out to be wrong or has been replaced, use `supersede` with
  the prior `id`, or `retire`. Entries are tombstoned rather than deleted, so "why did we switch?"
  stays answerable a year later.
- Set `pinned: true` only for constraints that must be in front of you every session regardless of
  topic. The window is byte-capped, so pinning is scarce space and over-pinning is reported.
- Use `op=note` to append an annotation to an existing entry instead of duplicating it.
- `scope=global` is for things that are true of you and your environment across every project; project
  is the default and never leaks into another repository.

Two entries being similar is not a reason to merge them, and a disagreement between two entries is not
a reason to silently overwrite one — that is precisely the thing to surface ("earlier you said X, now
Y; which stands?"). Merges and scope-narrowing qualifications are proposed to the user and stay pending
until approved. Consequential changes may be held for confirmation by policy; if a write comes back
`pending`, say so rather than implying it was recorded.

Before adding something, consider whether the conversation just restates an entry you already wrote —
the tool reports the footprint so you can see what another entry will cost the context window.
