package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// migration is a versioned batch of DDL statements applied atomically.
// Registering a new version means appending to the ordered registry with a
// version strictly greater than every previous entry; statements within a
// version execute in slice order.
type migration struct {
	version    int
	statements []string
}

// baselineMigrations returns the registry the store applies on Open. Version 1
// is the union of the historical schema slice and the dialect-specific FTS
// statements; later versions are appended by their owners (RAG, admissions,
// etc.) via runMigrations.
//
// Version 2 adds the RAG chunk lexical index (SQLite FTS5 virtual table +
// triggers with a backfill; Postgres GIN index on to_tsvector('simple',
// content)) and the rag_collection_revisions counter that the vector-index
// cache polls to detect cross-writer mutations.
//
// Version 3 adds the devices table (paired clients holding a device token;
// see docs/plans/2026-10-02-desktop-contract.md). Identical on both dialects.
func baselineMigrations(dialect string) []migration {
	return []migration{
		{version: 1, statements: baselineV1(dialect)},
		{version: 2, statements: baselineV2(dialect)},
		{version: 3, statements: append([]string(nil), devicesV3...)},
	}
}

// baselineV1 assembles the frozen v1 statement list. The Postgres form maps
// the historical `BLOB` column on rag_chunks to `BYTEA`; every other statement
// is byte-identical across dialects.
func baselineV1(dialect string) []string {
	out := make([]string, 0, len(migrations)+len(sqliteFTS))
	for _, q := range migrations {
		if dialect == "postgres" && isRagChunksCreate(q) {
			q = strings.Replace(q, "embedding   BLOB,", "embedding   BYTEA,", 1)
		}
		out = append(out, q)
	}
	if dialect == "sqlite" {
		out = append(out, sqliteFTS...)
	} else {
		out = append(out, postgresFTS...)
	}
	return out
}

func isRagChunksCreate(q string) bool {
	return strings.Contains(q, "CREATE TABLE IF NOT EXISTS rag_chunks")
}

// baselineV2 assembles the v2 statement list. Once shipped the slice is frozen
// by ledger checksum; further schema changes ship as v3.
func baselineV2(dialect string) []string {
	if dialect == "sqlite" {
		return append([]string(nil), sqliteV2...)
	}
	return append([]string(nil), postgresV2...)
}

// migrate applies the baseline registry. It is the sqlStore hook Open calls
// after connecting; keep the signature stable so callers do not shift.
func (s *sqlStore) migrate(ctx context.Context) error {
	return s.runMigrations(ctx, baselineMigrations(s.dialect))
}

