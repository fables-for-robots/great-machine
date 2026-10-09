### Task 12: db, the database of draal

**Files:**
- Create: `sqlc.yaml`
- Create: `db/migrations/0001_init.sql`
- Create: `db/queries/services.sql`, `db/queries/generations.sql`, `db/queries/rollouts.sql`
- Create: `db/generate.go`
- Create: `db/dbq/` (what `sqlc generate` writes: `db.go`, `models.go`, `services.sql.go`, `generations.sql.go`, `rollouts.sql.go`; committed, never edited)
- Create: `db/db.go` (opening, pragmas, migrations, transactions)
- Create: `db/store.go` (the methods of the contract)
- Test: `db/db_test.go`, `db/store_test.go`
- Modify: `go.mod`, `go.sum` (`modernc.org/sqlite v1.59.0`)
- Modify: `.github/workflows/test.yml` (a step named `sqlc` after the step named `test`)

**Interfaces:**
- Consumes, from `wire` (Task 2):

  ```go
  type ID string

  type RolloutRequest struct {
  	Service  string   `json:"service"`
  	Binary   string   `json:"binary"`
  	Args     []string `json:"args,omitempty"`
  	Closure  []string `json:"closure"`
  	Parallel int      `json:"parallel"`
  }
  type RollbackRequest struct {
  	Service  string `json:"service"`
  	Parallel int    `json:"parallel"`
  }

  type RolloutState string

  const (
  	RolloutPreparing  RolloutState = "preparing"
  	RolloutActivating RolloutState = "activating"
  	RolloutCompleted  RolloutState = "completed"
  	RolloutHalted     RolloutState = "halted"
  	RolloutSuperseded RolloutState = "superseded"
  )

  type RolloutInfo struct {
  	ID         uint64       `json:"id"`
  	Service    string       `json:"service"`
  	Generation uint64       `json:"generation"`
  	Parallel   int          `json:"parallel"`
  	State      RolloutState `json:"state"`
  	Reason     string       `json:"reason,omitempty"`
  	Created    time.Time    `json:"created"`
  	CreatedBy  ID           `json:"created_by"`
  	Finished   time.Time    `json:"finished"` // the zero time while under way
  	Prepared  int `json:"prepared"`
  	Activated int `json:"activated"`
  	Awaited   int `json:"awaited"`
  }
  ```

- Produces, package `db`:

  ```go
  var (
  	ErrNotFound          = errors.New("not found")
  	ErrNoStable          = errors.New("the service has no stable generation")
  	ErrNothingToRollBack = errors.New("the last rollout completed")
  )

  type DB struct{ /* unexported */ }

  func Open(path string) (*DB, error)
  func (d *DB) Close() error

  type Service struct {
  	Name   string
  	Stable uint64 // 0 for none
  }

  type Generation struct {
  	Service   string
  	Number    uint64
  	Binary    string
  	Args      []string // nil for none
  	Closure   []string
  	Created   time.Time
  	CreatedBy wire.ID
  }

  func (d *DB) Services(ctx context.Context) ([]Service, error)
  func (d *DB) Service(ctx context.Context, name string) (Service, error)
  func (d *DB) Generation(ctx context.Context, service string, number uint64) (Generation, error)
  func (d *DB) NewRollout(ctx context.Context, req wire.RolloutRequest, by wire.ID, now time.Time) (wire.RolloutInfo, error)
  func (d *DB) NewRollback(ctx context.Context, req wire.RollbackRequest, by wire.ID, now time.Time) (wire.RolloutInfo, error)
  func (d *DB) LastRollout(ctx context.Context, service string) (wire.RolloutInfo, bool, error)
  func (d *DB) Rollouts(ctx context.Context, service string, limit int) ([]wire.RolloutInfo, error)
  func (d *DB) SetRolloutState(ctx context.Context, id uint64, state wire.RolloutState, reason string, now time.Time) (bool, error)
  ```

  What the contract leaves open, and this task settles:

  - The errors are wrapped: compare them with `errors.Is`.
  - Times are stored as Unix nanoseconds and come back in UTC, equal to what
    went in. A `Finished` that is not set comes back as the zero `time.Time`.
  - `Generation.Args` is **nil** for a generation without arguments, whether
    the request had `nil` or an empty slice. Arguments and closure are
    stored as JSON text, and every other value comes back byte for byte.
  - A rollout that is superseded gets `Finished` set to the `now` of the
    rollout that superseded it.
  - `SetRolloutState` takes `activating`, `completed` and `halted`; any
    other state is an error. It may move a rollout from `preparing`
    straight to `completed` or `halted`. For a rollout that is not there it
    returns false and no error.
  - `Rollouts` with a limit of 0 or less returns none.
  - `NewRollback` checks in this order: `ErrNotFound`, `ErrNoStable`,
    `ErrNothingToRollBack`.
  - A partial unique index lets the database itself refuse a second
    rollout under way for a service.

The layout and the practices are those of `db/` in jaccard-store: the
migrations are embedded and applied at `Open`, with `PRAGMA user_version`
counting them; the queries are in `db/queries/*.sql`; what sqlc makes of
them is in `db/dbq`, committed; writes go through a pool of one connection
that begins every transaction `IMMEDIATE`, reads through a pool of four
connections that are `query_only`; the journal is WAL, `synchronous` is
`FULL`, foreign keys are enforced and a lock is waited for 30 seconds.

- [ ] **Step 1: Write the failing test of Open**

`db/db_test.go`:

```go
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
	if version != 1 {
		t.Errorf("user_version = %d, want 1: one migration", version)
	}
	for _, table := range []string{"services", "generations", "rollouts"} {
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
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./db/`
Expected: FAIL — the package does not build: `undefined: DB`, `undefined: Open`, `undefined: timeOf`

- [ ] **Step 3: Add the driver**

```bash
go get modernc.org/sqlite@v1.59.0
```

