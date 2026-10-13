package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amber-store/core/key"
	"github.com/amber-store/core/packstore"
	"github.com/amber-store/jaccard-store/client"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/push"
	"github.com/fables-for-robots/great-machine/storeserver"
	"github.com/fables-for-robots/great-machine/web"
	"github.com/fables-for-robots/great-machine/wire"
)

// serviceNamed returns the service of the name given in a fleet, or nil.
func serviceNamed(f wire.Fleet, name string) *wire.Service {
	for i := range f.Services {
		if f.Services[i].Name == name {
			return &f.Services[i]
		}
	}
	return nil
}

// grant has the admin of the harness give a key a role.
func (h *harness) grant(id wire.ID, role wire.Role, service, note string) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	if err := h.op.Grant(ctx, wire.GrantRequest{ID: id, Role: role, Service: service, Note: note}); err != nil {
		h.t.Fatalf("granting %s of %q to %s: %v", role, service, id, err)
	}
}

// revoke has the admin of the harness take a grant away.
func (h *harness) revoke(id wire.ID, role wire.Role, service string) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	if err := h.op.Revoke(ctx, wire.RevokeRequest{ID: id, Role: role, Service: service}); err != nil {
		h.t.Fatalf("revoking %s of %q from %s: %v", role, service, id, err)
	}
}

// registerAs dials draal with the key given and registers as a zathras of
// the service, with the statement given. The connection and the stream of
// the reports are closed when the test ends. It returns what draal
// refused the registration with.
func (h *harness) registerAs(ctx context.Context, k node.Key, service string, statement *wire.Statement) (node.Conn, error) {
	h.t.Helper()
	conn, err := h.dial(ctx, k)
	if err != nil {
		h.t.Fatalf("dialing draal as a zathras of %s: %v", service, err)
	}
	h.t.Cleanup(func() { conn.Close() })
	s, err := conn.OpenStream(ctx)
	if err != nil {
		h.t.Fatalf("opening the stream of the reports: %v", err)
	}
	h.t.Cleanup(func() { s.Close() })
	resp, err := wire.Call(s, &wire.Request{Register: &wire.Register{
		Service: service, Name: service + "-0", Version: "e2e", State: wire.State{Phase: wire.PhaseIdle}, Statement: statement,
	}})
	if err != nil {
		return conn, err
	}
	if resp.Registered == nil {
		h.t.Fatalf("draal answered the registration with %+v", resp)
	}
	return conn, nil
}

// pullOver fetches a reference from draal's store as a zathras does: over
// a connection of the store's protocol that is dialed from the connection
// it has to draal. It returns the root the reference has.
func (h *harness) pullOver(ctx context.Context, conn node.Conn, name string) (key.Key, error) {
	h.t.Helper()
	o, err := node.Again(ctx, conn, storeserver.ALPN)
	if err != nil {
		return key.Key{}, err
	}
	defer o.Close()
	objects, err := packstore.Open(filepath.Join(h.t.TempDir(), "packstore"), packstore.WithSync(false))
	if err != nil {
		h.t.Fatal(err)
	}
	defer objects.Close()
	res, err := client.New(o, nil).Pull(ctx, objects, name, client.PullOptions{})
	return res.Root, err
}

// unauthorized says whether err is a refusal of the store for want of a
// grant. The store's protocol has no codes of ours: its refusal is a text.
func unauthorized(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unauthorized")
}

