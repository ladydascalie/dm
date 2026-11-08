package shot

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"dm/internal/vt"
)

// loadScenes reads the scene list. A missing file is a skip.
func loadScenes(t *testing.T) []Scene {
	t.Helper()
	path := filepath.Join("testdata", "scenes.json")
	scenes, err := LoadScenes(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no scene data at %s", path)
	}
	if err != nil {
		t.Fatalf("scenes: %v", err)
	}
	return scenes
}

// loadFrame reads the frame bytes the client harness drew for one scene. The
// oracle parses dm's own frame bytes, not a hand-built grid.
func loadFrame(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "frames", name+".bin")
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no frame at %s", path)
	}
	if err != nil {
		t.Fatalf("frame %s: %v", path, err)
	}
	return b
}

// frameGrid replays a frame into a fresh terminal and reads its cells.
func frameGrid(t *testing.T, sc Scene, frame []byte) *Grid {
	t.Helper()
	screen := vt.NewTerm(sc.Cols, sc.Rows)
	screen.Write(frame)
	screen.Flush()
	return GridFromScreen(screen, sc.Cols, sc.Rows)
}

// TestRowWidth is the width invariant. Every row the rasteriser reports must
// land exactly on the scene width. A wide glyph marks its continuation slot,
// so the rasteriser advances the pen once for the glyph and skips the slot it
// already covered.
func TestRowWidth(t *testing.T) {
	for _, sc := range loadScenes(t) {
		t.Run(sc.Name, func(t *testing.T) {
			r, err := Rasterise(frameGrid(t, sc, loadFrame(t, sc.Name)), Options{})
			if err != nil {
				t.Fatalf("rasterise: %v", err)
			}
			if bad := r.Overshoot(sc.Cols); len(bad) > 0 {
				for _, row := range bad {
					t.Errorf("row %d: pen %d, want %d", row.Y, row.Pen, sc.Cols)
				}
			}
		})
	}
}

// TestGoldenCJK compares the cjk scene against its golden. The scene carries
// three CJK glyphs, a three-person ZWJ family emoji and a combining acute. It
// is the oracle that proves the new VT clusters each one in its real width.
func TestGoldenCJK(t *testing.T) {
	var sc Scene
	for _, s := range loadScenes(t) {
		if s.Name == "cjk" {
			sc = s
			break
		}
	}
	if sc.Name == "" {
		t.Fatal("no cjk scene")
	}
	r, err := Rasterise(frameGrid(t, sc, loadFrame(t, "cjk")), Options{})
	if err != nil {
		t.Fatalf("rasterise: %v", err)
	}
	data, err := r.PNG()
	if err != nil {
		t.Fatalf("png: %v", err)
	}
	path := filepath.Join("testdata", "golden", "cjk-dark.png")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: %v", path, err)
	}
	comparePNG(t, path, want, data)
}

// comparePNG decodes both images and compares their pixels.
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
		t.Fatalf("golden %s: pixels differ", path)
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
