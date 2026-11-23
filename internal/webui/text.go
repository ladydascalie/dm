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
	// A screen of 46 rows is mostly blank at the bottom, and much of the rest is
	// chrome a phone cannot use: keep what the pane said and drop the rest.
	out.Lines = shapeText(res.Lines)
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

// shapeText keeps what the pane said and drops what a phone cannot use: the
// empty rows under the last line, the long rules across the width, the gaps
// between lines, and the spaces at the end of a row a wrap would carry over.
//
// It runs here, not in the page, so the page paints the lines it is given and
// never learns a rule about terminals.
func shapeText(lines []string) []string {
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = collapseRule(strings.TrimRight(line, " "))
		if line == "" {
			// Five blank lines between two answers are four lines of scrolling
			// for a reader who cannot see them.
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, line)
	}
	return trimBlankTail(out)
}

// ruleLen is the run of box drawing characters left by a long rule. Pi draws a
// rule across the pane; on a phone an 80 column rule wraps to two rows of bars
// that say nothing a short rule does not.
const ruleLen = 3

// ruleMin is the shortest run worth cutting. Shorter runs are things a pane
// meant to draw, not a wall of one character.
const ruleMin = 6

// collapseRule turns each long run of box drawing characters in a line into a
// short one. Runs are found one at a time, so a status row keeps the word
// between two bars.
func collapseRule(line string) string {
	if !strings.ContainsFunc(line, isBox) {
		return line
	}
	var b strings.Builder
	b.Grow(len(line))
	run := 0
	var ch rune
	flush := func() {
		if run >= ruleMin {
			run = ruleLen
		}
		for i := 0; i < run; i++ {
			b.WriteRune(ch)
		}
		run = 0
	}
	for _, r := range line {
		if isBox(r) && (run == 0 || r == ch) {
			if run == 0 {
				ch = r
			}
			run++
			continue
		}
		if run > 0 {
			flush()
		}
		b.WriteRune(r)
	}
	if run > 0 {
		flush()
	}
	return b.String()
}

// isBox reports whether r is one of the box drawing characters, light and
// heavy, that pi draws its rules and frames with.
func isBox(r rune) bool {
	return r >= '\u2500' && r <= '\u257f'
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
