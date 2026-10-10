package e2e

import (
	"bytes"
	"context"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// shellGoroutines are the functions a goroutine of a shell session is in,
// in draal and in a zathras, as a stack names them.
var shellGoroutines = []string{
	"draal.(*Draal).serveShell",
	"draal.(*relayShell).",
	"zathras.(*Agent).answerShell",
}

// shellStacks returns the stacks of all goroutines if one of them is of a
// shell session, and nil otherwise.
func shellStacks() []byte {
	buf := make([]byte, 4<<20)
	buf = buf[:runtime.Stack(buf, true)]
	for _, name := range shellGoroutines {
		if bytes.Contains(buf, []byte(name)) {
			return buf
		}
	}
	return nil
}

// An operator's shell runs a program that reads no input, and the
// operator sends far more input than the terminal of the shell and the
// two streams on the way hold. Then the operator goes. Within the
// deadline the program and the shell are gone, and neither draal nor the
// zathras has a goroutine of the session left.
//
// Before a stream that is closed ended a Write that waits, all of it
// stayed until the connection of the zathras ended: the operator's Write,
// draal's two goroutines, the two of the zathras, and the program.
//
// This test is not parallel: it looks at the goroutines of the whole
// process, where the other tests of the package have shells of their own.
// Those wait until the tests that are not parallel have ended.
func TestShellEndsWithTheOperatorWhileInputWaits(t *testing.T) {
	h := start(t, options{zathrases: 1})
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	sh, err := h.op.Shell(ctx, h.pods[0].id, wire.ShellRequest{Cols: 80, Rows: 24, Term: "dumb"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer sh.Close()

	// What the shell prints is read to the end of the session, as a
	// terminal does it.
	var (
		mu     sync.Mutex
		output []byte
	)
	go func() {
		buf := make([]byte, 16<<10)
		for {
			n, err := sh.Read(buf)
			mu.Lock()
			// Only the beginning is looked at: the rest is the echo of
			// the flood.
			if len(output) < 64<<10 {
				output = append(output, buf[:n]...)
			}
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	// printed waits for a number that the shell prints behind name. The
	// terminal echoes what is typed, and that has "$$" where the output
	// has the number.
	printed := func(name string) int {
		t.Helper()
		re := regexp.MustCompile(name + `=(\d+)\.`)
		var number int
		eventually(t, "the shell to print "+name, func() bool {
			mu.Lock()
			defer mu.Unlock()
			m := re.FindSubmatch(output)
			if m == nil {
				return false
			}
			number, _ = strconv.Atoi(string(m[1]))
			return true
		})
		return number
	}

	// The shell, and in it a program that says its process ID and reads
	// nothing from then on.
	if _, err := sh.Write([]byte("echo shell=$$.\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	shell := printed("shell")
	if _, err := sh.Write([]byte("sh -c 'echo program=$$.; exec sleep 1000'\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	program := printed("program")
	if !alive(shell) || !alive(program) {
		t.Fatalf("the shell %d or its program %d does not run", shell, program)
	}

	// 16 MiB of input, in lines. A terminal takes a few kilobytes of
	// whole lines that nobody reads, a stream of go-iroh 512 KiB and some
	// 64 KiB that it buffers before that, and there are two streams on
	// the way: all but some 1.2 MiB can never be written.
	const size = 16 << 20
	chunk := bytes.Repeat([]byte("0123456789abcde\n"), 1<<10)
	var written atomic.Int64
	flooded := make(chan error, 1)
	go func() {
		for written.Load() < size {
			if _, err := sh.Write(chunk); err != nil {
				flooded <- err
				return
			}
			written.Add(int64(len(chunk)))
		}
		flooded <- nil
	}()
	// The Write waits: nothing more was written for a second, though not
	// all is written.
	last, since := int64(-1), time.Now()
	eventually(t, "the input to come to a halt", func() bool {
		select {
		case err := <-flooded:
			t.Fatalf("the flood ended with %v after %d bytes; want it to wait for a program that reads nothing", err, written.Load())
		default:
		}
		if now := written.Load(); now != last {
			last, since = now, time.Now()
		}
		return last > 0 && time.Since(since) > time.Second
	})
	t.Logf("the input came to a halt after %d bytes", last)
	if !alive(program) {
		t.Fatalf("the program %d has ended before the operator went", program)
	}

	// The operator goes.
	closed := time.Now()
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-flooded:
		if err == nil {
			t.Error("the Write that waited ended without an error")
		}
	case <-time.After(deadline):
		t.Fatalf("the operator's Write still waits %v after Close", deadline)
	}
	eventually(t, "the program and the shell to end", func() bool { return !alive(program) && !alive(shell) })
	end := time.Now().Add(deadline)
	for stacks := shellStacks(); stacks != nil; stacks = shellStacks() {
		if time.Now().After(end) {
			t.Fatalf("goroutines of the session are left %v after the operator went:\n%s", deadline, stacks)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("all of the session was gone %v after the operator", time.Since(closed).Round(time.Millisecond))
}
