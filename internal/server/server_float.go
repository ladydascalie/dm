package server

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"dm/internal/wire"

	"github.com/creack/pty"
)

// floatProc is one floating command process: a PTY child the client that
// opened it types into and that dies with it. The server does not lay it out
// and it never reaches the snapshot: the client owns the view.
type floatProc struct {
	id    string
	owner *client
	pty   *os.File
	cmd   *exec.Cmd

	// mu guards the size and the dead flag. The pty itself is written and
	// resized off s.mu, so a slow child never holds the session.
	mu   sync.Mutex
	cols int
	rows int
	dead bool
}

// floatSpec is what the client asks for on float-open. The client picks the id
// and the size; the server supplies the folder and the command.
type floatSpec struct {
	id      string
	target  string
	cwd     string
	command string
	cols    int
	rows    int
}

// floatEnv is the child's environment: the server's own, plus the pane and
// session it belongs to, plus a TERM when the server has none. A child with no
// TERM paints with whatever the default is, which is not what a pager wants.
func floatEnv(session, target string) []string {
	env := append(os.Environ(), "DM_PANE="+target, "DM_SESSION="+session)
	if os.Getenv("TERM") == "" {
		env = append(env, "TERM=xterm-256color")
	}
	return env
}

// openFloat starts a shell command on a PTY for cl and registers it. The id is
// the client's, so a second float with the same id is refused. Nothing else in
// the session sees the float: its bytes go to the owner alone.
func (s *server) openFloat(cl *client, spec floatSpec) error {
	if spec.id == "" {
		return fmt.Errorf("float-open needs a float id")
	}
	cols, rows := spec.cols, spec.rows
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	s.mu.Lock()
	session := s.st.Name
	if s.floats == nil {
		s.floats = map[string]*floatProc{}
	}
	if s.floats[spec.id] != nil {
		s.mu.Unlock()
		return fmt.Errorf("float %q already exists", spec.id)
	}
	s.mu.Unlock()

	cmd := exec.Command("/bin/sh", "-c", spec.command)
	cmd.Dir = spec.cwd
	cmd.Env = floatEnv(session, spec.target)
	// The child leads its own session, so SIGKILL to its process group takes
	// the shell and anything it started. StartWithSize sets the same flag.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return fmt.Errorf("start float %q: %w", spec.id, err)
	}
	f := &floatProc{id: spec.id, owner: cl, pty: ptmx, cmd: cmd, cols: cols, rows: rows}

	s.mu.Lock()
	if s.floats[spec.id] != nil {
		s.mu.Unlock()
		s.stopFloat(f)
		_ = cmd.Wait()
		return fmt.Errorf("float %q already exists", spec.id)
	}
	s.floats[spec.id] = f
	s.mu.Unlock()

	go s.readFloat(f)
	// The child may exit on its own. Closing the pty here wakes the read loop,
	// which owns the exit frame, so the owner sees the output before it.
	go func() {
		_ = cmd.Wait()
		s.stopFloat(f)
	}()
	return nil
}

// readFloat hands the child's output to the owner and retires the float when
// the child goes away.
func (s *server) readFloat(f *floatProc) {
	buf := make([]byte, 32<<10)
	for {
		n, err := f.pty.Read(buf)
		if n > 0 {
			f.owner.deliver(wire.Msg{Type: "bytes", Target: f.id, Data: append([]byte(nil), buf[:n]...)})
		}
		if err != nil {
			s.retireFloat(f)
			return
		}
	}
}

// retireFloat ends a float once, from the read loop: it forgets the float,
// kills what the child left behind, closes the pty and tells the owner the
// float is gone. A close that already ran makes it a no-op, so one float sends
// exactly one exit frame.
func (s *server) retireFloat(f *floatProc) {
	if !f.markDead() {
		return
	}
	s.mu.Lock()
	if s.floats[f.id] == f {
		delete(s.floats, f.id)
	}
	s.mu.Unlock()
	f.signalGroup()
	_ = f.pty.Close()
	f.owner.deliver(wire.Msg{Type: "exit", Target: f.id})
}

// stopFloat ends the child and closes the pty. It delivers nothing: the read
// loop wakes on the closed pty and sends the one exit frame, so the owner sees
// the last of the output first.
func (s *server) stopFloat(f *floatProc) {
	f.signalGroup()
	_ = f.pty.Close()
}

// closeFloat kills a float the operator closed. It says nothing on the wire:
// the caller closes its view and the read loop's exit frame follows.
func (s *server) closeFloat(id string) {
	s.mu.Lock()
	f := s.floats[id]
	if f == nil {
		s.mu.Unlock()
		return
	}
	delete(s.floats, id)
	s.mu.Unlock()
	s.stopFloat(f)
}

// closeFloatFor closes id only when cl opened it. An unknown id, or a float
// another client owns, is not an error: it is not this client's to close.
func (s *server) closeFloatFor(cl *client, id string) {
	s.mu.Lock()
	f := s.floats[id]
	owned := f != nil && f.owner == cl
	s.mu.Unlock()
	if owned {
		s.closeFloat(id)
	}
}

// killClientFloats ends every float a departing client owns, so a detached or
// dead client leaves no process behind.
func (s *server) killClientFloats(cl *client) {
	s.mu.Lock()
	var ids []string
	for id, f := range s.floats {
		if f.owner == cl {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.closeFloat(id)
	}
}

// floatFor looks a float up by id. It returns nil for an id the server does not
// know, so the caller can fall through to the panes.
func (s *server) floatFor(id string) *floatProc {
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.floats[id]
}

// resizeFloat gives a float the size the client drew it at.
func (s *server) resizeFloat(id string, cols, rows int) {
	f := s.floatFor(id)
	if f == nil || cols < 1 || rows < 1 {
		return
	}
	f.mu.Lock()
	f.cols, f.rows = cols, rows
	f.mu.Unlock()
	_ = pty.Setsize(f.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// writeFloat sends keys to a float. It reports whether id named one.
func (s *server) writeFloat(id string, keys []byte) bool {
	f := s.floatFor(id)
	if f == nil {
		return false
	}
	_, _ = f.pty.Write(keys)
	return true
}

// markDead flips the dead flag once and reports whether this call flipped it.
func (f *floatProc) markDead() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead {
		return false
	}
	f.dead = true
	return true
}

// signalGroup kills the child's process group. A child already gone makes the
// kill fail with ESRCH, which is the answer we want anyway.
func (f *floatProc) signalGroup() {
	if f.cmd != nil && f.cmd.Process != nil {
		_ = syscall.Kill(-f.cmd.Process.Pid, syscall.SIGKILL)
	}
}
