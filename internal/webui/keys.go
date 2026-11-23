package webui

import (
	"encoding/json"
	"net/http"

	"dm/internal/wire"
)

// paneKeys is the whole keyboard a phone is offered. A phone has no Escape and
// no Control, and these two are the keys a reader actually needs from one: Esc
// interrupts a running turn and declines an open question, and Ctrl+C ends a
// command in a shell pane. Nothing else is accepted, so the page cannot become
// a general keyboard driver for the desk.
var paneKeys = map[string]byte{
	"esc":   0x1b,
	"ctrlc": 0x03,
}

// keys sends one key press to a pane. It is the phone's stop, and the only way
// to interrupt a run without walking back to the terminal.
func (h *Handler) keys(w http.ResponseWriter, r *http.Request) {
	if h.cfg.ReadOnly {
		http.Error(w, "dm web: read only", http.StatusForbidden)
		return
	}
	var req struct {
		Pane string `json:"pane"`
		Key  string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !paneIDPattern.MatchString(req.Pane) {
		http.Error(w, "dm web: bad pane id", http.StatusBadRequest)
		return
	}
	k, ok := paneKeys[req.Key]
	if !ok {
		http.Error(w, "dm web: unknown key "+req.Key+": want esc or ctrlc", http.StatusBadRequest)
		return
	}
	h.reply(w, wire.Msg{Action: "keys", Target: req.Pane, Keys: []byte{k}})
}
