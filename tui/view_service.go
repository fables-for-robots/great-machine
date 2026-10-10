package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// The view of a service: what it is in a line, the rollout under way or
// halted with where every zathras stands in it, its zathrases as a
// table, and its last rollouts.

// serviceParts is how the rows of the view of a service are shared out.
type serviceParts struct {
	top   []string // the line of the service and what is said of its rollout
	table int      // how many zathrases of the table are in sight
	last  int      // how many rows the last rollouts have, with their title
}

// zathrasesHeight returns how many zathrases of a service's table are
// in sight.
func (m *model) zathrasesHeight() int {
	svc, ok := m.serviceNamed(m.service)
	if !ok {
		return 1
	}
	return max(m.shareService(svc, m.bodyHeight()).table, 1)
}

// shareService shares h rows out among the parts of the view: the top
// as it is, as long as it leaves room; then the table, as many rows as
// it has zathrases, but for five that are left to the last rollouts
// while the table has three; and the last rollouts get the rest.
func (m *model) shareService(svc wire.Service, h int) serviceParts {
	if m.shared.service != nil && m.shared.serviceH == h {
		return *m.shared.service
	}
	p := serviceParts{top: m.serviceTop(svc)}
	rows := max(len(svc.Zathrases), 1)
	if spare := h - 4 - 3; len(p.top) > spare { // the table's head and three rows, a line between
		p.top = p.top[:max(spare, 1)]
	}
	room := h - len(p.top) - 2 // an empty line, and the head of the table
	p.table = min(rows, max(room-5, 3), max(room, 1))
	if rest := room - p.table - 1; rest >= 2 {
		p.last = rest
	}
	m.shared.service, m.shared.serviceH = &p, h
	return p
}

// serviceView draws a service in at most h lines.
func (m *model) serviceView(h int) []string {
	svc, ok := m.serviceNamed(m.service)
	if !ok {
		// Review Focus 4: the service went while its view was open.
		out := []string{"", line(m.width, bold(" draal knows no service of this name any more: "), plainText(named(m.service)))}
		out = append(out, sentences(m.width, stPlain, "A service is there as long as a zathras of it is connected or something was rolled out to it. The view is drawn again when it is back; Esc goes to the services.")...)
		return out
	}
	p := m.shareService(svc, h)
	out := append([]string{}, p.top...)
	out = append(out, "")
	out = append(out, m.zathrasTable(svc, p.table)...)
	if p.last > 0 {
		out = append(out, "")
		out = append(out, m.lastRollouts(p.last)...)
	}
	return out
}

// serviceTop draws the line of the service, and under it the rollout
// that is under way or halted, or that there was none.
func (m *model) serviceTop(svc wire.Service) []string {
	now := m.cfg.Now()
	stable := "no stable generation"
	if svc.Stable > 0 {
		stable = "stable generation " + human.Generation(svc.Stable)
	}
	first := []seg{plainText(" "), bold(middle(named(svc.Name), m.width/3)), dim(" · " + stable + " · ")}
	first = append(first, phaseStrip(svc.Zathrases)...)
	out := []string{line(m.width, first...)}

	r := svc.Rollout
	if r == nil {
		// Review Focus 4: a service without a rollout, and the command
		// that makes one.
		out = append(out, "", line(m.width, bold(" Nothing was rolled out to this service yet.")))
		out = append(out, sentences(m.width, stPlain, "Its zathrases are connected and run nothing. A rollout gives them a generation: on the machine that has the binary in its Nix store, as an operator of this draal,")...)
		return append(out, line(m.width, plainText("   great-machine rollout "+named(svc.Name)+" /nix/store/…/bin/…")))
	}
	if !rolloutOpen(r) {
		return out
	}
	sign, st := rolloutSign(r.State)
	parallel := "all of them at a time"
	if r.Parallel > 0 {
		parallel = fmt.Sprintf("%d at a time", r.Parallel)
	}
	head := []seg{plainText(" "), {sign, st}, bold(fmt.Sprintf(" Rollout %d to generation %s", r.ID, human.Generation(r.Generation))), plainText(": "), {string(r.State), st}}
	if r.State == wire.RolloutHalted && !r.Finished.IsZero() {
		head = append(head, plainText(" "+human.Ago(r.Finished, now)+", after "+human.Took(r.Finished.Sub(r.Created))))
	}
	out = append(out, "", line(m.width, head...),
		line(m.width, dim("   "+parallel+", started "+whenWords(r.Created, now)+" by the operator "+human.ShortID(r.CreatedBy))))
	if r.State == wire.RolloutHalted {
		// The reason, in full.
		for i, l := range wrap(r.Reason, m.width-9, 0) {
			label := "   why  "
			if i > 0 {
				label = "        "
			}
			out = append(out, line(m.width, dim(label), seg{l, stBad}))
		}
	}
	out = append(out, line(m.width, dim("   draal counts "),
		plainText(fmt.Sprintf("%d prepared, %d activated, %d awaited", r.Prepared, r.Activated, r.Awaited)),
		dim(" of "+human.Count(len(svc.Zathrases), "zathras", "zathrases")+" connected")))
	if len(svc.Zathrases) == 0 {
		return append(out, line(m.width, dim("   No zathras of the service is connected, so none has a part in this rollout now.")))
	}
	// Where every zathras stands, by its own report.
	names := map[stage][]string{}
	for _, z := range svc.Zathrases {
		s := stageOf(z.State, r.Generation)
		names[s] = append(names[s], m.nameIn(svc, z, 24))
	}
	for _, s := range stages {
		if len(names[s]) == 0 {
			continue
		}
		out = append(out, line(m.width, dim("   "+pad(s.String(), 13)), bold(pad(fmt.Sprint(len(names[s])), 4)), plainText(strings.Join(names[s], ", "))))
	}
	return out
}

