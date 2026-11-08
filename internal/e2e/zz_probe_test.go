package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

type buf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *buf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *buf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func (w *buf) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.b.Reset()
}

func dmCLI(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("/tmp/dmprobe", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("dm %v: %v: %s", args, err, out)
	}
	return string(out)
}

var scrollRe = regexp.MustCompile(`SCROLL [^ ]+ \+\d+/\d+`)

func TestProbeWheelAltLeft(t *testing.T) {
	cmd := exec.Command("/tmp/dmprobe", "-s", "probe2")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Kill()
		f.Close()
	}()
	var out buf
	go func() {
		b := make([]byte, 65536)
		for {
			n, err := f.Read(b)
			if n > 0 {
				out.Write(b[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(1500 * time.Millisecond)

	var list struct {
		Snapshot struct {
			Panes []struct {
				ID   string `json:"id"`
				Rect struct{ X, Y, W, H int }
			} `json:"panes"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(dmCLI(t, "list", "-s", "probe2")), &list); err != nil {
		t.Fatal(err)
	}
	rects := map[string]struct{ X, Y, W, H int }{}
	for _, p := range list.Snapshot.Panes {
		rects[p.ID] = p.Rect
		t.Logf("pane %s rect %+v", p.ID, p.Rect)
	}

	wheel := func(id string, up bool) {
		r := rects[id]
		btn := 64
		if !up {
			btn = 65
		}
		seq := fmt.Sprintf("\x1b[<%d;%d;%dM", btn, r.X+2+1, r.Y+2+1)
		out.Reset()
		if _, err := f.Write([]byte(seq)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(700 * time.Millisecond)
		got := out.String()
		m := scrollRe.FindString(got)
		t.Logf("wheel over %s: status=%q bytes=%d", id, m, len(got))
	}

	wheel("w1p2", true)
	wheel("w1p2", true)
	wheel("w1p1", true)
	wheel("w1p1", true)
	wheel("w1p2", true)
}
