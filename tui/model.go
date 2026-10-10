package tui

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/fables-for-robots/great-machine/wire"
)

// screen is one of the three views.
type screen int

const (
	// screenServices lists every service.
	screenServices screen = iota
	// screenService is one service: its zathrases and its rollouts.
	screenService
	// screenZathras is one zathras: its state, its figures and its log.
	screenZathras
)

// connState is the state of the connection to draal.
type connState int

const (
	// connConnecting: draal was never heard of yet.
	connConnecting connState = iota
	// connLive: a fleet has come over the connection there is.
	connLive
	// connLost: there was a connection, and it ended.
	connLost
)

// asked is how far something is that draal is asked for.
type asked int

const (
	// askedNot: it was not asked for: there is no connection.
	askedNot asked = iota
	// askedFor: draal was asked and has not answered.
	askedFor
	// askedHave: draal has answered.
	askedHave
	// askedRefused: draal has refused.
	askedRefused
	// askedEnded: of a log: it was followed and has ended.
	askedEnded
)

// The messages by which the program tells the model things. Each but
// tickMsg has the number of the connection it is of: the model counts
// the connections (epoch), and what is of an older one than the one it
// waits for is dropped unread.

// dialedMsg says that Dial has connected and the watch was begun.
type dialedMsg struct{ epoch int }

// dialFailedMsg says that Dial, or the beginning of the watch, failed,
// or that the two had not answered within ConnectWait.
type dialFailedMsg struct {
	epoch int
	err   error
}

// fleetMsg is a fleet of the watch.
type fleetMsg struct {
	epoch int
	fleet wire.Fleet
}

// lostMsg says that the connection is lost: the watch has ended, or a
// request failed with something that is no refusal of draal's.
type lostMsg struct {
	epoch int
	err   error // nil when the watch just ended
}

// rolloutsMsg answers effRollouts: the rollouts, or draal's refusal. n
// is the number of the request it answers.
type rolloutsMsg struct {
	epoch    int
	n        int
	service  string
	rollouts []wire.RolloutInfo
	err      error
}

// historyMsg answers effHistory: the samples, or draal's refusal. n is
// the number of the request it answers.
type historyMsg struct {
	epoch   int
	n       int
	zathras wire.ID
	samples []wire.Sample
	err     error
}

// logMsg is lines of the log that effFollow with this id asked for: at
// most logKept of them, and one message every logEvery at most. Until
// the first one, the model knows only that the log was asked for: draal
// answers a log with its lines alone, and a log without a line has not
// answered.
type logMsg struct {
	epoch int
	id    int
	lines []wire.LogLine
}

// logEndedMsg says that the log of this id has ended: err is draal's
// refusal, or nil when the lines just ended.
type logEndedMsg struct {
	epoch int
	id    int
	err   error
}

// retryMsg says that the pause effWait asked for with this number is
// over.
type retryMsg struct{ n int }

// tickMsg comes every second: the times that are drawn move on.
type tickMsg struct{}

// effect is something the model wants done. The program does it.
type effect any

// effDial asks for Dial and, on what it returns, Watch. Everything the
// program says of that connection has this epoch. It answers with
// dialedMsg and then fleetMsg after fleetMsg, or with dialFailedMsg; and
// with lostMsg when the watch ends.
type effDial struct{ epoch int }

// effHangUp asks for the connection there is to be closed, and
// everything that was asked of it to be ended.
type effHangUp struct{}

// effWait asks for retryMsg{n} after the pause.
type effWait struct {
	pause time.Duration
	n     int
}

// effRollouts asks draal for the rollouts of a service. Its answer has
// its number n: two requests may be under way, and their answers may
// come in any order.
type effRollouts struct {
	epoch   int
	n       int
	service string
}

// effHistory asks draal for the samples of a zathras. Its answer has its
// number n.
type effHistory struct {
	epoch   int
	n       int
	zathras wire.ID
}

// effFollow asks for the log of a zathras to be followed, in place of
// the one that is followed now. Its lines come as logMsg with this id.
type effFollow struct {
	epoch   int
	zathras wire.ID
	id      int
}

// effUnfollow asks for the log that is followed to be ended.
type effUnfollow struct{}

