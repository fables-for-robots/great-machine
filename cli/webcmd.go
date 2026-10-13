package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/web"
	"github.com/urfave/cli/v2"
)

// webCommand is `great-machine web`.
func webCommand() *cli.Command {
	return &cli.Command{
		Name:  "web",
		Usage: "the web page of the fleet, served on this machine with the operator's key",
		Description: "Serves the web page on a loopback address and prints its link, which\n" +
			"has a ticket in it: the page opens with that link, and answers nothing\n" +
			"without it. The link is good for as long as the command runs.\n\n" +
			"The page asks draal with the operator's key, as the terminal UI does: it\n" +
			"can do what that key was granted, and draal serves no page itself. draal\n" +
			"is dialed when the page first asks for something, so the page loads\n" +
			"whether draal can be reached or not, and says so when it cannot.",
		Flags: append(operatorFlags(),
			&cli.StringFlag{Name: "addr", EnvVars: []string{"GREAT_MACHINE_WEB_ADDR"}, Value: "127.0.0.1:8080",
				Usage: "loopback `ADDR`ess the web page listens on"}),
		Action: func(c *cli.Context) error {
			cfg, err := readWeb(c)
			if err != nil {
				return err
			}
			return runWeb(c.Context, cfg, c.App.Writer, slog.New(slog.NewTextHandler(c.App.ErrWriter, nil)))
		},
	}
}

// webConfig is what the command line of web says.
type webConfig struct {
	operator operatorSettings
	addr     string
	// listen is for tests: what binds addr. Nil means net.Listen. No flag
	// sets it.
	listen func(network, addr string) (net.Listener, error)
	// dial is for tests: what dials draal, in the place of a dial with
	// the operator's key. No flag sets it.
	dial func(ctx context.Context) (*operator.Client, <-chan struct{}, error)
}

// readWeb reads the command line of web. An address that is not loopback
// is refused: the page acts with the operator's key for whoever has its
// cookie, and the ticket and the cookie travel in the clear.
func readWeb(c *cli.Context) (webConfig, error) {
	if err := noArguments(c); err != nil {
		return webConfig{}, err
	}
	s, err := readOperator(c)
	if err != nil {
		return webConfig{}, err
	}
	addr := strings.TrimSpace(c.String("addr"))
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return webConfig{}, usageError("--addr: %q is not an address to listen on: want HOST:PORT, such as 127.0.0.1:8080", addr)
	}
	if !web.IsLoopback(addr) {
		return webConfig{}, usageError("--addr: %q is not a loopback address: the page acts with the operator's key, and listens on this machine alone; "+
			"from another machine it is reached through a tunnel", addr)
	}
	return webConfig{operator: s, addr: addr}, nil
}

// secret returns 32 random bytes as they are written in a URL.
func secret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pageLink returns the link of the page for a person on this machine:
// localhost with the port that was bound, and the ticket. Where the page
// listens on a loopback address that localhost is not, the link has that
// address.
func pageLink(bound net.Addr, ticket string) string {
	host, port := "localhost", 0
	if tcp, ok := bound.(*net.TCPAddr); ok {
		port = tcp.Port
		if ip := tcp.IP; !ip.Equal(net.IPv4(127, 0, 0, 1)) && !ip.Equal(net.IPv6loopback) {
			host = ip.String()
		}
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/?ticket=" + ticket
}

// runWeb serves the web page until ctx is done, which SIGINT and SIGTERM
// do: that is how the command is meant to end, and no error. It writes
// one line to out when it listens, the link of the page.
func runWeb(ctx context.Context, cfg webConfig, out io.Writer, log *slog.Logger) error {
	dial := cfg.dial
	if dial == nil {
		// The key is read once, and before anything listens: a file that
		// cannot be read is no reason to print the link of a page that
		// can ask draal nothing.
		k, err := node.LoadOrCreateKey(cfg.operator.key)
		if err != nil {
			return fmt.Errorf("the operator's key: %w", err)
		}
		dial = func(ctx context.Context) (*operator.Client, <-chan struct{}, error) {
			conn, err := node.Dial(ctx, node.DialConfig{Key: k, Server: cfg.operator.draal, Addrs: cfg.operator.addrs})
			if err != nil {
				return nil, nil, fmt.Errorf("connecting to draal %s: %w", cfg.operator.draal, err)
			}
			return operator.New(conn), conn.Done(), nil
		}
		log.Info("the web page asks draal with the operator's key", "draal", string(cfg.operator.draal), "operator", string(k.ID()))
	} else {
		log.Info("the web page asks draal", "draal", string(cfg.operator.draal))
	}
	ticket, err := secret()
	if err != nil {
		return fmt.Errorf("making the ticket of the page: %w", err)
	}
	cookie, err := secret()
	if err != nil {
		return fmt.Errorf("making the secret of the page's cookie: %w", err)
	}

	listen := cfg.listen
	if listen == nil {
		listen = net.Listen
	}
	listener, err := listen("tcp", cfg.addr)
	if err != nil {
		return fmt.Errorf("the web page: %w", err)
	}
	port := 0
	if tcp, ok := listener.Addr().(*net.TCPAddr); ok {
		port = tcp.Port
	}

	client := newPageClient(dial)
	// Deferred first, so that it happens last: the connection to draal
	// is closed when the page asks nothing any more.
	defer client.Close()
	page := startWeb(listener, web.New(web.Config{Client: client, Ticket: ticket, Secret: cookie, Port: port, Log: log}), webStopWait, log)
	defer page.Stop()
	if !web.Built() {
		log.Warn("the web page was not built into this program: it answers every request for it with a line that says so")
	}
	if _, err := fmt.Fprintln(out, pageLink(listener.Addr(), ticket)); err != nil {
		return fmt.Errorf("printing the link of the page: %w", err)
	}

	select {
	case <-ctx.Done():
	case err := <-page.Failed():
		return err
	}
	// The sockets of the page end with the server, and what they asked
	// of draal with the client.
	page.Stop()
	log.Info("stopping")
	return nil
}
