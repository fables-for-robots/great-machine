package launcher

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// userEnv tells the child of the kind "new" who its launcher is to start
// processes as: "UID:GID", or nothing for the user of the process.
const userEnv = "LAUNCHER_TEST_USER"

func init() { children["new"] = newChild }

// newChild is the test binary as a process that makes a launcher. It
// writes "refused: " and the error when New fails, and otherwise "made",
// and on the next line what a process started through the launcher
// reports.
func newChild() int {
	cfg := Config{Log: quiet}
	if text := os.Getenv(userEnv); text != "" {
		u, err := ParseUser(text)
		if err != nil {
			fmt.Println("the test gave no user:", err)
			return 1
		}
		cfg.User = &u
	}
	l, err := New(cfg)
	if err != nil {
		fmt.Println("refused:", err)
		return 0
	}
	defer l.Close()
	fmt.Println("made")
	cmd := reportingSelf()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := l.Start(cmd, cmd.Start); err != nil {
		fmt.Println("Start:", err)
		return 1
	}
	if err := cmd.Wait(); err != nil {
		fmt.Println("Wait:", err)
		return 1
	}
	return 0
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

// statusOfThisThread reads the fields of /proc/thread-self/status. The
// goroutine that calls it is locked to its thread.
func statusOfThisThread(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open("/proc/thread-self/status")
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

// set reads a set of capabilities as /proc has it.
func set(t *testing.T, status map[string]string, name string) uint64 {
	t.Helper()
	v, err := strconv.ParseUint(status[name], 16, 64)
	if err != nil {
		t.Fatalf("%s is %q in the status: %v", name, status[name], err)
	}
	return v
}

// rootWithSysAdmin skips a test in a process that is not root with
// CAP_SYS_ADMIN in its bounding set, which is what a zathras is in the
// container of a pod, and returns the bounding set.
func rootWithSysAdmin(t *testing.T) uint64 {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("needs root: scripts/test-linux.sh runs it as root, in a container")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	bounding := set(t, statusOfThisThread(t), "CapBnd")
	if bounding&(1<<capSysAdmin) == 0 {
		t.Skip("needs CAP_SYS_ADMIN, which the container of scripts/test-linux.sh has")
	}
	return bounding
}

// What a launcher starts as root has what the process has but
// CAP_SYS_ADMIN, in every set, and cannot gain it.
func TestWhatIsStartedHasNoCapSysAdmin(t *testing.T) {
	bounding := rootWithSysAdmin(t)
	l := newLauncher(t, Config{})
	r, _, status := run(t, l, reporting())
	if status != 0 {
		t.Fatalf("the child ended with %d", status)
	}
	for _, name := range []string{"CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb"} {
		if set(t, r.Status, name)&(1<<capSysAdmin) != 0 {
			t.Errorf("%s of the child has CAP_SYS_ADMIN: %s", name, r.Status[name])
		}
	}
	want := bounding &^ (1 << capSysAdmin)
	for _, name := range []string{"CapPrm", "CapEff", "CapBnd"} {
		if got := set(t, r.Status, name); got != want {
			t.Errorf("%s of the child is %#x, want what this process has but CAP_SYS_ADMIN, %#x", name, got, want)
		}
	}
	if r.Status["NoNewPrivs"] != "1" {
		t.Errorf("NoNewPrivs of the child is %q, want 1", r.Status["NoNewPrivs"])
	}
	if r.Uid != 0 || r.Gid != 0 {
		t.Errorf("the child runs as %d:%d, want root", r.Uid, r.Gid)
	}
}

// As another user a process has the user, the group and no other group,
// and no capability at all.
func TestWhatIsStartedAsAnotherUser(t *testing.T) {
	rootWithSysAdmin(t)
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
	for _, name := range []string{"CapInh", "CapPrm", "CapEff", "CapAmb"} {
		if got := set(t, r.Status, name); got != 0 {
			t.Errorf("%s of the child is %#x, want none", name, got)
		}
	}
	if set(t, r.Status, "CapBnd")&(1<<capSysAdmin) != 0 {
		t.Errorf("CapBnd of the child has CAP_SYS_ADMIN: %s", r.Status["CapBnd"])
	}
	if r.Status["NoNewPrivs"] != "1" {
		t.Errorf("NoNewPrivs of the child is %q, want 1", r.Status["NoNewPrivs"])
	}
}

// What the launcher's thread gave up, the rest of the process has not:
// it mounts.
func TestTheRestOfTheProcessKeepsWhatItHad(t *testing.T) {
	bounding := rootWithSysAdmin(t)
	newLauncher(t, Config{})
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	status := statusOfThisThread(t)
	for _, name := range []string{"CapPrm", "CapEff", "CapBnd"} {
		if got := set(t, status, name); got != bounding {
			t.Errorf("%s of another thread is %#x after a launcher was made, want %#x as before", name, got, bounding)
		}
	}
	if status["NoNewPrivs"] != "0" {
		t.Errorf("NoNewPrivs of another thread is %q, want 0", status["NoNewPrivs"])
	}
	dir := t.TempDir()
	if err := unix.Mount("tmpfs", dir, "tmpfs", 0, ""); err != nil {
		t.Fatalf("the rest of the process cannot mount any more: %v", err)
	}
	if err := unix.Unmount(dir, 0); err != nil {
		t.Errorf("unmounting: %v", err)
	}
}

// Close ends the thread: the runtime does not give a thread that gave up
// a capability to another goroutine.
func TestCloseEndsTheThread(t *testing.T) {
	l, err := New(Config{Log: quiet})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if l.tid == os.Getpid() {
		// The goroutine got the first thread of the process, which cannot
		// end while the process lives: the runtime parks it for good
		// instead. No second launcher gets that thread.
		l.Close()
		if l, err = New(Config{Log: quiet}); err != nil {
			t.Fatalf("New: %v", err)
		}
	}
	task := fmt.Sprintf("/proc/self/task/%d", l.tid)
	if _, err := os.Stat(task); err != nil {
		t.Fatalf("the launcher's thread %d is no thread of this process: %v", l.tid, err)
	}
	l.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(task); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the launcher's thread %d is still there after Close", l.tid)
		}
		time.Sleep(time.Millisecond)
	}
}

