package webui

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"dm/internal/paths"
	"dm/internal/vt"
	"dm/internal/wire"
)

// maxThumbs bounds one thumbnail stream. A window with more panes than this
// shows its first ones: the page is a phone, not a wall.
const maxThumbs = 8

// shotEvent is one pane's picture on the thumbnail stream.
type shotEvent struct {
	Pane string       `json:"pane"`
	Snap *vt.Snapshot `json:"snap,omitempty"`
	// Gone reports a pane that would not give a picture, so the tile can say
	// so instead of staying blank.
	Gone bool `json:"gone,omitempty"`
}

// thumbs serves the picture stream for the thumbnail grid: one pane socket per
// pane, multiplexed into one event stream, so a phone holds one connection
// instead of one per tile.
//
// Each pane is asked for a screen subscription, so the pane coalesces its
// output and the page never polls. The stream ends when every pane stops, which
// lets the browser's own reconnect start a fresh set.
func (h *Handler) thumbs(w http.ResponseWriter, r *http.Request) {
	ids := paneList(r.URL.Query().Get("panes"))
	if len(ids) == 0 || len(ids) > maxThumbs {
		http.Error(w, "dm web: bad pane list", http.StatusBadRequest)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "dm web: response is not streamable", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	events := make(chan shotEvent, 64)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			h.shots(ctx, id, events)
		}(id)
	}
	go func() {
		wg.Wait()
		close(events)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	for ev := range events {
		if ev.Gone {
			sendAny(w, fl, "gone", ev)
			continue
		}
		sendAny(w, fl, "shot", ev)
	}
	sendAny(w, fl, "end", map[string]bool{"end": true})
}

// shots subscribes one pane and forwards its pictures until the context ends.
func (h *Handler) shots(ctx context.Context, id string, out chan<- shotEvent) {
	c, err := net.DialTimeout("unix", paths.PaneSockAt(h.cfg.Root, h.cfg.Session, id), 2*time.Second)
	if err != nil {
		drop(ctx, out, id)
		return
	}
	defer func() { _ = c.Close() }()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-done:
		}
	}()

	if err := wire.WriteMsg(c, wire.Msg{Type: "req", Action: "screen-sub", ID: 1}); err != nil {
		drop(ctx, out, id)
		return
	}
	// A pane daemon older than this build ignores the subscription and answers
	// nothing, so silence is a fact worth painting: no picture, not a blank
	// tile that looks like a quiet pane.
	first := time.AfterFunc(2*time.Second, func() {
		select {
		case out <- shotEvent{Pane: id, Gone: true}:
		case <-ctx.Done():
		}
	})
	defer first.Stop()
	for {
		m, err := wire.ReadMsg(c)
		if err != nil {
			drop(ctx, out, id)
			return
		}
		if m.Type != "screen" || m.VTSnap == nil {
			continue
		}
		first.Stop()
		select {
		case out <- shotEvent{Pane: id, Snap: m.VTSnap}:
		case <-ctx.Done():
			return
		}
	}
}

// drop reports a pane that will not paint. A cancelled page is not news, so a
// dead context sends nothing.
func drop(ctx context.Context, out chan<- shotEvent, id string) {
	select {
	case out <- shotEvent{Pane: id, Gone: true}:
	case <-ctx.Done():
	}
}

// paneList parses and validates the panes query. An id that is not a pane id
// is dropped, so only the shape a socket name is built from survives.
func paneList(q string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, maxThumbs)
	for _, id := range strings.Split(q, ",") {
		if !paneIDPattern.MatchString(id) || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
