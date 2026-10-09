### Task 3: node and node/nodetest — keys, connections over go-iroh, and a network in memory

**Files:**
- Create: `node/key.go` (`Key`, `NewKey`, `LoadOrCreateKey`, `ParseID`)
- Create: `node/node.go` (the package comment; `Stream`, `Conn`, `Listener`)
- Create: `node/nodetest/nodetest.go` (`Net`, `New`, `ID`)
- Create: `node/conn.go` (`Stream` and `Conn` over go-iroh)
- Create: `node/listen.go` (`ListenConfig`, `Listen`)
- Create: `node/dial.go` (`DialConfig`, `Dial`)
- Test: `node/key_test.go`, `node/conn_test.go`, `node/iroh_test.go`, `node/vanish_test.go`, `node/nodetest/nodetest_test.go`
- Modify: `go.mod`, `go.sum` (`github.com/tmc/go-iroh v0.3.0`, `github.com/amber-store/jaccard-store v0.7.0`, if they are not there yet)

**Interfaces:**
- Consumes: `wire.ID` (`type ID string`) and `wire.ALPN` (`const ALPN = "great-machine/1"`) of Task 2. Of jaccard-store v0.7.0, package `github.com/amber-store/jaccard-store/node`: `LoadOrCreateKey(path string) (key.SecretKey, error)` and `Bind(ctx context.Context, cfg ServerConfig) (*iroh.Endpoint, error)` with `ServerConfig{Key, ALPN, Bind, Local, Log}`. Of go-iroh v0.3.0: packages `iroh`, `iroh/mdns`, `key`, `netaddr`, `relay`.
- Produces: the `node` and `nodetest` sections of the contract, exactly: `node.Key`, `NewKey`, `LoadOrCreateKey`, `Key.ID`, `Key.Iroh` (go-iroh's `key.SecretKey`, by value), `ParseID`, `Stream`, `Conn`, `Listener`, `ListenConfig`, `Listen`, `DialConfig`, `Dial`; `nodetest.Net`, `nodetest.New`, `(*Net).Listen`, `(*Net).Dial`, `nodetest.ID`.

This task settles the first item of section 15 of the spec. What was found, with go-iroh v0.3.0 on loopback, and what every task that speaks the protocol may rely on. It holds for go-iroh and for `nodetest` alike; `node/conn_test.go` runs the same checks over both:

- **Both sides open streams and accept them** on one connection, several at once.
- **A stream is not seen by the peer's `AcceptStream` until its opener has written to it** (or ended it with `CloseWrite` or `Close`). Whoever opens a stream writes first. Every protocol of great-machine begins with a `Request` of the opener, so this holds; nobody may rely on the opposite.
- `CloseWrite`: the peer reads what was written and then `io.EOF`; the other direction is as it was. A `Write` of this side after it fails.
- `Close` of a stream: what this side wrote before is still delivered and the peer then reads `io.EOF` (**not** an error); a `Write` of the peer fails, also one that waits; a `Read` of this side fails with an error that is not `io.EOF`. It may follow `CloseWrite` and be called twice, and returns nil.
- `Close` of a connection, by either side: `Done` is closed on both sides at once; every `AcceptStream`, `Read` and `Write` that waits fails, with an error that is not `io.EOF`; **what was written and has not arrived is lost**. A side that wants its last message read waits for the peer to end the stream.
- A `Write` returns before the peer reads while less than 512 KiB wait to be read; beyond that it waits for the peer. A frame may be up to 4 MiB: who writes a large one needs a peer that reads meanwhile.
- `Listener.Close` ends `Accept` and every connection that was accepted. A connection that was dialed has to be closed with `Close` even when `Done` is closed already: that frees its endpoint.
- **A peer that vanishes is noticed** by `Done` 6 to 8 seconds later on the side that dialed and 7 to 9 on the side that listens (measured with `kill -9`, twelve times each: 6.0–7.9 s and 7.1–8.8 s). `Dial` asks for an idle timeout of 6 s and a keep-alive of 2 s; QUIC makes the shorter idle timeout of the two sides hold for both, so the listening side, which jaccard-store binds with go-iroh's 30 s, follows.

And about the design:

- `Listen` **calls** jaccard-store's `node.Bind`, which binds and announces (relay, pkarr, mDNS). `LoadOrCreateKey` calls jaccard-store's too: the key file is the same.
- `Dial` is **this package's own**, modelled on jaccard-store's `node.Dial`: that one returns a type without `AcceptStream`, and takes no idle timeout.
- `Listen`'s context bounds `Listen` alone, like `Dial`'s.

- [ ] **Step 1: Add go-iroh and jaccard-store**

Skip what `go.mod` already requires.

Run: `go get github.com/tmc/go-iroh@v0.3.0 github.com/amber-store/jaccard-store@v0.7.0`
Expected: `go: added github.com/tmc/go-iroh v0.3.0` and `go: added github.com/amber-store/jaccard-store v0.7.0`

- [ ] **Step 2: Write the failing test of the keys**

`node/key_test.go`:

```go
package node_test

import (
	"encoding/base32"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

var hexID = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestNewKey(t *testing.T) {
	a, err := node.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	b, err := node.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	if !hexID.MatchString(string(a.ID())) {
		t.Errorf("ID %q is not 64 lowercase hexadecimal characters", a.ID())
	}
	if a.ID() == b.ID() {
		t.Errorf("two new keys have the same ID %s", a.ID())
	}
	if got := wire.ID(a.Iroh().Public().EndpointID().String()); got != a.ID() {
		t.Errorf("the key go-iroh gets has the ID %s, want %s", got, a.ID())
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "made", "on", "the", "way")
	path := filepath.Join(dir, "operator.key")

	created, err := node.LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKey of a missing file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the key file was not created: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("the key file has mode %o, want 600", got)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}\n$`).Match(content) {
		t.Errorf("the key file is not 64 hexadecimal characters and a line feed (it has %d bytes)", len(content))
	}

	again, err := node.LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKey of the file it created: %v", err)
	}
	if again.ID() != created.ID() {
		t.Errorf("read again, the ID is %s; it was %s", again.ID(), created.ID())
	}
	if after, _ := os.ReadFile(path); string(after) != string(content) {
		t.Error("reading the key file changed it")
	}
}

func TestLoadOrCreateKeyReadsAKnownSeed(t *testing.T) {
	// The seed of RFC 8032, test 1, and its public key.
	const seed = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
	const id = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
	for name, content := range map[string]string{
		"with a line feed":    seed + "\n",
		"without a line feed": seed,
		"in capitals":         strings.ToUpper(seed) + "\n",
		"with space around":   "  " + seed + " \r\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			k, err := node.LoadOrCreateKey(path)
			if err != nil {
				t.Fatalf("LoadOrCreateKey: %v", err)
			}
			if k.ID() != id {
				t.Errorf("got the ID %s, want %s", k.ID(), id)
			}
		})
	}
}

func TestLoadOrCreateKeyRefusesWhatIsNoKey(t *testing.T) {
	for name, content := range map[string]string{
		"empty":            "",
		"text":             "this is no key\n",
		"too short":        strings.Repeat("ab", 31) + "\n",
		"too long":         strings.Repeat("ab", 33) + "\n",
		"not hexadecimal":  strings.Repeat("zz", 32) + "\n",
		"a seed of zeros":  strings.Repeat("00", 32) + "\n",
		"two seeds":        strings.Repeat("ab", 32) + "\n" + strings.Repeat("cd", 32) + "\n",
		"very much of it":  strings.Repeat("ab", 4096),
		"an endpoint ID 0": "0\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := node.LoadOrCreateKey(path)
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the error does not name the file: %v", err)
			}
			if after, _ := os.ReadFile(path); string(after) != content {
				t.Error("the file that is no key was changed")
			}
		})
	}
}

func TestParseID(t *testing.T) {
	k, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	id := string(k.ID())
	raw, err := hex.DecodeString(id)
	if err != nil {
		t.Fatal(err)
	}
	// Other iroh programs write an endpoint ID as base32 without padding.
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)

	valid := []struct {
		name string
		text string
	}{
		{"hexadecimal", id},
		{"hexadecimal in capitals", strings.ToUpper(id)},
		{"base32 in capitals", b32},
		{"base32 in lower case", strings.ToLower(b32)},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			got, err := node.ParseID(tt.text)
			if err != nil {
				t.Fatalf("ParseID(%q): %v", tt.text, err)
			}
			if string(got) != id {
				t.Errorf("ParseID(%q): got %s, want %s", tt.text, got, id)
			}
		})
	}

	invalid := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"a word", "draal"},
		{"63 characters", id[:63]},
		{"65 characters", id + "0"},
		{"64 characters that are no hexadecimal", strings.Repeat("zz", 32)},
		{"a line feed behind it", id + "\n"},
		{"a space before it", " " + id},
		{"the beginning of an ID", id[:8]},
		{"32 bytes that are no point of the curve", "02" + strings.Repeat("00", 31)},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			got, err := node.ParseID(tt.text)
			if err == nil {
				t.Fatalf("ParseID(%q): got %s, want an error", tt.text, got)
			}
			if got != "" {
				t.Errorf("ParseID(%q): got %q beside the error", tt.text, got)
			}
		})
	}
}
```

- [ ] **Step 3: Run the test to see it fail**

Run: `go test ./node/`
Expected: FAIL — no Go files but the test in `node`, or `undefined: node.NewKey`, `undefined: node.LoadOrCreateKey`, `undefined: node.ParseID`

- [ ] **Step 4: Write the keys**

`node/key.go`:

```go
package node

import (
	"fmt"

	jnode "github.com/amber-store/jaccard-store/node"
	"github.com/tmc/go-iroh/key"

	"github.com/fables-for-robots/great-machine/wire"
)

