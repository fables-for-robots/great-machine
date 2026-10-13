package operator

import (
	"reflect"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

func TestGrants(t *testing.T) {
	want := []wire.Grant{
		{ID: nodetest.ID(1), Role: wire.RoleAdmin, Fixed: true},
		{ID: nodetest.ID(2), Role: wire.RoleDeployer, Service: "web", Note: "ci", By: nodetest.ID(1), At: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)},
	}
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		if req.Grants == nil {
			refuse(s, wire.CodeBadRequest, "not a request for the grants")
			return
		}
		answer(s, &wire.Response{Grants: &wire.GrantList{Grants: want}})
	})
	got, err := c.Grants(t.Context())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Grants = %+v, %v; want %+v", got, err, want)
	}
}

func TestGrantAndRevoke(t *testing.T) {
	asked := make(chan wire.Request, 2)
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		asked <- *req
		switch {
		case req.Grant != nil:
			answer(s, &wire.Response{Granted: &wire.Granted{}})
		case req.Revoke != nil:
			answer(s, &wire.Response{Revoked: &wire.Revoked{}})
		default:
			refuse(s, wire.CodeBadRequest, "neither a grant nor a revocation")
		}
	})
	grant := wire.GrantRequest{ID: nodetest.ID(2), Role: wire.RoleDeployer, Service: "web", Note: "ci"}
	if err := c.Grant(t.Context(), grant); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if got := wait(t, "the grant", asked).Grant; got == nil || *got != grant {
		t.Errorf("draal was asked to grant %+v, want %+v", got, grant)
	}
	revoke := wire.RevokeRequest{ID: nodetest.ID(2), Role: wire.RoleDeployer, Service: "web"}
	if err := c.Revoke(t.Context(), revoke); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got := wait(t, "the revocation", asked).Revoke; got == nil || *got != revoke {
		t.Errorf("draal was asked to revoke %+v, want %+v", got, revoke)
	}
}

func TestARefusedGrantIsTheErrorOfDraal(t *testing.T) {
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		refuse(s, wire.CodeUnauthorized, "no admin")
	})
	err := c.Grant(t.Context(), wire.GrantRequest{ID: nodetest.ID(2), Role: wire.RoleAdmin})
	if wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Fatalf("Grant = %v, want the refusal of draal", err)
	}
	if err := c.Revoke(t.Context(), wire.RevokeRequest{ID: nodetest.ID(2), Role: wire.RoleAdmin}); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Fatalf("Revoke = %v, want the refusal of draal", err)
	}
}

func TestMe(t *testing.T) {
	want := wire.Identity{ID: nodetest.ID(4), Grants: []wire.Grant{{ID: nodetest.ID(4), Role: wire.RoleDeployer, Service: "web"}}}
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		if req.Me == nil {
			refuse(s, wire.CodeBadRequest, "not who am I")
			return
		}
		answer(s, &wire.Response{Me: &want})
	})
	got, err := c.Me(t.Context())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Me = %+v, %v; want %+v", got, err, want)
	}
}

func TestStorePage(t *testing.T) {
	want := wire.StorePage{Status: 200, Type: "application/json", Body: []byte(`{"refs":3}`)}
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		if req.StorePage == nil || req.StorePage.Path != "/api/stats?x=1" {
			refuse(s, wire.CodeBadRequest, "not the page of the store")
			return
		}
		answer(s, &wire.Response{StorePage: &want})
	})
	got, err := c.StorePage(t.Context(), "/api/stats?x=1")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("StorePage = %+v, %v; want %+v", got, err, want)
	}
}
