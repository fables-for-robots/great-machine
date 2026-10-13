package tui

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// The view of who may do what: the grants as a table, a line for each
// as `great-machine access list` prints them, and below the table the
// grant that is chosen, whole. The form that grants is drawn in the
// place of the table while it is open.

const (
	// accessTop is how many rows stand above the grants: how many there
	// are, an empty row and the head of the table.
	accessTop = 3
	// accessDetail is how many rows the grant that is chosen has below
	// the table, an empty one among them, in a terminal that has them.
	accessDetail = 6
)

// detailRows returns how many rows the grant that is chosen has below
// the table: none in a terminal that needs its rows for the table.
func (m *model) detailRows() int {
	if m.bodyHeight() < accessTop+accessDetail+4 {
		return 0
	}
	return accessDetail
}

// accessHeight returns how many grants of the list are in sight.
func (m *model) accessHeight() int {
	return max(m.bodyHeight()-accessTop-m.detailRows(), 1)
}

// accessView draws the grants, or why there are none to draw, in at
// most h lines.
func (m *model) accessView(h int) []string {
	w := m.width
	switch {
	case m.form != nil:
		return m.formView()
	case m.notAdmin:
		return m.notAdminView()
	case m.grantsAre == askedRefused:
		return append([]string{""}, sentences(w, stBad, "draal does not list the grants: "+m.grantsErr)...)
	case m.grantsAre == askedFor:
		return []string{"", line(w, plainText(" Asking draal who may do what …"))}
	case m.grantsAre == askedNot:
		return append([]string{""}, sentences(w, stPlain, "There is no connection to draal: who may do what is asked for when it is back.")...)
	case len(m.grants) == 0:
		out := []string{"", line(w, bold(" draal has no grant on record."))}
		return append(out, sentences(w, stDim, "Who is an admin without one is an admin of draal's command line: great-machine draal --admin ID")...)
	}

	shown := m.accessHeight()
	count := human.Count(len(m.grants), "grant", "grants")
	if m.grantsOf > len(m.grants) {
		count = fmt.Sprintf("the first %d of %d grants", len(m.grants), m.grantsOf)
	}
	if len(m.grants) > shown {
		count += fmt.Sprintf(" · %d to %d are in sight", m.gTop+1, min(m.gTop+shown, len(m.grants)))
	}
	out := []string{line(w, dim(" "+count)), ""}

	// The columns. The note has what is left of the width; in a terminal
	// that is wide enough the ID is whole, and in one that is narrow who
	// granted is left to the lines below the table.
	const roleW = 8
	svcW := width("SERVICE")
	for _, g := range m.grants {
		svcW = max(svcW, width(serviceWords(g)))
	}
	svcW = min(svcW, 16)
	grantedW, byWhom := 20, true
	if w < 72 {
		grantedW, byWhom = 8, false
	}
	idW := 8
	if w >= 2+roleW+2+svcW+2+64+2+12+2+grantedW {
		idW = 64
	}
	noteW := w - (2 + roleW + 2 + svcW + 2 + idW + 2 + 2 + grantedW)
	if noteW < 6 {
		noteW = 0
	}
	cells := func(mark, role, service, id, note, granted string) []seg {
		row := []seg{plainText(mark + pad(role, roleW) + "  " + pad(service, svcW) + "  " + pad(id, idW) + "  ")}
		if noteW > 0 {
			row = append(row, plainText(pad(note, noteW)+"  "))
		}
		return append(row, dim(granted))
	}
	head := cells("  ", "ROLE", "SERVICE", "ID", "NOTE", "GRANTED")
	for i := range head {
		head[i].st = stDim
	}
	out = append(out, line(w, head...))

	now := m.cfg.Now()
	for i := m.gTop; i < min(m.gTop+shown, len(m.grants)); i++ {
		g := m.grants[i]
		id := human.ShortID(g.ID)
		if idW == 64 {
			id = string(g.ID)
		}
		if i == m.gIdx {
			out = append(out, chosen(w, cells("▸ ", string(g.Role), serviceWords(g), id, g.Note, grantedWords(g, now, byWhom))...))
		} else {
			out = append(out, line(w, cells("  ", string(g.Role), serviceWords(g), id, g.Note, grantedWords(g, now, byWhom))...))
		}
	}
	if rows := m.detailRows(); rows > 0 && m.gIdx < len(m.grants) {
		out = append(out, m.grantDetail(m.grants[m.gIdx], rows)...)
	}
	return out
}

// serviceWords says of which service a grant is.
func serviceWords(g wire.Grant) string {
	if g.Service == "" {
		return "every"
	}
	return clean(g.Service)
}

// grantedWords says when a grant was made and, where there is room, by
// whom: the beginning of the ID of the admin. An admin of the command
// line was granted by nobody.
func grantedWords(g wire.Grant, now time.Time, byWhom bool) string {
	switch {
	case g.Fixed && byWhom:
		return "draal's command line"
	case g.Fixed:
		return "by draal"
	case !byWhom || g.By == "":
		return human.Ago(g.At, now)
	case g.At.IsZero():
		return "by " + human.ShortID(g.By)
	}
	return human.Ago(g.At, now) + " by " + human.ShortID(g.By)
}