// effQuit ends the program: the user has quit.
type effQuit struct{}

// effFail ends the program with an error: draal refuses this operator.
type effFail struct{ err error }

// rolloutKey is what of a service's last rollout its list of rollouts
// was asked for: when it changes, the list is asked for again.
type rolloutKey struct {
	id    uint64
	state wire.RolloutState
}

// logRow is a row of the log as it is drawn: a line of the log is one
// row, or several when it is longer than the terminal is wide.
type logRow struct {
	entry int    // which of the buffer's entries, as they are now
	seq   uint64 // the number of that entry
	sub   int    // which row of its entry
	spans []span // of a line: what of it is in this row
}

// logRowsPerLine is how many rows one line of a log may take. What is
// longer ends in an ellipsis.
const logRowsPerLine = 4

// model is the state of the terminal UI. It is changed by update alone,
// which is a function of the model and a message: it reads no clock but
// the one it was given, and does nothing but return effects.
type model struct {
	cfg Config

	width, height int

	// The connection.
	conn       connState
	epoch      int           // the number of the connection that is dialed or there
	everDialed bool          // Dial has succeeded once
	since      time.Time     // when the connection was lost, or first asked for
	pause      time.Duration // the pause before the next dial; 0 before the first failure
	retryAt    time.Time     // when the pause is over; the zero time while a dial is under way
	waitN      int           // the number of the pause that counts
	dialErr    string        // why the last dial failed; clean
	fatal      error         // why the program ends, when it does not end by a key

	// The fleet, as it came last: services by name, zathrases by name
	// and then by ID.
	fleet   wire.Fleet
	have    bool      // a fleet has come, ever
	fleetAt time.Time // when the last one came

	screen screen
	help   bool
	notice string     // one line that is said once: gone with the next key
	sh     shellState // the shell that was asked for, if one was

	// screenServices: what is chosen, by name, and where it was.
	svcName string
	svcIdx  int
	svcTop  int

	// screenService: the service, and of its zathrases what is chosen.
	service      string
	zID          wire.ID
	zIdx         int
	zTop         int
	rollouts     []wire.RolloutInfo
	rolloutsAre  asked
	rolloutsErr  string // draal's refusal; clean
	rolloutsOf   rolloutKey
	rolloutsN    int       // the number of the request whose answer counts: the newest
	rolloutsFrom time.Time // when they came

	// screenZathras: the zathras, as it was seen last.
	zathras   wire.ID
	seen      wire.Zathras
	seenIn    string // its service
	present   bool   // it is in the fleet that came last
	goneSince time.Time
	samples   []wire.Sample
	history   asked
	historyN  int    // the number of the request whose answer counts: the newest
	histErr   string // draal's refusal; clean
	log       *logBuffer
	logID     int // the number of the log that is followed
	logIs     asked
	rows      []logRow
	follow    bool // the end of the log is kept in sight
	logTop    int  // the first row in sight, when follow is not set

	// shared is how the rows of the view that is open are shared out
	// among its parts. It is worked out once for a message, by whoever
	// asks first, and thrown away with the next.
	shared shared
}

// newModel returns the model of a terminal UI that has not begun.
func newModel(cfg Config) *model {
	return &model{cfg: cfg.withDefaults(), log: newLogBuffer(), follow: true}
}

// start is what the model wants at the beginning: the first dial.
func (m *model) start() []effect {
	m.conn, m.since = connConnecting, m.cfg.Now()
	return []effect{m.dial()}
}

// dial begins a new connection.
func (m *model) dial() effect {
	m.epoch++
	m.retryAt = time.Time{}
	return effDial{epoch: m.epoch}
}

// wait begins a pause, after which a dial is tried again.
func (m *model) wait() effect {
	m.waitN++
	m.retryAt = m.cfg.Now().Add(m.pause)
	return effWait{pause: m.pause, n: m.waitN}
}

// refusal returns the *wire.Error in err's chain: what draal answered,
// as against a connection that failed.
func refusal(err error) (*wire.Error, bool) {
	var e *wire.Error
	ok := errors.As(err, &e)
	return e, ok
}

