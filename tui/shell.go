package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/fables-for-robots/great-machine/wire"
)

// The shell of a zathras comes with a later task. This file is its
// place: the effect by which the model asks for one, the message by
// which the program says how it ended, and what the key does until then.

// effShell asks the program for a shell on the zathras: to give the
// terminal to the session, and to take it back when the shell has ended
// or was left.
type effShell struct {
	epoch   int
	zathras wire.ID
}

// shellEndedMsg says how a shell ended: with an exit status, left with
// Ctrl-], or with an error.
type shellEndedMsg struct {
	exit     int
	detached bool
	err      error
}

// openShell is the key s on a zathras that is connected, with a
// connection to draal that is live.
func (m *model) openShell() []effect {
	m.notice = "The shell is not part of this terminal UI yet: `great-machine shell` opens one."
	return nil
}

// shellEnded takes the message that a shell has ended. While the shell
// had the terminal the model was told nothing, for as long as that was:
// what the view holds is asked for anew.
func (m *model) shellEnded(msg shellEndedMsg) []effect {
	return m.again()
}

// shell carries out effShell. Until the shell is built it does nothing.
// It is called from Update, and what it returns is run by Bubble Tea:
// tea.Exec gives the terminal away for as long as its command runs, and
// takes it back.
func (r *runner) shell(e effShell) tea.Cmd {
	return nil
}
