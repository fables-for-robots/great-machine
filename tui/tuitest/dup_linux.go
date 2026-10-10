package tuitest

import "syscall"

// dup makes the descriptor to a copy of from, closing what to was.
func dup(from, to int) error {
	return syscall.Dup3(from, to, 0)
}
