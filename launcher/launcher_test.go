package launcher

import (
	"bytes"
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

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

// With a user, a command is given the user and the group, and no other
// group, and keeps what else it was to be started with. Nothing is
// started here, so this is tested by whoever runs the tests.
func TestStartGivesTheCommandTheUser(t *testing.T) {
	l := &Launcher{user: &User{UID: 65534, GID: 65533}, log: quiet}
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	failed := errors.New("it was not started")
	if err := l.Start(cmd, func() error { return failed }); err != failed {
		t.Errorf("Start = %v, want what start returned", err)
	}
	cred := cmd.SysProcAttr.Credential
	if cred == nil || cred.Uid != 65534 || cred.Gid != 65533 || len(cred.Groups) != 0 || cred.NoSetGroups {
		t.Errorf("the command is to run as %+v, want user 65534 and group 65533, with its other groups set to none", cred)
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Error("the command lost the process group it was to have")
	}

	// A command that was to be started with nothing special is given the
	// user all the same.
	plain := exec.Command("/bin/sh")
	l.Start(plain, func() error { return nil })
	if plain.SysProcAttr == nil || plain.SysProcAttr.Credential == nil || plain.SysProcAttr.Credential.Uid != 65534 {
		t.Errorf("a command without attributes of its own is to run with %+v, want user 65534", plain.SysProcAttr)
	}
}

// Whose processes run as root: those of a launcher without a user in a
// process that is root, and those of a launcher whose user is root.
func TestAsRoot(t *testing.T) {
	for _, c := range []struct {
		name string
		user *User
		euid int
		want bool
	}{
		{"no user, in a process that is root", nil, 0, true},
		{"no user, in a process that is not", nil, 1000, false},
		{"another user, in a process that is root", &User{UID: 65534, GID: 65534}, 0, false},
		{"root with another group, in a process that is root", &User{UID: 0, GID: 100}, 0, true},
	} {
		if got := asRoot(c.user, c.euid); got != c.want {
			t.Errorf("%s: asRoot = %v, want %v", c.name, got, c.want)
		}
	}
}

// The warning names every capability that a container does not have by
// default, and --user. A pod that was left with CAP_SYS_ADMIN, which a
// zathras mounted with up to stage 4, is told so: nothing drops it any
// more, and a service that is root has it.
func TestTheWarningNamesWhatIsBeyondAContainersDefault(t *testing.T) {
	warned := func(bounding uint64) string {
		var log bytes.Buffer
		(&Launcher{log: slog.New(slog.NewTextHandler(&log, nil))}).warn(bounding)
		return log.String()
	}
	if got := warned(containerCaps); got != "" {
		t.Errorf("a launcher warned of a container's own capabilities: %s", got)
	}
	if got := warned(0); got != "" {
		t.Errorf("a launcher warned of no capability at all: %s", got)
	}
	got := warned(containerCaps | 1<<capSysAdmin)
	for _, word := range []string{"level=WARN", "CAP_SYS_ADMIN", "--user"} {
		if !strings.Contains(got, word) {
			t.Errorf("the warning for a container with CAP_SYS_ADMIN does not have %s: %s", word, got)
		}
	}
	if strings.Contains(got, "CAP_CHOWN") {
		t.Errorf("the warning names a capability every container has: %s", got)
	}
	got = warned(containerCaps | 1<<12 | 1<<19)
	if !strings.Contains(got, "CAP_NET_ADMIN CAP_SYS_PTRACE") {
		t.Errorf("the warning for two capabilities more does not name them: %s", got)
	}
}
