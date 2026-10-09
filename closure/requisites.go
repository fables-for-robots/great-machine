package closure

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// run runs a command and returns what it wrote to standard output. An
// error of the command carries what it wrote to standard error. The tests
// replace it.
var run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// The command was killed because ctx ended: that is the cause.
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return nil, fmt.Errorf("%w: %s", err, said)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// Of returns the store paths of the closure of storePath, sorted, by
// `nix-store --query --requisites`. The closure of a store path holds the
// store path itself.
func Of(ctx context.Context, storePath string) ([]string, error) {
	out, err := run(ctx, "nix-store", "--query", "--requisites", storePath)
	if err != nil {
		return nil, fmt.Errorf("nix-store --query --requisites %s: %w", storePath, err)
	}
	var paths []string
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, ok := strings.CutPrefix(line, storeDir+"/")
		if !ok || name == "" || strings.Contains(name, "/") {
			return nil, fmt.Errorf("nix-store --query --requisites %s: %q is no store path under %s", storePath, line, storeDir)
		}
		paths = append(paths, line)
	}
	slices.Sort(paths)
	return paths, nil
}

// Names returns the names of store paths: what follows /nix/store/.
func Names(storePaths []string) []string {
	names := make([]string, len(storePaths))
	for i, p := range storePaths {
		names[i] = strings.TrimPrefix(p, storeDir+"/")
	}
	return names
}
