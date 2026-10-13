package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/fables-for-robots/great-machine/wire"
)

// meAdmin is who the key of the tests is: an admin of draal's command
// line.
var meAdmin = wire.Identity{ID: idOf(0xee), Grants: []wire.Grant{{ID: idOf(0xee), Role: wire.RoleAdmin, Fixed: true}}}

// fixtureGrants returns the grants of the tests as draal lists them: the
// admin of the command line first, a key with two grants, a deployer of
// every service without a note, and a note longer than its column.
func fixtureGrants() []wire.Grant {
	day := 24 * time.Hour
	return []wire.Grant{
		{ID: idOf(0xee), Role: wire.RoleAdmin, Fixed: true},
		{ID: idOf(0xa1), Role: wire.RoleAdmin, Note: "the laptop of a colleague", By: idOf(0xee), At: t0.Add(-40 * day)},
		{ID: idOf(0xc1), Role: wire.RoleDeployer, Service: "web", Note: "ci", By: idOf(0xee), At: t0.Add(-30 * day)},
		{ID: idOf(0xc1), Role: wire.RoleDeployer, Service: "worker", Note: "ci", By: idOf(0xee), At: t0.Add(-30 * day)},
		{ID: idOf(0xd1), Role: wire.RoleDeployer, By: idOf(0xa1), At: t0.Add(-3 * time.Hour)},
		{ID: idOf(0x51), Role: wire.RoleZathras, Service: "web", Note: "the pods of web", By: idOf(0xee), At: t0.Add(-30 * day)},
		{ID: idOf(0x52), Role: wire.RoleZathras, Service: "worker", Note: "the pods of worker, which are many and whose note is longer than any column", By: idOf(0xee), At: t0.Add(-29 * day)},
	}
}

// onAccess returns a model that is live, knows who its key is, and
// shows the grants.
func onAccess(w, h int) (*model, *fakeClock) {
	m, clock := newTestModel(w, h)
	goLive(m, fixtureFleet())
	m.update(meMsg{epoch: m.epoch, n: m.meN, me: meAdmin})
	press(m, "a")
	m.update(grantsMsg{epoch: m.epoch, n: m.grantsN, grants: fixtureGrants()})
	return m, clock
}

// typeText types the text, a key for every character of it.
func typeText(m *model, text string) []effect {
	var effs []effect
	for _, r := range text {
		effs = append(effs, m.update(tea.KeyPressMsg{Code: r, Text: string(r)})...)
	}
	return effs
}

// The key is asked who it is when the connection is made, and again
// when it was lost and is made anew. A draal that refuses, or one of
// before, costs the header that part and nothing else.
func TestTheKeyIsAskedWhoItIs(t *testing.T) {
	clock := &fakeClock{at: t0}
	m := newModel(Config{Draal: "3f9a2c1b", Now: clock.now})
	m.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.start()
	wantEffects(t, "dialed", m.update(dialedMsg{epoch: 1}), effMe{epoch: 1, n: 1})
	m.update(fleetMsg{epoch: 1, fleet: fixtureFleet()})
	if header := screenOf(m)[0]; strings.Contains(header, "eeab12cd") {
		t.Errorf("before draal has said who the key is, the header is %q", header)
	}
	wantEffects(t, "who the key is", m.update(meMsg{epoch: 1, n: 1, me: meAdmin}))
	wantSaid(t, "the header of an admin", m, "eeab12cd admin · draal 3f9a2c1b · ● live")

	// An answer to an older question, or of another connection, is not
	// read.
	other := wire.Identity{ID: idOf(0x99), Grants: []wire.Grant{{ID: idOf(0x99), Role: wire.RoleDeployer, Service: "web"}}}
	m.update(meMsg{epoch: 1, n: 0, me: other})
	m.update(meMsg{epoch: 2, n: 1, me: other})
	if m.me.ID != idOf(0xee) {
		t.Errorf("an answer that was not asked for is taken: the key is %s", m.me.ID[:8])
	}

	// Lost and back: asked again, and what was known stays until then.
	m.update(lostMsg{epoch: 1})
	wantSaid(t, "the header while the connection is lost", m, "eeab12cd admin")
	m.update(retryMsg{n: m.waitN})
	wantEffects(t, "dialed again", m.update(dialedMsg{epoch: m.epoch}), effMe{epoch: m.epoch, n: 2})
	m.update(fleetMsg{epoch: m.epoch, fleet: fixtureFleet()})
	m.update(meMsg{epoch: m.epoch, n: 2, me: other})
	wantSaid(t, "the header of a deployer", m, "99ab12cd deployer of web · draal 3f9a2c1b · ● live")

	// A draal that refuses, as one of before does that knows no such
	// request: the header is as it was without, and nothing else is.
	m.update(lostMsg{epoch: m.epoch})
	m.update(retryMsg{n: m.waitN})
	m.update(dialedMsg{epoch: m.epoch})
	m.update(fleetMsg{epoch: m.epoch, fleet: fixtureFleet()})
	wantEffects(t, "draal refuses to say", m.update(meMsg{epoch: m.epoch, n: 3, err: wire.Errorf(wire.CodeBadRequest, "draal answers no such request")}))
	header := screenOf(m)[0]
	if strings.Contains(header, "99ab12cd") || !strings.Contains(header, "draal 3f9a2c1b · ● live") || m.conn != connLive {
		t.Errorf("after a refusal the header is %q, and the connection %v", header, m.conn)
	}
	// And one that fails otherwise is no lost connection: the watch
	// says that.
	wantEffects(t, "the question failed", m.update(meMsg{epoch: m.epoch, n: 3, err: errors.New("stream closed")}))
	if m.conn != connLive {
		t.Errorf("a question that failed lost the connection: %v", m.conn)
	}
}

