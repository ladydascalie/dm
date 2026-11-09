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

// benchRing builds a raw output ring of realistic, append-only shell output: a
// prompt, ls rows, git status, go test output and a build log, coloured as a
// shell writes them. No carriage return and no cursor move, so the window
// applies.
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
			"  compiling 12 of 88 files\r\n" +
			"  compiling 47 of 88 files\r\n" +
			"  compiling 88 of 88 files\r\n" +
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

// windowStream builds a raw stream of n lines. body returns one line without
// its ending; every line ends in \r\n.
func windowStream(n int, body func(i int) string) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(body(i))
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// TestReflowStartWindow pins the offset derivation: a short ring replays whole,
// a normal stream starts on the next line boundary, a stream that ends inside a
// line is clamped to the cutoff, and a screen switch in the skipped prefix
// forces the whole ring.
func TestReflowStartWindow(t *testing.T) {
	short := []byte("abc\r\n")
	if got := reflowStart(short, 10, 2); got != 0 {
		t.Errorf("short ring start = %d, want 0", got)
	}

	// Four printable bytes per line at two columns: the window keeps the last
	// line and starts after the newline that ends the previous one.
	lines := []byte("aaaa\r\nbbbb\r\ncccc\r\n")
	if got := reflowStart(lines, 2, 1); got != len("aaaa\r\nbbbb\r\n") {
		t.Errorf("line-boundary start = %d, want %d", got, len("aaaa\r\nbbbb\r\n"))
	}

	// A newline-free tail longer than the window is clamped to the cutoff, so
	// the replay stays bounded by the cell count.
	long := []byte(strings.Repeat("0123456789", 600))
	clamped := reflowStart(long, 10, 2)
	if clamped <= 0 || clamped >= len(long) {
		t.Errorf("newline-free start = %d, want a cut inside 0..%d", clamped, len(long))
	}
	if left := scanCount(long[clamped:], 2*2*10+1); left > 2*2*10 {
		t.Errorf("newline-free tail holds %d printable bytes, want at most %d", left, 2*2*10)
	}

	// A long line inside the window with enough text after it: the next
	// boundary still fills the screen, so the cut moves past the long line.
	longBody := strings.Repeat("0123456789", 3)
	longThenMany := []byte(longBody + "\r\nabcdefghij\r\nklmnopqrst")
	wantPastLong := len(longBody) + len("\r\n")
	if got := reflowStart(longThenMany, 10, 2); got != wantPastLong {
		t.Errorf("long-line follow start = %d, want %d", got, wantPastLong)
	}
	// One line fewer and the boundary leaves less than a screenful, with no
	// earlier newline to walk back to: the start is clamped to the cutoff.
	longThenFew := []byte(longBody + "\r\nabcdefghij\r\nklmno")
	if got := reflowStart(longThenFew, 10, 2); got <= 0 || scanCount(longThenFew[got:], 2*2*10+1) > 2*2*10 {
		t.Errorf("long-line tail start = %d, want a clamped cut inside 0..%d", got, len(longThenFew))
	}

	// An ordinary long stream is cut, not replayed whole.
	big := windowStream(2000, func(i int) string { return fmt.Sprintf("line %04d %s", i, strings.Repeat("x", 20)) })
	if start := reflowStart(big, 80, 24); start <= 0 || start >= len(big) {
		t.Errorf("big ring start = %d, want a cut inside 0..%d", start, len(big))
	}

	// A pager cycle in the skipped prefix replays the whole ring. The alt dump
	// here is long enough that the cutoff lands inside it, after the entry.
	head := windowStream(400, func(i int) string { return fmt.Sprintf("primary %03d %s", i, strings.Repeat("p", 20)) })
	altDump := "\x1b[?1049h\x1b[2J\x1b[H" + strings.Repeat("alt row\r\n", 3000) + "\x1b[?1049l$ "
	pager := append(append([]byte(nil), head...), []byte(altDump)...)
	if got := reflowStart(pager, 80, 24); got != 0 {
		t.Errorf("pager start = %d, want 0 for a whole-ring replay", got)
	}

	for _, seq := range []string{"?47h", "?47l", "?1047h", "?1047l", "?1048h", "?1048l", "?1049h", "?1049l"} {
		ring := []byte("text \x1b[" + seq + " more text")
		if !hasAltScreenCycle(ring) {
			t.Errorf("hasAltScreenCycle missed %s", seq)
		}
	}
	if hasAltScreenCycle([]byte("plain output with ?1049 and no final")) {
		t.Error("hasAltScreenCycle reported a cycle in plain text")
	}
}

