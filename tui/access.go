package tui

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// Who may do what: who the key of the terminal UI is to draal, and the
// view of the grants, which an admin lists, grants and revokes in.
//
// The key is asked who it is when a connection is made. The view is
// opened with a from the services. In it g opens a form that grants,
// and x asks whether the grant that is chosen is to be revoked, which y
// answers. While the form is open every key is the form's: a letter is
// a letter there, also q.

// grantsKept is how many grants of a list are kept, and of the grants of
// the key itself. draal's table is what admins put there, and is no
// longer than that; the terminal UI holds what it is sent to a bound all
// the same.
const grantsKept = 2000

// formFieldCap is how many bytes a field of the form holds. An endpoint
// ID is 64 characters and draal cuts a note at 200 bytes: what is typed
// or pasted beyond the cap is left out.
const formFieldCap = 256

// meMsg answers effMe: who the key is, or why draal does not say.
type meMsg struct {
	epoch int
	n     int
	me    wire.Identity
	err   error
}

// grantsMsg answers effGrants: the grants, or draal's refusal.
type grantsMsg struct {
	epoch  int
	n      int
	grants []wire.Grant
	err    error
}

// grantedMsg answers effGrant: nil, or draal's refusal.
type grantedMsg struct {
	epoch int
	n     int
	err   error
}

// revokedMsg answers effRevoke: nil, or draal's refusal.
type revokedMsg struct {
	epoch int
	n     int
	err   error
}

// effMe asks draal who the key is. Its answer has its number n.
type effMe struct {
	epoch int
	n     int
}

// effGrants asks draal for the grants. Its answer has its number n.
type effGrants struct {
	epoch int
	n     int
}

// effGrant asks draal to grant a role. Its answer has its number n.
type effGrant struct {
	epoch int
	n     int
	req   wire.GrantRequest
}

// effRevoke asks draal to revoke a grant. Its answer has its number n.
type effRevoke struct {
	epoch int
	n     int
	req   wire.RevokeRequest
}

// The fields of the form, in the order they are in.
const (
	fieldRole = iota
	fieldID
	fieldService
	fieldNote
)

// formRoles are the roles the form grants, in the order the arrow keys
// go through them.
var formRoles = []wire.Role{wire.RoleAdmin, wire.RoleDeployer, wire.RoleZathras}

// grantForm is the form that grants: what was typed into it, and how far
// draal is with it.
type grantForm struct {
	field   int // the field the keys go to
	roleIdx int // which of formRoles
	id      string
	service string
	note    string
	asking  bool   // draal was asked and has not answered
	said    string // what draal refused, or why it was not asked; clean
}

// role returns the role the form has.
func (f *grantForm) role() wire.Role { return formRoles[f.roleIdx] }

// text returns the text of a field of text, to be changed.
func (f *grantForm) text(field int) *string {
	switch field {
	case fieldID:
		return &f.id
	case fieldService:
		return &f.service
	case fieldNote:
		return &f.note
	}
	return nil
}

// step moves to the next field (by 1) or to the one before (by -1), and
// not beyond the first and the last. An admin is of every service, and
// is not asked for one.
func (f *grantForm) step(by int) {
	next := f.field + by
	if next == fieldService && f.role() == wire.RoleAdmin {
		next += by
	}
	f.field = min(max(next, fieldRole), fieldNote)
}

