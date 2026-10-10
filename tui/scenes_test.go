package tui

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// scene is a state of the terminal UI that the tests draw: a view, or
// one of the odd states of one.
type scene struct {
	name  string
	model func(w, h int) *model
}

// scenes returns every view and every odd state there is a sentence
// for.
func scenes() []scene {
	live := func(w, h int, f wire.Fleet) *model {
		m, _ := newTestModel(w, h)
		goLive(m, f)
		return m
	}
	return []scene{
		{"services", func(w, h int) *model { return live(w, h, fixtureFleet()) }},
		{"services-connecting", func(w, h int) *model { m, _ := newTestModel(w, h); return m }},
		{"services-no-answer", func(w, h int) *model {
			m, _ := newTestModel(w, h)
			m.update(dialFailedMsg{epoch: 1, err: errors.New("connecting to draal 3f9a2c1b: context deadline exceeded")})
			return m
		}},
		{"services-none", func(w, h int) *model { return live(w, h, wire.Fleet{Services: []wire.Service{}}) }},
		{"services-lost", func(w, h int) *model {
			m, clock := newTestModel(w, h)
			goLive(m, fixtureFleet())
			clock.at = t0.Add(42 * time.Second)
			m.update(lostMsg{epoch: 1})
			clock.at = t0.Add(time.Minute + 15*time.Second)
			m.update(tickMsg{})
			return m
		}},
		{"services-help", func(w, h int) *model { m := live(w, h, fixtureFleet()); press(m, "?"); return m }},
		{"service-rollout-under-way", func(w, h int) *model { m, _ := onService(w, h, "web"); return m }},
		{"service-halted", func(w, h int) *model { m, _ := onService(w, h, "worker"); press(m, "G"); return m }},
		{"service-no-zathras", func(w, h int) *model { m, _ := onService(w, h, "empty"); return m }},
		{"service-no-rollout", func(w, h int) *model { m, _ := onService(w, h, "fresh"); return m }},
		{"service-asking", func(w, h int) *model {
			m := live(w, h, fixtureFleet())
			press(m, "down", "down", "enter")
			return m
		}},
		{"service-refused", func(w, h int) *model {
			m := live(w, h, fixtureFleet())
			press(m, "down", "down", "enter")
			m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "web", err: wire.Errorf(wire.CodeInternal, "reading the rollouts: the database is locked")})
			return m
		}},
		{"service-gone", func(w, h int) *model {
			m, _ := onService(w, h, "web")
			f := fixtureFleet()
			f.Services = slices.Delete(f.Services, 2, 3)
			m.update(fleetMsg{epoch: 1, fleet: f})
			return m
		}},
		{"service-lost", func(w, h int) *model {
			m, clock := onService(w, h, "web")
			m.update(lostMsg{epoch: 1})
			clock.at = t0.Add(3 * time.Second)
			m.update(tickMsg{})
			return m
		}},
		{"zathras", func(w, h int) *model { m, _ := onZathras(w, h, "web", 2); return m }},
		{"zathras-backoff", func(w, h int) *model { m, _ := onZathras(w, h, "web", 3); return m }},
		{"zathras-no-samples", func(w, h int) *model { m, _ := onZathras(w, h, "worker", 2); return m }},
		{"zathras-idle-no-log", func(w, h int) *model {
			m, _ := onService(w, h, "fresh")
			press(m, "down", "enter")
			m.update(historyMsg{epoch: 1, n: m.historyN, zathras: m.zathras})
			return m
		}},
		{"zathras-asking", func(w, h int) *model { m, _ := onService(w, h, "web"); press(m, "enter"); return m }},
		{"zathras-long-name", func(w, h int) *model { m, _ := onZathras(w, h, "worker", 4); return m }},
		{"zathras-gone", func(w, h int) *model {
			m, clock := onZathras(w, h, "web", 4)
			clock.at = t0.Add(10 * time.Second)
			m.update(logEndedMsg{epoch: 1, id: m.logID})
			f := fixtureFleet()
			f.Services[2].Zathrases = f.Services[2].Zathrases[:4]
			m.update(fleetMsg{epoch: 1, fleet: f})
			clock.at = t0.Add(25 * time.Second)
			m.update(tickMsg{})
			return m
		}},
		{"zathras-lost", func(w, h int) *model {
			m, clock := onZathras(w, h, "web", 2)
			m.update(lostMsg{epoch: 1, err: errors.New("the connection to draal ended")})
			m.update(retryMsg{n: m.waitN})
			m.update(dialFailedMsg{epoch: m.epoch, err: errors.New("connecting to draal 3f9a2c1b: context deadline exceeded")})
			clock.at = t0.Add(90 * time.Second)
			m.update(tickMsg{})
			return m
		}},
		{"zathras-scrolled", func(w, h int) *model { m, _ := onZathras(w, h, "web", 2); press(m, "pgup", "pgup"); return m }},
		{"zathras-notice", func(w, h int) *model {
			m, _ := onZathras(w, h, "web", 2)
			press(m, "s")
			m.update(shellOpenedMsg{n: 1})
			m.update(shellEndedMsg{opened: true, exit: 3})
			return m
		}},
		{"zathras-opening-shell", func(w, h int) *model { m, _ := onZathras(w, h, "web", 2); press(m, "s"); return m }},
		{"zathras-no-shell", func(w, h int) *model {
			m, _ := onZathras(w, h, "web", 2)
			press(m, "s")
			m.update(shellOpenedMsg{n: 1, err: fmt.Errorf("operator: shell: %w", wire.Errorf(wire.CodeUnavailable, "zathras 22ab12cd is not connected"))})
			return m
		}},
		{"zathras-help", func(w, h int) *model { m, _ := onZathras(w, h, "web", 2); press(m, "?"); return m }},
	}
}
