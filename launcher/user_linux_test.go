package launcher

import (
	"bufio"
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// root skips a test in a process that is not root: only root starts a
// process as another user.
func root(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("another user takes root: scripts/test-linux.sh runs this as root, in a container")
	}
}

// reportingSelf is reporting for a child that may run as another user.
// The test binary lies in a directory of the user that runs the tests,
// where another user does not reach it by its path; the link reaches the
// file whoever asks.
func reportingSelf() *exec.Cmd {
	cmd := exec.Command("/proc/self/exe")
	cmd.Env = append(os.Environ(), childEnv+"=report")
	return cmd
}

// set reads a set of capabilities as /proc has it.
func set(t *testing.T, status map[string]string, name string) uint64 {
	t.Helper()
	v, err := strconv.ParseUint(status[name], 16, 64)
	if err != nil {
		t.Fatalf("%s is %q in the status: %v", name, status[name], err)
	}
	return v
}

// statusOfThisProcess reads the fields of /proc/self/status.
func statusOfThisProcess(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	status := make(map[string]string)
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		if k, v, ok := strings.Cut(lines.Text(), ":"); ok {
			status[k] = strings.TrimSpace(v)
		}
	}
	return status
}

// As another user a process has the user, the group and no other group,
// and with them no capability: a process that is not root has none once
// it has executed a program, whatever the container was given.
func TestWhatIsStartedAsAnotherUser(t *testing.T) {
	root(t)
	l := newLauncher(t, Config{User: &User{UID: 65534, GID: 65533}})
	r, _, status := run(t, l, reportingSelf())
	if status != 0 {
		t.Fatalf("the child ended with %d", status)
	}
	if r.Uid != 65534 || r.Gid != 65533 || len(r.Groups) != 0 {
		t.Errorf("the child runs as %d:%d with the groups %v, want 65534:65533 and no other group", r.Uid, r.Gid, r.Groups)
	}
	// The real, effective, saved and file system IDs alike.
	if want := "65534\t65534\t65534\t65534"; r.Status["Uid"] != want {
		t.Errorf("Uid of the child is %q, want %q", r.Status["Uid"], want)
	}
	if want := "65533\t65533\t65533\t65533"; r.Status["Gid"] != want {
		t.Errorf("Gid of the child is %q, want %q", r.Status["Gid"], want)
	}
	for _, name := range []string{"CapPrm", "CapEff", "CapAmb"} {
		if got := set(t, r.Status, name); got != 0 {
			t.Errorf("%s of the child is %#x, want none", name, got)
		}
	}
}

// Without a user what root starts is root, with what this process has:
// the launcher takes nothing from it.
func TestWhatIsStartedAsRootHasWhatThisProcessHas(t *testing.T) {
	root(t)
	own := statusOfThisProcess(t)
	l := newLauncher(t, Config{})
	r, _, status := run(t, l, reporting())
	if status != 0 {
		t.Fatalf("the child ended with %d", status)
	}
	if r.Uid != 0 || r.Gid != 0 {
		t.Errorf("the child runs as %d:%d, want root", r.Uid, r.Gid)
	}
	for _, name := range []string{"CapPrm", "CapEff", "CapBnd", "NoNewPrivs"} {
		if r.Status[name] != own[name] {
			t.Errorf("%s of the child is %s, want this process's, %s", name, r.Status[name], own[name])
		}
	}
}

// The bounding set that the warning goes by is the one the kernel shows
// of this process.
func TestTheBoundingSetIsThisProcesss(t *testing.T) {
	got, err := boundingSet()
	if err != nil {
		t.Fatalf("boundingSet: %v", err)
	}
	if want := set(t, statusOfThisProcess(t), "CapBnd"); got != want {
		t.Errorf("boundingSet = %#x, and CapBnd of this process is %#x", got, want)
	}
}

// A launcher warns when what it starts as root has capabilities a
// container does not have by default, and names them; in a container
// that was given none it has nothing to warn about. As another user
// there is nothing to warn about, whatever the set.
func TestALauncherWarnsOfWhatThisProcessHasBeyondAContainersDefault(t *testing.T) {
	root(t)
	bounding, err := boundingSet()
	if err != nil {
		t.Fatalf("boundingSet: %v", err)
	}
	var log bytes.Buffer
	newLauncher(t, Config{Log: slog.New(slog.NewTextHandler(&log, nil))})
	beyond := beyondDefault(bounding)
	if len(beyond) == 0 {
		if strings.Contains(log.String(), "level=WARN") {
			t.Errorf("a launcher warned in a container that has no more than its default: %s", log.String())
		}
	} else {
		for _, name := range append(beyond, "--user") {
			if !strings.Contains(log.String(), name) {
				t.Errorf("the warning does not name %s: %s", name, log.String())
			}
		}
	}
	log.Reset()
	newLauncher(t, Config{User: &User{UID: 65534, GID: 65534}, Log: slog.New(slog.NewTextHandler(&log, nil))})
	if strings.Contains(log.String(), "level=WARN") {
		t.Errorf("a launcher for a user that is not root warned: %s", log.String())
	}
}
