package memory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

// searchRows executes the assembled FTS5 query against one vault. Ranking is
// BM25 with trust as the tie-break, and the snippet comes from SQLite so a hit
// costs the relevant sentence rather than the whole document.
func (s *Store) searchRows(ctx context.Context, b *bank, cond []string, args []any, q SearchQuery) ([]Hit, error) {
	if len(cond) == 0 {
		return nil, nil
	}
	// bm25() and snippet() both require a MATCH constraint to exist, but a
	// keyword-less continuation search filters on the thread alone. Without a
	// match term the ranking falls back to the lifecycle profile and the summary
	// stands in for the snippet, so "continue the thread" resolves instead of
	// erroring on the rank function.
	var (
		stmt string
		bind []any
	)
	if q.Query != "" {
		bind = append([]any{"[", "]"}, args...)
		stmt = fmt.Sprintf(`SELECT %s, snippet(entries_fts, 0, ?, ?, ' ... ', 12) AS snippet
			FROM entries_fts JOIN entries e ON e.id = entries_fts.id
			WHERE %s
			ORDER BY bm25(entries_fts) ASC, e.trust DESC
			LIMIT ?`,
			prefixed(entryColumns, "e"), strings.Join(cond, " AND "))
	} else {
		bind = append([]any{}, args...)
		stmt = fmt.Sprintf(`SELECT %s, e.summary AS snippet
			FROM entries_fts JOIN entries e ON e.id = entries_fts.id
			WHERE %s
			ORDER BY e.hot_score DESC, e.trust DESC, e.id ASC
			LIMIT ?`,
			prefixed(entryColumns, "e"), strings.Join(cond, " AND "))
	}
	bind = append(bind, q.Limit)

	rows, err := b.read.QueryContext(ctx, stmt, bind...)
	if err != nil {
		return nil, fmt.Errorf("search memory: %w", err)
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		r, err := scanRow[entryRow](rows)
		if err != nil {
			return nil, fmt.Errorf("scan search row: %w", err)
		}
		hit := Hit{Entry: r.entry(b.scope), Snippet: sanitize(r.Snippet)}
		// A row whose tamper seal no longer recomputes is held out of recall even if
		// it was never re-indexed after the change, which is the direct-DB-tampering
		// case the ingest-time quarantine does not cover.
		if !s.entryVerifies(hit.Entry) {
			s.verifyDrops.Add(1)
			continue
		}
		hit.Score = hit.Trust
		hit.Why = explain(hit, q)
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search rows: %w", err)
	}
	return hits, nil
}