// What the header says of the roles of a key, in few words.
func TestRolesWords(t *testing.T) {
	g := func(role wire.Role, service string) wire.Grant { return wire.Grant{Role: role, Service: service} }
	for _, tt := range []struct {
		grants []wire.Grant
		want   string
	}{
		{nil, "no role"},
		{[]wire.Grant{g(wire.RoleAdmin, "")}, "admin"},
		{[]wire.Grant{g(wire.RoleDeployer, "web"), g(wire.RoleAdmin, "")}, "admin"},
		{[]wire.Grant{g(wire.RoleDeployer, "")}, "deployer"},
		{[]wire.Grant{g(wire.RoleDeployer, "web")}, "deployer of web"},
		{[]wire.Grant{g(wire.RoleDeployer, "web"), g(wire.RoleDeployer, "api")}, "deployer of api, web"},
		{[]wire.Grant{g(wire.RoleDeployer, "web"), g(wire.RoleDeployer, "api"), g(wire.RoleDeployer, "db")}, "deployer of 3 services"},
		{[]wire.Grant{g(wire.RoleDeployer, "web"), g(wire.RoleDeployer, "")}, "deployer"},
		{[]wire.Grant{g(wire.RoleDeployer, "web"), g(wire.RoleZathras, "api")}, "deployer of web; zathras of api"},
		{[]wire.Grant{g("\x1b]0;x\x07root", "web")}, "root of web"},
	} {
		if got := rolesWords(tt.grants); got != tt.want {
			t.Errorf("the roles of %+v are said as %q, want %q", tt.grants, got, tt.want)
		}
	}
}

// Every key of the view of the grants.
func TestAccessKeys(t *testing.T) {
	services := func() *model { m, _ := newTestModel(80, 24); goLive(m, fixtureFleet()); return m }
	access := func() *model { m, _ := onAccess(80, 24); press(m, "down", "down"); return m } // the third grant
	chosen := func(m *model) string {
		if m.screen != screenAccess || len(m.grants) == 0 {
			return ""
		}
		g := m.grants[m.gIdx]
		return string(g.ID[:2]) + " " + string(g.Role) + " " + g.Service
	}

	// a opens it from the services, and asks for the grants. From a
	// service and from a zathras it does nothing.
	m := services()
	wantEffects(t, "a on the services", press(m, "a"), effGrants{epoch: 1, n: 1})
	if m.screen != screenAccess || m.grantsAre != askedFor {
		t.Errorf("a on the services: on %v, the grants are %v", m.screen, m.grantsAre)
	}
	m, _ = onService(80, 24, "web")
	wantEffects(t, "a on a service", press(m, "a"))
	m, _ = onZathras(80, 24, "web", 1)
	wantEffects(t, "a on a zathras", press(m, "a"))
	if m.screen != screenZathras {
		t.Errorf("a on a zathras: on %v", m.screen)
	}

	for _, tt := range []struct {
		key    string
		screen screen
		chosen string
		help   bool
		effs   []effect
	}{
		{"up", screenAccess, "a1 admin ", false, nil},
		{"k", screenAccess, "a1 admin ", false, nil},
		{"down", screenAccess, "c1 deployer worker", false, nil},
		{"j", screenAccess, "c1 deployer worker", false, nil},
		{"pgup", screenAccess, "ee admin ", false, nil},
		{"pgdown", screenAccess, "52 zathras worker", false, nil},
		{"G", screenAccess, "52 zathras worker", false, nil},
		{"enter", screenAccess, "c1 deployer web", false, nil},
		{"right", screenAccess, "c1 deployer web", false, nil},
		{"l", screenAccess, "c1 deployer web", false, nil},
		{"a", screenAccess, "c1 deployer web", false, nil},
		{"s", screenAccess, "c1 deployer web", false, nil},
		{"tab", screenAccess, "c1 deployer web", false, nil},
		{"esc", screenServices, "", false, nil},
		{"left", screenServices, "", false, nil},
		{"h", screenServices, "", false, nil},
		{"backspace", screenServices, "", false, nil},
		{"?", screenAccess, "c1 deployer web", true, nil},
		{"q", screenAccess, "c1 deployer web", false, []effect{effQuit{}}},
		{"ctrl+c", screenAccess, "c1 deployer web", false, []effect{effQuit{}}},
	} {
		m := access()
		effs := press(m, tt.key)
		if m.screen != tt.screen || chosen(m) != tt.chosen || m.help != tt.help || m.form != nil || m.asked != nil {
			t.Errorf("%s: on %v with %q chosen, help %v, a form %v, a question %v; want on %v with %q, help %v and neither",
				tt.key, m.screen, chosen(m), m.help, m.form != nil, m.asked != nil, tt.screen, tt.chosen, tt.help)
		}
		wantEffects(t, tt.key, effs, tt.effs...)
	}

	// g opens the form, x asks whether the grant is to be revoked.
	m = access()
	wantEffects(t, "g", press(m, "g"))
	if m.form == nil || m.screen != screenAccess {
		t.Errorf("g opened no form")
	}
	m = access()
	wantEffects(t, "x", press(m, "x"))
	if m.asked == nil || m.asked.ID != idOf(0xc1) || m.asked.Service != "web" {
		t.Errorf("x asks about %+v, want the grant that is chosen", m.asked)
	}

	// Back on the services, what was chosen there is chosen still, and
	// the view is as it was left when it is opened again.
	m = services()
	press(m, "down", "a")
	m.update(grantsMsg{epoch: 1, n: m.grantsN, grants: fixtureGrants()})
	press(m, "down", "down", "esc")
	if m.screen != screenServices || m.svcName != "fresh" {
		t.Errorf("back on the services, on %v with %q chosen", m.screen, m.svcName)
	}
	wantEffects(t, "a again", press(m, "a"), effGrants{epoch: 1, n: 2})
	if chosen(m) != "c1 deployer web" || m.grantsAre != askedHave {
		t.Errorf("opened again, %q is chosen and the grants are %v; want what was chosen and what was listed, until draal has answered anew", chosen(m), m.grantsAre)
	}
}

