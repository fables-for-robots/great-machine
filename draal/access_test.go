package draal

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

// The keys of these tests, beside the operator, who is an admin of the
// command line, and the stranger, who holds nothing.
//
// They are the IDs of keys there are: a grant is of an endpoint ID, and
// not every 64 hexadecimal characters are one.
var (
	deployerOfWeb   = aKey().ID()
	deployerOfEvery = aKey().ID()
	grantedAdmin    = aKey().ID()
	newcomer        = aKey().ID()
	// commandAdmin is an admin of the command line beside the operator of
	// the tests, whose ID is none of a key.
	commandAdmin = aKey().ID()
)

func aKey() node.Key {
	k, err := node.NewKey()
	if err != nil {
		panic(err)
	}
	return k
}

// granted returns a Config whose database and authority hold the role
// key of the tests and the grants given, as a draal has them that was
// started on its database.
func granted(t *testing.T, grants ...wire.Grant) Config {
	t.Helper()
	cfg := testConfig(t)
	all := append([]wire.Grant{{ID: theRole().ID(), Role: wire.RoleZathras, By: operatorID, At: time.Unix(1, 0).UTC()}}, grants...)
	for _, g := range all {
		if g.By == "" {
			g.By, g.At = operatorID, time.Unix(2, 0).UTC()
		}
		if err := cfg.DB.PutGrant(context.Background(), g); err != nil {
			t.Fatalf("PutGrant: %v", err)
		}
	}
	held, err := cfg.DB.Grants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Authz = authz.New(authz.Config{Admins: []wire.ID{operatorID, commandAdmin}}, held)
	return cfg
}

// theUsual are a deployer of web, a deployer of every service and an
// admin by a grant.
func theUsual() []wire.Grant {
	return []wire.Grant{
		{ID: deployerOfWeb, Role: wire.RoleDeployer, Service: "web", Note: "ci"},
		{ID: deployerOfEvery, Role: wire.RoleDeployer},
		{ID: grantedAdmin, Role: wire.RoleAdmin},
	}
}

// register asks to be registered as a zathras of the service with the
// statement given, and returns what draal answered.
func register(t *testing.T, conn node.Conn, service string, st *wire.Statement) error {
	t.Helper()
	_, resp, err := ask(t, conn, &wire.Request{Register: &wire.Register{Service: service, Name: "pod-1", Version: "test", State: idle(), Statement: st}})
	if err == nil && resp.Registered == nil {
		t.Fatalf("register answered %+v", resp)
	}
	return err
}

