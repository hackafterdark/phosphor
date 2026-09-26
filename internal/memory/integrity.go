package memory

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hackafterdark/phosphor/pkg/otel"
)

// Integrity gives a vault tamper-evidence: every entry carries a keyed digest
// (a MAC) over its machine-authored content, and the index refuses to inject or
// recall a row whose digest no longer verifies. It is integrity, not
// confidentiality: the plaintext stays a human-readable Obsidian note, only its
// authenticity is sealed.
//
// The signing key lives at the global vault root under 0600, one key for both
// banks so a global fact and a project fact carry digests from the same anchor.
// It is exportable as a BIP39 phrase so the person who owns the vault can back
// the key up on paper and restore it on a new machine.

const (
	// integrityKeyFileName is the secret at the global vault root.
	integrityKeyFileName = "integrity.key"
	// integrityKeyBytes is the HMAC-SHA256 key width. 32 bytes is also the
	// largest entropy size BIP39 accepts, so the key exports to a phrase with no
	// re-framing.
	integrityKeyBytes = 32
	// integrityBootstrapTimeout bounds the one-time seal pass over a legacy vault.
	integrityBootstrapTimeout = 20 * time.Second
	// integritySealKey is the per-vault meta flag that records the corpus has been
	// sealed once, so a later open does not re-run the pass and quietly bless a
	// digest someone stripped off a file.
	integritySealKey = "integrity_sealed"
	// macDomain tags the canonical form so a digest over these fields can never
	// be replayed against some other structure that happens to serialise the
	// same bytes.
	macDomain = "phosphor-memory-mac/v1\n"
)

// ErrNoIntegrityKey reports that integrity is on but the signing key is
// missing. Callers surface it rather than fall back to unsigned writes, because
// writing an entry with no digest is exactly the hole the feature seals.
var ErrNoIntegrityKey = errors.New("memory integrity key is missing")

// ---------------------------------------------------------------------------
// Key management
// ---------------------------------------------------------------------------

// integrityKeyPathFor is where a vault's signing key lives: at its root, beside
// entries/ and .phosphor-index/, so the vault-protection guard that already
// covers that directory covers the key too.
func integrityKeyPathFor(vaultDir string) string {
	return filepath.Join(vaultDir, integrityKeyFileName)
}

// loadKeyFor reads a vault's signing key, or reports ErrNoIntegrityKey when the
// file has not been created yet. The key is stored as hex rather than as raw
// bytes so it survives being handled as text: a 32-byte key whose leading or
// trailing byte happens to be a whitespace byte would otherwise be trimmed away
// on read and the vault would fail closed against a key that was never touched.
func loadKeyFor(vaultDir string) ([]byte, error) {
	raw, err := os.ReadFile(integrityKeyPathFor(vaultDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoIntegrityKey
		}
		return nil, fmt.Errorf("read integrity key: %w", err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("decode integrity key: %w", err)
	}
	if len(key) != integrityKeyBytes {
		return nil, fmt.Errorf("integrity key is %d bytes, want %d", len(key), integrityKeyBytes)
	}
	return key, nil
}

// writeKeyFor stores a signing key under 0600 as hex. The key is the whole
// security property of the vault, so it is written owner-only and never through
// the shared staging path the entries use; the hex encoding keeps it a plain text
// file that no editor or line-ending conversion can silently corrupt.
func writeKeyFor(vaultDir string, key []byte) error {
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		return fmt.Errorf("create vault root: %w", err)
	}
	if err := os.WriteFile(integrityKeyPathFor(vaultDir), []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return fmt.Errorf("write integrity key: %w", err)
	}
	return nil
}

// loadOrCreateKeyFor is the open-time entry point: adopt the vault's existing key
// or, on first enablement, mint one. It reports whether it minted, because the
// mint is the moment the operator has to be told about key custody — the seal's
// whole security property rests on that file, and losing it locks the corpus out
// of recall until a backup phrase restores it.
func loadOrCreateKeyFor(vaultDir string) ([]byte, bool, error) {
	key, err := loadKeyFor(vaultDir)
	if err == nil {
		return key, false, nil
	}
	if !errors.Is(err, ErrNoIntegrityKey) {
		return nil, false, err
	}
	key = make([]byte, integrityKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, false, fmt.Errorf("generate integrity key: %w", err)
	}
	if err := writeKeyFor(vaultDir, key); err != nil {
		return nil, false, err
	}
	return key, true, nil
}

