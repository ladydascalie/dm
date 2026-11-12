package blackbox

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// historyRow is one numbered row of the stream the test writes. Its 48 cells fit
// the narrowest pane this session gives a split, and 13000 of them are 637000
// bytes, past the old fixed 512 KiB ring.
func historyRow(n int) string {
	return fmt.Sprintf("ROW-%06d-%s", n, strings.Repeat("x", 37))
}

// historyRows is the command that prints the numbered stream. seq and awk keep
// the pane shell busy with one process, not a loop of thirteen thousand.
const historyRows = `seq 1 13000 | awk '{printf "ROW-%06d-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n", $1}'`

// historyRowNumbers returns the row numbers a history reply carries, oldest
// first, ignoring the prompt and command echo rows.
func historyRowNumbers(lines []string) []int {
	out := make([]int, 0, len(lines))
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "ROW-") {
			continue
		}
		rest := s[len("ROW-"):]
		if len(rest) < 6 {
			continue
		}
		n, err := strconv.Atoi(rest[:6])
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

// TestHistoryBudgetFromConfig proves the config reaches the pane ring end to
// end: a session with history-lines 8000 keeps more than the old 512 KiB, and a
// reload with a smaller history-lines shrinks the ring in place, keeping the
// newest rows.
func TestHistoryBudgetFromConfig(t *testing.T) {
	// Not parallel: the stream and its replay are a short CPU burst, and the
	// rest of the suite is full of timing-sensitive waits.
	h := newHarness(t)
	cfg := reloadConfigPath(t, h)
	// The config must exist before new: the server spawns the pane with
	// --history from it.
	writeReloadConfig(t, cfg, "prefix \"ctrl+b\"\nhistory-lines 8000\n")

	const session = "histbig"
	h.run("new", "-s", session)
	pane := h.focus(session)
	pidPath := filepath.Join(h.stateDir(), "sessions", session, "panes", pane+".sock.pid")
	panePID := readPID(t, pidPath)

	h.run("write-to", pane, "-s", session, "--msg", historyRows, "--enter")
	h.waitFor(session, pane, historyRow(13000))
	// The last row is on screen before the shell's prompt and the ring's tail
	// settle; give the pane a quiet moment before asking for the history.
	time.Sleep(time.Second)

	before := historyRowNumbers(h.history(session, pane, 13010))
	if len(before) < 12900 {
		t.Fatalf("history before the reload carries %d data rows, want the whole 13000-row stream", len(before))
	}
	if before[0] != 1 {
		t.Fatalf("history starts at row %d, want row 1: the 512 KiB ring would have dropped it",
			before[0])
	}
	if last := before[len(before)-1]; last < 12950 {
		t.Fatalf("history ends at row %d, want the newest rows near 13000", last)
	}

	// Shrink the window: history-lines 2000 asks for the 512 KiB floor, so the
	// ring keeps roughly the newest 10700 rows of the stream.
	writeReloadConfig(t, cfg, "prefix \"ctrl+b\"\nhistory-lines 2000\n")
	h.rpc(session, map[string]any{"action": "reload"})
	if got := readPID(t, pidPath); got != panePID {
		t.Fatalf("pane restarted on reload: pid %d, want %d", got, panePID)
	}

	after := historyRowNumbers(h.history(session, pane, 13010))
	if len(after) == 0 {
		t.Fatal("history after the reload is empty")
	}
	if after[0] == 1 {
		t.Fatalf("row 1 survived the shrink; the ring did not resize")
	}
	if after[0] < 1000 {
		t.Fatalf("history after the shrink starts at row %d, want the oldest rows dropped", after[0])
	}
	if len(after) >= len(before) {
		t.Fatalf("history holds %d rows after the shrink, was %d: the ring did not shrink",
			len(after), len(before))
	}
	if len(after) < 9000 || len(after) > 11000 {
		t.Fatalf("history holds %d rows after the shrink, want about 10700 for a 512 KiB ring",
			len(after))
	}
	if last := after[len(after)-1]; last < 12950 {
		t.Fatalf("history ends at row %d after the shrink, want the newest rows kept", last)
	}
}