// sortFleet returns the fleet in the order the terminal UI keeps it:
// services by name, zathrases by name and then by ID. draal sends it so;
// what is chosen must not depend on that. What it says in words is
// capped on the way (see capped), but for the name of a service and the
// ID of a zathras, which are what draal is asked by.
func sortFleet(f wire.Fleet) wire.Fleet {
	out := wire.Fleet{Services: slices.Clone(f.Services)}
	slices.SortStableFunc(out.Services, func(a, b wire.Service) int { return cmp.Compare(a.Name, b.Name) })
	for i := range out.Services {
		if r := out.Services[i].Rollout; r != nil {
			last := *r
			last.Reason, last.State = capped(last.Reason), wire.RolloutState(capped(string(last.State)))
			out.Services[i].Rollout = &last
		}
		zs := slices.Clone(out.Services[i].Zathrases)
		for j := range zs {
			zs[j].Name = capped(zs[j].Name)
			zs[j].State = cappedState(zs[j].State)
		}
		slices.SortStableFunc(zs, func(a, b wire.Zathras) int {
			return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
		})
		out.Services[i].Zathrases = zs
	}
	return out
}

// cappedState returns what a zathras reports with its words capped: its
// phase, its binary, its arguments (64 of them at most), and how it ended last.
func cappedState(st wire.State) wire.State {
	st.Phase = wire.Phase(capped(string(st.Phase)))
	st.Binary = capped(st.Binary)
	if len(st.Args) > 0 {
		args := make([]string, 0, min(len(st.Args), 65))
		for i, a := range st.Args {
			if i == 64 {
				args = append(args, fmt.Sprintf("… (%d arguments more)", len(st.Args)-64))
				break
			}
			args = append(args, capped(a))
		}
		st.Args = args
	}
	if st.LastExit != nil {
		exit := *st.LastExit
		exit.Signal, exit.Error = capped(exit.Signal), capped(exit.Error)
		st.LastExit = &exit
	}
	return st
}

// serviceNamed returns the service of that name in the fleet, if it is
// there.
func (m *model) serviceNamed(name string) (wire.Service, bool) {
	for _, svc := range m.fleet.Services {
		if svc.Name == name {
			return svc, true
		}
	}
	return wire.Service{}, false
}

// find returns a zathras of the fleet and the name of its service.
func (m *model) find(id wire.ID) (wire.Zathras, string, bool) {
	for _, svc := range m.fleet.Services {
		for _, z := range svc.Zathrases {
			if z.ID == id {
				return z, svc.Name, true
			}
		}
	}
	return wire.Zathras{}, "", false
}

// update takes a message and returns what the model wants done.
func (m *model) update(msg tea.Msg) []effect {
	m.shared = shared{}
	effs := m.take(msg)
	m.shared = shared{}
	m.keepInSight()
	return effs
}

// take is update, but for keeping what is chosen in sight.
func (m *model) take(msg tea.Msg) []effect {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.key(msg.String())
	case tea.WindowSizeMsg:
		m.width, m.height = max(msg.Width, 0), max(msg.Height, 0)
		m.buildRows()
	case dialedMsg:
		if msg.epoch == m.epoch {
			m.everDialed = true
		}
	case dialFailedMsg:
		return m.dialFailed(msg)
	case fleetMsg:
		return m.newFleet(msg)
	case lostMsg:
		return m.lost(msg)
	case retryMsg:
		if msg.n == m.waitN && m.conn != connLive && !m.retryAt.IsZero() {
			return []effect{m.dial()}
		}
	case rolloutsMsg:
		m.newRollouts(msg)
	case historyMsg:
		m.newHistory(msg)
	case logMsg:
		if msg.epoch == m.epoch && msg.id == m.logID && m.screen == screenZathras {
			m.logIs = askedHave
			if m.log.add(msg.lines) > 0 {
				m.buildRows()
			}
		}
	case logEndedMsg:
		m.logEnded(msg)
	case shellOpenedMsg:
		return m.shellOpened(msg)
	case shellEndedMsg:
		return m.shellEnded(msg)
	}
	return nil
}

