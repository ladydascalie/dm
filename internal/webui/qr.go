package webui

import (
	"fmt"
	"os"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/term"
)

// qrLevel is the error recovery level the link uses. Medium survives a phone
// held at an angle or a little glare, and stays small enough to fit a pane.
const qrLevel = qrcode.Medium

// qrText renders text as a QR code for a terminal: each module is two cells
// wide and one cell tall, painted as a solid background colour with spaces in
// front of it.
//
// Two cells per module is the whole trick: a terminal cell is about twice as
// tall as it is wide, so this makes a module square in pixels without any
// glyph. Half blocks would give the same square, but they lean on a glyph
// reaching the cell edge, and a font that pads it draws a grid over the code.
// A coloured background cell has no glyph to be wrong about, and the doubled
// width also doubles the code on screen, which is what a camera wants.
//
// The colours are explicit rather than the terminal's own pair: a dark theme
// would otherwise paint the code light on dark, and contrast and inversion
// handling are not something to leave to a theme.
func qrText(text string) (string, error) {
	code, err := qrcode.New(text, qrLevel)
	if err != nil {
		return "", err
	}
	const (
		dark  = "\x1b[40m"
		light = "\x1b[107m"
		reset = "\x1b[0m\n"
	)
	var b strings.Builder
	// The bitmap carries the quiet zone, so the code scans against the pane
	// around it.
	for _, row := range code.Bitmap() {
		last := ""
		for _, module := range row {
			c := light
			if module {
				c = dark
			}
			if c != last {
				b.WriteString(c)
				last = c
			}
			b.WriteString("  ")
		}
		b.WriteString(reset)
	}
	return b.String(), nil
}

// qrSize is the rendered size in cells: two per module across, one per module
// down, so the code is twice as wide as it is tall in cells.
func qrSize(text string) (cols, rows int) {
	code, err := qrcode.New(text, qrLevel)
	if err != nil {
		return 0, 0
	}
	bits := code.Bitmap()
	return 2 * len(bits[0]), len(bits)
}

// printQR writes the link as a QR code when the terminal is wide enough to
// show it. A code that wraps is a code that does not scan, so a narrow pane
// gets the URL alone and the hint stays honest.
func printQR(url string) {
	// Art in a log file is noise: the code is for a person looking at a pane.
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	art, err := qrText(url)
	if err != nil {
		return
	}
	cols, rows := qrSize(url)
	if width, height, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		// A code that wraps or scrolls out of view does not scan, so say so
		// rather than printing one that cannot work.
		if width < cols || height < rows+3 {
			fmt.Printf("  the code needs a pane of %dx%d to scan; resize, then rerun\n", cols, rows+3)
			return
		}
	}
	fmt.Println()
	fmt.Print(art)
}
