// Package authz is the one place that decides whether a peer may do what it
// asks for: every request passes Authorize before anything is done.
package authz

import (
	"github.com/fables-for-robots/great-machine/wire"
)

// Operation is what a peer asks to do.
type Operation string

// The operations. OpRegister is what a zathras does, and the others are
// asked by a peer that was granted a role.
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
	// OpAccess is the grants: listing them, granting and revoking.
	OpAccess Operation = "access"
	// OpMe is a peer asking who it is, which every peer may.
	OpMe Operation = "me"

	// The operations of the store, which draal answers on jaccard-store's
	// protocol. OpStorePull is a pull of a reference, which a zathras does
	// for the service it registered for.
	OpStorePull Operation = "store-pull"
	// OpStorePush is the first two steps of a push, which name the
	// reference: it is of the service the name is under.
	OpStorePush Operation = "store-push"
	// OpStoreCommit is the last step of a push, which names an upload and
	// no reference. The store answers it for the endpoint that began the
	// upload, and whoever deploys anything may ask.
	OpStoreCommit Operation = "store-commit"
	OpStoreList   Operation = "store-list"
	OpStoreDelete Operation = "store-delete"
	// OpStorePage is the page of the store, which draal answers on its
	// own protocol.
	OpStorePage Operation = "store-page"
)

// Authorizer decides whether peer may do op on service. service is ""
// for an operation that is of no one service. A refusal is a *wire.Error
// with CodeUnauthorized. *Authority is the one there is.
type Authorizer interface {
	Authorize(peer wire.ID, op Operation, service string) error
}
