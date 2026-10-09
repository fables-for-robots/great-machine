package wire

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestValidService(t *testing.T) {
	valid := []string{
		"a",
		"web",
		"web-2",
		"a1",
		"1a",
		"a--b",
		strings.Repeat("a", 63),
	}
	for _, name := range valid {
		if err := ValidService(name); err != nil {
			t.Errorf("ValidService(%q): %v", name, err)
		}
	}

	invalid := []string{
		"",
		"Web",
		"-a",
		"a-",
		"-",
		"a_b",
		"a.b",
		"a/b",
		"../x",
		"a b",
		"wéb",
		"web\n",
		strings.Repeat("a", 64),
	}
	for _, name := range invalid {
		err := ValidService(name)
		var refusal *Error
		if !errors.As(err, &refusal) {
			t.Errorf("ValidService(%q): got %v, want a *wire.Error", name, err)
			continue
		}
		if refusal.Code != CodeBadRequest {
			t.Errorf("ValidService(%q): code %q, want %q", name, refusal.Code, CodeBadRequest)
		}
		if !strings.Contains(refusal.Message, strconv.Quote(name)) {
			t.Errorf("ValidService(%q): the message does not quote the name: %s", name, refusal.Message)
		}
	}
}