// dialFailed takes the failure of a dial. What draal refuses an operator
// that it never let in ends the program; anything else is tried again
// after a pause, twice as long as the one before.
func (m *model) dialFailed(msg dialFailedMsg) []effect {
	if msg.epoch != m.epoch || m.conn == connLive {
		return nil
	}
	if e, ok := refusal(msg.err); ok && e.Code == wire.CodeUnauthorized && !m.everDialed {
		m.fatal = msg.err
		return []effect{effFail{err: msg.err}}
	}
	m.dialErr = ""
	if msg.err != nil {
		m.dialErr = clean(capped(msg.err.Error()))
	}
	m.pause = min(max(2*m.pause, m.cfg.RetryFirst), m.cfg.RetryMax)
	return []effect{m.wait()}
}

// lost takes the end of the connection: what is shown stays, and a dial
// is tried again after the first pause.
func (m *model) lost(msg lostMsg) []effect {
	if msg.epoch != m.epoch || !m.retryAt.IsZero() {
		return nil
	}
	if m.conn != connLost {
		m.conn, m.since = connLost, m.cfg.Now()
	}
	m.dialErr = ""
	if msg.err != nil {
		m.dialErr = clean(capped(msg.err.Error()))
	}
	// What was asked of the connection is ended with it.
	if m.rolloutsAre == askedFor {
		m.rolloutsAre = askedNot
	}
	if m.history == askedFor {
		m.history = askedNot
	}
	if m.logIs == askedFor || m.logIs == askedHave {
		m.logIs = askedEnded
		m.log.say("The connection to draal was lost.")
		m.buildRows()
	}
	m.epoch++ // nothing of the old connection is read from here on
	m.pause = m.cfg.RetryFirst
	// A shell that draal was asked for over it is not waited for.
	effs := m.giveUpShell("No shell was opened: the connection to draal was lost.")
	return append(effs, effHangUp{}, m.wait())
}

// newFleet takes a fleet. The first one of a connection makes the
// connection live, and what the view that is open holds is asked for
// again.
func (m *model) newFleet(msg fleetMsg) []effect {
	if msg.epoch != m.epoch {
		return nil
	}
	now := m.cfg.Now()
	first := m.conn != connLive
	m.conn, m.pause, m.dialErr, m.retryAt = connLive, 0, "", time.Time{}
	m.fleet, m.have, m.fleetAt = sortFleet(msg.fleet), true, now
	m.choose()

	effs := m.rolloutsDue(first)
	if m.screen == screenZathras {
		z, in, ok := m.find(m.zathras)
		switch {
		case ok:
			// It is there. It is asked for its samples and its log when
			// the connection is new, when it was gone, and when it has
			// connected again between two fleets.
			again := first || !m.present || !z.Connected.Equal(m.seen.Connected)
			m.seen, m.seenIn, m.present = z, in, true
			if z.State.Sample != nil {
				m.samples = mergeSamples(m.samples, []wire.Sample{*z.State.Sample}, now)
			}
			if again {
				effs = append(effs, m.askZathras()...)
			}
		case m.present:
			m.present, m.goneSince = false, now
		}
	}
	return effs
}

// rolloutsDue asks for the rollouts of the service that is open when
// they are due: when they were not asked for over this connection (anew
// says that the connection is new), and when the last rollout of the
// service is another one, or in another state, than when they were
// asked for.
func (m *model) rolloutsDue(anew bool) []effect {
	if m.screen != screenService || m.conn != connLive {
		return nil
	}
	svc, ok := m.serviceNamed(m.service)
	if !ok || !anew && m.rolloutsAre != askedNot && keyOf(svc) == m.rolloutsOf {
		return nil
	}
	return []effect{m.askRollouts(svc)}
}

// keyOf returns what of a service's last rollout its list is asked for.
func keyOf(svc wire.Service) rolloutKey {
	if svc.Rollout == nil {
		return rolloutKey{}
	}
	return rolloutKey{id: svc.Rollout.ID, state: svc.Rollout.State}
}

// askRollouts asks for the rollouts of the service that is open.
func (m *model) askRollouts(svc wire.Service) effect {
	m.rolloutsOf = keyOf(svc)
	if m.rolloutsAre != askedHave {
		m.rolloutsAre = askedFor
	}
	m.rolloutsN++
	return effRollouts{epoch: m.epoch, n: m.rolloutsN, service: svc.Name}
}