// A key that was granted deployer of web does everything a deployer does
// with web, and nothing with api: it is told that it is no deployer of
// api, sees no api in the fleet, and a zathras of api is to it as one
// that is not connected. The bucket of the store sets the environment of
// the test, so this test is not parallel.
func TestADeployerOfAServiceDeploysItAndNoOther(t *testing.T) {
	h := start(t, options{zathrases: 1, store: true})
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	h.podService = "api"
	api := h.startPod("api-0")
	h.fleetWhere("the zathras of api to register", func(f wire.Fleet) bool {
		svc := serviceNamed(f, "api")
		return svc != nil && len(svc.Zathrases) == 1
	})
	web := h.pods[0]

	// A key that was granted nothing is told who it is, and nothing else.
	k := newKey(t)
	dep := h.operator(k)
	if me, err := dep.Me(ctx); err != nil || me.ID != k.ID() || len(me.Grants) != 0 {
		t.Fatalf("Me of a key with no grant = %+v, %v; want its ID and no grants", me, err)
	}
	if _, err := dep.Watch(ctx); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Fatalf("Watch of a key with no grant: %v, want unauthorized", err)
	}

	// What is granted holds at once, for the connection that is there.
	h.grant(k.ID(), wire.RoleDeployer, "web", "ci")
	me, err := dep.Me(ctx)
	if err != nil || len(me.Grants) != 1 {
		t.Fatalf("Me after the grant = %+v, %v; want one grant", me, err)
	}
	if g := me.Grants[0]; g.ID != k.ID() || g.Role != wire.RoleDeployer || g.Service != "web" || g.Note != "ci" || g.By != h.opKey.ID() || g.At.IsZero() || g.Fixed {
		t.Errorf("the grant is %+v, want deployer of web, noted ci, by the admin of the harness", g)
	}

	// The store: where web's closures go, a push of one, and none of
	// either for api.
	info, err := dep.Store(ctx, "web")
	if err != nil || info.Prefix != "/services/web/nix/store/" {
		t.Fatalf("Store(web) as its deployer = %+v, %v", info, err)
	}
	if _, err := dep.Store(ctx, "api"); wire.CodeOf(err) != wire.CodeUnauthorized || !strings.Contains(err.Error(), "is no deployer of api") {
		t.Errorf("Store(api) as a deployer of web: %v, want it told that it is no deployer of api", err)
	}
	dir := t.TempDir()
	if _, res, err := h.pushAs(ctx, k, info.Prefix, storePathOf(t, dir, "bbb-web", "the program of web")); err != nil || res != (push.Result{Pushed: 1}) {
		t.Errorf("the push of web's store path as its deployer = %+v, %v; want it pushed", res, err)
	}
	if _, res, err := h.pushAs(ctx, k, "/services/api/nix/store/", storePathOf(t, dir, "ccc-api", "the program of api")); !unauthorized(err) || res != (push.Result{}) {
		t.Errorf("the push of api's store path as a deployer of web = %+v, %v; want it refused as unauthorized", res, err)
	}
	if f := h.storeFigures(); f.Refs != 1 {
		t.Errorf("the store holds %d references, want the one of web", f.Refs)
	}

	// The rollout of web, and none of api.
	rolled, err := dep.Rollout(ctx, wire.RolloutRequest{Service: "web", Binary: h.binary, Closure: []string{storeName}, Parallel: 1})
	if err != nil {
		t.Fatalf("Rollout of web as its deployer: %v", err)
	}
	h.await(rolled, wire.RolloutCompleted)
	if list, err := dep.Rollouts(ctx, "web"); err != nil || len(list) != 1 || list[0].ID != rolled.ID {
		t.Errorf("Rollouts(web) as its deployer = %+v, %v; want the one rollout", list, err)
	}
	if _, err := dep.Rollout(ctx, wire.RolloutRequest{Service: "api", Binary: h.binary, Closure: []string{storeName}, Parallel: 1}); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Rollout of api as a deployer of web: %v, want unauthorized", err)
	}
	if _, err := dep.Rollback(ctx, wire.RollbackRequest{Service: "api"}); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Rollback of api as a deployer of web: %v, want unauthorized", err)
	}
	if _, err := dep.Rollouts(ctx, "api"); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Rollouts(api) as a deployer of web: %v, want unauthorized", err)
	}
	if svc := serviceNamed(h.fleetWhere("the fleet", func(wire.Fleet) bool { return true }), "api"); svc == nil || svc.Rollout != nil {
		t.Errorf("after the refused rollout api is %+v, want it without a rollout", svc)
	}

	// The fleet a deployer of web watches has web in it and no api.
	watching, stop := context.WithCancel(ctx)
	fleets, err := dep.Watch(watching)
	if err != nil {
		t.Fatalf("Watch as a deployer of web: %v", err)
	}
	seen := <-fleets
	stop()
	var names []string
	for _, svc := range seen.Services {
		names = append(names, svc.Name)
	}
	if !slices.Equal(names, []string{"web"}) {
		t.Errorf("a deployer of web sees the services %q, want web alone", names)
	}

	// The zathras of web answers it, and the one of api is to it as one
	// that is not connected, which an admin finds connected.
	if _, err := dep.Logs(ctx, web.id, false); err != nil {
		t.Errorf("Logs of web's zathras as its deployer: %v", err)
	}
	if _, err := dep.History(ctx, web.id); err != nil {
		t.Errorf("History of web's zathras as its deployer: %v", err)
	}
	if _, err := h.op.Logs(ctx, api.id, false); err != nil {
		t.Errorf("Logs of api's zathras as an admin: %v", err)
	}
	if _, err := dep.Logs(ctx, api.id, false); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Logs of api's zathras as a deployer of web: %v, want it unavailable", err)
	}
	if _, err := dep.History(ctx, api.id); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("History of api's zathras as a deployer of web: %v, want it unavailable", err)
	}
	if sh, err := dep.Shell(ctx, api.id, wire.ShellRequest{Cols: 80, Rows: 24}); wire.CodeOf(err) != wire.CodeUnavailable {
		if sh != nil {
			sh.Close()
		}
		t.Errorf("Shell in api's zathras as a deployer of web: %v, want it unavailable", err)
	}

	// Who may do what is an admin's to read and to change.
	if _, err := dep.Grants(ctx); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Grants as a deployer: %v, want unauthorized", err)
	}
	if err := dep.Grant(ctx, wire.GrantRequest{ID: k.ID(), Role: wire.RoleDeployer, Service: "api"}); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("a deployer granting itself api: %v, want unauthorized", err)
	}
	if err := dep.Revoke(ctx, wire.RevokeRequest{ID: h.roleKey.ID(), Role: wire.RoleZathras}); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("a deployer revoking the role key: %v, want unauthorized", err)
	}
	if _, err := dep.StorePage(ctx, "/api/stats"); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("the page of the store as a deployer: %v, want unauthorized", err)
	}
	if page, err := h.op.StorePage(ctx, "/api/stats"); err != nil || page.Status != 200 || !strings.Contains(string(page.Body), `"refs"`) {
		t.Errorf("the page of the store as an admin = %d %q, %v; want the figures of the store", page.Status, page.Body, err)
	}
}