// IntegrityKeyPath is where the default global vault keeps its signing key.
func IntegrityKeyPath() string {
	return integrityKeyPathFor(GlobalVaultDir())
}

// IntegrityKeyMnemonic exports the default global vault's signing key as a BIP39
// phrase so the owner can back it up offline. An empty passphrase is used,
// matching how the key is stored: the key itself is the secret, the phrase is only
// its text form.
func IntegrityKeyMnemonic() (string, error) {
	key, err := loadKeyFor(GlobalVaultDir())
	if err != nil {
		return "", err
	}
	return bip39Encode(key)
}

// RestoreIntegrityKeyFromMnemonic writes the default global vault's signing key
// from a BIP39 phrase, replacing whatever key was there. This is how a restored
// backup becomes the authority again: every entry signed under the original key
// verifies again.
func RestoreIntegrityKeyFromMnemonic(phrase string) ([]byte, error) {
	key, err := bip39Decode(phrase)
	if err != nil {
		return nil, err
	}
	if len(key) != integrityKeyBytes {
		return nil, fmt.Errorf("restored key is %d bytes, want %d", len(key), integrityKeyBytes)
	}
	if err := writeKeyFor(GlobalVaultDir(), key); err != nil {
		return nil, err
	}
	return key, nil
}

// ---------------------------------------------------------------------------
// Canonical form
// ---------------------------------------------------------------------------

// canonicalField writes one length-prefixed field into the digest input. The
// length prefix is what makes the form injective: a body that happens to contain
// "\nsummary=..." cannot be forged into a second field, because the reader trusts
// the byte count, not the separator.
func canonicalField(sb *strings.Builder, key, value string) {
	fmt.Fprintf(sb, "%s[%d]:", key, len(value))
	sb.WriteString(value)
	sb.WriteByte('\n')
}

// macCanonical renders the signed projection of an entry: the machine-authored
// content, the identity it is keyed by, and the classification that steers recall.
//
// It deliberately EXCLUDES the fields the system mutates in place without a
// re-author (trust, status, hot_score, the recall/helpful counters, the
// timestamps, and the phosphor.sanitized audit stamp) and the regions a human
// owns in Obsidian: the title label, the Notes block, and the tags and links
// frontmatter lists.
//
// tags and links are exempt by a recorded decision, not by oversight. tags maps
// to Obsidian's native tag property and is the single most hand-edited field in
// the vault (the properties pane and inline #tag both write it); links is
// free-form YAML a person may prune or repair. Neither is a column in the entries
// row, so the machine cannot restate them on a re-seal and the agent does not own
// them the way it owns the body. Signing them would quarantine a note the moment a
// person touched a tag in Obsidian, and a rotate could not re-derive them from the
// index, so the seal would fail closed against legitimate human editing. The recall
// surfaces they steer (the tag EXISTS path and the wikilink graph) are therefore
// trusted at the same level as the human Notes region they sit beside.
func macCanonical(e Entry) []byte {
	var sb strings.Builder
	sb.WriteString(macDomain)
	canonicalField(&sb, "id", e.ID)
	canonicalField(&sb, "type", string(NormalType(string(e.Type))))
	canonicalField(&sb, "thread", e.Thread)
	canonicalField(&sb, "summary", e.Summary)
	canonicalField(&sb, "body", strings.TrimSpace(e.Body))
	canonicalField(&sb, "source", e.Source)
	canonicalField(&sb, "expires", e.Expires)
	canonicalField(&sb, "supersedes", e.Supersedes)
	canonicalField(&sb, "from_decision", e.FromDecision)
	canonicalField(&sb, "owner", string(e.Owner))
	canonicalField(&sb, "created", e.Created)
	asserted := "0"
	if e.Asserted != nil && *e.Asserted {
		asserted = "1"
	}
	canonicalField(&sb, "asserted", asserted)
	return []byte(sb.String())
}

