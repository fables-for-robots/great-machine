package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

const carol = wire.ID("ca40100000000000000000000000000000000000000000000000000000000000")

func TestGrantsArePutChangedAndRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draal.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := d.Grants(ctx); err != nil || len(got) != 0 {
		t.Fatalf("a new database has the grants %+v, %v; want none", got, err)
	}
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	put := []wire.Grant{
		{ID: bob, Role: wire.RoleZathras, Service: "web", Note: "the pods of web", By: alice, At: at},
		{ID: bob, Role: wire.RoleDeployer, Service: "web", By: alice, At: at.Add(time.Second)},
		{ID: bob, Role: wire.RoleDeployer, Service: "api", By: alice, At: at.Add(2 * time.Second)},
		{ID: carol, Role: wire.RoleDeployer, Note: "every service", By: alice, At: at.Add(3 * time.Second)},
		{ID: alice, Role: wire.RoleAdmin, By: alice, At: at.Add(4 * time.Second)},
	}
	for _, g := range put {
		if err := d.PutGrant(ctx, g); err != nil {
			t.Fatalf("PutGrant(%+v): %v", g, err)
		}
	}
	// By role, then by service, then by ID: the order they are listed in.
	want := []wire.Grant{put[4], put[3], put[2], put[1], put[0]}
	got, err := d.Grants(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Grants =\n%+v, %v; want\n%+v", got, err, want)
	}

	// A grant that is there is there with the new note, and is as old as
	// it was.
	again := put[0]
	again.Note, again.By, again.At = "renamed", carol, at.Add(time.Hour)
	if err := d.PutGrant(ctx, again); err != nil {
		t.Fatal(err)
	}
	got, _ = d.Grants(ctx)
	if len(got) != 5 || got[4].Note != "renamed" || got[4].By != alice || !got[4].At.Equal(at) {
		t.Errorf("after a grant was put again there are %d, the one %+v; want 5, with the new note and the old time and admin", len(got), got[4])
	}

	if err := d.DeleteGrant(ctx, bob, wire.RoleDeployer, "api"); err != nil {
		t.Fatalf("DeleteGrant: %v", err)
	}
	if err := d.DeleteGrant(ctx, bob, wire.RoleDeployer, "api"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting a grant that is not there: %v, want ErrNotFound", err)
	}
	// The grant of every service is another one than that of a service.
	if err := d.DeleteGrant(ctx, bob, wire.RoleDeployer, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting the grant of every service that the key has not: %v, want ErrNotFound", err)
	}

	// They are in the file.
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err = d.Grants(ctx)
	if err != nil || len(got) != 4 || got[3].Note != "renamed" {
		t.Errorf("after the database was opened again: %+v, %v; want the four that were left", got, err)
	}
}

// A database of before stage 6 gets the table of the grants, and keeps
// what it had.
func TestOpenAddsTheGrantsToADatabaseOfBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draal.sqlite")
	first, err := embedded.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"PRAGMA journal_mode = WAL",
		string(first),
		"INSERT INTO services (name, stable, created_at) VALUES ('web', 3, 7)",
		"PRAGMA user_version = 1",
	} {
		if _, err := old.Exec(stmt); err != nil {
			t.Fatalf("making a database of before: %v", err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := Open(path)
	if err != nil {
		t.Fatalf("opening a database of before: %v", err)
	}
	defer d.Close()
	if got, err := d.Grants(ctx); err != nil || len(got) != 0 {
		t.Errorf("the grants of a database of before: %+v, %v; want none", got, err)
	}
	if s, err := d.Service(ctx, "web"); err != nil || s.Stable != 3 {
		t.Errorf("the service of before: %+v, %v", s, err)
	}
	var version int
	if err := d.writer.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Errorf("user_version = %d, %v; want 2", version, err)
	}
}
