package client

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/wire"
)

// The depth seam: a harness that names its renderer's colour depth gets that
// depth, with no environment to detect. A browser page has no COLORTERM, so
// this is the only way it can ask for the colours its canvas can show.

// attachAtDepth runs one attached session at depth d over a fake server and
// returns the bytes the client painted. Both runs see the same session, so the
// only difference between them is the depth.
func attachAtDepth(t *testing.T, d theme.Depth) string {
	t.Helper()
	// Attach loads the keymap and the theme from the config path, which writes
	// a file when it is missing. Keep the operator's own config out of it.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

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

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = Attach(Harness{
			Conn: clientConn, Out: out, Keys: keys, Winch: winch,
			Size: size.get, Session: "seam", Depth: &d,
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

// settle waits until the writer has been quiet long enough for a frame to be
// complete.
func settle(t *testing.T, out *syncBuffer) {
	t.Helper()
	last := -1
	for stable := 0; stable < 5; {
		if n := out.Len(); n != last {
			last, stable = n, 0
		} else {
			stable++
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAttachDepthSeam proves the harness's depth reaches the chrome: the same
// session paints truecolour escapes when the harness asks for truecolour and
// palette escapes when it asks for sixteen colours.
func TestAttachDepthSeam(t *testing.T) {
	old := theme.CurrentDepth()
	t.Cleanup(func() { theme.SetDepth(old) })

	th := theme.DefaultTheme()
	railBG := th.Palette.RailBG

	deep := attachAtDepth(t, theme.TrueColor)
	if want := "48;2;" + strconv.Itoa(railBG.R) + ";" + strconv.Itoa(railBG.G) + ";" + strconv.Itoa(railBG.B); !strings.Contains(deep, want) {
		t.Fatalf("truecolour frame carries no %q: the rail background never reached 24-bit", want)
	}

	shallow := attachAtDepth(t, theme.Color16)
	if shallow == "" {
		t.Fatal("the 16-colour frame is empty")
	}
	for _, bad := range []string{"38;2;", "48;2;", "38;5;", "48;5;"} {
		if strings.Contains(shallow, bad) {
			t.Fatalf("16-colour frame carries %q", bad)
		}
	}
	// The same rail row, at the depth the harness named: the rail text on the
	// rail background, as bright white on black.
	if want := "\x1b[0;97;40m"; !strings.Contains(shallow, want) {
		t.Fatalf("16-colour frame carries no %q: the rail never reached the ANSI palette", want)
	}
}
