package tui

import "context"

// RunDrawn is Run for the tests of the packages that run the terminal UI
// as a whole: drawn is called with every view, as it is handed to the
// renderer. That is what a person reads on the screen once the terminal
// has caught up. What the renderer writes to the terminal is no screen:
// it writes what has changed since the last frame, in pieces, and only a
// terminal emulator could say what that adds up to.
//
// drawn is called from the goroutine that draws, one call at a time, and
// must not wait.
func RunDrawn(ctx context.Context, cfg Config, drawn func(view string)) error {
	return run(ctx, cfg, drawn)
}
