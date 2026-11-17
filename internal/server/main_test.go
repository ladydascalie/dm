package server

import (
	"os"
	"testing"

	"dm/internal/notify"
)

// TestMain marks every desktop notification this package's tests cause with the
// suite's name. These tests run the real server and therefore the real
// notification path: a scenario that wedges a pane pops a desktop alert, and a
// popup that leaves the terminal must not read as a live one.
func TestMain(m *testing.M) {
	_ = os.Setenv(notify.EnvTag, "TESTSUITE")
	os.Exit(m.Run())
}