// What is chosen in the list stays chosen by what it is, and is in
// sight, however many grants there are.
func TestTheGrantThatIsChosenStaysChosenAndInSight(t *testing.T) {
	m, _ := onAccess(80, 24)
	press(m, "down", "down", "down") // deployer of worker
	list := fixtureGrants()
	list = append([]wire.Grant{{ID: idOf(0x01), Role: wire.RoleAdmin, By: idOf(0xee), At: t0}}, list...)
	m.update(grantsMsg{epoch: 1, n: m.grantsN, grants: list})
	if g := m.grants[m.gIdx]; g.ID != idOf(0xc1) || g.Service != "worker" || m.gIdx != 4 {
		t.Errorf("with a grant before it, %s %s is chosen at %d", g.ID[:2], g.Service, m.gIdx)
	}
	// It goes: what is at its place is chosen.
	list = append(list[:4:4], list[5:]...)
	m.update(grantsMsg{epoch: 1, n: m.grantsN, grants: list})
	if g := m.grants[m.gIdx]; g.ID != idOf(0xd1) || m.gIdx != 4 {
		t.Errorf("with it gone, %s is chosen at %d; want what is at its place", g.ID[:2], m.gIdx)
	}
	m.update(grantsMsg{epoch: 1, n: m.grantsN, grants: nil})
	if m.gIdx != 0 {
		t.Errorf("with no grant left, the row %d is chosen", m.gIdx)
	}
	wantEffects(t, "keys in an empty list", press(m, "down", "up", "G", "pgdown", "pgup", "x"))
	if m.asked != nil {
		t.Error("x with no grant chosen asks about one")
	}

	// Three hundred of them, in a terminal of 24 rows and of 12.
	var many []wire.Grant
	for i := range 300 {
		many = append(many, wire.Grant{ID: wire.ID(fmt.Sprintf("%04x", i) + strings.Repeat("0", 60)), Role: wire.RoleDeployer, Service: fmt.Sprintf("svc-%03d", i), By: idOf(0xee), At: t0})
	}
	for _, size := range [][2]int{{80, 24}, {leastWidth, leastHeight}, {200, 60}} {
		m, _ = onAccess(size[0], size[1])
		m.update(grantsMsg{epoch: 1, n: m.grantsN, grants: many})
		seen := map[string]bool{}
		for i := range 300 {
			var rows []string
			for _, l := range screenOf(m) {
				if strings.HasPrefix(l, "▸ ") {
					rows = append(rows, l)
				}
			}
			if len(rows) != 1 || !strings.Contains(rows[0], fmt.Sprintf("svc-%03d", i)) {
				t.Fatalf("at %d×%d with grant %d chosen, the chosen rows in sight are %q", size[0], size[1], i, rows)
			}
			seen[rows[0]] = true
			press(m, "down")
		}
		if len(seen) != 300 {
			t.Errorf("at %d×%d, %d grants were in sight as the chosen one, want 300", size[0], size[1], len(seen))
		}
		wantSaid(t, "three hundred grants", m, "300 grants")
		for _, key := range []string{"pgup", "pgup", "G", "pgdown", "up"} {
			press(m, key)
			if m.gIdx < m.gTop || m.gIdx >= m.gTop+m.accessHeight() {
				t.Fatalf("at %d×%d after %s: row %d is chosen, and rows %d to %d are in sight", size[0], size[1], key, m.gIdx, m.gTop, m.gTop+m.accessHeight()-1)
			}
		}
	}
}

