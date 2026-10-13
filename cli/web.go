package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// The times of the server of the web page.
const (
	// webReadHeaderWait is how long a browser has to send the header of a
	// request.
	webReadHeaderWait = 10 * time.Second
	// webIdleWait is how long a connection may stay open with no request
	// on it.
	webIdleWait = 2 * time.Minute
	// webStopWait is how long the requests that are being answered have
	// to end when the server is stopped, and then once more the handlers
	// of the WebSockets.
	webStopWait = 5 * time.Second
)

// webServer is the HTTP server of the web page, which `great-machine web`
// runs on the machine of an operator.
type webServer struct {
	server *http.Server
	end    context.CancelFunc // ends the context of every request
	failed chan error         // what Serve returned, when it was not told to end
	wait   time.Duration
	log    *slog.Logger

	mu       sync.Mutex
	stopping bool           // no request is begun any more
	handlers sync.WaitGroup // the requests being answered, WebSockets among them
	stop     sync.Once
}

// startWeb serves handler on listener until Stop. wait is how long Stop
// gives what is being answered to end.
func startWeb(listener net.Listener, handler http.Handler, wait time.Duration, log *slog.Logger) *webServer {
	requests, end := context.WithCancel(context.Background())
	w := &webServer{end: end, failed: make(chan error, 1), wait: wait, log: log}
	w.server = &http.Server{
		Handler:           w.counted(handler),
		ReadHeaderTimeout: webReadHeaderWait,
		IdleTimeout:       webIdleWait,
		// No WriteTimeout and no ReadTimeout: either would cut a
		// WebSocket, which lives as long as the page is open. The handler
		// bounds every write to a browser by itself. IdleTimeout does not
		// touch a WebSocket: the server has let go of its connection.
		BaseContext: func(net.Listener) context.Context { return requests },
		ErrorLog:    slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	go func() {
		if err := w.server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			w.failed <- fmt.Errorf("the web page: %w", err)
		}
	}()
	return w
}

// counted is handler, with every request it answers counted in
// w.handlers, so that Stop can wait for them. The server itself loses
// sight of a connection that became a WebSocket.
func (w *webServer) counted(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		if w.stopping {
			// Stop is waiting for the handlers, and one more must not be
			// added to those it waits for. No request begins this late,
			// for the listener is closed and the server begins none on
			// a connection it keeps, but for one: on a connection that
			// was accepted just before the listener closed, whose
			// request comes when Shutdown has returned and Close has
			// not ended the connection yet. It is not handed to the
			// handler and not counted. It is told why in one line, and
			// that its connection ends; Close ends it.
			w.mu.Unlock()
			rw.Header().Set("Connection", "close")
			http.Error(rw, "great-machine: the web page is ending", http.StatusServiceUnavailable)
			return
		}
		w.handlers.Add(1)
		w.mu.Unlock()
		defer w.handlers.Done()
		handler.ServeHTTP(rw, r)
	})
}

// Failed receives an error if the server ends by itself.
func (w *webServer) Failed() <-chan error { return w.failed }

// Stop ends the server: the listener is closed, the requests that are
// being answered get w.wait to end, and then the WebSockets are ended and
// waited for as long again. It may be called more than once.
func (w *webServer) Stop() {
	w.stop.Do(func() {
		ctx, done := context.WithTimeout(context.Background(), w.wait)
		defer done()
		// Shutdown closes the listener and waits for the requests that
		// are no WebSockets. Those it neither waits for nor closes, and
		// Close does not close them either: a connection that a handler
		// took over is the handler's. What ends them is the context of
		// their requests.
		w.server.Shutdown(ctx)
		w.mu.Lock()
		w.stopping = true
		w.mu.Unlock()
		w.end()
		w.server.Close()

		ended := make(chan struct{})
		go func() {
			w.handlers.Wait()
			close(ended)
		}()
		select {
		case <-ended:
		case <-time.After(w.wait):
			w.log.Warn("the web page was stopped while requests were still being answered")
		}
	})
}
