package webui

import (
	"encoding/json"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"

	"dm/internal/wire"
)

// textReply is one pane's visible screen, ready to be wrapped by whatever font
// and width the reader has.
//
// This is the page's pane view: the pane as the text it printed, not as a grid
// of cells. The grid belongs to the pane's own terminal, and a phone is not
// that terminal. Rev is a hash of the text, so a poll on a quiet pane costs an
// id and no body.
type textReply struct {
	Pane    string   `json:"pane"`
	Rev     string   `json:"rev"`
	Changed bool     `json:"changed"`
	Lines   []string `json:"lines,omitempty"`
	State   string   `json:"state,omitempty"`
	Message string   `json:"message,omitempty"`
}

// text serves one pane's screen as text. It reads the pane through the read
// action, which is the primitive the CLI uses, so nothing here depends on the
// pane being drawn at a particular size.
func (h *Handler) text(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("pane")
	if !paneIDPattern.MatchString(id) {
		http.Error(w, "dm web: bad pane id", http.StatusBadRequest)
		return
	}
	m, err := h.rpc(wire.Msg{Action: "read", Target: id})
	if err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusBadGateway)
		return
	}
	if !m.OK {
		http.Error(w, "dm web: "+m.Error, http.StatusBadGateway)
		return
	}
	// The read action answers in Res: the screen lines and the status.
	var res struct {
		Lines  []string    `json:"lines"`
		Status wire.Status `json:"status"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		http.Error(w, "dm web: read reply: "+err.Error(), http.StatusBadGateway)
		return
	}
	out := textReply{Pane: id, State: res.Status.State, Message: res.Status.Message}
	// A screen of 46 rows is mostly blank at the bottom: keep the text the pane
	// printed and drop the emptiness under it.
	out.Lines = trimBlankTail(res.Lines)
	text := strings.Join(out.Lines, "\n")
	out.Rev = revOf(text)
	if r.URL.Query().Get("since") == out.Rev {
		out.Changed = false
		out.Lines = nil
	} else {
		out.Changed = true
	}
	writeJSON(w, http.StatusOK, out)
}

// trimBlankTail drops the empty rows under the last line the pane printed.
func trimBlankTail(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}

// revOf is the text's identity: two polls of one unchanged screen agree on it.
func revOf(text string) string {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(text))
	return strconv.FormatUint(sum.Sum64(), 36)
}