func hmacTag(key []byte, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// signEntry returns the hex HMAC-SHA256 of the entry's canonical form.
func signEntry(key []byte, e Entry) string {
	return hex.EncodeToString(hmacTag(key, macCanonical(e)))
}

// verifyEntry reports whether an entry carries a digest that recomputes to the
// one it was stored with. hmac.Equal is constant-time, so a failed check gives
// away no timing on how far into the digest the first difference is.
func verifyEntry(key []byte, e Entry) bool {
	if e.Mac == "" {
		return false
	}
	want, err := hex.DecodeString(e.Mac)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	return hmac.Equal(want, hmacTag(key, macCanonical(e)))
}

// ---------------------------------------------------------------------------
// Store wiring
// ---------------------------------------------------------------------------

// signFor stamps a fresh digest on an entry under the active key. It is a no-op
// when integrity is off, which is what keeps an unsigned vault's serialised bytes
// byte-for-byte what they were before the feature existed.
func (s *Store) signFor(e *Entry) {
	if !s.integrityActive() || len(s.integrityKey) == 0 {
		e.Mac = ""
		return
	}
	e.Mac = signEntry(s.integrityKey, *e)
}

// entryVerifies checks one row against the key. An unsigned vault admits every
// row; an integrity-enabled vault admits only rows whose digest recomputes.
func (s *Store) entryVerifies(e Entry) bool {
	if !s.integrityActive() {
		return true
	}
	if len(s.integrityKey) == 0 {
		return false
	}
	return verifyEntry(s.integrityKey, e)
}

// initIntegrity resolves the signing key at open time when the feature is on. A
// failure here is fatal to opening the store: integrity must fail loudly rather
// than leave an enabled vault whose writes look successful but cannot be verified.
func (s *Store) initIntegrity(ctx context.Context) error {
	if !s.settings.IntegrityEnabled() {
		s.integrityKey = nil
		return nil
	}
	key, minted, err := s.loadOrCreateIntegrityKey(ctx)
	if err != nil {
		slog.Warn("Memory integrity is enabled but the signing key is unavailable; the memory store will not open",
			"error", err)
		s.integrityKey = nil
		return err
	}
	s.integrityKey = key
	if minted {
		s.keyJustMinted.Store(true)
		slog.Info("Created the memory tamper-seal signing key for this vault",
			"path", filepath.ToSlash(integrityKeyPathFor(s.vaultKeyDir())),
			"recovery", "run /memory key show and store the phrase offline; losing the key locks the corpus out of recall")
	}
	return nil
}

// ReloadIntegrityKey re-reads the vault's signing key after an operator restores
// it, so the live handle can adopt the phrase without waiting for a process
// restart.
func (s *Store) ReloadIntegrityKey(ctx context.Context) error {
	return s.initIntegrity(ctx)
}

func (s *Store) loadOrCreateIntegrityKey(ctx context.Context) ([]byte, bool, error) {
	dir := s.vaultKeyDir()
	key, err := loadKeyFor(dir)
	if err == nil {
		return key, false, nil
	}
	if !errors.Is(err, ErrNoIntegrityKey) {
		return nil, false, err
	}

	sealed, sealedErr := s.corpusHasSealedEntries(ctx)
	if sealedErr != nil {
		return nil, false, fmt.Errorf("inspect sealed memory entries: %w", sealedErr)
	}
	if sealed {
		return nil, false, fmt.Errorf("%w: existing sealed entries require their original signing key; restore it with /memory key restore or rotate it with /memory key rotate after backing it up", ErrNoIntegrityKey)
	}

	key = make([]byte, integrityKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, false, fmt.Errorf("generate integrity key: %w", err)
	}
	if err := writeKeyFor(dir, key); err != nil {
		return nil, false, err
	}
	return key, true, nil
}

func (s *Store) corpusHasSealedEntries(ctx context.Context) (bool, error) {
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		var sealed int
		if err := b.read.QueryRowContext(ctx, "SELECT COUNT(1) FROM entries WHERE mac <> ''").Scan(&sealed); err != nil {
			return false, err
		}
		if sealed > 0 {
			return true, nil
		}
	}
	return false, nil
}

