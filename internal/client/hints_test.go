package client

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/box"
	"dm/internal/boxview"
	"dm/internal/keymap"
	"dm/internal/keymapview"
	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/wire"
)

// hintKeymap parses one overlay mode whose keys are all rebound, so a test can
// tell a derived hint from the literal one.
func hintKeymap(t *testing.T, src string) *keymap.Config {
	t.Helper()
	cfg, err := keymap.ParseConfig(src)
	if err != nil {
		t.Fatalf("parse test keymap: %v", err)
	}
	return cfg
}

// TestBoxHintNamesTheKeymap: the message box border names the box keymap's
// keys, so a rebind moves the hint and the reader affordance stays with the
// page that has a body.
func TestBoxHintNamesTheKeymap(t *testing.T) {
	t.Parallel()
	cfg := hintKeymap(t, `keymap "box" {
		"down" "n"
		"up" "a"
		"toggle-page" "ctrl+w"
		"close-esc" "ctrl+q"
	}`)
	snap := &wire.Snapshot{Cols: 100, Rows: 30}
	b := boxview.NewBoxView(snap, theme.DefaultTheme(), cfg)
	if got, want := b.Float.Hint, "n/a move  ctrl+w watchers  ctrl+q close"; got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
	b.Set(make([]box.Entry, 0), theme.DefaultTheme())
	b.TogglePage(theme.DefaultTheme())
	if got, want := b.Float.Hint, "n/a move  ctrl+w log  ctrl+q close"; got != want {
		t.Fatalf("page hint = %q, want %q", got, want)
	}
	// A reload swaps the config, and the border follows it in place.
	b.SetKeymap(keymap.LoadDefault())
	if got := b.Float.Hint; !strings.Contains(got, "j/k move") || !strings.Contains(got, "esc close") {
		t.Fatalf("hint after a reload = %q, want the default keys", got)
	}
}

// TestReaderHintNamesTheKeymap: the body reader names the box keymap's scroll
// pair and close key, with the reader's own word for the close.
func TestReaderHintNamesTheKeymap(t *testing.T) {
	t.Parallel()
	cfg := hintKeymap(t, `keymap "box" {
		"down" "n"
		"up" "a"
		"toggle-page" "w"
		"close-esc" "ctrl+q"
	}`)
	e := box.Entry{Seq: 1, Key: "lane/a", Body: "text"}
	r := newBodyReader(e, layout.Rect{X: 2, Y: 2, W: 40, H: 6}, theme.DefaultTheme(), cfg)
	if got, want := r.Float.Hint, "n/a scroll  ctrl+q back"; got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
	r.SetKeymap(keymap.LoadDefault())
	if got, want := r.Float.Hint, "j/k scroll  esc back"; got != want {
		t.Fatalf("hint after a reload = %q, want %q", got, want)
	}
}

// TestMessageHintNamesOnlyTheBoxKeymap: the message view names every key it
// paints from the box keymap, so no key in the corner is a literal and a rebind
// moves it.
func TestMessageHintNamesOnlyTheBoxKeymap(t *testing.T) {
	t.Parallel()
	cfg := hintKeymap(t, `keymap "box" {
		"down" "n"
		"up" "a"
		"toggle-page" "w"
		"filter-notify" "f"
		"body" "e"
		"close-esc" "ctrl+q"
	}`)
	b := boxview.NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30}, theme.DefaultTheme(), cfg)
	b.Set([]box.Entry{{Seq: 1, Key: "lane/a", Body: "text"}}, theme.DefaultTheme())
	if got, want := b.Float.Hint, "n/a move  w watchers  f notify  e body  ctrl+q close"; got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
	b.SetKeymap(keymap.LoadDefault())
	if got := b.Float.Hint; !strings.Contains(got, "j/k move") || !strings.Contains(got, "m notify") || !strings.Contains(got, "enter body") {
		t.Fatalf("hint after a reload = %q, want the default keys", got)
	}
}

// TestSessionsHintNamesTheKeymap: the session picker names the picker keymap's
// keys, and the row carries no outer padding.
func TestSessionsHintNamesTheKeymap(t *testing.T) {
	t.Parallel()
	cfg := hintKeymap(t, `keymap "picker" {
		"down" "n"
		"up" "a"
		"pick" "ctrl+p"
		"close-esc" "ctrl+q"
	}`)
	if got, want := sessionsHint(cfg), "n/a move  ctrl+p switch  ctrl+q cancel"; got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
	if got, want := sessionsHint(keymap.LoadDefault()), "j/k move  enter switch  esc cancel"; got != want {
		t.Fatalf("default hint = %q, want %q", got, want)
	}
}

