package operator

import (
	"fmt"
	"strings"

	"github.com/fables-for-robots/great-machine/wire"
)

// MinIDPrefix is the least number of characters by which the beginning of
// an endpoint ID names a zathras. Anything shorter is taken as the
// beginning of a name alone, so that a few letters meant as a name never
// pick a zathras by its ID.
const MinIDPrefix = 4

// Resolve finds the zathras of a service that name names: the one whose
// name is name; or else the one whose name, or whose ID, begins with
// name; or, with name "", the only one. The beginning of an ID counts
// from MinIDPrefix characters on, in either case of its letters. Any
// other outcome is an error that lists the zathrases of the service, or
// those that name matches when there are several, one on a line with
// name and ID, so that it can be printed as it is.
func Resolve(f wire.Fleet, service, name string) (wire.Zathras, error) {
	var all []wire.Zathras
	found := false
	for _, svc := range f.Services {
		if svc.Name == service {
			all, found = svc.Zathrases, true
		}
	}
	if !found {
		return wire.Zathras{}, fmt.Errorf("draal knows no service %q", service)
	}
	if len(all) == 0 {
		return wire.Zathras{}, fmt.Errorf("service %q has no zathras connected", service)
	}
	if name == "" {
		if len(all) == 1 {
			return all[0], nil
		}
		return wire.Zathras{}, fmt.Errorf("service %q has %d zathrases; name one of them:\n%s", service, len(all), listing(all))
	}

	var exact, begins []wire.Zathras
	id := strings.ToLower(name)
	for _, z := range all {
		if z.Name == name {
			exact = append(exact, z)
		}
		if strings.HasPrefix(z.Name, name) || (len(name) >= MinIDPrefix && strings.HasPrefix(string(z.ID), id)) {
			begins = append(begins, z)
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(exact) > 1:
		return wire.Zathras{}, fmt.Errorf("%d zathrases of service %q are named %q; name one by the beginning of its ID:\n%s",
			len(exact), service, name, listing(exact))
	case len(begins) == 1:
		return begins[0], nil
	case len(begins) > 1:
		return wire.Zathras{}, fmt.Errorf("%q is the beginning of %d zathrases of service %q:\n%s",
			name, len(begins), service, listing(begins))
	}
	return wire.Zathras{}, fmt.Errorf("service %q has no zathras %q; it has:\n%s", service, name, listing(all))
}

// listing writes zathrases one on a line, with name and ID.
func listing(zs []wire.Zathras) string {
	width := 0
	for _, z := range zs {
		width = max(width, len(z.Name))
	}
	var b strings.Builder
	for i, z := range zs {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "  %-*s  %s", width, z.Name, z.ID)
	}
	return b.String()
}