// The form: four fields, of which the role is one of three and changed
// with the arrow keys. Enter on the last asks draal.
func TestTheFormThatGrants(t *testing.T) {
	m, _ := onAccess(80, 24)
	press(m, "g")
	f := m.form
	if f == nil || f.field != fieldRole || f.role() != wire.RoleDeployer {
		t.Fatalf("the form opens as %+v, want on the role, which is deployer", f)
	}
	// The role, with the arrows; it has no text.
	press(m, "right")
	if f.role() != wire.RoleZathras {
		t.Errorf("right of deployer is %q", f.role())
	}
	press(m, "right")
	if f.role() != wire.RoleAdmin {
		t.Errorf("right of zathras is %q, want admin: the three go round", f.role())
	}
	press(m, "left", "left")
	if f.role() != wire.RoleDeployer {
		t.Errorf("left twice of admin is %q", f.role())
	}
	typeText(m, "zz")
	press(m, "backspace")
	if f.role() != wire.RoleDeployer || f.id != "" {
		t.Errorf("typing on the role made it %q, and the ID %q", f.role(), f.id)
	}

	// The ID: typed, pasted, taken back. q and the other keys of the
	// view are letters here.
	press(m, "enter")
	if f.field != fieldID {
		t.Fatalf("enter on the role goes to the field %d, want the ID", f.field)
	}
	wantEffects(t, "letters that are keys elsewhere", typeText(m, "qgxa?hjkl"))
	if f.id != "qgxa?hjkl" || m.form == nil || m.help {
		t.Fatalf("typed into the ID: %q, the form %v, the keys %v", f.id, m.form != nil, m.help)
	}
	for range len("qgxa?hjkl") {
		press(m, "backspace")
	}
	press(m, "backspace") // one more than there is
	id := string(idOf(0x77))
	m.update(tea.PasteMsg{Content: "  " + id[:32] + "\n" + id[32:] + "\r\n"})
	if f.id != id {
		t.Errorf("an ID that was pasted with spaces and line ends around and in it is %q, want %q", f.id, id)
	}
	// Left and right change nothing on a field of text.
	press(m, "left", "right")
	if f.id != id || f.role() != wire.RoleDeployer {
		t.Errorf("left and right on the ID: %q, %q", f.id, f.role())
	}

	// The service and the note; up and down, tab and shift+tab move.
	press(m, "tab")
	typeText(m, "web")
	press(m, "down")
	typeText(m, "the ci of web")
	if f.field != fieldNote || f.service != "web" || f.note != "the ci of web" {
		t.Errorf("the form is %+v", f)
	}
	press(m, "up", "up", "up", "up")
	if f.field != fieldRole {
		t.Errorf("up from the note four times is the field %d, want the role and no further", f.field)
	}
	m.update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if f.field != fieldRole {
		t.Errorf("tab and shift+tab: the field %d", f.field)
	}
	press(m, "down", "down", "down", "down")
	if f.field != fieldNote {
		t.Errorf("down past the last field is the field %d", f.field)
	}

	// Enter on the note asks draal, with what was typed, trimmed.
	typeText(m, "  ")
	wantEffects(t, "enter on the note", press(m, "enter"),
		effGrant{epoch: 1, n: 1, req: wire.GrantRequest{ID: idOf(0x77), Role: wire.RoleDeployer, Service: "web", Note: "the ci of web"}})
	if !f.asking {
		t.Error("the form does not wait for draal")
	}
	// While draal is asked no key changes the form.
	wantEffects(t, "keys while draal is asked", append(typeText(m, "more"), press(m, "enter", "up", "backspace")...))
	if f.note != "the ci of web  " || f.field != fieldNote {
		t.Errorf("while draal is asked the form became %+v", f)
	}

	// Refused: it is said, and the form stays as it was filled.
	wantEffects(t, "refused", m.update(grantedMsg{epoch: 1, n: 1, err: fmt.Errorf("operator: grant: %w", wire.Errorf(wire.CodeBadRequest, "%s is no endpoint ID", "x\x1b[2Jy"))}))
	if m.form != f || f.asking || f.id != id || f.service != "web" || !strings.Contains(f.said, "is no endpoint ID") || strings.Contains(f.said, "\x1b") {
		t.Errorf("after a refusal the form is %+v", f)
	}
	wantSaid(t, "a refusal in the form", m, "draal refuses: bad-request: xy is no endpoint ID")
	// Asked again, and granted: the form goes, it is said, and the list
	// is asked for anew.
	wantEffects(t, "enter again", press(m, "enter"), effGrant{epoch: 1, n: 2, req: wire.GrantRequest{ID: idOf(0x77), Role: wire.RoleDeployer, Service: "web", Note: "the ci of web"}})
	wantEffects(t, "the answer to the first", m.update(grantedMsg{epoch: 1, n: 1}))
	if m.form == nil {
		t.Fatal("an answer to a request that was asked again closed the form")
	}
	wantEffects(t, "granted", m.update(grantedMsg{epoch: 1, n: 2}), effGrants{epoch: 1, n: 2})
	if m.form != nil || m.notice != "Granted: deployer of web to 77ab12cd." {
		t.Errorf("after the grant the form is %v and it is said %q", m.form, m.notice)
	}

	// An admin is of every service: the field of the service is not
	// asked for, and what was typed into it is not sent.
	press(m, "g")
	f = m.form
	press(m, "left", "down")
	typeText(m, id)
	press(m, "down")
	if f.field != fieldNote {
		t.Errorf("below the ID of an admin is the field %d, want the note", f.field)
	}
	press(m, "up")
	if f.field != fieldID {
		t.Errorf("above the note of an admin is the field %d, want the ID", f.field)
	}
	press(m, "up", "right", "down", "down")
	typeText(m, "web")
	press(m, "up", "up", "left", "down", "down")
	wantEffects(t, "an admin", press(m, "enter"), effGrant{epoch: 1, n: 3, req: wire.GrantRequest{ID: idOf(0x77), Role: wire.RoleAdmin}})
	m.update(grantedMsg{epoch: 1, n: 3})
	if m.notice != "Granted: admin to 77ab12cd." {
		t.Errorf("after the grant of an admin it is said %q", m.notice)
	}

	// Esc gives the form up, also while draal is asked: its answer is
	// then nobody's.
	press(m, "g", "down")
	typeText(m, id)
	press(m, "down", "down")
	press(m, "enter")
	wantEffects(t, "esc while draal is asked", press(m, "esc"))
	if m.form != nil || m.screen != screenAccess {
		t.Errorf("esc: the form %v, on %v", m.form, m.screen)
	}
	wantEffects(t, "the answer to a form that was given up", m.update(grantedMsg{epoch: 1, n: 4}))
	// And the next form is empty.
	press(m, "g")
	if f := m.form; f.id != "" || f.note != "" || f.said != "" || f.role() != wire.RoleDeployer {
		t.Errorf("a new form is %+v", f)
	}
	// Ctrl-C quits, here too.
	wantEffects(t, "ctrl+c in the form", press(m, "ctrl+c"), effQuit{})
}

// What is typed into the form is kept to a length, and is text and
// nothing else, whatever is pasted.
func TestTheFormKeepsWhatIsTypedToItsLimit(t *testing.T) {
	m, _ := onAccess(80, 24)
	press(m, "g", "down")
	m.update(tea.PasteMsg{Content: strings.Repeat("a", 1<<20)})
	if n := len(m.form.id); n > formFieldCap {
		t.Errorf("of a megabyte that was pasted, %d bytes are kept", n)
	}
	typeText(m, "more")
	if n := len(m.form.id); n > formFieldCap {
		t.Errorf("typed behind what was full: %d bytes", n)
	}
	press(m, "down", "down")
	m.update(tea.PasteMsg{Content: "a note\x1b]0;pwned\x07 with\ttabs\nand ‮lines\x00"})
	if found := unsafe(m.view()); found != "" {
		t.Errorf("what was pasted is drawn with %s", found)
	}
	wantSaid(t, "what was pasted", m, "a note with")
}

