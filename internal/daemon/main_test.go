package daemon

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	for i, arg := range os.Args[1:] {
		if arg == "--headless-worker" {
			os.Exit(RunHeadlessWorker(os.Args[i+2:]))
		}
	}
	os.Exit(m.Run())
}
