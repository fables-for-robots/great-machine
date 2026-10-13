package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/fables-for-robots/great-machine/human"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// accessCommand is `great-machine access`: who may do what. It is an
// admin's.
func accessCommand() *cli.Command {
	service := func() cli.Flag {
		return &cli.StringFlag{Name: "service", Usage: "`NAME` of the service the role is of (default: every service)"}
	}
	return &cli.Command{
		Name:  "access",
		Usage: "list, grant and revoke who may do what: an admin's",
		Description: "A role is granted to a key, by its endpoint ID, for a service or for\n" +
			"every service, and a key has as many grants as it was given:\n\n" +
			"   admin      does everything, and is always of every service\n" +
			"   deployer   pushes and rolls out, and looks at what it rolled out\n" +
			"   zathras    is for the role key of a service, which signs for its pods\n\n" +
			"`great-machine id --key FILE` prints the endpoint ID of a key.",
		Subcommands: []*cli.Command{
			{
				Name:  "list",
				Usage: "print every grant",
				Flags: operatorFlags(),
				Action: func(c *cli.Context) error {
					if err := noArguments(c); err != nil {
						return err
					}
					client, _, err := connect(c.Context, c)
					if err != nil {
						return err
					}
					defer client.Close()
					return runAccessList(c.Context, client, c.App.Writer, time.Now())
				},
			},
			{
				Name:      "grant",
				Usage:     "give a key a role",
				ArgsUsage: "ROLE ID",
				Flags: operatorFlags(service(),
					&cli.StringFlag{Name: "note", Usage: "`TEXT` that is kept beside the grant, such as whose key it is"}),
				Action: func(c *cli.Context) error {
					id, role, of, err := readGrant(c)
					if err != nil {
						return err
					}
					client, _, err := connect(c.Context, c)
					if err != nil {
						return err
					}
					defer client.Close()
					return runAccessGrant(c.Context, client, wire.GrantRequest{ID: id, Role: role, Service: of, Note: c.String("note")}, c.App.Writer)
				},
			},
			{
				Name:      "revoke",
				Usage:     "take a grant away, and end what it was used for",
				ArgsUsage: "ROLE ID",
				Flags:     operatorFlags(service()),
				Action: func(c *cli.Context) error {
					id, role, of, err := readGrant(c)
					if err != nil {
						return err
					}
					client, _, err := connect(c.Context, c)
					if err != nil {
						return err
					}
					defer client.Close()
					return runAccessRevoke(c.Context, client, wire.RevokeRequest{ID: id, Role: role, Service: of}, c.App.Writer)
				},
			},
		},
	}
}

// readGrant reads ROLE, ID and --service of grant and of revoke, and
// --draal with them, before anything is dialed.
func readGrant(c *cli.Context) (wire.ID, wire.Role, string, error) {
	args, err := arguments(c, 2, "ROLE", "ID")
	if err != nil {
		return "", "", "", err
	}
	role := wire.Role(args[0])
	if err := wire.ValidRole(role); err != nil {
		return "", "", "", usageError("%s", err.(*wire.Error).Message)
	}
	id, err := node.ParseID(args[1])
	if err != nil {
		return "", "", "", usageError("%q is not an endpoint ID, as `great-machine id` prints one: %w", args[1], err)
	}
	of := c.String("service")
	if of != "" {
		if role == wire.RoleAdmin {
			return "", "", "", usageError("an admin is of every service: --service is for a deployer and a zathras")
		}
		if of, err = service(of); err != nil {
			return "", "", "", err
		}
	}
	if _, err := draalID(c); err != nil {
		return "", "", "", err
	}
	return id, role, of, nil
}

// shortID is the beginning of an endpoint ID, as the tables have it.
func shortID(id wire.ID) string {
	if len(id) > 8 {
		return string(id[:8])
	}
	return string(id)
}

// of says what a role is of: a service, or every one. An admin is of
// nothing in particular.
func of(role wire.Role, service string) string {
	switch {
	case role == wire.RoleAdmin:
		return ""
	case service == "":
		return " of every service"
	}
	return " of " + service
}

// article is the role with its article: "an admin", "a deployer".
func article(role wire.Role) string {
	if role == wire.RoleAdmin {
		return "an " + string(role)
	}
	return "a " + string(role)
}

// runAccessList prints every grant: the role, the service or "every", the
// ID, the note, who granted it and how long ago.
func runAccessList(ctx context.Context, client *operator.Client, stdout io.Writer, now time.Time) error {
	grants, err := client.Grants(ctx)
	if err != nil {
		return err
	}
	if len(grants) == 0 {
		_, err := io.WriteString(stdout, "no grants: draal was given no admin, and nobody was granted anything\n")
		return err
	}
	rows := []string{"ROLE\tSERVICE\tID\tNOTE\tGRANTED BY\tWHEN"}
	for _, g := range grants {
		service, note, by, when := g.Service, g.Note, shortID(g.By), human.Ago(g.At, now)
		if service == "" {
			service = "every"
		}
		if note == "" {
			note = human.None
		}
		if g.Fixed {
			by, when = "the command line", human.None
		}
		rows = append(rows, fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s", g.Role, service, g.ID, note, by, when))
	}
	_, err = io.WriteString(stdout, table(rows))
	return err
}

// runAccessGrant grants, and says what was granted.
func runAccessGrant(ctx context.Context, client *operator.Client, req wire.GrantRequest, stdout io.Writer) error {
	if err := client.Grant(ctx, req); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "granted: %s is %s%s\n", req.ID, article(req.Role), of(req.Role, req.Service))
	return err
}

// runAccessRevoke revokes, and says what was revoked. draal has ended
// what the grant was used for when it has answered.
func runAccessRevoke(ctx context.Context, client *operator.Client, req wire.RevokeRequest, stdout io.Writer) error {
	if err := client.Revoke(ctx, req); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "revoked: %s is no %s%s any more, and what it was let in for is ended\n", req.ID, req.Role, of(req.Role, req.Service))
	return err
}
