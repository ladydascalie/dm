package pane

import (
	"testing"
	"time"

	"dm/internal/vt"
)

// TestPaneFlushesQuietCluster proves the daemon places the parser's pending
// cluster once its output stops, so the read action shows a program's last
// character without waiting for the next chunk.
func TestPaneFlushesQuietCluster(t *testing.T) {
	t.Parallel()
	p := &paneProc{vt: vt.NewTerm(20, 3), ring: &scrollRing{}, subs: map[*subConn]bool{}}
	p.mu.Lock()
	p.publishLocked([]byte("日"))
	p.mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		got := p.vt.Grid().Cell(0, 0).Content
		p.mu.Unlock()
		if got == "日" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("cell 0,0 = %q after 2s, want 日", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
