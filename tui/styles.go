package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/fables-for-robots/great-machine/wire"
)

// The looks of the terminal UI. Its colours are four of the terminal's
// own sixteen (30 to 37, which a light terminal has dark and a dark one
// light), and bold, faint and reverse, which every terminal has: it is
// light or dark as the terminal is, and with NO_COLOR the renderer
// leaves the colours out and the rest stays. Nothing is said by colour
// alone: what is coloured has a sign and a word too.
var (
	stPlain = lipgloss.NewStyle()
	stBold  = lipgloss.NewStyle().Bold(true)
	stDim   = lipgloss.NewStyle().Faint(true)
	stBar   = lipgloss.NewStyle().Reverse(true)
	stTitle = lipgloss.NewStyle().Reverse(true).Bold(true)
	stOK    = lipgloss.NewStyle().Foreground(lipgloss.Green)
	stWarn  = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	stBad   = lipgloss.NewStyle().Foreground(lipgloss.Red).Bold(true)
	stBusy  = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
)

// seg is a piece of a line: text, which may be anything, and its look.
type seg struct {
	text string
	st   lipgloss.Style
}

// line draws the pieces side by side in at most w cells. Every piece is
// cleaned and cut on the way: nothing gets into a line but through
// here, and no line is wider than it may be.
func line(w int, segs ...seg) string {
	var b strings.Builder
	for _, s := range segs {
		if w <= 0 {
			break
		}
		text := cut(s.text, w)
		if text == "" {
			continue
		}
		w -= width(text)
		b.WriteString(s.st.Render(text))
	}
	return b.String()
}

// chosen draws the pieces as the row that is chosen: in reverse, across
// all of w cells, whatever the looks of the pieces.
func chosen(w int, segs ...seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(clean(s.text))
	}
	return stBar.Render(pad(b.String(), w))
}

// plainText is a piece without a look.
func plainText(text string) seg { return seg{text, stPlain} }

// dim is a piece that matters less.
func dim(text string) seg { return seg{text, stDim} }

// bold is a piece that matters more.
func bold(text string) seg { return seg{text, stBold} }

// phaseSign returns the sign and the look of a phase.
func phaseSign(p wire.Phase) (string, lipgloss.Style) {
	switch knownPhase(p) {
	case wire.PhaseRunning:
		return "●", stOK
	case wire.PhaseStopping:
		return "◐", stWarn
	case wire.PhaseBackoff:
		return "✖", stBad
	}
	return "○", stDim
}

// rolloutSign returns the sign and the look of a state of a rollout.
func rolloutSign(s wire.RolloutState) (string, lipgloss.Style) {
	switch s {
	case wire.RolloutPreparing, wire.RolloutActivating:
		return "▶", stBusy
	case wire.RolloutCompleted:
		return "✔", stOK
	case wire.RolloutHalted:
		return "✖", stBad
	}
	return "–", stDim
}
