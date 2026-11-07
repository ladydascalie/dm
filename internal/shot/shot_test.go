package shot

import (
	"bytes"
	"go/parser"
	"go/token"
	"image"
	"image/png"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRenderDimensions(t *testing.T) {
	g := NewGrid(4, 2)
	for x := 0; x < 4; x++ {
		g.Set(x, 0, Cell{Content: "a", Fg: RGB{255, 255, 255}})
	}
	r, err := Rasterise(g, Options{})
	if err != nil {
		t.Fatalf("rasterise: %v", err)
	}
	data, err := r.PNG()
	if err != nil {
		t.Fatalf("png: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := image.Rect(0, 0, 4*DefaultCellW, 2*DefaultCellH)
	if got := img.Bounds(); got != want {
		t.Fatalf("bounds = %v, want %v", got, want)
	}
}

func TestCellBackground(t *testing.T) {
	red := RGB{255, 0, 0}
	g := NewGrid(2, 1)
	g.Set(1, 0, Cell{Content: " ", Bg: red})
	r, err := Rasterise(g, Options{})
	if err != nil {
		t.Fatalf("rasterise: %v", err)
	}
	px := r.Image.RGBAAt(DefaultCellW+DefaultCellW/2, DefaultCellH/2)
	if px.R != red.R || px.G != red.G || px.B != red.B {
		t.Fatalf("pixel = %v, want %v", px, red)
	}
}

func TestASCIIRowWidth(t *testing.T) {
	const w = 8
	g := NewGrid(w, 1)
	for x := 0; x < w; x++ {
		g.Set(x, 0, Cell{Content: string(rune('a' + x))})
	}
	r, err := Rasterise(g, Options{})
	if err != nil {
		t.Fatalf("rasterise: %v", err)
	}
	if got := r.Rows[0].Pen; got != w {
		t.Fatalf("pen = %d, want %d", got, w)
	}
}

func TestWideGlyphOvershoot(t *testing.T) {
	const w = 4
	g := NewGrid(w, 1)
	g.Set(0, 0, Cell{Content: "世"})
	g.Set(1, 0, Cell{Content: "a"})
	g.Set(2, 0, Cell{Content: "b"})
	g.Set(3, 0, Cell{Content: "c"})
	r, err := Rasterise(g, Options{})
	if err != nil {
		t.Fatalf("rasterise: %v", err)
	}
	if got := r.Rows[0].Pen; got <= w {
		t.Fatalf("pen = %d, want an overshoot past %d", got, w)
	}
	if len(r.Overshoot(w)) != 1 {
		t.Fatalf("overshoot rows = %v, want row 0", r.Overshoot(w))
	}
}

// TestTestOnly fails when a non-test file imports this package. The embedded
// font must never reach the dm binary.
func TestTestOnly(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	const self = "dm/internal/shot"
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if p == self {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %s; internal/shot is test-only", rel, self)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
