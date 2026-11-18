package daemon

import (
	"os"
	"strconv"
	"testing"

	"ackbar/internal/agent"
	"ackbar/internal/agents/grok"
	"ackbar/internal/testutil"
)

func TestMain(m *testing.M) {
	reg := agent.NewRegistry()
	_ = reg.Register(grok.New())
	for i, arg := range os.Args[1:] {
		if arg == "--headless-worker" {
			os.Exit(RunHeadlessWorkerWithRegistry(os.Args[i+2:], reg))
		}
	}
	_ = os.Setenv("ACKBAR_TEST_SUITE_PID", strconv.Itoa(os.Getpid()))
	testutil.RunIsolatedTestMain(m)
}
