package tui

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fables-for-robots/great-machine/wire"
)

// wantEffects fails the test when the effects are not those wanted.
func wantEffects(t *testing.T, what string, got []effect, want ...effect) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	// A request has a number, which counts up. Where a test does not
	// name it, it is not what the test is about.
	got = slices.Clone(got)
	for i := range min(len(got), len(want)) {
		switch w := want[i].(type) {
		case effRollouts:
			if g, ok := got[i].(effRollouts); ok && w.n == 0 {
				g.n = 0
				got[i] = g
			}
		case effHistory:
			if g, ok := got[i].(effHistory); ok && w.n == 0 {
				g.n = 0
				got[i] = g
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: the model wants %+v, want %+v", what, got, want)
	}
}

func TestTheFirstFleetMakesTheConnectionLive(t *testing.T) {
	clock := &fakeClock{at: t0}
	m := newModel(Config{Now: clock.now})
	wantEffects(t, "at the start", m.start(), effDial{epoch: 1})
	if m.conn != connConnecting || m.have {
		t.Errorf("before draal answered the connection is %v, and a fleet is there: %v", m.conn, m.have)
	}
	wantEffects(t, "dialed", m.update(dialedMsg{epoch: 1}))
	if m.conn != connConnecting {
		t.Errorf("dialed, and before the first fleet, the connection is %v", m.conn)
	}
	// The fleet comes in any order, and is kept in one: services by
	// name, zathrases by name and then by ID.
	f := fixtureFleet()
	slices.Reverse(f.Services)
	slices.Reverse(f.Services[1].Zathrases) // web
	wantEffects(t, "the first fleet", m.update(fleetMsg{epoch: 1, fleet: f}))
	if m.conn != connLive || !m.have || !m.fleetAt.Equal(t0) {
		t.Errorf("after the first fleet the connection is %v", m.conn)
	}
	var names []string
	for _, svc := range m.fleet.Services {
		names = append(names, svc.Name)
	}
	web := m.fleet.Services[2].Zathrases
	if strings.Join(names, " ") != "empty fresh web worker" || web[1].Name != web[2].Name || web[1].ID >= web[2].ID || web[0].Name != "web-7d9f8b6c4-2xkqp" {
		t.Errorf("the services are %v, and the zathrases of web %v %v %v", names, web[0].Name, web[1].ID[:2], web[2].ID[:2])
	}
	if m.svcName != "empty" || m.screen != screenServices {
		t.Errorf("the first service is not chosen: %q", m.svcName)
	}
	// A fleet of a connection that is not the one there is, is not read.
	m.update(fleetMsg{epoch: 0, fleet: wire.Fleet{}})
	m.update(fleetMsg{epoch: 2, fleet: wire.Fleet{}})
	if len(m.fleet.Services) != 4 {
		t.Error("a fleet of another connection was taken")
	}
}

// Every key of the contract's table, on every view.
func TestKeys(t *testing.T) {
	type state struct {
		screen screen
		chosen string // the service, or the first two characters of the zathras' ID
		help   bool
	}
	at := func(m *model) state {
		s := state{screen: m.screen, help: m.help}
		switch m.screen {
		case screenServices:
			s.chosen = m.svcName
		case screenService:
			s.chosen = string(m.zID[:min(2, len(m.zID))])
		case screenZathras:
			s.chosen = string(m.zathras[:2])
		}
		return s
	}
	services := func() *model { m, _ := newTestModel(80, 24); goLive(m, fixtureFleet()); press(m, "down"); return m } // fresh is chosen
	service := func() *model { m, _ := onService(80, 24, "web"); press(m, "down"); return m }                         // the second zathras
	zathras := func() *model { m, _ := onZathras(80, 24, "web", 1); return m }

	for _, tt := range []struct {
		on   string
		make func() *model
		key  string
		want state
		effs []effect
	}{
		// The services.
		{"services", services, "up", state{screenServices, "empty", false}, nil},
		{"services", services, "k", state{screenServices, "empty", false}, nil},
		{"services", services, "down", state{screenServices, "web", false}, nil},
		{"services", services, "j", state{screenServices, "web", false}, nil},
		{"services", services, "pgup", state{screenServices, "empty", false}, nil},
		{"services", services, "pgdown", state{screenServices, "worker", false}, nil},
		{"services", services, "g", state{screenServices, "empty", false}, nil},
		{"services", services, "G", state{screenServices, "worker", false}, nil},
		{"services", services, "enter", state{screenService, "10", false}, []effect{effRollouts{epoch: 1, service: "fresh"}}},
		{"services", services, "right", state{screenService, "10", false}, []effect{effRollouts{epoch: 1, service: "fresh"}}},
		{"services", services, "l", state{screenService, "10", false}, []effect{effRollouts{epoch: 1, service: "fresh"}}},
		{"services", services, "esc", state{screenServices, "fresh", false}, nil},
		{"services", services, "left", state{screenServices, "fresh", false}, nil},
		{"services", services, "h", state{screenServices, "fresh", false}, nil},
		{"services", services, "backspace", state{screenServices, "fresh", false}, nil},
		{"services", services, "s", state{screenServices, "fresh", false}, nil},
		{"services", services, "?", state{screenServices, "fresh", true}, nil},
		{"services", services, "q", state{screenServices, "fresh", false}, []effect{effQuit{}}},
		{"services", services, "ctrl+c", state{screenServices, "fresh", false}, []effect{effQuit{}}},
		{"services", services, "x", state{screenServices, "fresh", false}, nil},
		{"services", services, "tab", state{screenServices, "fresh", false}, nil},
		// A service.
		{"a service", service, "up", state{screenService, "20", false}, nil},
		{"a service", service, "k", state{screenService, "20", false}, nil},
		{"a service", service, "down", state{screenService, "22", false}, nil},
		{"a service", service, "j", state{screenService, "22", false}, nil},
		{"a service", service, "pgup", state{screenService, "20", false}, nil},
		{"a service", service, "pgdown", state{screenService, "24", false}, nil},
		{"a service", service, "g", state{screenService, "20", false}, nil},
		{"a service", service, "G", state{screenService, "24", false}, nil},
		{"a service", service, "enter", state{screenZathras, "21", false}, []effect{effHistory{epoch: 1, zathras: idOf(0x21)}, effFollow{epoch: 1, zathras: idOf(0x21), id: 1}}},
		{"a service", service, "right", state{screenZathras, "21", false}, []effect{effHistory{epoch: 1, zathras: idOf(0x21)}, effFollow{epoch: 1, zathras: idOf(0x21), id: 1}}},
		{"a service", service, "l", state{screenZathras, "21", false}, []effect{effHistory{epoch: 1, zathras: idOf(0x21)}, effFollow{epoch: 1, zathras: idOf(0x21), id: 1}}},
		{"a service", service, "esc", state{screenServices, "web", false}, nil},
		{"a service", service, "left", state{screenServices, "web", false}, nil},
		{"a service", service, "h", state{screenServices, "web", false}, nil},
		{"a service", service, "backspace", state{screenServices, "web", false}, nil},
		{"a service", service, "s", state{screenService, "21", false}, nil},
		{"a service", service, "?", state{screenService, "21", true}, nil},
		{"a service", service, "q", state{screenService, "21", false}, []effect{effQuit{}}},
		{"a service", service, "ctrl+c", state{screenService, "21", false}, []effect{effQuit{}}},
		// A zathras. The keys that move scroll its log: see TestLogScrolls.
		{"a zathras", zathras, "enter", state{screenZathras, "21", false}, nil},
		{"a zathras", zathras, "right", state{screenZathras, "21", false}, nil},
		{"a zathras", zathras, "l", state{screenZathras, "21", false}, nil},
		{"a zathras", zathras, "esc", state{screenService, "21", false}, []effect{effUnfollow{}}},
		{"a zathras", zathras, "left", state{screenService, "21", false}, []effect{effUnfollow{}}},
		{"a zathras", zathras, "h", state{screenService, "21", false}, []effect{effUnfollow{}}},
		{"a zathras", zathras, "backspace", state{screenService, "21", false}, []effect{effUnfollow{}}},
		{"a zathras", zathras, "?", state{screenZathras, "21", true}, nil},
		{"a zathras", zathras, "q", state{screenZathras, "21", false}, []effect{effQuit{}}},
		{"a zathras", zathras, "ctrl+c", state{screenZathras, "21", false}, []effect{effQuit{}}},
	} {
		m := tt.make()
		effs := press(m, tt.key)
		if got := at(m); got != tt.want {
			t.Errorf("%s, %s: now at %+v, want %+v", tt.on, tt.key, got, tt.want)
		}
		wantEffects(t, tt.on+", "+tt.key, effs, tt.effs...)
	}

	// The keys are said by any key, and q quits also then.
	m := services()
	press(m, "?")
	wantEffects(t, "a key while the keys are shown", press(m, "enter"))
	if m.help || m.screen != screenServices {
		t.Errorf("a key while the keys are shown: help %v, on %v; want the keys gone and nothing opened", m.help, m.screen)
	}
	press(m, "?")
	wantEffects(t, "q while the keys are shown", press(m, "q"), effQuit{})

	// s on a zathras: the place of the shell.
	m = zathras()
	wantEffects(t, "s on a zathras", press(m, "s"))
	if m.notice == "" {
		t.Error("s on a zathras says nothing")
	}
	press(m, "j")
	if m.notice != "" {
		t.Errorf("after the next key the notice is still %q", m.notice)
	}
	m.present = false
	press(m, "s")
	if !strings.Contains(m.notice, "not connected") {
		t.Errorf("s on a zathras that is gone says %q", m.notice)
	}
}

func TestOpeningWithNothingChosen(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, wire.Fleet{Services: []wire.Service{}})
	wantEffects(t, "enter without a service", press(m, "enter", "down", "up", "G", "g", "pgdown", "pgup"))
	if m.screen != screenServices || m.svcName != "" {
		t.Errorf("with no service, the model is on %v with %q chosen", m.screen, m.svcName)
	}
	m, _ = onService(80, 24, "empty")
	wantEffects(t, "enter without a zathras", press(m, "enter", "down", "up", "G", "g", "pgdown", "pgup"))
	if m.screen != screenService || m.zID != "" {
		t.Errorf("with no zathras, the model is on %v with %q chosen", m.screen, m.zID)
	}
}

