package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sqlitedrv "modernc.org/sqlite"

	"github.com/hackafterdark/phosphor/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
)

// indexPragmas mirror the workspace index connections: WAL plus a long busy
// timeout is what lets the watcher, the agent, and a parallel session read the
// same vault-backed index while somebody else owns the write lock.
var indexPragmas = map[string]string{
	"journal_mode": "WAL",
	"synchronous":  "NORMAL",
	"busy_timeout": "30000",
	"page_size":    "4096",
	"temp_store":   "MEMORY",
	"cache_size":   "-8000",
}

// writePragmas is the writer-side variant. A second Phosphor (or an agent run
// with `phosphor run`) against the same workspace can hold the WAL write lock
// for the length of its own rescan, so a writer that slept the full 30s inside
// sqlite3_step was turning one contended transaction into a UI freeze in every
// other caller of the same handle. The writer instead waits a short slice, and
// tx() retries with backoff so the total bounded wait is explicit, observable,
// and cancellable through the caller's context.
var writePragmas = map[string]string{
	"journal_mode": "WAL",
	"synchronous":  "NORMAL",
	"busy_timeout": "150",
	"page_size":    "4096",
	"temp_store":   "MEMORY",
	"cache_size":   "-8000",
}

// memoryDSN builds a modernc/sqlite DSN that applies the given pragmas on every
// pooled connection and takes write locks up front (BEGIN IMMEDIATE).
func memoryDSN(dbPath string, pragmas map[string]string) string {
	params := url.Values{}
	for name, value := range pragmas {
		params.Add("_pragma", fmt.Sprintf("%s(%s)", name, value))
	}
	params.Set("_txlock", "immediate")
	return fmt.Sprintf("file:%s?%s", dbPath, params.Encode())
}

// Settings are the tuning knobs the store needs that are not per-call. They come
// from config.Memory so the store stays free of a config dependency.
type Settings struct {
	// MaxInjectBytes is the hard ceiling on the whole always-injected block (Tier
	// A plus the thread hint). Enforced by trimming the lowest-scoring entries
	// rather than by a warning, so memory can never out-grow its share of the
	// window no matter how large the corpus gets.
	MaxInjectBytes int
	// MaxUnusedDays ages entries out to cold so a long-lived vault cannot hoard
	// dead-but-still-hot bytes.
	MaxUnusedDays int
	// HysteresisDelta is the margin an entrant must beat an eviction victim by,
	// which is what stops two topics from ping-ponging the injected block and
	// busting the provider prefix cache.
	HysteresisDelta float64
	// PromoteThreshold is the hot_score a Tier B entry must reach to be considered
	// for promotion.
	PromoteThreshold float64
	// AutoPromote is the tri-state gate on the hot_score half of the promotion
	// clause. nil is on: the automatic path is pure index arithmetic and spends no
	// model call, so unlike distillation it needs no explicit opt-in. false
	// collapses Tier A to pins only, which is the lever to pull while hand-tuning a
	// vault or when trusting the recall signal not to be steering the window.
	AutoPromote *bool
	// AutoPromoteMinTrust is the trust floor the automatic half must clear, no
	// matter how hot a non-pinned entry runs. Only user confirmation raises trust
	// (to 0.8) above the unreviewed 0.5 default, so the shipped floor means
	// unreviewed content structurally cannot self-inject into every prompt.
	AutoPromoteMinTrust float64
	// AutoPromoteSharePct caps the share of MaxInjectBytes the automatic (non-pinned)
	// entries may claim between them, leaving the rest to pins. A recall-flood
	// therefore cannot evict a curated pinned window, no matter how high it scores.
	AutoPromoteSharePct int
	// WriteRetries is how many times a contended write is replayed after sqlite
	// reports the lock taken by another process, with backoff between attempts.
	// The total wait is therefore bounded and visible rather than one long sleep
	// inside the driver, and the caller's context stays in charge throughout.
	WriteRetries int
	// ThreadHintMaxLines caps the names-only continuation hint.
	ThreadHintMaxLines int
	// EnableProse is the kill switch for the write-time keyword/tag enrichment the
	// gated prose runtime performs. nil means the build default: on when the
	// binary linked the phosphor_prose gate, off otherwise.
	EnableProse *bool
	// Integrity seals each entry with a keyed digest so the index can refuse to
	// inject or recall a row whose content was changed outside a system write. It
	// is on by default (nil): the derived index sits in the workspace where anything
	// that can write the project can write it, and the seal is what makes that copy
	// safe to read. The one duty it creates is key custody, answered by announcing
	// the key's first creation and pointing the operator at the recovery phrase.
	Integrity *bool
}

// ProseEnabled resolves the enrichment kill switch against the build gate: the
// runtime runs only when a user has not switched it off and this binary actually
// linked it, which keeps the default build's behaviour dependency-free.
func (s Settings) ProseEnabled() bool {
	if s.EnableProse == nil {
		return proseLinked
	}
	return *s.EnableProse && proseLinked
}

// AutoPromoteEnabled resolves the tri-state against the shipped default: nil is
// on, because promotion is computed in the index and pays nothing to run. Only
// an explicit false collapses Tier A to the pins-only posture.
func (s Settings) AutoPromoteEnabled() bool {
	return s.AutoPromote == nil || *s.AutoPromote
}

// IntegrityEnabled resolves the integrity tri-state against the shipped default,
// which is on: the tamper seal is the posture every vault runs under unless its
// operator explicitly opts out. The asymmetry that once argued for opt-in — that
// minting a signing key quietly burdens someone with a backup duty — is answered
// by minting it loudly: the creation is logged, and the status and the sidebar
// point at /memory key show until the corpus says otherwise. Only an explicit
// false turns the seal off.
func (s Settings) IntegrityEnabled() bool {
	return s.Integrity == nil || *s.Integrity
}

// DefaultSettings is the shipped tuning.
func DefaultSettings() Settings {
	return Settings{
		MaxInjectBytes:      8192,
		MaxUnusedDays:       90,
		HysteresisDelta:     0.15,
		PromoteThreshold:    1.0,
		AutoPromoteMinTrust: 0.75,
		AutoPromoteSharePct: 50,
		WriteRetries:        defaultWriteRetries,
		ThreadHintMaxLines:  5,
	}
}