// A zathras registers with what a role key of its service signed for it,
// at this draal: every other registration is refused, in words that say
// what is wrong.
func TestARegistrationNeedsAStatementOfARoleKeyOfTheService(t *testing.T) {
	onlyWeb, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	ungranted, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg := granted(t, wire.Grant{ID: onlyWeb.ID(), Role: wire.RoleZathras, Service: "web"})
	w := startWorld(t, cfg)
	me, other := nodetest.ID(40), nodetest.ID(41)

	for _, c := range []struct {
		what    string
		service string
		st      *wire.Statement
		says    []string
	}{
		{"no statement", "web", nil, []string{"statement", "--role-key", "v0.4.0"}},
		{"a statement for another zathras", "web", statementOf(theRole(), cfg.ID, other), []string{"is not what the role key", string(theRole().ID())}},
		{"a statement for another draal", "web", statementOf(theRole(), nodetest.ID(98), me), []string{"is not what the role key"}},
		{"a signature that is cut", "web", &wire.Statement{By: theRole().ID(), Signature: statementOf(theRole(), cfg.ID, me).Signature[:63]}, []string{"is not what the role key"}},
		{"a role key that is none", "web", &wire.Statement{By: "nobody", Signature: make([]byte, 64)}, []string{"names no role key"}},
		{"a role key that was granted nothing", "web", statementOf(ungranted, cfg.ID, me),
			[]string{string(ungranted.ID()) + " is no zathras of web", "great-machine access grant zathras " + string(ungranted.ID()) + " --service web"}},
		{"a role key of another service", "api", statementOf(onlyWeb, cfg.ID, me),
			[]string{string(onlyWeb.ID()) + " is no zathras of api", "--service api"}},
	} {
		err := register(t, w.dial(me), c.service, c.st)
		if wire.CodeOf(err) != wire.CodeUnauthorized {
			t.Errorf("register with %s = %v, want unauthorized", c.what, err)
			continue
		}
		for _, word := range c.says {
			if !strings.Contains(err.Error(), word) {
				t.Errorf("the refusal of %s does not say %q: %v", c.what, word, err)
			}
		}
	}
	if f := w.fleetNow(); len(f.Services) != 0 {
		t.Fatalf("the fleet has %+v after refused registrations", f.Services)
	}

	// What is well-formed is looked at first: a name that is none is a
	// bad request, with a statement or without.
	if _, _, err := ask(t, w.dial(me), &wire.Request{Register: &wire.Register{Service: "Not A Name", Name: "x", State: idle()}}); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Errorf("a registration that is not well-formed and has no statement = %v, want a bad-request", err)
	}

	// The role key of web signs for web, and the one of every service for
	// any; draal keeps who signed.
	if err := register(t, w.dial(me), "web", statementOf(onlyWeb, cfg.ID, me)); err != nil {
		t.Fatalf("register with the role key of web: %v", err)
	}
	if err := register(t, w.dial(other), "api", statementOf(theRole(), cfg.ID, other)); err != nil {
		t.Fatalf("register with the role key of every service: %v", err)
	}
	eventually(t, "both to be in the fleet", func() bool { return len(w.fleetNow().Services) == 2 })
	if sess := w.d.session(me); sess == nil || sess.by != onlyWeb.ID() {
		t.Errorf("draal has the session %+v of the zathras, want one that the role key of web signed for", sess)
	}
}

