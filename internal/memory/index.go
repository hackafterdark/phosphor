package memory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

// Sync reconciles the index against the markdown vaults, re-indexing only files
// whose content hash changed and pruning rows whose file disappeared. It is the
// same content-hash posture the workspace index uses, and it is what makes
// "Obsidian may edit anything at any time" a supported condition rather than a
// race.
func (s *Store) Sync(ctx context.Context) error {
	ctx, span := otel.StartSpan(ctx, "memory.sync")
	defer span.End()
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		if err := s.syncBank(ctx, b); err != nil {
			otel.RecordError(span, err)
			return err
		}
	}
	return nil
}

// SyncIfStale re-indexes only when a file on disk differs from the ledger. The
// tools call it before reading so a human edit made in Obsidian between turns is
// visible without needing a watcher to have fired.
func (s *Store) SyncIfStale(ctx context.Context) (changed bool, err error) {
	ctx, span := otel.StartSpan(ctx, "memory.sync_if_stale")
	defer func() {
		if err != nil {
			otel.RecordError(span, err)
		}
		span.SetAttributes(attribute.Bool("phosphor.memory.changed", changed))
		span.End()
	}()
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	changed = false
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		stale, serr := s.bankHasChanges(b)
		if serr != nil {
			return changed, serr
		}
		if !stale {
			continue
		}
		if err := s.syncBank(ctx, b); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

func (s *Store) bankHasChanges(b *bank) (bool, error) {
	path := filepath.Join(b.dir, EntriesDirName)
	if !dirExists(path) {
		// An empty vault is not a change, but an emptied vault with rows is.
		n, err := s.count(b, "SELECT COUNT(1) FROM entries")
		return n > 0 && err == nil, err
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		seen[filepath.ToSlash(p)] = true
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		hash, ok, err := s.fileHash(b, filepath.ToSlash(p))
		if err != nil {
			return err
		}
		if !ok || hash != HashBytes(raw) {
			return errStopWalk
		}
		return nil
	})
	if err == errStopWalk {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("scan vault: %w", err)
	}
	// Files that vanished from disk but are still in the ledger.
	rows, err := b.read.QueryContext(context.Background(), "SELECT path FROM file_hashes")
	if err != nil {
		return false, fmt.Errorf("read ledger: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var indexed string
		if err := rows.Scan(&indexed); err != nil {
			return false, fmt.Errorf("scan ledger path: %w", err)
		}
		if !seen[indexed] {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) syncBank(ctx context.Context, b *bank) error {
	path := filepath.Join(b.dir, EntriesDirName)
	seen := map[string]bool{}
	if dirExists(path) {
		err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".md") || strings.Contains(p, ".bak.") {
				return nil
			}
			slash := filepath.ToSlash(p)
			seen[slash] = true
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			hash := HashBytes(raw)
			if existing, ok, err := s.fileHash(b, slash); err == nil && ok && existing == hash {
				return nil
			}
			return s.indexEntry(ctx, b, p, hash, false)
		})
		if err != nil {
			return fmt.Errorf("rescan vault: %w", err)
		}
	}

	// Prune rows whose file is gone. The tombstone stays out of the corpus, but
	// the audit trail survives in the retire path; a vanished file is a human
	// delete, which is a lifecycle signal, not a silent truncation.
	rows, err := b.read.QueryContext(ctx, "SELECT id, path FROM file_hashes")
	if err != nil {
		return fmt.Errorf("read ledger for prune: %w", err)
	}
	type ledger struct {
		ID   string `db:"id"`
		Path string `db:"path"`
	}
	var stale []ledger
	for rows.Next() {
		l, err := scanRow[ledger](rows)
		if err != nil {
			rows.Close()
			return fmt.Errorf("scan ledger row: %w", err)
		}
		stale = append(stale, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate ledger: %w", err)
	}
	for _, l := range stale {
		if seen[l.Path] {
			continue
		}
		if err := s.removeRow(ctx, b, l.ID, l.Path); err != nil {
			return err
		}
	}
	return nil
}

// removeRow drops the derived rows for a deleted file and records the human
// deletion as a trust drop on any same-id entry that is still alive, because a
// person deleting a note is the strongest "this was wrong or useless" signal
// available without a model call.
func (s *Store) removeRow(ctx context.Context, b *bank, id, path string) error {
	return s.tx(ctx, b, func(tx *sql.Tx) error {
		deletes := []struct {
			stmt string
			args []any
		}{
			{"DELETE FROM entries_fts WHERE id = ?", []any{id}},
			{"DELETE FROM entry_tags WHERE entry_id = ?", []any{id}},
			{"DELETE FROM edges WHERE src = ? OR dst = ?", []any{id, id}},
			{"DELETE FROM entries WHERE id = ?", []any{id}},
			{"DELETE FROM file_hashes WHERE path = ?", []any{path}},
		}
		for _, d := range deletes {
			if _, err := tx.ExecContext(ctx, d.stmt, d.args...); err != nil {
				return fmt.Errorf("prune %q: %w", firstLine(d.stmt), err)
			}
		}
		return nil
	})
}

func (s *Store) fileHash(b *bank, path string) (string, bool, error) {
	var hash string
	err := b.read.QueryRowContext(context.Background(), "SELECT content_hash FROM file_hashes WHERE path = ?", path).Scan(&hash)
	if err != nil {
		return "", false, nil
	}
	return hash, true, nil
}

func (s *Store) count(b *bank, query string) (int, error) {
	var n int
	if err := b.read.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// errStopWalk is the sentinel a filepath walk callback returns to bail out early
// once it has found what it was looking for.
var errStopWalk = fmt.Errorf("stop walk")