// MaxInjectBytesCeiling is the absolute upper bound on a configured Tier A
// budget. The always-injected block rides on every prompt, so the budget is a
// standing tax on the context window rather than a burst limit, and this
// ceiling guards typos rather than serving as a tuning surface: 32 KiB is
// roughly eight thousand tokens, a single-digit share of even the smallest
// window this agent plausibly runs against, yet a generous multiple of the
// shipped default.
const MaxInjectBytesCeiling = 32 * 1024

// injectClampWarn keeps the misconfiguration notice to one line per process.
// The memory tools open a store per call, and an obnoxious config value is
// still the single fact it was the first time one opened.
var injectClampWarn sync.Once

// ClampInjectBytes folds a configured Tier A budget down to the ceiling. Zero
// and negative values pass through so the caller's own "unset means default"
// check still gets to fire.
func ClampInjectBytes(n int) int {
	if n > MaxInjectBytesCeiling {
		return MaxInjectBytesCeiling
	}
	return n
}

// Store owns the derived SQLite index over one or two markdown vaults. The
// markdown is the source of truth: delete memory.db, rescan, and the index comes
// back identical.
type Store struct {
	project *bank
	global  *bank

	// syncMu serializes rescans so the watcher, an agent write, and a tool
	// triggered refresh never interleave their index transactions.
	syncMu sync.Mutex

	settings Settings
	now      func() time.Time

	// injectRequested is the Tier A budget the store was opened with before the
	// ceiling clamped it. It is what makes the clamp reportable rather than
	// silent: the effective number alone cannot say whether anyone asked for it.
	injectRequested int

	// integrityKey is the HMAC signing key when the tamper seal is on and nil when
	// it is off. Resolved once at open; a nil under an enabled posture is the
	// fail-closed case where every entry's digest check is made to fail rather
	// than let unsealed content reach the prompt.
	integrityKey []byte

	// verifyDrops counts the rows the read-side seal gate held out of recall this
	// session because their digest failed to recompute — the direct-DB-tampering
	// and lost-key signal, and the counter that turns silent amnesia into a
	// reportable one. It is process-scoped on purpose: whether the corpus is
	// being eaten right now is a session question, while fsck and the persisted
	// quarantined flag own the durable account.
	verifyDrops atomic.Uint64

	// keyJustMinted records that this process created the signing key for the
	// first time, which is the one moment the recovery phrase has to reach the
	// operator before the vault starts feeling like it always had one.
	keyJustMinted atomic.Bool

	// cacheKey is the sharedStores map key when this store is process-shared, and
	// the empty string for an unshared one (tests, the eval harness). refCount
	// keeps the long-lived handles open across the open/close churn of per-call
	// tool use; closeMu makes the final close idempotent and race-free against a
	// parallel Open that might adopt the store just as its last user drops it.
	cacheKey string
	refCount atomic.Int32
	closeMu  sync.Mutex
	closed   bool
}

// bank is one vault's derived index. Reads and writes get separate connection
// pools: WAL gives readers a snapshot that never blocks behind the writer, so a
// search no longer queues behind a 30s busy wait, while every write funnels
// through a single pooled connection that serializes this process's writers in
// front of the file-level lock the other instances contend for.
type bank struct {
	scope Scope
	dir   string
	read  *sql.DB
	write *sql.DB

	// writeRetries is how many times a contended write is replayed after sqlite
	// reports the lock held by another holder, resolved from Settings at open and
	// read-only after. Keeping it per bank rather than process-wide is what stops
	// one agent's tuning from shortening another agent's tolerance for the same
	// file, which matters precisely because several instances are expected to run
	// against one workspace.
	writeRetries int

	// busyRetries counts lock waits this vault's writer had to ride out. It is
	// the number a status view or a test reads to tell "ran clean" apart from
	// "ran clean while a second instance was contending for the same file".
	busyRetries atomic.Int64
}

// OpenOptions selects which vaults a store covers.
type OpenOptions struct {
	// WorkspaceDir is the project root; the project vault lives under
	// .phosphor/memory inside it. Empty means global-only.
	WorkspaceDir string
	// GlobalDir overrides the global vault location (tests).
	GlobalDir string
	Settings  Settings
	// Shared asks for the process-wide handle cache so every opener of the same
	// vaults shares one set of long-lived connections. The app and the memory
	// tools set it: the tools open a store per call, and closing a WAL handle
	// behind another instance's reader has produced WAL desync in this project
	// before. Tests that want an isolated corpus leave it false.
	Shared bool

	// Maintenance opens a store even when the enabled tamper seal cannot load its
	// key. Recall remains fail-closed and normal writes stay refused; the handle
	// exists so the operator can inspect or rotate the key.
	Maintenance bool
}

// sharedStores hands out one long-lived handle set per distinct pair of vaults
// for the life of the process. Two Phosphor instances against one workspace,
// and the watcher, the agent, and a `phosphor run` side by side within one
// process, all end up opening the same memory.db; a cached handle means no
// instance ever closes a WAL connection out from under another one's reader.
var sharedStores sync.Map // string -> *Store

func storeCacheKey(globalDir, workspaceDir string) string {
	key := normalizeForCompare(globalDir)
	if workspaceDir == "" {
		return key
	}
	return key + "|" + normalizeForCompare(ProjectVaultDir(workspaceDir))
}

// adoptShared takes a reference on the cached handle set for key, if one is
// still live. The whole check runs under the candidate's closeMu, which is the
// same lock the last closer takes before dropping the handles, so an adopter
// and a final close cannot both win: either this takes the reference and the
// closer's decrement stops short of zero, or the closer has finished and the
// candidate reports itself closed.
func adoptShared(key string) (*Store, bool) {
	cached, ok := sharedStores.Load(key)
	if !ok {
		return nil, false
	}
	s := cached.(*Store)
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed || s.refCount.Load() <= 0 {
		sharedStores.Delete(key)
		return nil, false
	}
	s.refCount.Add(1)
	return s, true
}

// Open prepares the index over the project and global vaults, creating the
// directories and schema on demand.
func Open(opts OpenOptions) (*Store, error) {
	globalDir := opts.GlobalDir
	if globalDir == "" {
		globalDir = GlobalVaultDir()
	}
	if !opts.Shared || opts.Maintenance {
		return openStore(opts, globalDir, "")
	}
	key := storeCacheKey(globalDir, opts.WorkspaceDir)
	if s, ok := adoptShared(key); ok {
		return s, nil
	}
	s, err := openStore(opts, globalDir, key)
	if err != nil {
		return nil, err
	}
	if _, loaded := sharedStores.LoadOrStore(key, s); loaded {
		// A parallel opener published a handle set first. Adopt it if it is still
		// live and only then drop ours, making ours unshared (empty cacheKey) on
		// the way out so its Close can never evict the winner's map entry when our
		// own last user drops it.
		if p, ok := adoptShared(key); ok {
			_ = s.closeBanks()
			s.cacheKey = ""
			return p, nil
		}
		// The other store was already on its way to closing and retired itself from
		// the map, so publish the fresh handles we just opened instead.
		sharedStores.Store(key, s)
	}
	return s, nil
}

