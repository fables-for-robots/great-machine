// Package db is what draal keeps, one SQLite database: the services, the
// generations they were given and the rollouts that moved them.
//
// A service has at most one rollout that is under way, which a new rollout
// supersedes. The stable generation of a service is the target of its last
// rollout that completed.
//
// Every method is one transaction. Times are stored as unix nanoseconds and
// returned in UTC; a time that is not set is stored as 0 and returned as the
// zero time.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/db/dbq"
	_ "modernc.org/sqlite" // the pure-Go SQLite driver, registered as "sqlite"
)

var (
	// ErrNotFound is returned when the service or generation asked for is
	// not in the database.
	ErrNotFound = errors.New("not found")
	// ErrNoStable is returned by NewRollback for a service none of whose
	// rollouts has completed.
	ErrNoStable = errors.New("the service has no stable generation")
	// ErrNothingToRollBack is returned by NewRollback when the last rollout
	// of the service completed: there is nothing to go back from.
	ErrNothingToRollBack = errors.New("the last rollout completed")
)

// maxReaders is the number of connections the reads share. Writes have one
// connection of their own.
const maxReaders = 4

// busyTimeout bounds how long a connection waits for a lock another
// connection holds before it fails with SQLite's busy error.
const busyTimeout = 30 * time.Second

//go:embed migrations/*.sql
var embedded embed.FS

// DB is an open database. Its methods may be called from several goroutines
// at once.
type DB struct {
	// writer is a pool of one connection, so write transactions queue in Go
	// and never meet SQLite's lock. They begin IMMEDIATE: a write never has
	// to upgrade a read lock, which SQLite refuses with a busy error that
	// waiting does not cure.
	writer *sql.DB
	// readers serve the read-only transactions, which in WAL mode neither
	// wait for the writer nor hold it up.
	readers *sql.DB
}

// Open opens the database file at path, creating it if it is missing, in WAL
// mode with foreign keys enforced, and brings its schema up to date.
func Open(path string) (*DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	writer, err := openPool(dataSourceName(abs, "_txlock=immediate"), 1)
	if err != nil {
		return nil, err
	}
	if err := prepare(context.Background(), writer); err != nil {
		writer.Close()
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	readers, err := openPool(dataSourceName(abs, "_pragma=query_only(1)"), maxReaders)
	if err != nil {
		writer.Close()
		return nil, err
	}
	return &DB{writer: writer, readers: readers}, nil
}

// Close closes the database. The last connection to go writes the log back
// into the database file.
func (d *DB) Close() error {
	return errors.Join(d.readers.Close(), d.writer.Close())
}

// dataSourceName names the database at the absolute path to the driver. The
// pragmas apply to every connection. It is a file: URI, so that '?', '#' and
// '%' in the path are escaped.
func dataSourceName(abs string, extra ...string) string {
	params := append([]string{
		fmt.Sprintf("_pragma=busy_timeout(%d)", busyTimeout.Milliseconds()),
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(FULL)",
		"_pragma=foreign_keys(1)",
	}, extra...)
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return u.String() + "?" + strings.Join(params, "&")
}

func openPool(dsn string, size int) (*sql.DB, error) {
	pool, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(size)
	pool.SetMaxIdleConns(size)
	return pool, nil
}

// prepare checks the pragmas SQLite accepts silently and then ignores (a
// filesystem without shared memory leaves the journal mode as it was) and
// applies the migrations that are missing.
func prepare(ctx context.Context, pool *sql.DB) error {
	var mode string
	if err := pool.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("read journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("journal_mode is %q, want wal", mode)
	}
	var foreignKeys int
	if err := pool.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("read foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("foreign_keys is %d, want 1", foreignKeys)
	}
	return migrate(ctx, pool)
}

// migrate applies, in one transaction, the migrations the database has not
// seen. They are the files of migrations/ in the order of their names;
// PRAGMA user_version records how many have been applied.
func migrate(ctx context.Context, pool *sql.DB) error {
	names, err := fs.Glob(embedded, "migrations/*.sql")
	if err != nil {
		return err
	}
	slices.Sort(names)
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if version < 0 || version > len(names) {
		return fmt.Errorf("schema version %d is not one this release knows (it has %d)", version, len(names))
	}
	if version == len(names) {
		return nil
	}
	for _, name := range names[version:] {
		body, err := fs.ReadFile(embedded, name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(names))); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}
	return tx.Commit()
}

// write runs fn in a write transaction, which is committed if fn returns nil
// and rolled back otherwise.
func (d *DB) write(ctx context.Context, fn func(q *dbq.Queries) error) error {
	return inTx(ctx, d.writer, nil, fn)
}

// read runs fn in a read-only transaction: every query of fn sees the
// database as it was at the first.
func (d *DB) read(ctx context.Context, fn func(q *dbq.Queries) error) error {
	return inTx(ctx, d.readers, &sql.TxOptions{ReadOnly: true}, fn)
}

func inTx(ctx context.Context, pool *sql.DB, opts *sql.TxOptions, fn func(q *dbq.Queries) error) error {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(dbq.New(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// notFound turns the absence of a row into ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// timeOf is the time a column holds: the zero time for 0.
func timeOf(nanoseconds int64) time.Time {
	if nanoseconds == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanoseconds).UTC()
}
