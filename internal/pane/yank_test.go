package pane

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"reflect"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// yankPane is a pane daemon with a pipe for its PTY and a socket the test
// drives. The pipe's read end is drained, so a write never blocks. A reader
// drains the socket, because a publish broadcasts to every subscriber and the
// test only wants the replies.
func yankPane(t *testing.T, cols, rows int) (*paneProc, net.Conn, chan wire.Msg) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	go func() { _, _ = io.Copy(io.Discard, r) }()
	p := &paneProc{
		id: "w1p1", vt: vt.NewTerm(cols, rows), ring: NewRing(ringDefault),
		subs: map[*subConn]bool{}, st: wire.Status{State: "idle"}, ptmx: w,
	}
	serve, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	msgs := make(chan wire.Msg, 64)
	go func() {
		for {
			m, err := wire.ReadMsg(cli)
			if err != nil {
				close(msgs)
				return
			}
			msgs <- m
		}
	}()
	go p.serve(serve)
	return p, cli, msgs
}

// paneSend sends one request that has no reply, such as a write.
func paneSend(t *testing.T, cli net.Conn, m wire.Msg) {
	t.Helper()
	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))
	if err := wire.WriteMsg(cli, m); err != nil {
		t.Fatalf("send %s: %v", m.Action, err)
	}
}

// paneRequest sends one request and reads its reply, skipping the broadcasts
// a publish sends to the same socket.
func paneRequest(t *testing.T, cli net.Conn, msgs chan wire.Msg, m wire.Msg) wire.Msg {
	t.Helper()
	paneSend(t, cli, m)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case reply, ok := <-msgs:
			if !ok {
				t.Fatalf("socket closed before the %s reply", m.Action)
			}
			if reply.Type == "resp" && reply.ID == m.ID {
				return reply
			}
		case <-deadline:
			t.Fatalf("no %s reply", m.Action)
		}
	}
}

// paneSync waits for the pane to process everything sent so far. A ping is
// ordered after the writes on the same socket.
func paneSync(t *testing.T, cli net.Conn, msgs chan wire.Msg) {
	t.Helper()
	paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "ping", ID: 99})
}

// yankReply is the decoded yank response.
type yankReply struct {
	Lines  []string `json:"lines"`
	Start  int      `json:"start"`
	Rows   int      `json:"rows"`
	Anchor bool     `json:"anchor"`
}

func decodeYank(t *testing.T, m wire.Msg) yankReply {
	t.Helper()
	var res yankReply
	if len(m.Res) > 0 {
		if err := json.Unmarshal(m.Res, &res); err != nil {
			t.Fatalf("decode yank reply: %v", err)
		}
	}
	return res
}

// An Enter records the output row the next output starts on, and a second
// Enter moves it.
func TestAnchorRecordsOnEnter(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("one\r\ntwo\r\n"))
	p.mu.Unlock()

	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)
	p.mu.Lock()
	first, set := p.anchor, p.anchorSet
	p.mu.Unlock()
	if !set {
		t.Fatal("Enter did not set the anchor")
	}

	p.mu.Lock()
	p.publishLocked([]byte("\r\nthree\r\nfour\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 2})
	paneSync(t, cli, msgs)
	p.mu.Lock()
	second := p.anchor
	p.mu.Unlock()
	if second <= first {
		t.Fatalf("anchor did not move: %d then %d", first, second)
	}
}

// The yank reply starts at the anchor, so the rows before the last Enter are
// not copied.
func TestYankStartsAfterTheAnchor(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("one\r\ntwo\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("\r\nthree\r\nfour\r\n"))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if !res.Anchor {
		t.Fatal("yank reported no anchor after an Enter")
	}
	if want := []string{"three", "four", "", "", ""}; !reflect.DeepEqual(res.Lines, want) {
		t.Fatalf("yank lines = %#v, want %#v", res.Lines, want)
	}
	if res.Start != 0 {
		t.Fatalf("yank start = %d, want 0", res.Start)
	}
	if res.Rows != 5 {
		t.Fatalf("yank rows = %d, want 5", res.Rows)
	}
}

// A pane that has never been sent an Enter reports no anchor, so the client
// can fall back to the visible screen.
func TestYankWithoutEnterHasNoAnchor(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("one\r\ntwo\r\n"))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 1}))
	if res.Anchor {
		t.Fatal("yank reported an anchor before any Enter")
	}
	if len(res.Lines) != 0 {
		t.Fatalf("yank lines = %#v, want none", res.Lines)
	}
}

// The shell echoes the command dm wrote, then ends the line. The copy starts
// on the output, not on that echoed command line.
func TestYankDropsTheWrittenEcho(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("old\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write",
		Msg: "go test ./...", Enter: true, ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("go test ./...\r\nok dm 1.2s\r\nfail dm 0.1s\r\n"))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if want := []string{"ok dm 1.2s", "fail dm 0.1s"}; !reflect.DeepEqual(yankCopy(res), want) {
		t.Fatalf("yank copy = %#v, want %#v", yankCopy(res), want)
	}
}

// A prompt that repaints the line on Enter puts bytes between the anchor and
// the output. The command start mark beats them: the copy begins at the output.
func TestYankStartsAtTheCommandMark(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("old\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write",
		Msg: "make", Enter: true, ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("make\r\n\x1b]133;C\x1b\\built target\r\ndone\r\n"))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if want := []string{"built target", "done"}; !reflect.DeepEqual(yankCopy(res), want) {
		t.Fatalf("yank copy = %#v, want %#v", yankCopy(res), want)
	}
}

