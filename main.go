package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"dm/internal/cells"
	"dm/internal/paths"

	"golang.org/x/term"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "__server":
			exit(runServer(args[1:]))
			return
		case "__pane":
			exit(runPane(args[1:]))
			return
		case "__probe":
			exit(runProbe(args[1:]))
			return
		case "-h", "--help", "help":
			fmt.Print(usageText())
			return
		}
	}
	exit(runCLI(args))
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "dm:", err)
		os.Exit(1)
	}
}

type cmdFlags struct {
	fs         *flag.FlagSet
	session    string
	msg        string
	enter      bool
	dir        string
	msgJSON    string
	lines      int
	scrollback int
	jsonOut    bool
	key        string
	keyPrefix  string
	state      string
	limit      int
	after      int
	timeout    time.Duration
	dry        bool
	args       []string
}

func newFlags(name string) *cmdFlags {
	c := &cmdFlags{fs: flag.NewFlagSet(name, flag.ExitOnError)}
	c.fs.StringVar(&c.session, "s", "main", "session name")
	return c
}

func (c *cmdFlags) parse(args []string) {
	c.fs.StringVar(&c.msg, "msg", "", "message text")
	c.fs.StringVar(&c.msgJSON, "action-json", "", "action JSON")
	c.fs.BoolVar(&c.enter, "enter", false, "press enter after the message")
	c.fs.StringVar(&c.dir, "dir", "right", "split direction: left right up down")
	c.fs.IntVar(&c.lines, "lines", 0, "number of screen lines")
	c.fs.IntVar(&c.scrollback, "scrollback", -1, "history rows above the screen")
	c.fs.BoolVar(&c.jsonOut, "json", false, "print JSON")
	c.fs.StringVar(&c.key, "key", "", "box key")
	c.fs.StringVar(&c.keyPrefix, "prefix", "", "box key prefix")
	c.fs.StringVar(&c.state, "state", "", "box state: note working blocked done error")
	c.fs.IntVar(&c.limit, "limit", 0, "list rows, 0 for the whole box")
	c.fs.IntVar(&c.after, "after", -1, "box seq cursor: entries after this one")
	c.fs.DurationVar(&c.timeout, "timeout", 5*time.Minute, "wait timeout")
	c.fs.BoolVar(&c.dry, "dry-run", false, "print what would be removed")
	_ = c.fs.Parse(reorder(args))
	c.args = c.fs.Args()
}