// put adds text to the field the keys go to, as far as the field holds
// it. What is added is clean; into the ID no space gets, so that one
// that was copied with a line end, or in two lines, is the ID.
func (f *grantForm) put(text string) {
	into := f.text(f.field)
	if into == nil {
		return
	}
	// A megabyte that is pasted is not cleaned whole to keep some bytes.
	if len(text) > 4*formFieldCap {
		text = text[:4*formFieldCap]
	}
	text = clean(text)
	if f.field == fieldID {
		text = strings.Join(strings.Fields(text), "")
	}
	room := formFieldCap - len(*into)
	if len(text) > room {
		text = text[:max(room, 0)]
		for len(text) > 0 && !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	*into += text
}

// request returns what the form asks draal for: what was typed, without
// the spaces around it.
func (f *grantForm) request() wire.GrantRequest {
	req := wire.GrantRequest{ID: wire.ID(strings.TrimSpace(f.id)), Role: f.role(), Note: strings.TrimSpace(f.note)}
	if req.Role != wire.RoleAdmin {
		req.Service = strings.TrimSpace(f.service)
	}
	return req
}

// cappedGrant returns a grant with its words capped, as every word of
// draal's is where it comes in.
func cappedGrant(g wire.Grant) wire.Grant {
	g.ID, g.By = wire.ID(capped(string(g.ID))), wire.ID(capped(string(g.By)))
	g.Role, g.Service, g.Note = wire.Role(capped(string(g.Role))), capped(g.Service), capped(g.Note)
	return g
}

// cappedGrants returns the first grantsKept of a list, capped.
func cappedGrants(list []wire.Grant) []wire.Grant {
	out := make([]wire.Grant, 0, min(len(list), grantsKept))
	for _, g := range list[:min(len(list), grantsKept)] {
		out = append(out, cappedGrant(g))
	}
	return out
}

// askMe asks draal who the key is.
func (m *model) askMe() effect {
	m.meN++
	return effMe{epoch: m.epoch, n: m.meN}
}

// newMe takes draal's answer to the newest question who the key is. A
// refusal, and a question that failed, leave the key unknown and
// nothing else: a draal of before knows no such question.
func (m *model) newMe(msg meMsg) {
	if msg.epoch != m.epoch || msg.n != m.meN {
		return
	}
	if msg.err != nil {
		m.me, m.haveMe = wire.Identity{}, false
		return
	}
	m.me = wire.Identity{ID: wire.ID(capped(string(msg.me.ID))), Grants: cappedGrants(msg.me.Grants)}
	m.haveMe = true
}

// rolesWords says the roles of a key in few words: an admin is that and
// nothing else; of any other role, of which services it is.
func rolesWords(grants []wire.Grant) string {
	services := map[wire.Role][]string{}
	every := map[wire.Role]bool{}
	var roles []wire.Role
	for _, g := range grants {
		if g.Role == wire.RoleAdmin {
			return "admin"
		}
		if _, known := services[g.Role]; !known {
			services[g.Role] = nil
			roles = append(roles, g.Role)
		}
		if g.Service == "" {
			every[g.Role] = true
		} else if !slices.Contains(services[g.Role], g.Service) {
			services[g.Role] = append(services[g.Role], g.Service)
		}
	}
	if len(roles) == 0 {
		return "no role"
	}
	// The roles in the order of the three, and what else draal names
	// behind them.
	rank := func(r wire.Role) int {
		if i := slices.Index(formRoles, r); i >= 0 {
			return i
		}
		return len(formRoles)
	}
	slices.SortStableFunc(roles, func(a, b wire.Role) int { return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a, b)) })
	var parts []string
	for _, r := range roles {
		of := services[r]
		slices.Sort(of)
		word := clean(string(r))
		switch {
		case every[r]:
		case len(of) > 2:
			word += " of " + human.Count(len(of), "service", "services")
		default:
			word += " of " + clean(strings.Join(of, ", "))
		}
		parts = append(parts, word)
	}
	return strings.Join(parts, "; ")
}

// openAccess is the key a on the services: the view of the grants, which
// are asked for when there is a connection to ask over.
func (m *model) openAccess() []effect {
	m.screen = screenAccess
	m.chooseGrant()
	if m.conn != connLive {
		return nil
	}
	return []effect{m.askGrants()}
}

// askGrants asks draal for the grants. What was listed before stays in
// sight until the answer has come.
func (m *model) askGrants() effect {
	if m.grantsAre != askedHave {
		m.grantsAre = askedFor
	}
	m.grantsN++
	return effGrants{epoch: m.epoch, n: m.grantsN}
}

