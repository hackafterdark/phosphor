package memory

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// rivalWriter drives a second handle over the same index outside the store and
// its handle cache, standing in for the second Phosphor process in the "two
// apps, one workspace" scenario: same file, same WAL protocol, an independent
// connection that can own the write lock.
//
// It waits on the long busy timeout rather than the store's short one, because
// it plays the incumbent: its own COMMIT has to be able to wait its turn behind
// a store transaction that happened to win the lock a moment earlier, and a
// rival that gave up on its own lock wait could strand the test holding a lock
// nobody can hand back.
type rivalWriter struct {
	db *sql.DB
	tx *sql.Tx
}

// startRivalWriter takes the WAL write lock and holds it until release. It is
// started before the contended write so the contention is real from the very
// first attempt rather than raced into existence.
func startRivalWriter(t *testing.T, store *Store, scope Scope, marker string) *rivalWriter {
	t.Helper()
	b := store.bankFor(scope)
	dbPath := filepath.Join(b.dir, IndexDirName, IndexFileName)
	pragmas := map[string]string{
		"journal_mode": "WAL",
		"synchronous":  "NORMAL",
		"busy_timeout": "30000",
	}
	db, err := sql.Open("sqlite", memoryDSN(dbPath, pragmas))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "INSERT INTO tags(term, kind) VALUES(?, 'vocab')", marker)
	require.NoError(t, err)
	// Let the lock be observably held before the test's own writer starts
	// queueing behind it.
	time.Sleep(50 * time.Millisecond)
	return &rivalWriter{db: db, tx: tx}
}

// release hands the write lock back. It is a Commit over a transaction that
// already owns the lock, so it never waits on anything and can be run from an
// observer while the contended write is still retrying — which is the only way
// to show a write riding a lock out rather than merely failing against one.
func (r *rivalWriter) release(t *testing.T) {
	t.Helper()
	require.NoError(t, r.tx.Commit(), "the rival must be able to hand the lock back")
}

// runAgainstHeldLock runs the contended write in the background and hands the
// lock back the instant the store has demonstrably been turned away from it, so
// the write has to survive by retrying rather than by never meeting the
// contention.
func runAgainstHeldLock(t *testing.T, store *Store, scope Scope, rival *rivalWriter, write func() error) error {
	t.Helper()
	seen := store.BusyRetries(scope)
	done := make(chan error, 1)
	go func() { done <- write() }()
	deadline := time.Now().Add(20 * time.Second)
	for store.BusyRetries(scope) == seen {
		if time.Now().After(deadline) {
			t.Fatal("the write never met the held lock, so the contention was not real")
		}
		time.Sleep(2 * time.Millisecond)
	}
	rival.release(t)
	select {
	case err := <-done:
		return err
	case <-time.After(20 * time.Second):
		t.Fatal("the write neither landed nor failed after the lock was handed back")
		return nil
	}
}

// plantRivalWriter is the never-frees variant of a rival: it takes the write
// lock and keeps it for the rest of the test, so every attempt the store makes
// is contended.
func plantRivalWriter(t *testing.T, store *Store, scope Scope, marker string) *rivalWriter {
	t.Helper()
	r := startRivalWriter(t, store, scope, marker)
	t.Cleanup(func() { _ = r.tx.Rollback() })
	return r
}

func TestSharedOpenHandsOutOneHandleSet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	workspace := t.TempDir()

	first, err := Open(OpenOptions{GlobalDir: dir, WorkspaceDir: workspace, Shared: true})
	require.NoError(t, err)
	second, err := Open(OpenOptions{GlobalDir: dir, WorkspaceDir: workspace, Shared: true})
	require.NoError(t, err)
	require.Same(t, first, second, "two openers of the same vaults must share one handle set")

	// The shared store survives any one user closing; only the last close may
	// drop the handles, so a per-call tool close can never yank the connection
	// out from under a still-running watcher or prompt build.
	require.NoError(t, first.Close())
	third, err := Open(OpenOptions{GlobalDir: dir, WorkspaceDir: workspace, Shared: true})
	require.NoError(t, err)
	require.Same(t, first, third, "a store with refs left must still be adoptable after another user closed")

	require.NoError(t, second.Close())
	require.NoError(t, third.Close())

	// Last close evicts; the next open must build fresh handles.
	fourth, err := Open(OpenOptions{GlobalDir: dir, WorkspaceDir: workspace, Shared: true})
	require.NoError(t, err)
	require.NotSame(t, first, fourth)
	require.NoError(t, fourth.Close())
}

