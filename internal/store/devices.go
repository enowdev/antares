package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Device is a paired client (a desktop app, the CLI, later a phone) holding a
// long-lived device token. Only the token's SHA-256 is stored.
type Device struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	TokenHash  string     `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// Revoked reports whether the device may no longer authorize.
func (d *Device) Revoked() bool { return d.RevokedAt != nil }

const (
	// DeviceTokenPrefix starts every device token: "atd_" + 48 hex chars.
	DeviceTokenPrefix = "atd_"
	deviceTokenBytes  = 24
	// DeviceTokenLen is the full length of a device token.
	DeviceTokenLen = len(DeviceTokenPrefix) + 2*deviceTokenBytes
	// DeviceNameMax is the longest accepted device name, in characters.
	DeviceNameMax = 64
	// deviceTouchEvery throttles last_seen_at writes per device.
	deviceTouchEvery = time.Minute
)

// DevicePlatforms are the accepted platform values; anything else is "other".
var DevicePlatforms = []string{"desktop-macos", "desktop-windows", "desktop-linux", "cli", "other"}

// ErrInvalidDeviceName is returned for an empty or over-long device name.
var ErrInvalidDeviceName = errors.New("device name must be 1-64 characters")

// NormalizeDevicePlatform maps an unknown platform to "other".
func NormalizeDevicePlatform(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	for _, ok := range DevicePlatforms {
		if p == ok {
			return p
		}
	}
	return "other"
}

// NormalizeDeviceName trims a name and checks its length (1-64 characters).
func NormalizeDeviceName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || utf8.RuneCountInString(n) > DeviceNameMax || !utf8.ValidString(n) {
		return "", ErrInvalidDeviceName
	}
	return n, nil
}

// HashDeviceToken is the stored form of a device token: SHA-256, lowercase hex.
func HashDeviceToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// LooksLikeDeviceToken reports whether s has the device token shape, so
// callers skip a database lookup for anything else.
func LooksLikeDeviceToken(s string) bool {
	if len(s) != DeviceTokenLen || !strings.HasPrefix(s, DeviceTokenPrefix) {
		return false
	}
	for _, c := range s[len(DeviceTokenPrefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// PairDevice creates a device and returns it with its plain token. The token
// exists only in the return value; the store keeps its hash.
func PairDevice(ctx context.Context, s Store, name, platform string) (*Device, string, error) {
	name, err := NormalizeDeviceName(name)
	if err != nil {
		return nil, "", err
	}
	secret, err := randomHex(deviceTokenBytes)
	if err != nil {
		return nil, "", err
	}
	idHex, err := randomHex(8)
	if err != nil {
		return nil, "", err
	}
	token := DeviceTokenPrefix + secret
	d := &Device{
		ID:        "dev_" + idHex,
		Name:      name,
		Platform:  NormalizeDevicePlatform(platform),
		TokenHash: HashDeviceToken(token),
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := s.CreateDevice(ctx, d); err != nil {
		return nil, "", err
	}
	return d, token, nil
}

const deviceCols = `id,name,platform,token_hash,created_at,last_seen_at,revoked_at`

// CreateDevice inserts a new device row.
func (s *sqlStore) CreateDevice(ctx context.Context, d *Device) error {
	if d.ID == "" || d.TokenHash == "" {
		return errors.New("device id and token hash are required")
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	}
	_, err := s.exec(ctx, `INSERT INTO devices (`+deviceCols+`) VALUES (?,?,?,?,?,?,?)`,
		d.ID, d.Name, d.Platform, d.TokenHash, ms(d.CreatedAt), msPtr(d.LastSeenAt), msPtr(d.RevokedAt))
	return err
}

// GetDevice returns one device by id, or ErrNotFound.
func (s *sqlStore) GetDevice(ctx context.Context, id string) (*Device, error) {
	return scanDeviceRow(s.row(ctx, `SELECT `+deviceCols+` FROM devices WHERE id=?`, id))
}

// DeviceByTokenHash returns the device holding a token hash (revoked or not),
// or ErrNotFound. Callers must check Revoked().
func (s *sqlStore) DeviceByTokenHash(ctx context.Context, tokenHash string) (*Device, error) {
	return scanDeviceRow(s.row(ctx, `SELECT `+deviceCols+` FROM devices WHERE token_hash=?`, tokenHash))
}

// ListDevices returns every device, newest first, revoked ones included.
func (s *sqlStore) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.query(ctx, `SELECT `+deviceCols+` FROM devices ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// RevokeDevice marks a device revoked. Revoking twice keeps the first time.
func (s *sqlStore) RevokeDevice(ctx context.Context, id string) error {
	if _, err := s.GetDevice(ctx, id); err != nil {
		return err
	}
	_, err := s.exec(ctx, `UPDATE devices SET revoked_at=? WHERE id=? AND revoked_at IS NULL`,
		time.Now().UnixMilli(), id)
	return err
}

// TouchDevice records that a device was seen at `at`, at most once a minute:
// a write within a minute of the stored value is skipped by the WHERE clause.
func (s *sqlStore) TouchDevice(ctx context.Context, id string, at time.Time) error {
	_, err := s.exec(ctx, `UPDATE devices SET last_seen_at=? WHERE id=? AND (last_seen_at IS NULL OR last_seen_at <= ?)`,
		at.UnixMilli(), id, at.Add(-deviceTouchEvery).UnixMilli())
	return err
}

func scanDeviceRow(r *sql.Row) (*Device, error) {
	d, err := scanDevice(r)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func scanDevice(sc interface{ Scan(...any) error }) (*Device, error) {
	var d Device
	var created int64
	var seen, revoked sql.NullInt64
	if err := sc.Scan(&d.ID, &d.Name, &d.Platform, &d.TokenHash, &created, &seen, &revoked); err != nil {
		return nil, err
	}
	d.CreatedAt = fromMS(created).UTC()
	d.LastSeenAt = utcPtr(fromMSPtr(seen))
	d.RevokedAt = utcPtr(fromMSPtr(revoked))
	return &d, nil
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
