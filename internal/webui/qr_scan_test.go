package webui

import (
	qrcode "github.com/skip2/go-qrcode"

	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The scan gate is opt-in, like the esctest gate: it shells out to zbarimg,
// which is not a build dependency. It exists because a QR that does not scan is
// the one failure this feature cannot show in a unit test of its own text.
//
//	DM_QRSCAN=1 go test ./internal/webui/ -run TestQRArtScans -v

// qrArtImage turns the terminal art back into pixels at the module size the
// slots give on a real cell grid. That geometry is what a camera sees and what
// a unit test of the string cannot.
func qrArtGrid(t *testing.T, art string) [][]bool {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(art, "\n"), "\n")
	grid := make([][]bool, 0, len(lines))
	for _, line := range lines {
		var row []bool
		dark := false
		for i := 0; i < len(line); {
			switch {
			case strings.HasPrefix(line[i:], "\x1b[40m"):
				dark = true
				i += len("\x1b[40m")
			case strings.HasPrefix(line[i:], "\x1b[107m"):
				dark = false
				i += len("\x1b[107m")
			case strings.HasPrefix(line[i:], "\x1b[0m"):
				i += len("\x1b[0m")
			case line[i] == ' ':
				// Two spaces per module, the terminal's cell aspect.
				j := i
				for j < len(line) && line[j] == ' ' {
					j++
				}
				run := (j - i) / 2
				for k := 0; k < run; k++ {
					row = append(row, dark)
				}
				i = j
			default:
				t.Fatalf("unexpected art at %q", line[i:])
			}
		}
		grid = append(grid, row)
	}
	return grid
}

func qrArtImage(t *testing.T, grid [][]bool, cellW, cellH int) image.Image {
	t.Helper()
	cols := len(grid[0])
	img := image.NewGray(image.Rect(0, 0, cols*cellW, len(grid)*cellH))
	for y, row := range grid {
		if len(row) != cols {
			t.Fatalf("row %d is %d modules, want %d", y, len(row), cols)
		}
		for x, dark := range row {
			grey := color.Gray{Y: 255}
			if dark {
				grey = color.Gray{Y: 0}
			}
			for dy := 0; dy < cellH; dy++ {
				for dx := 0; dx < cellW; dx++ {
					img.SetGray(x*cellW+dx, y*cellH+dy, grey)
				}
			}
		}
	}
	return img
}

func bitsRow(row []bool) string {
	var b strings.Builder
	for _, m := range row {
		if m {
			b.WriteString("#")
		} else {
			b.WriteString(".")
		}
	}
	return b.String()
}

func TestQRArtScans(t *testing.T) {
	if os.Getenv("DM_QRSCAN") == "" {
		t.Skip("set DM_QRSCAN=1 to decode the art with zbarimg")
	}
	if _, err := exec.LookPath("zbarimg"); err != nil {
		t.Skip("no zbarimg")
	}
	url := "http://100.71.227.52:8099/?t=c33beba64055b013"
	art, err := qrText(url)
	if err != nil {
		t.Fatal(err)
	}
	// One module is two 8 px cells across and one 16 px cell down, so it lands
	// on screen as a square: 16 by 16. That is what the camera sees.
	grid := qrArtGrid(t, art)
	want, err := qrcode.New(url, qrLevel)
	if err != nil {
		t.Fatal(err)
	}
	wantBits := want.Bitmap()
	if len(grid) != len(wantBits) || len(grid[0]) != len(wantBits[0]) {
		t.Fatalf("art is %dx%d modules, encoder says %dx%d", len(grid[0]), len(grid), len(wantBits[0]), len(wantBits))
	}
	for y := range wantBits {
		for x := range wantBits[y] {
			if grid[y][x] != wantBits[y][x] {
				t.Logf("grid:   %s", bitsRow(grid[y]))
				t.Logf("encode: %s", bitsRow(wantBits[y]))
				t.Fatalf("module %d,%d is %v, encoder says %v", x, y, grid[y][x], wantBits[y][x])
			}
		}
	}
	img := qrArtImage(t, grid, 16, 16)
	path := filepath.Join(t.TempDir(), "qr.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	out, err := exec.Command("zbarimg", "--quiet", "--raw", path).CombinedOutput()
	if err != nil {
		t.Fatalf("zbarimg: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != url {
		t.Fatalf("decoded %q, want %q", got, url)
	}
}
