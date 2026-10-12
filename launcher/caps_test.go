package launcher

import (
	"errors"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// containerCaps is the bounding set of a container that was given nothing
// and had nothing taken: what CapBnd reads there.
const containerCaps = 0x00000000a80425fb

func TestTheCapabilitiesAContainerHasByDefault(t *testing.T) {
	if defaultCaps != containerCaps {
		t.Errorf("defaultCaps is %#x, want %#x", defaultCaps, uint64(containerCaps))
	}
	if defaultCaps&(1<<capSysAdmin) != 0 {
		t.Error("CAP_SYS_ADMIN is among the capabilities of a container's default")
	}
}

func TestBeyondDefault(t *testing.T) {
	for _, c := range []struct {
		name     string
		bounding uint64
		want     []string
	}{
		{"a container's own", containerCaps, nil},
		{"fewer than a container has", 0x00000000000004fb, nil},
		{"none", 0, nil},
		{"with CAP_SYS_ADMIN, which was not dropped", containerCaps | 1<<capSysAdmin, []string{"CAP_SYS_ADMIN"}},
		{"with two more", containerCaps | 1<<12 | 1<<19, []string{"CAP_NET_ADMIN", "CAP_SYS_PTRACE"}},
		{"a privileged container's, without CAP_SYS_ADMIN", 0x000001ffffffffff &^ (1 << capSysAdmin), nil},
		{"one the kernel has and this program has no name for", containerCaps | 1<<45, []string{"capability 45"}},
	} {
		got := beyondDefault(c.bounding)
		if c.name == "a privileged container's, without CAP_SYS_ADMIN" {
			// Every capability there is but the fourteen and CAP_SYS_ADMIN.
			if len(got) != 41-14-1 || slices.Contains(got, "CAP_SYS_ADMIN") || slices.Contains(got, "CAP_CHOWN") || !slices.Contains(got, "CAP_SYS_MODULE") {
				t.Errorf("%s: beyondDefault = %q", c.name, got)
			}
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: beyondDefault(%#x) = %q, want %q", c.name, c.bounding, got, c.want)
		}
	}
}

// What comes of a CAP_SYS_ADMIN that could not be dropped: a launcher
// whose processes run as root cannot be; one whose processes run as
// another user can, for they have no capability whatever the set holds.
func TestRefusal(t *testing.T) {
	if err := refusalOf(nil, true); err != nil {
		t.Errorf("dropped, as root: refused: %v", err)
	}
	if err := refusalOf(nil, false); err != nil {
		t.Errorf("dropped, as another user: refused: %v", err)
	}
	if err := refusalOf(syscall.EPERM, false); err != nil {
		t.Errorf("not dropped, as another user: refused: %v", err)
	}
	err := refusalOf(syscall.EPERM, true)
	if err == nil {
		t.Fatal("not dropped, as root: not refused")
	}
	if !errors.Is(err, syscall.EPERM) {
		t.Errorf("the refusal does not hold why the capability was not dropped: %v", err)
	}
	for _, word := range []string{"CAP_SYS_ADMIN", "CAP_SETPCAP", "--user"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the refusal does not name %s: %v", word, err)
		}
	}
}
