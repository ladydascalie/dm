package pane

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// pasteSink is the pty a paste test writes to: it keeps every byte the pane
// hands the program, so the test reads the frame whole. Nothing reads from it,
// because the pane's own read loop never starts here.
type pasteSink struct {
	mu  sync.Mutex
	buf []byte
}

func (s *pasteSink) Read([]byte) (int, error) { return 0, io.EOF }

func (s *pasteSink) Write(b []byte) (int, error) {
	s.mu.Lock()
	s.buf = append(s.buf, b...)
	s.mu.Unlock()
	return len(b), nil
}

func (s *pasteSink) Close() error { return nil }

func (s *pasteSink) Resize(int, int) error { return nil }

func (s *pasteSink) bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.buf...)
}

// pastePane is a pane daemon whose pty keeps what the pane writes, plus a
// socket the test drives.
func pastePane(t *testing.T) (*paneProc, net.Conn, chan wire.Msg, *pasteSink) {
	t.Helper()
	pty := &pasteSink{}
	p := &paneProc{
		id: "w1p1", vt: vt.NewTerm(40, 10), ring: NewRing(ringDefault),
		subs: map[*subConn]bool{}, st: wire.Status{State: "idle"}, ptmx: pty,
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
	return p, cli, msgs, pty
}

// pasteWait waits for the pty to hold want and no more, and fails with what the
// program actually received.
func pasteWait(t *testing.T, pty *pasteSink, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := string(pty.bytes()); got == want {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("the program received %q, want %q", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPasteIsFramedForABracketedPasteProgram proves a program that asked for
// bracketed paste receives the block between the 200~ and 201~ markers, so its
// line editor takes the block whole instead of running each line.
func TestPasteIsFramedForABracketedPasteProgram(t *testing.T) {
	t.Parallel()
	p, cli, msgs, pty := pastePane(t)
	p.mu.Lock()
	p.publishLocked([]byte("\x1b[?2004h"))
	p.mu.Unlock()

	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: "one\ntwo", Paste: true, ID: 1})
	paneSync(t, cli, msgs)
	pasteWait(t, pty, "\x1b[200~one\ntwo\x1b[201~")
}

// TestPasteArrivesBareForAPlainProgram proves a program that never asked for
// bracketed paste gets the text alone: the markers would reach it as keys and
// leave their tail on its line.
func TestPasteArrivesBareForAPlainProgram(t *testing.T) {
	t.Parallel()
	_, cli, msgs, pty := pastePane(t)
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: "one\ntwo", Paste: true, ID: 1})
	paneSync(t, cli, msgs)
	pasteWait(t, pty, "one\ntwo")
}

// TestPasteNeverEnters proves a paste never submits: a frame that asks for an
// enter with its paste still leaves the program without one, so a block lands
// whole and waits for the send control's own enter.
func TestPasteNeverEnters(t *testing.T) {
	t.Parallel()
	_, cli, msgs, pty := pastePane(t)
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: "one\ntwo", Paste: true, Enter: true, ID: 1})
	paneSync(t, cli, msgs)
	pasteWait(t, pty, "one\ntwo")
}

// TestPasteKeepsTheTypedEcho proves a paste records no echo: the echo a yank
// drops belongs to the command line the reader typed, and a pasted block is not
// one, so the typed command stays the echo the next yank strips.
func TestPasteKeepsTheTypedEcho(t *testing.T) {
	t.Parallel()
	p, cli, msgs, _ := pastePane(t)
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: "typed", Enter: true, ID: 1})
	paneSync(t, cli, msgs)
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: "block\nblock", Paste: true, ID: 2})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	echo := string(p.echo)
	p.mu.Unlock()
	if echo != "typed" {
		t.Fatalf("echo = %q, want the typed command kept: a paste is not a line to strip", echo)
	}
}
