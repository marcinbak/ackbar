package daemon

import (
	"os"
	"strconv"
	"testing"

	"ackbar/internal/testutil"
)

func TestMain(m *testing.M) {
	for i, arg := range os.Args[1:] {
		if arg == "--headless-worker" {
			os.Exit(RunHeadlessWorker(os.Args[i+2:]))
		}
	}
	_ = os.Setenv("ACKBAR_TEST_SUITE_PID", strconv.Itoa(os.Getpid()))
	testutil.RunIsolatedTestMain(m)
}