// runMigrations upgrades the database to match registry. It is idempotent,
// concurrency-safe, and refuses to run against a database whose ledger
// records a checksum mismatch, an unknown newer version, or a gap in the
// applied history.
//
// All ledger reads, ledger writes, and pending DDL for the run execute
// inside ONE transaction that also holds a writer lock:
//   - Postgres: pg_advisory_xact_lock at the top of the tx, released on
//     commit or rollback.
//   - SQLite: an empty UPDATE against schema_migrations promotes the tx to
//     a RESERVED lock so a second connection racing the same startup blocks
//     on busy_timeout until we commit or roll back, instead of racing the
//     ledger read and re-applying pending versions.
//
// A failure anywhere in the pending batch rolls the whole batch back —
// including previously committed statements of the same run and any ledger
// rows written for earlier versions in this run.
//
// The registry MUST be ordered by ascending version with no gaps interior to
// the sequence starting at 1; a caller adding v3 simply appends
// {version: 3, statements: […]} to the slice returned by baselineMigrations.
func (s *sqlStore) runMigrations(ctx context.Context, registry []migration) error {
	if err := validateRegistry(registry); err != nil {
		return err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire conn: %w", err)
	}
	defer conn.Close()

	// On SQLite the ledger table has to exist before the tx can UPDATE it
	// to take the writer lock. CREATE TABLE IF NOT EXISTS serializes
	// through the database file lock, so two concurrent openers racing the
	// same CREATE is safe (second one is a no-op). Postgres creates its
	// ledger inside the migration tx, after the advisory lock.
	if err := ensureLedgerSQLite(ctx, conn, s.dialect); err != nil {
		return err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Take the writer lock as the first tx op, before any read. Postgres
	// uses a tx-scoped advisory lock (auto-released on commit/rollback);
	// SQLite promotes the tx to RESERVED via an empty UPDATE against the
	// ledger, which is enough to make a second connection's identical
	// startup wait on busy_timeout instead of racing the check-and-apply.
	if err := acquireWriterLock(ctx, tx, s.dialect); err != nil {
		return err
	}

	applied, err := readLedgerTx(ctx, tx)
	if err != nil {
		return err
	}

	// Reject a database that reports a version we do not know about; that
	// almost certainly means a newer binary ran against this DB and rolling
	// backwards will corrupt data.
	maxKnown := registry[len(registry)-1].version
	maxApplied := 0
	for v := range applied {
		if v > maxKnown {
			return fmt.Errorf("migrate: database has unknown migration version %d (this build only knows up to %d); refusing to downgrade schema", v, maxKnown)
		}
		if v > maxApplied {
			maxApplied = v
		}
	}
	// The registry itself is contiguous (validateRegistry above); the
	// applied set must also be contiguous [1..maxApplied]. A missing
	// interior version means somebody hand-deleted a ledger row or ran
	// migrations against this DB from an incompatible fork — either way,
	// silently re-applying only the tail is worse than refusing.
	for v := 1; v <= maxApplied; v++ {
		if _, ok := applied[v]; !ok {
			return fmt.Errorf("migrate: ledger is missing version %d before applied version %d; refusing to run", v, maxApplied)
		}
	}

	insert := "INSERT INTO schema_migrations(version, checksum, applied_at) VALUES (?, ?, ?)"
	if s.dialect == "postgres" {
		insert = "INSERT INTO schema_migrations(version, checksum, applied_at) VALUES ($1, $2, $3)"
	}
	now := time.Now().UnixMilli()
	for _, m := range registry {
		want := checksum(m.statements)
		if got, ok := applied[m.version]; ok {
			if got != want {
				return fmt.Errorf("migrate: checksum mismatch for version %d (recorded %s, computed %s); registry statements changed since the migration ran", m.version, got, want)
			}
			continue
		}
		if err := applyMigration(ctx, tx, s.dialect, m); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, insert, m.version, want, now); err != nil {
			return fmt.Errorf("migrate: record v%d: %w", m.version, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate: commit: %w", err)
	}
	committed = true
	return nil
}

// acquireWriterLock takes an exclusive-writer lock inside the migration
// transaction as its very first op, so any concurrent startup blocks on us
// instead of racing the ledger read + pending-apply window. On Postgres it
// also creates the ledger table (safe now that we hold the lock); on SQLite
// the ledger is created before the tx starts because we need it to exist to
// UPDATE it.
func acquireWriterLock(ctx context.Context, tx *sql.Tx, dialect string) error {
	switch dialect {
	case "postgres":
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(4482093571)"); err != nil {
			return fmt.Errorf("migrate: advisory xact lock: %w", err)
		}
		// With the advisory lock held, CREATE TABLE IF NOT EXISTS is
		// serialized against any other migrator on this database.
		if _, err := tx.ExecContext(ctx, ledgerDDL); err != nil {
			return fmt.Errorf("migrate: create schema_migrations: %w", err)
		}
		return nil
	case "sqlite":
		// An UPDATE that matches zero rows still promotes the tx to a
		// RESERVED lock on the database file. No data change, but any
		// other connection trying to write (its own UPDATE / INSERT
		// during migrations) will busy-wait until we commit or roll
		// back. The ledger must already exist for the UPDATE to parse;
		// runMigrations creates it via ensureLedgerSQLite before BeginTx.
		if _, err := tx.ExecContext(ctx, "UPDATE schema_migrations SET version = version WHERE 1 = 0"); err != nil {
			return fmt.Errorf("migrate: acquire sqlite writer lock: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("migrate: unknown dialect %q", dialect)
	}
}

// validateRegistry rejects a build-time mistake before touching the DB. In
// addition to strict ascending order (each subsequent version strictly
// greater than the previous) and non-empty statement lists, the registry
// MUST be contiguous starting at 1: v1, v2, v3, ... Any gap would produce
// a database that this build's own gap check in runMigrations would then
// refuse to touch on the very next startup, so we catch it here.
func validateRegistry(reg []migration) error {
	if len(reg) == 0 {
		return fmt.Errorf("migrate: registry is empty")
	}
	for i, m := range reg {
		want := i + 1
		if m.version != want {
			return fmt.Errorf("migrate: registry must be contiguous starting at 1 (position %d wants version %d, got %d)", i, want, m.version)
		}
		if len(m.statements) == 0 {
			return fmt.Errorf("migrate: version %d has no statements", m.version)
		}
	}
	return nil
}

// ledgerDDL is the schema of the migration bookkeeping table. Referenced by
// ensureLedgerSQLite and by the Postgres branch of acquireWriterLock.
const ledgerDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	checksum   TEXT NOT NULL,
	applied_at BIGINT NOT NULL
)`

// ensureLedgerSQLite creates schema_migrations before the migration tx begins
// so the tx can take a writer lock by UPDATE-ing it. SQLite serializes the
// CREATE via the database file lock; racing openers each issue an identical
// no-op after the first winner returns. Postgres creates its ledger inside
// the tx after taking the advisory lock, and this is a no-op there.
func ensureLedgerSQLite(ctx context.Context, conn *sql.Conn, dialect string) error {
	if dialect != "sqlite" {
		return nil
	}
	if _, err := conn.ExecContext(ctx, ledgerDDL); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	return nil
}

// readLedgerTx reads schema_migrations inside the migration tx so the
// snapshot is consistent with the writer lock held by acquireWriterLock.
func readLedgerTx(ctx context.Context, tx *sql.Tx) (map[int]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("migrate: read schema_migrations: %w", err)
	}
	defer rows.Close()
	out := make(map[int]string)
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return nil, fmt.Errorf("migrate: scan schema_migrations: %w", err)
		}
		out[v] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: iterate schema_migrations: %w", err)
	}
	return out, nil
}

// applyMigration executes one version's statements inside the migration tx.
// It does NOT begin or commit the tx and it does NOT write the ledger row —
// runMigrations wraps the whole pending batch in one tx and inserts ledger
// rows there, so a failure in any statement of any pending version rolls the
// entire batch (including earlier statements and any ledger rows written in
// this run) back.
func applyMigration(ctx context.Context, tx *sql.Tx, dialect string, m migration) error {
	for _, q := range m.statements {
		skip, err := shouldSkipLegacyAlter(ctx, tx, dialect, q)
		if err != nil {
			return fmt.Errorf("migrate: v%d probe %q: %w", m.version, firstLineOf(q), err)
		}
		if skip {
			continue
		}
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("migrate: v%d %q: %w", m.version, firstLineOf(q), err)
		}
	}
	return nil
}

// shouldSkipLegacyAlter answers whether the historical
// `ALTER TABLE vps_hosts ADD COLUMN host_key ...` statement is already
// satisfied by the live schema. Fresh SQLite installs run the CREATE with the
// column baked in; only databases opened by an older Antares binary need the
// ALTER. Any other ALTER runs unconditionally — string matching duplicate
// column errors is out of scope for the ledger.
func shouldSkipLegacyAlter(ctx context.Context, tx *sql.Tx, dialect, q string) (bool, error) {
	if !isLegacyHostKeyAlter(q) {
		return false, nil
	}
	return columnExists(ctx, tx, dialect, "vps_hosts", "host_key")
}

func isLegacyHostKeyAlter(q string) bool {
	u := strings.ToUpper(strings.Join(strings.Fields(q), " "))
	return strings.HasPrefix(u, "ALTER TABLE VPS_HOSTS ADD COLUMN HOST_KEY")
}

// columnExists queries the live catalog (never a stale in-memory view) for
// the given table/column. Both dialects support running the probe inside the
// migration transaction; the answer sees earlier CREATE TABLE statements from
// the same tx.
func columnExists(ctx context.Context, tx *sql.Tx, dialect, table, column string) (bool, error) {
	switch dialect {
	case "sqlite":
		rows, err := tx.QueryContext(ctx, "SELECT 1 FROM pragma_table_info(?) WHERE name = ?", table, column)
		if err != nil {
			return false, err
		}
		defer rows.Close()
		return rows.Next(), nil
	case "postgres":
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = $1
			  AND column_name = $2`, table, column).Scan(&one)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, fmt.Errorf("columnExists: unknown dialect %q", dialect)
	}
}

// checksum is the SHA-256 of the statements joined by a NUL byte. NUL never
// appears in the DDL literals so the encoding is unambiguous; whitespace inside
// each statement is preserved verbatim so trivial reformatting IS detected.
func checksum(stmts []string) string {
	h := sha256.New()
	for i, s := range stmts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
