package reaper

import (
	"os"
	"testing"
)

// What Init does as process 1 is tested in a container, by
// scripts/test-pid1.sh with the program in testdata/pid1.
func TestInitDoesNothingInAProcessThatIsNotTheFirst(t *testing.T) {
	if os.Getpid() == 1 {
		t.Skip("this test is process 1")
	}
	status, was := Init()
	if status != 0 || was {
		t.Errorf("Init() = %d, %v; want 0, false", status, was)
	}
}