func openStore(opts OpenOptions, globalDir, cacheKey string) (*Store, error) {
	settings := mergeSettings(DefaultSettings(), opts.Settings)
	if opts.Settings.MaxInjectBytes > MaxInjectBytesCeiling {
		injectClampWarn.Do(func() {
			slog.Warn("Configured memory inject budget exceeds the ceiling; enforcing the ceiling",
				"requested", opts.Settings.MaxInjectBytes, "ceiling", MaxInjectBytesCeiling)
		})
	}
	s := &Store{
		settings:        settings,
		injectRequested: opts.Settings.MaxInjectBytes,
		now:             time.Now,
		cacheKey:        cacheKey,
	}
	s.refCount.Add(1)

	global, err := openBank(ScopeGlobal, globalDir, s.settings)
	if err != nil {
		return nil, err
	}
	s.global = global

	if opts.WorkspaceDir != "" {
		project, err := openBank(ScopeProject, ProjectVaultDir(opts.WorkspaceDir), s.settings)
		if err != nil {
			_ = global.close()
			return nil, err
		}
		s.project = project
		// Seeding the vocabulary is deliberately not done here. Open runs once per
		// memory tool call, and seeding is a write transaction over the same file the
		// vault watcher indexes, so seeding on every open put a contended write lock
		// in front of the hot read path. The app seeds it once per process instead,
		// see internal/app. Best-effort either way: a workspace without a go.mod or
		// Taskfile simply has no vocabulary yet.
	}
	initCtx, cancelInit := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelInit()
	if err := s.initIntegrity(initCtx); err != nil {
		if !opts.Maintenance {
			if s.project != nil {
				_ = s.project.close()
			}
			_ = global.close()
			return nil, err
		}
	}
	if s.integrityActive() && len(s.integrityKey) > 0 {
		// Seal a corpus written before the seal existed, once per bank, so enabling
		// the feature over an existing vault — or rebuilding a bank's index after its
		// database was deleted, which the derived store treats as routine — does not
		// quarantine it whole. The pass syncs first, because an index rebuilt from
		// the files has no rows to bless until then. The guard is per bank and lives
		// in the global index so a later open does not re-run it: a stripped digest
		// must stay caught, not get silently re-blessed.
		bctx, cancel := context.WithTimeout(context.Background(), integrityBootstrapTimeout)
		defer cancel()
		if _, err := s.bootstrapOnce(bctx); err != nil {
			slog.Warn("Failed to seal the memory vault at integrity enablement", "error", err)
		}
	}
	return s, nil
}

func mergeSettings(def, over Settings) Settings {
	out := def
	if over.MaxInjectBytes > 0 {
		out.MaxInjectBytes = ClampInjectBytes(over.MaxInjectBytes)
	}
	if over.MaxUnusedDays > 0 {
		out.MaxUnusedDays = over.MaxUnusedDays
	}
	if over.HysteresisDelta > 0 {
		out.HysteresisDelta = over.HysteresisDelta
	}
	if over.PromoteThreshold > 0 {
		out.PromoteThreshold = over.PromoteThreshold
	}
	if over.AutoPromote != nil {
		out.AutoPromote = over.AutoPromote
	}
	if over.AutoPromoteMinTrust > 0 {
		out.AutoPromoteMinTrust = over.AutoPromoteMinTrust
	}
	if over.AutoPromoteSharePct > 0 {
		out.AutoPromoteSharePct = over.AutoPromoteSharePct
	}
	if over.WriteRetries > 0 {
		out.WriteRetries = over.WriteRetries
	}
	// Zero here is an intentional disable (the hint is the only setting where 0
	// carries meaning), so only a negative from the config layer means "unset".
	if over.ThreadHintMaxLines >= 0 {
		out.ThreadHintMaxLines = over.ThreadHintMaxLines
	}
	if over.EnableProse != nil {
		out.EnableProse = over.EnableProse
	}
	if over.Integrity != nil {
		out.Integrity = over.Integrity
	}
	return out
}

func openBank(scope Scope, dir string, settings Settings) (*bank, error) {
	if err := os.MkdirAll(filepath.Join(dir, EntriesDirName), 0o755); err != nil {
		return nil, fmt.Errorf("create vault %s: %w", filepath.ToSlash(dir), err)
	}
	dbPath := filepath.Join(dir, IndexDirName, IndexFileName)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create index dir: %w", err)
	}
	db, err := sql.Open("sqlite", memoryDSN(dbPath, writePragmas))
	if err != nil {
		return nil, fmt.Errorf("open memory db: %w", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping memory db: %w", err)
	}
	// One writer connection: SQLite serializes writes at the file level anyway,
	// and funneling this process's writers through a single pooled handle keeps
	// the cross-process lock contention to one waiter at a time. Readers get
	// their own pool, which WAL lets run beside the writer without blocking.
	db.SetMaxOpenConns(1)
	read, err := sql.Open("sqlite", memoryDSN(dbPath, indexPragmas))
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open memory read db: %w", err)
	}
	if err := read.PingContext(context.Background()); err != nil {
		read.Close()
		db.Close()
		return nil, fmt.Errorf("ping memory read db: %w", err)
	}
	read.SetMaxOpenConns(4)
	b := &bank{scope: scope, dir: dir, read: read, write: db, writeRetries: settings.WriteRetries}
	if err := b.createSchema(context.Background()); err != nil {
		_ = read.Close()
		db.Close()
		return nil, fmt.Errorf("create memory schema: %w", err)
	}
	return b, nil
}

