package vt

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// reflowScreen renders every row of a terminal as text, top to bottom.
func reflowScreen(t *Term) []string {
	_, rows := t.Grid().Size()
	out := make([]string, rows)
	for y := 0; y < rows; y++ {
		out[y] = t.Grid().Text(y)
	}
	return out
}

// TestReflowWiden prints three 20-column lines at 10 columns and reflows them
// at 20: each line comes back whole on its own row, with no continuation slot,
// and the cursor sits after the last character.
func TestReflowWiden(t *testing.T) {
	lines := []string{
		"0123456789abcdefghij",
		"klmnopqrstuvwxyzABCD",
		"EFGHIJKLMNOPQRSTUVWX",
	}
	ring := []byte(strings.Join(lines, "\r\n"))

	term := Reflow(ring, 20, 6)
	for y, want := range lines {
		if got := term.Grid().Text(y); got != want {
			t.Errorf("row %d = %q, want %q", y, got, want)
		}
		for x := 0; x < 20; x++ {
			if c := term.grid.lines[y][x]; c.Width == WidthContinuation {
				t.Errorf("row %d col %d is a continuation slot", y, x)
			}
		}
	}
	if got := term.Grid().Cursor(); got.X != 19 || got.Y != 2 || !got.WrapNext {
		t.Errorf("cursor = %+v, want col 19 row 2 with wrap pending", got)
	}
}

// TestReflowNarrow reflows one 20-column line at 10 columns: it becomes two
// rows, and the second is a continuation of the first.
func TestReflowNarrow(t *testing.T) {
	ring := []byte("0123456789abcdefghij")

	term := Reflow(ring, 10, 4)
	if got := term.Grid().Text(0); got != "0123456789" {
		t.Errorf("row 0 = %q, want %q", got, "0123456789")
	}
	if got := term.Grid().Text(1); got != "abcdefghij" {
		t.Errorf("row 1 = %q, want %q", got, "abcdefghij")
	}
	if !term.Grid().LineWrapped(1) {
		t.Error("row 1 is not marked as reached by autowrap")
	}
	if got := term.Grid().Cursor(); got.X != 9 || got.Y != 1 || !got.WrapNext {
		t.Errorf("cursor = %+v, want col 9 row 1 with wrap pending", got)
	}
}

// TestReflowCursor matches the final position of a direct feed at the new
// width, in both directions.
func TestReflowCursor(t *testing.T) {
	cases := []struct {
		name string
		ring []byte
		cols int
		rows int
	}{
		{
			name: "widen",
			ring: []byte("the quick brown fox\r\njumps over the lazy dog\r\nand keeps going"),
			cols: 40,
			rows: 8,
		},
		{
			name: "narrow",
			ring: []byte("the quick brown fox jumps over the lazy dog and keeps going"),
			cols: 17,
			rows: 8,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Reflow(tc.ring, tc.cols, tc.rows)

			want := NewTerm(tc.cols, tc.rows)
			want.Write(tc.ring)
			want.Flush()

			if got.Grid().Cursor() != want.Grid().Cursor() {
				t.Errorf("cursor = %+v, want %+v", got.Grid().Cursor(), want.Grid().Cursor())
			}
			gotRows, wantRows := reflowScreen(got), reflowScreen(want)
			for y := range wantRows {
				if gotRows[y] != wantRows[y] {
					t.Errorf("row %d = %q, want %q", y, gotRows[y], wantRows[y])
				}
			}
		})
	}
}

// TestReflowAltScreen leaves Grid().Modes().AltScreen set, so the caller can
// refuse a pane whose app owns the screen.
func TestReflowAltScreen(t *testing.T) {
	ring := []byte("\x1b[?1049h\x1b[2J\x1b[Halternate screen app")

	term := Reflow(ring, 20, 5)
	if !term.Grid().Modes().AltScreen {
		t.Fatal("AltScreen is false after a replay that entered it")
	}
}

