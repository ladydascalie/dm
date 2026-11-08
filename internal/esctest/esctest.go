// Package esctest runs esctest2 against the dm VT.
//
// esctest2 is a conformance suite for terminal emulators. It is a Python
// program that writes escape sequences to its stdout and reads replies from
// its stdin. This package puts it on a pty, feeds everything it writes through
// the dm VT, and answers the queries the VT cannot answer itself from
// the terminal's state.
//
// esctest2 is GPL-2.0. It is not vendored here. The harness finds it on disk
// and runs it as a separate program, the same way Ghostty does.
package esctest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"dm/internal/vt"

	"github.com/creack/pty"
)

// Options controls a run.
type Options struct {
	// Dir is the esctest2 directory. Empty means search: $ESCTEST_DIR, then
	// internal/esctest/testdata/esctest, then /tmp/esctest2.
	Dir string
	// Args are extra arguments for esctest.py, after the fixed ones.
	Args []string
	// Verbose names each sequence the harness answers on Stderr.
	Verbose bool
	// Stdout receives the esctest log. Nil means os.Stdout.
	Stdout io.Writer
	// Stderr receives verbose output. Nil means os.Stderr.
	Stderr io.Writer
}

// Find returns the directory holding esctest.py. It searches dir, then
// $ESCTEST_DIR, then internal/esctest/testdata/esctest, then /tmp/esctest2.
func Find(dir string) (string, error) {
	var candidates []string
	if dir != "" {
		candidates = append(candidates, dir)
	}
	if env := os.Getenv("ESCTEST_DIR"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates,
		filepath.Join("internal", "esctest", "testdata", "esctest"),
		"/tmp/esctest2",
	)
	for _, c := range candidates {
		if p, ok := esctestPy(c); ok {
			return p, nil
		}
	}
	return "", fmt.Errorf("esctest2 not found: set ESCTEST_DIR or run the fetch target")
}

// esctestPy accepts either the repo root or the package directory.
func esctestPy(dir string) (string, bool) {
	for _, p := range []string{
		filepath.Join(dir, "esctest.py"),
		filepath.Join(dir, "esctest", "esctest.py"),
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return filepath.Dir(p), true
		}
	}
	return "", false
}

// fixedArgs returns the esctest2 arguments the harness always passes, before
// the caller's own. The options declare what the harness answers itself:
// allowC2Controls for the 8-bit C1 forms, and xtermWinopsEnabled because the
// harness handles the window operations, including the resize that makes
// DECCOLM work.
func fixedArgs(logPath string) []string {
	return []string{
		"esctest.py",
		"--expected-terminal=xterm",
		"--xterm-checksum=411",
		"--xterm-reverse-wrap=411",
		"--timeout=0.2",
		"--options", "allowC2Controls", "xtermWinopsEnabled",
		"--logfile=" + logPath,
	}
}

// Run starts esctest2 on a 25x80 pty, answers its queries from the dm VT,
// copies the log to opts.Stdout, and returns esctest2's exit code.
func Run(ctx context.Context, opts Options) (int, error) {
	dir, err := Find(opts.Dir)
	if err != nil {
		return 1, err
	}

	logFile, err := os.CreateTemp("", "esctest-*.log")
	if err != nil {
		return 1, err
	}
	logPath := logFile.Name()
	logFile.Close()
	os.Remove(logPath)
	defer os.Remove(logPath)

	args := append(fixedArgs(logPath), opts.Args...)

	cmd := exec.CommandContext(ctx, "python3", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 25, Cols: 80})
	if err != nil {
		return 1, err
	}
	defer ptmx.Close()

	h := newHarness(ptmx, opts)
	runErr := h.run()
	waitErr := cmd.Wait()
	if runErr != nil {
		return 1, runErr
	}

	if data, err := os.ReadFile(logPath); err == nil {
		out := opts.Stdout
		if out == nil {
			out = os.Stdout
		}
		_, _ = out.Write(data)
	}

	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if code < 0 {
		code = 1
	}
	if waitErr != nil && code == 0 {
		code = 1
	}
	return code, nil
}

// harness owns the pty master and the terminal the stream is replayed into.
type harness struct {
	ptmx    *os.File
	vt      *vt.Term
	reports *vt.Term
	verbose bool
	errw    io.Writer
	buf     []byte
	st      state
}

func newHarness(ptmx *os.File, opts Options) *harness {
	errw := opts.Stderr
	if errw == nil {
		errw = os.Stderr
	}
	h := &harness{ptmx: ptmx, verbose: opts.Verbose, errw: errw}
	h.vt = vt.NewTerm(80, 25)
	h.reports = vt.NewTerm(80, 25)
	h.st = newState(25, 80)
	return h
}

// run reads the pty until the child closes it. Linux reports the close as EIO.
func (h *harness) run() error {
	buf := make([]byte, 4096)
	for {
		n, err := h.ptmx.Read(buf)
		if n > 0 {
			h.feed(buf[:n])
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) {
				return nil
			}
			return err
		}
	}
}

// feed splits the stream at every sequence the harness handles. Everything
// before a handled sequence is written to the terminal first, so the terminal
// has processed it before the answer is computed.
func (h *harness) feed(chunk []byte) {
	h.buf = append(h.buf, translateC1(chunk)...)
	for {
		i := 0
		qStart, qEnd := -1, -1
		for i < len(h.buf) {
			s, n, ok := scan(h.buf[i:])
			if !ok {
				break
			}
			if handled(s) {
				qStart, qEnd = i, i+n
				break
			}
			h.observe(s)
			i += n
		}
		if qStart >= 0 {
			if qStart > 0 {
				h.writeVT(h.buf[:qStart])
			}
			s, _, _ := scan(h.buf[qStart:qEnd])
			h.answer(s, h.buf[qStart:qEnd])
			h.buf = append(h.buf[:0], h.buf[qEnd:]...)
			continue
		}
		if i > 0 {
			h.writeVT(h.buf[:i])
		}
		h.buf = append(h.buf[:0], h.buf[i:]...)
		return
	}
}

// writeVT feeds the terminal under test and the report VT the same bytes, so
// the report VT's cursor and grid stay where the tested terminal's are.
func (h *harness) writeVT(b []byte) {
	if len(b) == 0 {
		return
	}
	h.vt.Write(b)
	h.reports.Write(b)
}

func (h *harness) reply(s, name string) {
	if h.verbose {
		fmt.Fprintf(h.errw, "answer %s -> %q\n", name, s)
	}
	_, _ = h.ptmx.Write([]byte(s))
}

func (h *harness) logf(format string, args ...any) {
	if h.verbose {
		fmt.Fprintf(h.errw, format+"\n", args...)
	}
}

func (h *harness) rows() int {
	_, r := h.vt.Grid().Size()
	return r
}

func (h *harness) cols() int {
	c, _ := h.vt.Grid().Size()
	return c
}
