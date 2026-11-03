package main

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// palette is the floating quick switcher: television on its own PTY, drawn
// inside a box over the panes. The client owns it, so it lives and dies with
// the attach and never enters the session tree.
type palette struct {
	cmd  *exec.Cmd
	ptmx *os.File
	vt   vt10x.Terminal
	rect Rect
	out  chan []byte
	tail []byte // last bytes of the child's stream, for the selection
	done chan struct{}
	once sync.Once
}

const palTailMax = 64 << 10

// paletteRect centres the float at 80% by 70%, clamped so its box always fits
// on screen.
func paletteRect(cols, rows int) Rect {
	w, h := cols*8/10, rows*7/10
	if w > 132 {
		w = 132
	}
	if h > 40 {
		h = 40
	}
	if w > cols-4 {
		w = cols - 4
	}
	if h > rows-3 {
		h = rows - 3
	}
	if w < 20 {
		w = 20
	}
	if h < 6 {
		h = 6
	}
	if w > cols {
		w = cols
	}
	if h > rows {
		h = rows
	}
	return Rect{X: (cols - w) / 2, Y: (rows - h) / 2, W: w, H: h}
}

// shellArg quotes a word for the shell television uses to run its source and
// preview commands.
func shellArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// startPalette runs television against the session's dashboard rows.
func startPalette(session string, rect Rect) (*palette, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{
		"--no-remote",
		"--watch", "2",
		"--source-command", exe + " dash -s " + shellArg(session),
		"--source-display", "{}",
		"--source-output", "{split: :0}",
		"--input-header", " switch ",
	}
	if rect.H >= 14 && rect.W >= 64 {
		args = append(args,
			"--preview-command", exe+" peek {split: :0} --lines "+strconv.Itoa(rect.H-2)+" -s "+shellArg(session),
			"--preview-size", "50",
		)
	} else {
		args = append(args, "--no-preview")
	}
	cmd := exec.Command("tv", args...)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(rect.W), Rows: uint16(rect.H)})
	if err != nil {
		return nil, err
	}
	p := &palette{
		cmd: cmd, ptmx: ptmx, rect: rect,
		vt:   vt10x.New(vt10x.WithSize(rect.W, rect.H)),
		out:  make(chan []byte, 256),
		done: make(chan struct{}),
	}
	go p.readLoop()
	return p, nil
}

// cursorPos is the float's VT cursor for a cursor position report.
func (p *palette) cursorPos() (row, col int) {
	p.vt.Lock()
	defer p.vt.Unlock()
	c := p.vt.Cursor()
	return c.Y, c.X
}

// readLoop moves the child's output to the client, answering the terminal
// queries television makes at startup the way a pane daemon does. Without the
// answers a TUI can stall for seconds before its first frame.
func (p *palette) readLoop() {
	defer close(p.out)
	buf := make([]byte, 32<<10)
	var clean bytes.Buffer
	filter := &oscFilter{
		status: func(Status) {},
		query:  func() {},
		reply:  func(s string) { go func() { _, _ = p.ptmx.Write([]byte(s)) }() },
		cursor: p.cursorPos,
		fg:     termFG,
		bg:     termBG,
	}
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			clean.Reset()
			filter.feed(buf[:n], &clean)
			if clean.Len() > 0 {
				b := append([]byte(nil), clean.Bytes()...)
				select {
				case p.out <- b:
				case <-p.done:
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// resize refits the float after the terminal changed size.
func (p *palette) resize(rect Rect) {
	if rect == p.rect {
		return
	}
	p.rect = rect
	p.vt = vt10x.New(vt10x.WithSize(rect.W, rect.H))
	_ = pty.Setsize(p.ptmx, &pty.Winsize{Cols: uint16(rect.W), Rows: uint16(rect.H)})
}

// stop ends the child and releases the PTY. Waiting here keeps no zombie
// behind, since the palette is short lived.
func (p *palette) stop() {
	p.once.Do(func() {
		close(p.done)
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Signal(syscall.SIGHUP)
		}
		_ = p.ptmx.Close()
		_ = p.cmd.Wait()
	})
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// stripANSI removes escape sequences from a plain text stream.
func stripANSI(s string) string { return ansiSeq.ReplaceAllString(s, "") }

// palettePick pulls the chosen entry out of the child's stream. Television
// leaves the alternate screen, then prints the entry, so the answer is the
// last plain line after the final exit.
func palettePick(tail []byte) string {
	if i := bytes.LastIndex(tail, []byte("\x1b[?1049l")); i >= 0 {
		tail = tail[i+len("\x1b[?1049l"):]
	}
	lines := strings.Split(stripANSI(string(tail)), "\n")
	for j := len(lines) - 1; j >= 0; j-- {
		v := strings.TrimSpace(strings.ReplaceAll(lines[j], "\r", ""))
		if v != "" {
			return v
		}
	}
	return ""
}

// openPalette raises the float over the panes. It toggles: a second call while
// it is open shuts it.
func openPalette(t *tui) {
	if t.pal != nil {
		closePalette(t)
		return
	}
	if t.snap == nil || t.cols < 1 || t.rows < 1 {
		return
	}
	p, err := startPalette(t.session, paletteRect(t.cols, t.rows))
	if err != nil {
		t.notify("palette: " + err.Error())
		t.disarm()
		return
	}
	t.pal = p
	t.palOut = p.out
	t.armed = false
	t.inbuf = nil
	t.dirty = true
}

// paletteBytes feeds the child's output into the float's screen and keeps the
// tail that carries the choice.
func paletteBytes(t *tui, b []byte) {
	p := t.pal
	if p == nil {
		return
	}
	_, _ = p.vt.Write(b)
	p.tail = append(p.tail, b...)
	if len(p.tail) > palTailMax {
		p.tail = p.tail[len(p.tail)-palTailMax:]
	}
	t.dirty = true
}

// paletteClosed reads the choice, drops the float, then acts on it. The tail
// is copied before stop, because stop closes the PTY the tail came from.
func paletteClosed(t *tui) {
	p := t.pal
	t.pal = nil
	t.palOut = nil
	if p == nil {
		return
	}
	tail := append([]byte(nil), p.tail...)
	p.stop()
	t.dirty = true
	target := palettePick(tail)
	if target == "" {
		return
	}
	t.dispatchPick(target)
}

// closePalette ends the float without acting on a choice.
func closePalette(t *tui) {
	p := t.pal
	t.pal = nil
	t.palOut = nil
	if p == nil {
		return
	}
	p.stop()
	t.dirty = true
}

// dispatchPick turns a palette row target into the action that goes there.
// A workspace is sN, a window is wN, a pane is wNpM.
func (t *tui) dispatchPick(target string) {
	switch {
	case strings.HasPrefix(target, "s") && allDigits(target[1:]):
		t.action("focus-workspace", func(m *Msg) { m.Target = target })
	case strings.HasPrefix(target, "w") && strings.Contains(target, "p"):
		t.action("focus", func(m *Msg) { m.Target = target })
	case strings.HasPrefix(target, "w") && allDigits(target[1:]):
		t.action("focus-window", func(m *Msg) { m.Target = target })
	default:
		t.notify("palette: no target " + target)
	}
}
