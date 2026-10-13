package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// What is drawn: the model as text. A view is a list of lines; view
// puts the header above the one that is open and the keys below it.

const (
	// leastWidth and leastHeight are the smallest terminal the terminal
	// UI draws in. In a smaller one it says so, in one line.
	leastWidth  = 50
	leastHeight = 12
)

// shared is how the rows of a view are shared out among its parts, kept
// from when it was worked out to the next message: the model asks for
// it when it keeps what is chosen in sight, and the view draws by it.
type shared struct {
	service  *serviceParts
	serviceH int // the rows service was worked out for
	zathras  *zathrasParts
	zathrasH int // the rows zathras was worked out for
}

// tooSmall says whether the terminal is smaller than the terminal UI
// can draw in.
func (m *model) tooSmall() bool {
	return m.width < leastWidth || m.height < leastHeight
}

// view draws the model: as many lines as the terminal has rows at most,
// none wider than the terminal.
func (m *model) view() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if m.tooSmall() {
		return line(m.width, plainText(fmt.Sprintf("Too small: great-machine needs a terminal of %d×%d or more.", leastWidth, leastHeight)))
	}
	lines := []string{m.header()}
	lines = append(lines, m.banner()...)
	body := m.body(m.bodyHeight())
	if len(body) > m.bodyHeight() {
		body = body[:m.bodyHeight()]
	}
	lines = append(lines, body...)
	for len(lines) < m.height-1-len(m.noticeLines()) {
		lines = append(lines, "")
	}
	lines = append(lines, m.noticeLines()...)
	lines = append(lines, m.keys())
	for i, l := range lines {
		// Every line is made by line and its like, which keep to the
		// width as this package counts it (runeWidth). The renderer
		// counts by graphemes or by characters, as the terminal has it,
		// and for some scripts either count is another than ours. No
		// line is wider than the terminal by any of the three: one that
		// is, by the renderer's counts, is cut here.
		for to := m.width; to >= 0; to-- {
			g, wc := ansi.StringWidth(l), ansi.StringWidthWc(l)
			if g <= m.width && wc <= m.width {
				break
			}
			// Cut by graphemes, to as many cells fewer as the larger
			// count is over.
			to = min(to, g-(max(g, wc)-m.width))
			l = ansi.Truncate(l, max(to, 0), "")
		}
		lines[i] = l
	}
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
}

// bodyHeight returns how many rows the view that is open has: the
// terminal without the header, the keys, and what is said between.
func (m *model) bodyHeight() int {
	return max(m.height-2-len(m.banner())-len(m.noticeLines()), 0)
}

// body draws the view that is open in at most h lines.
func (m *model) body(h int) []string {
	switch {
	case m.help:
		return m.helpView(h)
	case !m.have:
		return m.connecting()
	}
	switch m.screen {
	case screenService:
		return m.serviceView(h)
	case screenZathras:
		return m.zathrasView(h)
	case screenAccess:
		return m.accessView(h)
	}
	return m.servicesView(h)
}

// header draws the first line: where one is, who the key is, draal, and
// the state of the connection.
func (m *model) header() string {
	conn := m.connWords()
	if m.cfg.Draal != "" && m.width >= 70 {
		conn = "draal " + cut(m.cfg.Draal, 12) + " · " + conn
	}
	// The right part has half of the line, and of the other half what
	// the left part does not need: who the key is has its place where
	// one is said in few words, and gives way where it is not.
	room := max(m.width/2, m.width-width(m.where(m.width))-4)
	for _, who := range m.whoWords() {
		if width(who+" · "+conn) <= room {
			conn = who + " · " + conn
			break
		}
	}
	conn = " " + cut(conn, room) + " "
	left := m.width - width(conn)
	return stTitle.Render(pad(" "+m.where(left-2), left)) + stBar.Render(conn)
}

// whoWords says who the key is in the ways the header may: the beginning
// of its ID with its roles and of which services they are, and with its
// roles alone. There is none while draal has not said.
func (m *model) whoWords() []string {
	if !m.haveMe {
		return nil
	}
	id := human.ShortID(m.me.ID)
	short := make([]wire.Grant, 0, len(m.me.Grants))
	for _, g := range m.me.Grants {
		short = append(short, wire.Grant{Role: g.Role})
	}
	return []string{id + " " + rolesWords(m.me.Grants), id + " " + rolesWords(short)}
}

