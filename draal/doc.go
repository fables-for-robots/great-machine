// Package draal is the controller: it accepts the connections of the
// zathrases and of the operators, keeps the fleet, carries out what
// rollout.Decide says, and answers the operator's requests.
//
// What an operator can do is the interface Controller, which *Draal
// implements. The handler of the protocol is written against those methods
// alone. Who may ask for which of them is decided by the grants (access.go),
// which the handler asks before it does anything.
package draal
