// Package draal is the controller: it accepts the connections of the
// zathrases and of the operators, keeps the fleet, carries out what
// rollout.Decide says, and answers the operator's requests.
//
// What an operator can do is the interface Controller, which *Draal
// implements. The handler of the protocol is written against those methods
// alone, and so is the HTTP API of stage 2.
package draal