// What is chosen stays chosen by what it is when the fleet changes
// around it, and when it is gone, what is at its place is chosen.
func TestWhatIsChosenStaysChosen(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	press(m, "down", "down") // web
	f := fixtureFleet()
	f.Services = append(f.Services, wire.Service{Name: "api"}, wire.Service{Name: "zeta"})
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.svcName != "web" || m.svcIdx != 3 {
		t.Errorf("with a service before it, %q is chosen at %d; want web at 3", m.svcName, m.svcIdx)
	}
	// web goes: what is at its place now is chosen.
	f = fixtureFleet()
	f.Services = slices.Delete(f.Services, 2, 3)
	f.Services = append(f.Services, wire.Service{Name: "api"})
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.svcName != "worker" || m.svcIdx != 3 {
		t.Errorf("with web gone, %q is chosen at %d; want worker at 3", m.svcName, m.svcIdx)
	}
	// Everything behind goes: the last is chosen.
	m.update(fleetMsg{epoch: 1, fleet: wire.Fleet{Services: []wire.Service{{Name: "api"}}}})
	if m.svcName != "api" || m.svcIdx != 0 {
		t.Errorf("with one service left, %q is chosen at %d", m.svcName, m.svcIdx)
	}
	m.update(fleetMsg{epoch: 1, fleet: wire.Fleet{}})
	if m.svcName != "" || m.svcIdx != 0 {
		t.Errorf("with no service left, %q is chosen at %d", m.svcName, m.svcIdx)
	}

	// The zathrases of a service: the second of the two of one name.
	m, _ = onService(80, 24, "web")
	press(m, "down", "down")
	if m.zID != idOf(0x22) {
		t.Fatalf("the third zathras is %s", m.zID[:2])
	}
	f = fixtureFleet()
	web := &f.Services[2]
	web.Zathrases = append([]wire.Zathras{{ID: idOf(0x1f), Name: "web-0"}}, web.Zathrases...)
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.zID != idOf(0x22) || m.zIdx != 3 {
		t.Errorf("with a zathras before it, %s is chosen at %d; want 22 at 3", m.zID[:2], m.zIdx)
	}
	// Its namesake goes: it is still the one chosen.
	web.Zathrases = slices.Delete(web.Zathrases, 2, 3)
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.zID != idOf(0x22) || m.zIdx != 2 {
		t.Errorf("with its namesake gone, %s is chosen at %d; want 22 at 2", m.zID[:2], m.zIdx)
	}
	// It goes: the one at its place is chosen.
	web.Zathrases = slices.Delete(web.Zathrases, 2, 3)
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.zID != idOf(0x23) || m.zIdx != 2 {
		t.Errorf("with it gone, %s is chosen at %d; want 23 at 2", m.zID[:2], m.zIdx)
	}
	// The service goes while its view is open, and is back: the view
	// stays, and what was chosen is chosen again.
	gone := fixtureFleet()
	gone.Services = slices.Delete(gone.Services, 2, 3)
	wantEffects(t, "the service goes", m.update(fleetMsg{epoch: 1, fleet: gone}))
	if _, ok := m.serviceNamed(m.service); ok || m.screen != screenService || m.service != "web" {
		t.Errorf("with its service gone, the model is on %v of %q", m.screen, m.service)
	}
	wantEffects(t, "keys on a service that is gone", press(m, "down", "enter", "G"))
	m.update(fleetMsg{epoch: 1, fleet: f})
	if m.zID != idOf(0x23) {
		t.Errorf("with its service back, %s is chosen; want 23", m.zID[:2])
	}
}