// where says where one is, in at most w cells: the program, the service
// and the zathras. Where there is no room for all of it, the names give
// way, the one of the zathras first, and never the start of the line.
func (m *model) where(w int) string {
	const root, sep = "great-machine", " › "
	service, zathras := named(m.service), named(m.seen.Name)
	switch m.screen {
	case screenServices:
		return cut(root, w)
	case screenAccess:
		return cut(root+sep+"access", w)
	case screenService:
		return cut(root+sep+middle(service, max(w-width(root+sep), 8)), w)
	}
	rest := w - width(root+sep+sep)
	service = middle(service, max(rest-8, 8))
	return cut(root+sep+service+sep+middle(zathras, max(rest-width(service), 8)), w)
}

// connWords says the state of the connection, in a sign and words.
func (m *model) connWords() string {
	now := m.cfg.Now()
	switch m.conn {
	case connLive:
		return "● live"
	case connLost:
		return "✖ lost for " + human.Age(now.Sub(m.since))
	}
	return "◌ connecting"
}

// retryWords says when draal is asked again.
func (m *model) retryWords() string {
	if m.retryAt.IsZero() {
		return "Asking draal now."
	}
	until := human.Until(m.retryAt, m.cfg.Now())
	if until == "now" {
		return "Asking draal again now."
	}
	return "Asking draal again " + until + "; any key asks now."
}

// banner is what stands under the header while there is no connection
// and a fleet is shown all the same: that it is old, since when, and
// when draal is asked again.
func (m *model) banner() []string {
	if m.conn == connLive || !m.have || m.tooSmall() {
		return nil
	}
	text := "✖ The connection to draal is lost since " + human.Clock(m.since) + ". What is shown is what draal sent last, " +
		human.Ago(m.fleetAt, m.cfg.Now()) + ". " + m.retryWords()
	if m.dialErr != "" {
		text += " (" + m.dialErr + ")"
	}
	var out []string
	for _, l := range wrap(text, m.width-2, 3) {
		out = append(out, line(m.width, seg{" " + l, stBad}))
	}
	return out
}

// noticeLines is what is said once, when there is something: a line,
// or as many as three when it is long, as the error of a shell may be.
// The question whether a grant is to be revoked is said there too: the
// next key answers it.
func (m *model) noticeLines() []string {
	notice := m.notice
	if m.asked != nil {
		notice = "Revoke " + grantWords(*m.asked, "from") + "? y revokes it; any other key leaves it."
	}
	if notice == "" || m.tooSmall() {
		return nil
	}
	var out []string
	for _, l := range wrap(notice, m.width-2, 3) {
		out = append(out, line(m.width, seg{" " + l, stWarn}))
	}
	return out
}

// keys draws the last line: the keys that work here. In a terminal
// that has no room for all of them, the ones that are said first stay:
// how to quit, the key that says the rest, and the way back.
func (m *model) keys() string {
	type key struct {
		key, does string
		rank      int // the lower, the longer it stays
	}
	var keys []key
	switch {
	case m.sh.stage == shellOpening:
		// No other key does anything while a shell is being opened.
		keys = []key{{"esc", "give the shell up", 0}}
	case m.sh.stage == shellOpen:
		keys = nil
	case m.help:
		keys = []key{{"any key", "back", 1}, {"q", "quit", 0}}
	case m.form != nil:
		// Every other key is a letter of the form, q among them.
		keys = []key{{"enter", "next", 2}, {"←→", "the role", 3}, {"esc", "give it up", 1}, {"ctrl-c", "quit", 0}}
	case m.asked != nil:
		keys = []key{{"y", "revoke", 1}, {"any other key", "leave it", 2}, {"q", "quit", 0}}
	case m.screen == screenAccess && !m.listed():
		// Without a list there is nothing to move in, to grant to or to
		// revoke from.
		keys = []key{{"esc", "back", 2}, {"?", "keys", 1}, {"q", "quit", 0}}
	case m.screen == screenAccess:
		keys = []key{{"↑↓", "move", 5}, {"g", "grant", 4}, {"x", "revoke", 3}, {"esc", "back", 2}, {"?", "keys", 1}, {"q", "quit", 0}}
		// An admin of draal's command line has no x, and neither has a
		// list without a grant.
		if m.gIdx >= len(m.grants) || m.grants[m.gIdx].Fixed {
			keys = []key{{"↑↓", "move", 4}, {"g", "grant", 3}, {"esc", "back", 2}, {"?", "keys", 1}, {"q", "quit", 0}}
		}
	case m.screen == screenServices:
		keys = []key{{"↑↓", "move", 4}, {"enter", "open", 3}, {"a", "access", 2}, {"?", "keys", 1}, {"q", "quit", 0}}
	case m.screen == screenService:
		keys = []key{{"↑↓", "move", 4}, {"enter", "open", 3}, {"esc", "back", 2}, {"?", "keys", 1}, {"q", "quit", 0}}
	default:
		keys = []key{{"↑↓", "scroll the log", 5}, {"G", "follow it", 4}, {"s", "shell", 3}, {"esc", "back", 2}, {"?", "keys", 1}, {"q", "quit", 0}}
	}
	room, most := m.width-1, -1
	for rank := 0; rank < len(keys); rank++ {
		for _, k := range keys {
			if k.rank == rank {
				room -= width(k.key) + 1 + width(k.does) + 2
			}
		}
		if room+2 < 0 {
			break
		}
		most = rank
	}
	segs := []seg{plainText(" ")}
	for _, k := range keys {
		if k.rank > most {
			continue
		}
		if len(segs) > 1 {
			segs = append(segs, dim("  "))
		}
		segs = append(segs, bold(k.key), dim(" "+k.does))
	}
	return line(m.width, segs...)
}

