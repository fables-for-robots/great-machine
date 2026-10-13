package operator

import (
	"context"

	"github.com/fables-for-robots/great-machine/wire"
)

// Grants returns every grant draal has, the admins of its command line
// among them. It is an admin's to ask.
func (c *Client) Grants(ctx context.Context) ([]wire.Grant, error) {
	resp, err := c.call(ctx, "grants", &wire.Request{Grants: &wire.GrantsRequest{}})
	if err != nil {
		return nil, err
	}
	if resp.Grants == nil {
		return nil, unexpected("grants")
	}
	return resp.Grants.Grants, nil
}

// Grant gives a key a role. It is an admin's to ask.
func (c *Client) Grant(ctx context.Context, req wire.GrantRequest) error {
	resp, err := c.call(ctx, "grant", &wire.Request{Grant: &req})
	if err != nil {
		return err
	}
	if resp.Granted == nil {
		return unexpected("grant")
	}
	return nil
}

// Revoke takes a grant away. draal has ended what the grant was used for
// when it answers. It is an admin's to ask.
func (c *Client) Revoke(ctx context.Context, req wire.RevokeRequest) error {
	resp, err := c.call(ctx, "revoke", &wire.Request{Revoke: &req})
	if err != nil {
		return err
	}
	if resp.Revoked == nil {
		return unexpected("revoke")
	}
	return nil
}

// Me returns who the key of this client is to draal: its endpoint ID and
// what it was granted. Every key may ask, one with no grant too.
func (c *Client) Me(ctx context.Context) (wire.Identity, error) {
	resp, err := c.call(ctx, "me", &wire.Request{Me: &wire.MeRequest{}})
	if err != nil {
		return wire.Identity{}, err
	}
	if resp.Me == nil {
		return wire.Identity{}, unexpected("me")
	}
	return *resp.Me, nil
}

// StorePage returns what the page of the store answers a GET of path
// with. It is an admin's to ask.
func (c *Client) StorePage(ctx context.Context, path string) (wire.StorePage, error) {
	resp, err := c.call(ctx, "store-page", &wire.Request{StorePage: &wire.StorePageRequest{Path: path}})
	if err != nil {
		return wire.StorePage{}, err
	}
	if resp.StorePage == nil {
		return wire.StorePage{}, unexpected("store-page")
	}
	return *resp.StorePage, nil
}
