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
