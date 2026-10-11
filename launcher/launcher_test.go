package launcher

import (
	"strings"
	"testing"
)

func TestReplaceIsForItsArgumentAlone(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"great-machine"},
		{"great-machine", "zathras"},
		{"great-machine", "zathras", Arg, "", "/bin/true", "true"},
		{Arg},
	} {
		if status, was := Replace(args); status != 0 || was {
			t.Errorf("Replace(%q) = %d, %v; want 0, false", args, status, was)
		}
	}
}

// Started with Arg and no program to become, the program says what it
// wants and ends as for a program that could not be executed.
func TestReplaceWithoutAProgram(t *testing.T) {
	for _, args := range [][]string{
		{"great-machine", Arg},
		{"great-machine", Arg, ""},
		{"great-machine", Arg, "", "/bin/true"},
	} {
		if status, was := Replace(args); status != 126 || !was {
			t.Errorf("Replace(%q) = %d, %v; want 126, true", args, status, was)
		}
	}
}

func TestParseUser(t *testing.T) {
	for text, want := range map[string]User{
		"0":           {0, 0},
		"1000":        {1000, 1000},
		"1000:100":    {1000, 100},
		"65534:65534": {65534, 65534},
		"0:0":         {0, 0},
		"4294967294":  {4294967294, 4294967294},
	} {
		got, err := ParseUser(text)
		if err != nil || got != want {
			t.Errorf("ParseUser(%q) = %+v, %v; want %+v", text, got, err, want)
		}
	}
	for _, text := range []string{
		"", ":", "root", "nobody:nogroup", "-1", "1:-1", "1:", ":1", "1:2:3",
		"1.5", "0x10", " 1", "1 ", "1: 2", "+1",
		"4294967295",   // (uid_t)-1, which is no user
		"1:4294967295", //
		"4294967296",   // more than an ID holds
		"99999999999999999999",
	} {
		got, err := ParseUser(text)
		if err == nil {
			t.Errorf("ParseUser(%q) = %+v, want an error", text, got)
			continue
		}
		if !strings.Contains(err.Error(), `"`+text+`"`) {
			t.Errorf("ParseUser(%q): the error does not quote what was given: %v", text, err)
		}
	}
}