// TestReflowWindowSameScreen proves the window never changes a visible row:
// every size and stream gives the same rows and cursor as a replay that is
// forced to start at the ring's first byte.
func TestReflowWindowSameScreen(t *testing.T) {
	plain := windowStream(2000, func(i int) string {
		return fmt.Sprintf("line %04d %s", i, strings.Repeat("x", 20))
	})
	coloured := windowStream(2000, func(i int) string {
		return fmt.Sprintf("\x1b[1;32mline %04d\x1b[0m \x1b[34m%s\x1b[0m", i, strings.Repeat("y", 20))
	})
	wide := windowStream(1000, func(i int) string {
		return strings.Repeat("日本語", 10)
	})
	combining := windowStream(1000, func(i int) string {
		return strings.Repeat("e\u0301", 15)
	})
	cleared := func() []byte {
		var b bytes.Buffer
		for i := 0; i < 2000; i++ {
			if i%50 == 0 {
				b.WriteString("\x1b[2J\x1b[H")
			}
			fmt.Fprintf(&b, "row %04d %s\r\n", i, strings.Repeat("c", 20))
		}
		return b.Bytes()
	}()
	longMiddle := func() []byte {
		var b bytes.Buffer
		for i := 0; i < 2000; i++ {
			if i == 1200 {
				b.WriteString(strings.Repeat("0123456789", 600) + "01234\r\n")
				continue
			}
			fmt.Fprintf(&b, "line %04d %s\r\n", i, strings.Repeat("m", 20))
		}
		return b.Bytes()
	}()
	longTail := func() []byte {
		b := append([]byte(nil), plain...)
		b = append(b, []byte(strings.Repeat("0123456789", 600)+"01234\r\n")...)
		return append(b, []byte("last one\r\nsecond\r\nthird\r\n")...)
	}()
	// A newline-free tail of a line that is a whole number of screens long at
	// each size below, so the clamped replay starts on a column boundary.
	longLastLine := strings.Repeat("abcdefg", 857) + "z"
	longLast := append(append([]byte(nil), plain...), []byte(longLastLine)...)
	pager := func(flat bool) []byte {
		var b bytes.Buffer
		for i := 0; i < 400; i++ {
			fmt.Fprintf(&b, "primary row %03d %s\r\n", i, strings.Repeat("p", 20))
		}
		b.WriteString("\x1b[?1049h\x1b[2J\x1b[H")
		if flat {
			b.WriteString(strings.Repeat("v", 10000))
		} else {
			for i := 0; i < 3000; i++ {
				fmt.Fprintf(&b, "alt row %04d %s\r\n", i, strings.Repeat("v", 20))
			}
		}
		b.WriteString("\x1b[?1049l")
		b.WriteString("user@host$ ")
		return b.Bytes()
	}

	cases := []struct {
		name string
		ring []byte
	}{
		{"plain", plain},
		{"coloured", coloured},
		{"wide", wide},
		{"combining", combining},
		{"clear", cleared},
		{"long middle", longMiddle},
		{"long tail", longTail},
		{"long last", longLast},
		{"pager", pager(false)},
		{"pager flat", pager(true)},
	}
	sizes := []struct{ cols, rows int }{
		{20, 6},
		{80, 24},
		{200, 50},
	}
	for _, tc := range cases {
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%s/%dx%d", tc.name, size.cols, size.rows), func(t *testing.T) {
				compareReflow(t, tc.ring, size.cols, size.rows)
			})
		}
	}
}

// compareReflow requires the windowed replay to match a replay of the whole
// ring on every row, every wrap flag and the cursor.
func compareReflow(t *testing.T, ring []byte, cols, rows int) {
	t.Helper()
	windowed := Reflow(ring, cols, rows)
	whole := reflowFrom(ring, cols, rows, 0)

	if got, want := windowed.Grid().Cursor(), whole.Grid().Cursor(); got != want {
		t.Errorf("cursor = %+v, want the whole-ring %+v", got, want)
	}
	for y := 0; y < rows; y++ {
		if got, want := windowed.Grid().Text(y), whole.Grid().Text(y); got != want {
			t.Errorf("row %d = %q, want the whole-ring %q", y, got, want)
		}
		if got, want := windowed.Grid().LineWrapped(y), whole.Grid().LineWrapped(y); got != want {
			t.Errorf("row %d wrapped = %v, want %v", y, got, want)
		}
	}
}

