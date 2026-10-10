package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/wire"
)

// The view of a zathras: its state in full, its figures over the last
// fifteen minutes as sparklines with the value now, and its log.

// zathrasParts is how the rows of the view of a zathras are shared out.
type zathrasParts struct {
	state   []string // its state
	figures []string // its sparklines, or why there are none
	log     int      // how many rows of the log are in sight
}

// logHeight returns how many rows of the log are in sight.
func (m *model) logHeight() int {
	return max(m.shareZathras(m.bodyHeight()).log, 1)
}

// shareZathras shares h rows out among the parts of the view. The log
// has its title and three rows at least; where the terminal has no
// room for everything, the figures give way first, the last of them
// first, and then the end of the state.
func (m *model) shareZathras(h int) zathrasParts {
	if m.shared.zathras != nil && m.shared.zathrasH == h {
		return *m.shared.zathras
	}
	p := zathrasParts{state: m.stateLines(), figures: m.figureLines()}
	const logLeast = 1 + 3
	if over := len(p.state) + 1 + len(p.figures) + 1 + logLeast - h; over > 0 {
		// The figures give way from their end; their title alone is none.
		p.figures = p.figures[:max(len(p.figures)-over, 0)]
		if len(p.figures) == 1 && m.hasFigures() {
			p.figures = nil
		}
	}
	if over := len(p.state) + 1 + logLeast - h; len(p.figures) == 0 && over > 0 {
		p.state = p.state[:max(len(p.state)-over, 1)]
	}
	used := len(p.state) + 1 + 1 // an empty line, and the title of the log
	if len(p.figures) > 0 {
		used += len(p.figures) + 1
	}
	p.log = max(h-used, 0)
	m.shared.zathras, m.shared.zathrasH = &p, h
	return p
}

// zathrasView draws a zathras in at most h lines.
func (m *model) zathrasView(h int) []string {
	p := m.shareZathras(h)
	out := append([]string{}, p.state...)
	if len(p.figures) > 0 {
		out = append(out, "")
		out = append(out, p.figures...)
	}
	out = append(out, "", m.logTitle())
	return append(out, m.logLines(p.log)...)
}