// connecting is the view before the first fleet: whom the terminal UI
// asks, and why there is nothing yet.
func (m *model) connecting() []string {
	draal := "draal"
	if m.cfg.Draal != "" {
		draal += " " + m.cfg.Draal
	}
	out := []string{"", line(m.width, plainText(" Connecting to "+draal+" …"))}
	if m.dialErr == "" {
		return out
	}
	out = append(out, "")
	for _, l := range wrap("draal does not answer: "+m.dialErr, m.width-2, 4) {
		out = append(out, line(m.width, seg{" " + l, stBad}))
	}
	return append(out, line(m.width, dim(" "+m.retryWords())))
}

// helpView is the keys, and what the signs mean.
func (m *model) helpView(h int) []string {
	rows := [][2]string{
		{"↑ ↓  k j", "move in a list; scroll a log"},
		{"Enter  →  l", "open what is chosen"},
		{"Esc  ←  h  Backspace", "back"},
		{"PgUp PgDn", "a page up, a page down"},
		{"g  G", "the start, the end; in a log G also follows it again"},
		{"s", "on a zathras: open a shell"},
		{"a", "on the services: who may do what (g grants, x revokes)"},
		{"?", "these keys"},
		{"q  Ctrl-C", "quit"},
	}
	out := []string{"", line(m.width, bold(" Keys")), ""}
	for _, r := range rows {
		out = append(out, line(m.width, plainText("   "), bold(pad(r[0], 22)), plainText(r[1])))
	}
	if h < len(out)+7 {
		return out
	}
	sign := func(s string, st lipgloss.Style, words string) []seg {
		return []seg{{s, st}, plainText(" " + words + "   ")}
	}
	phases := []seg{plainText("   ")}
	phases = append(phases, sign("●", stOK, "running")...)
	phases = append(phases, sign("◐", stWarn, "stopping")...)
	phases = append(phases, sign("✖", stBad, "backoff")...)
	phases = append(phases, sign("○", stDim, "idle")...)
	rollouts := []seg{plainText("   ")}
	rollouts = append(rollouts, sign("▶", stBusy, "under way")...)
	rollouts = append(rollouts, sign("✔", stOK, "completed")...)
	rollouts = append(rollouts, sign("✖", stBad, "halted")...)
	rollouts = append(rollouts, sign("–", stDim, "superseded")...)
	streams := []seg{plainText("   ")}
	streams = append(streams, sign("o", stDim, "standard output")...)
	streams = append(streams, sign("e", stBad, "standard error")...)
	streams = append(streams, sign("z", stBusy, "zathras itself")...)
	return append(out, "",
		line(m.width, bold(" Signs")), "",
		line(m.width, phases...),
		line(m.width, rollouts...),
		line(m.width, streams...),
	)
}

// sentences draws text as lines of words, with a margin, in the look
// given: what a view says when it has nothing to show.
func sentences(w int, st lipgloss.Style, text string) []string {
	var out []string
	for _, l := range wrap(text, w-2, 0) {
		out = append(out, line(w, seg{" " + l, st}))
	}
	return out
}