Expected: `go.mod` requires `modernc.org/sqlite v1.59.0`. (If the default
dev shell cannot be entered while `go.sum` is behind, run it as
`nix develop .#bare -c go get modernc.org/sqlite@v1.59.0`.)

- [ ] **Step 4: Write the schema, the queries and the sqlc configuration**

`sqlc.yaml`, at the root of the repository:

```yaml
version: "2"
sql:
  - engine: "sqlite"
    schema: "db/migrations"
    queries: "db/queries"
    gen:
      go:
        package: "dbq"
        out: "db/dbq"
        omit_sqlc_version: true
        emit_empty_slices: true
```

`db/migrations/0001_init.sql`:

```sql
-- A service gets its row with its first rollout.
CREATE TABLE services (
  name       TEXT    PRIMARY KEY,
  stable     INTEGER NOT NULL DEFAULT 0,   -- the stable generation; 0: none
  created_at INTEGER NOT NULL              -- unix nanoseconds
);

-- A generation is what a service runs. It is numbered from 1 within its
-- service and never changed.
CREATE TABLE generations (
  service    TEXT    NOT NULL REFERENCES services(name),
  number     INTEGER NOT NULL,
  binary     TEXT    NOT NULL,   -- an absolute path under /nix/store
  args       TEXT    NOT NULL,   -- a JSON array of strings
  closure    TEXT    NOT NULL,   -- a JSON array of names of store paths
  created_at INTEGER NOT NULL,   -- unix nanoseconds
  created_by TEXT    NOT NULL,   -- endpoint ID
  PRIMARY KEY (service, number)
);

-- A rollout is the move of a service to a generation.
CREATE TABLE rollouts (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  service     TEXT    NOT NULL,
  generation  INTEGER NOT NULL,
  parallel    INTEGER NOT NULL,              -- 0: all at once
  state       TEXT    NOT NULL,              -- 'preparing', 'activating', 'completed', 'halted' or 'superseded'
  reason      TEXT    NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,              -- unix nanoseconds
  created_by  TEXT    NOT NULL,              -- endpoint ID
  finished_at INTEGER NOT NULL DEFAULT 0,    -- unix nanoseconds; 0: under way
  FOREIGN KEY (service, generation) REFERENCES generations(service, number)
);
CREATE INDEX rollouts_service ON rollouts(service, id);
-- Not in the design's table: a service has at most one rollout under way, and
-- the database refuses a second one whatever the code does.
CREATE UNIQUE INDEX rollouts_under_way ON rollouts(service)
  WHERE state IN ('preparing', 'activating');
```

`db/queries/services.sql`:

```sql
-- name: InsertService :exec
-- Makes the row of a service that has none.
INSERT INTO services (name, stable, created_at)
VALUES (sqlc.arg(name), 0, sqlc.arg(created_at))
ON CONFLICT (name) DO NOTHING;

-- name: ServiceByName :one
SELECT * FROM services WHERE name = sqlc.arg(name);

-- name: ListServices :many
SELECT * FROM services ORDER BY name;

-- name: SetStable :exec
UPDATE services SET stable = sqlc.arg(stable) WHERE name = sqlc.arg(name);
```

`db/queries/generations.sql`:

```sql
-- name: LastGenerationNumber :one
-- The newest generation of a service. No row: it has none.
SELECT number FROM generations
WHERE service = sqlc.arg(service)
ORDER BY number DESC
LIMIT 1;

-- name: InsertGeneration :exec
INSERT INTO generations (service, number, binary, args, closure, created_at, created_by)
VALUES (
    sqlc.arg(service), sqlc.arg(number), sqlc.arg(binary), sqlc.arg(args),
    sqlc.arg(closure), sqlc.arg(created_at), sqlc.arg(created_by)
);

-- name: GenerationByNumber :one
SELECT * FROM generations
WHERE service = sqlc.arg(service) AND number = sqlc.arg(number);
```

`db/queries/rollouts.sql`:

```sql
-- name: InsertRollout :one
INSERT INTO rollouts (service, generation, parallel, state, reason, created_at, created_by, finished_at)
VALUES (
    sqlc.arg(service), sqlc.arg(generation), sqlc.arg(parallel), 'preparing',
    '', sqlc.arg(created_at), sqlc.arg(created_by), 0
)
RETURNING *;

-- name: SupersedeUnderWay :execrows
-- Ends the rollout of a service that is under way, if there is one.
UPDATE rollouts
SET state = 'superseded', finished_at = sqlc.arg(finished_at)
WHERE service = sqlc.arg(service) AND state IN ('preparing', 'activating');

-- name: RolloutByID :one
SELECT * FROM rollouts WHERE id = sqlc.arg(id);

-- name: LastRollout :one
SELECT * FROM rollouts
WHERE service = sqlc.arg(service)
ORDER BY id DESC
LIMIT 1;

-- name: ListRollouts :many
-- Newest first.
SELECT * FROM rollouts
WHERE service = sqlc.arg(service)
ORDER BY id DESC
LIMIT sqlc.arg(max);

-- name: SetRolloutState :execrows
-- Only a rollout that is under way is moved: the count is 0 for one that
-- was superseded, or has ended, in the meantime.
UPDATE rollouts
SET state = sqlc.arg(state), reason = sqlc.arg(reason), finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id) AND state IN ('preparing', 'activating');
```

`db/generate.go`:

```go
package db

// The queries in queries/ are compiled against the schema in migrations/ into
// dbq. sqlc comes from the Nix dev shell; the output is committed, so
// building needs no sqlc.
//go:generate sqlc generate -f ../sqlc.yaml
```

- [ ] **Step 5: Generate `db/dbq`**

Run: `go generate ./db`
Expected: no output, and five new files: `db/dbq/db.go`, `db/dbq/models.go`,
`db/dbq/services.sql.go`, `db/dbq/generations.sql.go`, `db/dbq/rollouts.sql.go`.

