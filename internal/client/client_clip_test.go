package client

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"dm/internal/testutil"
)

// settleClip pins the clipboard probe state for one test and restores it on
// the way out. The state is package scope, so a test that touches it must not
// run in parallel with another that does.
func settleClip(t *testing.T, mode clipMode, probeAt time.Time) {
	t.Helper()
	oldMode, oldAt := clipWrite, clipProbeAt
	clipWrite, clipProbeAt = mode, probeAt
	t.Cleanup(func() { clipWrite, clipProbeAt = oldMode, oldAt })
}

// An OSC 52 reply at the head of the buffer is a complete clipboard reply.
func TestTakeOSCReplyClipComplete(t *testing.T) {
	kind, value, n, started := takeOSCReply([]byte("\x1b]52;c;aGVsbG8=\x1b\\"))
	if !started || kind != 52 {
		t.Fatalf("started=%t kind=%d, want true 52", started, kind)
	}
	if value != "aGVsbG8=" || n != len("\x1b]52;c;aGVsbG8=\x1b\\") {
		t.Fatalf("value=%q n=%d, want aGVsbG8= and the full length", value, n)
	}
}

// A reply may end on the bell instead of the string terminator.
func TestTakeOSCReplyClipBell(t *testing.T) {
	kind, _, n, started := takeOSCReply([]byte("\x1b]52;c;\x07"))
	if !started || kind != 52 || n != len("\x1b]52;c;\x07") {
		t.Fatalf("started=%t kind=%d n=%d, want true 52 and the full length", started, kind, n)
	}
}

// A reply that is still arriving holds the head of the buffer.
func TestTakeOSCReplyClipPartial(t *testing.T) {
	for _, part := range [][]byte{{0x1b, ']'}, {0x1b, ']', '5', '2'}, []byte("\x1b]52"), []byte("\x1b]52;"), []byte("\x1b]52;c"), []byte("\x1b]52;c;")} {
		_, _, n, started := takeOSCReply(part)
		if !started || n != 0 {
			t.Fatalf("partial %q: started=%t n=%d, want true 0", part, started, n)
		}
	}
}

// The bulk path stops before a clipboard reply that shares a read with a
// keystroke, exactly as it does for a colour reply.
func TestOSCReplyAtFindsClip(t *testing.T) {
	if at := oscReplyAt([]byte("k\x1b]52;c;\x1b\\")); at != 1 {
		t.Fatalf("oscReplyAt = %d, want 1", at)
	}
	tail := []byte("k\x1b]52;")
	if at := oscReplyTail(tail); at != 1 {
		t.Fatalf("oscReplyTail = %d, want 1", at)
	}
}

// A clipboard reply settles the probe as spoken-for, whatever the deadline
// says.
func TestNoteClipReplySettlesTheProbe(t *testing.T) {
	settleClip(t, clipUnknown, time.Now().Add(-clipProbeWait))
	tui, _ := clientTUI(t, nil)
	tui.noteOSCReply(52, "aGVsbG8=")
	if clipWrite != clipOSC52 {
		t.Fatalf("clipWrite = %d, want clipOSC52", clipWrite)
	}
	if got := clipResolve(time.Now()); got != clipOSC52 {
		t.Fatalf("clipResolve = %d, want clipOSC52", got)
	}
}

// A probe whose window passes with no reply settles as external.
func TestClipResolveTimesOutToExternal(t *testing.T) {
	settleClip(t, clipUnknown, time.Now().Add(-clipProbeWait))
	if got := clipResolve(time.Now()); got != clipExternal {
		t.Fatalf("clipResolve = %d, want clipExternal", got)
	}
}

// A probe still inside its window stays unknown, and an unknown write rides
// OSC 52: a terminal that drops the sequence drops it silently.
func TestClipUnknownWritesOSC52(t *testing.T) {
	settleClip(t, clipUnknown, time.Now())
	tui, _ := clientTUI(t, nil)
	out := testutil.CaptureStdout(t, func() { tui.writeClipboard("hi") })
	if !strings.Contains(out, osc52("hi")) {
		t.Fatalf("clipboard = %q, want %q", out, osc52("hi"))
	}
}

// A settled external write goes to the platform tool, not to the terminal.
// This host has no carrier in the test PATH when the probe fails, so the
// write becomes a toast and the terminal sees nothing.
func TestClipExternalWritesToTheTool(t *testing.T) {
	settleClip(t, clipExternal, time.Now())
	tui, _ := clientTUI(t, nil)
	out := testutil.CaptureStdout(t, func() { tui.writeClipboard("hi") })
	if strings.Contains(out, osc52("hi")) {
		t.Fatalf("clipboard = %q, want no OSC 52", out)
	}
}

// clipTools keeps only the carriers the host has, in preference order.
func TestClipTools(t *testing.T) {
	found := map[string]bool{"pbcopy": true, "wl-copy": true}
	look := func(name string) (string, error) {
		if found[name] {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	got := clipTools("darwin", look)
	if len(got) != 1 || got[0].name != "pbcopy" {
		t.Fatalf("darwin tools = %+v, want pbcopy only", got)
	}
	got = clipTools("linux", look)
	if len(got) != 1 || got[0].name != "wl-copy" || len(got[0].args) != 0 {
		t.Fatalf("linux tools = %+v, want wl-copy only", got)
	}
	found["xclip"] = true
	got = clipTools("linux", look)
	if len(got) != 2 || got[1].name != "xclip" || !reflect.DeepEqual(got[1].args, []string{"-selection", "clipboard"}) {
		t.Fatalf("linux tools = %+v, want wl-copy then xclip", got)
	}
	if got := clipTools("linux", func(string) (string, error) { return "", exec.ErrNotFound }); got != nil {
		t.Fatalf("tools = %+v, want none", got)
	}
}

// clipQuery is the OSC 52 read probe, and under tmux it rides the passthrough.
func TestClipQuery(t *testing.T) {
	t.Setenv("TMUX", "")
	if got, want := clipQuery(), "\x1b]52;c;?\x1b\\"; got != want {
		t.Fatalf("clipQuery = %q, want %q", got, want)
	}
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	want := "\x1bPtmux;\x1b\x1b]52;c;?\x1b\x1b\\\x1b\\"
	if got := clipQuery(); got != want {
		t.Fatalf("tmux clipQuery = %q, want %q", got, want)
	}
}

// A harness with a clipboard of its own takes the text, so a page with no
// terminal can copy, and the caller is told the copy landed.
func TestWriteClipboardUsesTheHarnessSeam(t *testing.T) {
	settleClip(t, clipExternal, time.Time{})
	var got string
	tui := &tui{clipboard: func(text string) bool { got = text; return true }}
	if !tui.writeClipboard("hello") {
		t.Fatal("writeClipboard = false, want true")
	}
	if got != "hello" {
		t.Fatalf("clipboard got %q, want hello", got)
	}
	if len(tui.toasts) != 0 {
		t.Fatalf("toasts = %v, want none", tui.toasts)
	}
}

// A harness that refuses the copy is reported, and the caller must not go on
// to say "copied".
func TestWriteClipboardReportsARefusedSeam(t *testing.T) {
	settleClip(t, clipExternal, time.Time{})
	tui := &tui{clipboard: func(string) bool { return false }}
	if tui.writeClipboard("hello") {
		t.Fatal("writeClipboard = true, want false")
	}
	if len(tui.toasts) != 1 || !strings.Contains(tui.toasts[0].text, "clipboard") {
		t.Fatalf("toasts = %v, want one about the clipboard", tui.toasts)
	}
}