// reorder moves flags before positionals, so -s may follow a pane id.
func reorder(args []string) []string {
	withValue := map[string]bool{
		"-s": true, "--s": true, "-msg": true, "--msg": true,
		"-action-json": true, "--action-json": true, "-dir": true, "--dir": true,
		"-lines": true, "--lines": true, "-scrollback": true, "--scrollback": true,
		"-key": true, "--key": true, "-state": true, "--state": true,
		"-prefix": true, "--prefix": true, "-after": true, "--after": true,
		"-limit": true, "--limit": true, "-timeout": true, "--timeout": true,
	}
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if withValue[a] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func runCLI(argv []string) error {
	if len(argv) == 0 {
		return runClient("main")
	}
	switch argv[0] {
	case "attach", "a":
		c := newFlags("attach")
		c.parse(argv[1:])
		name := c.session
		if len(c.args) > 0 {
			name = c.args[0]
		}
		return runClient(name)

	case "new":
		c := newFlags("new")
		c.parse(argv[1:])
		m, err := rpc(c.session, Msg{Action: "ping"})
		if err != nil {
			return err
		}
		var res struct {
			Session string `json:"session"`
		}
		_ = json.Unmarshal(m.Res, &res)
		fmt.Println(res.Session)
		return nil

	case "ls", "list-sessions":
		c := newFlags("ls")
		c.parse(argv[1:])
		sessions, err := listSessionInfo()
		if err != nil {
			return err
		}
		if c.jsonOut {
			if sessions == nil {
				sessions = []SessionInfo{}
			}
			b, _ := json.Marshal(sessions)
			fmt.Println(string(b))
			return nil
		}
		if len(sessions) == 0 {
			fmt.Println("no sessions")
			return nil
		}
		for _, s := range sessions {
			state := "dead"
			if s.Live {
				state = "live"
			}
			fmt.Println(s.Name + "\t" + state)
		}
		return nil

	case "kill":
		c := newFlags("kill")
		c.parse(argv[1:])
		name := c.session
		if len(c.args) > 0 {
			name = c.args[0]
		}
		return killSession(name)

	case "purge":
		c := newFlags("purge")
		c.parse(argv[1:])
		return runPurge(c)

	case "man":
		fs := flag.NewFlagSet("man", flag.ExitOnError)
		out := fs.String("o", "", "write the page to this path")
		_ = fs.Parse(argv[1:])
		page := manPage()
		if *out == "" {
			fmt.Print(page)
			return nil
		}
		return os.WriteFile(*out, []byte(page), 0o644)

	case "llms":
		fs := flag.NewFlagSet("llms", flag.ExitOnError)
		out := fs.String("o", "", "write the document to this path")
		_ = fs.Parse(argv[1:])
		doc := llmsText()
		if *out == "" {
			fmt.Print(doc)
			return nil
		}
		return os.WriteFile(*out, []byte(doc), 0o644)

	case "exec":
		c := newFlags("exec")
		c.parse(argv[1:])
		raw := c.msgJSON
		if raw == "" && len(c.args) > 0 {
			raw = c.args[0]
		}
		if raw == "" {
			return fmt.Errorf("exec needs a JSON action")
		}
		var req Msg
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			return fmt.Errorf("bad action JSON: %w", err)
		}
		return printRPC(c.session, req)

	case "write-to", "write":
		c := newFlags("write-to")
		c.parse(argv[1:])
		if len(c.args) < 1 {
			return fmt.Errorf("write-to needs a pane id")
		}
		return printRPC(c.session, Msg{Action: "write-to", Target: c.args[0], Msg: c.msg, Enter: c.enter})

	case "read":
		c := newFlags("read")
		c.parse(argv[1:])
		if len(c.args) < 1 {
			return fmt.Errorf("read needs a pane id")
		}
		action := "read"
		if c.scrollback >= 0 {
			action = "history"
		}
		m, err := rpc(c.session, Msg{Action: action, Target: c.args[0], Count: c.scrollback})
		if err != nil {
			return err
		}
		if !m.OK {
			return fmt.Errorf("%s", m.Error)
		}
		var res struct {
			Lines  []string `json:"lines"`
			Status Status   `json:"status"`
		}
		_ = json.Unmarshal(m.Res, &res)
		lines := res.Lines
		if c.lines > 0 && len(lines) > c.lines {
			lines = lines[len(lines)-c.lines:]
		}
		fmt.Println(strings.Join(lines, "\n"))
		return nil

	case "split":
		c := newFlags("split")
		c.parse(argv[1:])
		if len(c.args) < 1 {
			return fmt.Errorf("split needs a pane id")
		}
		if c.jsonOut {
			return printRPC(c.session, Msg{Action: "split", Target: c.args[0], Dir: c.dir})
		}
		m, err := rpc(c.session, Msg{Action: "split", Target: c.args[0], Dir: c.dir})
		if err != nil {
			return err
		}
		if !m.OK {
			return fmt.Errorf("%s", m.Error)
		}
		var res struct {
			Pane string `json:"pane"`
		}
		_ = json.Unmarshal(m.Res, &res)
		fmt.Println(res.Pane)
		return nil

	case "focus":
		c := newFlags("focus")
		c.parse(argv[1:])
		if len(c.args) < 1 {
			return fmt.Errorf("focus needs a pane id")
		}
		return printRPC(c.session, Msg{Action: "focus", Target: c.args[0]})

	case "rename-workspace":
		c := newFlags("rename-workspace")
		c.parse(argv[1:])
		if len(c.args) < 2 {
			return fmt.Errorf("rename-workspace needs a workspace id and a name")
		}
		return printRPC(c.session, Msg{Action: "rename-workspace", Target: c.args[0], Name: c.args[1]})

	case "next-pane", "prev-pane", "new-window", "next-window", "prev-window":
		c := newFlags(argv[0])
		c.parse(argv[1:])
		return printRPC(c.session, Msg{Action: argv[0]})

	case "status":
		c := newFlags("status")
		c.parse(argv[1:])
		target := ""
		if len(c.args) > 0 {
			target = c.args[0]
		}
		if target == "" {
			m, err := rpc(c.session, Msg{Action: "status"})
			if err != nil {
				return err
			}
			if !m.OK {
				return fmt.Errorf("%s", m.Error)
			}
			var st map[string]Status
			_ = json.Unmarshal(m.Res, &st)
			ids := make([]string, 0, len(st))
			for id := range st {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				s := st[id]
				fmt.Printf("%s\t%s\t%s\n", id, s.State, s.Message)
			}
			return nil
		}
		m, err := rpc(c.session, Msg{Action: "status", Target: target})
		if err != nil {
			return err
		}
		if !m.OK {
			return fmt.Errorf("%s", m.Error)
		}
		var st Status
		_ = json.Unmarshal(m.Res, &st)
		fmt.Printf("%s\t%s\t%s\n", target, st.State, st.Message)
		return nil

	case "dash":
		c := newFlags("dash")
		c.parse(argv[1:])
		return dash(c.session)

	case "peek":
		c := newFlags("peek")
		c.parse(argv[1:])
		if len(c.args) < 1 {
			return fmt.Errorf("peek needs a target")
		}
		return peek(c.session, c.args[0], c.lines)

	case "msg":
		c := newFlags("msg")
		c.parse(argv[1:])
		return runBox(c)

	case "list":
		c := newFlags("list")
		c.parse(argv[1:])
		m, err := rpc(c.session, Msg{Action: "list"})
		if err != nil {
			return err
		}
		if !m.OK {
			return fmt.Errorf("%s", m.Error)
		}
		if c.jsonOut {
			fmt.Println(string(m.Res))
		} else {
			var pretty any
			_ = json.Unmarshal(m.Res, &pretty)
			b, _ := json.MarshalIndent(pretty, "", "  ")
			fmt.Println(string(b))
		}
		return nil

	default:
		// A bare word is a session name; leading flags are attach flags.
		c := newFlags("attach")
		c.parse(argv)
		name := c.session
		if len(c.args) > 0 {
			name = c.args[0]
		}
		return runClient(name)
	}
}

