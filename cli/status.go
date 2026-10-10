package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// statusCommand is `great-machine status [SERVICE]`.
func statusCommand() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "list the services, or the zathrases of one",
		ArgsUsage: "[SERVICE]",
		Flags:     operatorFlags(),
		Action: func(c *cli.Context) error {
			args, err := arguments(c, 0, "SERVICE")
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				if name, err = service(args[0]); err != nil {
					return err
				}
			}
			client, _, err := connect(c.Context, c)
			if err != nil {
				return err
			}
			defer client.Close()
			return runStatus(c.Context, client, name, c.App.Writer, time.Now())
		},
	}
}

// runStatus prints the services of the fleet, or with a service its
// zathrases, as they are now.
func runStatus(ctx context.Context, client *operator.Client, service string, stdout io.Writer, now time.Time) error {
	f, err := fleet(ctx, client)
	if err != nil {
		return err
	}
	text := formatFleet(f)
	if service != "" {
		if text, err = formatService(f, service, now); err != nil {
			return err
		}
	}
	_, err = io.WriteString(stdout, text)
	return err
}

// table writes rows as columns two spaces apart. A row is its cells with
// a tab between them.
func table(rows []string) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 8, 2, ' ', 0)
	for _, row := range rows {
		fmt.Fprintln(w, row)
	}
	w.Flush()
	return b.String()
}

// formatFleet is the table of services: the name, the stable generation,
// the zathrases counted by phase, and the last rollout.
func formatFleet(f wire.Fleet) string {
	if len(f.Services) == 0 {
		return "no services: no zathras is connected and nothing was rolled out\n"
	}
	rows := []string{"SERVICE\tSTABLE\tZATHRASES\tLAST ROLLOUT"}
	for _, s := range f.Services {
		rows = append(rows, strings.Join([]string{s.Name, human.Generation(s.Stable), phases(s.Zathrases), rolloutLine(s)}, "\t"))
	}
	return table(rows)
}

// formatService is the last rollout of a service on a line and its
// zathrases as a table: name, the first 8 characters of the ID, phase,
// generation, for how long it has been running or when it is started
// again, restarts, and CPU and memory where there is a sample.
func formatService(f wire.Fleet, name string, now time.Time) (string, error) {
	var s *wire.Service
	var names []string
	for i := range f.Services {
		names = append(names, f.Services[i].Name)
		if f.Services[i].Name == name {
			s = &f.Services[i]
		}
	}
	switch {
	case s == nil && len(names) == 0:
		return "", fmt.Errorf("no service %q: draal has no services", name)
	case s == nil:
		return "", fmt.Errorf("no service %q; there are: %s", name, strings.Join(names, ", "))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: stable generation %s, last rollout %s\n", s.Name, human.Generation(s.Stable), rolloutLine(*s))
	if r := s.Rollout; r != nil {
		fmt.Fprintf(&b, "rollout #%d: to generation %d, %s at a time, begun %s ago\n",
			r.ID, r.Generation, atATime(r.Parallel), human.Age(now.Sub(r.Created)))
	}
	if len(s.Zathrases) == 0 {
		b.WriteString("no zathras is connected\n")
		return b.String(), nil
	}
	rows := []string{"NAME\tID\tPHASE\tGENERATION\tSINCE\tRESTARTS\tCPU\tMEMORY"}
	for _, z := range s.Zathrases {
		st := z.State
		cpu, memory := "-", "-"
		if st.Sample != nil {
			cpu = strconv.FormatFloat(st.Sample.CPU, 'f', 2, 64)
			memory = human.Bytes(st.Sample.Memory)
			if st.Sample.MemoryLimit > 0 {
				memory += " of " + human.Bytes(st.Sample.MemoryLimit)
			}
		}
		id := string(z.ID)
		if len(id) > 8 {
			id = id[:8]
		}
		rows = append(rows, strings.Join([]string{
			z.Name, id, string(st.Phase), human.Generation(st.Generation), since(st, now),
			strconv.FormatUint(st.Restarts, 10), cpu, memory,
		}, "\t"))
	}
	b.WriteString(table(rows))
	return b.String(), nil
}

// atATime writes the parallel of a rollout.
func atATime(parallel int) string {
	if parallel == 0 {
		return "all"
	}
	return strconv.Itoa(parallel)
}

// phases counts zathrases by phase: "3 running, 1 backoff".
func phases(zs []wire.Zathras) string {
	if len(zs) == 0 {
		return "none"
	}
	count := map[wire.Phase]int{}
	for _, z := range zs {
		count[z.State.Phase]++
	}
	var parts []string
	for _, p := range []wire.Phase{wire.PhaseRunning, wire.PhaseStopping, wire.PhaseBackoff, wire.PhaseIdle} {
		if n := count[p]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, p))
		}
	}
	return strings.Join(parts, ", ")
}

// rolloutLine writes the last rollout of a service: "#12 completed",
// "#13 activating 2/5", "#13 halted: <reason>", or - for none. The counts
// are of the zathrases that are connected.
func rolloutLine(s wire.Service) string {
	r := s.Rollout
	if r == nil {
		return "-"
	}
	switch r.State {
	case wire.RolloutPreparing:
		return fmt.Sprintf("#%d preparing %d/%d", r.ID, r.Prepared, len(s.Zathrases))
	case wire.RolloutActivating:
		return fmt.Sprintf("#%d activating %d/%d", r.ID, r.Activated, len(s.Zathrases))
	case wire.RolloutHalted:
		return fmt.Sprintf("#%d halted: %s", r.ID, r.Reason)
	}
	return fmt.Sprintf("#%d %s", r.ID, r.State)
}

// since writes for how long the service of a zathras has been running,
// or when it is started again.
func since(st wire.State, now time.Time) string {
	switch st.Phase {
	case wire.PhaseRunning:
		return "up " + human.Age(st.RunningFor)
	case wire.PhaseBackoff:
		if wait := st.RestartAt.Sub(now); !st.RestartAt.IsZero() && wait > 0 {
			return "restart in " + human.Age(wait)
		}
		return "restart now"
	}
	return "-"
}
