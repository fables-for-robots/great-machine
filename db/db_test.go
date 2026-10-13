package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var (
	ctx = context.Background()
	// t0 is the time the tests begin at. It has nanoseconds, which the
	// database has to keep.
	t0 = time.Unix(1_800_000_000, 123_456_789).UTC()
)

func open(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "draal.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpenCreatesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draal.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the database file was not created: %v", err)
	}
	var version int
	if err := d.writer.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Errorf("user_version = %d, want 2: two migrations", version)
	}
	for _, table := range []string{"services", "generations", "rollouts", "grants"} {
		var n int
		if err := d.readers.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Errorf("table %s: %v", table, err)
		}
	}
}

func TestOpenTwiceKeepsTheData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draal.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.writer.ExecContext(ctx,
		"INSERT INTO services (name, stable, created_at) VALUES ('web', 3, 1)"); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	// The second Open finds the schema and applies no migration again: the
	// CREATE TABLE of the first would fail on a table that is there.
	d, err = Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer d.Close()
	var stable int
	if err := d.readers.QueryRowContext(ctx,
		"SELECT stable FROM services WHERE name = 'web'").Scan(&stable); err != nil {
		t.Fatal(err)
	}
	if stable != 3 {
		t.Errorf("stable = %d, want 3", stable)
	}
}

func TestOpenRefusesASchemaFromTheFuture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draal.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.writer.ExecContext(ctx, "PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if d, err := Open(path); err == nil {
		d.Close()
		t.Fatal("Open took a database with a schema version it does not know")
	}
}

func TestReadersCannotWrite(t *testing.T) {
	d := open(t)
	if _, err := d.readers.ExecContext(ctx,
		"INSERT INTO services (name, stable, created_at) VALUES ('web', 0, 1)"); err == nil {
		t.Fatal("a reading connection wrote")
	}
}

func TestTimeOf(t *testing.T) {
	if got := timeOf(0); !got.IsZero() {
		t.Errorf("timeOf(0) = %v, want the zero time", got)
	}
	if got := timeOf(t0.UnixNano()); !got.Equal(t0) || got.Location() != time.UTC {
		t.Errorf("timeOf = %v, want %v in UTC", got, t0)
	}
}