// x asks, and y revokes; any other key leaves the grant as it is.
func TestRevoking(t *testing.T) {
	m, _ := onAccess(80, 24)
	press(m, "down", "down") // deployer of web, of c1
	wantEffects(t, "x", press(m, "x"))
	wantSaid(t, "the question", m, "Revoke deployer of web from c1ab12cd? y revokes it; any other key leaves it.")
	wantEffects(t, "another key", press(m, "n"))
	if m.asked != nil || m.gIdx != 2 {
		t.Errorf("after another key: a question %v, row %d", m.asked != nil, m.gIdx)
	}
	// The key that gave it up did nothing else: not even down.
	press(m, "x")
	wantEffects(t, "down in place of y", press(m, "down"))
	if m.asked != nil || m.gIdx != 2 {
		t.Errorf("down in place of y: a question %v, and the row %d is chosen", m.asked != nil, m.gIdx)
	}

	press(m, "x")
	wantEffects(t, "y", press(m, "y"), effRevoke{epoch: 1, n: 1, req: wire.RevokeRequest{ID: idOf(0xc1), Role: wire.RoleDeployer, Service: "web"}})
	if m.asked != nil || !m.revoking {
		t.Errorf("after y: a question %v, revoking %v", m.asked != nil, m.revoking)
	}
	// A second one waits for the first.
	wantEffects(t, "x while a revocation is under way", press(m, "x"))
	if m.asked != nil || !strings.Contains(m.notice, "under way") {
		t.Errorf("x while a revocation is under way: a question %v, said %q", m.asked != nil, m.notice)
	}
	wantEffects(t, "revoked", m.update(revokedMsg{epoch: 1, n: 1}), effGrants{epoch: 1, n: 2})
	if m.revoking || m.notice != "Revoked: deployer of web from c1ab12cd." {
		t.Errorf("after the revocation: revoking %v, said %q", m.revoking, m.notice)
	}

	// Refused: it is said, and the list is not asked for.
	press(m, "x", "y")
	wantEffects(t, "refused", m.update(revokedMsg{epoch: 1, n: 2, err: wire.Errorf(wire.CodeBadRequest, "an admin cannot revoke its own\x07 admin grant")}))
	if m.revoking || m.notice != "Not revoked: bad-request: an admin cannot revoke its own admin grant" {
		t.Errorf("after a refusal: revoking %v, said %q", m.revoking, m.notice)
	}

	// An admin of the command line has no x.
	press(m, "g") // a form, to see that x is asked of the list alone
	press(m, "esc", "pgup")
	if !m.grants[m.gIdx].Fixed {
		t.Fatalf("the first grant is %+v", m.grants[m.gIdx])
	}
	wantEffects(t, "x on an admin of the command line", press(m, "x"))
	if m.asked != nil || !strings.Contains(m.notice, "command line") {
		t.Errorf("x on an admin of the command line: a question %v, said %q", m.asked != nil, m.notice)
	}
	// q quits, with or without a question.
	press(m, "down", "x")
	wantEffects(t, "q in place of y", press(m, "q"), effQuit{})
}

// A key that is no admin is told that the view is an admin's, with its
// own ID whole: that is what an admin needs.
func TestAccessOfAKeyThatIsNoAdmin(t *testing.T) {
	deployer := wire.Identity{ID: idOf(0xc1), Grants: []wire.Grant{{ID: idOf(0xc1), Role: wire.RoleDeployer, Service: "web"}, {ID: idOf(0xc1), Role: wire.RoleDeployer, Service: "worker"}}}
	for _, size := range [][2]int{{80, 24}, {leastWidth, leastHeight}, {200, 60}} {
		m, _ := newTestModel(size[0], size[1])
		goLive(m, fixtureFleet())
		m.update(meMsg{epoch: 1, n: m.meN, me: deployer})
		press(m, "a")
		wantSaid(t, "while draal is asked", m, "Asking draal who may do what …")
		refused := fmt.Errorf("operator: grants: %w", wire.Errorf(wire.CodeUnauthorized, "%s is no admin and may not list the grants", idOf(0xc1)))
		wantEffects(t, "refused", m.update(grantsMsg{epoch: 1, n: m.grantsN, err: refused}))
		wantSaid(t, "a key that is no admin", m, "This view is an admin's", "This key is no admin: it is deployer of web, worker.")
		if all := strings.ReplaceAll(strings.Join(screenOf(m), ""), " ", ""); !strings.Contains(all, string(idOf(0xc1))) {
			t.Errorf("at %d×%d the view does not have the ID of the key whole:\n%s", size[0], size[1], strings.Join(screenOf(m), "\n"))
		}
		// g and x are an admin's.
		wantEffects(t, "g and x", press(m, "g", "x"))
		if m.form != nil || m.asked != nil {
			t.Errorf("a key that is no admin has a form %v or a question %v", m.form != nil, m.asked != nil)
		}
	}

	// Who the key is, is not known (a draal that does not say): the
	// view says what it can.
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	press(m, "a")
	m.update(grantsMsg{epoch: 1, n: m.grantsN, err: wire.Errorf(wire.CodeUnauthorized, "no admin")})
	wantSaid(t, "a key that is not known", m, "This view is an admin's", "great-machine id")

	// Any other refusal is said as it is; and the view asks again when
	// it is opened again.
	m, _ = newTestModel(80, 24)
	goLive(m, fixtureFleet())
	press(m, "a")
	m.update(grantsMsg{epoch: 1, n: m.grantsN, err: wire.Errorf(wire.CodeBadRequest, "draal answers no such request")})
	wantSaid(t, "a draal of before", m, "draal does not list the grants: bad-request: draal answers no such request")
	wantEffects(t, "g on a list that was refused", press(m, "g", "x"))
	if m.form != nil {
		t.Error("a form was opened over a list that draal refused")
	}
	press(m, "esc")
	wantEffects(t, "opened again", press(m, "a"), effGrants{epoch: 1, n: 2})
	// Granted meanwhile: the list is there, and the refusal gone.
	m.update(grantsMsg{epoch: 1, n: 2, grants: fixtureGrants()})
	wantSaid(t, "the list after a refusal", m, "7 grants", "the laptop of a colleague")
	if m.notAdmin || m.grantsErr != "" {
		t.Errorf("after the list came, the refusal is still there: %v, %q", m.notAdmin, m.grantsErr)
	}
}