Run: `sqlc diff`
Expected: no output, exit status 0.

The generated code is not edited. What the handwritten code below relies
on, as sqlc 1.31.1 makes it (check with `grep -n '^func\|^type' db/dbq/*.go`):

```go
// models.go
type Generation struct {
	Service   string
	Number    int64
	Binary    string
	Args      string
	Closure   string
	CreatedAt int64
	CreatedBy string
}

type Rollout struct {
	ID         int64
	Service    string
	Generation int64
	Parallel   int64
	State      string
	Reason     string
	CreatedAt  int64
	CreatedBy  string
	FinishedAt int64
}

type Service struct {
	Name      string
	Stable    int64
	CreatedAt int64
}

// the queries
func (q *Queries) InsertService(ctx context.Context, arg InsertServiceParams) error             // Name, CreatedAt
func (q *Queries) ServiceByName(ctx context.Context, name string) (Service, error)
func (q *Queries) ListServices(ctx context.Context) ([]Service, error)
func (q *Queries) SetStable(ctx context.Context, arg SetStableParams) error                     // Stable, Name
func (q *Queries) LastGenerationNumber(ctx context.Context, service string) (int64, error)
func (q *Queries) InsertGeneration(ctx context.Context, arg InsertGenerationParams) error       // Service, Number, Binary, Args, Closure, CreatedAt, CreatedBy
func (q *Queries) GenerationByNumber(ctx context.Context, arg GenerationByNumberParams) (Generation, error) // Service, Number
func (q *Queries) InsertRollout(ctx context.Context, arg InsertRolloutParams) (Rollout, error)  // Service, Generation, Parallel, CreatedAt, CreatedBy
func (q *Queries) SupersedeUnderWay(ctx context.Context, arg SupersedeUnderWayParams) (int64, error) // FinishedAt, Service
func (q *Queries) RolloutByID(ctx context.Context, id int64) (Rollout, error)
func (q *Queries) LastRollout(ctx context.Context, service string) (Rollout, error)
func (q *Queries) ListRollouts(ctx context.Context, arg ListRolloutsParams) ([]Rollout, error)  // Service, Max
func (q *Queries) SetRolloutState(ctx context.Context, arg SetRolloutStateParams) (int64, error) // State, Reason, FinishedAt, ID
```

- [ ] **Step 6: Write Open**

`db/db.go`:

```go
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
```

- [ ] **Step 7: Run the tests to see them pass**

```bash
go mod tidy
go test ./db/ -v
```

Expected: PASS — `TestOpenCreatesTheFile`, `TestOpenTwiceKeepsTheData`,
`TestOpenRefusesASchemaFromTheFuture`, `TestReadersCannotWrite`, `TestTimeOf`.

Run: `test -z "$(gofmt -l .)" && go vet ./...`
Expected: no output. (`db/dbq` is formatted as sqlc writes it.)

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum sqlc.yaml db/
git commit -m "DB: the schema of draal, its queries and Open"
```

- [ ] **Step 9: Write the failing tests of the store**

`db/store_test.go`:

```go
package db

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	alice = wire.ID("a11ce00000000000000000000000000000000000000000000000000000000000")
	bob   = wire.ID("b0b0000000000000000000000000000000000000000000000000000000000000")
)

// request is a rollout of service, with a binary and a closure that tell
// one request from the next.
func request(service string, n int) wire.RolloutRequest {
	return wire.RolloutRequest{
		Service:  service,
		Binary:   fmt.Sprintf("/nix/store/%032d-web/bin/web", n),
		Args:     []string{"--listen", ":8080"},
		Closure:  []string{fmt.Sprintf("%032d-web", n), fmt.Sprintf("%032d-glibc", n)},
		Parallel: 1,
	}
}

