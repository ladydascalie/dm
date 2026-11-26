package webui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dm/internal/wire"
)

// sessionTail is how many turns the endpoint sends: a phone reads the tail of
// a conversation, not its whole history.
const sessionTail = 3

// stepBody is how many characters of one body a step carries: a tool's output,
// a call's arguments or a thought. The count of lines left over rides in the
// step's more field, so the page can say what it cut.
const stepBody = 4000

// revTail is how many trailing bytes the revision hashes. Enough to hold the
// end of the last JSONL entry, so an append moves the hash even when the
// filesystem's modification time is coarse.
const revTail = 512

var (
	errNoSession  = errors.New("no session")
	errRefused    = errors.New("session file refused")
	errBadSession = errors.New("session file is not JSONL")
)

// sessionReply is one pane's agent session, rendered for the page. Rev
// identifies the file's state, so an unchanged poll costs a stat and the page
// skips the body. Turns is absent from an unchanged reply.
type sessionReply struct {
	Pane      string        `json:"pane"`
	Rev       string        `json:"rev"`
	Changed   bool          `json:"changed"`
	SessionID string        `json:"sessionId,omitempty"`
	Turns     []sessionTurn `json:"turns,omitempty"`
}

// readSession is the full read the endpoint performs once the rev moved. It is
// a variable so a test can count the reads; production reads the whole file.
var readSession = func(f *os.File) ([]byte, error) { return io.ReadAll(f) }

// sessionTurn is one user message and everything the agent did about it.
type sessionTurn struct {
	At  string `json:"at"`
	Ask string `json:"ask"`
	// AskHTML is the ask rendered as the page paints it, and Ask stays for the
	// views that want one line of plain text.
	AskHTML string        `json:"askHtml,omitempty"`
	Steps   []sessionStep `json:"steps"`
	// Live is the newest unfinished step, absent when every step settled.
	Live    *sessionStep   `json:"live,omitempty"`
	Summary sessionSummary `json:"summary"`
	// calls maps a toolCall id to its step, unexported: it pairs a call with
	// its result and never reaches the page.
	calls map[string]int
}

