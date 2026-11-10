package blackbox

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dm/internal/cells"
)

// The box CLI tests drive the built binary against a temp session, the way a
// shell in a pane does, so they prove the wrapper and the server together.

// schemaEnum collects every enum array the published schema carries. The
// state enum is the parser's set, so a test can read it off the wire.
func schemaEnum(v any) []string {
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if enum, ok := t["enum"].([]any); ok {
				for _, e := range enum {
					if s, ok := e.(string); ok {
						out = append(out, s)
					}
				}
			}
			for _, x := range t {
				walk(x)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(v)
	return out
}

// boxEntry is one message-box row as the CLI prints it in JSON. It is a local
// shape: the test proves the printed fields, not a production type.
type boxEntry struct {
	Seq   int    `json:"seq"`
	Key   string `json:"key"`
	State string `json:"state"`
	Msg   string `json:"msg"`
}

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
	var rep struct {
		Entries []boxEntry `json:"entries"`
		Trimmed bool       `json:"trimmed"`
	}
	if err := json.Unmarshal([]byte(boxRun(t, h, env, "msg", "list", "--json", "-s", s)), &rep); err != nil {
		t.Fatalf("list --json is not the reply: %v", err)
	}
	if len(rep.Entries) != 3 || rep.Entries[0].Seq != 3 || rep.Entries[0].State != "blocked" {
		t.Fatalf("list --json entries = %+v", rep.Entries)
	}

	// A row wider than the terminal is cut, with an ellipsis.
	long := "this is a fairly long message that will not fit in sixty columns at all"
	post("--state", "working", "--key", "lane/c", "--msg", long)
	narrow := boxEnv(env, "COLUMNS=60")
	narrowOut := boxRun(t, h, narrow, "msg", "list", "-s", s)
	for _, line := range boxLines(narrowOut) {
		if n := cells.Cells(line); n > 60 {
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
	for _, st := range schemaEnum(got) {
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

// boxRows splits printed output into rows of fields.
func boxRows(out string) [][]string {
	var rows [][]string
	for _, line := range boxLines(out) {
		rows = append(rows, strings.Fields(line))
	}
	return rows
}

// boxAwaitWatcherRows polls dm msg watchers until done holds, because a read
// from another process reaches the server a moment after it starts.
func boxAwaitWatcherRows(t *testing.T, h *harness, session, why string, done func([][]string) bool) [][]string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		rows := boxRows(boxRun(t, h, h.env, "msg", "watchers", "-s", session))
		if done(rows) {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: dm msg watchers printed %v", why, rows)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestBoxCLIWatchers drives the presence verb through the binary: a wait run
// inside one pane shows as parked, the post that serves it clears the mark, and
// a read from the other pane leaves a second row.
func TestBoxCLIWatchers(t *testing.T) {
	h := newHarness(t)
	s := "boxwatch"
	h.run("new", "-s", s)
	panes := h.panes(s)
	if len(panes) != 1 {
		t.Fatalf("a new session has %d panes, want 1", len(panes))
	}
	root := panes[0]
	second := h.split(s, root, "right")
	inRoot := boxEnv(h.env, "DM_SESSION="+s, "DM_PANE="+root)
	inSecond := boxEnv(h.env, "DM_SESSION="+s, "DM_PANE="+second)

	// The wait parks inside the root pane, with no entry in the box yet.
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := boxTry(t, h, inRoot, "msg", "wait", "--key", "lane/a",
			"--state", "done", "--timeout", "20s", "-s", s)
		done <- result{out, err}
	}()

	rows := boxAwaitWatcherRows(t, h, s, "the parked wait", func(rows [][]string) bool {
		return len(rows) == 1 && len(rows[0]) == 4 && rows[0][3] == "wait"
	})
	for i, want := range []string{root, "lane/a", "0", "wait"} {
		if rows[0][i] != want {
			t.Fatalf("parked row = %v, want %v", rows[0], []string{root, "lane/a", "0", "wait"})
		}
	}

	// The post that serves the wait leaves it unparked at the seq it served.
	boxRun(t, h, inSecond, "msg", "post", "--state", "done", "--key", "lane/a",
		"--msg", "gate green", "-s", s)
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("the parked wait: %v\n%s", r.err, r.out)
		}
		if !strings.Contains(r.out, "gate green") {
			t.Fatalf("wait printed %q", r.out)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the parked wait never returned")
	}

	// A list from the other pane leaves its own watch, so two rows print.
	boxRun(t, h, inSecond, "msg", "list", "--key", "lane/a", "-s", s)
	rows = boxAwaitWatcherRows(t, h, s, "the second watch", func(rows [][]string) bool {
		return len(rows) == 2
	})
	seen := map[string]bool{}
	for _, row := range rows {
		if len(row) != 4 {
			t.Fatalf("row %v has %d columns, want 4", row, len(row))
		}
		seen[row[0]] = true
		if row[1] != "lane/a" || row[2] != "1" || row[3] != "-" {
			t.Fatalf("row %v, want lane/a at cursor 1 and no wait", row)
		}
	}
	if !seen[root] || !seen[second] {
		t.Fatalf("rows name %v, want both %s and %s", seen, root, second)
	}

	// --json prints the reply unchanged.
	var rep struct {
		Watchers []struct {
			Pane      string `json:"pane"`
			Key       string `json:"key"`
			KeyPrefix string `json:"keyPrefix"`
		} `json:"watchers"`
	}
	if err := json.Unmarshal([]byte(boxRun(t, h, h.env, "msg", "watchers", "--json", "-s", s)), &rep); err != nil {
		t.Fatalf("watchers --json is not the reply: %v", err)
	}
	if len(rep.Watchers) != 2 {
		t.Fatalf("watchers --json = %+v, want two", rep.Watchers)
	}

	// A branch read prints its filter with a trailing star.
	boxRun(t, h, inRoot, "msg", "list", "--prefix", "lane/", "-s", s)
	rows = boxAwaitWatcherRows(t, h, s, "the prefix watch", func(rows [][]string) bool {
		return len(rows) == 3
	})
	found := false
	for _, row := range rows {
		if row[1] == "lane/*" && row[2] == "1" && row[3] == "-" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no lane/* row in %v", rows)
	}
}

// boxErrBuffer collects a child's stderr under a mutex, so a test can read it
// while the child still runs.
type boxErrBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (e *boxErrBuffer) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.b.Write(p)
}

func (e *boxErrBuffer) String() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.b.String()
}

// boxSubProcess is a running dm msg sub. Its stdout lines arrive on a channel
// because a pipe carries no read deadline: every read is bounded by a select
// on that channel instead, and the process is killed in cleanup.
type boxSubProcess struct {
	t    *testing.T
	cmd  *exec.Cmd
	news chan string
	done chan error
	errs *boxErrBuffer
}

// boxSubExec runs dm msg sub and returns the handle that reads its lines.
func boxSubExec(t *testing.T, h *harness, env []string, args ...string) *boxSubProcess {
	t.Helper()
	p := &boxSubProcess{t: t, cmd: exec.Command(h.bin, args...), errs: &boxErrBuffer{},
		news: make(chan string, 64), done: make(chan error, 1)}
	p.cmd.Env = env
	p.cmd.Stderr = p.errs
	out, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("sub stdout: %v", err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("start dm %s: %v", strings.Join(args, " "), err)
	}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			p.news <- sc.Text()
		}
		close(p.news)
		p.done <- p.cmd.Wait()
	}()
	t.Cleanup(p.stop)
	return p
}

// stop kills the subscription and waits for it, so a test leaves no process
// behind and no goroutine writing to its buffers.
func (p *boxSubProcess) stop() {
	_ = p.cmd.Process.Kill()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
	}
}

// next returns the next line the sub prints, and fails when none arrives
// inside the wait, because a read that can block forever proves nothing.
func (p *boxSubProcess) next(wait time.Duration) string {
	p.t.Helper()
	select {
	case line, ok := <-p.news:
		if !ok {
			p.t.Fatalf("the sub exited before printing a line\nstderr:\n%s", p.errs.String())
		}
		return line
	case <-time.After(wait):
		p.t.Fatalf("the sub printed no line within %s\nstderr:\n%s", wait, p.errs.String())
	}
	return ""
}

// quiet fails when the sub prints a line inside the wait.
func (p *boxSubProcess) quiet(wait time.Duration, why string) {
	p.t.Helper()
	select {
	case line, ok := <-p.news:
		if !ok {
			p.t.Fatalf("%s: the sub exited\nstderr:\n%s", why, p.errs.String())
		}
		p.t.Fatalf("%s: the sub printed %q", why, line)
	case <-time.After(wait):
	}
}

// errText returns what the sub wrote to stderr so far.
func (p *boxSubProcess) errText() string { return p.errs.String() }

// boxSubErrWait polls the sub's stderr until done holds, with a deadline.
func boxSubErrWait(t *testing.T, p *boxSubProcess, why string, done func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := p.errText()
		if done(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: stderr is %q", why, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// boxKillServer SIGKILLs the session's server and waits until its socket
// refuses, so the next dial starts a fresh one instead of meeting the dead. It
// returns the pid it killed.
func boxKillServer(t *testing.T, h *harness, session string) int {
	t.Helper()
	dir := filepath.Join(h.stateDir(), "sessions", session)
	pid := readPID(t, filepath.Join(dir, "server.pid"))
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill server %d: %v", pid, err)
	}
	sock := filepath.Join(dir, "server.sock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := net.DialTimeout("unix", sock, 200*time.Millisecond)
		if err != nil {
			return pid
		}
		_ = c.Close()
		if time.Now().After(deadline) {
			t.Fatalf("server socket %s still answers after the kill", sock)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestBoxCLISub proves the subscription streams through the binary and survives
// a server restart: the entry posted to the new server prints on the same
// process, after the cursor the first entry left.
func TestBoxCLISub(t *testing.T) {
	h := newHarness(t)
	s := "boxsub"
	h.run("new", "-s", s)
	sub := boxSubExec(t, h, h.env, "msg", "sub", "--key", "lane/a", "-s", s)

	boxRun(t, h, h.env, "msg", "post", "--key", "lane/a", "--state", "done", "--msg", "gate green", "-s", s)
	seq, key, state, msg := boxFields(t, sub.next(20*time.Second))
	if seq != "1" || key != "lane/a" || state != "done" || msg != "gate green" {
		t.Fatalf("first row = %s %s %s %q, want 1 lane/a done \"gate green\"", seq, key, state, msg)
	}

	// Kill the server. The post below starts a new one, and the sub resumes at
	// the cursor the first row left, so the new entry prints on the same
	// process.
	old := boxKillServer(t, h, s)
	h.run("new", "-s", s)
	pidPath := filepath.Join(h.stateDir(), "sessions", s, "server.pid")
	if got := readPID(t, pidPath); got == old {
		t.Fatalf("server pid is still %d, so the server did not restart", got)
	}
	boxRun(t, h, h.env, "msg", "post", "--key", "lane/a", "--state", "done", "--msg", "gate again", "-s", s)
	seq, key, state, msg = boxFields(t, sub.next(20*time.Second))
	if seq != "2" || key != "lane/a" || state != "done" || msg != "gate again" {
		t.Fatalf("row after the restart = %s %s %s %q, want 2 lane/a done \"gate again\"", seq, key, state, msg)
	}
}

// TestBoxCLISubJSON proves --json prints the entry object alone, one per line,
// so a reader can parse NDJSON.
func TestBoxCLISubJSON(t *testing.T) {
	h := newHarness(t)
	s := "boxsubjson"
	h.run("new", "-s", s)
	sub := boxSubExec(t, h, h.env, "msg", "sub", "--key", "lane/a", "--json", "-s", s)
	boxRun(t, h, h.env, "msg", "post", "--key", "lane/a", "--state", "working", "--msg", "two lines", "-s", s)

	line := sub.next(20 * time.Second)
	var got boxEntry
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("sub --json line is not one entry: %v\n%s", err, line)
	}
	if got.Seq != 1 || got.Key != "lane/a" || got.State != "working" || got.Msg != "two lines" {
		t.Fatalf("sub --json entry = %+v", got)
	}
	// The frame's trimmed flag does not ride along: the line is the entry.
	var fields map[string]any
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		t.Fatalf("sub --json line is not an object: %v", err)
	}
	if _, ok := fields["trimmed"]; ok {
		t.Fatalf("sub --json line carries the frame: %s", line)
	}
}

// TestBoxCLISubStateFilter proves a state filter skips a row: the note prints
// nothing and the done entry is the first and only line.
func TestBoxCLISubStateFilter(t *testing.T) {
	h := newHarness(t)
	s := "boxsubstate"
	h.run("new", "-s", s)
	sub := boxSubExec(t, h, h.env, "msg", "sub", "--key", "lane/a", "--state", "done", "-s", s)

	boxRun(t, h, h.env, "msg", "post", "--key", "lane/a", "--state", "note", "--msg", "passing by", "-s", s)
	sub.quiet(500*time.Millisecond, "a note under --state done")

	boxRun(t, h, h.env, "msg", "post", "--key", "lane/a", "--state", "done", "--msg", "gate green", "-s", s)
	seq, _, state, msg := boxFields(t, sub.next(20*time.Second))
	if seq != "2" || state != "done" || msg != "gate green" {
		t.Fatalf("filtered row = %s %s %q, want the done entry at seq 2", seq, state, msg)
	}
}

// TestBoxCLISubBadRequest proves a refused subscription exits nonzero with the
// server's own words, rather than redialling a request that stays refused.
func TestBoxCLISubBadRequest(t *testing.T) {
	h := newHarness(t)
	s := "boxsubbad"
	h.run("new", "-s", s)
	out, err := boxTry(t, h, h.env, "msg", "sub", "--key", "lane/a", "--prefix", "lane/", "-s", s)
	if err == nil {
		t.Fatalf("sub with an exact key and a branch exited zero:\n%s", out)
	}
	if !strings.Contains(out, "exclusive") {
		t.Fatalf("sub bad request output = %q, want the server's exclusivity error", out)
	}
}

// TestBoxCLISubGapNote proves a cursor behind the oldest kept row prints the
// gap note once, on the first frame, and not again for the entries after it.
func TestBoxCLISubGapNote(t *testing.T) {
	h := newHarness(t)
	s := "boxsubgap"
	h.run("new", "-s", s)
	boxRun(t, h, h.env, "msg", "post", "--key", "lane/a", "--state", "note", "--msg", "dropped", "-s", s)
	boxRun(t, h, h.env, "msg", "clear", "--key", "lane/a", "-s", s)
	boxRun(t, h, h.env, "msg", "post", "--key", "lane/a", "--state", "done", "--msg", "kept", "-s", s)

	sub := boxSubExec(t, h, h.env, "msg", "sub", "--key", "lane/a", "--after", "0", "-s", s)
	if seq, _, _, _ := boxFields(t, sub.next(20*time.Second)); seq != "2" {
		t.Fatalf("row after the gap = %s, want the kept entry at seq 2", seq)
	}
	const note = "dm: entries between the cursor and the oldest kept row are gone"
	boxSubErrWait(t, sub, "the gap note", func(got string) bool {
		return strings.Contains(got, note)
	})

	boxRun(t, h, h.env, "msg", "post", "--key", "lane/a", "--state", "done", "--msg", "next", "-s", s)
	if seq, _, _, _ := boxFields(t, sub.next(20*time.Second)); seq != "3" {
		t.Fatalf("row after the gap = %s, want seq 3", seq)
	}
	if got := strings.Count(sub.errText(), note); got != 1 {
		t.Fatalf("the gap note printed %d times, want once:\n%s", got, sub.errText())
	}
}