// A list that is longer than the terminal: what is chosen is in sight.
func TestWhatIsChosenIsInSight(t *testing.T) {
	f := wire.Fleet{}
	big := wire.Service{Name: "zzz-big"}
	for i := range 300 {
		f.Services = append(f.Services, wire.Service{Name: fmt.Sprintf("svc-%03d", i)})
		big.Zathrases = append(big.Zathrases, wire.Zathras{ID: idOf(i%256) + wire.ID(fmt.Sprint(i)), Name: fmt.Sprintf("big-%03d", i)})
	}
	f.Services = append(f.Services, big)
	m, _ := newTestModel(80, 24)
	goLive(m, f)
	inSight := func(what string, top, idx, h int) {
		t.Helper()
		if idx < top || idx >= top+h {
			t.Fatalf("%s: row %d is chosen, and rows %d to %d are in sight", what, idx, top, top+h-1)
		}
	}
	for _, key := range []string{"down", "down", "pgdown", "pgdown", "up", "G", "up", "pgup", "g", "pgdown", "G"} {
		press(m, key)
		inSight("services, "+key, m.svcTop, m.svcIdx, m.servicesHeight())
	}
	if m.svcName != "zzz-big" {
		t.Errorf("at the end of the services %q is chosen", m.svcName)
	}
	if m.svcTop != len(f.Services)-m.servicesHeight() {
		t.Errorf("at the end of %d services the first in sight is %d, and %d are in sight", len(f.Services), m.svcTop, m.servicesHeight())
	}
	press(m, "G", "enter")
	if svc, _ := m.serviceNamed(m.service); m.screen != screenService || len(svc.Zathrases) != 300 {
		t.Fatalf("the service with three hundred zathrases is not open: %q", m.service)
	}
	for _, key := range []string{"down", "pgdown", "pgdown", "up", "G", "up", "pgup", "g", "pgdown"} {
		press(m, key)
		inSight("zathrases, "+key, m.zTop, m.zIdx, m.zathrasesHeight())
	}
	// The terminal gets smaller: still in sight.
	press(m, "G")
	m.update(tea.WindowSizeMsg{Width: 60, Height: 12})
	inSight("zathrases, in a smaller terminal", m.zTop, m.zIdx, m.zathrasesHeight())
	// The fleet gets shorter: still in sight, and no empty rows at the end.
	big.Zathrases = big.Zathrases[:5]
	m.update(fleetMsg{epoch: 1, fleet: wire.Fleet{Services: []wire.Service{big}}})
	if m.zIdx != 4 || m.zTop != max(5-m.zathrasesHeight(), 0) {
		t.Errorf("with five zathrases left, %d is chosen and %d the first in sight", m.zIdx, m.zTop)
	}
}

// What a view holds, and when it is asked for again.
func TestServiceAsksForItsRolloutsAgainWhenTheLastOneChanges(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	press(m, "down", "down")
	wantEffects(t, "opening web", press(m, "enter"), effRollouts{epoch: 1, service: "web"})
	if m.rolloutsAre != askedFor {
		t.Errorf("asked for, the rollouts are %v", m.rolloutsAre)
	}
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "worker", rollouts: fixtureRollouts("worker")})
	if m.rolloutsAre != askedFor {
		t.Error("the rollouts of another service were taken")
	}
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "web", rollouts: fixtureRollouts("web")})
	if m.rolloutsAre != askedHave || len(m.rollouts) != 20 {
		t.Errorf("the rollouts are %v, %d of them", m.rolloutsAre, len(m.rollouts))
	}
	// The same rollout in the same state: nothing is asked.
	f := fixtureFleet()
	wantEffects(t, "the same fleet", m.update(fleetMsg{epoch: 1, fleet: f}))
	// Its counts change: nothing is asked.
	f.Services[2].Rollout.Activated = 3
	wantEffects(t, "other counts", m.update(fleetMsg{epoch: 1, fleet: f}))
	// Its state changes.
	f.Services[2].Rollout.State = wire.RolloutCompleted
	wantEffects(t, "another state", m.update(fleetMsg{epoch: 1, fleet: f}), effRollouts{epoch: 1, service: "web"})
	if m.rolloutsAre != askedHave || len(m.rollouts) != 20 {
		t.Error("while they are asked for again, the rollouts there were are gone")
	}
	wantEffects(t, "the same state again", m.update(fleetMsg{epoch: 1, fleet: f}))
	// Another rollout.
	f.Services[2].Rollout.ID, f.Services[2].Rollout.State = 32, wire.RolloutPreparing
	wantEffects(t, "another rollout", m.update(fleetMsg{epoch: 1, fleet: f}), effRollouts{epoch: 1, service: "web"})
	// A rollout of another service: nothing.
	f.Services[3].Rollout.ID = 33
	wantEffects(t, "a rollout of another service", m.update(fleetMsg{epoch: 1, fleet: f}))
	// On the view of a zathras nothing of rollouts is asked; back on the
	// service, they are asked for when the rollout has changed meanwhile.
	press(m, "enter")
	f.Services[2].Rollout.State = wire.RolloutActivating
	for _, e := range m.update(fleetMsg{epoch: 1, fleet: f}) {
		if _, is := e.(effRollouts); is {
			t.Error("the rollouts are asked for on the view of a zathras")
		}
	}
	wantEffects(t, "back on the service", press(m, "esc"), effUnfollow{}, effRollouts{epoch: 1, service: "web"})
	// draal refuses.
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "web", err: wire.Errorf(wire.CodeInternal, "the database is \x1b[2Jlocked")})
	if m.rolloutsAre != askedRefused || m.rolloutsErr != "internal: the database is locked" {
		t.Errorf("refused, the rollouts are %v with %q", m.rolloutsAre, m.rolloutsErr)
	}
	// Left and opened again: asked again; another service: none of the
	// first one's rollouts are left.
	press(m, "esc")
	wantEffects(t, "opening web again", press(m, "enter"), effRollouts{epoch: 1, service: "web"})
	press(m, "esc", "down")
	wantEffects(t, "opening worker", press(m, "enter"), effRollouts{epoch: 1, service: "worker"})
	if len(m.rollouts) != 0 || m.rolloutsAre != askedFor || m.rolloutsErr != "" {
		t.Errorf("on worker the rollouts of web are still there: %d, %v, %q", len(m.rollouts), m.rolloutsAre, m.rolloutsErr)
	}
}

