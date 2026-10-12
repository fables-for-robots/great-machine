// Package authz is the one place that decides whether a peer may do what it
// asks for: every request passes Authorize before anything is done.
package authz

import (
	"github.com/fables-for-robots/great-machine/wire"
)

// Operation is what a peer asks to do.
type Operation string

// The operations. OpRegister is what a zathras does, and OpStorePull what
// the mount of a zathras does, with a key of its own that draal does not
// know; the others are an operator's.
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

	// The two operations of the store, which draal answers on
	// jaccard-store's protocol. OpStorePull is a pull of a reference.
	// OpStoreWrite is everything else of that protocol: the three steps
	// of a push, a list and a delete.
	OpStorePull  Operation = "store-pull"
	OpStoreWrite Operation = "store-write"
)

// Authorizer decides whether peer may do op on service. service is ""
// for watch and for the two operations of the store. A refusal is a
// *wire.Error with CodeUnauthorized.
type Authorizer interface {
	Authorize(peer wire.ID, op Operation, service string) error
}

// Allowlist lets every peer register and pull from the store, and its
// operators do everything else.
type Allowlist struct {
	operators map[wire.ID]struct{}
}

// NewAllowlist returns the allowlist of the operators given. With none,
// nobody can act as an operator. An empty ID is skipped: it would make an
// operator of a peer with no ID. IDs are compared exactly as they are.
func NewAllowlist(operators []wire.ID) Allowlist {
	set := make(map[wire.ID]struct{}, len(operators))
	for _, id := range operators {
		if id == "" {
			continue
		}
		set[id] = struct{}{}
	}
	return Allowlist{operators: set}
}

// Authorize returns nil when peer may do op on service: for OpRegister
// and OpStorePull always, and for every other operation when peer is an
// operator. The refusal names the operation and the peer.
func (a Allowlist) Authorize(peer wire.ID, op Operation, service string) error {
	if op == OpRegister || op == OpStorePull {
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
