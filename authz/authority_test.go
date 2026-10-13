package authz

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

func id(c byte) wire.ID { return wire.ID(strings.Repeat(string(c), 64)) }

var (
	fixedAdmin = id('a') // an admin of the command line
	admin      = id('b') // an admin by a grant
	depWeb     = id('c') // a deployer of web
	depAll     = id('d') // a deployer of every service
	depBoth    = id('e') // a deployer of web and of api, by two grants
	roleWeb    = id('f') // the role key of web: it holds zathras for web
	roleAll    = id('1') // a role key of every service
	zWeb       = id('2') // a zathras that is registered for web
	zAPI       = id('3') // a zathras that is registered for api
	nobody     = id('4')
)

func theGrants() []wire.Grant {
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	return []wire.Grant{
		{ID: admin, Role: wire.RoleAdmin, By: fixedAdmin, At: at},
		{ID: depAll, Role: wire.RoleDeployer, By: fixedAdmin, At: at},
		{ID: depBoth, Role: wire.RoleDeployer, Service: "api", By: fixedAdmin, At: at},
		{ID: depBoth, Role: wire.RoleDeployer, Service: "web", By: fixedAdmin, At: at},
		{ID: depWeb, Role: wire.RoleDeployer, Service: "web", Note: "ci", By: fixedAdmin, At: at},
		{ID: roleAll, Role: wire.RoleZathras, By: fixedAdmin, At: at},
		{ID: roleWeb, Role: wire.RoleZathras, Service: "web", By: fixedAdmin, At: at},
	}
}

func anAuthority() *Authority {
	return New(Config{
		Admins: []wire.ID{fixedAdmin},
		Registered: func(peer wire.ID) (string, bool) {
			switch peer {
			case zWeb:
				return "web", true
			case zAPI:
				return "api", true
			}
			return "", false
		},
	}, theGrants())
}

// who lists the keys that may, of all there are.
func who(a *Authority, op Operation, service string) []wire.ID {
	var may []wire.ID
	for _, peer := range []wire.ID{fixedAdmin, admin, depWeb, depAll, depBoth, roleWeb, roleAll, zWeb, zAPI, nobody} {
		if a.Authorize(peer, op, service) == nil {
			may = append(may, peer)
		}
	}
	return may
}

// Every row and every column of the table of the design: who may do
// what, for web, for api and for no service in particular.
func TestWhoMayDoWhat(t *testing.T) {
	a := anAuthority()
	admins := []wire.ID{fixedAdmin, admin}
	ofWeb := []wire.ID{fixedAdmin, admin, depWeb, depAll, depBoth}
	ofAPI := []wire.ID{fixedAdmin, admin, depAll, depBoth}
	ofEvery := []wire.ID{fixedAdmin, admin, depAll}
	deployers := []wire.ID{fixedAdmin, admin, depWeb, depAll, depBoth}
	everybody := []wire.ID{fixedAdmin, admin, depWeb, depAll, depBoth, roleWeb, roleAll, zWeb, zAPI, nobody}

	// What a deployer of the service may.
	for _, op := range []Operation{OpStore, OpRollout, OpRollback, OpRollouts, OpHistory, OpLogs, OpShell, OpStorePush, OpStoreList} {
		for service, want := range map[string][]wire.ID{"web": ofWeb, "api": ofAPI, "": ofEvery} {
			if got := who(a, op, service); !slices.Equal(got, want) {
				t.Errorf("%s of %q is granted to %v, want %v", op, service, got, want)
			}
		}
	}
	// A pull is a deployer's too, and a zathras's that is registered for
	// the service; a role key pulls nothing.
	for service, want := range map[string][]wire.ID{
		"web": append(slices.Clone(ofWeb), zWeb),
		"api": append(slices.Clone(ofAPI), zAPI),
		"":    ofEvery,
	} {
		if got := who(a, OpStorePull, service); !slices.Equal(got, want) {
			t.Errorf("a pull of %q is granted to %v, want %v", service, got, want)
		}
	}
	// What whoever deploys anything may: the fleet is cut to what it may
	// see, and a commit is of an upload the store knows the owner of.
	for _, op := range []Operation{OpWatch, OpStoreCommit} {
		for _, service := range []string{"", "web"} {
			if got := who(a, op, service); !slices.Equal(got, deployers) {
				t.Errorf("%s is granted to %v, want %v", op, got, deployers)
			}
		}
	}
	// What an admin alone may.
	for _, op := range []Operation{OpStoreDelete, OpAccess, OpStorePage} {
		for _, service := range []string{"", "web"} {
			if got := who(a, op, service); !slices.Equal(got, admins) {
				t.Errorf("%s of %q is granted to %v, want the admins", op, service, got)
			}
		}
	}
	// Who it is, everybody may ask.
	if got := who(a, OpMe, ""); !slices.Equal(got, everybody) {
		t.Errorf("me is granted to %v, want everybody", got)
	}
	// And what there is no such operation of, nobody may.
	if got := who(a, Operation("format"), "web"); len(got) != 0 {
		t.Errorf("an operation there is none of is granted to %v", got)
	}
}