// sessionStep is one assistant content block, or the result a tool call
// produced.
type sessionStep struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	// HTML is Text rendered for the transcript. Text stays for the activity
	// view, which flattens every step to one plain line.
	HTML    string `json:"html,omitempty"`
	MS      int64  `json:"ms,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Summary string `json:"summary,omitempty"`
	OK      *bool  `json:"ok,omitempty"`
	Exit    *int   `json:"exit,omitempty"`
	Output  string `json:"output,omitempty"`
	// Args is the call's arguments, printed as the JSON they are, so a tap
	// shows what the tool was told and not only what it answered.
	Args     string `json:"args,omitempty"`
	ArgsMore int    `json:"argsMore,omitempty"`
	More     int    `json:"more,omitempty"`
}

// sessionSummary counts one turn: the calls it made, how many failed, how long
// its tools ran, what it cost and which model answered.
type sessionSummary struct {
	Calls  int     `json:"calls"`
	Errors int     `json:"errors"`
	ToolMs int64   `json:"toolMs"`
	Cost   float64 `json:"cost"`
	Model  string  `json:"model"`
}

// session serves one pane's agent session as turns. The file comes from the
// pane's OSC 7502 report, through the snapshot's SessionFile; the handler
// never follows a path the pane did not report and never leaves the pi
// sessions tree.
func (h *Handler) session(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	if !paneIDPattern.MatchString(pane) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad pane id"})
		return
	}
	reported, err := h.sessionFileFor(pane)
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"error": err.Error()})
		return
	}
	root, err := h.sessionsRoot()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	file, err := resolveSession(root, reported)
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"error": err.Error()})
		return
	}
	f, err := os.Open(file)
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"error": err.Error()})
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"error": err.Error()})
		return
	}
	rev, err := revFor(f, fi.Size(), fi.ModTime())
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"error": err.Error()})
		return
	}
	// An unchanged file costs a stat and the tail read: the whole file is read
	// and parsed only when the caller's rev is stale.
	if since := r.URL.Query().Get("since"); since != "" && since == rev {
		writeJSON(w, http.StatusOK, sessionReply{Pane: pane, Rev: rev})
		return
	}
	b, err := readSession(f)
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"error": err.Error()})
		return
	}
	id, turns, err := parseSession(b)
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sessionReply{
		Pane:      pane,
		Rev:       rev,
		Changed:   true,
		SessionID: id,
		Turns:     turns,
	})
}

// revFor identifies a file's state for the poll without reading all of it. The
// input is the size, the modification time and a hash of the last bytes. Every
// way a session file changes moves at least one part:
//   - an append grows the size and moves the mtime;
//   - a rewrite moves the mtime, and new trailing bytes move the hash;
//   - a compaction truncates and rewrites, so the size and the trailing bytes
//     both change.
//
// The parts cover each other: a same-size rewrite inside one mtime tick still
// moves the tail hash, and an append of bytes identical to the old tail still
// moves the size and the mtime.
func revFor(f *os.File, size int64, mtime time.Time) (string, error) {
	n := size
	if n > revTail {
		n = revTail
	}
	tail := make([]byte, n)
	if n > 0 {
		if _, err := f.ReadAt(tail, size-n); err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
	}
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d:%d:", size, mtime.UnixNano())
	h.Write(tail)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// statusFor maps a session read's refusal to the HTTP status the page expects.
func statusFor(err error) int {
	switch {
	case errors.Is(err, errNoSession):
		return http.StatusNotFound
	case errors.Is(err, errRefused):
		return http.StatusForbidden
	case errors.Is(err, errBadSession):
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}

// sessionFileFor asks the session server for the panes, and returns the file
// the named pane's agent reported. The server is the only source: a pane the
// snapshot does not list has no session to read.
func (h *Handler) sessionFileFor(pane string) (string, error) {
	m, err := h.rpc(wire.Msg{Action: "list"})
	if err != nil {
		return "", err
	}
	if !m.OK {
		return "", fmt.Errorf("%s", m.Error)
	}
	var res struct {
		Snapshot wire.Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		return "", fmt.Errorf("list reply: %w", err)
	}
	for _, p := range res.Snapshot.All {
		if p.ID != pane {
			continue
		}
		if p.SessionFile == "" {
			return "", errNoSession
		}
		return p.SessionFile, nil
	}
	return "", errNoSession
}

// sessionsRoot is the pi sessions tree the viewer may read: the config's
// directory, or ~/.pi/agent/sessions.
func (h *Handler) sessionsRoot() (string, error) {
	dir := h.cfg.SessionsDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("pi sessions: %w", err)
		}
		dir = filepath.Join(home, ".pi", "agent", "sessions")
	}
	return filepath.Abs(dir)
}

// resolveSession returns the real path of p when it sits inside root. It
// resolves symlinks on both sides first, so a link that points out of the tree
// is refused instead of followed, whatever the pane's frame said.
func resolveSession(root, p string) (string, error) {
	if p == "" {
		return "", errNoSession
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", errNoSession
	}
	realFile, err := filepath.EvalSymlinks(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errNoSession
		}
		return "", errRefused
	}
	rel, err := filepath.Rel(realRoot, realFile)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errRefused
	}
	return realFile, nil
}

// sessionLine is one JSONL record. Only the two types the viewer reads carry
// fields here; the rest (model_change, custom) parse and are skipped.
type sessionLine struct {
	Type      string     `json:"type"`
	ID        string     `json:"id"`
	Timestamp string     `json:"timestamp"`
	Message   *piMessage `json:"message"`
}

// piMessage is one message record, whichever role wrote it.
type piMessage struct {
	Role       string          `json:"role"`
	Timestamp  json.RawMessage `json:"timestamp"`
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model"`
	DurationMs int64           `json:"durationMs"`
	Usage      *piUsage        `json:"usage"`
	ToolName   string          `json:"toolName"`
	ToolCallID string          `json:"toolCallId"`
	IsError    bool            `json:"isError"`
	Details    *piExit         `json:"details"`
	Structured *piExit         `json:"structuredContent"`
}