// stateLines draws the state of the zathras in full, as it reported it
// last.
func (m *model) stateLines() []string {
	now := m.cfg.Now()
	z, st := m.seen, m.seen.State
	w := m.width
	var out []string
	if !m.present {
		// Review Focus 4: the zathras went while its view was open.
		text := "✖ This zathras is not connected to draal any more, since " + human.Clock(m.goneSince) + " (" + human.Ago(m.goneSince, now) +
			"). Below is what it reported last. Nothing is asked of it now; the view carries on when it is back."
		for _, l := range wrap(text, w-2, 3) {
			out = append(out, line(w, seg{" " + l, stBad}))
		}
	}
	// Its name, in full: also one of 253 characters.
	for _, l := range wrap(named(z.Name), w-2, 4) {
		out = append(out, line(w, plainText(" "), bold(l)))
	}
	sign, signSt := phaseSign(st.Phase)
	phase := []seg{plainText(" "), {sign, signSt}, {" " + string(knownPhase(st.Phase)), signSt}}
	switch note := phaseNote(st, now); {
	case note == "" || st.Generation == 0:
	case knownPhase(st.Phase) == wire.PhaseRunning:
		phase = append(phase, plainText(" "+note))
	default:
		phase = append(phase, plainText(", "+note))
	}
	stable := uint64(0)
	if svc, ok := m.serviceNamed(m.seenIn); ok {
		stable = svc.Stable
	}
	generation := "generation " + human.Generation(st.Generation)
	switch {
	case st.Generation == 0:
		phase = append(phase, dim(" · "), plainText("no generation: nothing was activated on it"))
	case st.Generation == stable:
		phase = append(phase, dim(" · "), plainText(generation), dim(", the stable one of "+named(m.seenIn)))
	case stable == 0:
		phase = append(phase, dim(" · "), plainText(generation), dim("; "+named(m.seenIn)+" has no stable one"))
	default:
		phase = append(phase, dim(" · "), plainText(generation), dim(", not the stable one of "+named(m.seenIn)+" ("+human.Generation(stable)+")"))
	}
	out = append(out, line(w, phase...))
	out = append(out, line(w, dim(" id         "), plainText(capped(string(z.ID)))))

	field := func(label, value string, rows int) {
		for i, l := range wrap(value, w-13, rows) {
			if i > 0 {
				label = ""
			}
			out = append(out, line(w, dim(" "+pad(label, 11)), plainText(l)))
		}
	}
	if st.Binary != "" {
		field("runs", strings.Join(append([]string{st.Binary}, st.Args...), " "), 3)
	}
	process := "not running"
	if st.PID != 0 {
		process = fmt.Sprintf("pid %d", st.PID)
	}
	field("process", process+", "+human.Count(int(min(st.Restarts, 1<<31)), "restart", "restarts")+" since this zathras started", 1)
	if e := st.LastExit; e != nil {
		// What is known of it, and no more: a zathras says when it was
		// and of which generation, and one that does not is not quoted.
		text := human.Exit(e)
		if !e.At.IsZero() {
			text += " at " + whenWords(e.At, now)
		}
		text += ", after " + human.Took(e.Ran)
		if e.Generation != 0 {
			text += ", of generation " + human.Generation(e.Generation)
		}
		if e.Error != "" {
			text += ": " + e.Error
		}
		field("last exit", text, 3)
	} else {
		field("last exit", "none: the service has not ended since this zathras started", 1)
	}
	connected := "since " + whenWords(z.Connected, now)
	if z.Connected.IsZero() {
		connected = "draal does not say since when"
	}
	if len(st.Prepared) > 0 {
		parts := make([]string, len(st.Prepared))
		for i, g := range st.Prepared {
			parts[i] = human.Generation(g)
		}
		connected += " · prepared " + strings.Join(parts, ", ")
	} else {
		connected += " · nothing prepared"
	}
	if st.Preparing != 0 {
		connected += " · preparing " + human.Generation(st.Preparing)
	}
	field("connected", connected, 2)
	return out
}

// hasFigures says whether the figures are sparklines, under a title, as
// against a sentence that says why there are none.
func (m *model) hasFigures() bool {
	return len(m.samples) > 0
}

// figureLines draws the figures of the container: a sparkline for each
// with the value now and the scale, or a sentence that says why there
// are none.
func (m *model) figureLines() []string {
	w := m.width
	if len(m.samples) == 0 {
		// Review Focus 4: a zathras without samples.
		switch {
		case m.history == askedFor:
			return sentences(w, stDim, "Figures: asking draal …")
		case m.history == askedRefused:
			return sentences(w, stBad, "Figures: draal does not send them: "+m.histErr)
		case m.history == askedNot && !m.present:
			return sentences(w, stDim, "Figures: they were not received before this zathras went.")
		case m.history == askedNot:
			return sentences(w, stDim, "Figures: not asked for: there is no connection to draal.")
		}
		return sentences(w, stPlain, "This zathras reports no figures. zathras reads them from cgroup v2 on Linux; where that is missing it sends none, and there is nothing to draw. Its state, its log and its shell do not depend on them.")
	}
	now := m.samples[len(m.samples)-1]
	const labelW, valueW, scaleW = 11, 11, 24
	sparkW := min(max(w-1-labelW-1-valueW-2-2-scaleW, 8), sparkWidest)
	row := func(label, value string, pick func(wire.Sample) float64, top float64, scale string) string {
		return line(w, dim(" "+pad(label, labelW)), plainText(" "+padLeft(value, valueW)+"  "),
			seg{spark(m.samples, pick, sparkW, top), stBusy}, dim("  "+scale))
	}
	cpu := func(s wire.Sample) float64 { return s.CPU }
	memory := func(s wire.Sample) float64 { return float64(s.Memory) }
	cpuTop := coreTop(largest(m.samples, cpu))
	memTop, memScale := byteTop(largest(m.samples, memory)), ""
	if now.MemoryLimit > 0 {
		memTop = float64(now.MemoryLimit)
		memScale = "to " + human.Bytes(now.MemoryLimit) + ", its limit"
	} else {
		memScale = "to " + human.Bytes(uint64(memTop)) + "; no limit"
	}
	rate := func(label string, pick func(wire.Sample) float64) string {
		top := byteTop(largest(m.samples, pick))
		return row(label, human.Rate(pick(now)), pick, top, "to "+human.Rate(top))
	}
	return []string{
		line(w, bold(" Figures of the container"), dim(" · the last 15 minutes, to "+human.Clock(now.At)+" ("+human.Ago(now.At, m.cfg.Now())+")")),
		row("CPU", human.CPU(now.CPU), cpu, cpuTop, "to "+human.CPU(cpuTop)+" of one core"),
		row("memory", human.Bytes(now.Memory), memory, memTop, memScale),
		rate("disk read", func(s wire.Sample) float64 { return s.DiskRead }),
		rate("disk write", func(s wire.Sample) float64 { return s.DiskWrite }),
		rate("net in", func(s wire.Sample) float64 { return s.NetRx }),
		rate("net out", func(s wire.Sample) float64 { return s.NetTx }),
	}
}