func TestZathrasHoldsItsSamplesAndItsLog(t *testing.T) {
	m, _ := onService(80, 24, "web")
	wantEffects(t, "opening a zathras", press(m, "enter"),
		effHistory{epoch: 1, zathras: idOf(0x20)}, effFollow{epoch: 1, zathras: idOf(0x20), id: 1})
	if m.history != askedFor || m.logIs != askedFor || len(m.samples) != 1 {
		t.Errorf("asked for: history %v, log %v, and %d samples, want the one of the fleet", m.history, m.logIs, len(m.samples))
	}
	m.update(historyMsg{epoch: 1, n: m.historyN, zathras: idOf(0x21), samples: fixtureSamples()})
	if m.history != askedFor {
		t.Error("the samples of another zathras were taken")
	}
	m.update(historyMsg{epoch: 1, n: m.historyN, zathras: idOf(0x20), samples: fixtureSamples()})
	if m.history != askedHave || len(m.samples) != 450 {
		t.Errorf("history %v with %d samples, want 450: the one of the fleet is the last of the history", m.history, len(m.samples))
	}
	// The samples of the fleets that follow are added, and the oldest go.
	f := fixtureFleet()
	f.Services[2].Zathrases[0].State.Sample = &wire.Sample{At: t0.Add(2 * time.Second), CPU: 3}
	wantEffects(t, "a fleet with a sample", m.update(fleetMsg{epoch: 1, fleet: f}))
	if n := len(m.samples); n != 451 || m.samples[n-1].CPU != 3 || m.seen.State.Sample.CPU != 3 {
		t.Errorf("%d samples, the last with %v cores", n, m.samples[n-1].CPU)
	}
	f.Services[2].Zathrases[0].State.Sample = &wire.Sample{At: t0.Add(4 * time.Second), CPU: 4}
	m.update(fleetMsg{epoch: 1, fleet: f})
	if n := len(m.samples); n != 451 || m.samples[n-1].CPU != 4 {
		t.Errorf("%d samples, the last with %v cores: want the oldest gone", n, m.samples[n-1].CPU)
	}
	// The log: until its first line it is asked for, and no more is
	// known of it: draal answers a log with its lines alone.
	if m.logIs != askedFor || len(m.rows) != 0 {
		t.Errorf("before its first line, the log is %v with %d rows", m.logIs, len(m.rows))
	}
	m.update(logMsg{epoch: 1, id: 0, lines: fixtureLines(5)})
	m.update(logMsg{epoch: 0, id: 1, lines: fixtureLines(5)})
	if len(m.rows) != 0 {
		t.Error("lines of another log were taken")
	}
	m.update(logMsg{epoch: 1, id: 1, lines: fixtureLines(5)})
	if len(m.rows) != 5 || len(m.log.entries) != 5 {
		t.Errorf("%d rows of %d lines", len(m.rows), len(m.log.entries))
	}
	// Left: the log is ended, and what is on its way is not taken by
	// the next view.
	wantEffects(t, "leaving", press(m, "esc"), effUnfollow{})
	press(m, "down")
	wantEffects(t, "opening the next", press(m, "enter"),
		effHistory{epoch: 1, zathras: idOf(0x21)}, effFollow{epoch: 1, zathras: idOf(0x21), id: 3})
	m.update(logMsg{epoch: 1, id: 1, lines: fixtureLines(5)})
	m.update(historyMsg{epoch: 1, n: m.historyN, zathras: idOf(0x20), samples: fixtureSamples()})
	if len(m.rows) != 0 || len(m.samples) != 1 || m.history != askedFor {
		t.Errorf("the next zathras has %d rows and %d samples of the one before", len(m.rows), len(m.samples))
	}
	// draal refuses the samples: a zathras that reports none has none.
	m.update(historyMsg{epoch: 1, n: m.historyN, zathras: idOf(0x21), err: wire.Errorf(wire.CodeUnavailable, "zathras 21 is not connected")})
	if m.history != askedRefused || m.histErr != "unavailable: zathras 21 is not connected" {
		t.Errorf("refused, the history is %v with %q", m.history, m.histErr)
	}
}

// Review Focus 4: a zathras that goes while its view is open, and comes
// back.
func TestZathrasGoesWhileItsViewIsOpenAndComesBack(t *testing.T) {
	m, clock := onZathras(80, 24, "web", 4)
	id := idOf(0x24)
	if m.zathras != id || !m.present || len(m.rows) == 0 {
		t.Fatalf("the view is of %s, present: %v, with %d rows", m.zathras[:2], m.present, len(m.rows))
	}
	samples := len(m.samples)

	// It goes: draal ends its log, and the next fleet is without it.
	clock.at = t0.Add(10 * time.Second)
	m.update(logEndedMsg{epoch: 1, id: m.logID})
	gone := fixtureFleet()
	web := &gone.Services[2]
	web.Zathrases = web.Zathrases[:4]
	wantEffects(t, "the fleet without it", m.update(fleetMsg{epoch: 1, fleet: gone}))
	if m.screen != screenZathras || m.zathras != id || m.present || !m.goneSince.Equal(clock.at) {
		t.Errorf("gone: on %v of %s, present: %v, gone since %v", m.screen, m.zathras[:2], m.present, m.goneSince)
	}
	// What it reported last, its samples and its log are kept.
	if m.seen.Name != "web-7d9f8b6c4-q5rvd" || m.seen.State.PID != 31000 || len(m.samples) != samples || len(m.log.entries) != logKept || m.logIs != askedEnded {
		t.Errorf("gone, the view has %q, %d samples, %d lines and a log that is %v", m.seen.Name, len(m.samples), len(m.log.entries), m.logIs)
	}
	if last := m.log.entries[len(m.log.entries)-1]; last.kind != entryNote || last.text != "The log has ended." {
		t.Errorf("the last row of the log is %+v", last)
	}
	// Nothing is asked of a zathras that is gone: not by a fleet, not by G.
	clock.at = t0.Add(12 * time.Second)
	wantEffects(t, "another fleet without it", m.update(fleetMsg{epoch: 1, fleet: gone}))
	wantEffects(t, "G on a zathras that is gone", press(m, "G"))
	if !m.goneSince.Equal(t0.Add(10 * time.Second)) {
		t.Errorf("gone since %v, want the time it was first missed", m.goneSince)
	}

	// It is back, with the ID it had: its samples and its log are asked
	// for again, and no line is there twice.
	clock.at = t0.Add(16 * time.Second)
	back := fixtureFleet()
	back.Services[2].Zathrases[4].Connected = clock.at
	back.Services[2].Zathrases[4].State.PID = 5
	logID := m.logID
	wantEffects(t, "the fleet with it again", m.update(fleetMsg{epoch: 1, fleet: back}),
		effHistory{epoch: 1, zathras: id}, effFollow{epoch: 1, zathras: id, id: logID + 1})
	if !m.present || m.seen.State.PID != 5 || m.logIs != askedFor {
		t.Errorf("back: present %v, pid %d, log %v", m.present, m.seen.State.PID, m.logIs)
	}
	again := append(fixtureLines(400), wire.LogLine{At: t0.Add(17 * time.Second), Stream: "stdout", Text: "back"})
	m.update(logMsg{epoch: 1, id: m.logID, lines: again})
	if got := len(m.log.entries); got != 400 {
		t.Errorf("%d rows are kept", got)
	}
	n := len(m.log.entries)
	if textOf(m.log.entries[n-1]) != "back" || m.log.entries[n-2].kind != entryNote || m.log.entries[n-3].kind != entryLine {
		t.Errorf("the end of the log is %q, %q, %q; want the last line of before, the note, and the new line",
			textOf(m.log.entries[n-3]), textOf(m.log.entries[n-2]), textOf(m.log.entries[n-1]))
	}
	wantEffects(t, "the fleet with it once more", m.update(fleetMsg{epoch: 1, fleet: back}))

	// It connects again between two fleets: it was never missed, and its
	// log has ended all the same.
	clock.at = t0.Add(30 * time.Second)
	back.Services[2].Zathrases[4].Connected = clock.at
	logID = m.logID
	wantEffects(t, "the fleet with it connected anew", m.update(fleetMsg{epoch: 1, fleet: back}),
		effHistory{epoch: 1, zathras: id}, effFollow{epoch: 1, zathras: id, id: logID + 1})

	// A log that ends while the zathras stays: G asks for it again.
	m.update(logEndedMsg{epoch: 1, id: m.logID, err: wire.Errorf(wire.CodeUnavailable, "the connection of zathras 24 failed")})
	if last := m.log.entries[len(m.log.entries)-1]; last.text != "draal does not send the log: the connection of zathras 24 failed" {
		t.Errorf("the last row of the log is %q", last.text)
	}
	logID = m.logID
	wantEffects(t, "G on a log that has ended", press(m, "G"), effFollow{epoch: 1, zathras: id, id: logID + 1})
	wantEffects(t, "G on a log that is followed", press(m, "G"))

	// Left while it is gone: back on its service, with what is at its
	// place chosen; nothing is to be ended.
	m.update(logEndedMsg{epoch: 1, id: m.logID})
	m.update(fleetMsg{epoch: 1, fleet: gone})
	wantEffects(t, "leaving a zathras that is gone", press(m, "esc"))
	if m.screen != screenService || m.zID != idOf(0x23) {
		t.Errorf("back on %v with %s chosen, want the service with 23", m.screen, m.zID[:min(2, len(m.zID))])
	}
}