func TestARefusalSaysWhichRoleItTakes(t *testing.T) {
	a := anAuthority()
	for _, c := range []struct {
		peer    wire.ID
		op      Operation
		service string
		want    string
	}{
		{depWeb, OpRollout, "api", string(depWeb) + " is no deployer of api and may not roll it out"},
		{depWeb, OpShell, "api", string(depWeb) + " is no deployer of api and may not open a shell in it"},
		{depWeb, OpStoreList, "", string(depWeb) + " is no deployer of every service and may not list the store"},
		{nobody, OpWatch, "", string(nobody) + " is no deployer of any service and may not watch the fleet"},
		{depAll, OpAccess, "", string(depAll) + " is no admin and may not manage the grants"},
		{depAll, OpStoreDelete, "web", string(depAll) + " is no admin and may not delete from the store"},
		{roleWeb, OpStorePull, "web", string(roleWeb) + " is no deployer of web and may not pull its store paths"},
	} {
		err := a.Authorize(c.peer, c.op, c.service)
		if wire.CodeOf(err) != wire.CodeUnauthorized {
			t.Errorf("%s of %q for %s: %v, want unauthorized", c.op, c.service, c.peer, err)
			continue
		}
		if got := err.(*wire.Error).Message; got != c.want {
			t.Errorf("the refusal reads %q, want %q", got, c.want)
		}
	}
}

func TestTheSignerOfAStatement(t *testing.T) {
	a := anAuthority()
	for _, c := range []struct {
		by      wire.ID
		service string
		ok      bool
	}{
		{roleWeb, "web", true},
		{roleWeb, "api", false},
		{roleAll, "web", true},
		{roleAll, "api", true},
		{depAll, "web", false},     // a deployer signs for nobody
		{fixedAdmin, "web", false}, // and neither does an admin
		{nobody, "web", false},
	} {
		err := a.Signer(c.by, c.service)
		if (err == nil) != c.ok {
			t.Errorf("Signer(%s, %s) = %v, want ok %v", c.by, c.service, err, c.ok)
		}
		if err != nil {
			if wire.CodeOf(err) != wire.CodeUnauthorized {
				t.Errorf("Signer(%s, %s) = %v, want unauthorized", c.by, c.service, err)
			}
			want := "great-machine access grant zathras " + string(c.by) + " --service " + c.service
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q does not say how it is mended: %s", err, want)
			}
		}
	}
}

func TestWhatAKeySees(t *testing.T) {
	a := anAuthority()
	for _, c := range []struct {
		peer     wire.ID
		every    bool
		services []string
	}{
		{fixedAdmin, true, nil},
		{admin, true, nil},
		{depAll, true, nil},
		{depWeb, false, []string{"web"}},
		{depBoth, false, []string{"api", "web"}},
		{roleWeb, false, nil}, // a role key sees nothing
		{nobody, false, nil},
	} {
		every, services := a.Sees(c.peer)
		if every != c.every || !slices.Equal(services, c.services) {
			t.Errorf("Sees(%s) = %v, %v; want %v, %v", c.peer, every, services, c.every, c.services)
		}
	}
}

func TestWhatAKeyHoldsAndAllThereIs(t *testing.T) {
	a := anAuthority()
	if got := a.Holds(fixedAdmin); len(got) != 1 || got[0].Role != wire.RoleAdmin || !got[0].Fixed || got[0].ID != fixedAdmin {
		t.Errorf("the admin of the command line holds %+v, want one admin grant that is fixed", got)
	}
	if got := a.Holds(depBoth); len(got) != 2 || got[0].Service != "api" || got[1].Service != "web" {
		t.Errorf("the deployer of two services holds %+v", got)
	}
	if got := a.Holds(nobody); len(got) != 0 {
		t.Errorf("a key with no grant holds %+v", got)
	}
	all := a.All()
	if len(all) != 1+len(theGrants()) || !all[0].Fixed || all[0].ID != fixedAdmin {
		t.Fatalf("All = %+v, want the admin of the command line and then the %d grants", all, len(theGrants()))
	}
	for i, g := range theGrants() {
		if all[i+1] != g {
			t.Errorf("All[%d] = %+v, want %+v", i+1, all[i+1], g)
		}
	}
	if !a.Fixed(fixedAdmin) || a.Fixed(admin) {
		t.Error("Fixed does not tell the admin of the command line from one by a grant")
	}
}