func printRPC(name string, req Msg) error {
	m, err := rpc(name, req)
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	if len(m.Res) > 0 {
		var pretty any
		_ = json.Unmarshal(m.Res, &pretty)
		b, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(b))
	}
	return nil
}

// runBox dispatches the msg verbs. Each one is a thin wrapper on one box
// action: the shapes come from box.go, so the CLI carries no wire of its own.
func runBox(c *cmdFlags) error {
	if len(c.args) == 0 {
		return fmt.Errorf("msg needs a verb: post, list, wait, sub, clear, watchers or schema")
	}
	switch c.args[0] {
	case "post":
		return boxPostVerb(c)
	case "list":
		return boxListVerb(c)
	case "wait":
		return boxWaitVerb(c)
	case "sub":
		return boxSubVerb(c)
	case "clear":
		return boxClearVerb(c)
	case "watchers":
		return boxWatchersVerb(c)
	case "schema":
		return boxSchemaVerb(c)
	}
	return fmt.Errorf("msg: unknown verb %q: want post, list, wait, sub, clear, watchers or schema", c.args[0])
}

// boxPostVerb posts one entry and prints it as the server stored it.
func boxPostVerb(c *cmdFlags) error {
	st, err := parseState(c.state)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(postReq{Key: c.key, State: st, Msg: c.msg, From: boxFrom(c.session)})
	if err != nil {
		return err
	}
	m, err := rpc(c.session, Msg{Action: "msg-post", Res: raw})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	var rep postReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	boxPrint([]entry{rep.Entry}, 0)
	return nil
}

