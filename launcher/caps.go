package launcher

import "fmt"

// capSysAdmin is CAP_SYS_ADMIN, which a zathras mounts its store with and
// which nothing it starts is to have.
const capSysAdmin = 21

// capNames are the capabilities of Linux by their numbers.
var capNames = [...]string{
	0:  "CAP_CHOWN",
	1:  "CAP_DAC_OVERRIDE",
	2:  "CAP_DAC_READ_SEARCH",
	3:  "CAP_FOWNER",
	4:  "CAP_FSETID",
	5:  "CAP_KILL",
	6:  "CAP_SETGID",
	7:  "CAP_SETUID",
	8:  "CAP_SETPCAP",
	9:  "CAP_LINUX_IMMUTABLE",
	10: "CAP_NET_BIND_SERVICE",
	11: "CAP_NET_BROADCAST",
	12: "CAP_NET_ADMIN",
	13: "CAP_NET_RAW",
	14: "CAP_IPC_LOCK",
	15: "CAP_IPC_OWNER",
	16: "CAP_SYS_MODULE",
	17: "CAP_SYS_RAWIO",
	18: "CAP_SYS_CHROOT",
	19: "CAP_SYS_PTRACE",
	20: "CAP_SYS_PACCT",
	21: "CAP_SYS_ADMIN",
	22: "CAP_SYS_BOOT",
	23: "CAP_SYS_NICE",
	24: "CAP_SYS_RESOURCE",
	25: "CAP_SYS_TIME",
	26: "CAP_SYS_TTY_CONFIG",
	27: "CAP_MKNOD",
	28: "CAP_LEASE",
	29: "CAP_AUDIT_WRITE",
	30: "CAP_AUDIT_CONTROL",
	31: "CAP_SETFCAP",
	32: "CAP_MAC_OVERRIDE",
	33: "CAP_MAC_ADMIN",
	34: "CAP_SYSLOG",
	35: "CAP_WAKE_ALARM",
	36: "CAP_BLOCK_SUSPEND",
	37: "CAP_AUDIT_READ",
	38: "CAP_PERFMON",
	39: "CAP_BPF",
	40: "CAP_CHECKPOINT_RESTORE",
}

// defaultCaps are the capabilities a container has when it was given none
// and none was taken from it, with containerd as with Docker: CHOWN,
// DAC_OVERRIDE, FOWNER, FSETID, KILL, SETGID, SETUID, SETPCAP,
// NET_BIND_SERVICE, NET_RAW, SYS_CHROOT, MKNOD, AUDIT_WRITE and SETFCAP.
const defaultCaps uint64 = 1<<0 | 1<<1 | 1<<3 | 1<<4 | 1<<5 | 1<<6 | 1<<7 | 1<<8 |
	1<<10 | 1<<13 | 1<<18 | 1<<27 | 1<<29 | 1<<31

// beyondDefault returns the names of the capabilities of a bounding set
// that a container does not have by default, by their numbers.
func beyondDefault(bounding uint64) []string {
	var names []string
	for n := range 64 {
		if bounding&^defaultCaps&(1<<n) == 0 {
			continue
		}
		if n < len(capNames) {
			names = append(names, capNames[n])
		} else {
			names = append(names, fmt.Sprintf("capability %d", n))
		}
	}
	return names
}

// refusalOf returns why a launcher cannot be when CAP_SYS_ADMIN was to be
// dropped from the bounding set of its thread and dropErr came of it, and
// nil when it can be: the capability was dropped, or the processes do not
// run as root, and then have no capability whatever the set holds.
func refusalOf(dropErr error, asRoot bool) error {
	if dropErr == nil || !asRoot {
		return nil
	}
	return fmt.Errorf("launcher: the service and the shells would have CAP_SYS_ADMIN, which this process mounts with: "+
		"it could not be dropped from what they are started with (%w), which takes CAP_SETPCAP. "+
		"Give the container CAP_SETPCAP, which it has unless it was taken away, or run them as a user that is not root, with --user", dropErr)
}