// askZathras asks for what the view of a zathras holds: its samples
// once, and its log, followed. Lines the buffer has are not taken again.
func (m *model) askZathras() []effect {
	if m.history != askedHave {
		m.history = askedFor
	}
	m.logID++
	m.logIs = askedFor
	m.log.resume()
	m.historyN++
	return []effect{
		effHistory{epoch: m.epoch, n: m.historyN, zathras: m.zathras},
		effFollow{epoch: m.epoch, zathras: m.zathras, id: m.logID},
	}
}

// again asks anew for what the view that is open holds, as after a
// time in which the model was told nothing: the rollouts of a service,
// the samples and the log of a zathras. What was on its way is not
// read; no line of the log is taken twice.
func (m *model) again() []effect {
	if m.conn != connLive {
		return nil
	}
	switch m.screen {
	case screenService:
		return m.rolloutsDue(true)
	case screenZathras:
		if m.present {
			return m.askZathras()
		}
	}
	return nil
}

// choose keeps what is chosen in the lists chosen by what it is: a
// service by its name, a zathras by its ID. When that is gone, what is
// now at its place is chosen, or the last.
func (m *model) choose() {
	services := m.fleet.Services
	at := slices.IndexFunc(services, func(s wire.Service) bool { return s.Name == m.svcName })
	switch {
	case len(services) == 0:
		m.svcName, m.svcIdx = "", 0
	case at >= 0:
		m.svcIdx = at
	default:
		m.svcIdx = min(max(m.svcIdx, 0), len(services)-1)
		m.svcName = services[m.svcIdx].Name
	}

	svc, _ := m.serviceNamed(m.service)
	zs := svc.Zathrases
	at = slices.IndexFunc(zs, func(z wire.Zathras) bool { return z.ID == m.zID })
	switch {
	case len(zs) == 0:
		// The service has no zathras now, or is gone itself: what was
		// chosen is remembered, for when it is back.
		m.zIdx = 0
	case at >= 0:
		m.zIdx = at
	default:
		m.zIdx = min(max(m.zIdx, 0), len(zs)-1)
		m.zID = zs[m.zIdx].ID
	}
}

// newRollouts takes the answer to the newest request for the rollouts of
// the service that is open, whatever view is in sight when it comes: the
// view of one of its zathrases, too. An answer to an older request is
// dropped: it may come last, and says what was.
func (m *model) newRollouts(msg rolloutsMsg) {
	if msg.epoch != m.epoch || msg.service != m.service || msg.n != m.rolloutsN {
		return
	}
	if msg.err != nil {
		m.rolloutsAre, m.rolloutsErr = askedRefused, clean(capped(msg.err.Error()))
		return
	}
	list := slices.Clone(msg.rollouts)
	for i := range list {
		list[i].Reason, list[i].State = capped(list[i].Reason), wire.RolloutState(capped(string(list[i].State)))
	}
	m.rollouts, m.rolloutsAre, m.rolloutsErr, m.rolloutsFrom = list, askedHave, "", m.cfg.Now()
}

// newHistory takes the samples of the zathras that is open.
func (m *model) newHistory(msg historyMsg) {
	if msg.epoch != m.epoch || msg.zathras != m.zathras || msg.n != m.historyN || m.screen != screenZathras {
		return
	}
	if msg.err != nil {
		m.history, m.histErr = askedRefused, clean(capped(msg.err.Error()))
		return
	}
	m.samples, m.history, m.histErr = mergeSamples(m.samples, msg.samples, m.cfg.Now()), askedHave, ""
}

// logEnded takes the end of the log that is followed.
func (m *model) logEnded(msg logEndedMsg) {
	if msg.epoch != m.epoch || msg.id != m.logID || m.screen != screenZathras {
		return
	}
	m.logIs = askedEnded
	if e, ok := refusal(msg.err); ok {
		m.log.say("draal does not send the log: " + capped(e.Message))
	} else {
		m.log.say("The log has ended.")
	}
	m.buildRows()
}