// Key is the secret key of an endpoint. The zero value is no key: Listen
// and Dial refuse it.
type Key struct {
	secret key.SecretKey
}

// NewKey makes a new key, as a zathras does every time it starts.
func NewKey() (Key, error) {
	secret, err := key.GenerateSecretKey()
	if err != nil {
		return Key{}, fmt.Errorf("new key: %w", err)
	}
	return Key{secret: secret}, nil
}

// LoadOrCreateKey reads the key file at path, which holds the seed as 64
// hexadecimal characters, and creates it with mode 0600, and its directory,
// when it is missing.
//
// It is the key file of jaccard-store, read and written by its code: white
// space around the seed is allowed, and a file of any other shape is an
// error that names the path, repeats nothing of the contents and leaves
// the file as it is.
func LoadOrCreateKey(path string) (Key, error) {
	secret, err := jnode.LoadOrCreateKey(path)
	if err != nil {
		return Key{}, err
	}
	return Key{secret: secret}, nil
}

// ID returns the endpoint ID of the key: its public half.
func (k Key) ID() wire.ID {
	return idOf(k.secret.Public().EndpointID())
}

// Iroh returns the key as go-iroh has it (key.SecretKey of
// github.com/tmc/go-iroh/key), for the packages that dial with a library
// of their own.
func (k Key) Iroh() key.SecretKey {
	return k.secret
}

// ParseID accepts an endpoint ID as 64 hexadecimal characters or as
// go-iroh writes it, and refuses what is no endpoint ID.
//
// go-iroh writes an ID as 64 lowercase hexadecimal characters, and reads
// the 52 characters of base32 of other iroh programs as well. Hexadecimal
// in capitals is accepted. What is returned is always in lower case.
func ParseID(text string) (wire.ID, error) {
	id, err := key.ParseEndpointID(text)
	if err != nil {
		return "", fmt.Errorf("%q is no endpoint ID: %w", text, err)
	}
	return idOf(id), nil
}

