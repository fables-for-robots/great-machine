package cli

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/fables-for-robots/great-machine/closure"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/push"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// rolloutCommand is `great-machine rollout [--parallel N] [--detach]
// [--jobs N] [--temp-dir DIR] SERVICE PATH [-- ARG...]`.
func rolloutCommand() *cli.Command {
	return &cli.Command{
		Name:      "rollout",
		Usage:     "push the closure of a binary and roll it out to a service",
		ArgsUsage: "SERVICE PATH [-- ARG...]",
		Description: "PATH is the binary: a path inside /nix/store, or one that leads there\n" +
			"through symbolic links, such as ./result/bin/web. What follows -- are the\n" +
			"arguments the service is started with.\n\n" +
			"The command follows the rollout and ends with 0 when it completed and\n" +
			"with 1 when it halted or was superseded. An interrupt ends the following\n" +
			"and not the rollout.",
		Flags: operatorFlags(parallelFlag(), detachFlag(),
			&cli.IntFlag{Name: "jobs", EnvVars: []string{"GREAT_MACHINE_JOBS"}, Value: 5,
				Usage: "store paths pushed at a time"},
			&cli.StringFlag{Name: "temp-dir", EnvVars: []string{"GREAT_MACHINE_TEMP_DIR"},
				Usage: "`DIR` in which the temporary stores of a push are made (default: the system's)"},
		),
		Action: func(c *cli.Context) error {
			cfg, err := readRollout(c)
			if err != nil {
				return err
			}
			settings, err := readOperator(c)
			if err != nil {
				return err
			}
			p := newProgress(c.App.ErrWriter)
			defer p.close()
			return runRollout(c.Context, cfg, rolloutDeps{
				binary:     closure.Binary,
				checkLinux: closure.CheckLinux,
				closure:    closure.Of,
				connect:    settings.connect,
				push:       push.Push,
			}, c.App.Writer, p)
		},
	}
}

// rolloutConfig is what the command line of rollout says.
type rolloutConfig struct {
	service  string
	path     string
	args     []string // of the service
	parallel int
	detach   bool
	jobs     int
	tempDir  string
}

// rolloutDeps is what a rollout does through other packages, which a test
// replaces.
type rolloutDeps struct {
	binary     func(path string) (binary, storePath string, err error)
	checkLinux func(binary string) error
	closure    func(ctx context.Context, storePath string) ([]string, error)
	connect    func(ctx context.Context) (*operator.Client, node.Key, error)
	push       func(ctx context.Context, cfg push.Config, storePaths []string) (push.Result, error)
}