// newGrants takes the answer to the newest request for the grants,
// whatever view is in sight when it comes.
func (m *model) newGrants(msg grantsMsg) {
	if msg.epoch != m.epoch || msg.n != m.grantsN {
		return
	}
	if msg.err != nil {
		m.grants, m.grantsOf, m.grantsAre = nil, 0, askedRefused
		m.grantsErr, m.notAdmin = clean(capped(msg.err.Error())), false
		if e, ok := refusal(msg.err); ok && e.Code == wire.CodeUnauthorized {
			m.notAdmin = true
		}
		// A form or a question is of a list, and there is none now.
		m.form, m.asked = nil, nil
		return
	}
	m.grants, m.grantsOf = cappedGrants(msg.grants), len(msg.grants)
	m.grantsAre, m.grantsErr, m.notAdmin = askedHave, "", false
	m.chooseGrant()
}

// grantKey is what a grant is chosen by.
type grantKey struct {
	id      wire.ID
	role    wire.Role
	service string
}

// keyOfGrant returns what a grant is chosen by.
func keyOfGrant(g wire.Grant) grantKey {
	return grantKey{id: g.ID, role: g.Role, service: g.Service}
}

// chooseGrant keeps the grant that is chosen chosen by what it is. When
// it is gone, what is now at its place is chosen, or the last.
func (m *model) chooseGrant() {
	at := slices.IndexFunc(m.grants, func(g wire.Grant) bool { return keyOfGrant(g) == m.gKey })
	switch {
	case len(m.grants) == 0:
		m.gIdx = 0
	case at >= 0:
		m.gIdx = at
	default:
		m.gIdx = min(max(m.gIdx, 0), len(m.grants)-1)
		m.gKey = keyOfGrant(m.grants[m.gIdx])
	}
}

// moveGrant moves what is chosen in the list of the grants.
func (m *model) moveGrant(delta int) {
	if n := len(m.grants); n > 0 {
		m.gIdx = min(max(m.gIdx+delta, 0), n-1)
		m.gKey = keyOfGrant(m.grants[m.gIdx])
	}
}

// listed says whether the grants are there to be granted to and revoked
// from: draal has listed them to this key.
func (m *model) listed() bool {
	return m.grantsAre == askedHave
}

// grantWords says a grant in a few words: the role, of which service,
// and whose it is.
func grantWords(g wire.Grant, joint string) string {
	words := clean(string(g.Role))
	if g.Service != "" {
		words += " of " + clean(g.Service)
	}
	return words + " " + joint + " " + human.ShortID(wire.ID(clean(string(g.ID))))
}

// accessKey takes a key on the view of the grants that is no key of
// every view. It says whether the key was one of them.
func (m *model) accessKey(name string) (effs []effect, taken bool) {
	switch name {
	case "g":
		if m.listed() {
			m.form = &grantForm{roleIdx: slices.Index(formRoles, wire.RoleDeployer)}
		}
		return nil, true
	case "x":
		switch {
		case !m.listed() || m.gIdx >= len(m.grants):
		case m.revoking:
			m.notice = "A revocation is under way: draal has not answered it yet."
		case m.grants[m.gIdx].Fixed:
			m.notice = "This is an admin of draal's command line: no request revokes it."
		default:
			g := m.grants[m.gIdx]
			m.asked = &g
		}
		return nil, true
	}
	return nil, false
}

// answerQuestion takes the key that answers whether a grant is to be
// revoked: y revokes it, any other key leaves it, and does nothing
// else.
func (m *model) answerQuestion(name string) []effect {
	g := *m.asked
	m.asked = nil
	if name != "y" {
		return nil
	}
	if m.conn != connLive {
		m.notice = "There is no connection to draal: nothing was revoked."
		return nil
	}
	m.revokeN++
	m.revoking, m.revokingOf = true, g
	return []effect{effRevoke{epoch: m.epoch, n: m.revokeN, req: wire.RevokeRequest{ID: g.ID, Role: g.Role, Service: g.Service}}}
}

