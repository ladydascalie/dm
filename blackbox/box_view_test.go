package blackbox

// The message view under a real attach client: the keys reach the view, the
// reader paints an entry, and a clear leaves the log. The client-side unit
// tests prove the dispatch; these prove the painted frame and the wire.

import (
	"strings"
	"testing"
	"time"
)

// postEntry posts one box entry through the CLI, the producer the operator
// uses.
func postEntry(t *testing.T, h *harness, session, key, state, msg, body string) {
	t.Helper()
	args := []string{"msg", "post", "--key", key, "--state", state, "--msg", msg, "-s", session}
	if body != "" {
		args = append(args, "--body", body)
	}
	h.run(args...)
}

// TestAttachBoxOpensTheBody opens the message view and enters the selected
// entry: the reader overlay paints the body text, and one entry is enough for
// the overlay to be tall enough to carry it.
func TestAttachBoxOpensTheBody(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "body")
	h.focus("body")

	a := startAttach(t, h, "body")
	a.waitContains(t, firstPaint)

	postEntry(t, h, "body", "msg/broadcast", "note", "SUMMARY-ROW",
		"BODYLINE-ALPHA\nBODYLINE-BETA")

	a.write(prefixKey + "m")
	a.waitContains(t, " box ")
	a.waitContains(t, "SUMMARY-ROW")
	a.write("\r")
	a.waitContains(t, "BODYLINE-ALPHA")
	a.waitContains(t, "BODYLINE-BETA")

	// Esc closes the reader and the view stays up.
	a.write("\x1b")
	a.pause(400 * time.Millisecond)
	a.waitContains(t, "SUMMARY-ROW")
}

// TestAttachBoxReadsASummary opens the reader on a notification that carries
// no body: the summary is text the overlay wraps.
func TestAttachBoxReadsASummary(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "sum")
	pane := h.focus("sum")

	a := startAttach(t, h, "sum")
	a.waitContains(t, firstPaint)

	postEntry(t, h, "sum", "notify/"+pane, "blocked", "NEEDS-YOU-NOW", "")

	a.write(prefixKey + "N")
	a.waitContains(t, "notifications")
	a.write("\r")
	// The reader title names the entry, and the hint is the reader's own: the
	// list page's hint says "move".
	a.waitContains(t, "j/k scroll")
	a.waitContains(t, "NEEDS-YOU-NOW")
}

// TestAttachFilteredViewClears proves the clear keys reach the wire: x clears
// one exact topic, X clears the branch the filter shows and leaves every other
// topic alone.
func TestAttachFilteredViewClears(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "nclear")
	pane := h.focus("nclear")

	a := startAttach(t, h, "nclear")
	a.waitContains(t, firstPaint)

	postEntry(t, h, "nclear", "msg/keep", "note", "KEEP-ROW", "")
	postEntry(t, h, "nclear", "notify/"+pane, "done", "NOTE-ONE", "")
	postEntry(t, h, "nclear", "notify/"+pane, "error", "NOTE-TWO", "")

	a.write(prefixKey + "N")
	a.waitContains(t, "notifications")
	a.write("x")
	waitDrop(t, h, "nclear", "NOTE-TWO")
	a.write("X")
	waitDrop(t, h, "nclear", "NOTE-ONE")
	if out := h.run("msg", "list", "--json", "-s", "nclear"); !strings.Contains(out, "KEEP-ROW") {
		t.Fatalf("clear all touched a key the filter did not show:\n%s", out)
	}
}

// waitDrop waits until the session's log no longer carries msg.
func waitDrop(t *testing.T, h *harness, session, msg string) {
	t.Helper()
	start := time.Now()
	for {
		out := h.run("msg", "list", "--json", "-s", session)
		if !strings.Contains(out, msg) {
			return
		}
		if time.Now().After(start.Add(waitBudget)) {
			t.Fatalf("%q was not cleared within the wait budget:\n%s", msg, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