// An answer to an older request for the list is dropped: it may come
// last, and says what was.
func TestAnOlderListOfGrantsIsDropped(t *testing.T) {
	m, _ := onAccess(80, 24)
	press(m, "esc", "a") // asked a second time
	m.update(grantsMsg{epoch: 1, n: 2, grants: fixtureGrants()[:3]})
	m.update(grantsMsg{epoch: 1, n: 1, grants: fixtureGrants()})
	m.update(grantsMsg{epoch: 0, n: 2, grants: fixtureGrants()})
	if len(m.grants) != 3 {
		t.Errorf("%d grants are kept, want the 3 of the newest answer", len(m.grants))
	}
	// The list is taken on whatever view it comes.
	press(m, "esc", "a", "esc")
	m.update(grantsMsg{epoch: 1, n: 3, grants: fixtureGrants()})
	if len(m.grants) != 7 || m.screen != screenServices {
		t.Errorf("a list that came on another view: %d grants, on %v", len(m.grants), m.screen)
	}
}

// The connection lost and back while the view, the form or the question
// is open: what is shown and what was typed stays, and the list is
// asked for again of the new connection.
func TestAccessWhileTheConnectionIsLostAndBack(t *testing.T) {
	back := func(m *model) []effect {
		m.update(retryMsg{n: m.waitN})
		m.update(dialedMsg{epoch: m.epoch})
		return m.update(fleetMsg{epoch: m.epoch, fleet: fixtureFleet()})
	}

	// The list.
	m, _ := onAccess(80, 24)
	press(m, "down", "down")
	m.update(lostMsg{epoch: 1})
	if m.screen != screenAccess || len(m.grants) != 7 || m.gIdx != 2 {
		t.Errorf("lost: on %v with %d grants, row %d", m.screen, len(m.grants), m.gIdx)
	}
	wantSaid(t, "the list while the connection is lost", m, "✖ The connection to draal is lost", "the laptop of a colleague")
	wantEffects(t, "back", back(m), effGrants{epoch: m.epoch, n: 2})
	if m.gIdx != 2 {
		t.Errorf("back: row %d is chosen", m.gIdx)
	}

	// Opened while the connection is lost: nothing is asked, and it is
	// said; asked when it is back.
	m, _ = newTestModel(80, 24)
	goLive(m, fixtureFleet())
	m.update(lostMsg{epoch: 1})
	m.update(retryMsg{n: m.waitN}) // a dial is under way: keys end no pause
	wantEffects(t, "a while the connection is lost", press(m, "a"))
	wantSaid(t, "the view without a connection", m, "There is no connection to draal: who may do what is asked for when it is back.")
	m.update(dialedMsg{epoch: m.epoch})
	wantEffects(t, "back", m.update(fleetMsg{epoch: m.epoch, fleet: fixtureFleet()}), effGrants{epoch: m.epoch, n: 1})

	// The list was asked for and the connection is lost before the
	// answer: asked again.
	m, _ = newTestModel(80, 24)
	goLive(m, fixtureFleet())
	press(m, "a")
	m.update(lostMsg{epoch: 1})
	if m.grantsAre != askedNot {
		t.Errorf("lost before the list came: the grants are %v", m.grantsAre)
	}
	wantEffects(t, "back", back(m), effGrants{epoch: m.epoch, n: 2})

	// The form: what was typed stays. A grant that draal was asked for
	// is not known to be made, and the form says so.
	m, _ = onAccess(80, 24)
	press(m, "g", "down")
	typeText(m, string(idOf(0x77)))
	press(m, "down", "down")
	typeText(m, "a note")
	press(m, "enter")
	effs := m.update(lostMsg{epoch: 1, err: errors.New("stream closed")})
	f := m.form
	if f == nil || f.asking || f.id != string(idOf(0x77)) || f.note != "a note" || !strings.Contains(f.said, "lost") {
		t.Fatalf("lost while a grant was asked for: the form is %+v", f)
	}
	wantEffects(t, "lost", effs, effHangUp{}, effWait{pause: time.Second, n: 1})
	// Typing in a pause dials not: a key of the form is a letter.
	m.update(tickMsg{})
	wantEffects(t, "typing in a pause", typeText(m, "!"))
	// Enter without a connection asks nobody, and says so.
	wantEffects(t, "enter without a connection", press(m, "enter"))
	if f.asking || !strings.Contains(f.said, "no connection") {
		t.Errorf("enter without a connection: asking %v, said %q", f.asking, f.said)
	}
	wantEffects(t, "back", back(m), effGrants{epoch: m.epoch, n: 2})
	if m.form != f || f.note != "a note!" {
		t.Errorf("back: the form is %+v", m.form)
	}
	wantEffects(t, "enter when the connection is back", press(m, "enter"),
		effGrant{epoch: m.epoch, n: 2, req: wire.GrantRequest{ID: idOf(0x77), Role: wire.RoleDeployer, Note: "a note!"}})
	// The answer of the connection before is nobody's.
	wantEffects(t, "an answer of the old connection", m.update(grantedMsg{epoch: 1, n: 2}))
	if m.form == nil {
		t.Error("an answer of the connection before closed the form")
	}

	// The question: it stays, and y without a connection revokes
	// nothing.
	m, _ = onAccess(80, 24)
	press(m, "down", "x")
	m.update(lostMsg{epoch: 1})
	m.update(retryMsg{n: m.waitN})
	if m.asked == nil {
		t.Fatal("lost: the question is gone")
	}
	wantEffects(t, "y without a connection", press(m, "y"))
	if m.revoking || !strings.Contains(m.notice, "no connection") {
		t.Errorf("y without a connection: revoking %v, said %q", m.revoking, m.notice)
	}
	// A revocation that was asked for when the connection was lost.
	m, _ = onAccess(80, 24)
	press(m, "down", "x", "y")
	m.update(lostMsg{epoch: 1})
	if m.revoking || !strings.Contains(m.notice, "lost") {
		t.Errorf("lost while a revocation was asked for: revoking %v, said %q", m.revoking, m.notice)
	}
	wantEffects(t, "the answer of the old connection", m.update(revokedMsg{epoch: 1, n: 1}))
}