// TestReflowPagerInsideWindow is the review repro: a pager cycle that lies
// wholly inside the window. The alt dump is between roughly one and two
// screens, so the cutoff lands after the ?1049h entry and the prefix check
// alone does not fire; the window saves its own partial primary screen and the
// ?1049l restore leaves the top rows blank and the prompt too high.
func TestReflowPagerInsideWindow(t *testing.T) {
	cases := []struct{ cols, rows, altLines int }{
		{80, 24, 90},
		{200, 50, 520},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%dx%d", tc.cols, tc.rows), func(t *testing.T) {
			var b bytes.Buffer
			for i := 0; i < 2000; i++ {
				fmt.Fprintf(&b, "primary line %04d %s\r\n", i, strings.Repeat("p", 20))
			}
			b.WriteString("\x1b[?1049h\x1b[2J\x1b[H")
			for i := 0; i < tc.altLines; i++ {
				fmt.Fprintf(&b, "alt line %04d %s\r\n", i, strings.Repeat("v", 20))
			}
			b.WriteString("\x1b[?1049luser@host$ ")
			compareReflow(t, b.Bytes(), tc.cols, tc.rows)
		})
	}
}

// progressRing builds plain output followed by an in-place progress line
// rewritten with carriage returns.
func progressRing(cols, _ int) []byte {
	lines, ticks := 200, 500
	if cols >= 200 {
		lines, ticks = 3000, 3000
	}
	var b bytes.Buffer
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "downloaded asset %04d payload\r\n", i)
	}
	for i := 0; i < ticks; i++ {
		fmt.Fprintf(&b, "\rdownloading %3d%%", i%100)
	}
	return b.Bytes()
}

// frameRing builds plain output followed by a three-line block redrawn with
// cursor-up frames.
func frameRing(cols, _ int) []byte {
	lines, frames := 200, 300
	if cols >= 200 {
		lines, frames = 3000, 3000
	}
	var b bytes.Buffer
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "build line %04d output\r\n", i)
	}
	b.WriteString("status alpha\r\nstatus beta\r\nstatus gamma\r\n")
	for i := 0; i < frames; i++ {
		fmt.Fprintf(&b, "\x1b[2Aframe %04d alpha\r\nframe %04d beta\r\nframe %04d gamma\r\n", i, i, i)
	}
	return b.Bytes()
}

// addressedRing builds plain output followed by a block placed with an
// absolute cursor position on each frame.
func addressedRing(cols, _ int) []byte {
	lines, frames := 200, 300
	if cols >= 200 {
		lines, frames = 3000, 3000
	}
	var b bytes.Buffer
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "log line %04d text\r\n", i)
	}
	for i := 0; i < frames; i++ {
		fmt.Fprintf(&b, "\x1b[4;1Hblock %04d one\r\nblock %04d two\r\nblock %04d three", i, i, i)
	}
	return b.Bytes()
}

// TestReflowControlFallback proves cursor-addressed output replays the whole
// ring, and that a control-free tail still takes the window.
func TestReflowControlFallback(t *testing.T) {
	sizes := []struct{ cols, rows int }{
		{80, 24},
		{200, 50},
	}
	builders := []struct {
		name  string
		build func(cols, rows int) []byte
	}{
		{"progress CR", progressRing},
		{"cursor-up frames", frameRing},
		{"cursor addressed", addressedRing},
	}
	for _, b := range builders {
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%s/%dx%d", b.name, size.cols, size.rows), func(t *testing.T) {
				ring := b.build(size.cols, size.rows)
				if start := reflowStart(ring, size.cols, size.rows); start != 0 {
					t.Errorf("start = %d, want 0: screen control must replay the whole ring", start)
				}
				compareReflow(t, ring, size.cols, size.rows)
			})
		}
	}

	// Plain append-only output still takes the window.
	for _, size := range sizes {
		ring := windowStream(2000, func(i int) string {
			return fmt.Sprintf("plain line %04d %s", i, strings.Repeat("w", 20))
		})
		t.Run(fmt.Sprintf("control free/%dx%d", size.cols, size.rows), func(t *testing.T) {
			if start := reflowStart(ring, size.cols, size.rows); start <= 0 || start >= len(ring) {
				t.Errorf("start = %d, want a cut inside 0..%d", start, len(ring))
			}
			compareReflow(t, ring, size.cols, size.rows)
		})
	}
}

// BenchmarkReflow512KCR measures the same ring with a progress line at the end.
// Its carriage return defeats the window, so the whole ring is replayed.
func BenchmarkReflow512KCR(b *testing.B) {
	ring := append(benchRing(512<<10), []byte("\rdownloading 42%")...)
	b.SetBytes(int64(len(ring)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Reflow(ring, 200, 50)
	}
}