// TestReflowPartialEscape starts the ring inside an escape sequence, as a ring
// cut at its oldest byte does. It must not panic and must keep the text it can
// decode.
func TestReflowPartialEscape(t *testing.T) {
	cases := []struct {
		name string
		ring string
		want string
	}{
		{name: "mid OSC", ring: "0;rgb:ff/00/00\r\nhello world", want: "hello world"},
		{name: "mid OSC text", ring: "0;rgb:ff/00/00 still text", want: "0;rgb:ff/00/00 still text"},
		{name: "mid CSI", ring: "abc\x1b[3", want: "abc"},
		{name: "lone ESC", ring: "\x1b", want: ""},
		{name: "mid SGR", ring: "\x1b[38;5;1", want: ""},
		{name: "text then mid CSI", ring: "keep this\x1b[2", want: "keep this"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := Reflow([]byte(tc.ring), 40, 4)
			screen := strings.Join(reflowScreen(term), "\n")
			if !strings.Contains(screen, tc.want) {
				t.Errorf("screen %q does not hold %q", screen, tc.want)
			}
		})
	}
}

// benchRing builds a raw output ring of realistic shell output: a prompt, ls
// rows, git status, go test output and a progress line, coloured as a shell
// writes them.
func benchRing(n int) []byte {
	blocks := []string{
		"\x1b[1;32muser@host\x1b[0m:\x1b[1;34m~/Code/Personal/dm\x1b[0m$ ls -la\r\n" +
			"total 148\r\n" +
			"drwxr-xr-x  12 user user   4096 Nov  8 21:04 .\r\n" +
			"drwxr-xr-x   4 user user   4096 Nov  7 10:11 ..\r\n" +
			"-rw-r--r--   1 user user   2314 Nov  8 20:58 go.mod\r\n" +
			"-rw-r--r--   1 user user  18221 Nov  8 21:03 main.go\r\n" +
			"drwxr-xr-x   3 user user   4096 Nov  8 21:03 internal\r\n",
		"\x1b[1;32muser@host\x1b[0m:\x1b[1;34m~/Code/Personal/dm\x1b[0m$ git status\r\n" +
			"On branch main\r\n" +
			"Your branch is up to date with 'origin/main'.\r\n" +
			"\r\n" +
			"Changes not staged for commit:\r\n" +
			"  \x1b[31mmodified:   internal/vt/reflow.go\x1b[0m\r\n" +
			"  \x1b[31mmodified:   internal/pane/pane.go\x1b[0m\r\n" +
			"\r\n" +
			"no changes added to commit (use \"git add\" and/or \"git commit -a\")\r\n",
		"\x1b[1;32muser@host\x1b[0m:\x1b[1;34m~/Code/Personal/dm\x1b[0m$ go test ./...\r\n" +
			"ok  \tgithub.com/example/dm/internal/vt\t0.042s\r\n" +
			"ok  \tgithub.com/example/dm/internal/pane\t0.118s\r\n" +
			"ok  \tgithub.com/example/dm/internal/server\t0.907s\r\n" +
			"?   \tgithub.com/example/dm/cmd/dm\t[no test files]\r\n",
		"\x1b[1;32muser@host\x1b[0m:\x1b[1;34m~/Code/Personal/dm\x1b[0m$ make build\r\n" +
			"  building release binary\r\n" +
			"  12%\r  47%\r  88%\r 100%\r\n" +
			"wrote ./bin/dm\r\n",
	}
	var buf bytes.Buffer
	buf.Grow(n + 4096)
	for i := 0; buf.Len() < n; i++ {
		fmt.Fprintf(&buf, "\x1b[90m# %d\x1b[0m\r\n", i)
		buf.WriteString(blocks[i%len(blocks)])
	}
	return buf.Bytes()
}

// BenchmarkReflow512K measures replaying a 512 KiB ring at 200x50, the settle
// cost the server pays per affected pane.
func BenchmarkReflow512K(b *testing.B) {
	ring := benchRing(512 << 10)
	b.SetBytes(int64(len(ring)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Reflow(ring, 200, 50)
	}
}