// Each service has a role key of its own, and a zathras is let in for the
// service whose role key signed for it and for no other. What it pulls is
// what was pushed for that service: the closure of the other is refused
// it, and a key that did not register pulls nothing. The bucket of the
// store sets the environment of the test, so this test is not parallel.
func TestTheZathrasesOfTwoServicesPullTheirOwn(t *testing.T) {
	h := start(t, options{store: true})
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	// The closures, pushed by an admin, each under the prefix of its
	// service.
	dir := t.TempDir()
	paths := map[string]string{
		"web": storePathOf(t, dir, "bbb-web", "the program of web"),
		"api": storePathOf(t, dir, "ccc-api", "the program of api"),
	}
	name := func(service string) string {
		return "/services/" + service + "/nix/store/" + filepath.Base(paths[service])
	}
	for service, path := range paths {
		if _, res, err := h.pushAs(ctx, h.opKey, "/services/"+service+"/nix/store/", path); err != nil || res != (push.Result{Pushed: 1}) {
			t.Fatalf("the push of %s's store path = %+v, %v", service, res, err)
		}
	}

	// The role keys, each granted zathras of its service.
	roles := map[string]node.Key{"web": newKey(t), "api": newKey(t)}
	for service, role := range roles {
		h.grant(role.ID(), wire.RoleZathras, service, "the pods of "+service)
	}

	// A zathras registers for the service its role key is of, and for no
	// other; one that nobody signed for, or whose signer was granted
	// nothing, does not register at all.
	stray := newKey(t)
	if _, err := h.registerAs(ctx, stray, "api", h.statementBy(roles["web"], stray.ID())); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("registering for api with a statement of web's role key: %v, want unauthorized", err)
	}
	if _, err := h.registerAs(ctx, stray, "web", nil); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("registering with no statement: %v, want unauthorized", err)
	}
	if _, err := h.registerAs(ctx, stray, "web", h.statementBy(newKey(t), stray.ID())); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("registering with the statement of a key that was granted nothing: %v, want unauthorized", err)
	}
	other := newKey(t)
	if _, err := h.registerAs(ctx, stray, "web", h.statementBy(roles["web"], other.ID())); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("registering with a statement that was signed for another zathras: %v, want unauthorized", err)
	}
	// It has a connection to draal all the same, and pulls nothing over it.
	unregistered, err := h.dial(ctx, stray)
	if err != nil {
		t.Fatalf("dialing draal: %v", err)
	}
	defer unregistered.Close()
	if _, err := h.pullOver(ctx, unregistered, name("web")); !unauthorized(err) {
		t.Errorf("the pull of a key that did not register: %v, want it refused as unauthorized", err)
	}

	keys := map[string]node.Key{"web": newKey(t), "api": newKey(t)}
	conns := make(map[string]node.Conn)
	for service, k := range keys {
		conn, err := h.registerAs(ctx, k, service, h.statementBy(roles[service], k.ID()))
		if err != nil {
			t.Fatalf("registering the zathras of %s: %v", service, err)
		}
		conns[service] = conn
	}
	for service, theOther := range map[string]string{"web": "api", "api": "web"} {
		got, err := h.pullOver(ctx, conns[service], name(service))
		if err != nil {
			t.Errorf("the zathras of %s pulling its own store path: %v", service, err)
		} else if want := rootOf(t, paths[service]); got != want {
			t.Errorf("the zathras of %s pulled the root %s, and what was pushed has %s", service, got, want)
		}
		if _, err := h.pullOver(ctx, conns[service], name(theOther)); !unauthorized(err) {
			t.Errorf("the zathras of %s pulling the store path of %s: %v, want it refused as unauthorized", service, theOther, err)
		}
	}
	// A zathras pulls, and does nothing else with the store.
	if _, res, err := h.pushAs(ctx, keys["web"], "/services/web/nix/store/", storePathOf(t, t.TempDir(), "ddd-evil", "not the program of web")); !unauthorized(err) || res != (push.Result{}) {
		t.Errorf("the push of a zathras of web = %+v, %v; want it refused as unauthorized", res, err)
	}

	// The role key of api is revoked: its zathras has no connection any
	// more when the revocation is answered, and pulls nothing from then
	// on. The one of web is as it was.
	h.revoke(roles["api"].ID(), wire.RoleZathras, "api")
	// draal has closed the connection when it answers the revocation, and
	// the zathras learns so as soon as that has reached it.
	select {
	case <-conns["api"].Done():
	case <-time.After(deadline):
		t.Errorf("the connection of api's zathras is still there after the revocation of its role key")
	}
	if _, err := h.pullAs(ctx, keys["api"], name("api")); !unauthorized(err) {
		t.Errorf("the pull of api's zathras after its role key was revoked: %v, want it refused as unauthorized", err)
	}
	if _, err := h.registerAs(ctx, keys["api"], "api", h.statementBy(roles["api"], keys["api"].ID())); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("registering again with the statement of a revoked role key: %v, want unauthorized", err)
	}
	if _, err := h.pullOver(ctx, conns["web"], name("web")); err != nil {
		t.Errorf("the zathras of web pulling after the role key of api was revoked: %v", err)
	}
}

