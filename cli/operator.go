package cli

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// dialTimeout is how long an operator command tries to reach draal.
const dialTimeout = 30 * time.Second

// operatorSettings is what the flags of every operator command say.
type operatorSettings struct {
	draal wire.ID
	key   string // the file of the operator's key
	// addrs is for tests: draal is dialed at these addresses, without a
	// relay and without a lookup. No flag sets it.
	addrs []netip.AddrPort
}

// readOperator reads --draal and --key. A command calls it before it does
// anything, so that a command line that is not valid is refused at once.
func readOperator(c *cli.Context) (operatorSettings, error) {
	id, err := draalID(c)
	if err != nil {
		return operatorSettings{}, err
	}
	path, err := keyPath(c)
	if err != nil {
		return operatorSettings{}, err
	}
	return operatorSettings{draal: id, key: path}, nil
}

// keyPath returns the file of the operator's key: what --key says, or
// great-machine/operator.key in the user's configuration directory.
func keyPath(c *cli.Context) (string, error) {
	if path := c.String("key"); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no place for the operator's key, give one with --key: %w", err)
	}
	return filepath.Join(dir, "great-machine", "operator.key"), nil
}

// connect dials draal as the operator the settings name, and returns the
// client and the operator's key. The caller closes the client.
func (s operatorSettings) connect(ctx context.Context) (*operator.Client, node.Key, error) {
	k, err := node.LoadOrCreateKey(s.key)
	if err != nil {
		return nil, node.Key{}, fmt.Errorf("the operator's key: %w", err)
	}
	client, err := s.dial(ctx, k)
	if err != nil {
		return nil, node.Key{}, err
	}
	return client, k, nil
}

// dial dials draal with the key given. The caller closes the client.
func (s operatorSettings) dial(ctx context.Context, k node.Key) (*operator.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := node.Dial(ctx, node.DialConfig{Key: k, Server: s.draal, Addrs: s.addrs})
	if err != nil {
		return nil, fmt.Errorf("connecting to draal %s: %w", s.draal, err)
	}
	return operator.New(conn), nil
}

// connect reads the flags of an operator command and dials draal.
func connect(ctx context.Context, c *cli.Context) (*operator.Client, node.Key, error) {
	s, err := readOperator(c)
	if err != nil {
		return nil, node.Key{}, err
	}
	return s.connect(ctx)
}

// fleet asks draal for the fleet as it is now.
func fleet(ctx context.Context, client *operator.Client) (wire.Fleet, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fleets, err := client.Watch(ctx)
	if err != nil {
		return wire.Fleet{}, fmt.Errorf("asking draal for the fleet: %w", err)
	}
	select {
	case f, ok := <-fleets:
		if !ok {
			if err := ctx.Err(); err != nil {
				return wire.Fleet{}, err
			}
			return wire.Fleet{}, errors.New("asking draal for the fleet: the connection ended")
		}
		return f, nil
	case <-ctx.Done():
		return wire.Fleet{}, ctx.Err()
	}
}

// interrupted is what a command returns that was interrupted: the status
// 130, as a shell gives a program that SIGINT ended, and why.
func interrupted(format string, args ...any) error {
	return cli.Exit(fmt.Sprintf(format, args...), 130)
}