// TestLayoutHintNamesTheKeymap: the layout picker's footer names the layout
// keymap's keys, and its two notes stay whole.
func TestLayoutHintNamesTheKeymap(t *testing.T) {
	t.Parallel()
	cfg := hintKeymap(t, `keymap "layout" {
		"pick" "ctrl+p"
		"close-esc" "ctrl+q"
	}`)
	if got, want := layoutFooterText(true, 2, cfg), "ctrl+p pick  ctrl+q close"; got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
	if got, want := layoutFooterText(false, 2, cfg), "no room here for the preview"; got != want {
		t.Fatalf("dropped-preview note = %q, want %q", got, want)
	}
	if got, want := layoutFooterText(true, 1, cfg), "one pane: every layout is this"; got != want {
		t.Fatalf("one-pane note = %q, want %q", got, want)
	}
}

// TestRenameHintNamesTheKeymap: the rename prompt has a keymap mode, so its
// border names the keys the config binds to submit and cancel. A rebind moves
// the hint with the keys the prompt itself dispatches on.
func TestRenameHintNamesTheKeymap(t *testing.T) {
	t.Parallel()
	cfg := hintKeymap(t, `keymap "rename" {
		"submit" "ctrl+s"
		"cancel" "ctrl+q"
	}`)
	if got, want := renameHint(cfg), "ctrl+s  ctrl+q"; got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
	// The review's counter-example: a user block that rebinds submit only. The
	// merge keeps cancel on its default key, and the hint names both.
	path := filepath.Join(t.TempDir(), "config.kdl")
	if err := os.WriteFile(path, []byte("keymap \"rename\" {\n\t\"submit\" \"ctrl+s\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	merged, err := keymap.LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if got, want := renameHint(merged), "ctrl+s  esc"; got != want {
		t.Fatalf("merged hint = %q, want %q", got, want)
	}

	// The drawn prompt carries the new keys, not the old ones.
	snap := testutil.TestSnapshot(&layout.Node{Pane: "a"}, 60, 12, nil)
	var b bytes.Buffer
	drawRename(&b, theme.DefaultTheme(), snap, "s1", inputOf("main"), false, cfg)
	win := renamePromptVisible(b.String())
	if !strings.Contains(win, "ctrl+s  ctrl+q") {
		t.Fatalf("drawn prompt = %q, want the rebound hint", win)
	}
	if strings.Contains(win, "enter  esc") {
		t.Fatalf("drawn prompt = %q, the literal keys survived", win)
	}
}

// TestOverlayHintsArePlainRows: the rows the client owns spell their one or two
// keys through ui.Hints, so nothing carries outer padding and nothing carries
// an escape.
func TestOverlayHintsArePlainRows(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, got, want string }{
		{"palette", paletteHelpHint, "? help"},
		{"rename", renameHint(keymap.LoadDefault()), "enter  esc"},
		{"help box", helpBoxHint(), "esc"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s hint = %q, want %q", c.name, c.got, c.want)
		}
		if strings.HasPrefix(c.got, " ") || strings.HasSuffix(c.got, " ") {
			t.Fatalf("%s hint = %q, want no outer padding", c.name, c.got)
		}
		if strings.ContainsRune(c.got, 0x1b) {
			t.Fatalf("%s hint = %q, want plain text", c.name, c.got)
		}
	}
}

// TestRefreshHintsMovesOpenFloats: a reload renames the hints of the floats
// that keep their own config, so an open box, keymap view or reader names the
// new keys without reopening.
func TestRefreshHintsMovesOpenFloats(t *testing.T) {
	t.Parallel()
	defaults := keymap.LoadDefault()
	cfg := hintKeymap(t, `keymap "box" {
		"down" "n"
		"up" "a"
		"toggle-page" "w"
		"close-esc" "ctrl+q"
	}
	keymap "help" {
		"down" "n"
		"up" "a"
		"top" "t"
		"bottom" "B"
		"search" "f"
		"close-esc" "ctrl+q"
	}`)
	snap := &wire.Snapshot{Cols: 100, Rows: 30}
	tui := &tui{snap: snap}
	tui.box = boxview.NewBoxView(snap, theme.DefaultTheme(), defaults)
	tui.keys = keymapview.NewKeymapView(snap, defaults)
	tui.reader = newBodyReader(box.Entry{Seq: 1, Key: "lane/a", Body: "text"},
		layout.Rect{X: 2, Y: 2, W: 40, H: 6}, theme.DefaultTheme(), defaults)

	tui.refreshHints(cfg)

	for name, hint := range map[string]string{
		"box":    tui.box.Float.Hint,
		"keys":   tui.keys.Float.Hint,
		"reader": tui.reader.Float.Hint,
	} {
		if !strings.Contains(hint, "n/a") {
			t.Fatalf("%s hint = %q, want the rebound scroll pair", name, hint)
		}
	}
}