// nameIn returns the name of a zathras in at most w cells, as it is
// told apart among the zathrases of its service: one whose name another
// one has too is named with the start of its ID.
func (m *model) nameIn(svc wire.Service, z wire.Zathras, w int) string {
	twice := false
	for _, other := range svc.Zathrases {
		twice = twice || other.Name == z.Name && other.ID != z.ID
	}
	if !twice || w < 12 {
		return middle(named(z.Name), w)
	}
	id, _ := cutClean(clean(capped(string(z.ID))), 4)
	return middle(named(z.Name), w-5) + "·" + id
}

// zathrasTable draws the table of a service's zathrases: its head and
// rows of them, from the first in sight.
func (m *model) zathrasTable(svc wire.Service, rows int) []string {
	if len(svc.Zathrases) == 0 {
		// Review Focus 4: a service without a zathras.
		text := "No zathras of this service is connected. draal keeps the service because something was rolled out to it; a zathras that is started with --service " + named(svc.Name) + " gets its stable generation"
		if svc.Stable > 0 {
			text += ", " + human.Generation(svc.Stable)
		}
		return sentences(m.width, stPlain, text+".")
	}
	now := m.cfg.Now()
	wide := m.width >= 110
	// The columns: what is left of the width is the name's.
	const phaseW, genW, restartsW, cpuW = 16, 10, 8, 5
	exitW, memW, idW := 10, 10, 0
	if wide {
		exitW, memW, idW = 26, 24, 8
	}
	fixed := 2 + 1 + phaseW + 1 + genW + 1 + restartsW + 1 + exitW + 1 + cpuW + 1 + memW
	if idW > 0 {
		fixed += idW + 1
	}
	nameW := max(m.width-fixed, 8)

	head := []seg{{"  " + pad("NAME", nameW), stDim}}
	if idW > 0 {
		head = append(head, dim(" "+pad("ID", idW)))
	}
	head = append(head, dim(" "+pad("PHASE", phaseW)+" "+pad("GENERATION", genW)+" "+padLeft("RESTARTS", restartsW)+" "+pad("LAST EXIT", exitW)+" "+padLeft("CPU", cpuW)+" "+padLeft("MEMORY", memW)))
	out := []string{line(m.width, head...)}

	for i := m.zTop; i < min(m.zTop+rows, len(svc.Zathrases)); i++ {
		z := svc.Zathrases[i]
		st := z.State
		sign, signSt := phaseSign(st.Phase)
		phase := string(knownPhase(st.Phase))
		switch knownPhase(st.Phase) {
		case wire.PhaseRunning:
			if st.RunningFor > 0 {
				phase += " " + human.Age(st.RunningFor)
			}
		case wire.PhaseBackoff:
			if until := human.Until(st.RestartAt, now); until != human.None {
				phase += " " + strings.TrimPrefix(until, "in ")
			}
		}
		gen := human.Generation(st.Generation)
		if st.Generation != 0 && st.Generation == svc.Stable {
			gen += " stable"
		}
		exit := human.Exit(st.LastExit)
		if wide && st.LastExit != nil {
			exit += ", " + human.Ago(st.LastExit.At, now)
		} else if st.LastExit != nil && st.LastExit.Signal != "" {
			exit = st.LastExit.Signal
		}
		cpu, memory := "no", "figures"
		if s := st.Sample; s != nil {
			cpu, memory = human.CPU(s.CPU), human.Bytes(s.Memory)
			if wide && s.MemoryLimit > 0 {
				memory += " of " + human.Bytes(s.MemoryLimit)
			}
		}
		row := []seg{plainText("  " + pad(m.nameIn(svc, z, nameW), nameW))}
		if idW > 0 {
			row = append(row, dim(" "+pad(human.ShortID(z.ID), idW)))
		}
		row = append(row,
			plainText(" "), seg{sign, signSt}, plainText(" "+pad(phase, phaseW-2)),
			plainText(" "+pad(gen, genW)),
			plainText(" "+padLeft(fmt.Sprint(st.Restarts), restartsW)),
			plainText(" "+pad(exit, exitW)),
			plainText(" "+padLeft(cpu, cpuW)),
			plainText(" "+padLeft(memory, memW)),
		)
		if i == m.zIdx {
			row[0].text = "▸ " + row[0].text[2:]
			out = append(out, chosen(m.width, row...))
		} else {
			out = append(out, line(m.width, row...))
		}
	}
	return out
}