// What a key may ask is what it was granted, for the service it was
// granted it for.
func TestAnOperatorAsksWhatItWasGranted(t *testing.T) {
	cfg := granted(t, theUsual()...)
	cfg.Settle = time.Hour
	w := startWorld(t, cfg)
	web := w.zathras(10, "web", "web-1", idle())
	api := w.zathras(11, "api", "api-1", idle())

	// refused says whether draal refuses the request for want of a grant.
	refused := func(peer wire.ID, req *wire.Request) error {
		t.Helper()
		_, _, err := ask(t, w.dial(peer), req)
		if wire.CodeOf(err) == wire.CodeUnauthorized {
			return err
		}
		return nil
	}
	of := func(service string, zathras wire.ID) map[string]*wire.Request {
		rollout := webRollout(1)
		rollout.Service = service
		return map[string]*wire.Request{
			"store":    {Store: &wire.StoreRequest{Service: service}},
			"rollout":  {Rollout: &rollout},
			"rollback": {Rollback: &wire.RollbackRequest{Service: service}},
			"rollouts": {Rollouts: &wire.RolloutsRequest{Service: service}},
			"history":  {History: &wire.HistoryRequest{Zathras: zathras}},
			"logs":     {Logs: &wire.LogsRequest{Zathras: zathras}},
			"shell":    {Shell: &wire.ShellRequest{Zathras: zathras, Cols: 80, Rows: 24}},
		}
	}
	for _, peer := range []wire.ID{operatorID, grantedAdmin, deployerOfEvery} {
		for _, reqs := range []map[string]*wire.Request{of("web", web.id), of("api", api.id)} {
			for name, req := range reqs {
				if err := refused(peer, req); err != nil {
					t.Errorf("%s as %s: %v, want it granted", name, peer, err)
				}
			}
		}
	}
	for name, req := range of("web", web.id) {
		if err := refused(deployerOfWeb, req); err != nil {
			t.Errorf("%s of web as its deployer: %v, want it granted", name, err)
		}
	}
	// Of another service a deployer is refused what names the service, in
	// words that say which role it takes.
	for _, name := range []string{"store", "rollout", "rollback", "rollouts"} {
		err := refused(deployerOfWeb, of("api", api.id)[name])
		if err == nil {
			t.Errorf("%s of api as the deployer of web was granted", name)
			continue
		}
		if !strings.Contains(err.Error(), string(deployerOfWeb)+" is no deployer of api and may not ") {
			t.Errorf("the refusal of %s reads %q, want that it is no deployer of api", name, err)
		}
	}
	// Three keys rolled api out, and the one that was refused left no
	// fourth.
	if list, err := w.d.Rollouts(context.Background(), "api"); err != nil || len(list) != 3 {
		t.Errorf("api has %d rollouts (%v), want the 3 that were granted: one that is refused is not recorded", len(list), err)
	}

	// And of a zathras of another service it learns nothing: the answer
	// is that of a zathras that is not connected.
	for _, name := range []string{"history", "logs", "shell"} {
		_, _, errAPI := ask(t, w.dial(deployerOfWeb), of("api", api.id)[name])
		_, _, errGone := ask(t, w.dial(deployerOfWeb), of("api", nodetest.ID(77))[name])
		if wire.CodeOf(errAPI) != wire.CodeUnavailable || wire.CodeOf(errGone) != wire.CodeUnavailable {
			t.Errorf("%s of a zathras of api and of one that is not there, as the deployer of web: %v and %v; want unavailable twice", name, errAPI, errGone)
			continue
		}
		there := strings.ReplaceAll(errAPI.Error(), string(api.id), "ID")
		gone := strings.ReplaceAll(errGone.Error(), string(nodetest.ID(77)), "ID")
		if there != gone || strings.Contains(errAPI.Error(), "api") {
			t.Errorf("%s: the answers differ, or name the service: %q and %q", name, errAPI, errGone)
		}
	}

	// A key that holds nothing is refused everything but who it is, and
	// is told nothing of what there is.
	for name, req := range of("web", web.id) {
		if refused(strangerID, req) == nil {
			t.Errorf("%s as a stranger was not refused", name)
		}
	}
	if refused(strangerID, &wire.Request{Watch: &wire.WatchRequest{}}) == nil {
		t.Error("a stranger watches the fleet")
	}
	_, _, errThere := ask(t, w.dial(strangerID), of("web", web.id)["logs"])
	_, _, errGone := ask(t, w.dial(strangerID), of("web", nodetest.ID(77))["logs"])
	if errThere == nil || errGone == nil || errThere.Error() != errGone.Error() || strings.Contains(errThere.Error(), "web") {
		t.Errorf("the refusals of a stranger about a zathras differ, or name its service: %q and %q", errThere, errGone)
	}
}

// watch returns the first fleet a key is sent.
func (w *world) watch(peer wire.ID) (*wire.Fleet, error) {
	w.t.Helper()
	_, resp, err := ask(w.t, w.dial(peer), &wire.Request{Watch: &wire.WatchRequest{}})
	if err != nil {
		return nil, err
	}
	return resp.Fleet, nil
}

func names(f *wire.Fleet) string {
	var out []string
	for _, s := range f.Services {
		out = append(out, s.Name)
	}
	return strings.Join(out, " ")
}

// A key watches the services it may see, and no other.
func TestAKeyWatchesTheServicesItMaySee(t *testing.T) {
	cfg := granted(t, theUsual()...)
	cfg.Settle = time.Hour
	w := startWorld(t, cfg)
	w.zathras(10, "web", "web-1", idle())
	w.zathras(11, "api", "api-1", idle())
	eventually(t, "both services in the fleet", func() bool { return len(w.fleetNow().Services) == 2 })

	for peer, want := range map[wire.ID]string{
		operatorID:      "api web",
		grantedAdmin:    "api web",
		deployerOfEvery: "api web",
		deployerOfWeb:   "web",
	} {
		f, err := w.watch(peer)
		if err != nil || names(f) != want {
			t.Errorf("%s watches %q (%v), want %q", peer, names(f), err, want)
		}
	}
	if _, err := w.watch(strangerID); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("a stranger's watch = %v, want unauthorized", err)
	}
}

