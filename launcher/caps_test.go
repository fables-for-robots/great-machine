package launcher

import (
	"slices"
	"testing"
)

// containerCaps is the bounding set of a container that was given nothing
// and had nothing taken: what CapBnd reads there.
const containerCaps = 0x00000000a80425fb

// capSysAdmin is the number of CAP_SYS_ADMIN, which the container of a
// zathras had up to stage 4 and has no more.
const capSysAdmin = 21

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
		{"with CAP_SYS_ADMIN", containerCaps | 1<<capSysAdmin, []string{"CAP_SYS_ADMIN"}},
		{"with two more", containerCaps | 1<<12 | 1<<19, []string{"CAP_NET_ADMIN", "CAP_SYS_PTRACE"}},
		{"a privileged container's", 0x000001ffffffffff, nil},
		{"one the kernel has and this program has no name for", containerCaps | 1<<45, []string{"capability 45"}},
	} {
		got := beyondDefault(c.bounding)
		if c.name == "a privileged container's" {
			// Every capability there is but the fourteen.
			if len(got) != 41-14 || !slices.Contains(got, "CAP_SYS_ADMIN") || slices.Contains(got, "CAP_CHOWN") || !slices.Contains(got, "CAP_SYS_MODULE") {
				t.Errorf("%s: beyondDefault = %q", c.name, got)
			}
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: beyondDefault(%#x) = %q, want %q", c.name, c.bounding, got, c.want)
		}
	}
}
