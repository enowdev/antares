package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/secret"

	_ "github.com/jackc/pgx/v5/stdlib" // postgres driver
	_ "modernc.org/sqlite"             // pure-Go sqlite driver
)

// ErrNotFound is returned when a lookup by id yields nothing.
var ErrNotFound = errors.New("not found")

// ErrCursorRevisionConflict is returned when a full Cursor snapshot no longer
// owns the revision it attempted to replace.
var ErrCursorRevisionConflict = errors.New("cursor session state revision conflict")

type sqlStore struct {
	db      *sql.DB
	dialect string // sqlite|postgres
	dsn     string

	// box encrypts VPS credentials at rest; obtained lazily from secret.Default
	// on the first VPS operation so opening the store never depends on the key.
	boxOnce sync.Once
	box     *secret.Box
	boxErr  error

	// socialBox encrypts social media credentials; obtained lazily from
	// secret.SocialDefault. Opt-in: if the social master key is not configured,
	// social credential storage returns an error rather than storing plaintext.
	socialBoxOnce sync.Once
	socialKeyBox  *secret.Box
	socialKeyErr  error
}

// Open connects to the configured backend and applies migrations.
// driver is one of sqlite, postgres, memory.
func Open(ctx context.Context, driver, dsn string, maxConns, busyMS int, wal bool) (Store, error) {
	var (
		db  *sql.DB
		err error
		dia string
	)
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite3":
		dia = "sqlite"
		if dsn == "" {
			return nil, errors.New("sqlite dsn (file path) is required")
		}
		if err := os.MkdirAll(filepath.Dir(dsn), 0o700); err != nil {
			return nil, err
		}
		params := []string{"_pragma=foreign_keys(1)", "_pragma=busy_timeout(" + strconv.Itoa(max(busyMS, 5000)) + ")"}
		if wal {
			params = append(params, "_pragma=journal_mode(WAL)", "_pragma=synchronous(NORMAL)")
		}
		db, err = sql.Open("sqlite", "file:"+dsn+"?"+strings.Join(params, "&"))
	case "memory", "inmemory", "in-memory":
		dia = "sqlite"
		dsn = "file::memory:?cache=shared&_pragma=busy_timeout(5000)"
		db, err = sql.Open("sqlite", dsn)
		maxConns = 1 // shared-cache in-memory DBs die when the last conn closes
	case "postgres", "postgresql", "pgx":
		dia = "postgres"
		if dsn == "" {
			return nil, errors.New("postgres dsn is required")
		}
		db, err = sql.Open("pgx", dsn)
	default:
		return nil, fmt.Errorf("unknown database driver %q (want sqlite, postgres, or memory)", driver)
	}
	if err != nil {
		return nil, err
	}
	if maxConns <= 0 {
		maxConns = 8
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxLifetime(time.Hour)

	s := &sqlStore{db: db, dialect: dia, dsn: dsn}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect %s: %w", dia, err)
	}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *sqlStore) migrate(ctx context.Context) error {
	stmts := append([]string{}, migrations...)
	if s.dialect == "sqlite" {
		stmts = append(stmts, sqliteFTS...)
	} else {
		stmts = append(stmts, postgresFTS...)
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			// Additive `ALTER TABLE ... ADD COLUMN` migrations are idempotent by
			// intent: on a DB that already has the column (fresh CREATE, or a
			// prior run) the engine reports "duplicate column", which is success
			// for our purposes. Everything else is fatal.
			if isAddColumn(q) && isDuplicateColumn(err) {
				continue
			}
			return fmt.Errorf("migrate: %w\n%s", err, firstLine(q))
		}
	}
	return nil
}

func isAddColumn(q string) bool {
	u := strings.ToUpper(q)
	return strings.Contains(u, "ALTER TABLE") && strings.Contains(u, "ADD COLUMN")
}

func isDuplicateColumn(err error) bool {
	m := strings.ToLower(err.Error())
	// sqlite: "duplicate column name"; postgres: "column ... already exists".
	return strings.Contains(m, "duplicate column") || strings.Contains(m, "already exists")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

func (s *sqlStore) Driver() string { return s.dialect }
func (s *sqlStore) Close() error   { return s.db.Close() }

func (s *sqlStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// rebind converts ? placeholders to $n for Postgres.
func (s *sqlStore) rebind(q string) string {
	if s.dialect != "postgres" {
		return q
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteByte(q[i])
	}
	return b.String()
}

func (s *sqlStore) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.rebind(q), args...)
}

func (s *sqlStore) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.rebind(q), args...)
}

func (s *sqlStore) row(ctx context.Context, q string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.rebind(q), args...)
}

// upsert renders an ON CONFLICT clause that both dialects accept.
func onConflict(key, sets string) string {
	return " ON CONFLICT(" + key + ") DO UPDATE SET " + sets
}

// ---- time helpers -----------------------------------------------------------

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func msPtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

func fromMSPtr(v sql.NullInt64) *time.Time {
	if !v.Valid || v.Int64 == 0 {
		return nil
	}
	t := time.UnixMilli(v.Int64)
	return &t
}

// ---- embedding helpers ------------------------------------------------------

func encodeEmbedding(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

func decodeEmbedding(b []byte) []float32 {
	if len(b) < 4 {
		return nil
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
