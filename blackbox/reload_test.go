package blackbox

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// reloadMarker is program output, never a prompt. A bare row of it proves the
// pane's own text survived every reload.
const reloadMarker = "RELOAD-PADDING-MARKER"

// reloadConfigPath is the config file the harness session reads. newHarness
// gives each test its own XDG_CONFIG_HOME, so the operator's file is never
// touched.
func reloadConfigPath(t *testing.T, h *harness) string {
	t.Helper()
	var cfgDir string
	for _, e := range h.env {
		if strings.HasPrefix(e, "XDG_CONFIG_HOME=") {
			cfgDir = strings.TrimPrefix(e, "XDG_CONFIG_HOME=")
			break
		}
	}
	if cfgDir == "" {
		t.Fatal("harness env carries no XDG_CONFIG_HOME")
	}
	dir := filepath.Join(cfgDir, "dm")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "config.kdl")
}

// writeReloadConfig replaces the config in one rename, so a reload never reads
// a half-written file.
func writeReloadConfig(t *testing.T, path, src string) {
	t.Helper()
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		t.Fatal(err)
	}
	tmp := f.Name()
	if _, err := f.WriteString(src); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func writeReloadPadding(t *testing.T, path string, pad int) {
	t.Helper()
	src := "prefix \"ctrl+b\"\n"
	src += "padding " + strconv.Itoa(pad) + "\n"
	writeReloadConfig(t, path, src)
}

// reloadContentRect mirrors layout.InsetBox with an explicit padding. The
// harness insetBox reads the built-in padding only, so this test cannot use it
// after it has written a different value to its own config file.
func reloadContentRect(b paneRect, pad int) paneRect {
	if b.W < 3 || b.H < 3 {
		return paneRect{X: b.X, Y: b.Y, W: maxInt(1, b.W), H: maxInt(1, b.H)}
	}
	inset := 1 + pad
	if pad < 0 {
		inset = 1
	}
	if b.W < 2*inset+1 || b.H < 2*inset+1 {
		inset = 1
	}
	return paneRect{X: b.X + inset, Y: b.Y + inset, W: b.W - 2*inset, H: b.H - 2*inset}
}

func reloadRects(t *testing.T, h *harness, session string) map[string]paneRect {
	t.Helper()
	return h.rects(session)
}

func sameRects(a, b map[string]paneRect) bool {
	if len(a) != len(b) {
		return false
	}
	for id, r := range a {
		if b[id] != r {
			return false
		}
	}
	return true
}

