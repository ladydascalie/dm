package client

// The screenshot oracle. It builds a tui the way newFrameTUI does, draws one
// frame, replays the frame bytes into a vt10x screen, reads the cells into a
// shot.Grid, rasterises it and compares the PNG with a committed golden.
//
// This file lives in package client because newFrameTUI and the tui type are
// unexported here. internal/shot stays test-only: only this _test.go file
// imports it.

import (
	"bytes"
	"errors"
	"flag"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/shot"
	"dm/internal/switcher"
	"dm/internal/theme"
	"dm/internal/transport"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

// updateGolden regenerates the golden images. It is the only way to change
// them: a missing golden fails with the path it expected.
var updateGolden = flag.Bool("update", false, "regenerate golden images")

// shotData is the oracle's data directory, relative to internal/client.
const shotData = "../shot/testdata"

// TestGoldenMatrix renders every scene under every theme and compares the
// result with internal/shot/testdata/golden/<scene>-<theme>.png.
func TestGoldenMatrix(t *testing.T) {
	scenes, err := shot.LoadScenes(filepath.Join(shotData, "scenes.json"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no scene data at %s", shotData)
	}
	if err != nil {
		t.Fatalf("scenes: %v", err)
	}
	themes, err := shot.ThemeNames(shotData)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no theme data at %s", shotData)
	}
	if err != nil {
		t.Fatalf("themes: %v", err)
	}
	if len(scenes) == 0 || len(themes) == 0 {
		t.Skip("no scenes or themes")
	}
	for _, sc := range scenes {
		for _, name := range themes {
			t.Run(sc.Name+"/"+name, func(t *testing.T) {
				th, err := shot.LoadTheme(shotData, name)
				if err != nil {
					t.Fatalf("theme %s: %v", name, err)
				}
				r := renderScene(t, sc, th)
				data, err := r.PNG()
				if err != nil {
					t.Fatalf("png: %v", err)
				}
				path := filepath.Join(shotData, "golden", sc.Name+"-"+name+".png")
				if *updateGolden {
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatalf("mkdir: %v", err)
					}
					if err := os.WriteFile(path, data, 0o644); err != nil {
						t.Fatalf("write %s: %v", path, err)
					}
					return
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("golden %s: %v", path, err)
				}
				comparePNG(t, path, want, data)
			})
		}
	}
}

// renderScene draws one frame and rasterises the screen it produced.
func renderScene(t *testing.T, sc shot.Scene, th theme.Theme) *shot.Raster {
	t.Helper()
	tr, out := buildSceneTUI(t, sc, th)
	tr.draw()
	if out.Len() == 0 {
		t.Fatalf("scene %s: frame is empty", sc.Name)
	}
	screen := vt10x.New(vt10x.WithSize(sc.Cols, sc.Rows))
	if _, err := screen.Write(out.Bytes()); err != nil {
		t.Fatalf("scene %s: replay: %v", sc.Name, err)
	}
	grid := shot.GridFromScreen(screen, sc.Cols, sc.Rows)
	r, err := shot.Rasterise(grid, shot.Options{})
	if err != nil {
		t.Fatalf("scene %s: rasterise: %v", sc.Name, err)
	}
	return r
}

