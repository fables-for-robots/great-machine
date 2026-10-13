package wire

import (
	"bytes"
	"testing"
)

func TestStatementText(t *testing.T) {
	draal := ID("1111111111111111111111111111111111111111111111111111111111111111")
	zathras := ID("2222222222222222222222222222222222222222222222222222222222222222")
	want := "great-machine zathras statement 1\n" +
		"1111111111111111111111111111111111111111111111111111111111111111\n" +
		"2222222222222222222222222222222222222222222222222222222222222222\n"
	if got := string(StatementText(draal, zathras)); got != want {
		t.Errorf("the text of a statement is %q, want %q", got, want)
	}
	// It is of one draal and one zathras: either other makes another.
	if bytes.Equal(StatementText(draal, zathras), StatementText(zathras, draal)) {
		t.Error("the statement of a zathras for a draal is that of the draal for the zathras")
	}
}

func TestValidRole(t *testing.T) {
	for _, r := range []Role{RoleAdmin, RoleDeployer, RoleZathras} {
		if err := ValidRole(r); err != nil {
			t.Errorf("%q is refused: %v", r, err)
		}
	}
	for _, r := range []Role{"", "operator", "Admin", "root"} {
		err := ValidRole(r)
		if CodeOf(err) != CodeBadRequest {
			t.Errorf("%q: %v, want a bad-request", r, err)
		}
	}
}

func TestServiceOfStore(t *testing.T) {
	for name, want := range map[string]string{
		"/services/web/nix/store/abc-web":  "web",
		"/services/web/nix/store/":         "web", // the prefix of a listing
		"/services/a-1/nix/store/x/y":      "a-1",
		StorePrefix("api") + "0123-thing":  "api",
		"/services/web/nix/store/..":       "web", // the store says what a name is; this says whose
		"/services/web/nix/store/abc\x00d": "web",
	} {
		if got, ok := ServiceOfStore(name); !ok || got != want {
			t.Errorf("%q is of %q, %v; want of %q", name, got, ok, want)
		}
	}
	for _, name := range []string{
		"",
		"/",
		"/services/",
		"/services/web",           // shorter than the prefix of web: webshop is under it too
		"/services/web/",          //
		"/services/web/nix/store", // without the slash it is no prefix of a service
		"/services/we",
		"/services//nix/store/abc",
		"/services/Web/nix/store/abc", // no name of a service
		"/services/-web/nix/store/abc",
		"services/web/nix/store/abc",
		"/nix/store/abc",
		"/other/web/nix/store/abc",
		"/services/web/nix/storeroom/abc",
	} {
		if got, ok := ServiceOfStore(name); ok {
			t.Errorf("%q is taken to be of the service %q, and is under no service's prefix", name, got)
		}
	}
}

// A reader of before reads a message that has a field it does not know:
// a zathras that sends a statement registers with a draal that knows of
// none.
func TestAFieldTheReaderDoesNotKnowIsLeftOut(t *testing.T) {
	type newer struct {
		Service   string `json:"service"`
		Name      string `json:"name"`
		Statement []byte `json:"statement"`
		Later     struct {
			A int `json:"a"`
		} `json:"later"`
	}
	type older struct {
		Service string `json:"service"`
		Name    string `json:"name"`
	}
	var buf bytes.Buffer
	if err := Write(&buf, &newer{Service: "web", Name: "web-0", Statement: []byte{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	var got older
	if err := Read(&buf, &got); err != nil {
		t.Fatalf("reading a message with fields the reader does not know: %v", err)
	}
	if got.Service != "web" || got.Name != "web-0" {
		t.Errorf("read %+v", got)
	}
}
