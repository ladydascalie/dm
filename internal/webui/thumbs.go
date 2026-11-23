package webui

import (
	"context"
	"fmt"
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
// shows its first ones: the page is a phone, not a wall. The fleet reply
// publishes the same number, so the page asks for exactly what it paints.
const maxThumbs = 8

// paneSilence is how long a pane may stay quiet after a subscription before the
// stream reports it as gone. It is a variable so a test can sit on the edge
// between the report and the pane's own answer.
var paneSilence = 2 * time.Second

// silenceGate, when a test installs one, holds a silence report between the
// timer firing and its send. That is the window the report would fall through
// if the stream did not wait for it, and the only way to hold it still.
var silenceGate chan struct{}

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
	ids, err := paneList(r.URL.Query().Get("panes"))
	if err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusBadRequest)
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
			h.shots(ctx, id, &wg, events)
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
// pending is the stream's wait group: the silence report joins it, so the event
// channel cannot close under a send from the timer.
func (h *Handler) shots(ctx context.Context, id string, pending *sync.WaitGroup, out chan<- shotEvent) {
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
	//
	// The timer's wait entry is the permission to send: the channel closes only
	// after every entry is done, so no send can race the close. stopSilent
	// settles the entry exactly once. When Stop wins the callback never runs, so
	// its Done is the stopper's; when Stop loses the callback is running and its
	// own Done lands.
	pending.Add(1)
	silent := time.AfterFunc(paneSilence, func() {
		defer pending.Done()
		if silenceGate != nil {
			<-silenceGate
		}
		select {
		case out <- shotEvent{Pane: id, Gone: true}:
		case <-ctx.Done():
		}
	})
	stopSilent := func() {
		if silent.Stop() {
			pending.Done()
		}
	}
	defer stopSilent()
	for {
		m, err := wire.ReadMsg(c)
		if err != nil {
			drop(ctx, out, id)
			return
		}
		if m.Type != "screen" || m.VTSnap == nil {
			continue
		}
		stopSilent()
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

// paneList parses the panes query. An id that is not a pane id refuses the
// whole list: a page that asks for a bad pane learns it, instead of painting a
// tile that can never fill. A list longer than maxThumbs keeps its first ones,
// as the limit promises, so a nine pane window paints eight tiles and not none.
func paneList(q string) ([]string, error) {
	seen := make(map[string]bool, maxThumbs)
	out := make([]string, 0, maxThumbs)
	for _, id := range strings.Split(q, ",") {
		if !paneIDPattern.MatchString(id) {
			return nil, fmt.Errorf("bad pane id %q", id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if len(out) >= maxThumbs {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}