// buildSceneTUI builds the tui newFrameTUI would, then applies the scene's
// overlays. It does not touch draw().
func buildSceneTUI(t *testing.T, sc shot.Scene, th theme.Theme) (*tui, *bytes.Buffer) {
	t.Helper()
	root := sceneNode(sc.Root)
	snap := &wire.Snapshot{
		Session:      "main",
		Workspace:    "s1",
		Cols:         sc.Cols,
		Rows:         sc.Rows,
		Rail:         sc.Rail,
		Root:         root,
		Focus:        sc.Focus,
		ActiveWindow: sc.ActiveWindow,
	}
	for _, w := range sc.Windows {
		snap.Windows = append(snap.Windows, wire.WindowInfo{ID: w.ID, Name: w.Name})
	}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaForWithTabBar(sc.Cols, sc.Rows, sc.Rail), rects)
	winID, winName := "w1", "one"
	if len(sc.Windows) > 0 {
		winID, winName = sc.Windows[0].ID, sc.Windows[0].Name
	}
	for _, p := range sc.Panes {
		snap.Panes = append(snap.Panes, wire.PaneInfo{ID: p.ID, Rect: rects[p.ID]})
		snap.All = append(snap.All, wire.AllPane{
			ID: p.ID, Name: p.Name,
			Workspace: "s1", WorkspaceName: "main",
			Window: winID, WindowName: winName,
			Focus:             p.ID == sc.Focus,
			WorkspaceActive:   true,
			WindowActive:      true,
			WindowUnfolded:    true,
			WorkspaceUnfolded: true,
		})
	}
	out := &bytes.Buffer{}
	tr := &tui{snap: snap, vts: map[string]vt10x.Terminal{}, out: out, th: &th}
	for _, p := range sc.Panes {
		r := rects[p.ID]
		v := vt10x.New(vt10x.WithSize(r.W, r.H))
		_, _ = v.Write([]byte(vtContent(p.Content)))
		tr.vts[p.ID] = v
	}
	if sc.Toast != "" {
		tr.toasts = []toast{{text: sc.Toast}}
	}
	if sc.Float != nil {
		fr := layout.Rect{X: sc.Float.X, Y: sc.Float.Y, W: sc.Float.W, H: sc.Float.H}
		fv := vt10x.New(vt10x.WithSize(fr.W, fr.H))
		_, _ = fv.Write([]byte(vtContent(sc.Float.Content)))
		tr.AddFloatingPane(&panel.FloatingPane{ID: sc.Float.ID, Rect: fr, VT: fv, Active: true})
	}
	if sc.Switcher {
		tr.sw = switcher.NewSwitcher(switcher.PaletteRect(sc.Cols, sc.Rows), snap)
	}
	if sc.Help {
		tr.armed = true
	}
	if sc.Mode == "sessions" {
		tr.mode = "sessions"
		for i, s := range sc.Sessions {
			tr.sessions = append(tr.sessions, transport.SessionInfo{Name: s.Name, Live: s.Active})
			if s.Active {
				tr.sel = i
				tr.session = s.Name
			}
		}
	}
	return tr, out
}

// vtContent turns the scene's plain newlines into the CR+LF a terminal needs.
// vt10x treats a bare LF as a line feed that keeps the column, so a scene
// written with LF alone would staircase.
func vtContent(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func sceneNode(n *shot.Node) *layout.Node {
	if n == nil {
		return nil
	}
	out := &layout.Node{Pane: n.Pane, Dir: n.Dir}
	for _, k := range n.Kids {
		out.Kids = append(out.Kids, sceneNode(k))
	}
	return out
}

// comparePNG decodes both images and compares their pixels. A byte compare
// would also work, but pixels survive an encoder change.
func comparePNG(t *testing.T, path string, want, got []byte) {
	t.Helper()
	a, err := png.Decode(bytes.NewReader(want))
	if err != nil {
		t.Fatalf("golden %s: decode: %v", path, err)
	}
	b, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("render: decode: %v", err)
	}
	if a.Bounds() != b.Bounds() {
		t.Fatalf("golden %s: bounds %v, got %v", path, a.Bounds(), b.Bounds())
	}
	if !samePixels(a, b) {
		t.Fatalf("golden %s: pixels differ; run go test ./internal/client -run Golden -update", path)
	}
}

func samePixels(a, b image.Image) bool {
	ba := a.Bounds()
	for y := ba.Min.Y; y < ba.Max.Y; y++ {
		for x := ba.Min.X; x < ba.Max.X; x++ {
			if a.At(x, y) != b.At(x, y) {
				return false
			}
		}
	}
	return true
}