func TestUnsharedOpenIsNeverCached(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := Open(OpenOptions{GlobalDir: dir, Shared: false})
	require.NoError(t, err)
	b, err := Open(OpenOptions{GlobalDir: dir, Shared: false})
	require.NoError(t, err)
	require.NotSame(t, a, b, "an unshared open must not adopt a cached store")
	require.NoError(t, a.Close())
	require.NoError(t, b.Close())
	require.NoError(t, a.Close(), "close is idempotent")
}

// The core of the two-apps-one-workspace contract: contention from a parallel
// instance has to cost the writer retries and still land the write, never lose
// it and never park the caller in one long blind sleep.
func TestWriteRidesOutAnotherProcessWriteLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t, DefaultSettings())
	rival := startRivalWriter(t, store, ScopeGlobal, "rival-ride-through")

	err := runAgainstHeldLock(t, store, ScopeGlobal, rival, func() error {
		return store.Put(ctx, Entry{
			ID:      NewID("t", "Lock contention rides through"),
			Type:    TypeFact,
			Thread:  "t",
			Summary: "Lock contention rides through",
			Body:    "the writer retried with backoff while another process held the wal write lock",
			Status:  StatusActive,
		})
	})
	require.NoError(t, err, "the write must land once the rival hands the lock back")

	// The search reads the fts body index, so the probe term has to be body text.
	hits, err := store.Search(ctx, SearchQuery{Query: "backoff", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, hits, "the retried write must be readable")
}

// The other half: when the lock never frees, the write has to fail inside its
// budget with an error that names the contention. An unbounded wait behind a
// parallel instance is exactly what made a second app look like a crash.
func TestWriteFailsLoudlyWhenTheLockNeverFrees(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t, Settings{WriteRetries: 2})
	plantRivalWriter(t, store, ScopeGlobal, "rival-never-frees")

	seen := store.BusyRetries(ScopeGlobal)
	start := time.Now()
	err := store.Put(ctx, Entry{
		ID:      NewID("t", "Lock never frees"),
		Type:    TypeFact,
		Thread:  "t",
		Summary: "Lock never frees",
		Body:    "the writer gives up with a busy error instead of sleeping for thirty seconds",
		Status:  StatusActive,
	})
	require.Error(t, err, "an unbreakable lock must surface as an error")
	require.True(t, strings.Contains(strings.ToLower(err.Error()), "busy"),
		"the failure must name the lock contention: %v", err)
	require.Greater(t, store.BusyRetries(ScopeGlobal), seen, "the failure must come from retried contention")
	require.Less(t, time.Since(start), 10*time.Second, "giving up must stay far inside the old 30s blind wait")
}

func TestWritesSerializeAcrossManyWriters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t, DefaultSettings())

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := store.Put(ctx, Entry{
				ID:      NewID("stress", fmt.Sprintf("Concurrent entry %d", i)),
				Type:    TypeFact,
				Thread:  "stress",
				Summary: fmt.Sprintf("Concurrent entry %d", i),
				Body:    fmt.Sprintf("body %d written by a concurrent writer contending for the wal write lock", i),
				Status:  StatusActive,
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	hits, err := store.Search(ctx, SearchQuery{Query: "concurrent", Limit: 50})
	require.NoError(t, err)
	require.Len(t, hits, writers, "every contended write must land exactly once")
}

// WAL hands readers a snapshot that never queues behind the writer; that is
// only true while reads ride their own pool, so a parallel instance owning the
// write lock must not be able to stall a search.
func TestReadsNeverBlockBehindTheWriteLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t, DefaultSettings())
	require.NoError(t, store.Put(ctx, Entry{
		ID:      NewID("t", "Readable under lock"),
		Type:    TypeFact,
		Thread:  "t",
		Summary: "Readable under lock",
		Body:    "readers ride beside the writer under wal",
		Status:  StatusActive,
	}))
	plantRivalWriter(t, store, ScopeGlobal, "rival-read-block")

	// A writer of ours queued behind the held lock, so the read is being asked
	// for while the vault's write lock is genuinely owned elsewhere rather than
	// merely idle.
	seen := store.BusyRetries(ScopeGlobal)
	go func() {
		_ = store.Put(ctx, Entry{
			ID:      NewID("t", "Queued behind the held lock"),
			Type:    TypeFact,
			Thread:  "t",
			Summary: "Queued behind the held lock",
			Body:    "this write exists only to keep a writer queued behind the held lock",
			Status:  StatusActive,
		})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for store.BusyRetries(ScopeGlobal) == seen {
		if time.Now().After(deadline) {
			t.Fatal("the queued write never met the held lock")
		}
		time.Sleep(2 * time.Millisecond)
	}

	done := make(chan error, 1)
	go func() {
		_, err := store.Search(ctx, SearchQuery{Query: "readers", Limit: 5})
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("a read blocked behind another process's write lock")
	}
}