// vaultKeyDir is the root the store keeps its signing key under: the global
// vault's own directory, so an isolated store (a test, an override) carries its
// key with it rather than sharing the real user's.
func (s *Store) vaultKeyDir() string {
	if s.global != nil {
		return s.global.dir
	}
	return GlobalVaultDir()
}

// integrityActive reports whether the tamper seal is on for this store.
func (s *Store) integrityActive() bool { return s.settings.IntegrityEnabled() }

// BootstrapIntegrity seals every entry that currently carries no digest. It is
// the operator-forced form of the same pass the store runs once by itself at
// enablement; running it again is harmless because a row already sealed is left
// alone.
func (s *Store) BootstrapIntegrity(ctx context.Context) (int, error) {
	if !s.integrityActive() {
		return 0, nil
	}
	if len(s.integrityKey) == 0 {
		return 0, ErrNoIntegrityKey
	}
	if err := s.Sync(ctx); err != nil {
		return 0, err
	}
	total := 0
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		n, err := s.sealBankUnsigned(ctx, b)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// bootstrapOnce runs the blessing pass for every bank that has not had one yet.
// It syncs first, which is what makes the pass survive the cases it exists for:
// the derived index is disposable, so a bank opened over files with no database
// (a rebuild, a restored vault folder) has no rows to bless until a sync writes
// them, and rows a sync writes without a digest are quarantined by the tamper
// posture. Blessing therefore signs those rows and lifts the quarantine from
// exactly them; without it, a rebuilt or restored corpus would open straight into
// silent amnesia. The guard is per bank and rides the global index so enabling
// the feature over an existing vault seals it once, and a later open does not
// re-run the pass: a stripped digest must stay caught, not get silently
// re-blessed.
func (s *Store) bootstrapOnce(ctx context.Context) (int, error) {
	ctx, span := otel.StartSpan(ctx, "memory.integrity.bootstrap")
	defer span.End()
	if len(s.integrityKey) == 0 {
		return 0, ErrNoIntegrityKey
	}
	if err := s.Sync(ctx); err != nil {
		otel.RecordError(span, err)
		return 0, err
	}
	total := 0
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		sealed, err := s.bankSealed(ctx, b)
		if err != nil {
			otel.RecordError(span, err)
			return total, err
		}
		if sealed {
			continue
		}
		n, err := s.sealBankUnsigned(ctx, b)
		if err != nil {
			otel.RecordError(span, err)
			return total, err
		}
		total += n
		if err := s.markBankSealed(ctx, b); err != nil {
			otel.RecordError(span, err)
			return total, err
		}
	}
	return total, nil
}

// sealBankUnsigned signs the bank's entries that carry no digest yet under the
// active key, writing both the derived row and the backing markdown so a rebuild
// from the vault reproduces the same seals. Rows an index pass wrote without a
// digest were quarantined for exactly that, so the blessing pass clears the
// quarantine from the rows it blesses; a row that carried a digest which merely
// stopped matching — the tampering the seal exists to catch — is not selected
// here and stays held out of recall.
func (s *Store) sealBankUnsigned(ctx context.Context, b *bank) (int, error) {
	list, err := s.queryEntriesRaw(ctx, b, entryColumns, "mac = ''")
	if err != nil {
		return 0, err
	}
	signed := 0
	for _, e := range list {
		if err := s.resign(ctx, b, e.ID, s.integrityKey); err != nil {
			return signed, err
		}
		if err := b.execWrite(ctx, "UPDATE entries SET quarantined = 0 WHERE id = ?", e.ID); err != nil {
			return signed, fmt.Errorf("lift quarantine on blessed entry: %w", err)
		}
		signed++
	}
	if signed > 0 {
		slog.Info("Sealed unsigned memory entries under the integrity key",
			"count", signed, "vault", filepath.ToSlash(b.dir))
	}
	return signed, nil
}