func mustRollout(t *testing.T, d *DB, req wire.RolloutRequest, now time.Time) wire.RolloutInfo {
	t.Helper()
	r, err := d.NewRollout(ctx, req, alice, now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustSet(t *testing.T, d *DB, id uint64, state wire.RolloutState, reason string, now time.Time) {
	t.Helper()
	moved, err := d.SetRolloutState(ctx, id, state, reason, now)
	if err != nil {
		t.Fatal(err)
	}
	if !moved {
		t.Fatalf("rollout %d was not moved to %s", id, state)
	}
}

func mustLast(t *testing.T, d *DB, service string) wire.RolloutInfo {
	t.Helper()
	r, found, err := d.LastRollout(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("%s has no rollout", service)
	}
	return r
}

func stableOf(t *testing.T, d *DB, service string) uint64 {
	t.Helper()
	s, err := d.Service(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	return s.Stable
}

// complete rolls a new generation of service out to the end.
func complete(t *testing.T, d *DB, service string, n int, now time.Time) wire.RolloutInfo {
	t.Helper()
	r := mustRollout(t, d, request(service, n), now)
	mustSet(t, d, r.ID, wire.RolloutActivating, "", now)
	mustSet(t, d, r.ID, wire.RolloutCompleted, "", now)
	return r
}

func TestNewRolloutOfANewService(t *testing.T) {
	d := open(t)
	if _, err := d.Service(ctx, "web"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Service before the first rollout: %v, want ErrNotFound", err)
	}
	if _, found, err := d.LastRollout(ctx, "web"); err != nil || found {
		t.Fatalf("LastRollout before the first rollout: found %v, %v", found, err)
	}

	req := request("web", 1)
	req.Parallel = 3
	got, err := d.NewRollout(ctx, req, alice, t0)
	if err != nil {
		t.Fatal(err)
	}
	want := wire.RolloutInfo{
		ID:         got.ID,
		Service:    "web",
		Generation: 1,
		Parallel:   3,
		State:      wire.RolloutPreparing,
		Created:    t0,
		CreatedBy:  alice,
	}
	if got != want {
		t.Errorf("NewRollout:\n got %+v\nwant %+v", got, want)
	}
	if got.ID == 0 {
		t.Error("the rollout has no number")
	}
	if !got.Finished.IsZero() {
		t.Errorf("Finished = %v, want the zero time while under way", got.Finished)
	}
	if last := mustLast(t, d, "web"); last != want {
		t.Errorf("LastRollout:\n got %+v\nwant %+v", last, want)
	}

	s, err := d.Service(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	if s != (Service{Name: "web", Stable: 0}) {
		t.Errorf("Service = %+v, want web without a stable generation", s)
	}

	g, err := d.Generation(ctx, "web", 1)
	if err != nil {
		t.Fatal(err)
	}
	if g.Service != "web" || g.Number != 1 || g.Binary != req.Binary || g.CreatedBy != alice || !g.Created.Equal(t0) {
		t.Errorf("Generation = %+v", g)
	}
	if !slices.Equal(g.Args, req.Args) {
		t.Errorf("Args = %q, want %q", g.Args, req.Args)
	}
	if !slices.Equal(g.Closure, req.Closure) {
		t.Errorf("Closure = %q, want %q", g.Closure, req.Closure)
	}
}

func TestGenerationKeepsArgumentsExactly(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"spaces, quotes and what is no ASCII",
			[]string{"--motd", `it's a "großer" Tag, 日本 ☃`, "", " lead and trail ", "a\tb\nc", `back\slash`, "[]", `["x"]`},
			[]string{"--motd", `it's a "großer" Tag, 日本 ☃`, "", " lead and trail ", "a\tb\nc", `back\slash`, "[]", `["x"]`}},
		{"one empty argument", []string{""}, []string{""}},
		// No arguments come back as a nil slice, whether nil or an empty
		// slice went in.
		{"none, as nil", nil, nil},
		{"none, as an empty slice", []string{}, nil},
	}
	d := open(t)
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := request("web", i)
			req.Args = tt.args
			r := mustRollout(t, d, req, t0)
			g, err := d.Generation(ctx, "web", r.Generation)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(g.Args, tt.want) {
				t.Errorf("Args = %q, want %q", g.Args, tt.want)
			}
			if len(tt.want) == 0 && g.Args != nil {
				t.Errorf("Args = %#v, want nil for no arguments", g.Args)
			}
			if !slices.Equal(g.Closure, req.Closure) {
				t.Errorf("Closure = %q, want %q", g.Closure, req.Closure)
			}
		})
	}
}

func TestGenerationNotFound(t *testing.T) {
	d := open(t)
	if _, err := d.Generation(ctx, "web", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("of a service that is not there: %v, want ErrNotFound", err)
	}
	mustRollout(t, d, request("web", 1), t0)
	if _, err := d.Generation(ctx, "web", 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("a number that is not there: %v, want ErrNotFound", err)
	}
	if _, err := d.Generation(ctx, "web", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("generation 0: %v, want ErrNotFound", err)
	}
}