// Review Focus 3: the connection is lost and is back, on every view:
// what was shown stays, a dial is tried again with pauses that grow, a
// key ends a pause, and what the view holds is asked for again.
func TestConnectionLostAndBack(t *testing.T) {
	for _, tt := range []struct {
		on    string
		make  func() (*model, *fakeClock)
		again func(m *model) []effect // what the view asks for when the connection is back
	}{
		{"services", func() (*model, *fakeClock) {
			m, c := newTestModel(80, 24)
			goLive(m, fixtureFleet())
			press(m, "down")
			return m, c
		},
			func(m *model) []effect { return nil }},
		{"a service", func() (*model, *fakeClock) { m, c := onService(80, 24, "web"); press(m, "down"); return m, c },
			func(m *model) []effect { return []effect{effRollouts{epoch: m.epoch, service: "web"}} }},
		{"a zathras", func() (*model, *fakeClock) { return onZathras(80, 24, "web", 1) },
			func(m *model) []effect {
				return []effect{effHistory{epoch: m.epoch, zathras: idOf(0x21)}, effFollow{epoch: m.epoch, zathras: idOf(0x21), id: m.logID}}
			}},
	} {
		m, clock := tt.make()
		screen, svc, zID, rollouts, samples := m.screen, m.svcName, m.zID, len(m.rollouts), len(m.samples)

		// Lost: the watch ended.
		clock.at = t0.Add(time.Minute)
		wantEffects(t, tt.on+": lost", m.update(lostMsg{epoch: 1}), effHangUp{}, effWait{pause: time.Second, n: 1})
		if m.conn != connLost || !m.since.Equal(clock.at) || !m.retryAt.Equal(clock.at.Add(time.Second)) {
			t.Errorf("%s: lost: the connection is %v since %v, and a dial is tried at %v", tt.on, m.conn, m.since, m.retryAt)
		}
		// What was shown stays.
		if m.screen != screen || m.svcName != svc || m.zID != zID || len(m.fleet.Services) != 4 || len(m.rollouts) != rollouts || len(m.samples) != samples {
			t.Errorf("%s: lost: the view is not what it was", tt.on)
		}
		if tt.on == "a zathras" && (len(m.log.entries) != logKept || m.logIs != askedEnded || m.log.entries[logKept-1].text != "The connection to draal was lost.") {
			t.Errorf("%s: lost: the log has %d lines and is %v", tt.on, len(m.log.entries), m.logIs)
		}
		// What is still on its way from the old connection is not read,
		// and a second end of it changes nothing.
		wantEffects(t, tt.on+": lost twice", m.update(lostMsg{epoch: 1, err: errors.New("stream closed")}))
		wantEffects(t, tt.on+": a fleet of the old connection", m.update(fleetMsg{epoch: 1, fleet: wire.Fleet{}}))
		m.update(logMsg{epoch: 1, id: m.logID, lines: fixtureLines(3)})
		if m.conn != connLost || len(m.fleet.Services) != 4 {
			t.Errorf("%s: the old connection was read after it was lost", tt.on)
		}

		// The pauses double up to the longest: 1s, then 2s, 4s, …, 30s.
		epoch := m.epoch
		for i, pause := range []time.Duration{2, 4, 8, 16, 30, 30} {
			clock.at = m.retryAt
			wantEffects(t, tt.on+": the pause is over", m.update(retryMsg{n: m.waitN}), effDial{epoch: epoch + 1 + i})
			if !m.retryAt.IsZero() {
				t.Errorf("%s: while a dial is under way, a pause ends at %v", tt.on, m.retryAt)
			}
			wantEffects(t, tt.on+": the dial fails", m.update(dialFailedMsg{epoch: m.epoch, err: errors.New("draal does not answer\x07")}),
				effWait{pause: pause * time.Second, n: 2 + i})
			if !m.retryAt.Equal(clock.at.Add(pause * time.Second)) {
				t.Errorf("%s: the pause of %ds ends at %v", tt.on, pause, m.retryAt)
			}
			if m.dialErr != "draal does not answer" || m.conn != connLost || !m.since.Equal(t0.Add(time.Minute)) {
				t.Errorf("%s: after a dial that failed: %q, %v since %v", tt.on, m.dialErr, m.conn, m.since)
			}
		}
		// A pause that is not the one that counts ends nothing.
		wantEffects(t, tt.on+": an old pause is over", m.update(retryMsg{n: m.waitN - 1}))
		// A key ends a pause at once, and does what it does.
		epoch = m.epoch
		wantEffects(t, tt.on+": a key in a pause", press(m, "?"), effDial{epoch: epoch + 1})
		if !m.help || !m.retryAt.IsZero() {
			t.Errorf("%s: a key in a pause: help %v, and the pause ends at %v", tt.on, m.help, m.retryAt)
		}
		// The pause that the key ended is over: nothing. And a key while
		// a dial is under way dials no second time.
		wantEffects(t, tt.on+": the pause the key ended is over", m.update(retryMsg{n: m.waitN}))
		wantEffects(t, tt.on+": a key while a dial is under way", press(m, "?"))

		// Back: Dial succeeds, and with the first fleet the connection is
		// live, on the view it was on, and what the view holds is asked
		// for again of the new connection.
		clock.at = clock.at.Add(time.Second)
		wantEffects(t, tt.on+": dialed", m.update(dialedMsg{epoch: m.epoch}))
		if m.conn != connLost {
			t.Errorf("%s: dialed, and before a fleet came, the connection is %v", tt.on, m.conn)
		}
		logID := m.logID
		effs := m.update(fleetMsg{epoch: m.epoch, fleet: fixtureFleet()})
		wantEffects(t, tt.on+": back", effs, tt.again(m)...)
		if m.conn != connLive || m.screen != screen || m.svcName != svc || m.zID != zID || m.dialErr != "" || m.pause != 0 {
			t.Errorf("%s: back: the connection is %v, on %v with %q and %q chosen", tt.on, m.conn, m.screen, m.svcName, m.zID)
		}
		if tt.on == "a zathras" {
			if m.logID != logID+1 {
				t.Errorf("the log that is followed anew has the number %d, and the one before had %d", m.logID, logID)
			}
			// The ring comes once more: no line is there twice.
			before := len(m.log.entries)
			m.update(logMsg{epoch: m.epoch, id: m.logID, lines: fixtureLines(400)})
			if len(m.log.entries) != before {
				t.Errorf("after the reconnect the log has %d rows, and had %d: lines were taken twice", len(m.log.entries), before)
			}
		}
		// Lost again: the first pause is the first one again.
		n := m.waitN + 1
		wantEffects(t, tt.on+": lost again", m.update(lostMsg{epoch: m.epoch}), effHangUp{}, effWait{pause: time.Second, n: n})
	}
}

