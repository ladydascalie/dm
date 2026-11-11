package pane

import (
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dm/internal/wire"
)

// readTail blocks for a wait's tail reply, skipping broadcasts the pane sent
// before the wait arrived. Its arrival proves the wait is registered, so the
// test can produce output afterwards.
func readTail(t *testing.T, msgs chan wire.Msg) wire.Msg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				t.Fatal("socket closed before the tail reply")
			}
			if m.Type == "bytes" {
				continue
			}
			if m.Type != "resp" || m.WaitReason != "" {
				t.Fatalf("reply = %+v, want the tail reply", m)
			}
			return m
		case <-deadline:
			t.Fatal("no tail reply")
			return wire.Msg{}
		}
	}
}

// waitFinal reads the frames after the tail until the final reply, the one
// carrying a WaitReason, arrives. It returns the bytes frames in arrival
// order and the final reply.
func waitFinal(t *testing.T, msgs chan wire.Msg) (data [][]byte, final wire.Msg) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				t.Fatal("socket closed before the final wait reply")
			}
			switch {
			case m.Type == "resp" && m.WaitReason != "":
				return data, m
			case m.Type == "bytes":
				data = append(data, m.Data)
			default:
				t.Fatalf("unexpected frame %+v while waiting", m)
			}
		case <-deadline:
			t.Fatal("no final wait reply")
		}
	}
}

// waitChildren stands in for /proc's children file on one pane, backed by an
// atomic value so the poll goroutine and the test never race. The returned
// value moves the child set mid-test. The stand-in hangs off the pane, not a
// package var, so parallel tests share no global.
func waitChildren(t *testing.T, p *paneProc, start []int) *atomic.Value {
	t.Helper()
	kids := &atomic.Value{}
	kids.Store(start)
	p.children = func(int) []int { return kids.Load().([]int) }
	return kids
}

// markedPane is a pane with shell integration: one complete command cycle
// already behind it, so a wait sees marks and does not fall back to polling.
// The next command is entered on the socket, which records the wait's anchor.
func markedPane(t *testing.T, cols, rows int) (*paneProc, net.Conn, chan wire.Msg) {
	t.Helper()
	p, cli, msgs := yankPane(t, cols, rows)
	feedPane(p, []byte("\x1b]133;A\x1b\\prompt$\x1b]133;C\x1b\\old out\r\n\x1b]133;D;7\x1b\\"))
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: "cmd2", Enter: true, ID: 1})
	paneSync(t, cli, msgs)
	return p, cli, msgs
}