// grantDetail draws the grant that is chosen in at most rows lines, the
// first of them empty: its ID whole, its note, when it was granted and
// by whom. The ID comes first and is never cut: it is what a grant is
// revoked by, here or with `great-machine access revoke`.
func (m *model) grantDetail(g wire.Grant, rows int) []string {
	const label = 10
	w := m.width
	labelled := func(name string, lines []string, st lipgloss.Style) []string {
		var out []string
		for i, l := range lines {
			if i > 0 {
				name = ""
			}
			out = append(out, line(w, dim(" "+pad(name, label-1)), seg{l, st}))
		}
		return out
	}
	out := []string{""}
	out = append(out, labelled("id", wrap(string(g.ID), w-label, 2), stPlain)...)
	left := rows - len(out)
	if g.Fixed {
		words := sentences(w, stDim, "An admin of draal's command line: no request revokes it.")
		return append(out, words[:min(len(words), max(left, 0))]...)
	}
	note, noteSt := wrap(g.Note, w-label, 2), stPlain
	if len(note) == 0 {
		note, noteSt = []string{"none"}, stDim
	}
	now := m.cfg.Now()
	when := whenWords(g.At, now)
	by := wrap(string(g.By), w-label, 1)
	// The note has two lines when there is the room for them; who granted
	// has a line of its own when there is one, and is otherwise said by
	// the beginning of its ID behind the time.
	merged := len(by) == 0 || len(note)+2 > left
	if merged {
		note = note[:min(len(note), max(left-1, 0))]
		if len(by) > 0 {
			when += " by " + human.ShortID(g.By)
		}
	}
	out = append(out, labelled("note", note, noteSt)...)
	out = append(out, labelled("granted", []string{when}, stPlain)...)
	if !merged {
		out = append(out, labelled("by", by, stPlain)...)
	}
	return out[:min(len(out), rows)]
}

// notAdminView is the view of a key that draal does not list the grants
// to: whose view it is, and the ID of the key, whole, which is what an
// admin grants a role to.
func (m *model) notAdminView() []string {
	w := m.width
	out := []string{"", line(w, bold(" This view is an admin's."))}
	if !m.haveMe {
		out = append(out, sentences(w, stPlain, "This key is no admin. Its endpoint ID, which is what an admin grants a role to, is what great-machine id prints.")...)
	} else {
		holds := "This key is no admin: it is " + rolesWords(m.me.Grants) + "."
		if len(m.me.Grants) == 0 {
			holds = "This key is no admin: it holds no role."
		}
		out = append(out, sentences(w, stPlain, holds+" Its endpoint ID, which is what an admin grants a role to:")...)
		for _, l := range wrap(string(m.me.ID), w-4, 4) {
			out = append(out, line(w, plainText("   "), bold(l)))
		}
	}
	out = append(out, "")
	out = append(out, sentences(w, stDim, "Who may do what is listed, granted and revoked here by a key that holds the role admin. An admin grants a role with:")...)
	return append(out, line(w, dim("   great-machine access grant ROLE ID --service NAME")))
}

// formView draws the form that grants: its four fields, what draal said
// of it, and how it is used.
func (m *model) formView() []string {
	const label, short = 9, 12
	f, w := m.form, m.width
	room := w - 3 - label
	field := func(n int, name, value, hint string, valueSt lipgloss.Style) string {
		mark := "   "
		if f.field == n {
			mark = " ▸ "
			if n != fieldRole {
				// The end of what was typed is in sight, with the place
				// of the next character behind it.
				if width(value) > room-1 {
					tail, _ := tailClean(clean(value), max(room-2, 0))
					value = ellipsis + tail
				}
				value = clean(value) + "_"
			}
		}
		segs := []seg{plainText(mark), dim(pad(name, label))}
		if hint == "" || width(value) > short {
			return line(w, append(segs, seg{value, valueSt})...)
		}
		return line(w, append(segs, seg{pad(value, short), valueSt}, dim("   "+hint))...)
	}
	out := []string{line(w, bold(" Grant a role")), ""}
	out = append(out, field(fieldRole, "role", "‹ "+string(f.role())+" ›", "of admin, deployer, zathras", stBold))
	out = append(out, field(fieldID, "id", f.id, "an endpoint ID, as great-machine id prints one", stPlain))
	if f.role() == wire.RoleAdmin {
		out = append(out, line(w, plainText("   "), dim(pad("service", label)), dim("every: an admin is of every service")))
	} else {
		out = append(out, field(fieldService, "service", f.service, "empty: every service", stPlain))
	}
	out = append(out, field(fieldNote, "note", f.note, "", stPlain), "")
	switch {
	case f.asking:
		out = append(out, line(w, plainText(" Asking draal …")), "")
	case f.said != "":
		out = append(out, sentences(w, stBad, f.said)...)
		out = append(out, "")
	}
	return append(out, sentences(w, stDim, "Enter goes to the next field, and on the note it asks draal. ← → change the role. Esc gives the form up.")...)
}