// The view of the grants in a terminal of 80 columns and 24 rows, as a
// person reads it.
func TestAccessViewAt80x24(t *testing.T) {
	m, _ := onAccess(80, 24)
	wantScreen(t, "the grants", m, []string{
		" great-machine › access                eeab12cd admin · draal 3f9a2c1b · ● live",
		" 7 grants",
		"",
		"  ROLE      SERVICE  ID        NOTE                         GRANTED",
		"▸ admin     every    eeab12cd                               draal's command line",
		"  admin     every    a1ab12cd  the laptop of a colleague    40d ago by eeab12cd",
		"  deployer  web      c1ab12cd  ci                           30d ago by eeab12cd",
		"  deployer  worker   c1ab12cd  ci                           30d ago by eeab12cd",
		"  deployer  every    d1ab12cd                               3h ago by a1ab12cd",
		"  zathras   web      51ab12cd  the pods of web              30d ago by eeab12cd",
		"  zathras   worker   52ab12cd  the pods of worker, which …  29d ago by eeab12cd",
		"",
		" id       eeab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd",
		" An admin of draal's command line: no request revokes it.",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		" ↑↓ move  g grant  esc back  ? keys  q quit",
	})
	// The grant that is chosen is said whole below the list: its ID, its
	// note, when it was granted and by whom. It can be revoked, which
	// the admin of the command line cannot: the keys say x.
	press(m, "down", "down", "down", "down")
	wantScreen(t, "a grant of today", m, []string{
		" great-machine › access                eeab12cd admin · draal 3f9a2c1b · ● live",
		" 7 grants",
		"",
		"  ROLE      SERVICE  ID        NOTE                         GRANTED",
		"  admin     every    eeab12cd                               draal's command line",
		"  admin     every    a1ab12cd  the laptop of a colleague    40d ago by eeab12cd",
		"  deployer  web      c1ab12cd  ci                           30d ago by eeab12cd",
		"  deployer  worker   c1ab12cd  ci                           30d ago by eeab12cd",
		"▸ deployer  every    d1ab12cd                               3h ago by a1ab12cd",
		"  zathras   web      51ab12cd  the pods of web              30d ago by eeab12cd",
		"  zathras   worker   52ab12cd  the pods of worker, which …  29d ago by eeab12cd",
		"",
		" id       d1ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd",
		" note     none",
		" granted  09:00:00 (3h ago)",
		" by       a1ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		" ↑↓ move  g grant  x revoke  esc back  ? keys  q quit",
	})
	press(m, "G")
	wantSaid(t, "a long note", m, "note the pods of worker, which are many and whose note is longer than any column", "(29d ago)")

	// The form.
	press(m, "g", "down")
	typeText(m, string(idOf(0x77)))
	press(m, "down")
	typeText(m, "web")
	wantScreen(t, "the form", m, []string{
		" great-machine › access                eeab12cd admin · draal 3f9a2c1b · ● live",
		" Grant a role",
		"",
		"   role     ‹ deployer ›   of admin, deployer, zathras",
		"   id       77ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd",
		" ▸ service  web_           empty: every service",
		"   note",
		"",
		" Enter goes to the next field, and on the note it asks draal. ← → change the",
		" role. Esc gives the form up.",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		" enter next  ←→ the role  esc give it up  ctrl-c quit",
	})
	// The services say that there is such a view.
	m, _ = newTestModel(80, 24)
	goLive(m, fixtureFleet())
	if keys := screenOf(m)[23]; keys != " ↑↓ move  enter open  a access  ? keys  q quit" {
		t.Errorf("the keys of the services are %q", keys)
	}
	press(m, "?")
	wantSaid(t, "the keys", m, "a on the services: who may do what (g grants, x revokes)")
}

// Every odd state of the view is a sentence.
func TestAccessOddStatesAreSentences(t *testing.T) {
	for _, tt := range []struct {
		scene string
		says  []string
	}{
		{"access-asking", []string{"great-machine › access", "Asking draal who may do what …"}},
		{"access-no-admin", []string{"This view is an admin's", "This key is no admin: it is deployer of web.", "great-machine access grant ROLE"}},
		{"access-refused", []string{"draal does not list the grants: internal: reading the grants: the database is locked"}},
		{"access-none", []string{"draal has no grant on record.", "great-machine draal --admin ID"}},
		{"access-lost", []string{"✖ The connection to draal is lost", "7 grants", "the laptop of a colleague"}},
		{"access-form", []string{"Grant a role", "‹ deployer ›", "Enter goes to the next field"}},
		{"access-form-admin", []string{"‹ admin ›", "service every: an admin is of every service"}},
		{"access-form-asking", []string{"Asking draal …"}},
		{"access-form-refused", []string{"draal refuses: bad-request: \"zz\" is no endpoint ID"}},
		{"access-question", []string{"Revoke deployer of web from c1ab12cd? y revokes it; any other key leaves it.", "y revoke"}},
		{"access-help", []string{"a on the services: who may do what (g grants, x revokes)", "q Ctrl-C quit"}},
	} {
		wantSaid(t, tt.scene, sceneNamed(t, tt.scene, 80, 24), tt.says...)
	}
	// A wide terminal has the IDs whole in the list.
	wantSaid(t, "a wide terminal", sceneNamed(t, "access", 160, 40), "deployer web "+string(idOf(0xc1))+" ci", "40d ago by eeab12cd")
	// A narrow one says when and leaves out by whom, which the line
	// below the list has.
	m := sceneNamed(t, "access", 60, 20)
	wantSaid(t, "a narrow terminal", m, "deployer web c1ab12cd ci 30d ago")
	if all := strings.Join(screenOf(m), "\n"); strings.Contains(all, "30d ago by") {
		t.Errorf("a terminal of 60 columns has who granted in its list:\n%s", all)
	}
}

