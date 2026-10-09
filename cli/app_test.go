package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/urfave/cli/v2"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := Run([]string{"great-machine", "version"}, &stdout, &stderr); got != 0 {
		t.Fatalf("Run returned %d, want 0; stderr: %s", got, stderr.String())
	}
	if got, want := stdout.String(), Version+"\n"; got != want {
		t.Errorf("stdout is %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr is %q, want nothing", stderr.String())
	}
}

// TestRunInvalid holds Run to 2, and to one line on stderr, for every
// command line that is not valid.
func TestRunInvalid(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		stderr string
	}{
		{"unknown command", []string{"great-machine", "vorlon"},
			"great-machine: unknown command \"vorlon\"\n"},
		{"unknown flag of the program", []string{"great-machine", "--vorlon"},
			"great-machine: flag provided but not defined: -vorlon\n"},
		{"unknown flag of a command", []string{"great-machine", "version", "--vorlon"},
			"great-machine: flag provided but not defined: -vorlon\n"},
		{"an argument too many", []string{"great-machine", "version", "now"},
			"great-machine: version takes no argument, got \"now\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := Run(tt.args, &stdout, &stderr); got != 2 {
				t.Errorf("Run returned %d, want 2", got)
			}
			if got := stderr.String(); got != tt.stderr {
				t.Errorf("stderr is %q, want %q", got, tt.stderr)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout is %q, want nothing", stdout.String())
			}
		})
	}
}

// TestRunErrors runs commands of the test's own: what a command returns
// decides between 1 and 2.
func TestRunErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   int
		stderr string
	}{
		{"a failure", errors.New("the machine is asleep"), 1,
			"great-machine: the machine is asleep\n"},
		{"a usage error", usageError("want one argument %s, got %d", "SERVICE", 0), 2,
			"great-machine: want one argument SERVICE, got 0\n"},
		{"a usage error inside another error", errors.Join(errors.New("first"), usageError("second")), 2,
			"great-machine: first\nsecond\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := newApp(&stdout, &stderr)
			app.Commands = append(app.Commands, &cli.Command{
				Name:   "fail",
				Action: func(*cli.Context) error { return tt.err },
			})
			if got := run(context.Background(), app, []string{"great-machine", "fail"}); got != tt.want {
				t.Errorf("run returned %d, want %d", got, tt.want)
			}
			if got := stderr.String(); got != tt.stderr {
				t.Errorf("stderr is %q, want %q", got, tt.stderr)
			}
		})
	}
}

// TestRunBadFlagValue: a flag whose value cannot be read is a command
// line that is not valid, for a command that was added after newApp too.
func TestRunBadFlagValue(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr)
	app.Commands = append(app.Commands, &cli.Command{
		Name:   "count",
		Flags:  []cli.Flag{&cli.IntFlag{Name: "n"}},
		Action: func(*cli.Context) error { return nil },
	})
	if got := run(context.Background(), app, []string{"great-machine", "count", "--n", "many"}); got != 2 {
		t.Errorf("run returned %d, want 2; stderr: %s", got, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout is %q, want nothing", stdout.String())
	}
}

func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{{"great-machine"}, {"great-machine", "--help"}} {
		var stdout, stderr bytes.Buffer
		if got := Run(args, &stdout, &stderr); got != 0 {
			t.Errorf("Run(%q) returned %d, want 0", args, got)
		}
		if !bytes.Contains(stdout.Bytes(), []byte("version")) {
			t.Errorf("Run(%q): the help does not name the version command:\n%s", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("Run(%q): stderr is %q, want nothing", args, stderr.String())
		}
	}
}

// SIGHUP ends the context of a command as SIGTERM does: the reaper passes
// it on to zathras, which is to stop its service.
func TestSIGHUPEndsTheContext(t *testing.T) {
	// The test's own subscription keeps SIGHUP from ending the test
	// process, whatever signalContext does with it.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGHUP)
	defer signal.Stop(guard)
	ctx, stop := signalContext()
	defer stop()

	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("kill: %v", err)
	}
	<-guard // it has come
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the context of a command did not end with SIGHUP")
	}
}