// RotateIntegrityKey replaces the signing key with a fresh one and re-signs the
// whole corpus under it, so a rotation is one atomic action rather than a window
// where every entry fails its digest. Returns the number re-signed.
func (s *Store) RotateIntegrityKey(ctx context.Context) (int, error) {
	_, span := otel.StartSpan(ctx, "memory.integrity.rotate")
	defer span.End()
	if !s.integrityActive() {
		return 0, errors.New("memory integrity is off; there is no key to rotate")
	}
	key := make([]byte, integrityKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return 0, fmt.Errorf("generate integrity key: %w", err)
	}
	if err := writeKeyFor(s.vaultKeyDir(), key); err != nil {
		return 0, err
	}
	s.integrityKey = key
	resigned := 0
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		list, err := s.queryEntriesRaw(ctx, b, entryColumns, "1=1")
		if err != nil {
			return resigned, err
		}
		for _, e := range list {
			if err := s.resign(ctx, b, e.ID, key); err != nil {
				return resigned, err
			}
			resigned++
		}
	}
	slog.Info("Rotated the memory integrity key and re-signed the corpus", "count", resigned)
	return resigned, nil
}

// resign loads one entry by id under the raw (ungated) read path, re-seals it
// under key, and writes both the backing file and the derived row so the markdown
// a rebuild reads from carries the same seal the index does.
func (s *Store) resign(ctx context.Context, b *bank, id string, key []byte) error {
	list, err := s.queryEntriesRaw(ctx, b, entryColumns, "id = ?", id)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	e := list[0]
	path := EntryPath(b.dir, e.ID)

	// The entries row has no column for the tags, the links, or the human Notes
	// region: those live only in the markdown and in their side tables. A re-seal is a
	// pure re-stamp, so the row would otherwise be materialized straight back over the
	// file and silently drop all three. Read the file back when it is present and carry
	// those fields across, so a rotate or an enablement-time bootstrap re-seals the note
	// without erasing the parts of it the index cannot speak for. They are outside the
	// signed projection (see macCanonical), so adopting them here cannot skew the digest
	// written below, and the recall gate still verifies against the row it scans.
	if raw, rerr := os.ReadFile(path); rerr == nil {
		if parsed, perr := ParseEntryBytes(path, raw); perr == nil {
			e.Tags, e.Links, e.Notes = parsed.Tags, parsed.Links, parsed.Notes
		}
	}

	// The adopted Notes may have drifted since the row was last stamped, and the
	// stamp rides into the file written below, so it is recomputed over exactly
	// the bytes this pass emits rather than carried over from the row.
	e.Sanitized = sanitizedStamp(e.Body, e.Notes)

	e.Mac = signEntry(key, e)
	// Stamp the row directly, then the file, so a crash between the two leaves the
	// markdown authoritative and the next sync re-derives the row from it.
	if err := b.execWrite(ctx, "UPDATE entries SET mac = ?, sanitized = ? WHERE id = ?", e.Mac, e.Sanitized, e.ID); err != nil {
		return fmt.Errorf("stamp entry mac: %w", err)
	}
	return WriteEntry(path, e)
}

// vaultSealed reports whether this store's corpus has already been sealed once.
// The flag rides the global bank's meta table, which always exists and is the
// one place both banks agree, so one marker governs the whole store.
func (s *Store) vaultSealed(ctx context.Context) (bool, error) {
	b := s.bankFor(ScopeGlobal)
	var value string
	err := b.read.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", integritySealKey).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read integrity seal flag: %w", err)
	}
	return value == "1", nil
}