func TestWhoAKeyIs(t *testing.T) {
	w := startWorld(t, granted(t, theUsual()...))
	for peer, want := range map[wire.ID][]wire.Grant{
		strangerID:    nil,
		deployerOfWeb: {{ID: deployerOfWeb, Role: wire.RoleDeployer, Service: "web", Note: "ci", By: operatorID, At: time.Unix(2, 0).UTC()}},
		operatorID:    {{ID: operatorID, Role: wire.RoleAdmin, Fixed: true}},
	} {
		_, resp, err := ask(t, w.dial(peer), &wire.Request{Me: &wire.MeRequest{}})
		if err != nil || resp.Me == nil || resp.Me.ID != peer {
			t.Fatalf("me as %s = %+v, %v", peer, resp, err)
		}
		if len(resp.Me.Grants) != len(want) {
			t.Errorf("%s holds %+v, want %+v", peer, resp.Me.Grants, want)
			continue
		}
		for i, g := range want {
			if got := resp.Me.Grants[i]; got.ID != g.ID || got.Role != g.Role || got.Service != g.Service || got.Note != g.Note || got.Fixed != g.Fixed || got.By != g.By || !got.At.Equal(g.At) {
				t.Errorf("%s holds %+v, want %+v", peer, got, g)
			}
		}
	}
}

// grants asks for the grants as the key, and returns them.
func (w *world) grants(peer wire.ID) ([]wire.Grant, error) {
	w.t.Helper()
	_, resp, err := ask(w.t, w.dial(peer), &wire.Request{Grants: &wire.GrantsRequest{}})
	if err != nil {
		return nil, err
	}
	if resp.Grants == nil {
		w.t.Fatalf("grants answered %+v", resp)
	}
	return resp.Grants.Grants, nil
}

func holds(grants []wire.Grant, id wire.ID, role wire.Role, service string) *wire.Grant {
	for _, g := range grants {
		if g.ID == id && g.Role == role && g.Service == service {
			return &g
		}
	}
	return nil
}

