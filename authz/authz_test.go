package authz

import (
	"errors"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	alice   = wire.ID("a11ce00000000000000000000000000000000000000000000000000000000000")
	bob     = wire.ID("b0b0000000000000000000000000000000000000000000000000000000000000")
	mallory = wire.ID("3a110e0000000000000000000000000000000000000000000000000000000000")
)

// operations is every operation there is, with the service it is asked on.
var operations = []struct {
	op      Operation
	service string
}{
	{OpRegister, "web"},
	{OpStore, ""},
	{OpRollout, "web"},
	{OpRollback, "web"},
	{OpWatch, ""},
	{OpRollouts, "web"},
	{OpHistory, "web"},
	{OpLogs, "web"},
	{OpShell, "web"},
}

func TestAllowlist(t *testing.T) {
	tests := []struct {
		name      string
		operators []wire.ID
		peer      wire.ID
		operator  bool
	}{
		{"an operator", []wire.ID{alice, bob}, alice, true},
		{"the other operator", []wire.ID{alice, bob}, bob, true},
		{"a peer that is none", []wire.ID{alice, bob}, mallory, false},
		{"an empty allowlist", []wire.ID{}, alice, false},
		{"no allowlist", nil, alice, false},
		{"a peer without an ID", []wire.ID{alice}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var a Authorizer = NewAllowlist(tt.operators)
			for _, o := range operations {
				err := a.Authorize(tt.peer, o.op, o.service)
				allowed := tt.operator || o.op == OpRegister
				if allowed {
					if err != nil {
						t.Errorf("%s: refused: %v", o.op, err)
					}
					continue
				}
				if err == nil {
					t.Errorf("%s: allowed", o.op)
					continue
				}
				var refusal *wire.Error
				if !errors.As(err, &refusal) {
					t.Errorf("%s: the refusal is a %T, want a *wire.Error", o.op, err)
					continue
				}
				if refusal.Code != wire.CodeUnauthorized {
					t.Errorf("%s: code %q, want %q", o.op, refusal.Code, wire.CodeUnauthorized)
				}
				if !strings.Contains(refusal.Message, string(o.op)) {
					t.Errorf("%s: the message does not name the operation: %q", o.op, refusal.Message)
				}
				if tt.peer != "" && !strings.Contains(refusal.Message, string(tt.peer)) {
					t.Errorf("%s: the message does not name the peer: %q", o.op, refusal.Message)
				}
				if o.service != "" && !strings.Contains(refusal.Message, o.service) {
					t.Errorf("%s: the message does not name the service: %q", o.op, refusal.Message)
				}
			}
		})
	}
}

func TestRegisterIsAllowedOnEveryService(t *testing.T) {
	a := NewAllowlist(nil)
	for _, service := range []string{"web", "api", ""} {
		if err := a.Authorize(mallory, OpRegister, service); err != nil {
			t.Errorf("register on %q: %v", service, err)
		}
	}
}

func TestTheZeroAllowlistRefuses(t *testing.T) {
	var a Allowlist
	if err := a.Authorize(alice, OpShell, "web"); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("the zero Allowlist: %v, want unauthorized", err)
	}
	if err := a.Authorize(alice, OpRegister, "web"); err != nil {
		t.Errorf("the zero Allowlist refused register: %v", err)
	}
}

func TestTheListIsCopied(t *testing.T) {
	operators := []wire.ID{alice}
	a := NewAllowlist(operators)
	operators[0] = mallory
	if err := a.Authorize(mallory, OpShell, "web"); err == nil {
		t.Error("a change of the slice after NewAllowlist let mallory in")
	}
	if err := a.Authorize(alice, OpShell, "web"); err != nil {
		t.Errorf("a change of the slice after NewAllowlist locked alice out: %v", err)
	}
}

// An empty ID in the list makes nobody an operator, not even a peer with
// no ID.
func TestAnEmptyIDIsSkipped(t *testing.T) {
	a := NewAllowlist([]wire.ID{"", alice})
	if err := a.Authorize("", OpShell, "web"); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("the peer with no ID: %v, want unauthorized", err)
	}
	if err := a.Authorize(alice, OpShell, "web"); err != nil {
		t.Errorf("alice beside the empty ID: %v", err)
	}
}

// IDs are compared exactly: the same ID in upper case is another peer.
func TestIDsAreComparedExactly(t *testing.T) {
	a := NewAllowlist([]wire.ID{alice})
	if err := a.Authorize(wire.ID(strings.ToUpper(string(alice))), OpShell, "web"); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("alice in upper case: %v, want unauthorized", err)
	}
}