// logTitle draws the line above the log: what it is, and where one is
// in it.
func (m *model) logTitle() string {
	what := ""
	switch m.logIs {
	case askedNot:
		what = "not asked for: there is no connection to draal"
		if !m.present {
			what = "not asked for: the zathras is not connected"
		}
	case askedFor:
		// draal answers a log with its lines alone: until the first one
		// there is no telling a log that is followed and empty from one
		// that draal has not got to yet.
		what = "no line has come yet"
	case askedEnded:
		what = "ended"
		if m.present && m.conn == connLive {
			what = "ended; G asks for it again"
		}
	default:
		what = "followed live"
	}
	segs := []seg{bold(" Log"), dim(" · " + what)}
	if h := m.logHeight(); !m.follow && len(m.rows) > h {
		segs = append(segs, seg{fmt.Sprintf(" · rows %d to %d of %d; G goes to the end", m.logTop+1, min(m.logTop+h, len(m.rows)), len(m.rows)), stWarn})
	}
	return line(m.width, segs...)
}

// logLines draws h rows of the log: the end of it, or from the row one
// has scrolled to.
func (m *model) logLines(h int) []string {
	if h <= 0 {
		return nil
	}
	if len(m.rows) == 0 {
		return nil
	}
	top := m.logTop
	if m.follow {
		top = max(len(m.rows)-h, 0)
	}
	prefix := logPrefix(m.width)
	var out []string
	for _, r := range m.rows[top:min(top+h, len(m.rows))] {
		if r.entry >= len(m.log.entries) {
			continue
		}
		e := m.log.entries[r.entry]
		switch e.kind {
		case entryLost:
			out = append(out, line(m.width, seg{fmt.Sprintf("   … %s lost here: the log grew faster than it was read …", human.Count(int(min(e.lost, 1<<31)), "line was", "lines were")), stWarn}))
			continue
		case entryNote:
			out = append(out, line(m.width, dim("   — "+e.text)))
			continue
		}
		var b strings.Builder
		if r.sub > 0 {
			b.WriteString(strings.Repeat(" ", prefix))
		} else {
			b.WriteString(" ")
			if prefix > 3 {
				b.WriteString(line(9, dim(human.Clock(e.at)+" ")))
			}
			letter, st := streamSign(e.stream)
			b.WriteString(line(2, seg{letter + " ", st}))
		}
		b.WriteString(drawSpans(r.spans))
		out = append(out, b.String())
	}
	return out
}

// streamSign returns the letter and the look of a stream of a log.
func streamSign(stream string) (string, lipgloss.Style) {
	switch stream {
	case "stderr":
		return "e", stBad
	case "zathras":
		return "z", stBusy
	case "stdout":
		return "o", stDim
	}
	return "?", stDim
}