func TestSecondRolloutSupersedesTheFirst(t *testing.T) {
	d := open(t)
	first := mustRollout(t, d, request("web", 1), t0)
	second, err := d.NewRollout(ctx, request("web", 2), bob, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != 2 || second.State != wire.RolloutPreparing || second.CreatedBy != bob {
		t.Errorf("second = %+v, want generation 2, preparing, by bob", second)
	}
	if second.ID <= first.ID {
		t.Errorf("rollout numbers %d then %d, want them to grow", first.ID, second.ID)
	}

	all, err := d.Rollouts(ctx, "web", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != second.ID || all[1].ID != first.ID {
		t.Fatalf("Rollouts = %+v, want the second, then the first", all)
	}
	if all[1].State != wire.RolloutSuperseded {
		t.Errorf("the first is %s, want superseded", all[1].State)
	}
	if !all[1].Finished.Equal(t0.Add(time.Minute)) {
		t.Errorf("the first finished at %v, want when it was superseded", all[1].Finished)
	}

	// The generation of the first stays what it was.
	g, err := d.Generation(ctx, "web", 1)
	if err != nil {
		t.Fatal(err)
	}
	if g.Binary != request("web", 1).Binary {
		t.Errorf("generation 1 runs %s", g.Binary)
	}

	// Another service is not touched.
	other := mustRollout(t, d, request("api", 3), t0)
	if other.Generation != 1 {
		t.Errorf("the first generation of api is %d, want 1", other.Generation)
	}
	if got := mustLast(t, d, "web"); got.ID != second.ID || got.State != wire.RolloutPreparing {
		t.Errorf("web's last rollout = %+v, want the second, preparing", got)
	}
}

func TestSetRolloutStateCompletes(t *testing.T) {
	d := open(t)
	r := mustRollout(t, d, request("web", 1), t0)

	mustSet(t, d, r.ID, wire.RolloutActivating, "", t0.Add(time.Second))
	got := mustLast(t, d, "web")
	if got.State != wire.RolloutActivating || !got.Finished.IsZero() {
		t.Errorf("after activating: %+v, want activating and not finished", got)
	}
	if stable := stableOf(t, d, "web"); stable != 0 {
		t.Errorf("stable = %d while activating, want 0", stable)
	}

	done := t0.Add(time.Minute)
	mustSet(t, d, r.ID, wire.RolloutCompleted, "", done)
	got = mustLast(t, d, "web")
	if got.State != wire.RolloutCompleted || !got.Finished.Equal(done) || got.Reason != "" {
		t.Errorf("after completed: %+v, want completed, finished at %v", got, done)
	}
	if stable := stableOf(t, d, "web"); stable != 1 {
		t.Errorf("stable = %d, want 1: the target of the rollout that completed", stable)
	}

	// A rollout that has ended is not moved again.
	moved, err := d.SetRolloutState(ctx, r.ID, wire.RolloutHalted, "too late", done.Add(time.Second))
	if err != nil || moved {
		t.Errorf("halting a completed rollout: moved %v, %v; want false, nil", moved, err)
	}
	if got := mustLast(t, d, "web"); got.State != wire.RolloutCompleted || got.Reason != "" || !got.Finished.Equal(done) {
		t.Errorf("a completed rollout was changed: %+v", got)
	}
}

func TestSetRolloutStateFromPreparingToCompleted(t *testing.T) {
	// A service without a zathras completes in one decision.
	d := open(t)
	r := mustRollout(t, d, request("web", 1), t0)
	mustSet(t, d, r.ID, wire.RolloutCompleted, "", t0)
	if stable := stableOf(t, d, "web"); stable != 1 {
		t.Errorf("stable = %d, want 1", stable)
	}
}

func TestSetRolloutStateOnASupersededRollout(t *testing.T) {
	d := open(t)
	complete(t, d, "web", 1, t0)
	late := mustRollout(t, d, request("web", 2), t0)
	mustSet(t, d, late.ID, wire.RolloutActivating, "", t0)
	current := mustRollout(t, d, request("web", 3), t0.Add(time.Minute))

	// A decision that was made before the rollout was superseded arrives
	// after: it must not complete the rollout, and must not make generation
	// 2 the stable one.
	for _, state := range []wire.RolloutState{wire.RolloutCompleted, wire.RolloutHalted, wire.RolloutActivating} {
		moved, err := d.SetRolloutState(ctx, late.ID, state, "late", t0.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if moved {
			t.Errorf("a superseded rollout was moved to %s", state)
		}
	}
	if stable := stableOf(t, d, "web"); stable != 1 {
		t.Errorf("stable = %d, want 1 untouched", stable)
	}
	all, err := d.Rollouts(ctx, "web", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("%d rollouts, want 3", len(all))
	}
	if all[0].ID != current.ID || all[0].State != wire.RolloutPreparing {
		t.Errorf("the current rollout = %+v, want preparing", all[0])
	}
	if all[1].State != wire.RolloutSuperseded || all[1].Reason != "" || !all[1].Finished.Equal(t0.Add(time.Minute)) {
		t.Errorf("the superseded rollout was changed: %+v", all[1])
	}
}

func TestSetRolloutStateHalts(t *testing.T) {
	d := open(t)
	complete(t, d, "web", 1, t0)
	r := mustRollout(t, d, request("web", 2), t0)
	mustSet(t, d, r.ID, wire.RolloutActivating, "", t0)
	at := t0.Add(time.Minute)
	mustSet(t, d, r.ID, wire.RolloutHalted, "web-1: exited with status 1 after 2.1s", at)

	got := mustLast(t, d, "web")
	if got.State != wire.RolloutHalted || got.Reason != "web-1: exited with status 1 after 2.1s" || !got.Finished.Equal(at) {
		t.Errorf("after halted: %+v", got)
	}
	if stable := stableOf(t, d, "web"); stable != 1 {
		t.Errorf("stable = %d, want 1: a rollout that halted changes nothing", stable)
	}
}

func TestSetRolloutStateRefusesOtherStates(t *testing.T) {
	d := open(t)
	r := mustRollout(t, d, request("web", 1), t0)
	for _, state := range []wire.RolloutState{wire.RolloutPreparing, wire.RolloutSuperseded, "", "done"} {
		if moved, err := d.SetRolloutState(ctx, r.ID, state, "", t0); err == nil || moved {
			t.Errorf("to %q: moved %v, %v; want an error", state, moved, err)
		}
	}
	if got := mustLast(t, d, "web"); got.State != wire.RolloutPreparing {
		t.Errorf("the rollout is %s, want preparing", got.State)
	}
	if moved, err := d.SetRolloutState(ctx, r.ID+100, wire.RolloutCompleted, "", t0); err != nil || moved {
		t.Errorf("a rollout that is not there: moved %v, %v; want false, nil", moved, err)
	}
}

func TestNewRollback(t *testing.T) {
	d := open(t)
	back := wire.RollbackRequest{Service: "web", Parallel: 2}

	if _, err := d.NewRollback(ctx, back, alice, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("of a service that is not there: %v, want ErrNotFound", err)
	}

	first := mustRollout(t, d, request("web", 1), t0)
	if _, err := d.NewRollback(ctx, back, alice, t0); !errors.Is(err, ErrNoStable) {
		t.Errorf("before any rollout completed: %v, want ErrNoStable", err)
	}
	mustSet(t, d, first.ID, wire.RolloutCompleted, "", t0)
	if _, err := d.NewRollback(ctx, back, alice, t0); !errors.Is(err, ErrNothingToRollBack) {
		t.Errorf("after the last rollout completed: %v, want ErrNothingToRollBack", err)
	}
	// What was refused left nothing behind.
	if all, err := d.Rollouts(ctx, "web", 20); err != nil || len(all) != 1 {
		t.Fatalf("%d rollouts after three refusals, %v; want 1", len(all), err)
	}

	// Generation 2 is under way when the operator rolls back.
	second := mustRollout(t, d, request("web", 2), t0.Add(time.Minute))
	mustSet(t, d, second.ID, wire.RolloutActivating, "", t0.Add(time.Minute))
	at := t0.Add(2 * time.Minute)
	got, err := d.NewRollback(ctx, back, bob, at)
	if err != nil {
		t.Fatal(err)
	}
	want := wire.RolloutInfo{
		ID:         got.ID,
		Service:    "web",
		Generation: 1,
		Parallel:   2,
		State:      wire.RolloutPreparing,
		Created:    at,
		CreatedBy:  bob,
	}
	if got != want {
		t.Errorf("NewRollback:\n got %+v\nwant %+v", got, want)
	}
	all, err := d.Rollouts(ctx, "web", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0] != want || all[1].ID != second.ID || all[1].State != wire.RolloutSuperseded {
		t.Errorf("Rollouts = %+v, want the rollback, then the second, superseded", all)
	}
	// A rollback makes no generation.
	if _, err := d.Generation(ctx, "web", 3); !errors.Is(err, ErrNotFound) {
		t.Errorf("generation 3 after a rollback: %v, want ErrNotFound", err)
	}

	// It completes like any rollout, and then there is nothing to go back
	// from again.
	mustSet(t, d, got.ID, wire.RolloutCompleted, "", at)
	if stable := stableOf(t, d, "web"); stable != 1 {
		t.Errorf("stable = %d, want 1", stable)
	}
	if _, err := d.NewRollback(ctx, back, alice, at); !errors.Is(err, ErrNothingToRollBack) {
		t.Errorf("after the rollback completed: %v, want ErrNothingToRollBack", err)
	}

	// After a rollout that halted there is something to go back from, and
	// the next rollout is generation 3: a rollback took no number.
	third := mustRollout(t, d, request("web", 3), at)
	if third.Generation != 3 {
		t.Errorf("the rollout after a rollback is generation %d, want 3", third.Generation)
	}
	mustSet(t, d, third.ID, wire.RolloutHalted, "web-1: no", at)
	again, err := d.NewRollback(ctx, back, alice, at)
	if err != nil {
		t.Fatalf("after a rollout that halted: %v", err)
	}
	if again.Generation != 1 {
		t.Errorf("the rollback targets %d, want 1", again.Generation)
	}
}

func TestRolloutsOrderAndLimit(t *testing.T) {
	d := open(t)
	if all, err := d.Rollouts(ctx, "web", 20); err != nil || len(all) != 0 {
		t.Fatalf("of a service that is not there: %v, %v; want none", all, err)
	}
	var ids []uint64
	for i := 1; i <= 5; i++ {
		ids = append(ids, mustRollout(t, d, request("web", i), t0.Add(time.Duration(i)*time.Second)).ID)
	}
	mustRollout(t, d, request("api", 9), t0)
	slices.Reverse(ids)

	tests := []struct {
		limit int
		want  []uint64
	}{
		{20, ids},
		{5, ids},
		{2, ids[:2]},
		{1, ids[:1]},
		{0, nil},
		{-1, nil},
	}
	for _, tt := range tests {
		all, err := d.Rollouts(ctx, "web", tt.limit)
		if err != nil {
			t.Fatal(err)
		}
		var got []uint64
		for _, r := range all {
			if r.Service != "web" {
				t.Errorf("limit %d: a rollout of %s among web's", tt.limit, r.Service)
			}
			got = append(got, r.ID)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("limit %d: rollouts %v, want %v", tt.limit, got, tt.want)
		}
	}
	if last := mustLast(t, d, "web"); last.ID != ids[0] || last.Generation != 5 {
		t.Errorf("LastRollout = %+v, want generation 5", last)
	}
}

func TestServicesSorted(t *testing.T) {
	d := open(t)
	if all, err := d.Services(ctx); err != nil || len(all) != 0 {
		t.Fatalf("Services of an empty database: %v, %v", all, err)
	}
	for i, name := range []string{"web", "api", "worker-2", "worker-10"} {
		mustRollout(t, d, request(name, i), t0)
	}
	complete(t, d, "api", 7, t0)
	got, err := d.Services(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Service{{"api", 2}, {"web", 0}, {"worker-10", 0}, {"worker-2", 0}}
	if !slices.Equal(got, want) {
		t.Errorf("Services = %+v, want %+v", got, want)
	}
}

// Rollouts that are asked for at the same time get generations that follow
// one another, and leave exactly one rollout under way.
func TestConcurrentRollouts(t *testing.T) {
	d := open(t)
	const callers = 8
	var wg sync.WaitGroup
	generations := make([]uint64, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := d.NewRollout(ctx, request("web", i), alice, t0)
			generations[i], errs[i] = r.Generation, err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(generations)
	for i, g := range generations {
		if g != uint64(i+1) {
			t.Fatalf("generations %v, want 1 to %d, each once", generations, callers)
		}
	}

	all, err := d.Rollouts(ctx, "web", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != callers {
		t.Fatalf("%d rollouts, want %d", len(all), callers)
	}
	for i, r := range all {
		want := wire.RolloutSuperseded
		if i == 0 {
			want = wire.RolloutPreparing
		}
		if r.State != want {
			t.Errorf("rollout %d (generation %d) is %s, want %s", r.ID, r.Generation, r.State, want)
		}
	}
}

// A decision for a rollout races with the rollout that supersedes it:
// whichever comes first, the stable generation is never that of a rollout
// that was superseded.
func TestConcurrentSetStateAndSupersede(t *testing.T) {
	for range 20 {
		d := open(t)
		complete(t, d, "web", 1, t0)
		old := mustRollout(t, d, request("web", 2), t0)
		var wg sync.WaitGroup
		var moved bool
		var setErr, newErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			moved, setErr = d.SetRolloutState(ctx, old.ID, wire.RolloutCompleted, "", t0)
		}()
		go func() {
			defer wg.Done()
			_, newErr = d.NewRollout(ctx, request("web", 3), alice, t0)
		}()
		wg.Wait()
		if setErr != nil || newErr != nil {
			t.Fatal(setErr, newErr)
		}
		all, err := d.Rollouts(ctx, "web", 3)
		if err != nil {
			t.Fatal(err)
		}
		wantStable, wantState := uint64(1), wire.RolloutSuperseded
		if moved {
			wantStable, wantState = 2, wire.RolloutCompleted
		}
		if stable := stableOf(t, d, "web"); stable != wantStable {
			t.Errorf("moved %v: stable = %d, want %d", moved, stable, wantStable)
		}
		if all[1].State != wantState {
			t.Errorf("moved %v: the old rollout is %s, want %s", moved, all[1].State, wantState)
		}
		if all[0].State != wire.RolloutPreparing {
			t.Errorf("the new rollout is %s, want preparing", all[0].State)
		}
	}
}
```

- [ ] **Step 10: Run the tests to see them fail**

Run: `go test ./db/`
Expected: FAIL — the package does not build: `d.NewRollout undefined (type *DB has no field or method NewRollout)`, `undefined: Service`, and the like for the other methods

- [ ] **Step 11: Write the store**

`db/store.go`:

```go
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fables-for-robots/great-machine/db/dbq"
	"github.com/fables-for-robots/great-machine/wire"
)

// Service is a service as the database has it.
type Service struct {
	Name   string
	Stable uint64 // 0 for none
}

// Generation is what a service runs: a binary, its arguments and the closure
// of the store path the binary is in. Args is nil for a generation without
// arguments.
type Generation struct {
	Service   string
	Number    uint64
	Binary    string
	Args      []string
	Closure   []string
	Created   time.Time
	CreatedBy wire.ID
}

func serviceOf(r dbq.Service) Service {
	return Service{Name: r.Name, Stable: uint64(r.Stable)}
}

func generationOf(r dbq.Generation) (Generation, error) {
	args, err := listOf(r.Args)
	if err != nil {
		return Generation{}, fmt.Errorf("db: generation %d of %s: args: %w", r.Number, r.Service, err)
	}
	closure, err := listOf(r.Closure)
	if err != nil {
		return Generation{}, fmt.Errorf("db: generation %d of %s: closure: %w", r.Number, r.Service, err)
	}
	return Generation{
		Service:   r.Service,
		Number:    uint64(r.Number),
		Binary:    r.Binary,
		Args:      args,
		Closure:   closure,
		Created:   timeOf(r.CreatedAt),
		CreatedBy: wire.ID(r.CreatedBy),
	}, nil
}

// rolloutOf is a row as it travels. The counts stay zero: they are of the
// zathrases that are connected, which the database knows nothing of.
func rolloutOf(r dbq.Rollout) wire.RolloutInfo {
	return wire.RolloutInfo{
		ID:         uint64(r.ID),
		Service:    r.Service,
		Generation: uint64(r.Generation),
		Parallel:   int(r.Parallel),
		State:      wire.RolloutState(r.State),
		Reason:     r.Reason,
		Created:    timeOf(r.CreatedAt),
		CreatedBy:  wire.ID(r.CreatedBy),
		Finished:   timeOf(r.FinishedAt),
	}
}

// listText is a list of strings as a column holds it: a JSON array, "[]"
// for none.
func listText(list []string) (string, error) {
	if list == nil {
		list = []string{}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// listOf is the list a column holds: nil for an empty one.
func listOf(text string) ([]string, error) {
	var list []string
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list, nil
}

// Services returns every service, by name.
func (d *DB) Services(ctx context.Context) ([]Service, error) {
	var out []Service
	err := d.read(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListServices(ctx)
		if err != nil {
			return err
		}
		out = make([]Service, len(rows))
		for i, r := range rows {
			out[i] = serviceOf(r)
		}
		return nil
	})
	return out, err
}

// Service returns ErrNotFound for a service that has no row.
func (d *DB) Service(ctx context.Context, name string) (Service, error) {
	var out Service
	err := d.read(ctx, func(q *dbq.Queries) error {
		r, err := q.ServiceByName(ctx, name)
		if err != nil {
			return notFound(err)
		}
		out = serviceOf(r)
		return nil
	})
	return out, err
}

// Generation returns ErrNotFound for one that is not there.
func (d *DB) Generation(ctx context.Context, service string, number uint64) (Generation, error) {
	var out Generation
	err := d.read(ctx, func(q *dbq.Queries) error {
		r, err := q.GenerationByNumber(ctx, dbq.GenerationByNumberParams{
			Service: service,
			Number:  int64(number),
		})
		if err != nil {
			return notFound(err)
		}
		out, err = generationOf(r)
		return err
	})
	return out, err
}

// NewRollout does in one transaction: makes the service's row if it is
// missing, records a generation with the next number, marks the rollout
// that is under way as superseded, and records a rollout that is
// preparing and targets the new generation. The counts of what it
// returns are zero.
func (d *DB) NewRollout(ctx context.Context, req wire.RolloutRequest, by wire.ID, now time.Time) (wire.RolloutInfo, error) {
	args, err := listText(req.Args)
	if err != nil {
		return wire.RolloutInfo{}, fmt.Errorf("db: args: %w", err)
	}
	closure, err := listText(req.Closure)
	if err != nil {
		return wire.RolloutInfo{}, fmt.Errorf("db: closure: %w", err)
	}
	var out wire.RolloutInfo
	err = d.write(ctx, func(q *dbq.Queries) error {
		if err := q.InsertService(ctx, dbq.InsertServiceParams{
			Name:      req.Service,
			CreatedAt: now.UnixNano(),
		}); err != nil {
			return err
		}
		last, err := q.LastGenerationNumber(ctx, req.Service)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// No row leaves last at 0, and the first generation is 1.
		number := last + 1
		if err := q.InsertGeneration(ctx, dbq.InsertGenerationParams{
			Service:   req.Service,
			Number:    number,
			Binary:    req.Binary,
			Args:      args,
			Closure:   closure,
			CreatedAt: now.UnixNano(),
			CreatedBy: string(by),
		}); err != nil {
			return err
		}
		out, err = supersedeAndInsert(ctx, q, req.Service, number, req.Parallel, by, now)
		return err
	})
	if err != nil {
		return wire.RolloutInfo{}, fmt.Errorf("db: new rollout of %s: %w", req.Service, err)
	}
	return out, nil
}

// NewRollback records a rollout that targets the stable generation, and
// supersedes the one under way. ErrNotFound: no such service.
// ErrNoStable. ErrNothingToRollBack: the last rollout completed.
func (d *DB) NewRollback(ctx context.Context, req wire.RollbackRequest, by wire.ID, now time.Time) (wire.RolloutInfo, error) {
	var out wire.RolloutInfo
	err := d.write(ctx, func(q *dbq.Queries) error {
		service, err := q.ServiceByName(ctx, req.Service)
		if err != nil {
			return notFound(err)
		}
		if service.Stable == 0 {
			return ErrNoStable
		}
		last, err := q.LastRollout(ctx, req.Service)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && wire.RolloutState(last.State) == wire.RolloutCompleted {
			return ErrNothingToRollBack
		}
		out, err = supersedeAndInsert(ctx, q, req.Service, service.Stable, req.Parallel, by, now)
		return err
	})
	if err != nil {
		return wire.RolloutInfo{}, fmt.Errorf("db: new rollback of %s: %w", req.Service, err)
	}
	return out, nil
}

// supersedeAndInsert ends the rollout of the service that is under way and
// records a new one, which is preparing.
func supersedeAndInsert(ctx context.Context, q *dbq.Queries, service string, generation int64, parallel int, by wire.ID, now time.Time) (wire.RolloutInfo, error) {
	if _, err := q.SupersedeUnderWay(ctx, dbq.SupersedeUnderWayParams{
		Service:    service,
		FinishedAt: now.UnixNano(),
	}); err != nil {
		return wire.RolloutInfo{}, err
	}
	r, err := q.InsertRollout(ctx, dbq.InsertRolloutParams{
		Service:    service,
		Generation: generation,
		Parallel:   int64(parallel),
		CreatedAt:  now.UnixNano(),
		CreatedBy:  string(by),
	})
	if err != nil {
		return wire.RolloutInfo{}, err
	}
	return rolloutOf(r), nil
}

// LastRollout returns the newest rollout of the service, and false when
// it never had one.
func (d *DB) LastRollout(ctx context.Context, service string) (wire.RolloutInfo, bool, error) {
	var out wire.RolloutInfo
	var found bool
	err := d.read(ctx, func(q *dbq.Queries) error {
		r, err := q.LastRollout(ctx, service)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out, found = rolloutOf(r), true
		return nil
	})
	return out, found, err
}

// Rollouts returns the newest limit rollouts of the service, newest first.
func (d *DB) Rollouts(ctx context.Context, service string, limit int) ([]wire.RolloutInfo, error) {
	var out []wire.RolloutInfo
	err := d.read(ctx, func(q *dbq.Queries) error {
		// A negative LIMIT means no limit at all to SQLite.
		rows, err := q.ListRollouts(ctx, dbq.ListRolloutsParams{
			Service: service,
			Max:     int64(max(limit, 0)),
		})
		if err != nil {
			return err
		}
		out = make([]wire.RolloutInfo, len(rows))
		for i, r := range rows {
			out[i] = rolloutOf(r)
		}
		return nil
	})
	return out, err
}

// SetRolloutState moves a rollout that is under way to state, with the
// reason. To completed and halted it also sets Finished, and to completed
// it makes the target the stable generation of the service. It returns
// false, and changes nothing, when the rollout is not under way any more.
// The states a rollout can be moved to are activating, completed and halted;
// any other is an error.
func (d *DB) SetRolloutState(ctx context.Context, id uint64, state wire.RolloutState, reason string, now time.Time) (bool, error) {
	var finished int64
	switch state {
	case wire.RolloutActivating:
	case wire.RolloutCompleted, wire.RolloutHalted:
		finished = now.UnixNano()
	default:
		return false, fmt.Errorf("db: a rollout cannot be moved to %q", state)
	}
	var moved bool
	err := d.write(ctx, func(q *dbq.Queries) error {
		n, err := q.SetRolloutState(ctx, dbq.SetRolloutStateParams{
			ID:         int64(id),
			State:      string(state),
			Reason:     reason,
			FinishedAt: finished,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		moved = true
		if state != wire.RolloutCompleted {
			return nil
		}
		r, err := q.RolloutByID(ctx, int64(id))
		if err != nil {
			return err
		}
		return q.SetStable(ctx, dbq.SetStableParams{Name: r.Service, Stable: r.Generation})
	})
	if err != nil {
		return false, fmt.Errorf("db: set rollout %d to %s: %w", id, state, err)
	}
	return moved, nil
}
```

- [ ] **Step 12: Run the tests to see them pass**

Run: `go test -race -count=3 ./db/`
Expected: PASS (`ok  	github.com/fables-for-robots/great-machine/db`)

Run: `test -z "$(gofmt -l .)" && go vet ./... && sqlc diff`
Expected: no output.

- [ ] **Step 13: Commit**

```bash
git add db/
git commit -m "DB: services, generations and rollouts"
```

- [ ] **Step 14: Hold `db/dbq` to its queries in CI**

In `.github/workflows/test.yml`, in the job `test`, add this step directly
after the step named `test` (every `run` step of that job runs in the
flake's `bare` shell, which has sqlc, so the step needs no setup):

```yaml
      # db/dbq is what sqlc makes of db/queries and db/migrations, and
      # nothing else: a query that was changed without `go generate ./db`
      # fails here.
      - name: sqlc
        run: sqlc diff
```

- [ ] **Step 15: See the step's command fail and pass**

```bash
printf '\n-- name: CountServices :one\nSELECT count(*) FROM services;\n' >> db/queries/services.sql
sqlc diff; echo "exit status $?"
```

Expected: a diff of `db/dbq/services.sql.go` that adds `CountServices`, and `exit status 1`.

```bash
git checkout db/queries/services.sql
sqlc diff; echo "exit status $?"
nix run nixpkgs#actionlint -- .github/workflows/test.yml
```

Expected: `exit status 0` and nothing else: no diff, and nothing from actionlint.

- [ ] **Step 16: Commit**

```bash
git add .github/workflows/test.yml
git commit -m "CI: sqlc diff"
```