// prefixed qualifies every column of a select list with a table alias so a join
// against the FTS table stays unambiguous.
func prefixed(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// ftsQuery turns free text into an FTS5 MATCH expression. Tokens are quoted so a
// query containing FTS syntax characters is searched for literally instead of
// becoming a syntax error or an unintended operator.
func ftsQuery(q string) string {
	tokens := Tokenize(q, 0)
	if len(tokens) == 0 {
		trimmed := strings.TrimSpace(q)
		if trimmed == "" {
			return ""
		}
		return quoteFTS(trimmed)
	}
	quoted := make([]string, 0, len(tokens))
	for _, t := range tokens {
		quoted = append(quoted, quoteFTS(t))
	}
	return strings.Join(quoted, " OR ")
}

func quoteFTS(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// explain is the "why recalled" string. An adaptive or relevance-ranked system
// that cannot say why it surfaced something is not trustable, so the reason ships
// with the hit instead of being reconstructed later.
func explain(h Hit, q SearchQuery) string {
	parts := []string{}
	if h.Thread != "" {
		parts = append(parts, "thread:"+h.Thread)
	}
	if len(q.Tags) > 0 {
		parts = append(parts, "tags:"+strings.Join(q.Tags, ","))
	}
	if h.Pinned {
		parts = append(parts, "pinned")
	}
	parts = append(parts, fmt.Sprintf("trust=%.2f", h.Trust))
	if h.HotScore != 0 {
		parts = append(parts, fmt.Sprintf("hot=%.2f", h.HotScore))
	}
	if h.LastUsed != "" {
		parts = append(parts, "used:"+h.LastUsed)
	}
	return strings.Join(parts, " ")
}

// Get loads entries by id with their full body: the third rung of the
// index-to-summary-to-body ladder. It records the recall so the lifecycle can
// promote what actually gets used.
func (s *Store) Get(ctx context.Context, ids ...string) ([]Entry, error) {
	ctx, span := otel.StartSpan(ctx, "memory.get")
	defer span.End()
	span.SetAttributes(attribute.Int("phosphor.memory.ids", len(ids)))
	var out []Entry
	for _, id := range ids {
		for _, b := range s.banks() {
			if b == nil {
				continue
			}
			// The read ladder is agent-facing, so it excludes what the exclusion
			// invariants exclude everywhere else: a hand-retired tombstone or an
			// unconfirmed draft is structurally unable to reach a prompt, even by
			// direct id. Lifecycle paths act on those rows through SetStatus, which
			// reads the row directly, so nothing here blocks confirming or retiring.
			list, err := s.queryEntries(ctx, b, entryColumns,
				"quarantined = 0 AND status IN (?, ?) AND id = ?",
				string(StatusActive), string(StatusCold), id)
			if err != nil {
				return nil, err
			}
			if len(list) == 0 {
				continue
			}
			out = append(out, list[0])
			if err := b.execWrite(ctx,
				"UPDATE entries SET recall_count = recall_count + 1, last_used = ? WHERE id = ?",
				s.now().UTC().Format(time.RFC3339), id); err != nil {
				return nil, fmt.Errorf("record recall: %w", err)
			}
			break
		}
	}
	return out, nil
}

// ByThread returns the live entries of one thread, newest first: the query that
// makes "continue where we left off" resolve to something concrete.
func (s *Store) ByThread(ctx context.Context, thread string, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 20
	}
	var out []Entry
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		list, err := s.queryEntries(ctx, b, entryColumns,
			"quarantined = 0 AND status IN ('active','cold') AND thread = ? ORDER BY updated_ts DESC LIMIT "+fmt.Sprintf("%d", limit),
			strings.TrimSpace(thread))
		if err != nil {
			return nil, err
		}
		out = append(out, list...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Neighbors returns the one-hop related entries over the derived edges. Kept to a
// single join on purpose: there is no path engine here by design.
func (s *Store) Neighbors(ctx context.Context, id string) ([]Hit, error) {
	var hits []Hit
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		stmt := fmt.Sprintf(`SELECT DISTINCT %s, d.kind AS kind
				FROM edges d JOIN entries e ON e.id = d.dst
				WHERE d.src = ? AND e.quarantined = 0 AND e.status = 'active'
				LIMIT 20`, prefixed(entryColumns, "e"))
		rows, err := b.read.QueryContext(ctx, stmt, id)
		if err != nil {
			return nil, fmt.Errorf("neighbors: %w", err)
		}
		for rows.Next() {
			r, err := scanRow[entryRow](rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan neighbor row: %w", err)
			}
			e := r.entry(b.scope)
			if !s.entryVerifies(e) {
				s.verifyDrops.Add(1)
				continue
			}
			hits = append(hits, Hit{Entry: e, Score: e.Trust, Snippet: sanitize(e.Summary), Why: "linked:" + r.Kind})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate neighbor rows: %w", err)
		}
	}
	return hits, nil
}

// Dependents lists the entries derived from a decision, so flipping a decision
// surfaces its dependents for review instead of letting them drift silently.
func (s *Store) Dependents(ctx context.Context, decisionID string) ([]Entry, error) {
	var out []Entry
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		list, err := s.queryEntries(ctx, b, entryColumns, "quarantined = 0 AND from_decision = ?", decisionID)
		if err != nil {
			return nil, err
		}
		out = append(out, list...)
	}
	return out, nil
}

// TagsOf returns the canonical tags on an entry.
func (s *Store) TagsOf(ctx context.Context, id string) ([]string, error) {
	var out []string
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		rows, err := b.read.QueryContext(ctx, `SELECT t.term AS term FROM entry_tags et JOIN tags t ON t.id = et.tag_id
				WHERE et.entry_id = ? ORDER BY t.term`, id)
		if err != nil {
			return nil, fmt.Errorf("tags of: %w", err)
		}
		for rows.Next() {
			var term string
			if err := rows.Scan(&term); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan tag row: %w", err)
			}
			out = append(out, term)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate tag rows: %w", err)
		}
	}
	return out, nil
}

