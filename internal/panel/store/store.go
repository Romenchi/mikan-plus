package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"mikan/internal/panel/store/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	DB *sql.DB
	Q  *db.Queries
}

// Open creates the data directory if needed, opens the SQLite database and applies migrations.
func Open(ctx context.Context, dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, "mikan.db")
	// _txlock=immediate takes the write lock at BEGIN, so concurrent writers wait on
	// busy_timeout instead of failing with SQLITE_BUSY on lock upgrade.
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
		conn.Close()
		return nil, fmt.Errorf("chmod db: %w", err)
	}
	if err := migrate(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}
	return &Store{DB: conn, Q: db.New(conn)}, nil
}

func migrate(ctx context.Context, conn *sql.DB) error {
	var hasNodeRelays int
	_ = conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='node_relays'").Scan(&hasNodeRelays)
	if hasNodeRelays == 0 {
		var v13Applied int
		_ = conn.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id=13 AND is_applied=1").Scan(&v13Applied)
		if v13Applied > 0 {
			_, _ = conn.ExecContext(ctx, "DELETE FROM goose_db_version WHERE version_id=13")
		}
	}

	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, conn, fsys)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.DB.Close() }

// Tx runs fn inside a transaction bound to a Queries instance. The transaction takes the
// write lock when it begins, so a panic in fn must not leave it open: the panic goes on,
// the transaction is rolled back first (a background context would never cancel it).
func (s *Store) Tx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(s.Q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// IsUnique says whether err is a UNIQUE (or primary key) constraint violation, so that
// callers need not match the text of the driver's message.
func IsUnique(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	return se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || se.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}
