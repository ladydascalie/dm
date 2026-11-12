package blackbox

// The extensibility lane's end-to-end proofs: a config command block reaches
// the box through a floating command, and an argv split pane respawns its
// program after a server restart.

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"dm/internal/wire"
)

// configDir is the harness's isolated config root, so a test can drop a
// config.kdl the daemon reads.
func (h *harness) configDir() string {
	for _, e := range h.env {
		if strings.HasPrefix(e, "XDG_CONFIG_HOME=") {
			return strings.TrimPrefix(e, "XDG_CONFIG_HOME=")
		}
	}
	h.t.Fatal("harness env has no XDG_CONFIG_HOME")
	return ""
}

// floatOpen sends a float-open request over a socket it keeps open, the way
// the client's key path does, and returns the connection and the reply once
// the server answers OK. Keeping the connection open lets the float's command
// outlive the round trip. The reply must echo the command and the label.
func floatOpen(t *testing.T, h *harness, session string, v map[string]any) (net.Conn, wire.Msg) {
	t.Helper()
	raw, _ := json.Marshal(v)
	var req wire.Msg
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("float-open payload: %v", err)
	}
	req.Type = "req"
	req.ID = 1
	sock := filepath.Join(h.stateDir(), "sessions", session, "server.sock")
	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", sock, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := wire.WriteMsg(c, req); err != nil {
		t.Fatalf("float-open send: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		m, err := wire.ReadMsg(c)
		if err != nil {
			t.Fatalf("float-open reply: %v", err)
		}
		if m.Type != "resp" || m.ID != 1 {
			continue // the float's own bytes and exit frame
		}
		if !m.OK {
			t.Fatalf("float-open refused: %s", m.Error)
		}
		return c, m
	}
}

// waitBox polls the session box until a line carries needle. The float posts
// into the same server the float opened on, so the marker appearing is the
// proof the floated command ran.
func waitBox(t *testing.T, h *harness, session, needle string) {
	t.Helper()
	start := time.Now()
	deadline := start.Add(waitBudget)
	var last string
	for time.Now().Before(deadline) {
		out, err := h.try("msg", "list", "-s", session)
		last = out
		if err == nil && strings.Contains(out, needle) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("box never showed %q within the %s wait budget (waited %s); last list:\n%s",
		needle, waitBudget, time.Since(start).Round(time.Millisecond), last)
}

// TestCommandFloatEndToEnd drives the command: key path through the RPC. The
// config defines one command and a main binding; the client's key press would
// send float-open with the command text and the name as the label. The float's
// view is client-side, so the headless harness presses that request and reads
// the box the command posts into. The daemon parses the config at startup, so
// the server log must not carry a keymap error.
func TestCommandFloatEndToEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := "extfloat"

	// One command block and a main binding, the shape the command: key path
	// dispatches. The program posts a marker into the session box, using the
	// session the float's environment carries.
	cmd := strings.Join([]string{
		h.bin, "msg", "post", "--key", "lane/float", "--state", "done",
		"--msg", "MARK-FLOAT", "-s", "$DM_SESSION",
	}, " ")
	cfg := "command \"mark-float\" {\n\trun \"" + cmd + "\"\n}\n" +
		"keymap \"main\" {\n\t\"command:mark-float\" \"m\"\n}\n"
	if err := os.MkdirAll(filepath.Join(h.configDir(), "dm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.configDir(), "dm", "config.kdl"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	h.run("new", "-s", s)
	root := h.focus(s)

	// Press the key path through the RPC: float-open with the command and the
	// name as the label, over a socket that stays up while the float runs.
	conn, resp := floatOpen(t, h, s, map[string]any{
		"action": "float-open",
		"target": root,
		"float":  "f1",
		"cols":   80, "rows": 24,
		"res": map[string]any{"command": cmd, "label": "mark-float"},
	})
	defer func() { _ = conn.Close() }()
	if !strings.Contains(string(resp.Res), cmd) || !strings.Contains(string(resp.Res), "mark-float") {
		t.Fatalf("float-open reply = %s, want the command and label echoed", resp.Res)
	}

	// The floated command posts the marker into the box.
	waitBox(t, h, s, "MARK-FLOAT")

	// The config the daemon parsed carried the command block; a failed parse
	// would have logged a keymap error to the server log instead.
	log, err := os.ReadFile(filepath.Join(h.stateDir(), "sessions", s, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "keymap:") {
		t.Fatalf("server log reports a config error:\n%s", log)
	}
}

// TestArgvSplitRespawns drives the argv feature end to end: a split carries
// the program, the pane prints its marker, and after the server, the pane's
// process and its socket are taken away, the next server respawns the same
// pane id with the same program and the marker prints again.
func TestArgvSplitRespawns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := "argvsp"
	h.run("new", "-s", s)
	root := h.focus(s)

	res := h.rpc(s, map[string]any{
		"action": "split",
		"target": root,
		"dir":    "right",
		"argv":   []string{"sh", "-c", "echo ARGVPANE; sleep 30"},
	})
	argvPane, _ := res["pane"].(string)
	if argvPane == "" {
		t.Fatalf("split with argv returned no pane: %v", res)
	}
	h.waitFor(s, argvPane, "ARGVPANE")

	// Take the server away, then the pane's process and its socket too, so
	// the next server cannot adopt it and must respawn the program from the
	// pane's state.
	serverPID := readPID(t, filepath.Join(h.stateDir(), "sessions", s, "server.pid"))
	if err := syscall.Kill(serverPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	panePIDFile := filepath.Join(h.stateDir(), "sessions", s, "panes", argvPane+".sock.pid")
	panePID := readPID(t, panePIDFile)
	_ = syscall.Kill(panePID, syscall.SIGKILL)
	_ = os.Remove(filepath.Join(h.stateDir(), "sessions", s, "panes", argvPane+".sock"))
	time.Sleep(300 * time.Millisecond)

	// A fresh RPC restarts the server. The pane comes back under the same id
	// with a fresh process, and the marker prints again.
	h.waitFor(s, argvPane, "ARGVPANE")
	if got := readPID(t, filepath.Join(h.stateDir(), "sessions", s, "server.pid")); got == serverPID {
		t.Fatal("the server did not restart")
	}
	if got := readPID(t, panePIDFile); got == panePID {
		t.Fatalf("pane process %d was not respawned", panePID)
	}
	found := false
	for _, id := range h.panes(s) {
		if id == argvPane {
			found = true
		}
	}
	if !found {
		t.Fatalf("pane %s is gone after the restart", argvPane)
	}
}
