// Package e2e holds the tests of the whole: a draal, several zathrases
// with real supervisors and real child processes, and the operator's
// client, in one process over go-iroh on the loopback interface. Only the
// store of a zathras and the Nix store are stand-ins: the first fetches
// nothing and notes what it was asked to fetch, the second is a temporary
// directory that draal is pointed at with Config.StoreDir.
//
// A test that asks for it has draal's store as well, over a bucket in
// memory and under draal's own endpoint ID (store_test.go). And one test
// has no stand-in at all: the store of its zathras is that directory,
// which it fills from draal's store, and its service is started from
// what was written out, by a launcher (storedir_test.go).
//
// The package has no code but its tests.
package e2e