// newRevoked takes draal's answer to the revocation that is under way.
func (m *model) newRevoked(msg revokedMsg) []effect {
	if msg.epoch != m.epoch || msg.n != m.revokeN || !m.revoking {
		return nil
	}
	m.revoking = false
	if msg.err != nil {
		m.notice = "Not revoked: " + clean(capped(msg.err.Error()))
		return nil
	}
	m.notice = "Revoked: " + grantWords(m.revokingOf, "from") + "."
	return []effect{m.askGrants()}
}

// formKey takes a key while the form is open. Every key is the form's
// then, but Ctrl-C.
func (m *model) formKey(msg tea.KeyPressMsg) []effect {
	f := m.form
	name := msg.String()
	switch {
	case name == "ctrl+c":
		return []effect{effQuit{}}
	case name == "esc":
		// A request that is under way is nobody's from here on.
		m.form = nil
		m.grantN++
		return nil
	case f.asking:
		return nil
	}
	switch name {
	case "enter":
		if f.field != fieldNote {
			f.step(1)
			return nil
		}
		return m.submit()
	case "tab", "down":
		f.step(1)
	case "shift+tab", "up":
		f.step(-1)
	case "left", "right":
		if f.field == fieldRole {
			by := 1
			if name == "left" {
				by = len(formRoles) - 1
			}
			f.roleIdx = (f.roleIdx + by) % len(formRoles)
		}
	case "backspace":
		if into := f.text(f.field); into != nil && *into != "" {
			_, size := utf8.DecodeLastRuneInString(*into)
			*into = (*into)[:len(*into)-size]
		}
	default:
		if msg.Text != "" && msg.Mod&^tea.ModShift == 0 {
			f.put(msg.Text)
		}
	}
	return nil
}

// submit asks draal for what the form holds, when there is a connection
// to ask over.
func (m *model) submit() []effect {
	f := m.form
	if m.conn != connLive {
		f.said = "There is no connection to draal: Enter asks it when the connection is back."
		return nil
	}
	f.asking, f.said = true, ""
	m.grantN++
	return []effect{effGrant{epoch: m.epoch, n: m.grantN, req: f.request()}}
}

// newGranted takes draal's answer to what the form asked for: the form
// goes when the role was granted, and stays as it was filled when draal
// refuses.
func (m *model) newGranted(msg grantedMsg) []effect {
	f := m.form
	if f == nil || !f.asking || msg.epoch != m.epoch || msg.n != m.grantN {
		return nil
	}
	f.asking = false
	if msg.err != nil {
		f.said = "draal refuses: " + clean(capped(msg.err.Error()))
		if e, ok := refusal(msg.err); ok {
			f.said = "draal refuses: " + clean(capped(e.Error()))
		}
		return nil
	}
	req := f.request()
	m.form = nil
	m.notice = "Granted: " + grantWords(wire.Grant{ID: req.ID, Role: req.Role, Service: req.Service}, "to") + "."
	return []effect{m.askGrants()}
}

// accessLost takes the end of the connection for what the view of the
// grants had asked of it: nothing of it is answered any more.
func (m *model) accessLost() {
	if m.grantsAre == askedFor {
		m.grantsAre = askedNot
	}
	if f := m.form; f != nil && f.asking {
		f.asking = false
		f.said = "The connection to draal was lost before it answered: whether the role was granted is in the list when the connection is back."
	}
	if m.revoking {
		m.revoking = false
		m.notice = "The connection to draal was lost before it answered: whether the grant was revoked is in the list when the connection is back."
	}
}

// accessDue asks for the grants when the view is open and the
// connection is new, or the model was told nothing for a while.
func (m *model) accessDue() []effect {
	if m.screen != screenAccess || m.conn != connLive {
		return nil
	}
	return []effect{m.askGrants()}
}
