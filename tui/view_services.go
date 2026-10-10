package tui

import (
	"fmt"
	"time"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// The view of the services: every service with its stable generation,
// its zathrases by phase, and its last rollout and how far it is. A
// service is two lines and an empty one.

// serviceRows is how many rows one service takes in the list.
const serviceRows = 3

// servicesHeight returns how many services of the list are in sight.
func (m *model) servicesHeight() int {
	return max((m.bodyHeight()-2+1)/serviceRows, 1)
}

// servicesView draws the services in at most h lines.
func (m *model) servicesView(h int) []string {
	services := m.fleet.Services
	if len(services) == 0 {
		// Review Focus 4: no service yet, and what to do.
		out := []string{"", line(m.width, bold(" draal knows no service yet."))}
		out = append(out, sentences(m.width, stPlain, "A service is there as soon as a zathras of it is connected or something was rolled out to it. Start a zathras and name its service:")...)
		out = append(out, "", line(m.width, plainText("   great-machine zathras --draal ID --service NAME")), "")
		return append(out, sentences(m.width, stDim, "ID is the endpoint ID that draal logged when it started. The zathras is shown here as soon as it has registered.")...)
	}
	zathrases := 0
	nameWidth := 0
	for _, svc := range services {
		zathrases += len(svc.Zathrases)
		nameWidth = max(nameWidth, width(named(svc.Name)))
	}
	nameWidth = min(nameWidth, 30, m.width/3)
	summary := human.Count(len(services), "service", "services") + ", " + human.Count(zathrases, "zathras", "zathrases") + " connected"
	shown := m.servicesHeight()
	if len(services) > shown {
		summary += fmt.Sprintf(" · %d to %d are in sight", m.svcTop+1, min(m.svcTop+shown, len(services)))
	}
	out := []string{line(m.width, dim(" "+summary)), ""}
	now := m.cfg.Now()
	for i := m.svcTop; i < min(m.svcTop+shown, len(services)); i++ {
		svc := services[i]
		stable := "no stable generation"
		if svc.Stable > 0 {
			stable = "stable generation " + human.Generation(svc.Stable)
		}
		first := []seg{plainText("  "), bold(pad(middle(named(svc.Name), nameWidth), nameWidth)), dim("  " + pad(stable, 22))}
		first = append(first, phaseStrip(svc.Zathrases)...)
		if i == m.svcIdx {
			first[0] = plainText("▸ ")
			out = append(out, chosen(m.width, first...))
		} else {
			out = append(out, line(m.width, first...))
		}
		second := append([]seg{plainText(pad("", nameWidth+4))}, rolloutWords(svc, now)...)
		out = append(out, line(m.width, second...), "")
	}
	return out
}

// phaseStrip says how many zathrases are in each phase.
func phaseStrip(zs []wire.Zathras) []seg {
	if len(zs) == 0 {
		return []seg{dim("no zathras connected")}
	}
	count := byPhase(zs)
	var out []seg
	for _, p := range phases {
		if count[p] == 0 {
			continue
		}
		sign, st := phaseSign(p)
		out = append(out, seg{sign, st}, plainText(fmt.Sprintf(" %d %s  ", count[p], p)))
	}
	return out
}

// rolloutWords says the last rollout of a service in a line: what it
// is, how far, and since when. The counts are draal's, of the zathrases
// that are connected.
func rolloutWords(svc wire.Service, now time.Time) []seg {
	r := svc.Rollout
	if r == nil {
		return []seg{dim("nothing was rolled out yet")}
	}
	sign, st := rolloutSign(r.State)
	what := fmt.Sprintf(" rollout %d to generation %s: ", r.ID, human.Generation(r.Generation))
	// When it ended, where draal says so.
	ended := ""
	if !r.Finished.IsZero() {
		ended = " " + human.Ago(r.Finished, now)
	}
	switch r.State {
	case wire.RolloutPreparing:
		return []seg{{sign, st}, plainText(what), {"preparing", st},
			plainText(fmt.Sprintf(", %d of %d prepared, started %s", r.Prepared, len(svc.Zathrases), human.Ago(r.Created, now)))}
	case wire.RolloutActivating:
		return []seg{{sign, st}, plainText(what), {"activating", st},
			plainText(fmt.Sprintf(", %d of %d activated, started %s", r.Activated, len(svc.Zathrases), human.Ago(r.Created, now)))}
	case wire.RolloutHalted:
		return []seg{{sign, st}, plainText(what), {"halted", st}, plainText(ended + ": " + r.Reason)}
	}
	return []seg{{sign, st}, plainText(what), {string(r.State), st}, plainText(ended)}
}