func (b *bank) close() error {
	var errs []string
	if b.read != nil {
		if err := b.read.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if b.write != nil {
		if err := b.write.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close bank %s: %s", b.scope, strings.Join(errs, "; "))
	}
	return nil
}

// schemaStmts is the canonical DDL. Every statement is IF NOT EXISTS plus a
// schema-version stamp, so an upgrade only ever adds, and deleting the file
// rebuilds everything from markdown.
func (b *bank) schemaStmts() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS entries (
			id TEXT PRIMARY KEY,
			scope TEXT NOT NULL,
			type TEXT NOT NULL,
			summary TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			thread TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			trust REAL NOT NULL DEFAULT 0.5,
			hot_score REAL NOT NULL DEFAULT 0.0,
			pinned INTEGER NOT NULL DEFAULT 0,
			recall_count INTEGER NOT NULL DEFAULT 0,
			helpful_count INTEGER NOT NULL DEFAULT 0,
			created_ts TEXT NOT NULL,
			updated_ts TEXT NOT NULL,
			last_used TEXT NOT NULL DEFAULT '',
			expires TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			supersedes TEXT NOT NULL DEFAULT '',
			from_decision TEXT NOT NULL DEFAULT '',
				owner TEXT NOT NULL DEFAULT 'agent',
				asserted INTEGER NOT NULL DEFAULT 0,
				quarantined INTEGER NOT NULL DEFAULT 0,
				mac TEXT NOT NULL DEFAULT '',
				sanitized TEXT NOT NULL DEFAULT ''
			)`,
		// The FTS table indexes the body only. Metadata lives in ordinary columns,
		// which is what makes a returned snippet structurally unable to straddle
		// the YAML fence and inject a corrupt frontmatter fragment.
		`CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
			body,
			id UNINDEXED,
			thread UNINDEXED,
			type UNINDEXED
		)`,
		`CREATE TABLE IF NOT EXISTS tags (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			term TEXT NOT NULL UNIQUE,
			kind TEXT NOT NULL DEFAULT 'vocab'
		)`,
		`CREATE TABLE IF NOT EXISTS entry_tags (
			entry_id TEXT NOT NULL,
			tag_id INTEGER NOT NULL,
			PRIMARY KEY (entry_id, tag_id)
		)`,
		// The little graph: one edge kind per relation, queried as a join, never
		// walked as a traversal.
		`CREATE TABLE IF NOT EXISTS edges (
			src TEXT NOT NULL,
			dst TEXT NOT NULL,
			kind TEXT NOT NULL,
			PRIMARY KEY (src, dst, kind)
		)`,
		// The ledger binds a file to its hash and to the entry it is indexed as.
		// The id is what lets the prune in Sync reach the derived rows of a
		// hand-deleted note without reparsing bytes that no longer exist.
		`CREATE TABLE IF NOT EXISTS file_hashes (
			path TEXT PRIMARY KEY,
			content_hash TEXT NOT NULL,
			id TEXT NOT NULL
		)`,
		// Learned gate state. Deliberately not in phosphor.json: intent lives in the
		// config file, learning lives here.
		`CREATE TABLE IF NOT EXISTS buckets (
			bucket TEXT PRIMARY KEY,
			ask INTEGER NOT NULL DEFAULT -1,
			auto_lean REAL NOT NULL DEFAULT 0.0,
			risk REAL NOT NULL DEFAULT 0.0,
			samples INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS asklog (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TEXT NOT NULL,
			session_id TEXT NOT NULL DEFAULT '',
			op TEXT NOT NULL,
			bucket TEXT NOT NULL,
			decision TEXT NOT NULL,
			rationale TEXT NOT NULL DEFAULT '',
			item_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS proposals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TEXT NOT NULL,
			session_id TEXT NOT NULL DEFAULT '',
			op TEXT NOT NULL,
			bucket TEXT NOT NULL,
			rationale TEXT NOT NULL DEFAULT '',
			payload TEXT NOT NULL DEFAULT '',
			decided INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
}

const schemaVersion = "5"

func (b *bank) createSchema(ctx context.Context) error {
	for _, stmt := range b.schemaStmts() {
		if _, err := b.write.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec schema %q: %w", firstLine(stmt), err)
		}
	}
	// A bank created under the first schema has the ledger without its entry
	// binding, and its CREATE was already a no-op, so the column arrives by
	// ALTER. Upgrades may only ever add; anything more is a rebuild from the
	// markdown.
	if err := ensureLedgerEntryColumn(ctx, b); err != nil {
		return err
	}
	if err := ensureEntriesTitleColumn(ctx, b); err != nil {
		return err
	}
	if err := ensureEntriesMacColumn(ctx, b); err != nil {
		return err
	}
	if err := ensureEntriesSanitizedColumn(ctx, b); err != nil {
		return err
	}
	return b.stampSchemaVersion(ctx)
}

// ensureLedgerEntryColumn brings a pre-v2 ledger forward: the prune in Sync
// cannot reach the derived rows of a deleted file unless the row that tracks
// the file also names the entry it was indexed as.
func ensureLedgerEntryColumn(ctx context.Context, b *bank) error {
	var have int
	if err := b.read.QueryRowContext(ctx,
		"SELECT COUNT(1) FROM pragma_table_info('file_hashes') WHERE name = 'id'").Scan(&have); err != nil {
		return fmt.Errorf("probe ledger columns: %w", err)
	}
	if have > 0 {
		return nil
	}
	if _, err := b.write.ExecContext(ctx,
		"ALTER TABLE file_hashes ADD COLUMN id TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("add ledger entry column: %w", err)
	}
	return nil
}

// ensureEntriesTitleColumn upgrades a bank created before the human display
// label existed. Same add-only rule as the ledger column: an existing database
// gains the column, a fresh one already has it from the CREATE.
func ensureEntriesTitleColumn(ctx context.Context, b *bank) error {
	var have int
	if err := b.read.QueryRowContext(ctx,
		"SELECT COUNT(1) FROM pragma_table_info('entries') WHERE name = 'title'").Scan(&have); err != nil {
		return fmt.Errorf("probe entry title column: %w", err)
	}
	if have > 0 {
		return nil
	}
	if _, err := b.write.ExecContext(ctx,
		"ALTER TABLE entries ADD COLUMN title TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("add entry title column: %w", err)
	}
	return nil
}

// ensureEntriesMacColumn brings a bank created before the tamper seal forward:
// the derived digest column arrives by ALTER, under the same add-only rule the
// title and ledger columns follow.
func ensureEntriesMacColumn(ctx context.Context, b *bank) error {
	var have int
	if err := b.read.QueryRowContext(ctx,
		"SELECT COUNT(1) FROM pragma_table_info('entries') WHERE name = 'mac'").Scan(&have); err != nil {
		return fmt.Errorf("probe entry mac column: %w", err)
	}
	if have > 0 {
		return nil
	}
	if _, err := b.write.ExecContext(ctx,
		"ALTER TABLE entries ADD COLUMN mac TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("add entry mac column: %w", err)
	}
	return nil
}

