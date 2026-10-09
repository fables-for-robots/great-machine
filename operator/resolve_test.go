package operator

import (
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/wire"
)

// Endpoint IDs of the test: 64 hexadecimal characters each.
var (
	idA = wire.ID("a1b2c3" + strings.Repeat("0", 58))
	idB = wire.ID("a1b2d4" + strings.Repeat("1", 58))
	idC = wire.ID("c0ffee" + strings.Repeat("2", 58))
	idD = wire.ID("deadbe" + strings.Repeat("3", 58))
	idE = wire.ID("beef00" + strings.Repeat("4", 58))
)

// TestResolve pins Review Focus 4: two zathrases of a service with the
// same name are listed apart, a command that names them fails and prints
// their endpoint IDs, and the beginning of an endpoint ID is accepted
// where a name is.
func TestResolve(t *testing.T) {
	fleet := wire.Fleet{Services: []wire.Service{
		{Name: "web", Zathrases: []wire.Zathras{
			{ID: idA, Name: "web-1"},
			{ID: idB, Name: "web-10"},
			{ID: idC, Name: "worker"},
			{ID: idD, Name: "twin"},
			{ID: idE, Name: "twin"},
		}},
		{Name: "api", Zathrases: []wire.Zathras{{ID: idC, Name: "api-0"}}},
		{Name: "empty"},
	}}
	all := "  web-1   " + string(idA) + "\n" +
		"  web-10  " + string(idB) + "\n" +
		"  worker  " + string(idC) + "\n" +
		"  twin    " + string(idD) + "\n" +
		"  twin    " + string(idE)

	cases := []struct {
		about   string
		service string
		name    string
		want    wire.ID // of the zathras, or "" for an error
		errHas  []string
		errNot  []string
	}{
		{about: "the exact name wins over a name it begins", service: "web", name: "web-1", want: idA},
		{about: "the exact name of the longer one", service: "web", name: "web-10", want: idB},
		{about: "a unique beginning of a name", service: "web", name: "wo", want: idC},
		{about: "one letter is enough for a name", service: "web", name: "t", errHas: []string{string(idD), string(idE)}, errNot: []string{string(idA)}},
		{about: "a unique beginning of an ID", service: "web", name: "c0ff", want: idC},
		{about: "the beginning of an ID in capitals", service: "web", name: "C0FFEE", want: idC},
		{about: "a whole ID", service: "web", name: string(idB), want: idB},
		{about: "the beginning of an ID that is too short", service: "web", name: "c0f",
			errHas: []string{`no zathras "c0f"`, all}},
		{about: "the beginning of two IDs", service: "web", name: "a1b2",
			errHas: []string{"web-1 ", "web-10", string(idA), string(idB)}, errNot: []string{"worker"}},
		{about: "the beginning of two names", service: "web", name: "web",
			errHas: []string{string(idA), string(idB)}, errNot: []string{"worker", "twin"}},
		{about: "no name with one zathras", service: "api", name: "", want: idC},
		{about: "no name with several", service: "web", name: "", errHas: []string{"5 zathrases", all}},
		{about: "two zathrases with the same name", service: "web", name: "twin",
			errHas: []string{`named "twin"`, "  twin  " + string(idD) + "\n  twin  " + string(idE)}, errNot: []string{"worker"}},
		{about: "the beginning of the ID of one twin", service: "web", name: "dead", want: idD},
		{about: "the beginning of the ID of the other twin", service: "web", name: "beef0", want: idE},
		{about: "no match", service: "web", name: "db", errHas: []string{`no zathras "db"`, all}},
		{about: "a service that is not in the fleet", service: "db", name: "", errHas: []string{`no service "db"`}},
		{about: "a service that is not in the fleet, with a name", service: "db", name: "web-1", errHas: []string{`no service "db"`}},
		{about: "a service without a zathras", service: "empty", name: "", errHas: []string{`"empty"`, "no zathras connected"}},
		{about: "a service without a zathras, with a name", service: "empty", name: "x", errHas: []string{"no zathras connected"}},
	}
	for _, tc := range cases {
		t.Run(tc.about, func(t *testing.T) {
			got, err := Resolve(fleet, tc.service, tc.name)
			if tc.want != "" {
				if err != nil || got.ID != tc.want {
					t.Fatalf("Resolve(%q, %q) = %s (%s), %v; want %s", tc.service, tc.name, got.Name, got.ID, err, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Resolve(%q, %q) = %s (%s), want an error", tc.service, tc.name, got.Name, got.ID)
			}
			for _, has := range tc.errHas {
				if !strings.Contains(err.Error(), has) {
					t.Errorf("the error lacks %q:\n%v", has, err)
				}
			}
			for _, not := range tc.errNot {
				if strings.Contains(err.Error(), not) {
					t.Errorf("the error has %q, which it should not:\n%v", not, err)
				}
			}
		})
	}
}