// boxListVerb prints the box newest first, or the reply unchanged on --json.
// With --after the rows are what the cursor has not seen, and a dropped gap is
// reported on stderr.
func boxListVerb(c *cmdFlags) error {
	raw, err := json.Marshal(listReq{
		Key: c.key, KeyPrefix: c.keyPrefix,
		Limit: c.limit, AfterSeq: c.boxCursor(), From: boxFrom(c.session),
	})
	if err != nil {
		return err
	}
	m, err := rpc(c.session, Msg{Action: "msg-list", Res: raw})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	if c.jsonOut {
		fmt.Println(string(m.Res))
		return nil
	}
	var rep listReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	boxPrint(rep.Entries, boxTermWidth())
	if rep.Trimmed {
		fmt.Fprintln(os.Stderr, "dm: entries between the cursor and the oldest kept row are gone")
	}
	return nil
}

// boxWaitVerb blocks until the read finds an entry and prints it. Without
// --after it waits for the topic's newest entry to carry --state, which is the
// status board. With --after it returns the oldest entry past the cursor, so a
// subscriber sees every message in order. The reply arrives only when the wait
// ends, so it needs a deadline of its own: the one rpc sets is ten seconds,
// and a wait may run for an hour.
func boxWaitVerb(c *cmdFlags) error {
	var st boxState
	if c.state != "" {
		parsed, err := parseState(c.state)
		if err != nil {
			return err
		}
		st = parsed
	}
	raw, err := json.Marshal(waitReq{
		Key: c.key, KeyPrefix: c.keyPrefix,
		State: st, TimeoutMs: int(c.timeout / time.Millisecond),
		AfterSeq: c.boxCursor(), From: boxFrom(c.session),
	})
	if err != nil {
		return err
	}
	m, err := rpcWithin(c.session, Msg{Action: "msg-wait", Res: raw}, c.timeout+time.Minute)
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	if c.jsonOut {
		fmt.Println(string(m.Res))
		return nil
	}
	var rep waitReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	boxPrint([]entry{rep.Entry}, 0)
	if rep.Trimmed {
		fmt.Fprintln(os.Stderr, "dm: entries between the cursor and the oldest kept row are gone")
	}
	return nil
}

// The redial delays of a subscription whose connection went: 100 ms, doubling
// to a 2 s cap.
const (
	boxSubMin = 100 * time.Millisecond
	boxSubMax = 2 * time.Second
)

// errBoxSubEnded reports a subscription connection that went away. The verb
// answers it with a redial, not an exit, so a server restart is invisible.
var errBoxSubEnded = errors.New("box: sub connection ended")

// boxSubVerb streams entries until the process is killed. Each entry prints as
// one row, or as its own JSON object on one line under --json, so a reader can
// parse NDJSON. The cursor follows every printed seq; when the connection ends
// the verb redials with backoff and resends that cursor, so no entry is lost
// across a restart. A resp with an Error is not retried: it prints and exits
// nonzero, because a refused request will be refused again.
func boxSubVerb(c *cmdFlags) error {
	var st boxState
	if c.state != "" {
		parsed, err := parseState(c.state)
		if err != nil {
			return err
		}
		st = parsed
	}
	// From travels the way list and wait send it, so a sub inside a pane of
	// the target session is a watcher of the filter it names.
	req := subReq{
		From: boxFrom(c.session), Key: c.key, KeyPrefix: c.keyPrefix,
		State: st, AfterSeq: c.boxCursor(),
	}
	backoff := boxSubMin
	for {
		live, err := boxSubStream(c.session, &req, c.jsonOut)
		if !errors.Is(err, errBoxSubEnded) {
			return err
		}
		if live {
			backoff = boxSubMin
		}
		time.Sleep(backoff)
		backoff = min(2*backoff, boxSubMax)
	}
}