// piExit is the exit code a tool may record beside its content, in either the
// details block or the structured content newer tools write.
type piExit struct {
	ExitCode *int `json:"exitCode"`
	Exit     *int `json:"exit_code"`
}

func (e *piExit) code() (int, bool) {
	if e == nil {
		return 0, false
	}
	if e.ExitCode != nil {
		return *e.ExitCode, true
	}
	if e.Exit != nil {
		return *e.Exit, true
	}
	return 0, false
}

// piUsage is the token and cost record an assistant message carries.
type piUsage struct {
	Cost *struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

// piBlock is one assistant content block, or one text block of a tool result.
type piBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// parseSession reads the pi session JSONL into the tail of its turns, with
// every tool call paired with the result that carries its id. pi appends one
// entry per write and the write is not atomic, so a poll can land in the middle
// of the final entry. A trailing line that is not complete JSON is that partial
// append: it is dropped and the turns before it stand. A line that fails
// anywhere else is corruption, and refusing the file says so.
func parseSession(b []byte) (string, []sessionTurn, error) {
	id := ""
	var turns []sessionTurn
	lines := bytes.Split(b, []byte("\n"))
	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var l sessionLine
		if err := json.Unmarshal(line, &l); err != nil {
			if i == len(lines)-1 {
				break
			}
			return "", nil, fmt.Errorf("%w: line %d", errBadSession, i+1)
		}
		switch l.Type {
		case "session":
			id = l.ID
		case "message":
			if l.Message == nil {
				continue
			}
			switch l.Message.Role {
			case "user":
				turns = append(turns, newTurn(l))
			case "assistant":
				if len(turns) == 0 {
					continue
				}
				turns[len(turns)-1].addAssistant(l.Message)
			case "toolResult":
				if len(turns) == 0 {
					continue
				}
				turns[len(turns)-1].addResult(l.Message)
			}
		}
	}
	if len(turns) > sessionTail {
		turns = turns[len(turns)-sessionTail:]
	}
	if len(turns) > 0 {
		turns[len(turns)-1].finish()
	}
	return id, turns, nil
}

// newTurn opens a turn at a user message. The ask is that message's text.
func newTurn(l sessionLine) sessionTurn {
	at := l.Timestamp
	if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
		at = t.Format(time.RFC3339Nano)
	}
	ask := userText(l.Message.Content)
	return sessionTurn{
		At:      at,
		Ask:     ask,
		AskHTML: markdownHTML(ask),
		Steps:   []sessionStep{},
		calls:   map[string]int{},
	}
}

// userText renders a user message's content. The field is a string in older
// sessions and a list of text blocks in newer ones.
func userText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []piBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// addAssistant turns one assistant message's content blocks into steps, in
// order: prose, thinking, and one tool step per call. The message's cost and
// model join the turn's summary.
func (t *sessionTurn) addAssistant(m *piMessage) {
	var blocks []piBlock
	_ = json.Unmarshal(m.Content, &blocks)
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			t.Steps = append(t.Steps, sessionStep{
				Kind: "text",
				Text: b.Text,
				HTML: markdownHTML(b.Text),
			})
		case "thinking":
			if strings.TrimSpace(b.Thinking) == "" {
				continue
			}
			thought, more := cutBody(b.Thinking)
			t.Steps = append(t.Steps, sessionStep{
				Kind: "thinking",
				MS:   m.DurationMs,
				Text: thought,
				HTML: markdownHTML(thought),
				More: more,
			})
		case "toolCall":
			step := sessionStep{
				Kind:    "tool",
				Tool:    b.Name,
				Summary: toolSummary(b.Name, b.Arguments),
			}
			step.Args, step.ArgsMore = prettyArgs(b.Arguments)
			t.calls[b.ID] = len(t.Steps)
			t.Steps = append(t.Steps, step)
			t.Summary.Calls++
		}
	}
	if m.Model != "" {
		t.Summary.Model = m.Model
	}
	if m.Usage != nil && m.Usage.Cost != nil {
		t.Summary.Cost += m.Usage.Cost.Total
	}
}