// key takes a key by its name.
func (m *model) key(name string) []effect {
	// From the key that asks for a shell until the shell has ended, no
	// key is the model's but the one that gives the shell up.
	if effs, taken := m.shellTakesKey(name); taken {
		return effs
	}
	if name == "q" || name == "ctrl+c" {
		return []effect{effQuit{}}
	}
	var effs []effect
	// A key ends a pause at once.
	if m.conn != connLive && !m.retryAt.IsZero() {
		effs = append(effs, m.dial())
	}
	m.notice = ""
	if m.help {
		m.help = false
		return effs
	}
	switch name {
	case "?":
		m.help = true
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-m.page())
	case "pgdown":
		m.move(m.page())
	case "g":
		m.moveTo(false)
	case "G":
		m.moveTo(true)
		if m.screen == screenZathras && m.logIs == askedEnded && m.present && m.conn == connLive {
			// G follows a log again, also one that has ended.
			m.logID++
			m.logIs = askedFor
			m.log.resume()
			effs = append(effs, effFollow{epoch: m.epoch, zathras: m.zathras, id: m.logID})
		}
	case "enter", "right", "l":
		effs = append(effs, m.open()...)
	case "esc", "left", "h", "backspace":
		effs = append(effs, m.back()...)
	case "s":
		if m.screen == screenZathras {
			effs = append(effs, m.shellKey()...)
		}
	}
	return effs
}

// shellKey is the key s on a zathras: a shell, when there is a
// container to open it in and a connection to ask over.
func (m *model) shellKey() []effect {
	switch {
	case !m.present:
		m.notice = "This zathras is not connected: there is no container to open a shell in."
		return nil
	case m.conn != connLive:
		m.notice = "There is no connection to draal: a shell cannot be opened now."
		return nil
	}
	return m.openShell()
}

// page returns how far PgUp and PgDn move: a screenful less one row.
func (m *model) page() int {
	if m.screen == screenZathras {
		return max(m.logHeight()-1, 1)
	}
	if m.screen == screenService {
		return max(m.zathrasesHeight()-1, 1)
	}
	return max(m.servicesHeight()-1, 1)
}

// move moves what is chosen in the list that is in sight by delta
// places, or scrolls the log by delta rows.
func (m *model) move(delta int) {
	switch m.screen {
	case screenServices:
		if n := len(m.fleet.Services); n > 0 {
			m.svcIdx = min(max(m.svcIdx+delta, 0), n-1)
			m.svcName = m.fleet.Services[m.svcIdx].Name
		}
	case screenService:
		svc, _ := m.serviceNamed(m.service)
		if n := len(svc.Zathrases); n > 0 {
			m.zIdx = min(max(m.zIdx+delta, 0), n-1)
			m.zID = svc.Zathrases[m.zIdx].ID
		}
	case screenZathras:
		last := max(len(m.rows)-m.logHeight(), 0)
		top := m.logTop
		if m.follow {
			top = last
		}
		top = min(max(top+delta, 0), last)
		m.logTop, m.follow = top, top >= last
	}
}

// moveTo moves to the start or to the end of the list that is in sight.
// The end of a log is followed.
func (m *model) moveTo(end bool) {
	if m.screen == screenZathras {
		m.logTop, m.follow = 0, end || len(m.rows) <= m.logHeight()
		return
	}
	if end {
		m.move(len(m.fleet.Services) + 1<<20)
	} else {
		m.move(-len(m.fleet.Services) - 1<<20)
	}
}

// open opens what is chosen: a service, or a zathras of it.
func (m *model) open() []effect {
	switch m.screen {
	case screenServices:
		svc, ok := m.serviceNamed(m.svcName)
		if !ok {
			return nil
		}
		m.screen = screenService
		if m.service != svc.Name {
			m.service, m.zID, m.zIdx, m.zTop = svc.Name, "", 0, 0
			m.rollouts, m.rolloutsAre, m.rolloutsErr, m.rolloutsOf = nil, askedNot, "", rolloutKey{}
		}
		m.choose()
		return m.rolloutsDue(true)
	case screenService:
		z, in, ok := m.find(m.zID)
		if !ok || in != m.service {
			return nil
		}
		m.screen = screenZathras
		m.zathras, m.seen, m.seenIn, m.present, m.goneSince = z.ID, z, in, true, time.Time{}
		m.samples, m.history, m.histErr = nil, askedNot, ""
		if z.State.Sample != nil {
			m.samples = mergeSamples(nil, []wire.Sample{*z.State.Sample}, m.cfg.Now())
		}
		m.log, m.logIs, m.rows, m.follow, m.logTop = newLogBuffer(), askedNot, nil, true, 0
		if m.conn == connLive {
			return m.askZathras()
		}
	}
	return nil
}