// A revocation ends what the grant was used for before it is answered:
// the watch of a deployer, and the session of a zathras whose role key
// it was. The zathras dials again, as it does for ever, and is in the
// fleet again once its role key is granted again.
func TestARevocationEndsWhatTheGrantWasUsedFor(t *testing.T) {
	t.Parallel()
	h := start(t, options{})
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	role := newKey(t)
	h.grant(role.ID(), wire.RoleZathras, service, "the pods of web")
	h.podRole = &role
	p := h.startPod("web-0")
	first := h.fleetWhere("the zathras to register", func(f wire.Fleet) bool {
		_, ok := zathrasOf(f, p.id)
		return ok
	})
	before, _ := zathrasOf(first, p.id)

	// The deployer, its watch and its shell in the zathras.
	k := newKey(t)
	h.grant(k.ID(), wire.RoleDeployer, service, "")
	dep := h.operator(k)
	fleets, err := dep.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch as a deployer: %v", err)
	}
	sh, err := dep.Shell(ctx, p.id, wire.ShellRequest{Cols: 80, Rows: 24, Term: "dumb"})
	if err != nil {
		t.Fatalf("Shell as a deployer: %v", err)
	}
	defer sh.Close()
	if _, err := sh.Write([]byte("echo mar''ker\n")); err != nil {
		t.Fatalf("writing to the shell: %v", err)
	}
	for seen, buf := "", make([]byte, 4096); !strings.Contains(seen, "marker"); {
		n, err := sh.Read(buf)
		if err != nil {
			t.Fatalf("the shell ended before the revocation, after %q: %v", seen, err)
		}
		seen += string(buf[:n])
	}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		for range fleets {
		}
	}()
	h.revoke(k.ID(), wire.RoleDeployer, service)
	select {
	case <-ended:
	case <-time.After(deadline):
		t.Fatalf("the watch of a deployer went on after its grant was revoked")
	}
	if ctx.Err() != nil {
		t.Fatalf("the watch ended with the time of the test, and not with the revocation")
	}
	// The shell is ended with the connection it was opened on: what is
	// read of it comes to an end, and as it did not exit, the end is an
	// error and no exit status.
	read := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(sh)
		read <- err
	}()
	select {
	case err := <-read:
		if err == nil {
			t.Errorf("the shell of the deployer ended with an exit status after the revocation, want it ended with its connection")
		}
	case <-time.After(deadline):
		t.Fatalf("the shell of a deployer went on after its grant was revoked")
	}
	if status, err := sh.Wait(ctx); err == nil || ctx.Err() != nil {
		t.Errorf("the shell of the deployer after the revocation: status %d, %v; want the error it ended with", status, err)
	}
	if _, err := h.operator(k).Watch(ctx); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Watch on a new connection after the revocation: %v, want unauthorized", err)
	}

	// The role key: the zathras it signed for is gone from the fleet, and
	// nothing its statement says lets one in.
	h.revoke(role.ID(), wire.RoleZathras, service)
	h.fleetWhere("the zathras to be gone", func(f wire.Fleet) bool {
		_, ok := zathrasOf(f, p.id)
		return !ok
	})
	another := newKey(t)
	if _, err := h.registerAs(ctx, another, service, h.statementBy(role, another.ID())); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("registering with the statement of a revoked role key: %v, want unauthorized", err)
	}
	// The admin that revoked is an admin still, on the connection it asked on.
	if grants, err := h.op.Grants(ctx); err != nil || slices.ContainsFunc(grants, func(g wire.Grant) bool { return g.ID == role.ID() || g.ID == k.ID() }) {
		t.Errorf("the grants after the two revocations = %+v, %v; want neither key in them", grants, err)
	}

	// Granted again, the zathras comes back by itself.
	h.grant(role.ID(), wire.RoleZathras, service, "the pods of web, again")
	f := h.fleetWhere("the zathras to be in the fleet again", func(f wire.Fleet) bool {
		z, ok := zathrasOf(f, p.id)
		return ok && z.Connected.After(before.Connected)
	})
	if z, _ := zathrasOf(f, p.id); z.Name != p.name {
		t.Errorf("the zathras came back as %+v, want %s", z, p.name)
	}
}

