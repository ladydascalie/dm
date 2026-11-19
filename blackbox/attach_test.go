package blackbox

// The attach surface end to end: a real dm attach client under a pty the test
// owns. These are the proofs the raw-frame fakes cannot give: the client
// renders the pane's screen, forwards typed keys to the pane, opens the
// switcher, and detaches without taking the server down. Two clients share one
// session and both see a split that happens after they attached.

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// attachTerm is one dm attach client running under a pty. The reader goroutine
// keeps every byte the client wrote, in order, so a frame split across reads
// still matches as one string.
type attachTerm struct {
	cmd     *exec.Cmd
	master  io.WriteCloser
	mu      sync.Mutex
	out     bytes.Buffer
	readErr error
}

// startAttach spawns dm attach for the session under a fresh pty, sized
// 100x30, and reads the master from a goroutine for the life of the test. TERM
// is appended because the last value wins for a duplicated key, so the render
// does not depend on the terminal go test ran under. The harness env goes
// through untouched: stripping the dm overrides here would point the attach at
// the default state root instead of the test's own.
func startAttach(t *testing.T, h *harness, session string) *attachTerm {
	t.Helper()
	return startAttachArgs(t, h, "attach", "-s", session)
}

// startAttachArgs is startAttach with the client's own argv, so a test reaches
// the bare-word fallback or the -L layout flag instead of one fixed verb.
func startAttachArgs(t *testing.T, h *harness, args ...string) *attachTerm {
	t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Env = append(h.env[:len(h.env):len(h.env)], "TERM=xterm-256color")
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("attach %s: %v", strings.Join(args, " "), err)
	}
	a := &attachTerm{cmd: cmd, master: master}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = master.Close()
	})
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := master.Read(buf)
			a.mu.Lock()
			a.out.Write(buf[:n])
			if err != nil {
				a.readErr = err
			}
			a.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return a
}

// write sends bytes to the client as if the operator typed them.
func (a *attachTerm) write(s string) {
	// A write to a dead client fails the next wait instead of the test here.
	_, _ = io.WriteString(a.master, s)
}

// waitContains polls the client's output until it carries needle.
func (a *attachTerm) waitContains(t *testing.T, needle string) {
	t.Helper()
	start := time.Now()
	deadline := start.Add(waitBudget)
	for {
		a.mu.Lock()
		got := a.out.String()
		a.mu.Unlock()
		if strings.Contains(got, needle) {
			return
		}
		if time.Now().After(deadline) {
			tail := got
			if len(tail) > 600 {
				tail = tail[len(tail)-600:]
			}
			t.Fatalf("attach output never showed %q within the %s wait budget (waited %s); %d bytes, readErr=%v, tail:\n%q",
				needle, waitBudget, time.Since(start).Round(time.Millisecond), len(got), a.readErr, tail)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// pause gives the client a beat between keystrokes. The switcher's close and
// the next prefix sequence must not land in one input pass: a close that is
// still in flight would eat the prefix byte as its own close key and leave the
// bare key to fall through to the pane.
func (a *attachTerm) pause(d time.Duration) { time.Sleep(d) }

// waitExit waits for the client process to end and fails the test when it
// stays alive past the budget.
func (a *attachTerm) waitExit(t *testing.T, budget time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- a.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(budget):
		a.mu.Lock()
		tail := a.out.String()
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		a.mu.Unlock()
		t.Fatalf("attach did not exit within %s; tail:\n%q", budget, tail)
	}
}

// firstPaint is the synchronized-update marker every client frame starts
// with. A client that has written it has connected, synced and painted once,
// so pane output from here on streams into a live client.
const firstPaint = "\x1b[?2026h"

// prefixKey is the default prefix binding: ctrl+b, the sequence every
// main-keymap action is reached through.
const prefixKey = "\x02"

// TestAttachClientEndToEnd drives one real attach client: it renders the
// pane's screen as the pane produces output, typed keys reach the pane's
// shell, the switcher opens over the pane, and detach ends the client while
// the server keeps the session.
func TestAttachClientEndToEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "att")
	root := h.focus("att")

	a := startAttach(t, h, "att")
	a.waitContains(t, firstPaint)

	// The client renders what the pane prints once it is watching.
	h.run("write-to", root, "-s", "att", "--msg", "echo ATTACH-SEEN", "--enter")
	a.waitContains(t, "ATTACH-SEEN")

	// Keys typed at the client reach the pane's shell: the marker shows up in
	// the pane's screen as the server sees it, not only in the client's frame.
	a.write("echo ATTACH-KEYS\r")
	start := time.Now()
	deadline := start.Add(waitBudget)
	for {
		screen, err := h.try("read", root, "-s", "att")
		if err == nil && strings.Contains(screen, "ATTACH-KEYS") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed the typed keys (waited %s); last read:\n%s",
				time.Since(start).Round(time.Millisecond), screen)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// The switcher opens on the default binding, prefix then a.
	a.write(prefixKey + "a")
	a.waitContains(t, " switch ")
	a.waitContains(t, "press / to filter")
	// Close with ctrl+c: a lone esc byte can pair with the next byte in the
	// input parser and reach the client as an escape sequence instead.
	a.write("\x03")
	a.pause(300 * time.Millisecond)

	// Detach ends the client, not the session.
	a.write(prefixKey + "d")
	a.waitExit(t, 15*time.Second)
	tree := h.rpc("att", map[string]any{"action": "list"})
	if focus := tree["snapshot"].(map[string]any)["focus"]; focus != root {
		t.Fatalf("after detach focus = %v, want %q", focus, root)
	}
}

// TestAttachTwoClientsShareTheSession proves a snapshot change reaches every
// attached client: a split made after both attached shows up in both frames,
// and one client detaching leaves the other and the server working.
func TestAttachTwoClientsShareTheSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "duo")
	root := h.focus("duo")

	a := startAttach(t, h, "duo")
	b := startAttach(t, h, "duo")
	a.waitContains(t, firstPaint)
	b.waitContains(t, firstPaint)

	// Both clients render the pane's output, wherever it lands after they
	// attached.
	h.run("write-to", root, "-s", "duo", "--msg", "echo DUO-ROOT", "--enter")
	a.waitContains(t, "DUO-ROOT")
	b.waitContains(t, "DUO-ROOT")

	// The split broadcasts: both clients end up drawing both panes, so the
	// new pane's marker shows in both frames.
	p2 := h.split("duo", root, "down")
	h.run("write-to", p2, "-s", "duo", "--msg", "echo DUO-SPLIT", "--enter")
	h.waitFor("duo", p2, "DUO-SPLIT")
	a.waitContains(t, "DUO-SPLIT")
	b.waitContains(t, "DUO-SPLIT")

	// One client detaches. The other keeps rendering and the server answers.
	a.write(prefixKey + "d")
	a.waitExit(t, 15*time.Second)
	h.run("write-to", root, "-s", "duo", "--msg", "echo DUO-AFTER", "--enter")
	b.waitContains(t, "DUO-AFTER")
	if focus := h.focus("duo"); focus == "" {
		t.Fatal("the server did not survive the detach")
	}
}