// ensureEntriesSanitizedColumn carries the §9 audit stamp: the digest of the
// defanged projection of the entry text, recomputed at every index pass so
// /memory fsck can check the audit invariant by re-running the sanitizer rather
// than trusting that it ran. Like the lifecycle bookkeeping it is deliberately
// outside the tamper seal — it is derived from content the seal already covers.
func ensureEntriesSanitizedColumn(ctx context.Context, b *bank) error {
	var have int
	if err := b.read.QueryRowContext(ctx,
		"SELECT COUNT(1) FROM pragma_table_info(?) WHERE name = ?", "entries", "sanitized").Scan(&have); err != nil {
		return fmt.Errorf("probe entry sanitized column: %w", err)
	}
	if have > 0 {
		return nil
	}
	if _, err := b.write.ExecContext(ctx,
		"ALTER TABLE entries ADD COLUMN sanitized TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("add entry sanitized column: %w", err)
	}
	return nil
}

func (b *bank) stampSchemaVersion(ctx context.Context) error {
	_, err := b.write.ExecContext(ctx,
		"INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		"schema_version", schemaVersion)
	if err != nil {
		return fmt.Errorf("stamp schema version: %w", err)
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Close releases the database handles. A shared store only drops a reference:
// the last closer in the process is the one that actually closes the WAL
// handles, so per-call open/close in the tools never yanks connections out from
// under a still-running watcher, prompt build, or another session.
func (s *Store) Close() error {
	if s.cacheKey == "" {
		return s.closeBanks()
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if n := s.refCount.Add(-1); n > 0 {
		return nil
	}
	// Last user gone. Drop the map entry under closeMu, which is the same lock a
	// would-be adopter has to win before handing the store to a new caller, so
	// nobody can adopt a handle set that is about to close under it.
	sharedStores.Delete(s.cacheKey)
	return s.closeBanksLocked()
}

// closeBanks drops the handles of a store that lost the cache race and so was
// never published to anyone else.
func (s *Store) closeBanks() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	return s.closeBanksLocked()
}

func (s *Store) closeBanksLocked() error {
	if s.closed {
		return nil
	}
	s.closed = true
	var errs []string
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		if err := b.close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close memory store: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *Store) banks() []*bank {
	if s.project == nil {
		return []*bank{s.global}
	}
	return []*bank{s.project, s.global}
}

// VaultDir returns the markdown directory for a scope.
func (s *Store) VaultDir(scope Scope) string {
	if scope == ScopeProject && s.project != nil {
		return s.project.dir
	}
	return s.global.dir
}

// Settings exposes the resolved tuning for the budget report and the UI.
func (s *Store) Settings() Settings { return s.settings }

// DB exposes a vault handle for the few statements that do not warrant a store
// method (the lifecycle and gate code). It is the read pool; writes go through
// the store methods, tx, or execWrite.
func (s *Store) DB(scope Scope) *sql.DB { return s.bankFor(scope).read }

func (s *Store) bankFor(scope Scope) *bank {
	if scope == ScopeProject && s.project != nil {
		return s.project
	}
	return s.global
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// Put writes an entry to its vault and upserts the derived row. It does not run
// the gate; callers go through Gate.Add so sanitization and classification have
// already happened by the time bytes reach the disk.
func (s *Store) Put(ctx context.Context, e Entry) error {
	ctx, span := otel.StartSpan(ctx, "memory.put")
	defer span.End()
	span.SetAttributes(
		attribute.String("phosphor.memory.id", e.ID),
		attribute.String("phosphor.memory.scope", string(e.Scope)),
		attribute.String("phosphor.memory.type", string(e.Type)),
	)
	b := s.bankFor(e.Scope)
	e.Normalized(s.now())
	if e.Title == "" {
		e.Title = deriveTitle(e.Summary)
	}
	if e.ID == "" {
		return fmt.Errorf("entry needs an id")
	}
	if s.integrityActive() && len(s.integrityKey) == 0 {
		return fmt.Errorf("%w: cannot write a sealed memory entry without its signing key", ErrNoIntegrityKey)
	}
	// Stamp the defanged projection before the seal is computed, so the markdown
	// (the truth a rebuild reads from) carries the audit mark alongside the bytes
	// it was taken over. putRow recomputes it at index time either way; this is
	// what makes the file itself auditable without the database.
	e.Sanitized = sanitizedStamp(e.Body, e.Notes)
	// Seal the entry under the active key before the file is written, so the
	// markdown (the source of truth a rebuild reads from) carries the digest and the
	// re-index below verifies against it. With integrity off this leaves Mac empty and
	// the serialised bytes are exactly what they were before the seal existed.
	s.signFor(&e)
	path := EntryPath(b.dir, e.ID)
	e.Path = filepath.ToSlash(path)
	if err := WriteEntry(path, e); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read back entry: %w", err)
	}
	return s.indexEntry(ctx, b, path, HashBytes(raw), true)
}

// indexEntry upserts one row plus its tags, edges, and FTS copy from the file on
// disk, in a single transaction so the index is never observed half synced. The
// systemWrite flag carries who authored the file bytes the hash was taken of: a
// Put wrote them under system authority, a sync found them under a human's.
func (s *Store) indexEntry(ctx context.Context, b *bank, path string, hash string, systemWrite bool) error {
	entry, err := ParseEntry(path)
	if err != nil {
		var perr *ParseError
		if asParseError(err, &perr) {
			slog.Warn("Quarantining unreadable memory entry", "path", filepath.ToSlash(path), "error", perr.Err)
			return s.dropRow(ctx, b, "", path)
		}
		return err
	}
	entry.Scope = b.scope
	return s.putRow(ctx, b, entry, hash, systemWrite)
}

func (s *Store) putRow(ctx context.Context, b *bank, e Entry, hash string, systemWrite bool) error {
	asserted := 0
	if e.Asserted != nil && *e.Asserted {
		asserted = 1
	}
	pinned := 0
	if e.Pinned {
		pinned = 1
	}
	// A tampered seal is quarantine, not silent trust: a row whose digest no longer
	// recomputes is held out of every read path (which all filter on quarantined) and
	// counted, rather than admitted on the strength of a seal that failed.
	quarantined := 0
	if s.integrityActive() && !s.entryVerifies(e) {
		quarantined = 1
		slog.Warn("Quarantining a memory entry whose integrity seal failed to verify",
			"id", e.ID, "path", filepath.ToSlash(e.Path))
	}
	// The row's stamp is always freshly recomputed here rather than trusted from
	// the file, so it can never claim a sanitization pass over bytes it does not
	// hold: it is the audit view of "this row is the sanitized projection of the
	// file it was indexed from", which is exactly what Fsck checks.
	e.Sanitized = sanitizedStamp(e.Body, e.Notes)
	return s.tx(ctx, b, func(tx *sql.Tx) error {
		// Trust and status are system-owned once a row exists: trust belongs to the
		// confirmation and feedback loop, status to the lifecycle pass, so a hand
		// edit of either in the frontmatter is drift and is re-indexed away. A
		// first index has no row to defer to and adopts the file, which is what
		// lets checked-in fixtures and a migrated vault seed the corpus at all.
		if !systemWrite {
			var priorTrust float64
			var priorStatus, priorSource string
			err := tx.QueryRowContext(ctx, "SELECT trust, status, source FROM entries WHERE id = ?", e.ID).
				Scan(&priorTrust, &priorStatus, &priorSource)
			if err == nil {
				e.Trust = priorTrust
				if priorStatus != "" {
					e.Status = Status(priorStatus)
				}
				// source is system-owned like trust and status (§3): the tool
				// path authors it and a hand edit in the frontmatter is drift,
				// so once a row exists its value rides along and the file's
				// never becomes truth. With no row yet (a first index) the file
				// is the only truth there is, so its value is adopted — but only
				// past validation, since it is model- and human-writable text
				// that renders verbatim into the injected badge.
				e.Source = priorSource
			} else if e.Source != "" {
				src, verr := ValidateSource(e.Source)
				if verr != nil {
					slog.Warn("Dropping an invalid provenance string from a hand-authored entry file",
						"id", e.ID, "path", filepath.ToSlash(e.Path), "error", verr)
					e.Source = ""
				} else {
					e.Source = src
				}
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM entries_fts WHERE id = ?", e.ID); err != nil {
			return fmt.Errorf("clear fts row: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO entries(
					id, scope, type, summary, title, body, thread, status, trust, pinned,
					recall_count, helpful_count, created_ts, updated_ts, last_used,
					expires, source, supersedes, from_decision, owner, asserted, quarantined, mac, sanitized
				) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET
					type = excluded.type, summary = excluded.summary, title = excluded.title, body = excluded.body,
					thread = excluded.thread, status = excluded.status, trust = excluded.trust,
					pinned = excluded.pinned, updated_ts = excluded.updated_ts,
					last_used = excluded.last_used, expires = excluded.expires,
					source = excluded.source, supersedes = excluded.supersedes,
					from_decision = excluded.from_decision, owner = excluded.owner,
					asserted = excluded.asserted, quarantined = excluded.quarantined, mac = excluded.mac,
					sanitized = excluded.sanitized`,
			e.ID, string(e.Scope), string(e.Type), e.Summary, e.Title, e.Body, e.Thread, string(e.Status),
			e.Trust, pinned, e.RecallCount, e.HelpfulCount, e.Created, e.Updated, e.LastUsed,
			e.Expires, e.Source, e.Supersedes, e.FromDecision, string(e.Owner), asserted, quarantined, e.Mac, e.Sanitized,
		); err != nil {
			return fmt.Errorf("upsert entry row: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO entries_fts(body, id, thread, type) VALUES(?, ?, ?, ?)",
			e.Body, e.ID, e.Thread, string(e.Type)); err != nil {
			return fmt.Errorf("insert fts row: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM entry_tags WHERE entry_id = ?", e.ID); err != nil {
			return fmt.Errorf("clear tags: %w", err)
		}
		for _, tag := range e.Tags {
			if tag == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO tags(term, kind) VALUES(?, 'vocab') ON CONFLICT(term) DO NOTHING", tag); err != nil {
				return fmt.Errorf("upsert tag: %w", err)
			}
			var tagID int64
			if err := tx.QueryRowContext(ctx, "SELECT id FROM tags WHERE term = ?", tag).Scan(&tagID); err != nil {
				return fmt.Errorf("lookup tag: %w", err)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO entry_tags(entry_id, tag_id) VALUES(?, ?) ON CONFLICT(entry_id, tag_id) DO NOTHING", e.ID, tagID); err != nil {
				return fmt.Errorf("link tag: %w", err)
			}
		}
		if err := reindexEdges(ctx, tx, e); err != nil {
			return err
		}
		return upsertFileHash(ctx, tx, e.ID, e.Path, hash)
	})
}

// reindexEdges rebuilds the named relations for one entry: wikilinks,
// supersedes, and the decision link. Tag edges stay in entry_tags.
func reindexEdges(ctx context.Context, tx *sql.Tx, e Entry) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM edges WHERE src = ?", e.ID); err != nil {
		return fmt.Errorf("clear edges: %w", err)
	}
	for _, link := range e.Links {
		target := LinkTarget(link)
		if target == "" || target == e.ID {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO edges(src, dst, kind) VALUES(?, ?, 'wikilink') ON CONFLICT(src, dst, kind) DO NOTHING", e.ID, target); err != nil {
			return fmt.Errorf("link edge: %w", err)
		}
	}
	relations := [][2]string{{e.Supersedes, "supersedes"}, {e.FromDecision, "from_decision"}}
	for _, rel := range relations {
		if rel[0] == "" || rel[0] == e.ID {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO edges(src, dst, kind) VALUES(?, ?, ?) ON CONFLICT(src, dst, kind) DO NOTHING", e.ID, rel[0], rel[1]); err != nil {
			return fmt.Errorf("relation edge: %w", err)
		}
	}
	return nil
}

// LinkTarget strips the [[ ]] wikilink syntax Obsidian uses.
func LinkTarget(link string) string {
	t := strings.TrimSpace(link)
	t = strings.TrimPrefix(t, "[[")
	t = strings.TrimSuffix(t, "]]")
	if i := strings.IndexByte(t, '|'); i >= 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

func upsertFileHash(ctx context.Context, tx *sql.Tx, id, path, hash string) error {
	if path == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO file_hashes(path, content_hash, id) VALUES(?, ?, ?) ON CONFLICT(path) DO UPDATE SET content_hash = excluded.content_hash, id = excluded.id",
		path, hash, id); err != nil {
		return fmt.Errorf("record file hash: %w", err)
	}
	return nil
}

// Busy-retry policy. The DSN gives the writer a short in-sqlite wait so a
// contended lock surfaces as SQLITE_BUSY quickly; the retries here decide how
// long to keep waiting overall, with backoff and jitter so two instances doing
// the same rescan do not lockstep into each other, and with the caller's
// context still in charge so a cancelled turn stops waiting immediately.
// The retry policy. The DSN gives the writer a short in-sqlite wait so a
// contended lock surfaces as SQLITE_BUSY quickly, and the counts below decide
// how long to keep waiting overall: bounded backoff with jitter, so two
// instances doing the same rescan do not lockstep into each other, and with the
// caller's context in charge so a cancelled turn stops waiting at once.
const (
	defaultWriteRetries = 24
	retryBaseDelay      = 2 * time.Millisecond
	retryMaxDelay       = 250 * time.Millisecond
	sqliteBusy          = 5
	sqliteLocked        = 6
)

// isBusyErr reports whether err is sqlite saying the write lock is taken right
// now, which is the retryable class of lock failure. SQLITE_FULL is deliberately
// not in the list: no backoff recovers from that.
func isBusyErr(err error) bool {
	var serr *sqlitedrv.Error
	if !errors.As(err, &serr) {
		return false
	}
	switch serr.Code() {
	case sqliteBusy, sqliteLocked:
		return true
	}
	return false
}

func retryDelay(attempt int) time.Duration {
	d := retryBaseDelay << uint(attempt)
	if d > retryMaxDelay {
		d = retryMaxDelay
	}
	return d + time.Duration(rand.Int63n(int64(d/4)+1))
}

// BusyRetries reports how many times this vault's writer had to ride out a lock
// wait, which is the observable sign that a second instance is at work on the
// same files.
func (s *Store) BusyRetries(scope Scope) int64 { return s.bankFor(scope).busyRetries.Load() }

// execWrite runs a single write statement with the busy-retry policy. Every
// statement it wraps is idempotent (UPDATE ... SET col = col + ?, INSERT with
// ON CONFLICT DO NOTHING/DO UPDATE, DELETE by key), so replaying one after a
// busy failure lands the same state a single attempt would.
func (b *bank) execWrite(ctx context.Context, stmt string, args ...any) error {
	var err error
	for attempt := 0; attempt <= b.writeRetries; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(retryDelay(attempt - 1))
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("write %q: %w (after %d busy retries: %v)", firstLine(stmt), ctx.Err(), attempt, err)
			case <-timer.C:
			}
			timer.Stop()
		}
		if _, err = b.write.ExecContext(ctx, stmt, args...); err == nil {
			return nil
		}
		if !isBusyErr(err) {
			return err
		}
		b.busyRetries.Add(1)
		slog.Debug("Retrying memory write after a lock wait", "vault", filepath.ToSlash(b.dir), "stmt", firstLine(stmt), "attempt", attempt)
	}
	return fmt.Errorf("write %q: %w (gave up after %d busy retries)", firstLine(stmt), err, b.writeRetries)
}

// tx runs one immediate-mode transaction under the busy-retry policy: a failed
// attempt rolls back whole and the next attempt replays the whole body, so the
// body must stay idempotent. Retrying at the transaction boundary is also what
// keeps a half-applied row impossible under contention with another instance.
func (s *Store) tx(ctx context.Context, b *bank, fn func(*sql.Tx) error) error {
	var err error
	for attempt := 0; attempt <= b.writeRetries; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(retryDelay(attempt - 1))
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("memory transaction: %w (after %d busy retries: %v)", ctx.Err(), attempt, err)
			case <-timer.C:
			}
			timer.Stop()
		}
		var tx *sql.Tx
		if tx, err = b.write.BeginTx(ctx, nil); err != nil {
			if !isBusyErr(err) {
				return fmt.Errorf("begin transaction: %w", err)
			}
			b.busyRetries.Add(1)
			slog.Debug("Retrying memory transaction after a lock wait", "vault", filepath.ToSlash(b.dir), "attempt", attempt)
			continue
		}
		if err = fn(tx); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			_ = tx.Rollback()
			if isBusyErr(err) {
				b.busyRetries.Add(1)
				continue
			}
			return fmt.Errorf("commit transaction: %w", err)
		}
		return nil
	}
	return fmt.Errorf("memory transaction: %w (gave up after %d busy retries)", err, b.writeRetries)
}

func asParseError(err error, target **ParseError) bool {
	perr, ok := err.(*ParseError)
	if !ok {
		return false
	}
	*target = perr
	return true
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// SearchQuery is the retrieval request. Status defaults to active-only so a
// retired tombstone never surfaces unless somebody asks for history.
type SearchQuery struct {
	Query  string
	Thread string
	Types  []Type
	Tags   []string
	Status string
	// Scope restricts the search to one vault; empty searches both.
	Scope Scope
	Limit int
}

// Hit is a ranked result plus the explanation the provenance pill needs. Without
// Why, an injected memory is un-auditable, which is exactly how context bleed
// becomes invisible.
type Hit struct {
	Entry
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet"`
	Why     string  `json:"why"`
}

const maxSearchLimit = 50

type entryRow struct {
	ID           string  `db:"id"`
	Type         string  `db:"type"`
	Summary      string  `db:"summary"`
	Title        string  `db:"title"`
	Body         string  `db:"body"`
	Thread       string  `db:"thread"`
	Status       string  `db:"status"`
	Trust        float64 `db:"trust"`
	HotScore     float64 `db:"hot_score"`
	Pinned       int64   `db:"pinned"`
	RecallCount  int64   `db:"recall_count"`
	HelpfulCount int64   `db:"helpful_count"`
	Created      string  `db:"created_ts"`
	Updated      string  `db:"updated_ts"`
	LastUsed     string  `db:"last_used"`
	Expires      string  `db:"expires"`
	Source       string  `db:"source"`
	Supersedes   string  `db:"supersedes"`
	FromDecision string  `db:"from_decision"`
	Owner        string  `db:"owner"`
	Asserted     int64   `db:"asserted"`
	Quarantined  int64   `db:"quarantined"`
	Mac          string  `db:"mac"`
	Sanitized    string  `db:"sanitized"`

	// Snippet and Kind are not entry fields: they are the extra columns a search
	// (snippet) or a graph walk (kind) selects alongside the entry row. They live
	// here so a whole row scans in one pass and the extra value rides along with it.
	Snippet string `db:"snippet"`
	Kind    string `db:"kind"`
}

func (r entryRow) entry(scope Scope) Entry {
	e := Entry{
		ID:           r.ID,
		Type:         Type(r.Type),
		Summary:      r.Summary,
		Title:        r.Title,
		Body:         r.Body,
		Thread:       r.Thread,
		Status:       Status(r.Status),
		Trust:        r.Trust,
		HotScore:     r.HotScore,
		Pinned:       r.Pinned != 0,
		RecallCount:  int(r.RecallCount),
		HelpfulCount: int(r.HelpfulCount),
		Created:      r.Created,
		Updated:      r.Updated,
		LastUsed:     r.LastUsed,
		Expires:      r.Expires,
		Source:       r.Source,
		Supersedes:   r.Supersedes,
		FromDecision: r.FromDecision,
		Owner:        Owner(r.Owner),
		Quarantined:  r.Quarantined != 0,
		Scope:        scope,
		Mac:          r.Mac,
		Sanitized:    r.Sanitized,
	}
	if r.Asserted != 0 {
		yes := true
		e.Asserted = &yes
	}
	return e
}

// queryEntries runs a select over the entries table and scans the rows, then
// drops any row whose tamper seal no longer verifies. It is the single read gate
// every recall, injection, and display path funnels through, so one check here is
// what keeps a tampered row out of the prompt even if it was never re-indexed.
// With integrity off the gate admits every row, which is the pre-seal behaviour
// byte-for-byte.
func (s *Store) queryEntries(ctx context.Context, b *bank, selectList, where string, args ...any) ([]Entry, error) {
	list, err := s.queryEntriesRaw(ctx, b, selectList, where, args...)
	if err != nil {
		return nil, err
	}
	if !s.integrityActive() {
		return list, nil
	}
	out := list[:0]
	for _, e := range list {
		if !s.entryVerifies(e) {
			s.verifyDrops.Add(1)
			slog.Warn("Dropping a memory entry whose integrity seal failed to verify",
				"id", e.ID, "path", filepath.ToSlash(e.Path))
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// queryEntriesRaw is the ungated scanner. It backs queryEntries and the
// integrity maintenance paths, which must be able to see and re-seal a row whose
// digest currently fails (a stripped or tampered seal) rather than be blind to it.
func (s *Store) queryEntriesRaw(ctx context.Context, b *bank, selectList, where string, args ...any) ([]Entry, error) {
	rows, err := b.read.QueryContext(ctx, fmt.Sprintf("SELECT %s FROM entries WHERE %s", selectList, where), args...)
	if err != nil {
		return nil, fmt.Errorf("query entries: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		r, err := scanRow[entryRow](rows)
		if err != nil {
			return nil, fmt.Errorf("scan entry row: %w", err)
		}
		out = append(out, r.entry(b.scope))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entry rows: %w", err)
	}
	return out, nil
}

const entryColumns = "id, type, summary, title, body, thread, status, trust, hot_score, pinned, recall_count, helpful_count, created_ts, updated_ts, last_used, expires, source, supersedes, from_decision, owner, asserted, quarantined, mac, sanitized"

// Search runs BM25 over the FTS body index, filters on the metadata columns, and
// ranks by relevance then trust. Quarantined and pending rows are excluded, so a
// file that cannot be parsed and an inference that was never confirmed are both
// structurally unable to reach the prompt.
func (s *Store) Search(ctx context.Context, q SearchQuery) ([]Hit, error) {
	ctx, span := otel.StartSpan(ctx, "memory.search")
	defer span.End()
	span.SetAttributes(
		attribute.String("phosphor.memory.query", q.Query),
		attribute.String("phosphor.memory.thread", q.Thread),
		attribute.String("phosphor.memory.scope", string(q.Scope)),
	)
	if q.Limit <= 0 {
		q.Limit = 10
	}
	if q.Limit > maxSearchLimit {
		q.Limit = maxSearchLimit
	}
	status := strings.ToLower(strings.TrimSpace(q.Status))
	if status == "" {
		status = string(StatusActive)
	}

	var hits []Hit
	for _, b := range s.banks() {
		if b == nil || (q.Scope != "" && q.Scope != b.scope) {
			continue
		}
		bh, err := s.searchBank(ctx, b, q, status)
		if err != nil {
			otel.RecordError(span, err)
			return nil, err
		}
		hits = append(hits, bh...)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Trust != hits[j].Trust {
			return hits[i].Trust > hits[j].Trust
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}
	span.SetAttributes(attribute.Int("phosphor.memory.hits", len(hits)))
	return hits, nil
}

func (s *Store) searchBank(ctx context.Context, b *bank, q SearchQuery, status string) ([]Hit, error) {
	var (
		cond []string
		args []any
	)
	cond = append(cond, "e.quarantined = 0")
	if status != "all" {
		cond = append(cond, "e.status = ?")
		args = append(args, status)
	}
	if q.Query != "" {
		match := ftsQuery(q.Query)
		if match == "" {
			return nil, nil
		}
		cond = append(cond, "entries_fts MATCH ?")
		args = append(args, match)
	}
	if q.Thread != "" {
		cond = append(cond, "e.thread = ?")
		args = append(args, strings.TrimSpace(q.Thread))
	}
	if len(q.Types) > 0 {
		ph := make([]string, len(q.Types))
		for i, t := range q.Types {
			ph[i] = "?"
			args = append(args, string(t))
		}
		cond = append(cond, "e.type IN ("+strings.Join(ph, ",")+")")
	}
	if len(q.Tags) > 0 {
		ph := make([]string, len(q.Tags))
		for i, t := range q.Tags {
			ph[i] = "?"
			args = append(args, CanonicalTag(t))
		}
		// Lateral recall: entries carrying these tags, deliberately NOT scoped by
		// thread, so "what else ever touched FTS5" stays answerable.
		cond = append(cond, "EXISTS (SELECT 1 FROM entry_tags et JOIN tags t ON t.id = et.tag_id WHERE et.entry_id = e.id AND t.term IN ("+strings.Join(ph, ",")+"))")
	}
	return s.searchRows(ctx, b, cond, args, q)
}