// Whatever draal sends as grants, and whatever is typed, what is drawn
// is text of the terminal UI's own making.
func TestNothingOfAGrantReachesTheTerminalAsItCame(t *testing.T) {
	const evil = "\x1b]0;pwned\x07\x1b[2J\x1b[5;5H\x1b[?1049l‮\u009b31m\x00\x08\r"
	grants := fixtureGrants()
	for i := range grants {
		g := &grants[i]
		g.ID += wire.ID(evil)
		g.Role += wire.Role(evil)
		g.Service = evil + g.Service
		g.Note = evil + g.Note + evil
		g.By = wire.ID(evil) + g.By
	}
	grants = append(grants, wire.Grant{ID: evil, Role: evil, Service: evil, Note: evil, By: evil})
	me := wire.Identity{ID: wire.ID(evil) + idOf(0xee), Grants: grants}
	check := func(what string, m *model) {
		t.Helper()
		for _, size := range [][2]int{{80, 24}, {200, 60}, {50, 12}} {
			m.update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			drawn := m.view()
			if found := unsafe(drawn); found != "" {
				t.Errorf("%s at %d×%d: what is drawn has %s", what, size[0], size[1], found)
			}
			if strings.Contains(drawn, "\x1b]") || strings.Contains(drawn, "\x1b[2J") || strings.Contains(drawn, "\x1b[5;5H") {
				t.Errorf("%s at %d×%d: a sequence of a grant is in what is drawn", what, size[0], size[1])
			}
		}
	}
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	m.update(meMsg{epoch: 1, n: m.meN, me: me})
	check("the services, of a key whose name is sequences", m)
	press(m, "a")
	m.update(grantsMsg{epoch: 1, n: m.grantsN, grants: grants})
	for range len(grants) {
		check("the grants", m)
		press(m, "x")
		check("the question", m)
		press(m, "y")
		m.update(revokedMsg{epoch: 1, n: m.revokeN, err: wire.Errorf(wire.CodeBadRequest, "%s", evil+"refused")})
		check("a revocation that was refused", m)
		m.update(revokedMsg{epoch: 1, n: m.revokeN})
		press(m, "x", "y")
		m.update(revokedMsg{epoch: 1, n: m.revokeN})
		check("a revocation", m)
		press(m, "down")
	}
	press(m, "g")
	for range 4 {
		m.update(tea.PasteMsg{Content: evil + "typed" + evil})
		typeText(m, "typed\u202e")
		check("the form", m)
		press(m, "down")
	}
	press(m, "enter")
	m.update(grantedMsg{epoch: 1, n: m.grantN, err: wire.Errorf(wire.CodeBadRequest, "%s", evil+"refused")})
	check("a grant that was refused", m)
	press(m, "enter")
	m.update(grantedMsg{epoch: 1, n: m.grantN})
	check("a grant", m)
	m.update(grantsMsg{epoch: 1, n: m.grantsN, err: wire.Errorf(wire.CodeUnauthorized, "%s", evil+"no admin")})
	check("a key that is no admin", m)
	m.update(grantsMsg{epoch: 1, n: m.grantsN, err: wire.Errorf(wire.CodeInternal, "%s", evil+"refused")})
	check("a list that was refused", m)
	// And the text is still there to be read.
	m, _ = newTestModel(120, 30)
	goLive(m, fixtureFleet())
	press(m, "a")
	m.update(grantsMsg{epoch: 1, n: m.grantsN, grants: grants})
	wantSaid(t, "the text of hostile grants", m, "the laptop of a colleague", "the pods of web")
}

// What draal says in the words of a grant is capped where it comes in,
// and of a list that is longer than any is, so many are kept and the
// rest is counted.
func TestGrantsAreKeptToTheLimit(t *testing.T) {
	huge := strings.Repeat("word ", 1<<18)
	grants := fixtureGrants()
	for i := range grants {
		g := &grants[i]
		g.ID, g.Role, g.Service, g.Note, g.By = wire.ID(huge), wire.Role(huge), huge, huge, wire.ID(huge)
	}
	m, _ := onAccess(120, 30)
	m.update(grantsMsg{epoch: 1, n: m.grantsN, grants: grants})
	most := fieldCap + 40
	for _, g := range m.grants {
		if len(g.ID) > most || len(g.Role) > most || len(g.Service) > most || len(g.Note) > most || len(g.By) > most {
			t.Fatalf("of a grant whose words are a megabyte each, %d, %d, %d, %d and %d bytes are kept", len(g.ID), len(g.Role), len(g.Service), len(g.Note), len(g.By))
		}
	}
	m.update(meMsg{epoch: 1, n: m.meN, me: wire.Identity{ID: wire.ID(huge), Grants: grants}})
	if len(m.me.ID) > most || len(m.me.Grants) != len(grants) || len(m.me.Grants[0].Service) > most {
		t.Errorf("of who the key is, an ID of %d bytes is kept", len(m.me.ID))
	}
	_ = m.view()

	many := make([]wire.Grant, 100_000)
	for i := range many {
		many[i] = wire.Grant{ID: idOf(i % 256), Role: wire.RoleDeployer, Service: fmt.Sprintf("s%d", i)}
	}
	m.update(grantsMsg{epoch: 1, n: m.grantsN, grants: many})
	if len(m.grants) != grantsKept {
		t.Errorf("of a hundred thousand grants %d are kept, want %d", len(m.grants), grantsKept)
	}
	wantSaid(t, "a hundred thousand grants", m, fmt.Sprintf("the first %d of 100000 grants", grantsKept))
	m.update(meMsg{epoch: 1, n: m.meN, me: wire.Identity{ID: idOf(1), Grants: many}})
	if len(m.me.Grants) > grantsKept {
		t.Errorf("of the hundred thousand grants of the key %d are kept", len(m.me.Grants))
	}
}