// lastRollouts draws the last rollouts of the service in h lines, of
// which the first is their title.
func (m *model) lastRollouts(h int) []string {
	title := []seg{bold(" Last rollouts")}
	var body []string
	switch {
	case m.rolloutsAre == askedNot:
		body = sentences(m.width, stDim, "Not asked for: there is no connection to draal.")
	case m.rolloutsAre == askedFor:
		body = sentences(m.width, stDim, "Asking draal …")
	case m.rolloutsAre == askedRefused:
		body = sentences(m.width, stBad, "draal does not list them: "+m.rolloutsErr)
	case len(m.rollouts) == 0:
		body = sentences(m.width, stDim, "draal has no rollout of this service on record.")
	default:
		now := m.cfg.Now()
		shown := min(len(m.rollouts), h-1)
		if shown < len(m.rollouts) {
			title = append(title, dim(fmt.Sprintf(" · the newest %d of %d", shown, len(m.rollouts))))
		}
		for _, r := range m.rollouts[:shown] {
			sign, st := rolloutSign(r.State)
			took := ""
			switch {
			case !r.Finished.IsZero():
				took = "took " + human.Took(r.Finished.Sub(r.Created))
			case r.State.UnderWay():
				took = "under way"
			}
			body = append(body, line(m.width,
				plainText("   "+padLeft(fmt.Sprintf("#%d", r.ID), 5)+" "), seg{sign, st}, seg{" " + pad(string(r.State), 10), st},
				plainText(" to "+pad(human.Generation(r.Generation), 5)),
				dim(" "+pad(human.Ago(r.Created, now), 8)+" "+pad(took, 12)+" "), seg{r.Reason, stPlain}))
		}
	}
	out := append([]string{line(m.width, title...)}, body...)
	return out[:min(len(out), h)]
}

// whenWords says when a time was: on the clock when it is of the last
// day, with its day when it is older, and by how long ago.
func whenWords(t, now time.Time) string {
	switch {
	case t.IsZero():
		return human.None
	case now.Sub(t) < 24*time.Hour && t.Sub(now) < 24*time.Hour:
		return human.Clock(t) + " (" + human.Ago(t, now) + ")"
	}
	return sinceWords(t, now)
}

// sinceWords says since when a time is, on the clock and by how long.
func sinceWords(t, now time.Time) string {
	if t.IsZero() {
		return human.None
	}
	return human.DateTime(t) + " (" + human.Ago(t, now) + ")"
}
