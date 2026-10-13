package cli

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

func TestAccessList(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	f := &fakeDraal{grants: []wire.Grant{
		{ID: wire.ID(idA), Role: wire.RoleAdmin, Fixed: true},
		{ID: wire.ID(idB), Role: wire.RoleDeployer, Note: "the laptop of a colleague", By: wire.ID(idA), At: now.Add(-49 * time.Hour)},
		{ID: wire.ID(idB), Role: wire.RoleDeployer, Service: "web", Note: "ci", By: wire.ID(idA), At: now.Add(-90 * time.Second)},
		{ID: wire.ID(idC), Role: wire.RoleZathras, Service: "web", By: wire.ID(idA), At: now.Add(-3 * time.Minute)},
	}}
	var out bytes.Buffer
	if err := runAccessList(context.Background(), f.start(t), &out, now); err != nil {
		t.Fatalf("runAccessList: %v", err)
	}
	want := strings.Join([]string{
		"ROLE      SERVICE  ID                                                                NOTE                       GRANTED BY        WHEN",
		"admin     every    " + idA + "  -                          the command line  -",
		"deployer  every    " + idB + "  the laptop of a colleague  a1a1a1a1          2d ago",
		"deployer  web      " + idB + "  ci                         a1a1a1a1          1m ago",
		"zathras   web      " + idC + "  -                          a1a1a1a1          3m ago",
		"",
	}, "\n")
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}

	// Nobody was granted anything: a draal with no admin at all.
	out.Reset()
	if err := runAccessList(context.Background(), (&fakeDraal{}).start(t), &out, now); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "no grants: draal was given no admin, and nobody was granted anything\n"; got != want {
		t.Errorf("an empty list: got %q, want %q", got, want)
	}
}

func TestAccessGrantAndRevoke(t *testing.T) {
	f := &fakeDraal{}
	client := f.start(t)
	var out bytes.Buffer
	grant := wire.GrantRequest{ID: wire.ID(idB), Role: wire.RoleDeployer, Service: "web", Note: "ci"}
	if err := runAccessGrant(context.Background(), client, grant, &out); err != nil {
		t.Fatalf("runAccessGrant: %v", err)
	}
	if got, want := out.String(), "granted: "+idB+" is a deployer of web\n"; got != want {
		t.Errorf("the grant says %q, want %q", got, want)
	}
	out.Reset()
	if err := runAccessGrant(context.Background(), client, wire.GrantRequest{ID: wire.ID(idB), Role: wire.RoleAdmin}, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "granted: "+idB+" is an admin\n"; got != want {
		t.Errorf("the grant of an admin says %q, want %q", got, want)
	}
	out.Reset()
	revoke := wire.RevokeRequest{ID: wire.ID(idB), Role: wire.RoleZathras}
	if err := runAccessRevoke(context.Background(), client, revoke, &out); err != nil {
		t.Fatalf("runAccessRevoke: %v", err)
	}
	if got, want := out.String(), "revoked: "+idB+" is no zathras of every service any more, and what it was let in for is ended\n"; got != want {
		t.Errorf("the revocation says %q, want %q", got, want)
	}
	asked := f.asked()
	if len(asked) != 3 || asked[0].Grant == nil || *asked[0].Grant != grant || asked[2].Revoke == nil || *asked[2].Revoke != revoke {
		t.Errorf("draal was asked %+v, want the grant, the grant of an admin and the revocation", asked)
	}

	// What draal refuses is the error of the command, and nothing is said
	// to have been done.
	refusing := &fakeDraal{refuse: wire.Errorf(wire.CodeUnauthorized, "%s is no admin and may not manage the grants", idC)}
	out.Reset()
	err := runAccessGrant(context.Background(), refusing.start(t), grant, &out)
	if wire.CodeOf(err) != wire.CodeUnauthorized || out.Len() != 0 {
		t.Errorf("a grant that draal refuses: %v, and %q was printed; want the refusal and nothing", err, out.String())
	}
	if err := runAccessRevoke(context.Background(), refusing.start(t), revoke, &out); wire.CodeOf(err) != wire.CodeUnauthorized || out.Len() != 0 {
		t.Errorf("a revocation that draal refuses: %v, and %q was printed", err, out.String())
	}
}

func TestAccessCommandLines(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"no role", []string{"access", "grant", "--draal", idA}, "grant: ROLE is missing; usage: great-machine access grant"},
		{"no ID", []string{"access", "grant", "--draal", idA, "deployer"}, "grant: ID is missing"},
		{"a role that is none", []string{"access", "grant", "--draal", idA, "operator", idB}, `"operator" is no role: there are admin, deployer and zathras`},
		{"an ID that is none", []string{"access", "grant", "--draal", idA, "deployer", "ci"}, `"ci" is not an endpoint ID`},
		{"an admin of a service", []string{"access", "grant", "--draal", idA, "--service", "web", "admin", idB}, "an admin is of every service: --service is for a deployer and a zathras"},
		{"a service that is none", []string{"access", "revoke", "--draal", idA, "--service", "Web", "deployer", idB}, `"Web" is not the name of a service`},
		{"an argument too many", []string{"access", "revoke", "--draal", idA, "deployer", idB, "web"}, `revoke: unexpected argument "web"`},
		{"a flag behind the arguments", []string{"access", "grant", "--draal", idA, "deployer", idB, "--service", "web"}, "the flag --service has to come before ROLE"},
		{"list with an argument", []string{"access", "list", "--draal", idA, "all"}, `list takes no argument, got "all"`},
		{"no draal", []string{"access", "list"}, "--draal is required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cleanEnvironment(t)
			var stdout, stderr bytes.Buffer
			got := run(context.Background(), newApp(&stdout, &stderr), append([]string{"great-machine"}, tt.args...))
			if got != 2 || !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("the exit status is %d and stderr %q, want 2 and %q", got, stderr.String(), tt.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout is %q, want nothing", stdout.String())
			}
		})
	}
}

// The admins of draal's command line are given with --admin, or with the
// name the flag had, and with either of their variables.
func TestDraalAdminsAndTheNameTheFlagHad(t *testing.T) {
	cleanEnvironment(t)
	var got draalConfig
	reader := func(c *cli.Context) error {
		var err error
		got, err = readDraal(c)
		return err
	}
	base := []string{"--data", "data", "--s3-bucket", "packs"}
	read(t, draalCommand(), reader, append(base, "--admin", idA, "--operator", idB, "--admin", idC)...)
	if want := []wire.ID{wire.ID(idA), wire.ID(idC), wire.ID(idB)}; !slices.Equal(got.admins, want) {
		t.Errorf("--admin and --operator together: got %v, want %v", got.admins, want)
	}
	t.Setenv("GREAT_MACHINE_OPERATORS", idB)
	read(t, draalCommand(), reader, base...)
	if want := []wire.ID{wire.ID(idB)}; !slices.Equal(got.admins, want) {
		t.Errorf("the variable the flag had: got %v, want %v", got.admins, want)
	}
	t.Setenv("GREAT_MACHINE_ADMINS", idA+","+idC)
	read(t, draalCommand(), reader, base...)
	if want := []wire.ID{wire.ID(idA), wire.ID(idC), wire.ID(idB)}; !slices.Equal(got.admins, want) {
		t.Errorf("both variables: got %v, want the admins of both, %v", got.admins, want)
	}
}