// splitArguments cuts the arguments of rollout at the first --: its own
// are before it, and those of the service behind it, verbatim. The
// library leaves a -- that follows an argument where it is.
func splitArguments(args []string) (own, service []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// readRollout reads the command line of rollout.
func readRollout(c *cli.Context) (rolloutConfig, error) {
	own, args := splitArguments(c.Args().Slice())
	usage := "usage: great-machine rollout " + c.Command.ArgsUsage
	switch {
	case len(own) == 0:
		return rolloutConfig{}, usageError("rollout: SERVICE is missing; %s", usage)
	case len(own) == 1:
		return rolloutConfig{}, usageError("rollout: PATH is missing; %s", usage)
	case len(own) > 2:
		return rolloutConfig{}, usageError("rollout: unexpected argument %q: the flags of rollout come before SERVICE, and the arguments of the service behind --", own[2])
	}
	cfg := rolloutConfig{path: own[1], args: args, detach: c.Bool("detach"), jobs: c.Int("jobs"), tempDir: c.String("temp-dir")}
	var err error
	if cfg.service, err = service(own[0]); err != nil {
		return rolloutConfig{}, err
	}
	if cfg.parallel, err = parallel(c); err != nil {
		return rolloutConfig{}, err
	}
	if cfg.jobs < 1 {
		return rolloutConfig{}, usageError("--jobs: %d is not a number of store paths to push at a time: want 1 or more", cfg.jobs)
	}
	return cfg, nil
}

// runRollout is section 6.1 of the design: the binary, its closure, the
// store, the push, the rollout, and the following of it.
func runRollout(ctx context.Context, cfg rolloutConfig, deps rolloutDeps, stdout io.Writer, p *progress) (err error) {
	defer func() {
		if err != nil {
			p.fail(err)
		}
	}()

	p.begin("resolving the binary")
	binary, storePath, err := deps.binary(cfg.path)
	if err != nil {
		return err
	}
	if err := deps.checkLinux(binary); err != nil {
		return err
	}
	p.done(binary)

	p.begin("reading the closure")
	paths, err := deps.closure(ctx, storePath)
	if err != nil {
		return err
	}
	p.done(fmt.Sprintf("%d paths", len(paths)))

	p.begin("connecting to draal")
	client, key, err := deps.connect(ctx)
	if err != nil {
		return err
	}
	// Closed on every path; closing twice does nothing. The key of the
	// operator never has two endpoints at once, for two endpoints with
	// one key evict each other at the relay: the client is closed before
	// the push, which dials the store, and draal is dialed again after.
	defer func() { client.Close() }()
	p.done("")

	// The step lasts until the push says what the server has: asking
	// draal where the store is, and the store what it holds.
	p.begin("asking the store")
	store, err := client.Store(ctx)
	if err != nil {
		return fmt.Errorf("asking draal for the store: %w", err)
	}
	client.Close()
	steps := &pushSteps{p: p}
	result, err := deps.push(ctx, push.Config{
		Key:      key,
		Server:   store.Server,
		Prefix:   store.Prefix,
		Jobs:     cfg.jobs,
		TempDir:  cfg.tempDir,
		Progress: steps,
	}, paths)
	if err != nil {
		// Nothing was rolled out: draal has not heard of it.
		return fmt.Errorf("pushing the closure: %w", err)
	}
	switch {
	case !steps.found.Load():
		p.done(fmt.Sprintf("%d of %d already there", result.Present, result.Present+result.Pushed))
	case result.Pushed == 0:
		p.done("nothing to push")
	default:
		p.done(fmt.Sprintf("%d paths, %s", result.Pushed, byteSize(steps.bytes.Load())))
	}

	p.begin("connecting to draal")
	again, _, err := deps.connect(ctx)
	if err != nil {
		return fmt.Errorf("the closure was pushed and nothing was rolled out; running the command again is cheap: %w", err)
	}
	defer again.Close()
	p.done("")

	p.begin("rolling out")
	r, err := again.Rollout(ctx, wire.RolloutRequest{
		Service:  cfg.service,
		Binary:   binary,
		Args:     cfg.args,
		Closure:  closure.Names(paths),
		Parallel: cfg.parallel,
	})
	if err != nil {
		return fmt.Errorf("rolling %s out: %w", cfg.service, err)
	}
	p.done(fmt.Sprintf("generation %d, rollout #%d", r.Generation, r.ID))
	return watchRollout(ctx, again, r, cfg.detach, stdout, p)
}

// pushSteps is the push.Progress of a rollout: it ends the step that asks
// the store when the push says what the server has, and counts the paths
// of the step that pushes. A push calls it from several goroutines.
type pushSteps struct {
	p     *progress
	found atomic.Bool
	bytes atomic.Uint64
}

// Found says how many of the store paths the server has already, and how
// many are to be pushed.
func (s *pushSteps) Found(present, missing int) {
	s.found.Store(true)
	s.p.done(fmt.Sprintf("%d of %d already there", present, present+missing))
	s.p.begin("pushing")
	s.p.count(0, uint64(missing))
}

// Pushed says that one store path was pushed, and how many bytes its pack
// came to.
func (s *pushSteps) Pushed(storePath string, bytes uint64) {
	s.bytes.Add(bytes)
	s.p.add(1)
}