// An admin grants and revokes, the grants are in the database, and the
// next request is decided by them.
func TestAnAdminGrantsAndRevokes(t *testing.T) {
	cfg := granted(t, theUsual()...)
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	cfg.Now = func() time.Time { return at }
	w := startWorld(t, cfg)
	admin := w.dial(grantedAdmin)

	// Nobody but an admin asks.
	for _, peer := range []wire.ID{deployerOfEvery, deployerOfWeb, strangerID} {
		for name, req := range map[string]*wire.Request{
			"grants": {Grants: &wire.GrantsRequest{}},
			"grant":  {Grant: &wire.GrantRequest{ID: peer, Role: wire.RoleAdmin}},
			"revoke": {Revoke: &wire.RevokeRequest{ID: grantedAdmin, Role: wire.RoleAdmin}},
		} {
			if _, _, err := ask(t, w.dial(peer), req); wire.CodeOf(err) != wire.CodeUnauthorized || !strings.Contains(err.Error(), "is no admin and may not manage the grants") {
				t.Errorf("%s as %s = %v, want it refused for being no admin", name, peer, err)
			}
		}
	}
	list, err := w.grants(grantedAdmin)
	if err != nil || len(list) != 6 || !list[0].Fixed || list[0].ID != operatorID || !list[1].Fixed || list[2].Fixed {
		t.Fatalf("the grants: %+v, %v; want the two admins of the command line and then the four of the table", list, err)
	}

	// A grant: the newcomer may not, is granted, and may.
	if _, _, err := ask(t, w.dial(newcomer), &wire.Request{Rollouts: &wire.RolloutsRequest{Service: "web"}}); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Fatalf("the newcomer before its grant: %v, want unauthorized", err)
	}
	_, resp, err := ask(t, admin, &wire.Request{Grant: &wire.GrantRequest{ID: wire.ID(strings.ToUpper(string(newcomer))), Role: wire.RoleDeployer, Service: "web", Note: "a\x1b[31mnote\n" + strings.Repeat("x", 300)}})
	if err != nil || resp.Granted == nil {
		t.Fatalf("grant = %+v, %v", resp, err)
	}
	if _, _, err := ask(t, w.dial(newcomer), &wire.Request{Rollouts: &wire.RolloutsRequest{Service: "web"}}); err != nil {
		t.Errorf("the newcomer after its grant: %v", err)
	}
	list, _ = w.grants(grantedAdmin)
	g := holds(list, newcomer, wire.RoleDeployer, "web")
	if g == nil || g.By != grantedAdmin || !g.At.Equal(at) {
		t.Fatalf("the grant as it is listed: %+v, want it of the newcomer in lower case, by the admin who granted it, at the time", g)
	}
	if len(g.Note) != 200 || strings.ContainsAny(g.Note, "\x1b\n") {
		t.Errorf("the note is kept as %q (%d bytes), want it cleaned and cut at 200", g.Note, len(g.Note))
	}
	inDB, err := cfg.DB.Grants(context.Background())
	if err != nil || holds(inDB, newcomer, wire.RoleDeployer, "web") == nil {
		t.Errorf("the grant is not in the database: %+v, %v", inDB, err)
	}
	// Granted again, it is there with the new note.
	if _, _, err := ask(t, admin, &wire.Request{Grant: &wire.GrantRequest{ID: newcomer, Role: wire.RoleDeployer, Service: "web", Note: "ci of web"}}); err != nil {
		t.Fatalf("grant again: %v", err)
	}
	list, _ = w.grants(grantedAdmin)
	if g := holds(list, newcomer, wire.RoleDeployer, "web"); g == nil || g.Note != "ci of web" || len(list) != 7 {
		t.Errorf("after the grant was made again: %+v among %d, want the one grant with the new note", g, len(list))
	}

	// What is no grant is a bad request.
	for what, req := range map[string]wire.GrantRequest{
		"no ID":                   {Role: wire.RoleDeployer},
		"an ID that is none":      {ID: "nobody", Role: wire.RoleDeployer},
		"a role that is none":     {ID: newcomer, Role: "root"},
		"an admin of a service":   {ID: newcomer, Role: wire.RoleAdmin, Service: "web"},
		"a service that is none":  {ID: newcomer, Role: wire.RoleDeployer, Service: "Not A Name"},
		"a service of the every":  {ID: newcomer, Role: wire.RoleZathras, Service: "*"},
		"a role with wrong case":  {ID: newcomer, Role: "Deployer"},
		"an ID with spaces in it": {ID: newcomer + " ", Role: wire.RoleDeployer},
	} {
		if _, _, err := ask(t, admin, &wire.Request{Grant: &req}); wire.CodeOf(err) != wire.CodeBadRequest {
			t.Errorf("a grant with %s = %v, want a bad-request", what, err)
		}
	}

	// A revocation: of what is not there, of the command line's, of
	// one's own, and of what is there.
	for what, c := range map[string]struct {
		req  wire.RevokeRequest
		code wire.Code
	}{
		"a grant that is not there":           {wire.RevokeRequest{ID: newcomer, Role: wire.RoleDeployer, Service: "api"}, wire.CodeNotFound},
		"the grant of every service it lacks": {wire.RevokeRequest{ID: newcomer, Role: wire.RoleDeployer}, wire.CodeNotFound},
		"an admin of the command line":        {wire.RevokeRequest{ID: commandAdmin, Role: wire.RoleAdmin}, wire.CodeBadRequest},
		"its own admin grant":                 {wire.RevokeRequest{ID: grantedAdmin, Role: wire.RoleAdmin}, wire.CodeBadRequest},
		"a role that is none":                 {wire.RevokeRequest{ID: newcomer, Role: "root"}, wire.CodeBadRequest},
		"an ID that is none":                  {wire.RevokeRequest{ID: "nobody", Role: wire.RoleDeployer}, wire.CodeBadRequest},
	} {
		if _, _, err := ask(t, admin, &wire.Request{Revoke: &c.req}); wire.CodeOf(err) != c.code {
			t.Errorf("revoking %s = %v, want %s", what, err, c.code)
		}
	}
	if _, _, err := ask(t, w.dial(grantedAdmin), &wire.Request{Grants: &wire.GrantsRequest{}}); err != nil {
		t.Fatalf("the admin after the revocations that were refused: %v", err)
	}
	_, resp, err = ask(t, admin, &wire.Request{Revoke: &wire.RevokeRequest{ID: newcomer, Role: wire.RoleDeployer, Service: "web"}})
	if err != nil || resp.Revoked == nil {
		t.Fatalf("revoke = %+v, %v", resp, err)
	}
	if _, _, err := ask(t, w.dial(newcomer), &wire.Request{Rollouts: &wire.RolloutsRequest{Service: "web"}}); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("the newcomer after its grant was revoked: %v, want unauthorized", err)
	}
	if inDB, _ := cfg.DB.Grants(context.Background()); holds(inDB, newcomer, wire.RoleDeployer, "web") != nil {
		t.Error("the grant that was revoked is still in the database")
	}
	// Another admin revokes an admin: one cannot do it to oneself, and
	// there is always one left.
	if _, _, err := ask(t, w.dial(commandAdmin), &wire.Request{Revoke: &wire.RevokeRequest{ID: grantedAdmin, Role: wire.RoleAdmin}}); err != nil {
		t.Errorf("an admin revoking another: %v", err)
	}
}

