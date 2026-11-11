package cli

// The dispatch table: each verb parses its flags, validates its arguments and
// sends the wire action a real server answers.

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"

	"dm/internal/paths"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// TestNewPrintsTheSession proves dm new answers with the session name.
func TestNewPrintsTheSession(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"session":"main"}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"new"}); err != nil {
			t.Fatalf("dm new: %v", err)
		}
	})
	if strings.TrimSpace(out) != "main" {
		t.Fatalf("dm new printed %q, want the session name", out)
	}
}

// TestExecValidation proves exec refuses a missing and a malformed action
// before anything leaves the process.
func TestExecValidation(t *testing.T) {
	t.Parallel()
	if err := Run([]string{"exec"}); err == nil || !strings.Contains(err.Error(), "needs a JSON action") {
		t.Fatalf("dm exec without JSON = %v", err)
	}
	if err := Run([]string{"exec", "{nope"}); err == nil || !strings.Contains(err.Error(), "bad action JSON") {
		t.Fatalf("dm exec with bad JSON = %v", err)
	}
}

// TestExecSendsTheAction proves exec forwards the parsed action and prints
// the reply pretty-printed.
func TestExecSendsTheAction(t *testing.T) {
	rec := &recorder{}
	startFakeServer(t, "main", func(c net.Conn, req wire.Msg) (wire.Msg, bool) {
		rec.handler()(c, req)
		return wire.Msg{OK: true, Res: json.RawMessage(`{"pane":"w1p1"}`)}, true
	})
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"exec", "--action-json", `{"action":"focus","target":"w1p1"}`}); err != nil {
			t.Fatalf("dm exec: %v", err)
		}
	})
	if got := rec.last(); got.Action != "focus" || got.Target != "w1p1" {
		t.Fatalf("exec sent %+v, want focus on w1p1", got)
	}
	if !strings.Contains(out, "w1p1") {
		t.Fatalf("exec printed %q, want the reply", out)
	}
}

// TestExecRefusal surfaces the server's error text.
func TestExecRefusal(t *testing.T) {
	startFakeServer(t, "main", refuseHandler("no such pane"))
	if err := Run([]string{"exec", `{"action":"focus"}`}); err == nil || err.Error() != "no such pane" {
		t.Fatalf("dm exec refused = %v, want the server error", err)
	}
}

// TestWriteToNeedsAPane proves the pane argument is checked before any
// request goes out, so the validation needs no server.
func TestWriteToNeedsAPane(t *testing.T) {
	t.Parallel()
	for _, argv := range [][]string{
		{"write-to"}, {"write"}, {"read"}, {"yank"}, {"split"}, {"focus"},
		{"rename-workspace", "w1"}, {"set-project"}, {"peek"}, {"msg"},
	} {
		if err := Run(argv); err == nil {
			t.Fatalf("dm %v without arguments was accepted", argv)
		}
	}
}

// TestReadJoinsAndTrims proves read prints the reply joined with newlines,
// cut to the last --lines rows.
func TestReadJoinsAndTrims(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"lines":["one","two","three"]}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"read", "w1p1"}); err != nil {
			t.Fatalf("dm read: %v", err)
		}
	})
	if out != "one\ntwo\nthree\n" {
		t.Fatalf("dm read printed %q", out)
	}
	out = testutil.CaptureStdout(t, func() {
		if err := Run([]string{"read", "w1p1", "--lines", "2"}); err != nil {
			t.Fatalf("dm read --lines: %v", err)
		}
	})
	if out != "two\nthree\n" {
		t.Fatalf("dm read --lines 2 printed %q, want the last two rows", out)
	}
}

// TestReadScrollbackAsksForHistory proves a non-negative --scrollback switches
// the action from read to history.
func TestReadScrollbackAsksForHistory(t *testing.T) {
	rec := &recorder{}
	startFakeServer(t, "main", rec.handler())
	testutil.CaptureStdout(t, func() {
		if err := Run([]string{"read", "w1p1", "--scrollback", "5"}); err != nil {
			t.Fatalf("dm read --scrollback: %v", err)
		}
	})
	if got := rec.last(); got.Action != "history" || got.Count != 5 || got.Target != "w1p1" {
		t.Fatalf("read sent %+v, want history with count 5", got)
	}
}

// TestReadRefusal surfaces the server's error text.
func TestReadRefusal(t *testing.T) {
	startFakeServer(t, "main", refuseHandler("pane went away"))
	if err := Run([]string{"read", "w1p1"}); err == nil || err.Error() != "pane went away" {
		t.Fatalf("dm read refused = %v", err)
	}
}