// TestWaitMarkCarriesTheExitCode proves a wait on a pane with shell
// integration ends at the D mark and reports the exit code the mark carried.
func TestWaitMarkCarriesTheExitCode(t *testing.T) {
	t.Parallel()
	p, cli, msgs := markedPane(t, 40, 6)

	paneSend(t, cli, wire.Msg{Type: "req", Action: "wait", ID: 2})
	tail := readTail(t, msgs)
	feedPane(p, []byte("new out\r\n"))
	feedPane(p, []byte("\x1b]133;D;130\x1b\\"))

	data, final := waitFinal(t, msgs)
	if res := decodeYank(t, tail); !res.Anchor {
		t.Fatalf("tail reply = %+v, want an anchored tail", res)
	}
	if final.WaitReason != "mark" || !final.OK {
		t.Fatalf("final reply = %+v, want ok and reason mark", final)
	}
	if final.Exit == nil || *final.Exit != 130 {
		t.Fatalf("final exit = %v, want 130", final.Exit)
	}
	if len(data) != 2 || string(data[0]) != "new out\r\n" ||
		string(data[1]) != "\x1b]133;D;130\x1b\\" {
		t.Fatalf("bytes frames = %q, want the output then the D mark", data)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiter != nil {
		t.Fatal("the wait stayed registered after the mark")
	}
}

// TestWaitMarkWithoutAnExitCodeLeavesItUnknown proves a bare D mark ends the
// wait with the reason mark and no exit code.
func TestWaitMarkWithoutAnExitCodeLeavesItUnknown(t *testing.T) {
	t.Parallel()
	p, cli, msgs := markedPane(t, 40, 6)

	paneSend(t, cli, wire.Msg{Type: "req", Action: "wait", ID: 2})
	readTail(t, msgs)
	feedPane(p, []byte("\x1b]133;D\x1b\\"))

	_, final := waitFinal(t, msgs)
	if final.WaitReason != "mark" || final.Exit != nil {
		t.Fatalf("final reply = %+v, want reason mark and no exit code", final)
	}
}

// TestWaitStreamHasNoGapAndNoDuplicate proves the tail reply and the bytes
// frames together carry the command's output once, in order, across several
// publishes.
func TestWaitStreamHasNoGapAndNoDuplicate(t *testing.T) {
	t.Parallel()
	p, cli, msgs := markedPane(t, 40, 6)

	paneSend(t, cli, wire.Msg{Type: "req", Action: "wait", ID: 2})
	readTail(t, msgs)
	feedPane(p, []byte("one\r\n"))
	feedPane(p, []byte("two\r\n"))
	feedPane(p, []byte("\x1b]133;D;0\x1b\\"))

	data, final := waitFinal(t, msgs)
	want := []string{"one\r\n", "two\r\n", "\x1b]133;D;0\x1b\\"}
	if len(data) != len(want) {
		t.Fatalf("bytes frames = %d, want %d", len(data), len(want))
	}
	for i := range want {
		if string(data[i]) != want[i] {
			t.Fatalf("bytes frame %d = %q, want %q", i, data[i], want[i])
		}
	}
	if final.Exit == nil || *final.Exit != 0 {
		t.Fatalf("final exit = %v, want 0", final.Exit)
	}
}

// TestWaitChildrenFallbackEndsWhenTheChildLeaves proves a markless pane's
// wait ends when the shell's foreground children empty after holding some,
// and that the exit code stays unknown.
func TestWaitChildrenFallbackEndsWhenTheChildLeaves(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 40, 6)
	kids := waitChildren(t, p, []int{4242})

	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: "sleep 5", Enter: true, ID: 1})
	paneSync(t, cli, msgs)
	paneSend(t, cli, wire.Msg{Type: "req", Action: "wait", ID: 2})
	readTail(t, msgs)

	kids.Store([]int{})

	_, final := waitFinal(t, msgs)
	if final.WaitReason != "children" || final.Exit != nil {
		t.Fatalf("final reply = %+v, want reason children and no exit code", final)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiter != nil {
		t.Fatal("the wait stayed registered after the children emptied")
	}
}

// TestWaitIdleMarklessPaneEndsAtOnce proves a wait on a pane with no marks
// and no foreground child answers immediately instead of blocking forever.
func TestWaitIdleMarklessPaneEndsAtOnce(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 40, 6)
	waitChildren(t, p, nil)
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: "true", Enter: true, ID: 1})
	paneSync(t, cli, msgs)

	start := time.Now()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "wait", ID: 2})
	readTail(t, msgs)
	_, final := waitFinal(t, msgs)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("idle wait took %s", took)
	}
	if final.WaitReason != "idle" || final.Exit != nil {
		t.Fatalf("final reply = %+v, want reason idle and no exit code", final)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiter != nil {
		t.Fatal("the idle wait stayed registered")
	}
}

// TestSecondWaitIsRefused proves one pane waits on one command at a time.
func TestSecondWaitIsRefused(t *testing.T) {
	t.Parallel()
	p, cli, msgs := markedPane(t, 40, 6)

	paneSend(t, cli, wire.Msg{Type: "req", Action: "wait", ID: 1})
	readTail(t, msgs)
	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "wait", ID: 2})
	if reply.OK {
		t.Fatalf("second wait reply = %+v, want a refusal", reply)
	}
	if !strings.Contains(reply.Error, "already") {
		t.Fatalf("refusal error = %q, want an already-running note", reply.Error)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiter == nil {
		t.Fatal("the refusal displaced the running wait")
	}
}