// A command end mark cuts the copy exactly, so no prompt and no trailing blank
// rows follow the last line of output.
func TestYankEndsAtTheCommandEnd(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("old\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write",
		Msg: "ls", Enter: true, ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("ls\r\n\x1b]133;C\x1b\\a.txt\r\nb.txt\r\n\x1b]133;D\x1b\\prompt$ "))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if want := []string{"a.txt", "b.txt"}; !reflect.DeepEqual(yankCopy(res), want) {
		t.Fatalf("yank copy = %#v, want %#v", yankCopy(res), want)
	}
}

// A marked pane ends the copy at the prompt that follows the command, so the
// trailing prompt is not copied.
func TestYankDropsTheMarkedPrompt(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("one\r\ntwo\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("\r\n\x1b]133;C\x1b\\three\r\nfour\r\n\x1b]133;D\x1b\\\x1b]133;A\x1b\\prompt$ "))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if want := []string{"three", "four", "", "", ""}; !reflect.DeepEqual(res.Lines, want) {
		t.Fatalf("yank lines = %#v, want %#v", res.Lines, want)
	}
}

// An unmarked pane keeps the last row with content, prompt and all.
func TestYankKeepsTheUnmarkedPrompt(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("one\r\ntwo\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("\r\nthree\r\nprompt$ "))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if want := []string{"three", "prompt$", "", "", ""}; !reflect.DeepEqual(res.Lines, want) {
		t.Fatalf("yank lines = %#v, want %#v", res.Lines, want)
	}
}

// yankCopy is the text the client writes: the served rows from start, with
// trailing blank rows dropped.
func yankCopy(res yankReply) []string {
	if res.Start < 0 || res.Start > res.Rows || res.Rows > len(res.Lines) {
		return nil
	}
	lines := append([]string(nil), res.Lines[res.Start:res.Rows]...)
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// An alternate-screen excursion between the command line and the next command
// does not move the anchor: the copy starts on the next command's output.
func TestYankStartsAfterAnAltScreenExcursion(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("one\r\ntwo\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("\x1b[?1049hALT-1\r\nALT-2\r\n\x1b[?1049l"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 2})
	paneSync(t, cli, msgs)
	p.mu.Lock()
	p.publishLocked([]byte("\r\nafter\r\nthe\r\napp\r\n"))
	p.mu.Unlock()

	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 3}))
	if want := []string{"after", "the", "app"}; !reflect.DeepEqual(yankCopy(res), want) {
		t.Fatalf("yank copy = %#v, want %#v (lines %#v)", yankCopy(res), want, res.Lines)
	}
}

// An alternate-screen app that enters and leaves inside one pty read still
// drops the anchor: the mode at the chunk boundaries is the same, so only the
// transition count can see the excursion.
func TestYankDropsTheAnchorWhenAltFitsOneRead(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("old\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("\x1b[?1049hALT\r\n\x1b[?1049l"))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if res.Anchor {
		t.Fatal("yank kept the anchor across an alternate-screen excursion")
	}
	if len(res.Lines) != 0 {
		t.Fatalf("yank lines = %#v, want none", res.Lines)
	}
}

// A pane whose Enter is not echoed keeps its first output row. The output
// starts at the anchor, with no newline for the pty to have echoed.
func TestYankKeepsTheFirstRowWithoutAnEcho(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("old\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("first\r\nsecond\r\n"))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if want := []string{"first", "second"}; !reflect.DeepEqual(yankCopy(res), want) {
		t.Fatalf("yank copy = %#v, want %#v (lines %#v)", yankCopy(res), want, res.Lines)
	}
}

// A one-character first row also survives an un-echoed Enter: the newline
// after it is close to the anchor but has text before it, so it is output and
// not the echo.
func TestYankKeepsAShortFirstRowWithoutAnEcho(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("old\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("a\r\nsecond\r\n"))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if want := []string{"a", "second"}; !reflect.DeepEqual(yankCopy(res), want) {
		t.Fatalf("yank copy = %#v, want %#v (lines %#v)", yankCopy(res), want, res.Lines)
	}
}

// A command that clears the screen copies the output it printed afterwards,
// not the rows the clear wiped and not nothing.
func TestYankCopiesOutputAfterAClear(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("old\r\nrows\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("\r\n\x1b[2J\x1b[Hfile1\r\nfile2\r\n"))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if want := []string{"file1", "file2"}; !reflect.DeepEqual(yankCopy(res), want) {
		t.Fatalf("yank copy = %#v, want %#v (lines %#v)", yankCopy(res), want, res.Lines)
	}
}

// A pane resize re-maps the anchor's bytes onto the new screen; it neither
// empties the copy nor loses its first row.
func TestYankSurvivesAPaneResize(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("old\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("\r\nfirst\r\nlast\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "resize", Cols: 20, Rows: 3})
	paneSync(t, cli, msgs)

	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if want := []string{"first", "last"}; !reflect.DeepEqual(yankCopy(res), want) {
		t.Fatalf("yank copy = %#v, want %#v (lines %#v)", yankCopy(res), want, res.Lines)
	}
}

// A last line with no trailing newline is still copied: the prompt sharing its
// row must not drop it.
func TestYankKeepsTheLastLineWithoutANewline(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("old\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("\r\nlast line"))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if want := []string{"last line"}; !reflect.DeepEqual(yankCopy(res), want) {
		t.Fatalf("yank copy = %#v, want %#v (lines %#v)", yankCopy(res), want, res.Lines)
	}
}