// markVaultSealed records the legacy store-wide seal flag. The per-bank flags
// superseded it; the old row is still read (see bankSealed) so a corpus sealed
// under the single-flag form never becomes blessable again.
func (s *Store) markVaultSealed(ctx context.Context) error {
	b := s.bankFor(ScopeGlobal)
	_, err := b.write.ExecContext(ctx,
		"INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		integritySealKey, "1")
	if err != nil {
		return fmt.Errorf("stamp integrity seal flag: %w", err)
	}
	return nil
}

// sealBankKey derives the per-bank blessing flag's meta key from the bank's own
// directory. Every flag rides the global bank's meta table on purpose: it is the
// one index a tamperer who can only write the workspace cannot reach, so resetting
// the guard that keeps a stripped digest from being re-blessed is not something
// workspace-level write access buys.
func sealBankKey(b *bank) string {
	return fmt.Sprintf("%s:%s", integritySealKey, HashString(filepath.ToSlash(b.dir)))
}

// bankSealed reports whether this bank's corpus has had its one blessing pass.
// The legacy single flag predates the per-bank form and is honored as having
// sealed everything: a corpus sealed once under it must not become blessable now.
func (s *Store) bankSealed(ctx context.Context, b *bank) (bool, error) {
	g := s.bankFor(ScopeGlobal)
	var value string
	err := g.read.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", sealBankKey(b)).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return s.vaultSealed(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("read integrity seal flag: %w", err)
	}
	return value == "1", nil
}