// holds says whether a list of grants has the one of the key, the role
// and the service given.
func holds(grants []wire.Grant, id wire.ID, role wire.Role, service string) bool {
	return slices.ContainsFunc(grants, func(g wire.Grant) bool {
		return g.ID == id && g.Role == role && g.Service == service
	})
}

// The page of an admin says who it is, lists the grants, grants and
// revokes, and shows the page of the store; the page of a deployer says
// who it is, and is refused the rest with what draal said. The bucket of
// the store sets the environment of the test, so this test is not
// parallel.
func TestHTTPAccessAndThePageOfTheStore(t *testing.T) {
	h := start(t, options{zathrases: 1, store: true})
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	p := h.web()

	// Who the page is: the admin of draal's command line.
	var me wire.Identity
	status, body := p.get("/api/me", "")
	if err := json.Unmarshal(body, &me); status != http.StatusOK || err != nil || me.ID != h.opKey.ID() || len(me.Grants) != 1 ||
		me.Grants[0].Role != wire.RoleAdmin || !me.Grants[0].Fixed {
		t.Fatalf("GET /api/me = %d %s (%v), want the admin of the command line", status, body, err)
	}

	// A grant, and the list that has it, with who granted it.
	k := newKey(t)
	status, body = p.ask(http.MethodPost, "/api/grants", wire.GrantRequest{ID: k.ID(), Role: wire.RoleDeployer, Service: "web", Note: "through the page"})
	if status != http.StatusNoContent {
		t.Fatalf("POST /api/grants = %d %s, want 204", status, body)
	}
	var list wire.GrantList
	status, body = p.get("/api/grants", "")
	if err := json.Unmarshal(body, &list); status != http.StatusOK || err != nil {
		t.Fatalf("GET /api/grants = %d %s (%v)", status, body, err)
	}
	if len(list.Grants) != 3 || !list.Grants[0].Fixed || list.Grants[0].ID != h.opKey.ID() ||
		!holds(list.Grants, h.roleKey.ID(), wire.RoleZathras, "") {
		t.Errorf("the grants are %+v, want the admin of the command line first, the role key of the harness and the deployer", list.Grants)
	}
	if i := slices.IndexFunc(list.Grants, func(g wire.Grant) bool { return g.ID == k.ID() }); i < 0 ||
		list.Grants[i].Role != wire.RoleDeployer || list.Grants[i].Service != "web" || list.Grants[i].Note != "through the page" || list.Grants[i].By != h.opKey.ID() {
		t.Errorf("the grants are %+v, want the deployer of web among them, granted by the admin", list.Grants)
	}
	// What draal refuses of a grant is the page's answer.
	if status, body := p.ask(http.MethodPost, "/api/grants", map[string]string{"id": "nobody", "role": "deployer"}); status != http.StatusBadRequest || !strings.Contains(string(body), `"bad-request"`) {
		t.Errorf("POST /api/grants with what is no endpoint ID = %d %s, want 400 and a bad-request", status, body)
	}

	// The page of the store, which draal answers for an admin.
	var figures storeFigures
	status, body = p.get("/store/api/stats", "")
	if err := json.Unmarshal(body, &figures); status != http.StatusOK || err != nil || figures.Refs != 0 {
		t.Errorf("GET /store/api/stats = %d %s (%v), want the figures of an empty store", status, body, err)
	}
	if status, body := p.get("/store/", ""); status != http.StatusOK || !strings.Contains(string(body), "jaccard-store") {
		t.Errorf("GET /store/ = %d, %d bytes, want the page of the store", status, len(body))
	}

	// The page of the deployer: who it is, and nothing of the grants and
	// of the store.
	dp := h.webWith(web.Config{Client: pageClient{h.operator(k)}})
	status, body = dp.get("/api/me", "")
	if err := json.Unmarshal(body, &me); status != http.StatusOK || err != nil || me.ID != k.ID() || len(me.Grants) != 1 ||
		me.Grants[0].Role != wire.RoleDeployer || me.Grants[0].Service != "web" {
		t.Errorf("GET /api/me of the deployer = %d %s (%v), want it a deployer of web", status, body, err)
	}
	for _, path := range []string{"/api/grants", "/store/api/stats"} {
		if status, body := dp.get(path, ""); status != http.StatusForbidden || !strings.Contains(string(body), "is no admin") {
			t.Errorf("GET %s of the deployer = %d %s, want 403 and that it is no admin", path, status, body)
		}
	}
	if status, body := dp.ask(http.MethodPost, "/api/grants", wire.GrantRequest{ID: k.ID(), Role: wire.RoleAdmin}); status != http.StatusForbidden {
		t.Errorf("POST /api/grants of the deployer = %d %s, want 403", status, body)
	}
	if grants, err := h.op.Grants(ctx); err != nil || holds(grants, k.ID(), wire.RoleAdmin, "") {
		t.Errorf("after the refused grant the grants are %+v, %v; want the deployer no admin", grants, err)
	}

	// A revocation, as the page asks for one: the service is always named.
	revoke := "/api/grants?" + url.Values{"id": {string(k.ID())}, "role": {"deployer"}, "service": {"web"}}.Encode()
	if status, body := p.ask(http.MethodDelete, revoke, nil); status != http.StatusNoContent {
		t.Fatalf("DELETE %s = %d %s, want 204", revoke, status, body)
	}
	if grants, err := h.op.Grants(ctx); err != nil || holds(grants, k.ID(), wire.RoleDeployer, "web") {
		t.Errorf("after the revocation the grants are %+v, %v; want the deployer gone", grants, err)
	}
	if status, body := p.ask(http.MethodDelete, revoke, nil); status != http.StatusNotFound {
		t.Errorf("the same DELETE again = %d %s, want 404", status, body)
	}
	// An admin of the command line is revoked by no request.
	fixed := "/api/grants?" + url.Values{"id": {string(h.opKey.ID())}, "role": {"admin"}, "service": {""}}.Encode()
	if status, body := p.ask(http.MethodDelete, fixed, nil); status != http.StatusBadRequest {
		t.Errorf("DELETE of the admin of the command line = %d %s, want 400", status, body)
	}
}