// idOf returns id as it travels: 64 lowercase hexadecimal characters.
func idOf(id key.EndpointID) wire.ID {
	return wire.ID(id.String())
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go mod tidy && go vet ./node/ && go test ./node/ -v`
Expected: PASS — `TestNewKey`, `TestLoadOrCreateKey`, `TestLoadOrCreateKeyReadsAKnownSeed`, `TestLoadOrCreateKeyRefusesWhatIsNoKey`, `TestParseID`

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum node/key.go node/key_test.go
git commit -m "Node: keys and endpoint IDs"
```

- [ ] **Step 7: Write the failing tests of a connection, over the network in memory**

`testConn` is what a `Conn` and its `Stream`s have to do. Here it runs over `nodetest`; Step 13 runs the same over go-iroh.

`node/conn_test.go`:

```go
package node_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
)

// patience is how long a test waits for what has to happen. Nothing waits
// that long unless it fails.
const patience = 10 * time.Second

// notYet is how long a test waits for what must not happen.
const notYet = 250 * time.Millisecond

// connPair makes a connection and returns its two sides: client dialed,
// server accepted. Both are closed when the test ends.
type connPair func(t *testing.T) (client, server node.Conn)

// The same expectations are checked of the network in memory and, in
// iroh_test.go, of go-iroh, so that what is tested over the one holds over
// the other.
func TestNodetestConn(t *testing.T) {
	testConn(t, func(t *testing.T) (client, server node.Conn) {
		t.Helper()
		n := nodetest.New()
		l := n.Listen(nodetest.ID(1))
		t.Cleanup(func() { l.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), patience)
		defer cancel()
		client, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		t.Cleanup(func() { client.Close() })
		server, err = l.Accept(ctx)
		if err != nil {
			t.Fatalf("Accept: %v", err)
		}
		t.Cleanup(func() { server.Close() })
		if got := client.RemoteID(); got != nodetest.ID(1) {
			t.Errorf("the client's RemoteID is %s, want %s", got, nodetest.ID(1))
		}
		if got := server.RemoteID(); got != nodetest.ID(2) {
			t.Errorf("the server's RemoteID is %s, want %s", got, nodetest.ID(2))
		}
		return client, server
	})
}

// result is what a call that may wait returned.
type result struct {
	n    int
	data []byte
	err  error
}

// wait returns what arrives on ch, and fails the test when nothing does.
func wait[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(patience):
		t.Fatalf("%s: nothing after %v", what, patience)
		panic("not reached")
	}
}

// open opens a stream on from, writes first to it, and accepts it on to.
func open(t *testing.T, from, to node.Conn, first string) (opened, accepted node.Stream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	opened, err := from.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if _, err := opened.Write([]byte(first)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	accepted, err = to.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	expect(t, accepted, first)
	return opened, accepted
}

// expect reads exactly want from s.
func expect(t *testing.T, s io.Reader, want string) {
	t.Helper()
	got := make(chan result, 1)
	go func() {
		buf := make([]byte, len(want))
		_, err := io.ReadFull(s, buf)
		got <- result{data: buf, err: err}
	}()
	r := wait(t, got, "reading "+want)
	if r.err != nil {
		t.Fatalf("reading %q: %v", want, r.err)
	}
	if string(r.data) != want {
		t.Fatalf("read %q, want %q", r.data, want)
	}
}

// readAll reads s to its end in the background.
func readAll(s io.Reader) <-chan result {
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(s)
		ch <- result{data: data, err: err}
	}()
	return ch
}

func testConn(t *testing.T, pair connPair) {
	t.Run("streams in both directions on one connection", func(t *testing.T) {
		client, server := pair(t)
		for _, dir := range []struct {
			name     string
			from, to node.Conn
		}{
			{"opened by the side that dialed", client, server},
			{"opened by the side that accepted", server, client},
		} {
			opened, accepted := open(t, dir.from, dir.to, "request")
			if _, err := accepted.Write([]byte("answer")); err != nil {
				t.Fatalf("%s: Write of the answer: %v", dir.name, err)
			}
			expect(t, opened, "answer")
			if _, err := opened.Write([]byte("more")); err != nil {
				t.Fatalf("%s: second Write: %v", dir.name, err)
			}
			expect(t, accepted, "more")
		}
	})

	t.Run("several streams at once", func(t *testing.T) {
		client, server := pair(t)
		const each = 8
		ctx, cancel := context.WithTimeout(context.Background(), patience)
		defer cancel()

		// Each side answers every stream it accepts with what it read,
		// behind "echo ".
		echo := func(c node.Conn) {
			for {
				s, err := c.AcceptStream(ctx)
				if err != nil {
					return
				}
				go func() {
					data, err := io.ReadAll(s)
					if err != nil {
						return
					}
					s.Write(append([]byte("echo "), data...))
					s.CloseWrite()
				}()
			}
		}
		go echo(client)
		go echo(server)

		var wg sync.WaitGroup
		errs := make(chan error, 2*each)
		for i := 0; i < each; i++ {
			for name, c := range map[string]node.Conn{"client": client, "server": server} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					msg := bytes.Repeat([]byte(fmt.Sprintf("%s %d;", name, i)), 1000)
					s, err := c.OpenStream(ctx)
					if err != nil {
						errs <- fmt.Errorf("%s %d: OpenStream: %w", name, i, err)
						return
					}
					defer s.Close()
					if _, err := s.Write(msg); err != nil {
						errs <- fmt.Errorf("%s %d: Write: %w", name, i, err)
						return
					}
					if err := s.CloseWrite(); err != nil {
						errs <- fmt.Errorf("%s %d: CloseWrite: %w", name, i, err)
						return
					}
					got, err := io.ReadAll(s)
					if err != nil {
						errs <- fmt.Errorf("%s %d: reading the echo: %w", name, i, err)
						return
					}
					if want := append([]byte("echo "), msg...); !bytes.Equal(got, want) {
						errs <- fmt.Errorf("%s %d: the echo has %d bytes and is not what was sent (%d bytes)", name, i, len(got), len(msg))
					}
				}()
			}
		}
		finished := make(chan struct{})
		go func() { wg.Wait(); close(finished) }()
		wait(t, finished, "the streams")
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	})

	// Nobody may rely on the opposite: whoever opens a stream writes first.
	t.Run("a stream is not seen before its opener has written", func(t *testing.T) {
		client, server := pair(t)
		for _, dir := range []struct {
			name     string
			from, to node.Conn
		}{
			{"opened by the side that dialed", client, server},
			{"opened by the side that accepted", server, client},
		} {
			ctx, cancel := context.WithTimeout(context.Background(), patience)
			opened, err := dir.from.OpenStream(ctx)
			if err != nil {
				t.Fatalf("%s: OpenStream: %v", dir.name, err)
			}
			short, stop := context.WithTimeout(ctx, notYet)
			if s, err := dir.to.AcceptStream(short); err == nil {
				t.Errorf("%s: AcceptStream returned a stream nothing was written to: %v", dir.name, s)
			}
			stop()
			if _, err := opened.Write([]byte("now")); err != nil {
				t.Fatalf("%s: Write: %v", dir.name, err)
			}
			accepted, err := dir.to.AcceptStream(ctx)
			if err != nil {
				t.Fatalf("%s: AcceptStream after the Write: %v", dir.name, err)
			}
			expect(t, accepted, "now")
			cancel()
		}
	})

	t.Run("a stream that is ended without a Write is seen", func(t *testing.T) {
		client, server := pair(t)
		ctx, cancel := context.WithTimeout(context.Background(), patience)
		defer cancel()
		opened, err := client.OpenStream(ctx)
		if err != nil {
			t.Fatalf("OpenStream: %v", err)
		}
		if err := opened.CloseWrite(); err != nil {
			t.Fatalf("CloseWrite: %v", err)
		}
		accepted, err := server.AcceptStream(ctx)
		if err != nil {
			t.Fatalf("AcceptStream: %v", err)
		}
		if r := wait(t, readAll(accepted), "the end of the stream"); r.err != nil || len(r.data) != 0 {
			t.Errorf("read %q, %v; want the end and nothing before it", r.data, r.err)
		}
	})

	t.Run("AcceptStream gives up with its context", func(t *testing.T) {
		client, server := pair(t)
		short, stop := context.WithTimeout(context.Background(), notYet)
		defer stop()
		if _, err := server.AcceptStream(short); err == nil {
			t.Fatal("AcceptStream returned a stream nobody opened")
		}
		// The connection is as good as before.
		open(t, client, server, "still here")
	})

	t.Run("CloseWrite ends one direction", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		rest := readAll(accepted)
		if _, err := opened.Write([]byte(" and the rest")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := opened.CloseWrite(); err != nil {
			t.Fatalf("CloseWrite: %v", err)
		}
		// The peer reads what was written and then the end.
		if r := wait(t, rest, "the end of what was sent"); r.err != nil || string(r.data) != " and the rest" {
			t.Fatalf("the peer read %q, %v; want %q and the end", r.data, r.err, " and the rest")
		}
		// The other direction is as it was.
		if _, err := accepted.Write([]byte("answer")); err != nil {
			t.Fatalf("the peer's Write after CloseWrite: %v", err)
		}
		expect(t, opened, "answer")
		if _, err := opened.Write([]byte("x")); err == nil {
			t.Error("a Write after CloseWrite did not fail")
		}
		if err := accepted.CloseWrite(); err != nil {
			t.Fatalf("the peer's CloseWrite: %v", err)
		}
		if r := wait(t, readAll(opened), "the end of the answer"); r.err != nil || len(r.data) != 0 {
			t.Errorf("read %q, %v; want the end", r.data, r.err)
		}
	})

	t.Run("Close of a stream delivers what was written and then the end", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		// The peer waits in Read.
		rest := readAll(accepted)
		if _, err := opened.Write([]byte("last words")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := opened.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if r := wait(t, rest, "the peer's Read"); r.err != nil || string(r.data) != "last words" {
			t.Errorf("the peer read %q, %v; want %q and the end", r.data, r.err, "last words")
		}
	})

	t.Run("Close of a stream fails the peer's Write", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		// The peer writes until it may not: it comes to wait in Write when
		// nobody reads, and Close has to end that wait too.
		failed := make(chan error, 1)
		go func() {
			chunk := make([]byte, 64<<10)
			for {
				if _, err := accepted.Write(chunk); err != nil {
					failed <- err
					return
				}
			}
		}()
		if err := opened.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := wait(t, failed, "the failure of the peer's Write"); err == nil {
			t.Error("the peer's Write ended without an error")
		}
	})

	t.Run("Close of a stream fails this side's Read and Write", func(t *testing.T) {
		client, server := pair(t)
		opened, _ := open(t, client, server, "request")
		// This side waits in Read.
		blocked := make(chan result, 1)
		go func() {
			n, err := opened.Read(make([]byte, 1))
			blocked <- result{n: n, err: err}
		}()
		if err := opened.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if r := wait(t, blocked, "the Read that waited"); r.err == nil || r.err == io.EOF {
			t.Errorf("the Read that waited returned %d, %v; want an error that is not io.EOF", r.n, r.err)
		}
		if _, err := opened.Read(make([]byte, 1)); err == nil || err == io.EOF {
			t.Errorf("a Read after Close returned %v; want an error that is not io.EOF", err)
		}
		if _, err := opened.Write([]byte("x")); err == nil {
			t.Error("a Write after Close did not fail")
		}
	})

	t.Run("Close of a stream may come after CloseWrite and twice", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		if err := opened.CloseWrite(); err != nil {
			t.Fatalf("CloseWrite: %v", err)
		}
		if err := opened.Close(); err != nil {
			t.Errorf("Close after CloseWrite: %v", err)
		}
		if err := opened.Close(); err != nil {
			t.Errorf("a second Close: %v", err)
		}
		if r := wait(t, readAll(accepted), "the end"); r.err != nil {
			t.Errorf("the peer read %v, want the end", r.err)
		}
		if err := accepted.Close(); err != nil {
			t.Errorf("the peer's Close: %v", err)
		}
		// The connection is as good as before.
		open(t, server, client, "another")
	})

	for _, who := range []string{"the side that dialed", "the side that accepted"} {
		t.Run("Close of the connection by "+who, func(t *testing.T) {
			client, server := pair(t)
			closer, other := client, server
			if who == "the side that accepted" {
				closer, other = server, client
			}
			opened, accepted := open(t, client, server, "request")
			ctx, cancel := context.WithTimeout(context.Background(), patience)
			defer cancel()

			// Calls that wait, on both sides.
			accepts := make(chan error, 2)
			for _, c := range []node.Conn{client, server} {
				go func() {
					_, err := c.AcceptStream(ctx)
					accepts <- err
				}()
			}
			reads := make(chan error, 2)
			for _, s := range []node.Stream{opened, accepted} {
				go func() {
					_, err := s.Read(make([]byte, 1))
					reads <- err
				}()
			}
			select {
			case <-client.Done():
				t.Fatal("Done of the client is closed before anybody closed")
			case <-server.Done():
				t.Fatal("Done of the server is closed before anybody closed")
			case <-time.After(notYet):
			}

			if err := closer.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			wait(t, closer.Done(), "Done of the side that closed")
			wait(t, other.Done(), "Done of the other side")
			for range 2 {
				if err := wait(t, accepts, "an AcceptStream that waited"); err == nil {
					t.Error("an AcceptStream that waited returned a stream")
				}
			}
			for range 2 {
				if err := wait(t, reads, "a Read that waited"); err == nil || err == io.EOF {
					t.Errorf("a Read that waited returned %v; want an error that is not io.EOF", err)
				}
			}
			for name, c := range map[string]node.Conn{"client": client, "server": server} {
				if _, err := c.AcceptStream(ctx); err == nil {
					t.Errorf("%s: AcceptStream after the end returned a stream", name)
				}
				if s, err := c.OpenStream(ctx); err == nil {
					// Opening may still seem to work; writing may not.
					if _, err := s.Write([]byte("x")); err == nil {
						t.Errorf("%s: a Write to a stream opened after the end did not fail", name)
					}
				}
			}
			if _, err := opened.Write([]byte("x")); err == nil {
				t.Error("a Write to a stream after the end of the connection did not fail")
			}
			if err := closer.Close(); err != nil {
				t.Errorf("a second Close: %v", err)
			}
			if err := other.Close(); err != nil {
				t.Errorf("Close of the other side after the end: %v", err)
			}
		})
	}
}
```

`node/nodetest/nodetest_test.go`:

```go
package nodetest_test

import (
	"context"
	"io"
	"regexp"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

// What a Conn and a Stream of this package do is tested in package node,
// by the tests that run over go-iroh as well (node/conn_test.go). Here is
// what the network in memory has of its own.

// patience is how long a test waits for what has to happen.
const patience = 10 * time.Second

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	t.Cleanup(cancel)
	return ctx
}

// wait returns what arrives on ch, and fails the test when nothing does.
func wait[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(patience):
		t.Fatalf("%s: nothing after %v", what, patience)
		panic("not reached")
	}
}

func TestID(t *testing.T) {
	hexID := regexp.MustCompile(`^[0-9a-f]{64}$`)
	seen := make(map[wire.ID]int)
	for _, n := range []int{0, 1, 2, 15, 16, 255, 1 << 20} {
		id := nodetest.ID(n)
		if !hexID.MatchString(string(id)) {
			t.Errorf("ID(%d) = %q, which is not 64 lowercase hexadecimal characters", n, id)
		}
		if other, ok := seen[id]; ok {
			t.Errorf("ID(%d) and ID(%d) are both %s", n, other, id)
		}
		seen[id] = n
	}
	if nodetest.ID(7) != nodetest.ID(7) {
		t.Error("ID(7) is not the same twice")
	}
	if got, want := nodetest.ID(255), wire.ID("00000000000000000000000000000000000000000000000000000000000000ff"); got != want {
		t.Errorf("ID(255) = %s, want %s", got, want)
	}
}

func TestDialAndAccept(t *testing.T) {
	ctx := testContext(t)
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
	defer l.Close()
	if l.ID() != nodetest.ID(1) {
		t.Errorf("the listener's ID is %s, want %s", l.ID(), nodetest.ID(1))
	}

	// Dial returns before Accept is called, as a dial over iroh does.
	client, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	server, err := l.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer server.Close()
	if client.RemoteID() != nodetest.ID(1) {
		t.Errorf("the client's RemoteID is %s, want %s", client.RemoteID(), nodetest.ID(1))
	}
	if server.RemoteID() != nodetest.ID(2) {
		t.Errorf("the server's RemoteID is %s, want %s", server.RemoteID(), nodetest.ID(2))
	}
}

func TestDialFailsWhenNobodyListens(t *testing.T) {
	ctx := testContext(t)
	n := nodetest.New()
	if c, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1)); err == nil {
		c.Close()
		t.Fatal("Dial to an ID nobody listens as: no error")
	}

	l := n.Listen(nodetest.ID(1))
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1)); err == nil {
		c.Close()
		t.Fatal("Dial to an ID whose listener was closed: no error")
	}
}

func TestTwoNetsAreApart(t *testing.T) {
	ctx := testContext(t)
	a, b := nodetest.New(), nodetest.New()
	l := a.Listen(nodetest.ID(1))
	defer l.Close()
	if c, err := b.Dial(ctx, nodetest.ID(2), nodetest.ID(1)); err == nil {
		c.Close()
		t.Fatal("Dial reached a listener of another Net")
	}
}

func TestListenTwiceForOneIDPanics(t *testing.T) {
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
	defer l.Close()
	defer func() {
		if recover() == nil {
			t.Error("a second Listen for an ID that listens did not panic")
		}
	}()
	n.Listen(nodetest.ID(1))
}

func TestListenAgainAfterClose(t *testing.T) {
	ctx := testContext(t)
	n := nodetest.New()
	if err := n.Listen(nodetest.ID(1)).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A draal that is started again listens as the ID it had.
	l := n.Listen(nodetest.ID(1))
	defer l.Close()
	c, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		t.Fatalf("Dial to the second listener: %v", err)
	}
	c.Close()
}

func TestListenerCloseEndsAccept(t *testing.T) {
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
	failed := make(chan error, 1)
	go func() {
		_, err := l.Accept(context.Background())
		failed <- err
	}()
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := wait(t, failed, "the Accept that waited"); err == nil {
		t.Error("the Accept that waited returned a connection")
	}
	if _, err := l.Accept(context.Background()); err == nil {
		t.Error("Accept after Close returned a connection")
	}
	if err := l.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
}

func TestListenerAcceptGivesUpWithItsContext(t *testing.T) {
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
	defer l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Accept(ctx); err == nil {
		t.Fatal("Accept returned a connection nobody made")
	}
}

// As over iroh: closing the listener ends the connections it accepted,
// and those that wait to be accepted.
func TestListenerCloseEndsItsConnections(t *testing.T) {
	ctx := testContext(t)
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
	accepted, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	server, err := l.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	waiting, err := n.Dial(ctx, nodetest.ID(3), nodetest.ID(1))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wait(t, server.Done(), "Done of the connection that was accepted")
	wait(t, accepted.Done(), "Done of the connection that was dialed")
	wait(t, waiting.Done(), "Done of the connection that waited to be accepted")
}

// stream opens a stream from client to server over a new Net.
func stream(t *testing.T) (opened, accepted node.Stream) {
	t.Helper()
	ctx := testContext(t)
	n := nodetest.New()
	l := n.Listen(nodetest.ID(1))
	t.Cleanup(func() { l.Close() })
	client, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	server, err := l.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	opened, err = client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if _, err := opened.Write([]byte{1}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	accepted, err = server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	if _, err := io.ReadFull(accepted, make([]byte, 1)); err != nil {
		t.Fatalf("Read: %v", err)
	}
	return opened, accepted
}

// The streams are buffered. This test has one goroutine: were a Write to
// wait for the peer's Read, it would never end.
func TestAWriteReturnsBeforeThePeerReads(t *testing.T) {
	opened, accepted := stream(t)
	frames := [][]byte{[]byte("a request"), make([]byte, 64<<10), []byte("and one more")}
	for _, frame := range frames {
		if n, err := opened.Write(frame); err != nil || n != len(frame) {
			t.Fatalf("Write of %d bytes: %d, %v", len(frame), n, err)
		}
	}
	// And in the other direction, before anything was read.
	if _, err := accepted.Write([]byte("an answer")); err != nil {
		t.Fatalf("Write of the answer: %v", err)
	}
	for _, frame := range frames {
		got := make([]byte, len(frame))
		if _, err := io.ReadFull(accepted, got); err != nil {
			t.Fatalf("Read of %d bytes: %v", len(frame), err)
		}
		if string(got) != string(frame) {
			t.Errorf("read %d bytes that are not the %d that were written", len(got), len(frame))
		}
	}
	got := make([]byte, len("an answer"))
	if _, err := io.ReadFull(opened, got); err != nil || string(got) != "an answer" {
		t.Errorf("read %q, %v; want the answer", got, err)
	}
}

// A Write waits when 512 KiB wait to be read, as a stream of QUIC does,
// and goes on when the peer reads. A frame may be larger than that: who
// writes one needs a peer that reads meanwhile.
func TestALargeWriteWaitsForThePeerToRead(t *testing.T) {
	opened, accepted := stream(t)
	const size = 4 << 20
	written := make(chan error, 1)
	go func() {
		_, err := opened.Write(make([]byte, size))
		written <- err
	}()
	select {
	case err := <-written:
		t.Fatalf("a Write of %d bytes returned %v before the peer read anything", size, err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := io.ReadFull(accepted, make([]byte, size)); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := wait(t, written, "the Write"); err != nil {
		t.Errorf("Write: %v", err)
	}
}

func TestAReadIntoNothingReturnsAtOnce(t *testing.T) {
	opened, _ := stream(t)
	if n, err := opened.Read(nil); n != 0 || err != nil {
		t.Errorf("Read(nil): %d, %v; want 0 and no error", n, err)
	}
}
```

- [ ] **Step 8: Run the tests to see them fail**

Run: `go test ./node/...`
Expected: FAIL — `package github.com/fables-for-robots/great-machine/node/nodetest is not in std` or `no required module provides package …/node/nodetest`, and `undefined: node.Conn`

- [ ] **Step 9: Write the interfaces and the network in memory**

`node/node.go`:

```go
// Package node is where great-machine meets iroh: the keys, the endpoint
// draal listens on, and the dial of a zathras or an operator.
//
// A peer is known by its endpoint ID, the public half of its key, which
// the handshake proves. Every connection is for wire.ALPN. Both sides of a
// connection open streams and accept them: a zathras dials draal, and
// draal opens the streams on which it sends its commands.
//
// Listen is over the node package of jaccard-store, which binds the
// endpoint and announces it. The dial is this package's own, modelled on
// that of jaccard-store, because the side that dials has to accept streams
// and to set how soon a peer that vanished is noticed.
//
// The rest of great-machine uses the interfaces Conn, Stream and Listener
// and nothing of iroh, so that it is tested over node/nodetest.
package node

import (
	"context"
	"io"
	"net/netip"

	"github.com/fables-for-robots/great-machine/wire"
)

// Stream is one stream of a connection, in both directions.
//
// One goroutine may read while another writes. Two that write at once
// have to take turns themselves.
type Stream interface {
	io.Reader
	io.Writer
	// CloseWrite ends what this side sends. The peer reads io.EOF after
	// what was written. This side can still read.
	CloseWrite() error
	// Close ends the stream in both directions. What this side wrote
	// before is still delivered, and the peer reads io.EOF after it. A
	// Write of the peer fails, at once or when it next waits, and so does
	// a Read of this side. Close may be called after CloseWrite and more
	// than once.
	Close() error
}

// Conn is a connection to a peer. Both sides open streams and accept them.
type Conn interface {
	// RemoteID returns the endpoint ID of the peer, as the handshake
	// proved it.
	RemoteID() wire.ID
	// OpenStream opens a stream.
	//
	// THE PEER DOES NOT SEE THE STREAM UNTIL SOMETHING IS WRITTEN TO IT:
	// its AcceptStream returns only when the first bytes, or the end of
	// the stream, have arrived. Whoever opens a stream writes first. Every
	// protocol of great-machine begins with a Request of the side that
	// opened the stream, so this holds; a protocol in which the side that
	// accepts speaks first would wait for ever.
	OpenStream(ctx context.Context) (Stream, error)
	// AcceptStream waits for a stream the peer opens. It fails when the
	// connection has ended.
	AcceptStream(ctx context.Context) (Stream, error)
	// Done is closed when the connection has ended, whoever ended it.
	Done() <-chan struct{}
	// Close ends the connection, and every stream of it on both sides.
	// What was written and has not arrived is lost: a side that wants its
	// last message read waits for the peer to end the stream or the
	// connection. Close has to be called for a connection that was dialed
	// even when Done is closed already, to free its endpoint. It may be
	// called more than once.
	Close() error
}

// Listener accepts the connections of an endpoint.
type Listener interface {
	// ID returns the endpoint ID that peers dial.
	ID() wire.ID
	// LocalAddr is the UDP address the endpoint is bound to.
	LocalAddr() netip.AddrPort
	// Accept waits for a connection whose handshake has completed.
	Accept(ctx context.Context) (Conn, error)
	// Close ends Accept and the endpoint, and with it every connection
	// that was accepted.
	Close() error
}
```

`node/nodetest/nodetest.go`:

```go
// Package nodetest is a network in memory with the Conn, Stream and
// Listener of package node, for the tests of what speaks the protocol. It
// needs no iroh, no socket and no time.
//
// It behaves as the real thing does wherever a protocol could tell the
// difference:
//
//   - Both sides of a connection open streams and accept them.
//   - A stream is not seen by the peer's AcceptStream until its opener has
//     written to it or ended it.
//   - A Write returns before the peer has read, until 512 KiB wait to be
//     read; then it waits, as a stream of QUIC does.
//   - CloseWrite ends one direction: the peer reads io.EOF after what was
//     written, and can still write.
//   - Close of a stream delivers what was written and then io.EOF, fails
//     the peer's Write, and fails this side's Read.
//   - Close of a connection closes Done on both sides and fails every
//     call that waits on it or on one of its streams.
package nodetest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// window is how many bytes of a stream may wait to be read before a Write
// waits: the window a stream of go-iroh begins with.
const window = 512 << 10

// backlog is how many connections may wait for Accept before Dial waits.
const backlog = 64

var (
	errConnEnded     = errors.New("nodetest: the connection has ended")
	errStreamClosed  = errors.New("nodetest: the stream was closed on this side")
	errPeerClosed    = errors.New("nodetest: the peer closed the stream")
	errListenerEnded = errors.New("nodetest: the listener is closed")
)

// Net is a network in memory. Its streams are buffered: a Write of a
// frame returns before the peer has read it.
type Net struct {
	mu        sync.Mutex
	listeners map[wire.ID]*listener
}

// New returns a network on which nobody listens.
func New() *Net {
	return &Net{listeners: make(map[wire.ID]*listener)}
}

// ID returns an endpoint ID for tests: 64 hexadecimal characters made of n.
func ID(n int) wire.ID {
	return wire.ID(fmt.Sprintf("%064x", n))
}

// Listen makes id reachable. A second Listen for an id that listens fails
// the test through panic. An id whose listener was closed may listen again.
func (n *Net) Listen(id wire.ID) node.Listener {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.listeners[id]; ok {
		panic(fmt.Sprintf("nodetest: %s listens already", id))
	}
	l := &listener{
		net:      n,
		id:       id,
		incoming: make(chan *conn, backlog),
		done:     make(chan struct{}),
		conns:    make(map[*link]struct{}),
	}
	n.listeners[id] = l
	return l
}

// Dial connects from to to, and fails when nobody listens as to. It
// returns when the connection is made, before the listener's Accept has
// returned it.
func (n *Net) Dial(ctx context.Context, from, to wire.ID) (node.Conn, error) {
	n.mu.Lock()
	l := n.listeners[to]
	n.mu.Unlock()
	if l == nil {
		return nil, fmt.Errorf("nodetest: dial %s: nobody listens as that", to)
	}
	lk := &link{done: make(chan struct{})}
	client := &conn{link: lk, remote: to, accept: newQueue()}
	server := &conn{link: lk, remote: from, accept: newQueue()}
	client.peer, server.peer = server, client
	if !l.track(lk) {
		return nil, fmt.Errorf("nodetest: dial %s: %w", to, errListenerEnded)
	}
	select {
	case l.incoming <- server:
		return client, nil
	case <-l.done:
		lk.end()
		return nil, fmt.Errorf("nodetest: dial %s: %w", to, errListenerEnded)
	case <-ctx.Done():
		lk.end()
		return nil, fmt.Errorf("nodetest: dial %s: %w", to, ctx.Err())
	}
}

// listener is the Listener of one ID.
type listener struct {
	net      *Net
	id       wire.ID
	incoming chan *conn
	done     chan struct{}

	mu     sync.Mutex
	closed bool
	conns  map[*link]struct{}
}

func (l *listener) ID() wire.ID { return l.id }

// LocalAddr returns the zero value: a listener in memory has no address.
func (l *listener) LocalAddr() netip.AddrPort { return netip.AddrPort{} }

func (l *listener) Accept(ctx context.Context) (node.Conn, error) {
	select {
	case <-l.done:
		return nil, errListenerEnded
	default:
	}
	select {
	case c := <-l.incoming:
		return c, nil
	case <-l.done:
		return nil, errListenerEnded
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// track notes a connection to the listener, to end it when the listener
// is closed. It returns false when the listener is closed already.
func (l *listener) track(lk *link) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	l.conns[lk] = struct{}{}
	return true
}

// Close ends Accept, frees the ID and ends every connection to the
// listener, as closing the endpoint of the real thing does.
func (l *listener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	conns := l.conns
	l.conns = nil
	l.mu.Unlock()

	close(l.done)
	l.net.mu.Lock()
	delete(l.net.listeners, l.id)
	l.net.mu.Unlock()
	for lk := range conns {
		lk.end()
	}
	return nil
}

// link is what the two sides of a connection share: its end, and the
// streams to fail when it comes.
type link struct {
	mu      sync.Mutex
	ended   bool
	done    chan struct{}
	streams []*pipe
}

// add notes the two directions of a stream. It returns false when the
// connection has ended.
func (lk *link) add(a, b *pipe) bool {
	lk.mu.Lock()
	defer lk.mu.Unlock()
	if lk.ended {
		return false
	}
	lk.streams = append(lk.streams, a, b)
	return true
}

// end ends the connection for both sides.
func (lk *link) end() {
	lk.mu.Lock()
	if lk.ended {
		lk.mu.Unlock()
		return
	}
	lk.ended = true
	streams := lk.streams
	lk.streams = nil
	lk.mu.Unlock()

	close(lk.done)
	for _, p := range streams {
		p.fail()
	}
}

// conn is one side of a connection.
type conn struct {
	link   *link
	remote wire.ID
	peer   *conn
	// accept holds the streams the peer opened and wrote to.
	accept *queue
}

func (c *conn) RemoteID() wire.ID { return c.remote }

// OpenStream opens a stream. As with the real thing, the peer's
// AcceptStream does not return it until this side has written to it or
// ended it.
func (c *conn) OpenStream(ctx context.Context) (node.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	there, back := newPipe(), newPipe()
	if !c.link.add(there, back) {
		return nil, errConnEnded
	}
	mine := &stream{in: back, out: there}
	theirs := &stream{in: there, out: back}
	mine.announce = func() { c.peer.accept.push(theirs) }
	return mine, nil
}

func (c *conn) AcceptStream(ctx context.Context) (node.Stream, error) {
	return c.accept.pop(ctx, c.link.done)
}

func (c *conn) Done() <-chan struct{} { return c.link.done }

func (c *conn) Close() error {
	c.link.end()
	return nil
}

// queue is the streams that wait for AcceptStream, without a limit.
type queue struct {
	mu    sync.Mutex
	items []*stream
	ready chan struct{} // has room for one value: there may be items
}

func newQueue() *queue {
	return &queue{ready: make(chan struct{}, 1)}
}

func (q *queue) push(s *stream) {
	q.mu.Lock()
	q.items = append(q.items, s)
	q.mu.Unlock()
	q.wake()
}

func (q *queue) wake() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

// pop returns the oldest stream, and fails when done is closed, even if
// streams wait, or when ctx is done.
func (q *queue) pop(ctx context.Context, done <-chan struct{}) (*stream, error) {
	for {
		select {
		case <-done:
			return nil, errConnEnded
		default:
		}
		q.mu.Lock()
		if len(q.items) > 0 {
			s := q.items[0]
			q.items = q.items[1:]
			if len(q.items) > 0 {
				q.wake()
			}
			q.mu.Unlock()
			return s, nil
		}
		q.mu.Unlock()
		select {
		case <-q.ready:
		case <-done:
			return nil, errConnEnded
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// stream is one side of a stream: it reads one pipe and writes the other.
type stream struct {
	in, out *pipe
	// announce shows the stream to the peer's AcceptStream. It is nil on
	// the side that accepted the stream.
	announce func()
	once     sync.Once
}

// show makes the peer see the stream, the first time it is called.
func (s *stream) show() {
	if s.announce != nil {
		s.once.Do(s.announce)
	}
}

func (s *stream) Read(p []byte) (int, error) { return s.in.read(p) }

func (s *stream) Write(p []byte) (int, error) {
	s.show()
	return s.out.write(p)
}

func (s *stream) CloseWrite() error {
	s.show()
	s.out.closeWrite()
	return nil
}

func (s *stream) Close() error {
	s.show()
	s.out.closeWrite()
	s.in.closeRead()
	return nil
}

// pipe is one direction of a stream: bytes that were written and wait to
// be read.
type pipe struct {
	mu      sync.Mutex
	changed *sync.Cond
	buf     []byte
	eof     bool // the writer ended the direction
	gone    bool // the reader closed the stream
	failed  bool // the connection has ended
}

func newPipe() *pipe {
	p := &pipe{}
	p.changed = sync.NewCond(&p.mu)
	return p
}

func (p *pipe) write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	written := 0
	for {
		switch {
		case p.failed:
			return written, errConnEnded
		case p.eof:
			return written, errStreamClosed
		case p.gone:
			return written, errPeerClosed
		}
		if len(b) == 0 {
			return written, nil
		}
		if room := window - len(p.buf); room > 0 {
			n := min(room, len(b))
			p.buf = append(p.buf, b[:n]...)
			b = b[n:]
			written += n
			p.changed.Broadcast()
			continue
		}
		p.changed.Wait()
	}
}

func (p *pipe) read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		switch {
		case p.failed:
			return 0, errConnEnded
		case p.gone:
			return 0, errStreamClosed
		case len(p.buf) > 0:
			n := copy(b, p.buf)
			p.buf = p.buf[n:]
			p.changed.Broadcast()
			return n, nil
		case p.eof:
			return 0, io.EOF
		}
		if len(b) == 0 {
			return 0, nil
		}
		p.changed.Wait()
	}
}

// closeWrite ends the direction from the side that writes: the reader
// gets what was written and then io.EOF.
func (p *pipe) closeWrite() {
	p.mu.Lock()
	p.eof = true
	p.mu.Unlock()
	p.changed.Broadcast()
}

// closeRead ends the direction from the side that reads: what waits is
// dropped, and a Write fails.
func (p *pipe) closeRead() {
	p.mu.Lock()
	p.gone = true
	p.buf = nil
	p.mu.Unlock()
	p.changed.Broadcast()
}

// fail ends the direction because the connection has ended.
func (p *pipe) fail() {
	p.mu.Lock()
	p.failed = true
	p.buf = nil
	p.mu.Unlock()
	p.changed.Broadcast()
}
```

- [ ] **Step 10: Run the tests to see them pass**

Run: `go vet ./node/... && go test ./node/... -v`
Expected: PASS — `TestNodetestConn` with its 12 cases, and in `nodetest` `TestID`, `TestDialFailsWhenNobodyListens`, `TestListenTwiceForOneIDPanics`, `TestListenerCloseEndsAccept`, `TestAWriteReturnsBeforeThePeerReads` and the others

- [ ] **Step 11: Run them with the race detector**

Run: `go test -race -count=3 ./node/...`
Expected: `ok` for both packages. (The race detector needs cgo. If the shell has none, `go: -race requires cgo`, leave this step out.)

- [ ] **Step 12: Commit**

```bash
git add node/node.go node/conn_test.go node/nodetest/
git commit -m "Node: Conn, Stream and Listener, and a network in memory for tests"
```

- [ ] **Step 13: Write the failing tests over go-iroh**

`node/iroh_test.go`:

```go
package node_test

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// quiet is a logger that says nothing, to keep the output of the tests to
// what failed.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newKey(t *testing.T) node.Key {
	t.Helper()
	k, err := node.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

// listen binds an endpoint on loopback, without relays, that is closed
// when the test ends.
func listen(t *testing.T, k node.Key) node.Listener {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	l, err := node.Listen(ctx, node.ListenConfig{Key: k, Local: true, Log: quiet})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// dial connects to l over loopback. The connection is closed when the
// test ends.
func dial(t *testing.T, k node.Key, l node.Listener) node.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	c, err := node.Dial(ctx, node.DialConfig{
		Key:    k,
		Server: l.ID(),
		Addrs:  []netip.AddrPort{l.LocalAddr()},
		Log:    quiet,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// accept returns the next connection of l.
func accept(t *testing.T, l node.Listener) node.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	c, err := l.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// irohPair makes a connection over go-iroh on loopback.
func irohPair(t *testing.T) (client, server node.Conn) {
	t.Helper()
	serverKey, clientKey := newKey(t), newKey(t)
	l := listen(t, serverKey)
	client = dial(t, clientKey, l)
	server = accept(t, l)
	if got := client.RemoteID(); got != serverKey.ID() {
		t.Errorf("the client's RemoteID is %s, want the ID of the server's key, %s", got, serverKey.ID())
	}
	if got := server.RemoteID(); got != clientKey.ID() {
		t.Errorf("the server's RemoteID is %s, want the ID of the client's key, %s", got, clientKey.ID())
	}
	return client, server
}

// This settles the first item of section 15 of the spec: the side that
// accepted a connection opens streams on it, and the side that dialed
// accepts them.
func TestIrohConn(t *testing.T) {
	testConn(t, irohPair)
}

func TestListen(t *testing.T) {
	k := newKey(t)
	l := listen(t, k)
	if l.ID() != k.ID() {
		t.Errorf("the listener's ID is %s, want that of its key, %s", l.ID(), k.ID())
	}
	addr := l.LocalAddr()
	if !addr.Addr().IsLoopback() || addr.Port() == 0 {
		t.Errorf("a local listener is bound to %s, want a port on loopback", addr)
	}
}

func TestListenOnTheAddressItIsGiven(t *testing.T) {
	// Find a port that is free, by binding one and closing it.
	first := listen(t, newKey(t))
	addr := first.LocalAddr()
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	l, err := node.Listen(ctx, node.ListenConfig{Key: newKey(t), Local: true, Bind: addr, Log: quiet})
	if err != nil {
		t.Fatalf("Listen on %s: %v", addr, err)
	}
	defer l.Close()
	if l.LocalAddr() != addr {
		t.Errorf("bound to %s, want %s", l.LocalAddr(), addr)
	}
}

func TestListenRefusesNoKey(t *testing.T) {
	l, err := node.Listen(context.Background(), node.ListenConfig{Local: true, Log: quiet})
	if err == nil {
		l.Close()
		t.Fatal("Listen without a key: no error")
	}
}

func TestListenerCloseEndsAccept(t *testing.T) {
	l := listen(t, newKey(t))
	failed := make(chan error, 1)
	go func() {
		_, err := l.Accept(context.Background())
		failed <- err
	}()
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := wait(t, failed, "the Accept that waited"); err == nil {
		t.Error("the Accept that waited returned a connection")
	}
	if _, err := l.Accept(context.Background()); err == nil {
		t.Error("Accept after Close returned a connection")
	}
	if err := l.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
}

func TestListenerAcceptGivesUpWithItsContext(t *testing.T) {
	l := listen(t, newKey(t))
	short, stop := context.WithTimeout(context.Background(), notYet)
	defer stop()
	if _, err := l.Accept(short); err == nil {
		t.Fatal("Accept returned a connection nobody made")
	}
	// The listener is as good as before.
	client := dial(t, newKey(t), l)
	server := accept(t, l)
	open(t, client, server, "still here")
}

// Shutting an endpoint down says nothing to the peers, so Close of the
// listener has to end each connection itself.
func TestListenerCloseEndsItsConnections(t *testing.T) {
	l := listen(t, newKey(t))
	client := dial(t, newKey(t), l)
	server := accept(t, l)
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wait(t, server.Done(), "Done of the connection that was accepted")
	wait(t, client.Done(), "Done of the connection that was dialed")
}

func TestListenerAcceptsSeveralConnections(t *testing.T) {
	l := listen(t, newKey(t))
	keys := []node.Key{newKey(t), newKey(t), newKey(t)}
	want := make(map[wire.ID]bool)
	for _, k := range keys {
		dial(t, k, l)
		want[k.ID()] = true
	}
	for range keys {
		c := accept(t, l)
		if !want[c.RemoteID()] {
			t.Errorf("accepted a connection of %s, which did not dial or was accepted before", c.RemoteID())
		}
		delete(want, c.RemoteID())
	}
}

func TestDialRefuses(t *testing.T) {
	k := newKey(t)
	l := listen(t, newKey(t))
	addrs := []netip.AddrPort{l.LocalAddr()}
	tests := []struct {
		name string
		cfg  node.DialConfig
	}{
		{"no key", node.DialConfig{Server: l.ID(), Addrs: addrs}},
		{"no server", node.DialConfig{Key: k, Addrs: addrs}},
		{"a server ID that is none", node.DialConfig{Key: k, Server: "draal", Addrs: addrs}},
		{"its own ID", node.DialConfig{Key: k, Server: k.ID(), Addrs: addrs}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.Log = quiet
			c, err := node.Dial(context.Background(), tt.cfg)
			if err == nil {
				c.Close()
				t.Fatal("no error")
			}
		})
	}
}

func TestDialFailsWhenTheServerIsAnother(t *testing.T) {
	l := listen(t, newKey(t))
	other := newKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	c, err := node.Dial(ctx, node.DialConfig{
		Key:    newKey(t),
		Server: other.ID(),
		Addrs:  []netip.AddrPort{l.LocalAddr()},
		Log:    quiet,
	})
	if err == nil {
		c.Close()
		t.Fatal("Dial reached a server with another key than the one it asked for")
	}
}

func TestDialGivesUpWithItsContext(t *testing.T) {
	// An address at which nothing answers: that of a listener that was closed.
	l := listen(t, newKey(t))
	addr := l.LocalAddr()
	id := l.ID()
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	short, stop := context.WithTimeout(context.Background(), notYet)
	defer stop()
	c, err := node.Dial(short, node.DialConfig{Key: newKey(t), Server: id, Addrs: []netip.AddrPort{addr}, Log: quiet})
	if err == nil {
		c.Close()
		t.Fatal("Dial reached a listener that was closed")
	}
}

// The context of Dial bounds the dial alone.
func TestTheConnectionOutlivesTheContextOfItsDial(t *testing.T) {
	l := listen(t, newKey(t))
	ctx, cancel := context.WithCancel(context.Background())
	client, err := node.Dial(ctx, node.DialConfig{Key: newKey(t), Server: l.ID(), Addrs: []netip.AddrPort{l.LocalAddr()}, Log: quiet})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	cancel()
	server := accept(t, l)
	open(t, client, server, "after the context of the dial")
	open(t, server, client, "and back")
}
```

`node/vanish_test.go` is in package `node` itself, for the fields of `DialConfig` that nobody else can set:

`node/vanish_test.go`:

```go
package node

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"
)

// The tests of a peer that vanishes are inside the package, because they
// shorten the idle timeout and take an endpoint away without a word,
// neither of which the package lets anybody else do.

const (
	// testIdle and testKeepAlive stand in for the 6 and 2 seconds.
	testIdle      = time.Second
	testKeepAlive = 300 * time.Millisecond
	// noticedWithin is how long the tests wait for a vanished peer to be
	// noticed.
	noticedWithin = 20 * time.Second
)

// vanishPair makes a connection over loopback whose dialing side asks for
// the short idle timeout.
func vanishPair(t *testing.T) (client, server *conn, l *listener) {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), noticedWithin)
	defer cancel()

	serverKey, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(ctx, ListenConfig{Key: serverKey, Local: true, Log: quiet})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	c, err := Dial(ctx, DialConfig{
		Key:         clientKey,
		Server:      ln.ID(),
		Addrs:       []netip.AddrPort{ln.LocalAddr()},
		Log:         quiet,
		idleTimeout: testIdle,
		keepAlive:   testKeepAlive,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	s, err := ln.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return c.(*conn), s.(*conn), ln.(*listener)
}

// noticed waits for done, and fails the test when it is closed at once,
// which would mean the peer was told, or not at all.
func noticed(t *testing.T, done <-chan struct{}, since time.Time) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(noticedWithin):
		t.Fatalf("the vanished peer was not noticed within %v", noticedWithin)
	}
	if took := time.Since(since); took < testIdle/2 {
		t.Errorf("Done was closed after %v, sooner than the idle timeout of %v allows: the peer did not vanish, it was told", took, testIdle)
	}
}

// A connection that is quiet is not one whose peer is gone: the
// keep-alive holds it for longer than the idle timeout.
func TestAQuietConnectionStays(t *testing.T) {
	t.Parallel()
	client, server, _ := vanishPair(t)
	select {
	case <-client.Done():
		t.Fatal("the quiet connection ended on the side that dialed")
	case <-server.Done():
		t.Fatal("the quiet connection ended on the side that accepted")
	case <-time.After(3 * testIdle):
	}
}

// The side that accepted notices that the side that dialed is gone: draal
// and a zathras whose pod was killed. The idle timeout the dial asked for
// holds for the side that listens too, but that side takes no less than
// 5 seconds from its peer, and jaccard-store, which binds it, lets nobody
// set its own. So this test takes 5 to 8 seconds, whatever testIdle is.
func TestTheVanishedDialerIsNoticed(t *testing.T) {
	t.Parallel()
	client, server, _ := vanishPair(t)
	start := time.Now()
	// Shutting the endpoint down without closing the connection first
	// sends nothing: to the peer it is as if the machine were gone.
	if err := client.release(); err != nil {
		t.Fatalf("shutting the endpoint down: %v", err)
	}
	noticed(t, server.Done(), start)
}

// The side that dialed notices that the side that accepted is gone: a
// zathras and a draal that was killed.
func TestTheVanishedListenerIsNoticed(t *testing.T) {
	t.Parallel()
	client, _, l := vanishPair(t)
	start := time.Now()
	if err := l.ep.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutting the endpoint down: %v", err)
	}
	noticed(t, client.Done(), start)
}

// What the program runs with, so that nobody changes it without meeting
// the contract: a peer that vanishes is noticed within 10 seconds, which
// is the idle timeout and one keep-alive of the side that listens, half
// the idle timeout.
func TestTheTimesOfTheProgram(t *testing.T) {
	if worst := idleTimeout + idleTimeout/2; worst > 10*time.Second {
		t.Errorf("an idle timeout of %v lets a vanished peer go unnoticed for %v, more than 10 seconds", idleTimeout, worst)
	}
	if keepAlive > idleTimeout/2 {
		t.Errorf("a keep-alive of %v is more than half the idle timeout of %v", keepAlive, idleTimeout)
	}
}
```

- [ ] **Step 14: Run the tests to see them fail**

Run: `go test ./node/`
Expected: FAIL — `undefined: node.Listen`, `undefined: node.ListenConfig`, `undefined: node.Dial`, `undefined: node.DialConfig`, and in `vanish_test.go` `undefined: conn`, `undefined: listener`

- [ ] **Step 15: Write the connection over go-iroh**

`node/conn.go`:

```go
package node

import (
	"context"
	"sync"

	"github.com/tmc/go-iroh/iroh"

	"github.com/fables-for-robots/great-machine/wire"
)

// stream is a Stream over a stream of go-iroh.
type stream struct {
	s *iroh.Stream
}

func (s stream) Read(p []byte) (int, error)  { return s.s.Read(p) }
func (s stream) Write(p []byte) (int, error) { return s.s.Write(p) }

// CloseWrite sends the end of the stream after what was written.
func (s stream) CloseWrite() error {
	return s.s.CloseWrite()
}

// Close sends the end of the stream after what was written, which the
// peer reads as io.EOF, and tells the peer to stop sending, which fails
// its Write. Close of go-iroh's stream alone ends one direction only.
//
// The error of ending a direction that has ended before is of no use to
// anybody, so Close returns nil.
func (s stream) Close() error {
	s.s.CancelRead(0)
	_ = s.s.Close()
	return nil
}

// conn is a Conn over a connection of go-iroh. release, when it is set,
// frees what was bound for the connection alone: the endpoint of a dial.
type conn struct {
	c       *iroh.Conn
	release func() error

	close    sync.Once
	closeErr error
}

func (c *conn) RemoteID() wire.ID {
	return idOf(c.c.RemoteID())
}

func (c *conn) OpenStream(ctx context.Context) (Stream, error) {
	s, err := c.c.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return stream{s}, nil
}

func (c *conn) AcceptStream(ctx context.Context) (Stream, error) {
	s, err := c.c.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	return stream{s}, nil
}

func (c *conn) Done() <-chan struct{} {
	return c.c.Context().Done()
}

func (c *conn) Close() error {
	c.close.Do(func() {
		c.closeErr = c.c.Close()
		if c.release != nil {
			if err := c.release(); c.closeErr == nil {
				c.closeErr = err
			}
		}
	})
	return c.closeErr
}
```

`node/listen.go`:

```go
package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"

	jnode "github.com/amber-store/jaccard-store/node"
	"github.com/tmc/go-iroh/iroh"

	"github.com/fables-for-robots/great-machine/wire"
)

// ListenConfig configures the endpoint that accepts connections.
type ListenConfig struct {
	// Key is the identity of the endpoint. Its ID is what peers dial.
	Key Key
	// Bind is the UDP address to bind. The zero value: every address, a
	// port the system picks.
	Bind netip.AddrPort
	// Local is for tests: no relay, nothing announced, bound to 127.0.0.1
	// unless Bind says otherwise.
	Local bool
	// Log receives what binding and announcing have to say. Nil means
	// slog.Default().
	Log *slog.Logger
}

// Listen binds an endpoint that accepts connections for wire.ALPN.
//
// Unless cfg.Local, the endpoint uses the default relays and is announced
// through pkarr and mDNS, as jaccard-store announces its server, so that
// its ID is all a peer needs. Listen waits for the relay, 15 seconds at
// most. ctx bounds Listen alone: the endpoint and its announcing live
// until the Listener is closed.
func Listen(ctx context.Context, cfg ListenConfig) (Listener, error) {
	if cfg.Key.secret.IsZero() {
		return nil, errors.New("listen: no key")
	}
	// The announcing of jaccard-store runs until its context is done. It
	// gets one that ends with the listener, and with ctx only while
	// Listen runs.
	run, stop := context.WithCancel(context.WithoutCancel(ctx))
	binding := context.AfterFunc(ctx, stop)
	ep, err := jnode.Bind(run, jnode.ServerConfig{
		Key:   cfg.Key.secret,
		ALPN:  wire.ALPN,
		Bind:  cfg.Bind,
		Local: cfg.Local,
		Log:   cfg.Log,
	})
	if err != nil {
		stop()
		return nil, fmt.Errorf("listen: %w", err)
	}
	if !binding() {
		// ctx was done while the endpoint was bound, and took the
		// announcing with it.
		stop()
		ep.Shutdown(context.Background())
		return nil, fmt.Errorf("listen: %w", context.Cause(ctx))
	}
	return &listener{ep: ep, stop: stop, log: logger(cfg.Log), conns: make(map[*conn]struct{})}, nil
}

// listener is a Listener over an endpoint of go-iroh.
type listener struct {
	ep   *iroh.Endpoint
	stop context.CancelFunc
	log  *slog.Logger

	mu     sync.Mutex
	closed bool
	// conns are the connections that were accepted and have not ended.
	// Shutting the endpoint down says nothing to their peers, which would
	// take the idle timeout to notice, so Close closes each of them first.
	conns map[*conn]struct{}

	close    sync.Once
	closeErr error
}

func (l *listener) ID() wire.ID {
	return idOf(l.ep.ID())
}

func (l *listener) LocalAddr() netip.AddrPort {
	return l.ep.LocalAddr()
}

// Accept returns the next connection whose handshake has completed. A
// handshake that fails is not reported: Accept goes on to the next, and
// returns an error only when ctx is done or the listener is closed.
func (l *listener) Accept(ctx context.Context) (Conn, error) {
	for {
		ic, err := l.ep.Accept(ctx)
		if err == nil {
			c := &conn{c: ic}
			if !l.track(c) {
				c.Close()
				return nil, errors.New("accept: the listener is closed")
			}
			return c, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		select {
		case <-l.ep.Closed():
			return nil, fmt.Errorf("accept: the listener is closed: %w", err)
		default:
		}
		l.log.Debug("a connection was not accepted", "error", err)
	}
}

// track notes c until it has ended. It returns false when the listener is
// closed already.
func (l *listener) track(c *conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	l.conns[c] = struct{}{}
	go func() {
		<-c.Done()
		l.mu.Lock()
		delete(l.conns, c)
		l.mu.Unlock()
	}()
	return true
}

func (l *listener) Close() error {
	l.close.Do(func() {
		l.mu.Lock()
		l.closed = true
		conns := l.conns
		l.conns = nil
		l.mu.Unlock()
		for c := range conns {
			c.Close()
		}
		l.stop()
		l.closeErr = l.ep.Shutdown(context.Background())
	})
	return l.closeErr
}

// logger returns l, or the default logger when l is nil.
func logger(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return l
}
```

`node/dial.go`:

```go
package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/iroh/mdns"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// idleTimeout is how long a connection lives without hearing from its
	// peer. The two sides of a connection each name a time and the shorter
	// one holds for both, so the side that dials sets it for the side that
	// listens too, which jaccard-store binds with go-iroh's 30 seconds.
	// The side that listens takes no less than 5 seconds from its peer.
	//
	// A peer that vanished without a word is noticed later than this by
	// up to one keep-alive: the first keep-alive that is sent into the
	// silence starts the time again, once. That is at most 8 seconds for
	// the side that dialed and 9 for the side that listens, whose
	// keep-alive is half the idle timeout. Measured with a peer that was
	// killed, twelve times each: 6.0 to 7.9 seconds on the side that
	// dialed, 7.1 to 8.8 on the side that listens.
	idleTimeout = 6 * time.Second
	// keepAlive is how often a connection with nothing to send says that
	// it is alive, so that a quiet connection is not taken for a dead one.
	keepAlive = 2 * time.Second
	// mdnsLookupTimeout is how long a dial waits for an answer on the
	// local link.
	mdnsLookupTimeout = time.Second
	// lookupBudget is how long a dial waits for the lookups together.
	lookupBudget = 10 * time.Second
)

// DialConfig configures the connection to a server.
type DialConfig struct {
	// Key is the identity of this side. The server sees its ID as the
	// RemoteID of the connection.
	Key Key
	// Server is the endpoint ID of the server.
	Server wire.ID
	// Addrs is for tests: dial these addresses, use no relay, look
	// nothing up.
	Addrs []netip.AddrPort
	// Log receives what the dial has to say, at debug level. Nil means
	// slog.Default().
	Log *slog.Logger

	// idleTimeout and keepAlive replace the constants of those names, for
	// the tests of a peer that vanishes.
	idleTimeout time.Duration
	keepAlive   time.Duration
}

// Dial connects to the server for wire.ALPN. ctx bounds the dial alone.
// Close ends the connection and the endpoint that was bound for it.
//
// Without cfg.Addrs the server is looked up by its ID through mDNS, pkarr
// and DNS, and a connection is started to every address any of them
// answers with, the relay among them. The first that completes is the
// connection.
func Dial(ctx context.Context, cfg DialConfig) (Conn, error) {
	if cfg.Key.secret.IsZero() {
		return nil, errors.New("dial: no key")
	}
	if cfg.Server == "" {
		return nil, errors.New("dial: no server ID")
	}
	server, err := key.ParseEndpointID(string(cfg.Server))
	if err != nil {
		return nil, fmt.Errorf("dial: %q is no endpoint ID: %w", cfg.Server, err)
	}
	if server == cfg.Key.secret.Public().EndpointID() {
		return nil, fmt.Errorf("dial: %s is the ID of this side's own key", cfg.Server)
	}
	if cfg.idleTimeout == 0 {
		cfg.idleTimeout = idleTimeout
	}
	if cfg.keepAlive == 0 {
		cfg.keepAlive = keepAlive
	}
	log := logger(cfg.Log)

	// What is started for the lookups lives as long as the connection:
	// the endpoint may look the server up again.
	lookups, stop := context.WithCancel(context.Background())
	var (
		ep    *iroh.Endpoint
		cands []netaddr.TransportAddr
	)
	if len(cfg.Addrs) > 0 {
		ep, cands, err = bindDirect(ctx, cfg)
	} else {
		ep, cands, err = bindAndResolve(ctx, lookups, cfg, server, log)
	}
	if err != nil {
		stop()
		return nil, fmt.Errorf("dial %s: %w", cfg.Server, err)
	}

	log.Debug("connecting", "server", cfg.Server, "candidates", cands)
	c, err := raceConnect(ctx, ep, server, cands)
	if err != nil {
		stop()
		ep.Shutdown(context.Background())
		return nil, fmt.Errorf("dial %s: connect: %w", cfg.Server, err)
	}
	log.Debug("connected", "server", cfg.Server, "addr", c.RemoteAddr())
	return &conn{c: c, release: func() error {
		stop()
		return ep.Shutdown(context.Background())
	}}, nil
}

// options returns what every endpoint of a dial is bound with: the key,
// and the times after which a silent peer is given up.
func (cfg DialConfig) options() []iroh.Option {
	return []iroh.Option{
		iroh.WithSecretKey(cfg.Key.secret),
		iroh.WithTransportConfig(&iroh.QUICTransportConfig{
			MaxIdleTimeout:  cfg.idleTimeout,
			KeepAlivePeriod: cfg.keepAlive,
		}),
	}
}

// bindDirect binds an endpoint that reaches the addresses of cfg and
// nothing else: no relay and no lookups.
//
// go-iroh binds one socket for both families unless it is told an address,
// and macOS may give such a socket a port that an IPv4 socket of another
// process holds already; what arrives for that port over IPv4 then goes to
// the other socket, and the endpoint hears no answer. An IPv4 socket is
// given no port that another one has, so that is what is bound when there
// are IPv4 addresses alone to reach.
func bindDirect(ctx context.Context, cfg DialConfig) (*iroh.Endpoint, []netaddr.TransportAddr, error) {
	opts := append(cfg.options(), iroh.WithRelayMode(relay.ModeDisabled()))
	if ipv4Alone(cfg.Addrs) {
		opts = append(opts, iroh.WithBindAddr(netip.AddrPortFrom(netip.IPv4Unspecified(), 0)))
	}
	ep, err := iroh.Bind(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("bind: %w", err)
	}
	cands := make([]netaddr.TransportAddr, len(cfg.Addrs))
	for i, ap := range cfg.Addrs {
		cands[i] = netaddr.IPAddr{Addr: ap}
	}
	return ep, cands, nil
}

// ipv4Alone reports whether there are addresses and every one of them is
// an IPv4 address.
func ipv4Alone(addrs []netip.AddrPort) bool {
	for _, ap := range addrs {
		if !ap.Addr().Is4() {
			return false
		}
	}
	return len(addrs) > 0
}

// bindAndResolve binds an endpoint with the default relays and looks the
// server up through mDNS, pkarr and DNS. The mDNS listener runs until
// lookups is done; it asks on the local link and announces nothing of
// this side.
//
// It returns the union of what the lookups answered. One answer may be
// half of the picture: mDNS knows direct addresses and no relay, and any
// record may list addresses that cannot be reached from here. With all of
// them as candidates the relay stays among them.
func bindAndResolve(ctx, lookups context.Context, cfg DialConfig, server key.EndpointID, log *slog.Logger) (*iroh.Endpoint, []netaddr.TransportAddr, error) {
	pkarr, err := iroh.N0PkarrResolver(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("pkarr resolver: %w", err)
	}
	local := mdns.New(cfg.Key.secret.Public().EndpointID(),
		mdns.WithPassive(true), mdns.WithLookupTimeout(mdnsLookupTimeout), mdns.WithLogger(log))
	go func() {
		if err := local.Start(lookups); err != nil && lookups.Err() == nil {
			log.Debug("mDNS is unavailable; looking the server up through pkarr and DNS only", "error", err)
		}
	}()
	var services iroh.AddressLookupServices
	services.AddResolver(local)
	services.AddResolver(pkarr)
	services.AddResolver(iroh.N0DNSAddressLookup(nil))

	opts := append(cfg.options(), iroh.WithAddressLookup(&services), iroh.WithRelayMode(relay.ModeDefault()))
	ep, err := iroh.Bind(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("bind: %w", err)
	}
	cands, err := lookup(ctx, &services, server, log)
	if err != nil {
		ep.Shutdown(context.Background())
		return nil, nil, err
	}
	return ep, cands, nil
}

// lookup asks every service for the addresses of id and returns the union
// of the answers. It waits until all of them have answered or failed, and
// no longer than lookupBudget: a service that has not answered by then is
// left out, so that one that hangs does not use up the time the dial has.
func lookup(ctx context.Context, services *iroh.AddressLookupServices, id key.EndpointID, log *slog.Logger) ([]netaddr.TransportAddr, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupBudget)
	defer cancel()

	addr := netaddr.NewEndpointAddr(id)
	var lastErr error
	for item, err := range services.Resolve(ctx, id) {
		if err != nil {
			lastErr = err
			continue
		}
		log.Debug("server looked up", "source", item.Provenance(), "addrs", item.Addr().Addrs())
		addr = addr.WithAddrs(item.Addr().Addrs()...)
	}
	if addr.IsEmpty() {
		if lastErr != nil {
			return nil, fmt.Errorf("no address found: %w", lastErr)
		}
		return nil, errors.New("no address found")
	}
	return addr.Addrs(), nil
}

// raceConnect starts a connection to every candidate at once and returns
// the first that completes. The attempts that lose are canceled, and one
// that completes after the winner is closed.
//
// A record may list addresses that are dead from here, the address of a
// container bridge or of another network. Dialed one after the other they
// use up the time the dial has before a live one is tried; dialed
// together, a live one answers in milliseconds.
func raceConnect(ctx context.Context, ep *iroh.Endpoint, id key.EndpointID, cands []netaddr.TransportAddr) (*iroh.Conn, error) {
	if len(cands) == 0 {
		return nil, errors.New("no candidate addresses")
	}
	type result struct {
		conn *iroh.Conn
		err  error
	}
	ctx, cancel := context.WithCancel(ctx)
	results := make(chan result, len(cands))
	for _, ta := range cands {
		go func() {
			c, err := ep.Connect(ctx, netaddr.NewEndpointAddr(id, ta), wire.ALPN)
			if err != nil {
				err = fmt.Errorf("%s: %w", ta, err)
			}
			results <- result{c, err}
		}()
	}
	var errs []error
	for pending := len(cands); pending > 0; pending-- {
		r := <-results
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		cancel()
		go func(late int) {
			for ; late > 0; late-- {
				if r := <-results; r.conn != nil {
					r.conn.Close()
				}
			}
		}(pending - 1)
		return r.conn, nil
	}
	cancel()
	return nil, errors.Join(errs...)
}
```

- [ ] **Step 16: Run the tests to see them pass**

Run: `go mod tidy && gofmt -l node/ && go vet ./node/... && go test ./node/... -v`
Expected: `gofmt` prints nothing. PASS — `TestIrohConn` with the same 12 cases as `TestNodetestConn`, `TestListen`, `TestListenerCloseEndsItsConnections`, `TestDialRefuses`, `TestDialFailsWhenTheServerIsAnother`, `TestAQuietConnectionStays`, `TestTheVanishedListenerIsNoticed`, `TestTheVanishedDialerIsNoticed`, `TestTheTimesOfTheProgram`. The package takes about 10 seconds: `TestTheVanishedDialerIsNoticed` cannot take less than 5.

- [ ] **Step 17: Run them three times**

Run: `go test -count=3 ./node/...`
Expected: `ok` for both packages

- [ ] **Step 18: Commit**

```bash
git add go.mod go.sum node/
git commit -m "Node: listening and dialing over go-iroh"
```