// TestYankRange proves yank refuses a reply whose window does not fit the
// lines it names.
func TestYankRange(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"lines":["a","b"],"start":5,"rows":2,"anchor":true}`))
	if err := Run([]string{"yank", "w1p1"}); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("yank past the end = %v, want a range error", err)
	}
}

// TestYankNeedsEnter proves a pane without an anchor is refused, not printed.
func TestYankNeedsEnter(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"lines":["a"],"anchor":false}`))
	if err := Run([]string{"yank", "w1p1"}); err == nil || !strings.Contains(err.Error(), "no Enter yet") {
		t.Fatalf("yank without anchor = %v", err)
	}
}

// TestYankCutsFromTheAnchor proves yank prints the rows from the anchor to
// the cut and drops the trailing blank rows.
func TestYankCutsFromTheAnchor(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"lines":["cmd","out1","out2",""],"start":1,"rows":4,"anchor":true}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"yank", "w1p1"}); err != nil {
			t.Fatalf("dm yank: %v", err)
		}
	})
	if out != "out1\nout2\n" {
		t.Fatalf("dm yank printed %q, want the rows after the anchor", out)
	}
}

// TestYankLinesTrims proves --lines cuts yank output to the last rows.
func TestYankLinesTrims(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"lines":["a","b","c"],"start":0,"rows":3,"anchor":true}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"yank", "w1p1", "--lines", "2"}); err != nil {
			t.Fatalf("dm yank --lines: %v", err)
		}
	})
	if out != "b\nc\n" {
		t.Fatalf("dm yank --lines 2 printed %q", out)
	}
}

// TestSplitPrintsTheNewPane proves split prints the pane id the server chose.
func TestSplitPrintsTheNewPane(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"pane":"w1p2"}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"split", "w1p1", "--dir", "right"}); err != nil {
			t.Fatalf("dm split: %v", err)
		}
	})
	if strings.TrimSpace(out) != "w1p2" {
		t.Fatalf("dm split printed %q, want the new pane id", out)
	}
}

// TestSplitJSONPrintsTheReply proves --json prints the reply object itself.
func TestSplitJSONPrintsTheReply(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"pane":"w1p2"}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"split", "w1p1", "--json"}); err != nil {
			t.Fatalf("dm split --json: %v", err)
		}
	})
	if !strings.Contains(out, `"pane"`) || !strings.Contains(out, "w1p2") {
		t.Fatalf("dm split --json printed %q", out)
	}
}

// TestFocusAndFriendsSendTheirActions proves the one-shot verbs carry their
// action and target untouched.
func TestFocusAndFriendsSendTheirActions(t *testing.T) {
	for _, tc := range []struct {
		argv   []string
		action string
		target string
		name   string
	}{
		{[]string{"focus", "w1p1"}, "focus", "w1p1", ""},
		{[]string{"rename-workspace", "w1", "api"}, "rename-workspace", "w1", "api"},
		{[]string{"set-project", "w1", "dm"}, "set-project", "w1", "dm"},
		{[]string{"set-project", "w1"}, "set-project", "w1", ""},
		{[]string{"next-pane"}, "next-pane", "", ""},
		{[]string{"prev-window"}, "prev-window", "", ""},
	} {
		rec := &recorder{}
		startFakeServer(t, "main", rec.handler())
		testutil.CaptureStdout(t, func() {
			if err := Run(tc.argv); err != nil {
				t.Fatalf("dm %v: %v", tc.argv, err)
			}
		})
		got := rec.last()
		if got.Action != tc.action || got.Target != tc.target || got.Name != tc.name {
			t.Fatalf("dm %v sent action %q target %q name %q, want %q %q %q",
				tc.argv, got.Action, got.Target, got.Name, tc.action, tc.target, tc.name)
		}
	}
}

