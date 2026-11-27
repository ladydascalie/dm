package client

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/wire"
)

// The keymap seam: a harness that supplies its keymap never reads a config path,
// so a page with no HOME and no filesystem boots with no load error on the bar.

// attachFrames runs one attached session over a fake server with km as the
// keymap and returns the bytes the client painted. A nil km is the file load,
// the way Run does it.
func attachFrames(t *testing.T, km *keymap.Config) string {
	t.Helper()
	out := &syncBuffer{}
	size := &fakeSize{cols: 80, rows: 24}
	keys := make(chan []byte, 1)
	winch := make(chan os.Signal, 1)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	go func() {
		for {
			m, err := wire.ReadMsg(serverConn)
			if err != nil {
				return
			}
			if m.Type != "attach" && m.Type != "resize" {
				continue
			}
			snap := testutil.TestSnapshot(&layout.Node{Pane: "a"}, m.Cols, m.Rows, nil)
			snap.Session, snap.Workspace, snap.Focus = "seam", "main", "a"
			_ = wire.WriteMsg(serverConn, wire.Msg{Type: "snapshot", Snap: snap})
		}
	}()

	d := theme.TrueColor
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = Attach(Harness{
			Conn: clientConn, Out: out, Keys: keys, Winch: winch,
			Size: size.get, Session: "seam", Depth: &d, Keymap: km,
		})
	}()

	waitBytes(t, out, "the first frame")
	// A frame lands as more than one write, so wait for the buffer to settle
	// before closing the key channel and reading it.
	settle(t, out)
	close(keys)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the client did not return when its key channel closed")
	}
	return out.b.String()
}

// TestAttachKeymapSeamSkipsTheConfigPath proves the seam removes the boot toast
// the tour showed: with no HOME the config path cannot be resolved, so the file
// load toasts and the supplied keymap does not.
func TestAttachKeymapSeamSkipsTheConfigPath(t *testing.T) {
	// A page has no HOME. The same empty environment reaches both runs.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	withSeam := attachFrames(t, keymap.LoadDefault())
	if strings.Contains(withSeam, "config error") {
		t.Fatal("the harness keymap still read the config path and toasted its error")
	}

	// The same session with no seam does toast it, so the check above proves
	// the seam removed the toast and not that the frame is empty.
	without := attachFrames(t, nil)
	if !strings.Contains(without, "config error") {
		t.Fatal("the file load did not toast, so the test proves nothing")
	}
}