// markBankSealed records that this bank has had its blessing pass, so later opens
// skip it rather than re-bless a digest that was stripped off a file.
func (s *Store) markBankSealed(ctx context.Context, b *bank) error {
	g := s.bankFor(ScopeGlobal)
	_, err := g.write.ExecContext(ctx,
		"INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		sealBankKey(b), "1")
	if err != nil {
		return fmt.Errorf("stamp integrity seal flag: %w", err)
	}
	return nil
}

// IntegrityStatus is what the operator-facing integrity views report: the state
// of the key with a short public fingerprint for identity, and the corpus tallies
// that tell an operator whether the fail-closed door is standing open on
// tampering, on a lost key, or on nothing at all.
type IntegrityStatus struct {
	Enabled        bool   `json:"enabled"`
	KeyPresent     bool   `json:"key_present"`
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
	KeyJustMinted  bool   `json:"key_just_minted,omitempty"`
	Total          int64  `json:"total"`
	Signed         int64  `json:"signed"`
	Unsigned       int64  `json:"unsigned"`
	Quarantined    int64  `json:"quarantined"`
	// DroppedVerify is how many rows this process has held out of recall at read
	// time because their seal failed to recompute. It is the silent-amnesia
	// counter: tampering or a changed key stops being invisible.
	DroppedVerify int `json:"dropped_verify"`
}

// ReportIntegrity tallies the seal across both banks.
func (s *Store) ReportIntegrity(ctx context.Context) (IntegrityStatus, error) {
	st := IntegrityStatus{
		Enabled:        s.integrityActive(),
		KeyPresent:     len(s.integrityKey) > 0,
		KeyFingerprint: s.keyFingerprint(),
		KeyJustMinted:  s.keyJustMinted.Load() && len(s.integrityKey) > 0,
		DroppedVerify:  int(s.verifyDrops.Load()),
	}
	for _, b := range s.banks() {
		if b == nil {
			continue
		}
		var total, signed, quarantined int64
		err := b.read.QueryRowContext(ctx, `SELECT COUNT(1),
			COALESCE(SUM(CASE WHEN mac <> '' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(quarantined), 0) FROM entries`).Scan(&total, &signed, &quarantined)
		if err != nil {
			return st, fmt.Errorf("tally integrity: %w", err)
		}
		st.Total += total
		st.Signed += signed
		st.Unsigned += total - signed
		st.Quarantined += quarantined
	}
	return st, nil
}

// keyFingerprint is a short, deliberately non-secret identity for the signing
// key: enough for an operator to confirm that a restored key is the one the
// corpus was sealed under, without widening the exposure of the key bytes.
func (s *Store) keyFingerprint() string {
	if len(s.integrityKey) == 0 {
		return ""
	}
	digest := sha256.New()
	digest.Write(s.integrityKey)
	return hex.EncodeToString(digest.Sum(nil))[:12]
}

// ---------------------------------------------------------------------------
// BIP39
// ---------------------------------------------------------------------------

//go:embed bip39_english.txt
var bip39Wordlist string

var bip39Words = func() []string {
	return strings.Fields(strings.TrimSpace(bip39Wordlist))
}()

const (
	bip39BitsPerWord = 11
	bip39WordMask    = (1 << bip39BitsPerWord) - 1
)

// bip39Encode turns entropy (128..256 bits, a multiple of 32) into a BIP39
// mnemonic. The trailing checksum word is the leading bits of SHA256(entropy) —
// entropy alone, with no passphrase — which is what lets a decode catch a mistyped
// word without any knowledge of the original secret. BIP39 keeps the passphrase out
// of the mnemonic entirely (it stretches the phrase into a seed, not the phrase
// itself), so a backed-up phrase is a pure function of the key bytes.
func bip39Encode(entropy []byte) (string, error) {
	entropyBits := len(entropy) * 8
	if entropyBits < 128 || entropyBits > 256 || entropyBits%32 != 0 {
		return "", fmt.Errorf("invalid entropy length %d bits", entropyBits)
	}
	checksumBits := entropyBits / 32
	sum := sha256.Sum256(entropy)
	sumInt := new(big.Int).SetBytes(sum[:])
	checksum := new(big.Int).Rsh(sumInt, uint(256-checksumBits))

	entInt := new(big.Int).SetBytes(entropy)
	data := new(big.Int).Lsh(entInt, uint(checksumBits))
	data = data.Or(data, checksum)

	total := entropyBits + checksumBits
	out := make([]string, total/bip39BitsPerWord)
	for i := range out {
		chunk := new(big.Int).Rsh(data, uint(total-bip39BitsPerWord*(i+1)))
		out[i] = bip39Words[chunk.Int64()&bip39WordMask]
	}
	return strings.Join(out, " "), nil
}

// bip39Decode recovers the entropy a mnemonic encodes, rejecting a phrase whose
// checksum word does not match the rest (a single mistyped word fails here) or a
// phrase that is not a valid BIP39 length. The checksum is recomputed from the entropy
// alone, mirroring the encoder.
func bip39Decode(phrase string) ([]byte, error) {
	words := strings.Fields(strings.TrimSpace(phrase))
	if len(words) == 0 {
		return nil, errors.New("empty mnemonic")
	}
	index := make(map[string]int, len(bip39Words))
	for i, w := range bip39Words {
		index[w] = i
	}
	total := len(words) * bip39BitsPerWord
	data := new(big.Int)
	for _, w := range words {
		idx, ok := index[w]
		if !ok {
			return nil, fmt.Errorf("word %q is not in the wordlist", w)
		}
		data.Lsh(data, uint(bip39BitsPerWord))
		data.Or(data, big.NewInt(int64(idx)))
	}
	entropyBits := total * 32 / 33
	if entropyBits < 128 || entropyBits > 256 || entropyBits%32 != 0 {
		return nil, fmt.Errorf("invalid mnemonic length %d words", len(words))
	}
	checksumBits := entropyBits / 32

	entropy := new(big.Int).Rsh(data, uint(checksumBits))
	mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(checksumBits)), big.NewInt(1))
	gotSum := new(big.Int).And(data, mask)

	entropyBytes := leftPad(entropy.Bytes(), entropyBits/8)
	want := sha256.Sum256(entropyBytes)
	wantInt := new(big.Int).SetBytes(want[:])
	wantSum := new(big.Int).Rsh(wantInt, uint(256-checksumBits))
	if gotSum.Cmp(wantSum) != 0 {
		return nil, errors.New("mnemonic checksum mismatch")
	}
	return entropyBytes, nil
}

// leftPad widens a big.Int's minimal big-endian bytes to a fixed width, which is
// what the fixed-width entropy size demands even when a leading byte is zero.
func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		return b[len(b)-n:]
	}
	pad := make([]byte, n-len(b))
	return append(pad, b...)
}
