package db

import (
	"context"
	"fmt"

	"github.com/fables-for-robots/great-machine/db/dbq"
	"github.com/fables-for-robots/great-machine/wire"
)

// Grants returns every grant, by role, then by service, then by ID.
func (d *DB) Grants(ctx context.Context) ([]wire.Grant, error) {
	var out []wire.Grant
	err := d.read(ctx, func(q *dbq.Queries) error {
		rows, err := q.ListGrants(ctx)
		if err != nil {
			return err
		}
		out = make([]wire.Grant, len(rows))
		for i, r := range rows {
			out[i] = wire.Grant{
				ID:      wire.ID(r.ID),
				Role:    wire.Role(r.Role),
				Service: r.Service,
				Note:    r.Note,
				By:      wire.ID(r.CreatedBy),
				At:      timeOf(r.CreatedAt),
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("db: the grants: %w", err)
	}
	return out, nil
}

// PutGrant records a grant. One that is there already, with the ID, the
// role and the service of g, is there with the note of g, and is as old
// as it was: who granted it first, and when, stay.
func (d *DB) PutGrant(ctx context.Context, g wire.Grant) error {
	err := d.write(ctx, func(q *dbq.Queries) error {
		return q.PutGrant(ctx, dbq.PutGrantParams{
			ID:        string(g.ID),
			Role:      string(g.Role),
			Service:   g.Service,
			Note:      g.Note,
			CreatedAt: g.At.UnixNano(),
			CreatedBy: string(g.By),
		})
	})
	if err != nil {
		return fmt.Errorf("db: granting %s to %s: %w", g.Role, g.ID, err)
	}
	return nil
}

// DeleteGrant removes the grant with the ID, the role and the service,
// and returns ErrNotFound when there is none.
func (d *DB) DeleteGrant(ctx context.Context, id wire.ID, role wire.Role, service string) error {
	err := d.write(ctx, func(q *dbq.Queries) error {
		n, err := q.DeleteGrant(ctx, dbq.DeleteGrantParams{ID: string(id), Role: string(role), Service: service})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("db: revoking %s of %s: %w", role, id, err)
	}
	return nil
}