// A view that is opened while the connection is lost asks for nothing,
// and for what it holds when the connection is back.
func TestViewsOpenedWhileTheConnectionIsLost(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	m.update(lostMsg{epoch: 1})
	m.update(retryMsg{n: m.waitN}) // a dial is under way: keys end no pause
	press(m, "down", "down")
	wantEffects(t, "opening a service without a connection", press(m, "enter"))
	if m.screen != screenService || m.rolloutsAre != askedNot {
		t.Errorf("on %v, with rollouts that are %v", m.screen, m.rolloutsAre)
	}
	wantEffects(t, "opening a zathras without a connection", press(m, "enter"))
	if m.screen != screenZathras || m.history != askedNot || m.logIs != askedNot {
		t.Errorf("on %v, with a history that is %v and a log that is %v", m.screen, m.history, m.logIs)
	}
	wantEffects(t, "s without a connection", press(m, "s"))
	if !strings.Contains(m.notice, "no connection") {
		t.Errorf("s without a connection says %q", m.notice)
	}
	wantEffects(t, "back without a connection", press(m, "esc"))
	press(m, "enter")
	wantEffects(t, "the connection is back", goLive(m, fixtureFleet()),
		effHistory{epoch: m.epoch, zathras: idOf(0x20)}, effFollow{epoch: m.epoch, zathras: idOf(0x20), id: m.logID})
	wantEffects(t, "back on the service", press(m, "esc"), effUnfollow{}, effRollouts{epoch: m.epoch, service: "web"})
}

// The rollouts of a service are asked for, and a zathras is opened
// before draal has answered: the answer is taken all the same, and back
// on the service the list is there. No state is left that says "asking"
// for ever.
func TestRolloutsThatAreAnsweredOnAnotherView(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	press(m, "down", "down")
	wantEffects(t, "opening web", press(m, "enter"), effRollouts{epoch: 1, n: 1, service: "web"})
	press(m, "enter") // a zathras, before the answer
	m.update(rolloutsMsg{epoch: 1, n: 1, service: "web", rollouts: fixtureRollouts("web")})
	wantEffects(t, "back on the service", press(m, "esc"), effUnfollow{})
	if m.rolloutsAre != askedHave || len(m.rollouts) != 20 {
		t.Errorf("back on the service the rollouts are %v, %d of them; want the answer that came meanwhile", m.rolloutsAre, len(m.rollouts))
	}
	// The same with an answer that never comes while the zathras is in
	// sight: the list is still asked for, and its answer still counts.
	m, _ = newTestModel(80, 24)
	goLive(m, fixtureFleet())
	press(m, "down", "down", "enter", "enter", "esc")
	if m.rolloutsAre != askedFor {
		t.Fatalf("the rollouts are %v", m.rolloutsAre)
	}
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "web", rollouts: fixtureRollouts("web")})
	if m.rolloutsAre != askedHave {
		t.Errorf("the answer that came late was not taken: the rollouts are %v", m.rolloutsAre)
	}
	// An answer for a service that is not the open one any more is not
	// taken for the one that is.
	press(m, "esc", "down", "enter")
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN - 1, service: "web", rollouts: fixtureRollouts("web")})
	m.update(rolloutsMsg{epoch: 1, n: m.rolloutsN, service: "web", rollouts: fixtureRollouts("web")})
	if m.service != "worker" || len(m.rollouts) != 0 {
		t.Errorf("on %s, %d rollouts of web were taken", m.service, len(m.rollouts))
	}
}