// The access view of the terminal UI against a real draal: the grants are
// listed, a grant is made through the form, and revoked after a yes; the
// admin of the command line is not. Like every test of the terminal UI it
// is not parallel.
func TestTUIAccess(t *testing.T) {
	h := start(t, options{zathrases: 1})
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	u := h.startUI()
	u.Wait("● live", "▸ web", human.ShortID(h.opKey.ID()), "admin")

	u.Type("a")
	u.Wait("2 grants", "ROLE", "SERVICE", "▸ admin", "draal's command line", "zathras", "the pods of the harness")
	// The admin of the command line, which the cursor is on, is revoked
	// by nothing.
	u.Type("x")
	u.Wait("no request revokes it")

	// A grant through the form: the role as it opens, the ID, the service
	// and the note.
	k := newKey(t)
	u.Type("g")
	u.Wait("Grant a role", "‹ deployer ›")
	u.Type("\r" + string(k.ID()) + "\rweb\rci\r")
	u.Wait("3 grants", "deployer", human.ShortID(k.ID()), "ci")
	grants, err := h.op.Grants(ctx)
	if i := slices.IndexFunc(grants, func(g wire.Grant) bool { return g.ID == k.ID() }); err != nil || i < 0 ||
		grants[i].Role != wire.RoleDeployer || grants[i].Service != "web" || grants[i].Note != "ci" || grants[i].By != h.opKey.ID() {
		t.Fatalf("after the form the grants are %+v, %v; want a deployer of web, noted ci, by the admin", grants, err)
	}

	// The revocation of it: the grant under the cursor, after a yes.
	for range 4 {
		if strings.Contains(u.Screen(), "▸ deployer") {
			break
		}
		u.Type("j")
		time.Sleep(50 * time.Millisecond)
	}
	u.Wait("▸ deployer")
	u.Type("x")
	u.Type("y")
	u.Wait("Revoked:", "2 grants")
	if grants, err := h.op.Grants(ctx); err != nil || holds(grants, k.ID(), wire.RoleDeployer, "web") {
		t.Errorf("after the revocation the grants are %+v, %v; want the deployer gone", grants, err)
	}

	u.Type("\x1b")
	u.Wait("▸ web")
	u.Type("q")
	if err := u.end(); err != nil {
		t.Errorf("Run = %v, want nil when the user quits", err)
	}
}

// The terminal UI of a key that is no admin: the header says what it is,
// and the access view whose view it is, with the ID an admin grants to.
func TestTUIAccessOfAKeyThatIsNoAdmin(t *testing.T) {
	h := start(t, options{zathrases: 1})
	k := newKey(t)
	h.grant(k.ID(), wire.RoleDeployer, service, "")
	u := h.startUIAs(k)
	u.Wait("● live", "▸ web", human.ShortID(k.ID()), "deployer")

	u.Type("a")
	screen := u.Wait("This view is an admin's.", "This key is no admin", string(k.ID()))
	if strings.Contains(screen, "the pods of the harness") {
		t.Errorf("a key that is no admin is shown a grant:\n%s", screen)
	}

	u.Type("q")
	if err := u.end(); err != nil {
		t.Errorf("Run = %v, want nil when the user quits", err)
	}
}