// withoutSetpcap runs the test binary as a process that has no
// CAP_SETPCAP and makes a launcher for user, and returns what it wrote.
//
// It is started the way a launcher starts a process: from a thread that
// has dropped the capability from its bounding set, and is never used
// again.
func withoutSetpcap(t *testing.T, user string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), childEnv+"=new", userEnv+"="+user)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, unix.CAP_SETPCAP, 0, 0, 0); err != nil {
			started <- fmt.Errorf("dropping CAP_SETPCAP: %w", err)
			return
		}
		started <- cmd.Start()
	}()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the process without CAP_SETPCAP: %v: %s", err, out.String())
	}
	return out.String()
}

// A zathras that cannot drop CAP_SYS_ADMIN from what it starts does not
// start anything as root: the service would have the capability. As
// another user the service has no capability, whatever the set holds.
func TestALauncherThatCannotDropRefuses(t *testing.T) {
	rootWithSysAdmin(t)
	out := withoutSetpcap(t, "")
	if !strings.HasPrefix(out, "refused: ") {
		t.Fatalf("a launcher was made in a process without CAP_SETPCAP: %q", out)
	}
	for _, word := range []string{"CAP_SYS_ADMIN", "CAP_SETPCAP", "--user", "operation not permitted"} {
		if !strings.Contains(out, word) {
			t.Errorf("the refusal does not say %q: %s", word, out)
		}
	}

	out = withoutSetpcap(t, "65534:65534")
	made, rest, _ := strings.Cut(out, "\n")
	if made != "made" {
		t.Fatalf("with a user that is not root no launcher was made: %q", out)
	}
	var r report
	reported, _, _ := strings.Cut(rest, "\n")
	if err := json.Unmarshal([]byte(reported), &r); err != nil {
		t.Fatalf("the child reported %q: %v", rest, err)
	}
	if r.Uid != 65534 || set(t, r.Status, "CapEff") != 0 || set(t, r.Status, "CapPrm") != 0 || r.Status["NoNewPrivs"] != "1" {
		t.Errorf("the child runs as %d with CapEff %s, CapPrm %s and NoNewPrivs %s; want 65534, none, none and 1",
			r.Uid, r.Status["CapEff"], r.Status["CapPrm"], r.Status["NoNewPrivs"])
	}
}

// A launcher warns when what it starts as root keeps capabilities a
// container does not have by default, as in a privileged container, and
// names them; in the container of a pod it has nothing to warn about.
func TestTheWarningOfCapabilitiesBeyondAContainers(t *testing.T) {
	bounding := rootWithSysAdmin(t)
	var log bytes.Buffer
	newLauncher(t, Config{Log: slog.New(slog.NewTextHandler(&log, nil))})
	beyond := beyondDefault(bounding &^ (1 << capSysAdmin))
	if len(beyond) == 0 {
		if strings.Contains(log.String(), "level=WARN") {
			t.Errorf("a launcher warned in a container that has no more than its default and CAP_SYS_ADMIN: %s", log.String())
		}
	} else {
		for _, name := range append(beyond, "--user") {
			if !strings.Contains(log.String(), name) {
				t.Errorf("the warning does not name %s: %s", name, log.String())
			}
		}
	}
	// As another user there is nothing to warn about, whatever the set.
	log.Reset()
	newLauncher(t, Config{User: &User{UID: 65534, GID: 65534}, Log: slog.New(slog.NewTextHandler(&log, nil))})
	if strings.Contains(log.String(), "level=WARN") {
		t.Errorf("a launcher for a user that is not root warned: %s", log.String())
	}
}

// A zathras that is not root has no capability to drop. Its launcher is
// made all the same, and what it starts cannot gain privileges.
func TestWithoutRootNoNewPrivsAlone(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("this test is root")
	}
	l := newLauncher(t, Config{})
	r, _, status := run(t, l, reporting())
	if status != 0 {
		t.Fatalf("the child ended with %d", status)
	}
	if r.Status["NoNewPrivs"] != "1" {
		t.Errorf("NoNewPrivs of the child is %q, want 1", r.Status["NoNewPrivs"])
	}
	if got := set(t, r.Status, "CapEff"); got != 0 {
		t.Errorf("CapEff of the child is %#x, want none", got)
	}
}