// boxSubStream holds one subscription open: it dials, sends the one request and
// reads frames until the connection ends. It reports whether the connection was
// established, so a caller can tell a refusal to dial from a stream that ran
// and then went. A frame advances the request's cursor as it is printed, so the
// same request resumes the stream after a redial.
func boxSubStream(session string, req *subReq, jsonOut bool) (bool, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return false, err
	}
	c, err := dialServer(session)
	if err != nil {
		return false, errBoxSubEnded
	}
	defer c.Close()
	if err := c.send(Msg{Type: "req", Action: "msg-sub", ID: 1, Res: raw}); err != nil {
		return true, errBoxSubEnded
	}
	for {
		m, err := readMsg(c.Conn)
		if err != nil {
			return true, errBoxSubEnded
		}
		// A stream has no completion reply: a resp is either the refusal that
		// ends the process or, if it somehow carries OK, nothing to print.
		if m.Type == "resp" {
			if !m.OK {
				return true, fmt.Errorf("%s", m.Error)
			}
			continue
		}
		var frame subFrame
		if err := json.Unmarshal(m.Res, &frame); err != nil {
			return true, errBoxSubEnded
		}
		e := frame.Entry
		if jsonOut {
			b, err := json.Marshal(e)
			if err != nil {
				return true, err
			}
			fmt.Println(string(b))
		} else {
			boxPrint([]entry{e}, 0)
		}
		seq := e.Seq
		req.AfterSeq = &seq
		// The server sets trimmed on the first frame of a stream whose cursor
		// sat behind the oldest kept row and clears it after, so this is one
		// note per gap, not one per entry.
		if frame.Trimmed {
			fmt.Fprintln(os.Stderr, "dm: entries between the cursor and the oldest kept row are gone")
		}
	}
}

// boxCursor turns the -after flag into the request cursor. A negative flag is
// absent: the request carries no afterSeq, so the read is the status board.
func (c *cmdFlags) boxCursor() *int {
	if c.after < 0 {
		return nil
	}
	n := c.after
	return &n
}

// boxClearVerb clears one key, or the whole box when no key is given, and
// prints how many entries went.
func boxClearVerb(c *cmdFlags) error {
	raw, err := json.Marshal(clearReq{Key: c.key})
	if err != nil {
		return err
	}
	m, err := rpc(c.session, Msg{Action: "msg-clear", Res: raw})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	var rep clearReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	fmt.Println(rep.Cleared)
	return nil
}

// boxWatchersVerb prints the live watches as PANE FILTER CURSOR WAIT. FILTER is
// the key, or the branch prefix with a trailing star. WAIT is "wait" while a
// msg-wait is parked on the filter, and "-" otherwise.
func boxWatchersVerb(c *cmdFlags) error {
	m, err := rpc(c.session, Msg{Action: "msg-watchers"})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	if c.jsonOut {
		fmt.Println(string(m.Res))
		return nil
	}
	var rep watchersReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	boxPrintWatchers(rep.Watchers)
	return nil
}

// boxPrintWatchers writes one row per watch, each column padded to the widest
// row. The filter is what the reader asked for: a key, or a prefix and a star.
func boxPrintWatchers(ws []watch) {
	if len(ws) == 0 {
		return
	}
	paneW, filterW, cursorW := 0, 0, 1
	filters := make([]string, len(ws))
	for i, w := range ws {
		filters[i] = boxFilter(w)
		paneW = max(paneW, cells.Cells(w.Pane))
		filterW = max(filterW, cells.Cells(filters[i]))
		cursorW = max(cursorW, len(strconv.Itoa(w.Cursor)))
	}
	for i, w := range ws {
		wait := "-"
		if w.Parked {
			wait = "wait"
		}
		line := fmt.Sprintf("%s %s %*d %s", cells.Pad(w.Pane, paneW),
			cells.Pad(filters[i], filterW), cursorW, w.Cursor, wait)
		fmt.Println(strings.TrimRight(line, " "))
	}
}

// boxFilter is one watch's filter as a reader names it.
func boxFilter(w watch) string {
	if w.KeyPrefix != "" {
		return w.KeyPrefix + "*"
	}
	return w.Key
}

