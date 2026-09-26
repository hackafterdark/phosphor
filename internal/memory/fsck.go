package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

// FsckReport is the audit verdict of one /memory fsck pass over both banks.
// It exists to make the §9 security invariant *checkable* rather than merely
// asserted: "every indexed row's sanitization stamp equals the hash of the
// sanitized bytes of the file it was derived from" is only a claim until
// something re-runs the defanger over the corpus and counts the misses.
type FsckReport struct {
	// Scanned counts the entry files read off the vault.
	Scanned int `json:"scanned"`
	// Drifted counts files whose content hash left the ledger — bytes changed
	// outside a system write (an Obsidian edit, a sync, a hand-authored file).
	// Drift is a supported condition, not a fault; the number is disclosed so a
	// quiet edit is never invisible.
	Drifted int `json:"drifted"`
	// Restamped counts rows that were in sight but carried a stamp that no
	// longer recomputed from their file — legacy pre-stamp rows, or a row and a
	// file that diverged without the ledger noticing. The pass forces them
	// through the index so the sanitizer re-witnesses their bytes.
	Restamped int `json:"restamped"`
	// Unstamped counts rows that still hold no stamp after the repair pass. It
	// is the headline audit number: a healthy vault reads zero.
	Unstamped int `json:"unstamped"`
	// BadSource counts rows whose provenance string failed validation; the pass
	// defangs or empties them so an unvalidated citation never renders into
	// the injected badge again.
	BadSource int `json:"bad_source"`
}

// Fsck audits and repairs the sanitization posture of the corpus: it walks the
// vault, forces any row whose stamp no longer recomputes back through the
// index, runs the full Sync reconciliation, then reports what still fails the
// audit. It is safe at any time — the markdown is the truth and the index is
// derived from it — which is why /memory fsck can offer it as the repair for
// every symptom where the panels and the recall disagree with the files.
func (s *Store) Fsck(ctx context.Context) (FsckReport, error) {
	ctx, span := otel.StartSpan(ctx, "memory.fsck")
	defer span.End()
	var rep FsckReport
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		if err := s.fsckBank(ctx, b, &rep); err != nil {
			otel.RecordError(span, err)
			return rep, err
		}
	}
	span.SetAttributes(
		attribute.Int("phosphor.memory.scanned", rep.Scanned),
		attribute.Int("phosphor.memory.drifted", rep.Drifted),
		attribute.Int("phosphor.memory.restamped", rep.Restamped),
		attribute.Int("phosphor.memory.unstamped", rep.Unstamped),
		attribute.Int("phosphor.memory.bad_source", rep.BadSource),
	)
	return rep, nil
}

func (s *Store) fsckBank(ctx context.Context, b *bank, rep *FsckReport) error {
	// Row stamps load once up front so the walk stays a pure read pass; the
	// repair reindexes through the normal path rather than mutating rows in
	// place, which is what keeps the ledger, the FTS rows and the seal
	// consistent with whatever the sanitizer re-witnesses.
	stamps, err := s.rowAuditState(ctx, b)
	if err != nil {
		return err
	}
	dir := filepath.Join(b.dir, EntriesDirName)
	if dirExists(dir) {
		type stale struct {
			path, hash string
		}
		var force []stale
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".md") || strings.Contains(p, ".bak.") {
				return nil
			}
			rep.Scanned++
			raw, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			hash := HashBytes(raw)
			parsed, perr := ParseEntryBytes(p, raw)
			if perr != nil {
				// An unparseable file is Sync's and indexEntry's problem (they
				// quarantine and drop the row); the audit just must not mistake
				// its missing stamp for a healthy one.
				return nil
			}
			seen, have := stamps[parsed.ID]
			if have {
				if seen != sanitizedStamp(parsed.Body, parsed.Notes) {
					force = append(force, stale{path: p, hash: hash})
				}
				if stored, ok, herr := s.fileHash(b, filepath.ToSlash(p)); herr != nil {
					return herr
				} else if ok && stored != hash {
					rep.Drifted++
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("audit walk: %w", err)
		}
		for _, st := range force {
			if err := s.indexEntry(ctx, b, st.path, st.hash, false); err != nil {
				return err
			}
			rep.Restamped++
		}
	}
	// The full reconciliation after the targeted one: drifted and new files are
	// reindexed (their rows arrive with freshly recomputed stamps), vanished
	// files prune, and everything the walk counted converges toward zero.
	if err := s.syncBank(ctx, b); err != nil {
		return err
	}
	if err := b.read.QueryRowContext(ctx, "SELECT COUNT(1) FROM entries WHERE sanitized = ?", "").Scan(&rep.Unstamped); err != nil {
		return fmt.Errorf("count unstamped rows: %w", err)
	}
	bad, err := s.auditSources(ctx, b)
	if err != nil {
		return err
	}
	rep.BadSource += bad
	return nil
}

// rowAuditState loads every row's sanitization stamp for the audit walk. The
// presence of the key is load-bearing here: a file whose id is absent from the
// map is a new file (a different repair than a stale stamp), so it is keyed by
// id rather than kept as a set of healthy ids.
func (s *Store) rowAuditState(ctx context.Context, b *bank) (map[string]string, error) {
	stamps := map[string]string{}
	rows, err := b.read.QueryContext(ctx, "SELECT id, sanitized FROM entries")
	if err != nil {
		return nil, fmt.Errorf("load audit state: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, sanitized string
		if err := rows.Scan(&id, &sanitized); err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}
		stamps[id] = sanitized
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit rows: %w", err)
	}
	return stamps, nil
}

// auditSources re-validates every stored provenance string and repairs the
// ones that fail. Rows written before the gate validated source (or written
// straight into the index by a repair older than this pass) are the population
// it exists for: source renders verbatim into the injected ⟨…⟩ badge, so an
// unvalidated value must not survive an fsck. The defanged form is kept when
// it still passes — the words are the reader's — and only a value the grammar
// still rejects after defanging is emptied.
func (s *Store) auditSources(ctx context.Context, b *bank) (int, error) {
	rows, err := b.read.QueryContext(ctx, "SELECT id, source FROM entries WHERE source <> ?", "")
	if err != nil {
		return 0, fmt.Errorf("load provenance rows: %w", err)
	}
	type row struct {
		id, source string
	}
	var bad []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.source); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan provenance row: %w", err)
		}
		if _, verr := ValidateSource(r.source); verr != nil {
			r.source = ""
			bad = append(bad, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate provenance rows: %w", err)
	}
	if len(bad) == 0 {
		return 0, nil
	}
	slog.Warn("Repairing memory rows whose provenance string failed validation",
		"vault", filepath.ToSlash(b.dir), "count", len(bad))
	err = s.tx(ctx, b, func(tx *sql.Tx) error {
		for _, r := range bad {
			if _, uerr := tx.ExecContext(ctx, "UPDATE entries SET source = ? WHERE id = ?", r.source, r.id); uerr != nil {
				return fmt.Errorf("repair provenance %q: %w", r.id, uerr)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(bad), nil
}