// back leaves the view that is open for the one it was opened from.
// What the view held is ended.
func (m *model) back() []effect {
	switch m.screen {
	case screenZathras:
		m.screen = screenService
		followed := m.logIs == askedFor || m.logIs == askedHave
		m.logID++ // lines that are on their way are not for the next log
		m.logIs = askedNot
		if m.present {
			m.zID = m.zathras
		}
		m.choose()
		var effs []effect
		if followed {
			effs = append(effs, effUnfollow{})
		}
		return append(effs, m.rolloutsDue(false)...)
	case screenService:
		m.screen = screenServices
		if _, ok := m.serviceNamed(m.service); ok {
			m.svcName = m.service
		}
		m.choose()
	}
	return nil
}

// keepInSight moves the lists so that what is chosen is in sight.
func (m *model) keepInSight() {
	switch m.screen {
	case screenServices:
		m.svcTop = inSight(m.svcTop, m.svcIdx, len(m.fleet.Services), max(m.servicesHeight(), 1))
	case screenService:
		svc, _ := m.serviceNamed(m.service)
		m.zTop = inSight(m.zTop, m.zIdx, len(svc.Zathrases), max(m.zathrasesHeight(), 1))
	case screenZathras:
		if last := max(len(m.rows)-m.logHeight(), 0); m.logTop > last {
			m.logTop = last
		}
	}
}

// inSight returns the first row of a list of n rows that shows h of
// them, moved from top as little as puts row idx in sight.
func inSight(top, idx, n, h int) int {
	if idx < top {
		top = idx
	}
	if idx >= top+h {
		top = idx - h + 1
	}
	return min(max(top, 0), max(n-h, 0))
}

// buildRows makes the rows of the log out of its lines, for the width
// the terminal has. A reader who does not follow the end keeps the row
// that was first in sight where it is.
func (m *model) buildRows() {
	var anchor logRow
	held := !m.follow && m.logTop < len(m.rows)
	if held {
		anchor = m.rows[m.logTop]
	}
	textWidth := m.width - logPrefix(m.width)
	rows := make([]logRow, 0, len(m.log.entries))
	for i, e := range m.log.entries {
		if e.kind != entryLine {
			rows = append(rows, logRow{entry: i, seq: e.seq})
			continue
		}
		parts, cut := wrapSpans(e.spans, textWidth, logRowsPerLine)
		if (cut || e.cut) && len(parts) > 0 && textWidth > 0 {
			// The last row ends in an ellipsis, in a cell of its own.
			var last []span
			if kept, _ := wrapSpans(parts[len(parts)-1], textWidth-1, 1); len(kept) > 0 {
				last = slices.Clone(kept[0])
			}
			parts[len(parts)-1] = append(last, span{text: ellipsis, look: look{dim: true}})
		}
		for sub, spans := range parts {
			rows = append(rows, logRow{entry: i, seq: e.seq, sub: sub, spans: spans})
		}
		if len(parts) == 0 {
			rows = append(rows, logRow{entry: i, seq: e.seq})
		}
	}
	m.rows = rows
	if !held {
		return
	}
	// The row that was first in sight, or what is left of its line, or,
	// when its line is gone, the oldest there is.
	m.logTop = 0
	for i, r := range rows {
		if r.seq == anchor.seq && r.sub <= anchor.sub {
			m.logTop = i
		}
		if r.seq > anchor.seq {
			break
		}
	}
}

// logPrefix returns how many cells stand before the text of a line of a
// log: the time and the stream, or the stream alone in a terminal that
// is narrow.
func logPrefix(width int) int {
	if width < 60 {
		return 3
	}
	return 12
}
