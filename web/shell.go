package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// maxTerminal is the most columns or rows a terminal may have.
	maxTerminal = 1000
	// maxTerm is the most bytes the name of a terminal may have.
	maxTerm = 64
	// shellChunk is the most output of a shell one message carries.
	shellChunk = 32 << 10
)

// shellMessage is a text message of the page on the socket of a shell.
type shellMessage struct {
	Resize *struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"resize"`
}

// shellExit is the text message that says how the shell ended.
type shellExit struct {
	Exit int `json:"exit"`
}

// shellInput is what the page has sent for a shell and the shell has not
// been given yet: what was typed, at most readLimit bytes of it, and the
// last size of the terminal.
//
// The reader of the socket puts it in. A goroutine of its own, pump,
// hands it to the shell, whose Write may wait for as long as the program
// in the shell reads nothing, and for good. Before the shell runs nobody
// takes anything out.
//
// When there is no room for what was typed, the reader waits for room,
// which pump makes as it takes, and reads nothing meanwhile: what the
// page sends backs up in the browser, and a paste goes to the shell at
// the pace the shell takes it. That wait, unlike one in Write, ends when
// the handler says so (end), and by itself after WriteWait: a shell that
// took nothing for that long does not take its input, and the session
// ends. Nothing is ever dropped from the middle of what was typed.
type shellInput struct {
	h *handler

	more chan struct{} // has room for one value, and gets one when something was put in
	room chan struct{} // has room for one value, and gets one when something was taken out
	done chan struct{} // closed by end: the handler returns
	once sync.Once

	// full is done when what was typed found no room for WriteWait.
	full     context.Context
	overflow context.CancelFunc

	mu    sync.Mutex
	typed []byte
	size  *wire.WinSize
}

func newShellInput(h *handler) *shellInput {
	in := &shellInput{h: h, more: make(chan struct{}, 1), room: make(chan struct{}, 1), done: make(chan struct{})}
	in.full, in.overflow = context.WithCancel(context.Background())
	return in
}

// end ends a wait for room: the session is over.
func (in *shellInput) end() {
	in.once.Do(func() { close(in.done) })
}

// message takes one message of the page. A binary one is what was typed.
// A text one is a change of size; one that is none is dropped, as the
// page gets no answer on this socket but output.
func (in *shellInput) message(typ websocket.MessageType, data []byte) {
	if typ == websocket.MessageBinary {
		if !in.put(data) {
			in.overflow()
		}
		return
	}
	var m shellMessage
	if err := json.Unmarshal(data, &m); err != nil || m.Resize == nil ||
		m.Resize.Cols < 1 || m.Resize.Cols > maxTerminal || m.Resize.Rows < 1 || m.Resize.Rows > maxTerminal {
		in.h.log.Debug("a message of a page that is no change of size was dropped", "message", string(data[:min(len(data), 200)]))
		return
	}
	in.mu.Lock()
	in.size = &wire.WinSize{Cols: uint16(m.Resize.Cols), Rows: uint16(m.Resize.Rows)}
	in.mu.Unlock()
	in.tell(in.more)
}

// tell puts a value into a channel that has room for one, if it is not
// there already.
func (in *shellInput) tell(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// put puts in what was typed, behind what waits. With no room for it, it
// waits for room, for at most WriteWait, and returns false when there
// was none in that time. A message is no longer than readLimit, so there
// is room for it at the latest when nothing waits. Once it has returned
// false it takes nothing more.
func (in *shellInput) put(data []byte) bool {
	if in.full.Err() != nil {
		// A message was given up before this one. What was typed after
		// a hole must not reach the shell.
		return false
	}
	var late <-chan time.Time
	for {
		in.mu.Lock()
		if len(in.typed)+len(data) <= readLimit {
			in.typed = append(in.typed, data...)
			in.mu.Unlock()
			in.tell(in.more)
			return true
		}
		in.mu.Unlock()
		if late == nil {
			timer := time.NewTimer(in.h.cfg.WriteWait)
			defer timer.Stop()
			late = timer.C
			in.h.inputWaits.Add(1)
			defer in.h.inputWaits.Add(-1)
		}
		select {
		case <-in.room:
		case <-late:
			return false
		case <-in.done:
			return true // nobody is there to take it
		}
	}
}

// take takes out what waits, and so makes room.
func (in *shellInput) take() ([]byte, *wire.WinSize) {
	in.mu.Lock()
	typed, size := in.typed, in.size
	in.typed, in.size = nil, nil
	in.mu.Unlock()
	in.tell(in.room)
	return typed, size
}

// pump hands what the page sent to the shell, in its order, until stop
// is closed. An error ends nothing here: a session that has failed says
// so where its output is read.
//
// Nobody waits for pump. A Write that waits is ended by the Close of the
// shell, if the Shell keeps to that; if it does not, pump stays with it.
func (in *shellInput) pump(sh Shell, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-in.more:
		}
		typed, size := in.take()
		if size != nil {
			if err := sh.Resize(size.Cols, size.Rows); err != nil {
				in.h.log.Debug("resizing a shell", "error", err)
			}
		}
		if len(typed) > 0 {
			if _, err := sh.Write(typed); err != nil {
				in.h.log.Debug("writing to a shell", "error", err)
			}
		}
	}
}

// shellOutput is a piece of what a shell printed, or how its output
// ended.
type shellOutput struct {
	data []byte
	err  error
}

// readShell reads what the shell prints and hands it on piece by piece,
// each when the one before was taken and written (next), until the
// output ends or stop is closed. Nobody waits for it either: a Read
// that waits is the Shell's to end at its Close.
func readShell(sh Shell, pieces chan<- shellOutput, next, stop <-chan struct{}) {
	buf := make([]byte, shellChunk)
	for {
		n, err := sh.Read(buf)
		select {
		case pieces <- shellOutput{data: buf[:n], err: err}:
		case <-stop:
			return
		}
		if err != nil {
			return
		}
		select {
		case <-next:
		case <-stop:
			return
		}
	}
}

// shellRequest reads the size and the name of the terminal from the
// query: 80 columns, 24 rows and xterm-256color where it says nothing.
func shellRequest(r *http.Request, id wire.ID) (wire.ShellRequest, error) {
	req := wire.ShellRequest{Zathras: id, Cols: 80, Rows: 24, Term: "xterm-256color"}
	query := r.URL.Query()
	for name, to := range map[string]*uint16{"cols": &req.Cols, "rows": &req.Rows} {
		v := query.Get(name)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxTerminal {
			return req, wire.Errorf(wire.CodeBadRequest, "%s is %q, and can be 1 to %d", name, v, maxTerminal)
		}
		*to = uint16(n)
	}
	if term := query.Get("term"); term != "" {
		valid := len(term) <= maxTerm
		for i := 0; valid && i < len(term); i++ {
			c := term[i]
			valid = c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '+'
		}
		if !valid {
			return req, wire.Errorf(wire.CodeBadRequest,
				"term is %q, and can be up to %d of a to z, A to Z, 0 to 9, '-', '_', '.' and '+'", term, maxTerm)
		}
		req.Term = term
	}
	return req, nil
}

// shell runs a shell in the container of a zathras on a socket: binary
// messages are what is typed and what is printed.
//
// It returns when the session has ended, however that came about, and
// waits for nothing the Shell does but its Close: not for a Write, which
// may wait for a program that reads nothing, and not for a Read. What
// it does wait for, the reader of the socket, waits for room for input
// at most, and that wait it ends.
func (h *handler) shell(w http.ResponseWriter, r *http.Request) {
	id, err := zathrasID(r)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	req, err := shellRequest(r, id)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	in := newShellInput(h)
	defer in.overflow()
	s := h.open(w, r, in.message)
	if s == nil {
		return
	}
	defer s.done()
	// Before done waits for the reader of the socket, a wait of the
	// reader for room is ended.
	defer in.end()
	// tooMuch ends the session of a page whose input found no room for
	// WriteWait: the shell does not take its input, and nothing of it is
	// dropped to go on.
	tooMuch := func() {
		s.shut(websocket.StatusMessageTooBig, "the shell does not take its input")
	}

	// The start ends with the socket, and when what was typed found no
	// room for a shell that is not there yet.
	starting, started := context.WithCancel(s.ctx)
	stopFull := context.AfterFunc(in.full, started)
	sh, err := h.cfg.Client.Shell(starting, id, req)
	stopFull()
	started()
	if err != nil {
		if in.full.Err() != nil && s.ctx.Err() == nil {
			tooMuch()
			return
		}
		s.fail(err)
		return
	}
	// The session ends, and the shell with it, when this returns and when
	// the socket ends, whichever is first: the page closing the socket
	// ends the shell. Close is called once.
	var closing sync.Once
	closeShell := func() { closing.Do(func() { sh.Close() }) }
	defer closeShell()
	defer context.AfterFunc(s.ctx, closeShell)()

	stop := make(chan struct{})
	defer close(stop)
	go in.pump(sh, stop)

	// Output is read from the session only when the output before it has
	// been written to the page. So a page that reads slowly slows the
	// shell down, through draal and the zathras to the program that
	// prints, as a slow terminal does, and nothing between them grows
	// with what a program prints. This goroutine is also the one that
	// writes the exit status, so the page gets it behind all the output.
	pieces, next := make(chan shellOutput), make(chan struct{}, 1)
	go readShell(sh, pieces, next, stop)
	for {
		var piece shellOutput
		select {
		case piece = <-pieces:
		case <-in.full.Done():
			if s.ctx.Err() == nil {
				tooMuch()
			}
			return
		case <-s.ctx.Done():
			return // the page closed the socket, or the server ends
		}
		if len(piece.data) > 0 && s.write(websocket.MessageBinary, piece.data) != nil {
			return
		}
		if errors.Is(piece.err, io.EOF) {
			break
		}
		if piece.err != nil {
			// Review Focus 2: a session whose zathras goes ends with the
			// reason.
			s.fail(piece.err)
			return
		}
		next <- struct{}{}
	}
	if s.ctx.Err() != nil {
		return // the page closed the socket, which ended the shell
	}
	status, err := sh.Wait(s.ctx)
	if err != nil {
		s.fail(err)
		return
	}
	if s.writeJSON(shellExit{Exit: status}) == nil {
		s.end()
	}
}
