package daemon

import (
	"os"
	"strconv"
	"testing"

	"ackbar/internal/agents"
	"ackbar/internal/testutil"
)

func TestMain(m *testing.M) {
	reg, _ := agents.NewDefaultRegistry()
	if os.Getenv("ACKBAR_TEST_MOCK_PERSISTENT_PLUGIN") == "1" {
		registerMockPersistentPlugins(reg)
	}
	for i, arg := range os.Args[1:] {
		if arg == "--headless-worker" {
			os.Exit(RunHeadlessWorkerWithRegistry(os.Args[i+2:], reg))
		}
	}
	_ = os.Setenv("ACKBAR_TEST_SUITE_PID", strconv.Itoa(os.Getpid()))
	testutil.RunIsolatedTestMain(m)
}