// waitReloadRects polls the live layout until every pane rect matches want. A
// reload replies before the client has drawn, so the settled rect is the only
// fact worth asserting.
func waitReloadRects(t *testing.T, h *harness, session string, want map[string]paneRect) map[string]paneRect {
	t.Helper()
	start := time.Now()
	deadline := start.Add(waitBudget)
	var last map[string]paneRect
	for time.Now().Before(deadline) {
		last = reloadRects(t, h, session)
		if sameRects(last, want) {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane rects are %v, want %v within the %s wait budget (waited %s)",
		last, want, waitBudget, time.Since(start).Round(time.Millisecond))
	return last
}

// waitReloadRect polls until one pane has the wanted rectangle.
func waitReloadRect(t *testing.T, h *harness, session, pane string, want paneRect) paneRect {
	t.Helper()
	start := time.Now()
	deadline := start.Add(waitBudget)
	var last paneRect
	for time.Now().Before(deadline) {
		if r, ok := reloadRects(t, h, session)[pane]; ok {
			last = r
			if r == want {
				return r
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane %s rect is %+v, want %+v within the %s wait budget (waited %s)",
		pane, last, want, waitBudget, time.Since(start).Round(time.Millisecond))
	return last
}

func reloadSnapshotPad(t *testing.T, h *harness, session string) int {
	t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	pad, ok := snap["pad"].(float64)
	if !ok {
		t.Fatalf("snapshot carries no pad: %v", snap)
	}
	return int(pad)
}

// markerOffset is the column where the marker starts inside the pane's own
// text. That column is pane-local: the absolute cell it occupies is
// rect.X plus this offset.
func markerOffset(t *testing.T, out string) int {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == reloadMarker {
			return strings.Index(line, reloadMarker)
		}
	}
	t.Fatalf("pane text has no bare %q row:\n%s", reloadMarker, out)
	return 0
}

// TestReloadPaddingMovesPanesWithoutRestart proves a padding change lands on a
// live session. The config is rewritten, dm exec reloads it, and the pane's
// rectangle, persisted PTY size and text position all follow. A malformed file
// changes nothing, and putting the old padding back restores the first rect.
func TestReloadPaddingMovesPanesWithoutRestart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cfg := reloadConfigPath(t, h)
	writeReloadPadding(t, cfg, 0)

	const session = "reloadpad"
	h.run("new", "-s", session)
	pane := h.focus(session)
	area := h.paneAreaOf(session)

	first := reloadRects(t, h, session)
	before, ok := first[pane]
	if !ok {
		t.Fatalf("session has no pane %q: %v", pane, first)
	}
	wantFirst := reloadContentRect(area, 0)
	if before != wantFirst {
		t.Fatalf("padding 0 pane rect is %+v, want %+v (pane area %+v)", before, wantFirst, area)
	}
	if got := reloadSnapshotPad(t, h, session); got != 0 {
		t.Fatalf("snapshot pad = %d, want 0", got)
	}

	// Print the marker as program output, then hand the tty to cat so no
	// prompt can repaint in front of it when the resize arrives.
	h.run("write-to", pane, "-s", session, "--msg",
		"echo "+reloadMarker+"; exec cat >/dev/null", "--enter")
	firstText := waitScreen(t, h, session, pane, func(out string) bool {
		return hasBareRow(out, reloadMarker)
	})
	firstOff := markerOffset(t, firstText)
	firstAbs := before.X + firstOff

	// Grow the padding by one cell. Every edge moves inward by the delta and
	// the content box loses two cells on each axis.
	writeReloadPadding(t, cfg, 1)
	res := h.rpc(session, map[string]any{"action": "reload"})
	if pad, ok := res["pad"].(float64); !ok || int(pad) != 1 {
		t.Fatalf("reload reply pad = %v, want 1", res["pad"])
	}
	if got := reloadSnapshotPad(t, h, session); got != 1 {
		t.Fatalf("snapshot pad after reload = %d, want 1", got)
	}
	grown := waitReloadRect(t, h, session, pane, reloadContentRect(area, 1))
	if grown.X != before.X+1 || grown.Y != before.Y+1 ||
		grown.W != before.W-2 || grown.H != before.H-2 {
		t.Fatalf("padding 0 to 1 moved rect %+v to %+v, want every edge in by 1", before, grown)
	}
	st := h.sessionState(session)
	if ps := st.Panes[pane]; ps == nil || ps.Cols != grown.W || ps.Rows != grown.H {
		t.Fatalf("pane PTY size = %+v, want rect %dx%d", ps, grown.W, grown.H)
	}

	// The pane's own text still starts at the same pane-local column, so its
	// absolute start is rect.X plus that column: one cell further in.
	grownText := waitScreen(t, h, session, pane, func(out string) bool {
		return hasBareRow(out, reloadMarker)
	})
	grownAbs := grown.X + markerOffset(t, grownText)
	if grownAbs != firstAbs+1 {
		t.Fatalf("content starts at column %d, want %d one cell in from %d",
			grownAbs, firstAbs+1, firstAbs)
	}

	// A malformed file keeps the old config: the reply carries the parse error
	// and nothing moves while the failed reload settles.
	beforeBad := reloadRects(t, h, session)
	writeReloadConfig(t, cfg, "prefix \"ctrl+b\"\npadding nope\n")
	out, err := h.try("exec", `{"action":"reload"}`, "-s", session)
	if err == nil {
		t.Fatalf("malformed reload succeeded, want an error; output:\n%s", out)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("malformed reload carried no error text")
	}
	if !strings.Contains(out, "padding") {
		t.Fatalf("malformed reload error does not name padding:\n%s", out)
	}
	quiet := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(quiet) {
		if got := reloadRects(t, h, session); !sameRects(got, beforeBad) {
			t.Fatalf("malformed reload moved the rects to %v, want %v", got, beforeBad)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if got := reloadSnapshotPad(t, h, session); got != 1 {
		t.Fatalf("snapshot pad after a bad read = %d, want 1", got)
	}
	if got := h.run("read", pane, "-s", session); !strings.Contains(got, reloadMarker) {
		t.Fatalf("pane text lost the marker after a bad read:\n%s", got)
	}

	// Restore the original padding: every rect returns to its first value.
	writeReloadPadding(t, cfg, 0)
	res = h.rpc(session, map[string]any{"action": "reload"})
	if pad, ok := res["pad"].(float64); !ok || int(pad) != 0 {
		t.Fatalf("restoring reload reply pad = %v, want 0", res["pad"])
	}
	if got := reloadSnapshotPad(t, h, session); got != 0 {
		t.Fatalf("snapshot pad after restore = %d, want 0", got)
	}
	restored := waitReloadRects(t, h, session, first)[pane]
	if restored != before {
		t.Fatalf("restored rect %+v, want the first value %+v", restored, before)
	}

	// Shrinking the padding moves the content back out: the same absolute
	// column it had before the first reload.
	restoredText := waitScreen(t, h, session, pane, func(out string) bool {
		return hasBareRow(out, reloadMarker)
	})
	restoredAbs := restored.X + markerOffset(t, restoredText)
	if restoredAbs != firstAbs {
		t.Fatalf("restored content starts at column %d, want %d", restoredAbs, firstAbs)
	}
}
