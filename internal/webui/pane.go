package webui

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"

	"dm/internal/wire"
)

// paneIDPattern is the id shape a pane socket name is built from. Only such an
// id reaches a socket path, so a query string cannot walk out of the session
// directory.
var paneIDPattern = regexp.MustCompile(`^[a-z0-9]+p[0-9]+$`)

// msg writes text into a pane, with an optional Enter after it. It is the
// phone's way to answer a prompt without holding a terminal keyboard.
func (h *Handler) msg(w http.ResponseWriter, r *http.Request) {
	if h.cfg.ReadOnly {
		http.Error(w, "dm web: read only", http.StatusForbidden)
		return
	}
	var req struct {
		Pane  string `json:"pane"`
		Text  string `json:"text"`
		Enter bool   `json:"enter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !paneIDPattern.MatchString(req.Pane) {
		http.Error(w, "dm web: bad pane id", http.StatusBadRequest)
		return
	}
	h.reply(w, wire.Msg{Action: "write-to", Target: req.Pane, Msg: req.Text, Enter: req.Enter})
}

// reply runs one write action and answers the browser with the daemon's
// verdict, so a refused pane is visible instead of silent.
func (h *Handler) reply(w http.ResponseWriter, m wire.Msg) {
	got, err := h.rpc(m)
	if err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusBadGateway)
		return
	}
	if !got.OK {
		http.Error(w, "dm web: "+got.Error, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// sendAny writes one server-sent event and flushes it. A write to a socket the
// browser closed fails here and the read loop ends on the next frame.
func sendAny(w http.ResponseWriter, fl http.Flusher, name string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("dm web: event %s: %v", name, err)
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
	fl.Flush()
}