// boxSchemaVerb prints the schema the running server serves, so the CLI holds
// no copy of the wire.
func boxSchemaVerb(c *cmdFlags) error {
	m, err := rpc(c.session, Msg{Action: "msg-schema"})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	var rep schemaReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep.Schema, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// rpcWithin is rpc with a read deadline the caller sets, because a blocking
// wait replies long after the ten seconds rpc allows.
func rpcWithin(name string, req Msg, wait time.Duration) (Msg, error) {
	c, err := ensureServer(name)
	if err != nil {
		return Msg{}, err
	}
	defer c.Close()
	req.Type = "req"
	req.ID = 1
	if err := writeMsg(c, req); err != nil {
		return Msg{}, err
	}
	_ = c.SetReadDeadline(time.Now().Add(wait))
	for {
		got, err := readMsg(c)
		if err != nil {
			return Msg{}, err
		}
		if got.Type == "resp" {
			return got, nil
		}
	}
}

// boxFrom is the sender's pane id. A pane exports DM_PANE and DM_SESSION; from
// is only meaningful when that session is the one being written to, so a shell
// elsewhere posts with an empty from.
func boxFrom(session string) string {
	if os.Getenv("DM_SESSION") != session {
		return ""
	}
	return os.Getenv("DM_PANE")
}

// boxTermWidth is the columns a list row may use: the terminal, else $COLUMNS,
// else zero, which means no limit.
func boxTermWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	return 0
}

// boxPrint writes entries one per row as SEQ KEY STATE FROM MSG. A width above
// zero cuts the message to fit, with an cells.Ellipsis where it was cut.
func boxPrint(entries []entry, width int) {
	if len(entries) == 0 {
		return
	}
	seqW, keyW, stateW, fromW := 1, 0, 0, 0
	for _, e := range entries {
		seqW = max(seqW, len(strconv.Itoa(e.Seq)))
		keyW = max(keyW, cells.Cells(e.Key))
		stateW = max(stateW, cells.Cells(string(e.State)))
		fromW = max(fromW, cells.Cells(e.From))
	}
	msgW := -1 // no limit
	if width > 0 {
		msgW = max(width-(seqW+keyW+stateW+fromW+4), 0)
	}
	for _, e := range entries {
		msg := e.Msg
		if msgW >= 0 {
			msg = cells.Elide(msg, msgW)
		}
		line := fmt.Sprintf("%*d %s %s %s %s", seqW, e.Seq,
			cells.Pad(e.Key, keyW), cells.Pad(string(e.State), stateW), cells.Pad(e.From, fromW), msg)
		fmt.Println(strings.TrimRight(line, " "))
	}
}

// runPurge removes dead sessions whose server and panes are gone. A session
// that still answers on its socket, or whose server or a pane process is
// alive, is left alone and reported.
func runPurge(c *cmdFlags) error {
	sessions, err := listSessionInfo()
	if err != nil {
		return err
	}
	purged := 0
	for _, s := range sessions {
		if s.Live {
			continue
		}
		dir := paths.SessionDir(s.Name)
		if pidAlive(filepath.Join(dir, "server.pid")) {
			fmt.Printf("skip %s: server pid alive\n", s.Name)
			continue
		}
		if pidAliveAny(filepath.Join(dir, "panes")) {
			fmt.Printf("skip %s: live panes\n", s.Name)
			continue
		}
		if c.dry {
			fmt.Printf("would purge %s\n", s.Name)
			purged++
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			fmt.Printf("error purging %s: %v\n", s.Name, err)
			continue
		}
		fmt.Printf("purged %s\n", s.Name)
		purged++
	}
	if purged == 0 {
		fmt.Println("nothing to purge")
	}
	return nil
}

// pidAlive reports whether a pid file names a running process. A missing or
// malformed file is not alive.
func pidAlive(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// pidAliveAny reports whether any pane pid file under dir names a running
// process.
func pidAliveAny(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sock.pid") && pidAlive(filepath.Join(dir, e.Name())) {
			return true
		}
	}
	return false
}
