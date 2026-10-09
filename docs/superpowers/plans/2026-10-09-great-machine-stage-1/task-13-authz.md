### Task 13: authz, the one place that decides

**Files:**
- Create: `authz/authz.go`
- Test: `authz/authz_test.go`

**Interfaces:**
- Consumes, from `wire` (Task 2):

  ```go
  type ID string

  type Code string

  const CodeUnauthorized Code = "unauthorized"

  // Error is a refusal, as it travels.
  type Error struct {
  	Code    Code   `json:"code"`
  	Message string `json:"message"`
  }

  func (e *Error) Error() string // "<code>: <message>"
  func Errorf(code Code, format string, args ...any) *Error

  // CodeOf returns the code of the *Error in err's chain, CodeInternal for
  // any other error and "" for nil.
  func CodeOf(err error) Code
  ```

- Produces, package `authz`:

  ```go
  type Operation string

  const (
  	OpRegister Operation = "register"
  	OpStore    Operation = "store"
  	OpRollout  Operation = "rollout"
  	OpRollback Operation = "rollback"
  	OpWatch    Operation = "watch"
  	OpRollouts Operation = "rollouts"
  	OpHistory  Operation = "history"
  	OpLogs     Operation = "logs"
  	OpShell    Operation = "shell"
  )

  // Authorizer decides whether peer may do op on service. service is ""
  // for store and watch. A refusal is a *wire.Error with CodeUnauthorized.
  type Authorizer interface {
  	Authorize(peer wire.ID, op Operation, service string) error
  }

  // Allowlist lets every peer register and its operators do everything else.
  type Allowlist struct{ /* unexported */ }

  func NewAllowlist(operators []wire.ID) Allowlist
  func (a Allowlist) Authorize(peer wire.ID, op Operation, service string) error
  ```

  The message of a refusal names the peer, the operation and, when there
  is one, the service: `<peer> is no operator and may not shell on service web`.
  The zero `Allowlist` has no operators: it lets every peer register and
  refuses everything else. `Authorize` does not log; the caller logs the
  refusal with the peer and the operation, as section 4 of the spec asks.

- [ ] **Step 1: Write the failing test**

`authz/authz_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./authz/`
Expected: FAIL — the package does not build: `undefined: Operation`, `undefined: OpRegister`, `undefined: Authorizer`, `undefined: NewAllowlist`

- [ ] **Step 3: Write the implementation**

`authz/authz.go`:

```go
// Package authz is the one place that decides whether a peer may do what it
// asks for: every request passes Authorize before anything is done.
package authz

import (
	"github.com/fables-for-robots/great-machine/wire"
)

// Operation is what a peer asks to do.
type Operation string

// The operations. OpRegister is what a zathras does; the others are an
// operator's.
const (
	OpRegister Operation = "register"
	OpStore    Operation = "store"
	OpRollout  Operation = "rollout"
	OpRollback Operation = "rollback"
	OpWatch    Operation = "watch"
	OpRollouts Operation = "rollouts"
	OpHistory  Operation = "history"
	OpLogs     Operation = "logs"
	OpShell    Operation = "shell"
)

// Authorizer decides whether peer may do op on service. service is ""
// for store and watch. A refusal is a *wire.Error with CodeUnauthorized.
type Authorizer interface {
	Authorize(peer wire.ID, op Operation, service string) error
}

// Allowlist lets every peer register and its operators do everything else.
type Allowlist struct {
	operators map[wire.ID]struct{}
}

// NewAllowlist returns the allowlist of the operators given. With none,
// nobody can act as an operator.
func NewAllowlist(operators []wire.ID) Allowlist {
	set := make(map[wire.ID]struct{}, len(operators))
	for _, id := range operators {
		set[id] = struct{}{}
	}
	return Allowlist{operators: set}
}

// Authorize returns nil when peer may do op on service: for OpRegister
// always, and for every other operation when peer is an operator. The
// refusal names the operation and the peer.
func (a Allowlist) Authorize(peer wire.ID, op Operation, service string) error {
	if op == OpRegister {
		return nil
	}
	if _, ok := a.operators[peer]; ok {
		return nil
	}
	if service == "" {
		return wire.Errorf(wire.CodeUnauthorized, "%s is no operator and may not %s", peer, op)
	}
	return wire.Errorf(wire.CodeUnauthorized, "%s is no operator and may not %s on service %s", peer, op, service)
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test -race ./authz/ -v`
Expected: PASS — `TestAllowlist` with its six subtests, `TestRegisterIsAllowedOnEveryService`, `TestTheZeroAllowlistRefuses`, `TestTheListIsCopied`

Run: `test -z "$(gofmt -l .)" && go vet ./...`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add authz/
git commit -m "Authz: Authorize and the allowlist of operators"
```
