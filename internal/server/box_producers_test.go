package server

import (
	"net"
	"os"
	"strings"
	"testing"

	"dm/internal/box"
	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/wire"
)

// twoPaneSession puts two panes into the active window and returns their ids.
// The first one holds the focus.
func twoPaneSession(t *testing.T, s *server) (string, string) {
	t.Helper()
	w := s.st.ActiveWindow()
	a, b := w.NewPaneID(), w.NewPaneID()
	w.Root = &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: a}, {Pane: b}}}
	w.Focus = a
	s.st.Panes[a] = &session.PaneState{}
	s.st.Panes[b] = &session.PaneState{}
	return a, b
}

// paneConnFor plants a live-looking pane connection whose last status message
// is msg, so paneExited has a pane to retire.
func paneConnFor(t *testing.T, s *server, id, msg string) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
	})
	s.panes[id] = &paneConn{id: id, c: b, st: wire.Status{State: "working", Message: msg}}
}

// notifyEntries lists the entries logged under the notify topic so far.
func notifyEntries(t *testing.T, s *server) []box.Entry {
	t.Helper()
	entries, _, err := s.boxList(&box.ListReq{KeyPrefix: "notify/", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// A pane state change the pane machine calls worth interrupting posts one
// entry under the pane's notify topic and, with no client attached, pops.
func TestNotifyPaneStatePostsAndDispatches(t *testing.T) {
	dir, log := fakeNotifySend(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_NOTIFY", "")
	s := boxServer(t)
	focus := s.st.ActiveWindow().Focus

	s.notifyPaneState(focus, "blocked", "needs review")
	lines := waitNotifySend(t, log, 1)
	if !strings.Contains(lines[0], "notify/"+focus+" blocked") || !strings.Contains(lines[0], "needs review") {
		t.Fatalf("dispatch = %q, want the topic, state and msg", lines[0])
	}
	entries := notifyEntries(t, s)
	if len(entries) != 1 {
		t.Fatalf("logged = %+v, want one entry", entries)
	}
	if entries[0].Key != "notify/"+focus || string(entries[0].State) != "blocked" || entries[0].Msg != "needs review" {
		t.Fatalf("entry = %+v, want the state change on the pane's topic", entries[0])
	}
}

// The same post for the pane the operator is looking at pops nothing and
// still logs.
func TestNotifyPaneStateSuppressedWhenFocused(t *testing.T) {
	dir, log := fakeNotifySend(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_NOTIFY", "")
	s := boxServer(t)
	focus := s.st.ActiveWindow().Focus
	cl, _ := boxConn(t)
	t.Cleanup(cl.shutdown)
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()

	s.notifyPaneState(focus, "blocked", "needs review")
	noNotifySend(t, log, 0)
	entries := notifyEntries(t, s)
	if len(entries) != 1 {
		t.Fatalf("logged = %+v, want one entry", entries)
	}
}

// A pane exit with a survivor logs one entry under the pane's notify topic.
// The exit rides note, and note is not urgent, so a headless session pops
// nothing; the entry is logged either way.
func TestPaneExitPostsEntry(t *testing.T) {
	dir, log := fakeNotifySend(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_NOTIFY", "")
	s := boxServer(t)
	gone, _ := twoPaneSession(t, s)
	paneConnFor(t, s, gone, "building x")

	s.paneExited(gone)
	entries := notifyEntries(t, s)
	if len(entries) != 1 {
		t.Fatalf("logged = %+v, want one entry", entries)
	}
	if entries[0].Key != "notify/"+gone || entries[0].State != box.StateNote || entries[0].Msg != "exited: building x" {
		t.Fatalf("entry = %+v, want the exit on the pane's topic", entries[0])
	}
	noNotifySend(t, log, 0)
	if s.countPanes() != 1 {
		t.Fatalf("panes left = %d, want the survivor", s.countPanes())
	}
}

// The last pane's exit logs the same entry without popping; the session ends
// after. endSession's process exit is stubbed, so the test survives the
// shutdown it runs.
func TestLastPaneExitPostsEntry(t *testing.T) {
	exitCode := make(chan int, 1)
	endSessionExit = func(code int) { exitCode <- code }
	t.Cleanup(func() { endSessionExit = os.Exit })
	dir, log := fakeNotifySend(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_NOTIFY", "")
	s := boxServer(t)
	id := s.st.ActiveWindow().Focus
	paneConnFor(t, s, id, "done for now")

	s.paneExited(id)
	entries := notifyEntries(t, s)
	if len(entries) != 1 {
		t.Fatalf("logged = %+v, want one entry", entries)
	}
	if entries[0].Key != "notify/"+id || entries[0].State != box.StateNote || entries[0].Msg != "exited: done for now" {
		t.Fatalf("entry = %+v, want the exit on the pane's topic", entries[0])
	}
	noNotifySend(t, log, 0)
	s.mu.Lock()
	ended := s.ending
	s.mu.Unlock()
	if !ended {
		t.Fatal("the last pane's exit left the session alive")
	}
	select {
	case code := <-exitCode:
		if code != 0 {
			t.Fatalf("endSession exit code = %d, want 0", code)
		}
	default:
		t.Fatal("endSession never exited")
	}
}

// An exit the operator watched pops nothing; the entry still logs.
func TestPaneExitSuppressedWhenFocused(t *testing.T) {
	dir, log := fakeNotifySend(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_NOTIFY", "")
	s := boxServer(t)
	gone, _ := twoPaneSession(t, s)
	paneConnFor(t, s, gone, "building x")
	cl, _ := boxConn(t)
	t.Cleanup(cl.shutdown)
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()

	s.paneExited(gone)
	noNotifySend(t, log, 0)
	entries := notifyEntries(t, s)
	if len(entries) != 1 {
		t.Fatalf("logged = %+v, want one entry", entries)
	}
	if entries[0].Msg != "exited: building x" {
		t.Fatalf("entry = %+v, want the exit logged", entries[0])
	}
}