// Two requests are under way, and the answer to the older one comes
// last: it is dropped, for the rollouts and for the samples alike.
func TestAnOlderAnswerThatComesLastIsDropped(t *testing.T) {
	m, _ := newTestModel(80, 24)
	f := fixtureFleet()
	goLive(m, f)
	wantEffects(t, "opening web", press(m, "down", "down", "enter"), effRollouts{epoch: 1, n: 1, service: "web"})
	f.Services[2].Rollout.State = wire.RolloutCompleted
	wantEffects(t, "another state", m.update(fleetMsg{epoch: 1, fleet: f}), effRollouts{epoch: 1, n: 2, service: "web"})
	newer, older := fixtureRollouts("web"), fixtureRollouts("web")
	newer[0].State = wire.RolloutCompleted
	m.update(rolloutsMsg{epoch: 1, n: 2, service: "web", rollouts: newer})
	m.update(rolloutsMsg{epoch: 1, n: 1, service: "web", rollouts: older})
	m.update(rolloutsMsg{epoch: 1, n: 1, service: "web", err: wire.Errorf(wire.CodeInternal, "busy")})
	if m.rollouts[0].State != wire.RolloutCompleted || m.rolloutsAre != askedHave {
		t.Errorf("the list has rollout %d as %s (%v): an older answer was taken over a newer one", m.rollouts[0].ID, m.rollouts[0].State, m.rolloutsAre)
	}

	// The samples: asked for when the view is opened, and again when the
	// zathras has connected anew.
	wantEffects(t, "opening a zathras", press(m, "enter"),
		effHistory{epoch: 1, n: 1, zathras: idOf(0x20)}, effFollow{epoch: 1, zathras: idOf(0x20), id: 1})
	f.Services[2].Zathrases[0].Connected = t0
	wantEffects(t, "connected anew", m.update(fleetMsg{epoch: 1, fleet: f}),
		effHistory{epoch: 1, n: 2, zathras: idOf(0x20)}, effFollow{epoch: 1, zathras: idOf(0x20), id: 2})
	m.update(historyMsg{epoch: 1, n: 2, zathras: idOf(0x20), samples: fixtureSamples()})
	m.update(historyMsg{epoch: 1, n: 1, zathras: idOf(0x20), err: wire.Errorf(wire.CodeUnavailable, "zathras 20 is not connected")})
	if m.history != askedHave || m.histErr != "" || len(m.samples) != 450 {
		t.Errorf("the history is %v with %q and %d samples: an older answer was taken over a newer one", m.history, m.histErr, len(m.samples))
	}
}

// After a time in which the model was told nothing (a shell had the
// terminal), what the view that is open holds is asked for anew.
func TestAgainAsksForWhatTheViewHolds(t *testing.T) {
	m, _ := newTestModel(80, 24)
	goLive(m, fixtureFleet())
	wantEffects(t, "on the services", m.again())
	press(m, "down", "down", "enter")
	wantEffects(t, "on a service", m.again(), effRollouts{epoch: 1, service: "web"})
	press(m, "enter")
	m.update(logMsg{epoch: 1, id: m.logID, lines: fixtureLines(10)})
	id := m.logID
	wantEffects(t, "on a zathras", m.update(shellEndedMsg{}), effHistory{epoch: 1, zathras: idOf(0x20)}, effFollow{epoch: 1, zathras: idOf(0x20), id: id + 1})
	// What was on its way is not read, and the ring that comes anew
	// brings no line twice.
	m.update(logMsg{epoch: 1, id: id, lines: fixtureLines(10)})
	m.update(logMsg{epoch: 1, id: m.logID, lines: fixtureLines(10)})
	if len(m.log.entries) != 10 {
		t.Errorf("the log has %d lines, want the 10 it had", len(m.log.entries))
	}
	m.present = false
	wantEffects(t, "on a zathras that is gone", m.again())
	m.present = true
	m.update(lostMsg{epoch: 1})
	wantEffects(t, "without a connection", m.again())
}

// What draal refuses an operator at the start ends the terminal UI. A
// draal that does not answer is tried again.
func TestTheFirstDial(t *testing.T) {
	refused := fmt.Errorf("asking draal for the fleet: %w", wire.Errorf(wire.CodeUnauthorized, "this operator is not on the allowlist"))

	m, clock := newTestModel(80, 24)
	effs := m.update(dialFailedMsg{epoch: 1, err: refused})
	wantEffects(t, "refused at the first dial", effs, effFail{err: refused})
	if !errors.Is(m.fatal, refused) {
		t.Errorf("the model ends with %v", m.fatal)
	}

	// No answer: tried again after a second, then two, and the header
	// has why.
	m, clock = newTestModel(80, 24)
	wantEffects(t, "no answer", m.update(dialFailedMsg{epoch: 1, err: errors.New("connecting to draal 3f9a: timeout")}), effWait{pause: time.Second, n: 1})
	if m.conn != connConnecting || m.dialErr != "connecting to draal 3f9a: timeout" || !m.since.Equal(t0) {
		t.Errorf("no answer: the connection is %v since %v, with %q", m.conn, m.since, m.dialErr)
	}
	clock.at = t0.Add(time.Second)
	wantEffects(t, "the pause is over", m.update(retryMsg{n: 1}), effDial{epoch: 2})
	wantEffects(t, "the answer to the first dial comes late", m.update(dialFailedMsg{epoch: 1, err: refused}))
	wantEffects(t, "no answer again", m.update(dialFailedMsg{epoch: 2, err: errors.New("timeout")}), effWait{pause: 2 * time.Second, n: 2})
	// Refused at a later first dial: fatal all the same, as draal never
	// let this operator in.
	m.update(retryMsg{n: 2})
	wantEffects(t, "refused at the third dial", m.update(dialFailedMsg{epoch: 3, err: refused}), effFail{err: refused})

	// Refused after draal had let it in once (the allowlist was changed
	// while the terminal UI was open): it is said, and tried again.
	m, _ = newTestModel(80, 24)
	goLive(m, fixtureFleet())
	m.update(lostMsg{epoch: 1})
	m.update(retryMsg{n: m.waitN})
	wantEffects(t, "refused after it was let in", m.update(dialFailedMsg{epoch: m.epoch, err: refused}), effWait{pause: 2 * time.Second, n: 2})
	if m.fatal != nil || !strings.Contains(m.dialErr, "not on the allowlist") {
		t.Errorf("refused after it was let in: fatal %v, and the header has %q", m.fatal, m.dialErr)
	}

	// The times of a Config are what counts.
	m = newModel(Config{RetryFirst: 10 * time.Millisecond, RetryMax: 25 * time.Millisecond, Now: clock.now})
	m.start()
	for _, pause := range []time.Duration{10, 20, 25, 25} {
		effs := m.update(dialFailedMsg{epoch: m.epoch, err: errors.New("no")})
		wantEffects(t, "with other pauses", effs, effWait{pause: pause * time.Millisecond, n: m.waitN})
		m.update(retryMsg{n: m.waitN})
	}
}

