Fetch the full body of memories you have already surfaced with memory_search, by their ids.

This is the last rung of the index→summary→body ladder: a search returns a cheap summary and a snippet, and
you call this only when the snippet is not enough and you need the whole reasoning — the tradeoff behind a
decision, the full text of a requirement, the annotation a person added under the Notes heading. It returns the
sanitized body plus the human Notes region and the entry's provenance and links.

Pass the ids memory_search gave you; do not invent them. Keep the list small (a couple at a time) — pulling
every body when a summary would have answered the question is exactly the token waste the ladder exists to
avoid.
