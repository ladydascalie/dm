package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The box CLI tests drive the built binary against a temp session, the way a
// shell in a pane does, so they prove the wrapper and the server together.

// boxEnv builds a child environment with extra entries replacing any of the
// same name in base.
func boxEnv(base []string, extra ...string) []string {
	drop := map[string]bool{}
	for _, e := range extra {
		drop[strings.SplitN(e, "=", 2)[0]] = true
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, e := range base {
		if drop[strings.SplitN(e, "=", 2)[0]] {
			continue
		}
		out = append(out, e)
	}
	return append(out, extra...)
}

// boxRun runs one dm command and fails the test when it exits nonzero.
func boxRun(t *testing.T, h *harness, env []string, args ...string) string {
	t.Helper()
	out, err := boxTry(t, h, env, args...)
	if err != nil {
		t.Fatalf("dm %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// boxTry runs one dm command and returns its output and exit error.
func boxTry(t *testing.T, h *harness, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// boxFields splits one printed row. These rows carry an empty from column, so
// the fields after the state are the message.
func boxFields(t *testing.T, line string) (seq, key, state, msg string) {
	t.Helper()
	f := strings.Fields(line)
	if len(f) < 3 {
		t.Fatalf("row %q has fewer than three columns", line)
	}
	return f[0], f[1], f[2], strings.Join(f[3:], " ")
}

// boxLines returns the non-empty output lines.
func boxLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestBoxCLI drives every msg verb through the binary: post, list (newest
// first, key filter, limit, --json), wait (match and timeout), clear (one key
// then the whole box) and the rejection of a bad state.
func TestBoxCLI(t *testing.T) {
	h := newHarness(t)
	s := "boxcli"
	env := h.env

	post := func(flags ...string) string {
		args := append([]string{"msg", "post"}, flags...)
		args = append(args, "-s", s)
		return boxRun(t, h, env, args...)
	}

	seq, key, state, msg := boxFields(t, post("--state", "done", "--key", "lane/a", "--msg", "gate green"))
	if seq != "1" || key != "lane/a" || state != "done" || msg != "gate green" {
		t.Fatalf("post 1 = %s %s %s %q, want 1 lane/a done \"gate green\"", seq, key, state, msg)
	}
	post("--state", "working", "--key", "lane/b", "--msg", "running tests")
	post("--state", "blocked", "--key", "lane/a", "--msg", "waiting on gate")

	// Newest first, and the newest entry is the third post.
	list := boxRun(t, h, env, "msg", "list", "-s", s)
	lines := boxLines(list)
	if len(lines) != 3 {
		t.Fatalf("list has %d rows, want 3:\n%s", len(lines), list)
	}
	seq, key, state, msg = boxFields(t, lines[0])
	if seq != "3" || key != "lane/a" || state != "blocked" || msg != "waiting on gate" {
		t.Fatalf("newest row = %s %s %s %q, want 3 lane/a blocked", seq, key, state, msg)
	}
	if seq, _, _, _ = boxFields(t, lines[2]); seq != "1" {
		t.Fatalf("oldest row seq = %s, want 1", seq)
	}

	// The key filter keeps only that key, still newest first.
	filtered := boxLines(boxRun(t, h, env, "msg", "list", "--key", "lane/a", "-s", s))
	if len(filtered) != 2 {
		t.Fatalf("list --key lane/a has %d rows, want 2:\n%s", len(filtered), strings.Join(filtered, "\n"))
	}
	if seq, _, _, _ = boxFields(t, filtered[0]); seq != "3" {
		t.Fatalf("filtered newest seq = %s, want 3", seq)
	}

	// The limit cuts the tail, not the head.
	limited := boxLines(boxRun(t, h, env, "msg", "list", "--limit", "1", "-s", s))
	if len(limited) != 1 {
		t.Fatalf("list --limit 1 has %d rows, want 1", len(limited))
	}
	if seq, _, _, _ = boxFields(t, limited[0]); seq != "3" {
		t.Fatalf("limited row seq = %s, want 3", seq)
	}

	// --json prints the reply unchanged.
	var rep listReply
	if err := json.Unmarshal([]byte(boxRun(t, h, env, "msg", "list", "--json", "-s", s)), &rep); err != nil {
		t.Fatalf("list --json is not the reply: %v", err)
	}
	if len(rep.Entries) != 3 || rep.Entries[0].Seq != 3 || rep.Entries[0].State != stateBlocked {
		t.Fatalf("list --json entries = %+v", rep.Entries)
	}

	// A row wider than the terminal is cut, with an ellipsis.
	long := "this is a fairly long message that will not fit in sixty columns at all"
	post("--state", "working", "--key", "lane/c", "--msg", long)
	narrow := boxEnv(env, "COLUMNS=60")
	narrowOut := boxRun(t, h, narrow, "msg", "list", "-s", s)
	for _, line := range boxLines(narrowOut) {
		if n := boxCells(line); n > 60 {
			t.Fatalf("row is %d cells at COLUMNS=60: %q", n, line)
		}
	}
	if !strings.Contains(narrowOut, "…") {
		t.Fatalf("no cut row carries an ellipsis:\n%s", narrowOut)
	}

	// The newest entry for the key already carries the wanted state, so the
	// wait returns at once and exits zero.
	seq, key, state, msg = boxFields(t, boxRun(t, h, env, "msg", "wait", "--key", "lane/a", "--state", "blocked", "--timeout", "2s", "-s", s))
	if seq != "3" || key != "lane/a" || state != "blocked" || msg != "waiting on gate" {
		t.Fatalf("wait = %s %s %s %q, want the blocked entry at seq 3", seq, key, state, msg)
	}

	// A wait that cannot match exits nonzero and prints the server's error.
	out, err := boxTry(t, h, env, "msg", "wait", "--key", "lane/a", "--state", "error", "--timeout", "300ms", "-s", s)
	if err == nil {
		t.Fatalf("wait on a missing state exited zero:\n%s", out)
	}
	if !strings.Contains(out, "box: wait timed out") {
		t.Fatalf("wait timeout output = %q, want the server error text", out)
	}

	// A bad state is rejected before the request, in the server's words.
	out, err = boxTry(t, h, env, "msg", "post", "--key", "lane/a", "--state", "clear", "-s", s)
	if err == nil {
		t.Fatalf("post --state clear exited zero:\n%s", out)
	}
	if want := `box: bad state "clear": want one of note, working, blocked, done, error`; !strings.Contains(out, want) {
		t.Fatalf("bad state output = %q, want %q", out, want)
	}

	// Clear one key, then the whole box, and read the counts.
	if got := strings.TrimSpace(boxRun(t, h, env, "msg", "clear", "--key", "lane/a", "-s", s)); got != "2" {
		t.Fatalf("clear lane/a = %q, want 2", got)
	}
	if got := boxLines(boxRun(t, h, env, "msg", "list", "--key", "lane/a", "-s", s)); len(got) != 0 {
		t.Fatalf("lane/a still has %d rows after the clear", len(got))
	}
	if got := strings.TrimSpace(boxRun(t, h, env, "msg", "clear", "-s", s)); got != "2" {
		t.Fatalf("clear all = %q, want 2", got)
	}
	if got := boxLines(boxRun(t, h, env, "msg", "list", "-s", s)); len(got) != 0 {
		t.Fatalf("box still has %d rows after clear all", len(got))
	}

	// Seq never resets, so the first post after a clear continues the count.
	if seq, _, _, _ = boxFields(t, post("--state", "note", "--msg", "after clear")); seq != "5" {
		t.Fatalf("post after clear seq = %s, want 5", seq)
	}
}

// TestBoxCLISchema prints the schema the running server serves and finds the
// frozen state enum in it.
func TestBoxCLISchema(t *testing.T) {
	h := newHarness(t)
	out := boxRun(t, h, h.env, "msg", "schema", "-s", "boxschema")
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("msg schema is not JSON: %v\n%s", err, out)
	}
	if got["title"] != "dm message box" {
		t.Fatalf("schema title = %v", got["title"])
	}
	actions, _ := got["actions"].(map[string]any)
	for _, action := range []string{"msg-post", "msg-list", "msg-wait", "msg-clear", "msg-schema"} {
		if actions[action] == nil {
			t.Fatalf("schema has no %s action", action)
		}
	}
	// Every state name in the published enum is one the parser accepts.
	want := map[string]bool{"note": false, "working": false, "blocked": false, "done": false, "error": false}
	for _, st := range stateNames() {
		if _, ok := want[st]; !ok {
			t.Fatalf("the parser has a state %q the frozen set does not", st)
		}
		want[st] = true
	}
	for st, seen := range want {
		if !seen || !strings.Contains(out, `"`+st+`"`) {
			t.Fatalf("schema is missing state %q:\n%s", st, out)
		}
	}
	for _, field := range []string{"timeoutMs", `"entries"`, `"cleared"`, `"schema"`} {
		if !strings.Contains(out, field) {
			t.Fatalf("schema is missing %s", field)
		}
	}
}

// TestBoxCLIStream proves the topic stream reads through the binary: a branch
// filter, a cursor, and a wait that takes no state.
func TestBoxCLIStream(t *testing.T) {
	h := newHarness(t)
	s := "boxstream"
	env := h.env
	post := func(key, state, msg string) {
		boxRun(t, h, env, "msg", "post", "-s", s, "--key", key, "--state", state, "--msg", msg)
	}
	post("msg/a", "note", "one")
	post("msg/a", "working", "two")
	post("msg/b", "done", "three")

	rows := boxLines(boxRun(t, h, env, "msg", "list", "-s", s, "--prefix", "msg/"))
	if len(rows) != 3 {
		t.Fatalf("branch list has %d rows, want 3:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	// A cursor keeps only the rows past it, newest first.
	rows = boxLines(boxRun(t, h, env, "msg", "list", "-s", s, "--prefix", "msg/", "--after", "1"))
	if len(rows) != 2 {
		t.Fatalf("cursor list has %d rows, want 2:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if seq, _, _, _ := boxFields(t, rows[1]); seq != "2" {
		t.Fatalf("oldest row past the cursor = %s, want 2", seq)
	}

	// The stream wait returns the oldest unseen entry and needs no state.
	seq, key, _, msg := boxFields(t, boxRun(t, h, env, "msg", "wait", "-s", s,
		"--prefix", "msg/", "--after", "0", "--timeout", "2s"))
	if seq != "1" || key != "msg/a" || msg != "one" {
		t.Fatalf("stream wait = %s %s %q, want the first entry", seq, key, msg)
	}

	// Naming an exact topic and a branch is refused by the server.
	out, err := boxTry(t, h, env, "msg", "list", "-s", s, "--key", "msg/a", "--prefix", "msg/")
	if err == nil {
		t.Fatalf("key plus prefix exited zero:\n%s", out)
	}
	if !strings.Contains(out, "exclusive") {
		t.Fatalf("key plus prefix output = %q, want the exclusivity error", out)
	}
}

// TestBoxCLIFromPane proves from follows DM_PANE only inside the target
// session. A pane id that is not in the session's tree is rejected, which shows
// the value was sent; a shell in another session sends an empty from.
func TestBoxCLIFromPane(t *testing.T) {
	h := newHarness(t)
	s := "boxfrom"
	h.run("new", "-s", s)
	panes := h.panes(s)
	if len(panes) == 0 {
		t.Fatal("session has no panes")
	}
	pane := panes[0]

	inside := boxEnv(h.env, "DM_SESSION="+s, "DM_PANE="+pane)
	line := boxRun(t, h, inside, "msg", "post", "--state", "done", "--key", "lane/a", "--msg", "in-pane", "-s", s)
	if !strings.Contains(line, pane+" in-pane") {
		t.Fatalf("post row has no from %q: %q", pane, line)
	}

	// A pane-shaped id that is not in the tree is sent, and the server says so.
	missing := boxEnv(h.env, "DM_SESSION="+s, "DM_PANE=w9p9")
	out, err := boxTry(t, h, missing, "msg", "post", "--state", "done", "--key", "lane/b", "-s", s)
	if err == nil {
		t.Fatalf("post from a missing pane exited zero:\n%s", out)
	}
	if !strings.Contains(out, `no pane "w9p9"`) {
		t.Fatalf("missing pane output = %q", out)
	}

	// DM_SESSION names another session, so the pane id is not sent.
	other := boxEnv(h.env, "DM_SESSION=elsewhere", "DM_PANE=w9p9")
	line = boxRun(t, h, other, "msg", "post", "--state", "done", "--key", "lane/c", "--msg", "outside", "-s", s)
	if strings.Contains(line, "w9p9") {
		t.Fatalf("from leaked out of the target session: %q", line)
	}
	if !strings.Contains(line, "lane/c done  outside") {
		t.Fatalf("outside post = %q, want an empty from column", line)
	}

	// A wait inside the pane returns the entry with its from.
	wait := boxRun(t, h, inside, "msg", "wait", "--key", "lane/a", "--state", "done", "--timeout", "2s", "-s", s)
	if !strings.Contains(wait, pane+" in-pane") {
		t.Fatalf("wait reply has no from %q: %q", pane, wait)
	}
}

// TestBoxCLIWaitBlocks posts after a wait has parked, so the wrapper holds the
// socket open for a reply that arrives later.
func TestBoxCLIWaitBlocks(t *testing.T) {
	h := newHarness(t)
	s := "boxwait"
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		cmd := exec.Command(h.bin, "msg", "wait", "--key", "lane/a", "--state", "done", "--timeout", "20s", "-s", s)
		cmd.Env = h.env
		out, err := cmd.CombinedOutput()
		done <- result{string(out), err}
	}()

	// The wait may not be parked yet, and that is fine: a post that lands
	// first still satisfies it.
	time.Sleep(300 * time.Millisecond)
	boxRun(t, h, h.env, "msg", "post", "--state", "done", "--key", "lane/a", "--msg", "gate green", "-s", s)

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("blocked wait: %v\n%s", r.err, r.out)
		}
		seq, key, state, msg := boxFields(t, strings.TrimSpace(r.out))
		if seq != "1" || key != "lane/a" || state != "done" || msg != "gate green" {
			t.Fatalf("blocked wait = %s %s %s %q", seq, key, state, msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("blocked wait never returned")
	}
}
