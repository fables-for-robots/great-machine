package tui

// What is drawn comes with the next task. Until then the model has the
// little of it that its own tests need: how much room the lists and the
// log have, and a view that says where one is.

import (
	"fmt"
	"strings"
)

const (
	// leastWidth and leastHeight are the smallest terminal the terminal
	// UI draws in. In a smaller one it says so, in one line.
	leastWidth  = 50
	leastHeight = 12
)

// shared is what of the view that is open is worked out once for a
// message. The views of the next task keep there how their rows are
// shared out among their parts.
type shared struct{}

// tooSmall says whether the terminal is smaller than the terminal UI
// can draw in.
func (m *model) tooSmall() bool {
	return m.width < leastWidth || m.height < leastHeight
}

// servicesHeight returns how many services of the list are in sight.
func (m *model) servicesHeight() int {
	return max(m.height-2, 1)
}

// zathrasesHeight returns how many zathrases of a service's table are
// in sight.
func (m *model) zathrasesHeight() int {
	return max(m.height-2, 1)
}

// logHeight returns how many rows of the log are in sight.
func (m *model) logHeight() int {
	return max(m.height-2, 1)
}

// view draws the model: as many lines as the terminal has rows at most,
// none wider than the terminal.
func (m *model) view() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if m.tooSmall() {
		return cut(fmt.Sprintf("Too small: great-machine needs a terminal of %d×%d or more.", leastWidth, leastHeight), m.width)
	}
	where := []string{"great-machine"}
	if m.screen != screenServices {
		where = append(where, named(m.service))
	}
	if m.screen == screenZathras {
		where = append(where, named(m.seen.Name))
	}
	lines := make([]string, m.height)
	lines[0] = cut(" "+strings.Join(where, " › "), m.width)
	if m.notice != "" {
		lines[m.height-2] = cut(" "+m.notice, m.width)
	}
	lines[m.height-1] = cut(" ? keys  q quit", m.width)
	return strings.Join(lines, "\n")
}
