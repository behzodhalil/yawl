package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/base32"
	"fmt"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/behzodhalil/yawl/internal/server/store/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	DB *sql.DB
	Q  *db.Queries
}

func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1) // one writer; SQLite serialises anyway and this avoids SQLITE_BUSY between our own goroutines
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, err
	}
	if err := goose.Up(d, "migrations"); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{DB: d, Q: db.New(d)}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) Tx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(s.Q.WithTx(tx)); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// TakeLease takes or renews the named lease for holder until at+ttl. It reports
// false when someone else holds an unexpired lease. Written by hand because
// sqlc's SQLite engine drops parameters inside ON CONFLICT ... WHERE.
func (s *Store) TakeLease(ctx context.Context, name, holder string, at time.Time, ttl time.Duration) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO leases (name, holder, expires_at) VALUES (?, ?, ?)
ON CONFLICT (name) DO UPDATE SET holder = excluded.holder, expires_at = excluded.expires_at
WHERE leases.expires_at < ? OR leases.holder = excluded.holder`,
		name, holder, at.Add(ttl).UnixMilli(), at.UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func NewID(prefix string) string {
	b := make([]byte, 8)
	rand.Read(b)
	return prefix + strings.ToLower(b32.EncodeToString(b))[:12]
}

func Now() int64 { return time.Now().UnixMilli() }

func (s *Store) InstanceID(ctx context.Context) string {
	if id, err := s.Q.GetSetting(ctx, "instance.id"); err == nil {
		return id
	}
	id := NewID("ins_")
	s.Q.SetSetting(ctx, db.SetSettingParams{Key: "instance.id", Value: id})
	if again, err := s.Q.GetSetting(ctx, "instance.id"); err == nil {
		return again
	}
	return id
}