// dropRow removes the derived rows for an entry, used when a file becomes
// unparseable or is deleted by hand. A person deleting a note is the strongest
// "this was wrong or useless" signal available without a model call.
func (s *Store) dropRow(ctx context.Context, b *bank, id, path string) error {
	return s.tx(ctx, b, func(tx *sql.Tx) error {
		if id != "" {
			deletes := []struct {
				stmt string
				args []any
			}{
				{"DELETE FROM entries_fts WHERE id = ?", []any{id}},
				{"DELETE FROM entry_tags WHERE entry_id = ?", []any{id}},
				{"DELETE FROM edges WHERE src = ? OR dst = ?", []any{id, id}},
				{"DELETE FROM entries WHERE id = ?", []any{id}},
			}
			for _, d := range deletes {
				if _, err := tx.ExecContext(ctx, d.stmt, d.args...); err != nil {
					return fmt.Errorf("drop %q: %w", firstLine(d.stmt), err)
				}
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM file_hashes WHERE path = ?", path); err != nil {
			return fmt.Errorf("drop ledger row: %w", err)
		}
		return nil
	})
}

// SetStatus moves an entry to a lifecycle state and rewrites its file, so the
// markdown always says what the index believes.
func (s *Store) SetStatus(ctx context.Context, scope Scope, id string, status Status, supersedes string) error {
	return s.rewrite(ctx, s.bankFor(scope), id, func(e *Entry) {
		e.Status = status
		if supersedes != "" {
			e.Supersedes = supersedes
		}
	})
}

// SetPinned pins or unpins an entry. Pinned content is excluded from eviction by
// definition, which is how a human-asserted constraint holds its place in the
// always-injected window.
func (s *Store) SetPinned(ctx context.Context, scope Scope, id string, pinned bool) error {
	return s.rewrite(ctx, s.bankFor(scope), id, func(e *Entry) { e.Pinned = pinned })
}

// ListPending surfaces the unconfirmed drafts for the human review surface. It
// is deliberately the operator's lane, outside the agent-facing read ladder:
// that ladder keeps excluding pending rows so a draft cannot reach a prompt by
// any path other than through a decision, and this is the one enumeration that
// walks them, newest first, so /memory review has what it is deciding on.
func (s *Store) ListPending(ctx context.Context, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 25
	}
	var out []Entry
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		list, err := s.queryEntries(ctx, b, entryColumns,
			"quarantined = 0 AND status = ?", string(StatusPending))
		if err != nil {
			return nil, err
		}
		for _, e := range list {
			e.Scope = b.scope
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SetTrust adjusts the confidence score after feedback.
func (s *Store) SetTrust(ctx context.Context, scope Scope, id string, trust float64) error {
	if trust < 0 {
		trust = 0
	}
	if trust > 1 {
		trust = 1
	}
	return s.rewrite(ctx, s.bankFor(scope), id, func(e *Entry) { e.Trust = trust })
}

// AddNote appends to the human region under the agent's own marker, leaving
// whatever a person wrote above it untouched.
func (s *Store) AddNote(ctx context.Context, scope Scope, id, note string) error {
	return s.rewrite(ctx, s.bankFor(scope), id, func(e *Entry) {
		line := "> _agent: " + strings.TrimSpace(note) + "_"
		if strings.Contains(e.Notes, line) {
			return
		}
		switch {
		case e.Notes == "":
			e.Notes = line
		default:
			e.Notes = e.Notes + "\n>\n" + line
		}
	})
}

// SetHelpful records feedback: the signal both the lifecycle score and the
// adaptive gate learn from.
func (s *Store) SetHelpful(ctx context.Context, scope Scope, id string, helpful bool) error {
	b := s.bankFor(scope)
	delta := 1
	if !helpful {
		delta = -1
	}
	if err := b.execWrite(ctx,
		"UPDATE entries SET helpful_count = MAX(helpful_count + ?, 0), trust = MIN(1.0, MAX(0.0, trust + ?)) WHERE id = ?",
		delta, float64(delta)/20.0, id); err != nil {
		return fmt.Errorf("record helpful: %w", err)
	}
	return nil
}

// rewrite mutates the system-owned fields of one entry and writes the file back,
// preserving the human region verbatim. When the file is missing the row is the
// only truth left, so it is materialized back into markdown rather than drifting
// as an index-only record.
func (s *Store) rewrite(ctx context.Context, b *bank, id string, fn func(*Entry)) error {
	ctx, span := otel.StartSpan(ctx, "memory.rewrite")
	defer span.End()
	span.SetAttributes(attribute.String("phosphor.memory.id", id))
	list, err := s.queryEntries(ctx, b, entryColumns, "id = ?", id)
	if err != nil {
		otel.RecordError(span, err)
		return err
	}
	if len(list) == 0 {
		return fmt.Errorf("no memory entry %q", id)
	}
	e := list[0]
	// When the file is missing the row is the only truth left, so it is used as
	// the draft and materialized back into markdown; when it is present the
	// human's words are kept verbatim by reading it.
	path := EntryPath(b.dir, id)
	if raw, rerr := os.ReadFile(path); rerr == nil {
		parsed, perr := ParseEntryBytes(path, raw)
		if perr != nil {
			return perr
		}
		e = parsed
	}
	e.Scope = b.scope
	// trust, status, and source are system-owned fields, and the file may be a
	// hand-edited one, so all three come from the row regardless of which copy
	// seeded the entry. The mutator fn still overrides whatever field it exists
	// to change; this only stops an untrusted frontmatter value riding along
	// inside a rewrite that meant to touch something else.
	e.Trust = list[0].Trust
	e.Status = list[0].Status
	e.Source = list[0].Source
	fn(&e)
	e.Updated = s.now().UTC().Format(time.RFC3339)
	if e.ID == "" {
		e.ID = id
	}
	return s.Put(ctx, e)
}

// ThreadSummary is a names-only line for the continuation hint: names and
// recency, never facts, so the standing t0 cost stays a few dozen tokens.
type ThreadSummary struct {
	Thread   string `db:"thread" json:"thread"`
	Entries  int64  `db:"n" json:"entries"`
	Touched  string `db:"touched" json:"touched"`
	Headline string `db:"headline" json:"headline"`
}

// ActiveThreads returns the most recently touched threads for the hint block.
func (s *Store) ActiveThreads(ctx context.Context, limit int) ([]ThreadSummary, error) {
	if limit <= 0 {
		limit = s.settings.ThreadHintMaxLines
	}
	var out []ThreadSummary
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		rows, err := b.read.QueryContext(ctx, `SELECT thread, COUNT(1) AS n, MAX(updated_ts) AS touched,
				COALESCE((SELECT summary FROM entries e2 WHERE e2.thread = e.thread AND e2.quarantined = 0
					AND e2.status = 'active' ORDER BY e2.hot_score DESC, e2.updated_ts DESC, e2.id ASC LIMIT 1), '') AS headline
			FROM entries e
			WHERE quarantined = 0 AND status = 'active' AND thread <> ''
				GROUP BY thread ORDER BY touched DESC, thread ASC LIMIT ?`, limit)
		if err != nil {
			return nil, fmt.Errorf("active threads: %w", err)
		}
		for rows.Next() {
			t, err := scanRow[ThreadSummary](rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan thread row: %w", err)
			}
			out = append(out, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate thread rows: %w", err)
		}
	}
	// Ties break on the thread name, never on arrival order. The hint block is part
	// of the bytes every prompt opens with, so a nomination set that shuffled
	// between renders of an unchanged vault would bust the provider prefix cache
	// on its own; SQLite promises no order among equal timestamps, so the tie is
	// pinned here rather than inherited.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Touched != out[j].Touched {
			return out[i].Touched > out[j].Touched
		}
		return out[i].Thread < out[j].Thread
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Stats is the budget report the memory tool returns, so the agent can see what a
// further write would cost before it makes it.
type Stats struct {
	Total       int `json:"total"`
	Active      int `json:"active"`
	Hot         int `json:"hot"`
	Pending     int `json:"pending"`
	Retired     int `json:"retired"`
	Quarantined int `json:"quarantined"`
	Tags        int `json:"tags"`
	Injected    int `json:"injected_bytes"`
	InjectLimit int `json:"inject_limit"`
	// InjectLimitRequested is the raw configured budget before the ceiling
	// clamped it. It is zero when the operator never configured one, and it
	// exceeds InjectLimit only when a runaway config value was clamped.
	InjectLimitRequested int            `json:"inject_limit_requested"`
	ByType               map[string]int `json:"by_type"`
}

type typeCount struct {
	Type string `db:"type"`
	N    int64  `db:"n"`
}

// Report summarizes the corpus and the current always-injected footprint.
func (s *Store) Report(ctx context.Context) (Stats, error) {
	st := Stats{InjectLimit: s.settings.MaxInjectBytes, InjectLimitRequested: s.injectRequested, ByType: map[string]int{}}
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		// The counters accumulate across banks rather than per bank. A store with a
		// project vault and the global one reports the corpus the agent actually
		// faces, which is both of them; scanning each bank over the same target
		// would leave every number reflecting only the bank read last, while the
		// type histogram below has always summed both.
		single := func(query string, target *int) error {
			var n int
			if err := b.read.QueryRowContext(ctx, query).Scan(&n); err != nil {
				return fmt.Errorf("count for %q: %w", firstLine(query), err)
			}
			*target += n
			return nil
		}
		if err := single("SELECT COUNT(1) FROM entries", &st.Total); err != nil {
			return st, err
		}
		if err := single("SELECT COUNT(1) FROM entries WHERE quarantined = 0 AND status = 'active'", &st.Active); err != nil {
			return st, err
		}
		hot := fmt.Sprintf("SELECT COUNT(1) FROM entries WHERE quarantined = 0 AND status = 'active' AND (pinned = 1 OR hot_score >= %f)", s.settings.PromoteThreshold)
		if err := single(hot, &st.Hot); err != nil {
			return st, err
		}
		if err := single("SELECT COUNT(1) FROM entries WHERE quarantined = 0 AND status = 'pending'", &st.Pending); err != nil {
			return st, err
		}
		if err := single("SELECT COUNT(1) FROM entries WHERE quarantined = 0 AND status = 'retired'", &st.Retired); err != nil {
			return st, err
		}
		if err := single("SELECT COUNT(1) FROM entries WHERE quarantined = 1", &st.Quarantined); err != nil {
			return st, err
		}
		if err := single("SELECT COUNT(1) FROM tags", &st.Tags); err != nil {
			return st, err
		}
		rows, err := b.read.QueryContext(ctx, "SELECT type, COUNT(1) AS n FROM entries WHERE quarantined = 0 AND status = 'active' GROUP BY type")
		if err != nil {
			return st, fmt.Errorf("type histogram: %w", err)
		}
		for rows.Next() {
			tc, err := scanRow[typeCount](rows)
			if err != nil {
				rows.Close()
				return st, fmt.Errorf("scan histogram row: %w", err)
			}
			st.ByType[tc.Type] += int(tc.N)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return st, fmt.Errorf("iterate histogram rows: %w", err)
		}
	}
	block, err := s.TierABlock(ctx)
	if err != nil {
		return st, err
	}
	st.Injected = len(block)
	return st, nil
}