// fakeStore is the store as draal asks it: it notes whose connections it
// was told to end, and answers a page.
type fakeStore struct {
	mu      sync.Mutex
	dropped []wire.ID
}

func (s *fakeStore) Drop(peer wire.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropped = append(s.dropped, peer)
}

func (s *fakeStore) Page(path string) wire.StorePage {
	return wire.StorePage{Status: 200, Type: "text/plain", Body: []byte("the page at " + path)}
}

func (s *fakeStore) wasDropped(peer wire.ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.dropped {
		if id == peer {
			return true
		}
	}
	return false
}

// A revocation ends what the grant was used for, before it is answered:
// the connections of the key, of either protocol, and the sessions of
// the zathrases the key signed for.
func TestARevocationEndsWhatTheGrantWasUsedFor(t *testing.T) {
	role, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{}
	cfg := granted(t, append(theUsual(), wire.Grant{ID: role.ID(), Role: wire.RoleZathras, Service: "web"})...)
	cfg.Store = store
	cfg.Settle = time.Hour
	w := startWorld(t, cfg)

	// A deployer that watches, and one that does not lose its grant.
	watching := w.dial(deployerOfWeb)
	if _, _, err := ask(t, watching, &wire.Request{Watch: &wire.WatchRequest{}}); err != nil {
		t.Fatalf("watch: %v", err)
	}
	other := w.dial(deployerOfEvery)
	if _, _, err := ask(t, other, &wire.Request{Watch: &wire.WatchRequest{}}); err != nil {
		t.Fatalf("watch: %v", err)
	}
	admin := w.dial(commandAdmin)
	if _, _, err := ask(t, admin, &wire.Request{Revoke: &wire.RevokeRequest{ID: deployerOfWeb, Role: wire.RoleDeployer, Service: "web"}}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// It has ended when the revocation is answered.
	select {
	case <-watching.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the connection of the key whose grant was revoked is still there")
	}
	if closed(other) {
		t.Error("the connection of another key was ended with it")
	}
	if !store.wasDropped(deployerOfWeb) {
		t.Error("the store was not told to end the connections of the key")
	}

	// The zathrases a role key signed for go with its grant; one that
	// another role key signed for stays.
	mine, theirs := nodetest.ID(50), nodetest.ID(51)
	connMine, connTheirs := w.dial(mine), w.dial(theirs)
	if err := register(t, connMine, "web", statementOf(role, cfg.ID, mine)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := register(t, connTheirs, "web", statementOf(theRole(), cfg.ID, theirs)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, err := ask(t, admin, &wire.Request{Revoke: &wire.RevokeRequest{ID: role.ID(), Role: wire.RoleZathras, Service: "web"}}); err != nil {
		t.Fatalf("revoke of the role key: %v", err)
	}
	select {
	case <-connMine.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the session of a zathras is still there after the grant of its role key was revoked")
	}
	if closed(connTheirs) {
		t.Error("the session of a zathras of another role key was ended with it")
	}
	if !store.wasDropped(mine) || store.wasDropped(theirs) {
		t.Errorf("the store was told to end the connections of %v, want those of the zathras the role key signed for alone", store.dropped)
	}
	eventually(t, "the zathras to have left the fleet", func() bool {
		_, in := w.member("web", mine)
		return !in
	})
	// It does not come back: its role key signs for nobody now.
	if err := register(t, w.dial(mine), "web", statementOf(role, cfg.ID, mine)); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("the zathras registers again with a role key whose grant was revoked: %v", err)
	}

	// An admin that revokes a grant of its own key keeps the connection
	// it asked on: it is an admin as before, and is to read the answer.
	if _, _, err := ask(t, admin, &wire.Request{Grant: &wire.GrantRequest{ID: commandAdmin, Role: wire.RoleDeployer, Service: "web"}}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	elsewhere := w.dial(commandAdmin)
	if _, _, err := ask(t, elsewhere, &wire.Request{Watch: &wire.WatchRequest{}}); err != nil {
		t.Fatalf("watch: %v", err)
	}
	_, resp, err := ask(t, admin, &wire.Request{Revoke: &wire.RevokeRequest{ID: commandAdmin, Role: wire.RoleDeployer, Service: "web"}})
	if err != nil || resp.Revoked == nil {
		t.Fatalf("an admin revoking a grant of its own key = %+v, %v", resp, err)
	}
	if closed(admin) {
		t.Error("the connection the revocation was asked on was ended")
	}
	select {
	case <-elsewhere.Done():
	case <-time.After(5 * time.Second):
		t.Error("another connection of the key is still there")
	}
}

// The page of the store is an admin's, and is the store's to answer.
func TestThePageOfTheStore(t *testing.T) {
	cfg := granted(t, theUsual()...)
	cfg.Store = &fakeStore{}
	w := startWorld(t, cfg)
	_, resp, err := ask(t, w.dial(grantedAdmin), &wire.Request{StorePage: &wire.StorePageRequest{Path: "/api/stats?x=1"}})
	if err != nil || resp.StorePage == nil || resp.StorePage.Status != 200 || string(resp.StorePage.Body) != "the page at /api/stats?x=1" {
		t.Errorf("the page of the store as an admin = %+v, %v", resp, err)
	}
	if _, _, err := ask(t, w.dial(deployerOfEvery), &wire.Request{StorePage: &wire.StorePageRequest{Path: "/"}}); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("the page of the store as a deployer = %v, want unauthorized", err)
	}
	// A draal without a store has no page of one.
	none := startWorld(t, granted(t))
	if _, _, err := ask(t, none.dial(operatorID), &wire.Request{StorePage: &wire.StorePageRequest{Path: "/"}}); wire.CodeOf(err) != wire.CodeNotFound {
		t.Errorf("the page of the store of a draal that has none = %v, want not-found", err)
	}
}