// TestStatusBoard proves a targetless status prints every pane's row sorted
// by id.
func TestStatusBoard(t *testing.T) {
	startFakeServer(t, "main", okHandler(
		`{"w1p2":{"state":"idle"},"w1p1":{"state":"working","message":"on it"}}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"status"}); err != nil {
			t.Fatalf("dm status: %v", err)
		}
	})
	want := "w1p1\tworking\ton it\nw1p2\tidle\t\n"
	if out != want {
		t.Fatalf("dm status printed %q, want %q", out, want)
	}
}

// TestStatusOnePaneRow proves a named pane asks for that pane and prints one
// row from the reply.
func TestStatusOnePaneRow(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"state":"done","message":"merged"}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"status", "w1p1"}); err != nil {
			t.Fatalf("dm status w1p1: %v", err)
		}
	})
	if out != "w1p1\tdone\tmerged\n" {
		t.Fatalf("dm status w1p1 printed %q, want the status row", out)
	}
}

// TestStatusRefusal surfaces the server's error text.
func TestStatusRefusal(t *testing.T) {
	startFakeServer(t, "main", refuseHandler("no pane w9"))
	if err := Run([]string{"status", "w9"}); err == nil || err.Error() != "no pane w9" {
		t.Fatalf("dm status refused = %v", err)
	}
}

// TestPeekTrims proves peek prints the screen and cuts it to --lines.
func TestPeekTrims(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"lines":["r1","r2","r3"]}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"peek", "w1p1", "--lines", "2"}); err != nil {
			t.Fatalf("dm peek: %v", err)
		}
	})
	if out != "r2\nr3\n" {
		t.Fatalf("dm peek --lines 2 printed %q", out)
	}
}

// TestDashPrintsThePaletteRows proves dash lists every pane with a mark on
// the focused one.
func TestDashPrintsThePaletteRows(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"snapshot":{"session":"main","workspace":"main",
		"focus":"w1p1","all":[
		{"id":"w1p1","name":"alpha","status":"working","agent":true,"workspace":"s1","workspaceName":"main","window":"w1","windowName":"one","focus":true},
		{"id":"w1p2","app":"bash","workspace":"s1","workspaceName":"main","window":"w1","windowName":"one"}]}}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"dash"}); err != nil {
			t.Fatalf("dm dash: %v", err)
		}
	})
	if !strings.Contains(out, "w1p1 > ") {
		t.Fatalf("dm dash does not mark the focused pane: %q", out)
	}
	if !strings.Contains(out, "w1p2   ") {
		t.Fatalf("dm dash does not list the unfocused pane: %q", out)
	}
}

// TestListJSONPrintsTheReply proves --json passes the snapshot through
// untouched.
func TestListJSONPrintsTheReply(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"snapshot":{"session":"main","all":[{"id":"w1p1","workspace":"s1","window":"w1","focus":true}]}}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"list", "--json"}); err != nil {
			t.Fatalf("dm list --json: %v", err)
		}
	})
	if !strings.Contains(out, `"id":"w1p1"`) {
		t.Fatalf("dm list --json printed %q", out)
	}
}

// TestListTable proves the table prints a header, a star on the focused pane,
// and the app and status fallbacks.
func TestListTable(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"snapshot":{"session":"main","all":[
		{"id":"w1p1","agent":true,"workspace":"s1","workspaceName":"main","window":"w1","windowName":"one","focus":true},
		{"id":"w1p2","app":"bash","status":"idle","workspace":"s1","workspaceName":"main","window":"w1","windowName":"one","cwd":"/tmp/dm","branch":"main"}]}}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"list"}); err != nil {
			t.Fatalf("dm list: %v", err)
		}
	})
	for _, want := range []string{"PANE", "w1p1*", "agent", "-", "bash", "idle", "/tmp/dm", "main"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dm list table misses %q: %q", want, out)
		}
	}
}

// TestListWithoutASnapshot errors instead of printing an empty table.
func TestListWithoutASnapshot(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{}`))
	if err := Run([]string{"list"}); err == nil || !strings.Contains(err.Error(), "no snapshot") {
		t.Fatalf("dm list without a snapshot = %v", err)
	}
}

// TestKillWithoutAPidFile proves kill reports a session whose server never
// wrote a pid, instead of signalling nothing.
func TestKillWithoutAPidFile(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{}`))
	if err := Run([]string{"kill"}); err == nil || !strings.Contains(err.Error(), "no server pid") {
		t.Fatalf("dm kill without a pid file = %v", err)
	}
}

// TestManWriteError proves a bad output path is an error, not a silent
// success.
func TestManWriteError(t *testing.T) {
	t.Parallel()
	if err := Run([]string{"man", "-o", "/dev/null/no-such-dir/page"}); err == nil {
		t.Fatal("dm man -o to an unwritable path was accepted")
	}
}

// TestLsJSONEmpty proves an empty state tree prints an empty JSON array.
func TestLsJSONEmpty(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"ls", "--json"}); err != nil {
			t.Fatalf("dm ls --json: %v", err)
		}
	})
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("dm ls --json printed %q, want []", out)
	}
}

// TestLsTablePrintsDeadSessions proves the table prints one row per session
// on disk, with a dead session read from what it left behind.
func TestLsTablePrintsDeadSessions(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	if err := os.MkdirAll(paths.SessionDir("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"ls"}); err != nil {
			t.Fatalf("dm ls: %v", err)
		}
	})
	for _, want := range []string{"NAME", "STATE", "old", "dead"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dm ls table misses %q: %q", want, out)
		}
	}
}

// TestSplitRefusal surfaces the server's error text instead of the pane id.
func TestSplitRefusal(t *testing.T) {
	startFakeServer(t, "main", refuseHandler("no room to split"))
	if err := Run([]string{"split", "w1p1"}); err == nil || err.Error() != "no room to split" {
		t.Fatalf("dm split refused = %v, want the server error", err)
	}
}

// TestDashWithoutAServer proves the most common failure a user meets: no
// session running. The verb errors cleanly after the spawn retries give up,
// instead of hanging or panicking.
func TestDashWithoutAServer(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	err := Run([]string{"dash"})
	if err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("dm dash with no server = %v, want the did-not-start error", err)
	}
}