// TestWaitDeadPaneAnswersAtOnce proves a dead pane's wait answers with the
// reason dead instead of waiting for an end that will not come.
func TestWaitDeadPaneAnswersAtOnce(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 40, 6)
	p.mu.Lock()
	p.dead = true
	p.mu.Unlock()

	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "wait", ID: 1})
	if !reply.OK || reply.WaitReason != "dead" || reply.Exit != nil {
		t.Fatalf("dead wait reply = %+v, want ok, reason dead, no exit", reply)
	}
}

// TestWaiterDisconnectCancelsTheWait proves the wait ends and the children
// poll stops when the waiting connection goes away.
func TestWaiterDisconnectCancelsTheWait(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 40, 6)
	kids := waitChildren(t, p, []int{4242})

	paneSend(t, cli, wire.Msg{Type: "req", Action: "wait", ID: 1})
	readTail(t, msgs)
	_ = cli.Close()
	waitFor(t, 2*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.waiter == nil
	}, "the waiter to clear")

	// The poll stops without ending anything: the child set empties and no
	// wait is registered to complete.
	kids.Store([]int{})
	time.Sleep(2 * waitPollEvery)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiter != nil {
		t.Fatal("the wait came back after the waiter disconnected")
	}
}

// TestWaitAnswersAFinishedCommandAtOnce proves a wait that arrives after the
// command already returned answers from the D mark the pane still holds,
// instead of hanging until unrelated output moves the pane.
func TestWaitAnswersAFinishedCommandAtOnce(t *testing.T) {
	t.Parallel()
	p, cli, msgs := markedPane(t, 40, 6)

	// The command runs and returns before the wait request is read.
	feedPane(p, []byte("out\r\n\x1b]133;D;130\x1b\\"))
	paneSend(t, cli, wire.Msg{Type: "req", Action: "wait", ID: 2})

	start := time.Now()
	tail := readTail(t, msgs)
	_, final := waitFinal(t, msgs)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("finished-command wait took %s", took)
	}
	if res := decodeYank(t, tail); !res.Anchor {
		t.Fatalf("tail reply = %+v, want an anchored tail", res)
	}
	found := false
	for _, l := range decodeYank(t, tail).Lines {
		if strings.Contains(l, "out") {
			found = true
		}
	}
	if !found {
		t.Fatalf("tail lines = %#v, want the command's output", decodeYank(t, tail).Lines)
	}
	if final.WaitReason != "mark" || final.Exit == nil || *final.Exit != 130 {
		t.Fatalf("final reply = %+v, want reason mark and exit 130", final)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiter != nil {
		t.Fatal("the finished-command wait stayed registered")
	}
}

// TestReflowHandsARunningWaitToTheChildren proves a wait in flight across a
// reflow loses its mark path and finishes through the children fallback.
func TestReflowHandsARunningWaitToTheChildren(t *testing.T) {
	t.Parallel()
	p, cli, msgs := markedPane(t, 40, 6)
	kids := waitChildren(t, p, []int{4242})

	paneSend(t, cli, wire.Msg{Type: "req", Action: "wait", ID: 2})
	readTail(t, msgs)
	paneReply(t, cli, msgs, wire.Msg{Type: "req", Action: "reflow", Cols: 10, Rows: 4}, "screen")

	kids.Store([]int{})
	_, final := waitFinal(t, msgs)
	if final.WaitReason != "children" || final.Exit != nil {
		t.Fatalf("final reply = %+v, want reason children and no exit code", final)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiter != nil {
		t.Fatal("the wait survived the reflow unchanged")
	}
}

// TestReflowEndsAWaitWithNoRunningChild proves the fallback a reflow starts
// ends the wait when no child is running, so a command that died with its
// terminal never hangs the wait.
func TestReflowEndsAWaitWithNoRunningChild(t *testing.T) {
	t.Parallel()
	p, cli, msgs := markedPane(t, 40, 6)
	waitChildren(t, p, nil)

	paneSend(t, cli, wire.Msg{Type: "req", Action: "wait", ID: 2})
	readTail(t, msgs)
	paneReply(t, cli, msgs, wire.Msg{Type: "req", Action: "reflow", Cols: 10, Rows: 4}, "screen")

	_, final := waitFinal(t, msgs)
	if final.WaitReason != "children" || final.Exit != nil {
		t.Fatalf("final reply = %+v, want reason children and no exit code", final)
	}
}