// addResult hangs one tool result on the step its call made, by id. The step
// keeps the tool's duration, its ok flag, its exit code, and the head of its
// output with the count of lines left over.
func (t *sessionTurn) addResult(m *piMessage) {
	at, ok := t.calls[m.ToolCallID]
	if !ok {
		return
	}
	st := &t.Steps[at]
	text := blocksText(m.Content)
	good := !m.IsError
	st.OK = &good
	st.MS = m.DurationMs
	t.Summary.ToolMs += m.DurationMs
	if m.IsError {
		t.Summary.Errors++
	}
	if st.Tool == "bash" {
		exit := 0
		if n, ok := m.Details.code(); ok {
			exit = n
		} else if n, ok := m.Structured.code(); ok {
			exit = n
		} else if n, ok := exitFromText(text); ok {
			exit = n
		} else if m.IsError {
			exit = 1
		}
		st.Exit = &exit
	}
	st.Output, st.More = cutBody(text)
}

// finish marks the newest unfinished step of the newest turn: a tool call
// whose result never came is what the page shows as live.
func (t *sessionTurn) finish() {
	for i := len(t.Steps) - 1; i >= 0; i-- {
		if t.Steps[i].Kind == "tool" && t.Steps[i].OK == nil {
			live := t.Steps[i]
			t.Live = &live
			return
		}
	}
}

// blocksText joins the text blocks of a content list.
func blocksText(raw json.RawMessage) string {
	var blocks []piBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// prettyArgs prints a call's arguments the way a reader expects to see JSON:
// indented, with the cut the other bodies carry so a write tool's whole file
// cannot land in one step.
func prettyArgs(args json.RawMessage) (string, int) {
	if len(args) == 0 {
		return "", 0
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, args, "", "  "); err != nil {
		return "", 0
	}
	return cutBody(buf.String())
}

// toolSummary picks the one argument that says what a call did. A tool with
// no argument worth showing gets an empty summary.
func toolSummary(name string, args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return ""
	}
	str := func(key string) string {
		s, _ := m[key].(string)
		return s
	}
	switch name {
	case "bash":
		return str("command")
	case "read", "write":
		return str("path")
	case "codemode":
		return str("code")
	case "ask_user_question", "dm_turn":
		if q := firstQuestion(m); q != "" {
			return q
		}
		return str("question")
	}
	return ""
}

// firstQuestion returns the first question of an ask call, whether the retired
// ask_user_question tool or dm_turn carried it.
func firstQuestion(m map[string]any) string {
	qs, _ := m["questions"].([]any)
	if len(qs) == 0 {
		return ""
	}
	q, _ := qs[0].(map[string]any)
	s, _ := q["question"].(string)
	return s
}

// exitLine matches the status pi's bash tool appends when a command fails.
var exitLine = regexp.MustCompile(`Command exited with code (-?[0-9]+)`)

// exitFromText reads the exit code out of a bash result's text.
func exitFromText(text string) (int, bool) {
	m := exitLine.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// cutBody keeps the head of one body at stepBody characters and counts the
// lines left over.
func cutBody(text string) (string, int) {
	r := []rune(text)
	if len(r) <= stepBody {
		return text, 0
	}
	rest := strings.TrimRight(string(r[stepBody:]), "\n")
	more := 0
	if rest != "" {
		more = strings.Count(rest, "\n") + 1
	}
	return string(r[:stepBody]), more
}
