package daemon

import (
	"os"
	"strconv"
	"testing"
)

func TestMain(m *testing.M) {
	for i, arg := range os.Args[1:] {
		if arg == "--headless-worker" {
			os.Exit(RunHeadlessWorker(os.Args[i+2:]))
		}
	}
	_ = os.Setenv("ACKBAR_TEST_SUITE_PID", strconv.Itoa(os.Getpid()))
	os.Exit(m.Run())
}