// The log of a zathras: the end is followed until one scrolls, what one
// reads stays where it is while lines come, and G follows again.
func TestLogScrolls(t *testing.T) {
	m, _ := onZathras(80, 24, "web", 1)
	h := m.logHeight()
	if !m.follow || len(m.rows) <= h {
		t.Fatalf("at first the end is followed: %v, with %d rows and %d in sight", m.follow, len(m.rows), h)
	}
	last := func() int { return len(m.rows) - m.logHeight() }
	press(m, "up")
	if m.follow || m.logTop != last()-1 {
		t.Errorf("up: following %v, the first row in sight is %d of %d", m.follow, m.logTop, last())
	}
	press(m, "k", "pgup")
	if want := last() - 2 - (h - 1); m.logTop != want {
		t.Errorf("k and PgUp: the first row in sight is %d, want %d", m.logTop, want)
	}
	// Lines come: what is read stays in sight, by what it is.
	reading := m.rows[m.logTop]
	more := []wire.LogLine{{At: t0.Add(time.Second), Stream: "stdout", Text: "new 1"}, {At: t0.Add(2 * time.Second), Stream: "stdout", Text: "new 2"}}
	m.update(logMsg{epoch: 1, id: m.logID, lines: more})
	if now := m.rows[m.logTop]; m.follow || now.seq != reading.seq || now.sub != reading.sub {
		t.Errorf("after two more lines the first row in sight is of line %d, and was of line %d", now.seq, reading.seq)
	}
	// Down to the end follows again.
	press(m, "down", "j")
	if m.follow {
		t.Error("two rows down, and well before the end, the end is followed")
	}
	press(m, "pgdown", "pgdown", "pgdown")
	if !m.follow {
		t.Errorf("at the end, the end is not followed: the first row in sight is %d of %d", m.logTop, last())
	}
	press(m, "g")
	if m.follow || m.logTop != 0 {
		t.Errorf("g: following %v, the first row in sight is %d", m.follow, m.logTop)
	}
	press(m, "up", "pgup")
	if m.logTop != 0 {
		t.Errorf("up at the start: the first row in sight is %d", m.logTop)
	}
	// What is read is dropped by a flood: the oldest there is, is read.
	var flood []wire.LogLine
	for i := range 1000 {
		flood = append(flood, wire.LogLine{At: t0.Add(time.Minute + time.Duration(i)*time.Millisecond), Stream: "stdout", Text: fmt.Sprintf("flood %d", i)})
	}
	m.update(logMsg{epoch: 1, id: m.logID, lines: flood})
	if m.follow || m.logTop != 0 || len(m.log.entries) != logKept || len(m.rows) != logKept {
		t.Errorf("after a flood: following %v, the first row in sight is %d, of %d rows", m.follow, m.logTop, len(m.rows))
	}
	press(m, "G")
	if !m.follow {
		t.Error("G does not follow the end")
	}
	// A terminal that is narrower: the lines take more rows, and none is
	// wider than there is room.
	m.update(logMsg{epoch: 1, id: m.logID, lines: fixtureLines(100)[60:61]}) // already there: not taken
	m.update(logMsg{epoch: 1, id: m.logID, lines: []wire.LogLine{{At: t0.Add(time.Hour), Stream: "stderr", Text: strings.Repeat("0123456789", 1000)}}})
	for _, w := range []int{200, 80, 50, 61} {
		m.update(tea.WindowSizeMsg{Width: w, Height: 24})
		room := w - logPrefix(w)
		long := 0
		for _, r := range m.rows {
			if spansWidth(r.spans) > room {
				t.Fatalf("at %d columns a row is %d cells wide, and there is room for %d", w, spansWidth(r.spans), room)
			}
			if r.seq == m.log.entries[len(m.log.entries)-1].seq {
				long++
			}
		}
		if long != logRowsPerLine {
			t.Errorf("at %d columns a line of ten thousand characters takes %d rows, want %d", w, long, logRowsPerLine)
		}
		lastRow := m.rows[len(m.rows)-1]
		if got := lastRow.spans[len(lastRow.spans)-1].text; got != ellipsis {
			t.Errorf("at %d columns the last row of a line that is cut ends in %q", w, got)
		}
	}
	// A log that fits is not scrolled.
	m, _ = onZathras(80, 24, "web", 1)
	m.log, m.rows = newLogBuffer(), nil
	m.update(logMsg{epoch: 1, id: m.logID, lines: fixtureLines(2)})
	press(m, "up", "g", "pgup", "down")
	if !m.follow || m.logTop != 0 {
		t.Errorf("a log of two lines: following %v, the first row in sight is %d", m.follow, m.logTop)
	}
}

// Review Focus 5: a terminal that is small, or resized. In one that is
// too small the terminal UI says so in one line; at every size it draws
// without a panic, no more lines than there are rows, none wider than
// there are columns.
func TestEverySizeOfTerminal(t *testing.T) {
	check := func(what string, m *model, w, h int, keys ...string) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("%s at %d×%d: panic: %v", what, w, h, r)
			}
		}()
		m.update(tea.WindowSizeMsg{Width: w, Height: h})
		press(m, keys...)
		drawn := m.view()
		if found := unsafe(drawn); found != "" {
			t.Fatalf("%s at %d×%d: what is drawn has %s", what, w, h, found)
		}
		lines := strings.Split(plain(drawn), "\n")
		if len(lines) > max(h, 1) {
			t.Fatalf("%s at %d×%d: %d lines are drawn", what, w, h, len(lines))
		}
		// The widths are the renderer's, by graphemes and by characters:
		// what a terminal would count, not what this package counts.
		for i, line := range lines {
			if g, wc := ansi.StringWidth(line), ansi.StringWidthWc(line); g > w || wc > w {
				t.Fatalf("%s at %d×%d: line %d is %d cells wide by graphemes and %d by characters: %q", what, w, h, i, g, wc, line)
			}
		}
		if w < leastWidth || h < leastHeight {
			if len(lines) != 1 || (w >= 20 && !strings.Contains(lines[0], "Too small")) {
				t.Fatalf("%s at %d×%d: a terminal that is too small has %q", what, w, h, lines)
			}
		} else if len(lines) != h || !strings.Contains(lines[0], "great-machine") || !strings.Contains(lines[h-1], "quit") {
			t.Fatalf("%s at %d×%d: %d lines, from %q to %q; want the header first and the keys last", what, w, h, len(lines), lines[0], lines[len(lines)-1])
		}
	}
	// Every view and every odd state, in the terminals the contract
	// names and at the edges, with the keys that move.
	sizes := [][2]int{{80, 24}, {200, 60}, {20, 5}, {0, 0}, {1, 1}, {leastWidth, leastHeight}, {leastWidth - 1, 60}, {200, leastHeight - 1}, {59, 13}, {60, 15}, {109, 30}, {110, 30}, {1000, 300}}
	for _, sc := range scenes() {
		for _, size := range sizes {
			check(sc.name, sc.model(80, 24), size[0], size[1], "down", "pgdown", "up", "G", "g", "pgup")
		}
	}
	// Every size from 1×1 to 100×40, in a terminal that is resized from
	// one to the next: each of the three views, and the states that
	// draw the most.
	swept := []string{"services", "services-none", "services-lost", "service-rollout-under-way", "service-halted", "service-no-rollout",
		"zathras", "zathras-long-name", "zathras-gone", "zathras-help"}
	for _, sc := range scenes() {
		if !slices.Contains(swept, sc.name) {
			continue
		}
		m := sc.model(80, 24)
		for w := 1; w <= 100; w++ {
			for h := 1; h <= 40; h++ {
				check(sc.name, m, w, h)
			}
		}
	}
}