// A key that is an admin of the command line and has grants besides is
// listed with both, and is an admin whatever the grants are.
func TestAnAdminOfTheCommandLineWithGrants(t *testing.T) {
	a := New(Config{Admins: []wire.ID{fixedAdmin, "", fixedAdmin}}, []wire.Grant{{ID: fixedAdmin, Role: wire.RoleDeployer, Service: "web"}})
	if err := a.Authorize(fixedAdmin, OpAccess, ""); err != nil {
		t.Errorf("the admin of the command line: %v", err)
	}
	if got := a.Holds(fixedAdmin); len(got) != 2 || !got[0].Fixed {
		t.Errorf("it holds %+v, want its fixed grant and the one of the table", got)
	}
	// An empty ID among the admins is no admin, and one that is given
	// twice is one admin.
	if err := a.Authorize("", OpAccess, ""); err == nil {
		t.Error("a peer with no ID is an admin")
	}
	if all := a.All(); len(all) != 2 {
		t.Errorf("All = %+v, want one admin of the command line and one grant", all)
	}
}

func TestReplaceIsSeenByTheNextQuestion(t *testing.T) {
	a := anAuthority()
	if err := a.Authorize(depWeb, OpRollout, "web"); err != nil {
		t.Fatalf("before: %v", err)
	}
	if !a.Admins() {
		t.Error("an authority with admins says it has none")
	}
	a.Replace(nil)
	if err := a.Authorize(depWeb, OpRollout, "web"); err == nil {
		t.Error("a grant that is gone is still granted")
	}
	if err := a.Authorize(fixedAdmin, OpRollout, "web"); err != nil {
		t.Errorf("the admin of the command line, with no grant in the table: %v", err)
	}
	a.Replace([]wire.Grant{{ID: nobody, Role: wire.RoleDeployer, Service: "web"}})
	if err := a.Authorize(nobody, OpRollout, "web"); err != nil {
		t.Errorf("a grant that is new: %v", err)
	}
	if none := New(Config{}, nil); none.Admins() {
		t.Error("an authority with no admin of the command line and none in the table says it has one")
	}
}

// Who is registered may be said later: the store is opened before there
// is a fleet to ask.
func TestRegisteredIsSetLater(t *testing.T) {
	a := New(Config{}, nil)
	if err := a.Authorize(zWeb, OpStorePull, "web"); err == nil {
		t.Fatal("a pull is granted with nobody to ask who is registered")
	}
	a.SetRegistered(func(peer wire.ID) (string, bool) { return "web", peer == zWeb })
	if err := a.Authorize(zWeb, OpStorePull, "web"); err != nil {
		t.Errorf("a zathras that is registered: %v", err)
	}
	if err := a.Authorize(zWeb, OpStorePull, "api"); err == nil {
		t.Error("a zathras of web pulls a store path of api")
	}
	if err := a.Authorize(zWeb, OpStorePull, ""); err == nil {
		t.Error("a zathras of web pulls what is of no service")
	}
}

// Any says whether a key may ask a thing of some service at all: it is
// what draal asks before it tells a key that a zathras is not there.
func TestMayAKeyAskOfAnyService(t *testing.T) {
	a := anAuthority()
	for _, peer := range []wire.ID{fixedAdmin, admin, depWeb, depAll, depBoth} {
		if err := a.Any(peer, OpLogs); err != nil {
			t.Errorf("Any(%s, logs) = %v", peer, err)
		}
	}
	for _, peer := range []wire.ID{roleWeb, zWeb, nobody} {
		err := a.Any(peer, OpShell)
		if wire.CodeOf(err) != wire.CodeUnauthorized {
			t.Fatalf("Any(%s, shell) = %v, want unauthorized", peer, err)
		}
		if want := string(peer) + " is no deployer of any service and may not open a shell"; err.(*wire.Error).Message != want {
			t.Errorf("the refusal reads %q, want %q", err.(*wire.Error).Message, want)
		}
	}
	if err := a.Any(depAll, OpAccess); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Any(a deployer, access) = %v, want unauthorized", err)
	}
	if err := a.Any(nobody, OpMe); err != nil {
		t.Errorf("Any(nobody, me) = %v", err)
	}
}
