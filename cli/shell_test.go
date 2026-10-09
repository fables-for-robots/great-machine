package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/urfave/cli/v2"
)

// TestShellResult: what the session came to decides the exit status of
// `great-machine shell`.
func TestShellResult(t *testing.T) {
	done, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name     string
		ctx      context.Context
		exit     int
		detached bool
		err      error
		status   int    // of what is returned; 0 for nil
		message  string // of what is returned
		stderr   string
	}{
		{"the shell ended well", context.Background(), 0, false, nil, 0, "", ""},
		{"the shell ended with a status", context.Background(), 3, false, nil, 3, "", ""},
		{"a signal ended the shell", context.Background(), -1, false, nil, 1, "the shell on web-a was ended by a signal", ""},
		{"detached", context.Background(), 0, true, nil, 0, "", "\ndetached\n"},
		{"the session failed", context.Background(), 0, false, errors.New("the stream ended"), 1, "the shell on web-a: the stream ended", ""},
		{"interrupted", done, 0, false, context.Canceled, 130, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr buffer
			err := shellResult(tt.ctx, "web-a", tt.exit, tt.detached, tt.err, &stderr)
			status, message := 0, ""
			var coder cli.ExitCoder
			switch {
			case errors.As(err, &coder):
				status, message = coder.ExitCode(), err.Error()
			case err != nil:
				status, message = 1, err.Error()
			}
			if status != tt.status || message != tt.message {
				t.Errorf("got the status %d and %q, want %d and %q", status, message, tt.status, tt.message)
			}
			if stderr.String() != tt.stderr {
				t.Errorf("stderr is %q, want %q", stderr.String(), tt.stderr)
			}
		})
	}
}
