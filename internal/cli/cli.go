package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"dm/internal/box"
	"dm/internal/cells"
	"dm/internal/clidoc"
	"dm/internal/client"
	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/transport"
	"dm/internal/wire"

	"golang.org/x/term"
)

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
	body       string
	bodyFile   string
	seq        int
	from       string
	limit      int
	after      int
	timeout    time.Duration
	dry        bool
	all        bool
	force      bool
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
	c.fs.StringVar(&c.body, "body", "", "long body text under the message line")
	c.fs.StringVar(&c.bodyFile, "body-file", "", "read the body from a path, - for stdin")
	c.fs.IntVar(&c.seq, "seq", -1, "the entry's seq")
	c.fs.StringVar(&c.from, "from", "", "the posting pane")
	c.fs.IntVar(&c.limit, "limit", 0, "list rows, 0 for the whole box")
	c.fs.IntVar(&c.after, "after", -1, "box seq cursor: entries after this one")
	c.fs.DurationVar(&c.timeout, "timeout", 5*time.Minute, "wait timeout")
	c.fs.BoolVar(&c.dry, "dry-run", false, "print what would be removed")
	c.fs.BoolVar(&c.all, "all", false, "reset every session")
	c.fs.BoolVar(&c.force, "force", false, "confirm the discard")
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
		"-body": true, "--body": true, "-body-file": true, "--body-file": true,
		"-seq": true, "--seq": true, "-from": true, "--from": true,
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

func Run(argv []string) error {
	if len(argv) == 0 {
		return client.Run("main")
	}
	switch argv[0] {
	case "attach", "a":
		c := newFlags("attach")
		c.parse(argv[1:])
		name := c.session
		if len(c.args) > 0 {
			name = c.args[0]
		}
		return client.Run(name)

	case "new":
		c := newFlags("new")
		c.parse(argv[1:])
		m, err := transport.RPC(c.session, wire.Msg{Action: "ping"})
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
		return runLs(c)

	case "kill":
		c := newFlags("kill")
		c.parse(argv[1:])
		name := c.session
		if len(c.args) > 0 {
			name = c.args[0]
		}
		return transport.KillSession(name)

	case "reset":
		c := newFlags("reset")
		c.parse(argv[1:])
		return runReset(c)

	case "purge":
		c := newFlags("purge")
		c.parse(argv[1:])
		return runPurge(c)

	case "man":
		fs := flag.NewFlagSet("man", flag.ExitOnError)
		out := fs.String("o", "", "write the page to this path")
		_ = fs.Parse(argv[1:])
		page := clidoc.ManPage()
		if *out == "" {
			fmt.Print(page)
			return nil
		}
		return os.WriteFile(*out, []byte(page), 0o644)

	case "llms":
		fs := flag.NewFlagSet("llms", flag.ExitOnError)
		out := fs.String("o", "", "write the document to this path")
		_ = fs.Parse(argv[1:])
		doc := clidoc.LLMsText()
		if *out == "" {
			fmt.Print(doc)
			return nil
		}
		return os.WriteFile(*out, []byte(doc), 0o644)

	case "html":
		fs := flag.NewFlagSet("html", flag.ExitOnError)
		out := fs.String("o", "", "write the page to this path, or - for stdout")
		_ = fs.Parse(argv[1:])
		return runHTML(*out)

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
		var req wire.Msg
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
		return printRPC(c.session, wire.Msg{Action: "write-to", Target: c.args[0], Msg: c.msg, Enter: c.enter})

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
		m, err := transport.RPC(c.session, wire.Msg{Action: action, Target: c.args[0], Count: c.scrollback})
		if err != nil {
			return err
		}
		if !m.OK {
			return fmt.Errorf("%s", m.Error)
		}
		var res struct {
			Lines  []string    `json:"lines"`
			Status wire.Status `json:"status"`
		}
		_ = json.Unmarshal(m.Res, &res)
		lines := res.Lines
		if c.lines > 0 && len(lines) > c.lines {
			lines = lines[len(lines)-c.lines:]
		}
		fmt.Println(strings.Join(lines, "\n"))
		return nil

	case "yank":
		c := newFlags("yank")
		c.parse(argv[1:])
		if len(c.args) < 1 {
			return fmt.Errorf("yank needs a pane id")
		}
		lines, err := yankLines(c.session, c.args[0])
		if err != nil {
			return err
		}
		if c.lines > 0 && len(lines) > c.lines {
			lines = lines[len(lines)-c.lines:]
		}
		fmt.Println(strings.Join(lines, "\n"))
		return nil

	case "wait":
		c := newFlags("wait")
		c.parse(argv[1:])
		code, err := runWait(c)
		if err != nil {
			return err
		}
		// The CLI's own exit code mirrors the command's. The nonzero code
		// leaves through os.Exit, so main keeps its one error exit for a
		// failed wait, not a command that reported a code.
		if code != 0 {
			os.Exit(code)
		}
		return nil

	case "split":
		c := newFlags("split")
		c.parse(argv[1:])
		if len(c.args) < 1 {
			return fmt.Errorf("split needs a pane id")
		}
		if c.jsonOut {
			return printRPC(c.session, wire.Msg{Action: "split", Target: c.args[0], Dir: c.dir})
		}
		m, err := transport.RPC(c.session, wire.Msg{Action: "split", Target: c.args[0], Dir: c.dir})
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
		return printRPC(c.session, wire.Msg{Action: "focus", Target: c.args[0]})

	case "rename-workspace":
		c := newFlags("rename-workspace")
		c.parse(argv[1:])
		if len(c.args) < 2 {
			return fmt.Errorf("rename-workspace needs a workspace id and a name")
		}
		return printRPC(c.session, wire.Msg{Action: "rename-workspace", Target: c.args[0], Name: c.args[1]})

	case "set-project":
		c := newFlags("set-project")
		c.parse(argv[1:])
		if len(c.args) < 1 {
			return fmt.Errorf("set-project needs a workspace id and a project name")
		}
		name := ""
		if len(c.args) > 1 {
			name = c.args[1]
		}
		return printRPC(c.session, wire.Msg{Action: "set-project", Target: c.args[0], Name: name})

	case "next-pane", "prev-pane", "new-window", "next-window", "prev-window":
		c := newFlags(argv[0])
		c.parse(argv[1:])
		return printRPC(c.session, wire.Msg{Action: argv[0]})

	case "status":
		c := newFlags("status")
		c.parse(argv[1:])
		target := ""
		if len(c.args) > 0 {
			target = c.args[0]
		}
		if target == "" {
			m, err := transport.RPC(c.session, wire.Msg{Action: "status"})
			if err != nil {
				return err
			}
			if !m.OK {
				return fmt.Errorf("%s", m.Error)
			}
			var st map[string]wire.Status
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
		m, err := transport.RPC(c.session, wire.Msg{Action: "status", Target: target})
		if err != nil {
			return err
		}
		if !m.OK {
			return fmt.Errorf("%s", m.Error)
		}
		var st wire.Status
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
		return runList(c)

	default:
		// A bare word is a session name; leading flags are attach flags.
		c := newFlags("attach")
		c.parse(argv)
		name := c.session
		if len(c.args) > 0 {
			name = c.args[0]
		}
		return client.Run(name)
	}
}

func printRPC(name string, req wire.Msg) error {
	m, err := transport.RPC(name, req)
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
	case "show":
		return boxShowVerb(c)
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

// boxBody resolves the body the post carries: --body wins, --body-file reads
// a path or stdin, and naming both is ambiguous.
func boxBody(c *cmdFlags) (string, error) {
	if c.body != "" && c.bodyFile != "" {
		return "", fmt.Errorf("msg: --body and --body-file are exclusive")
	}
	if c.bodyFile == "" {
		return c.body, nil
	}
	if c.bodyFile == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("msg: read body: %w", err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(c.bodyFile)
	if err != nil {
		return "", fmt.Errorf("msg: read body: %w", err)
	}
	return string(b), nil
}

// boxPostVerb posts one entry and prints it as the server stored it.
func boxPostVerb(c *cmdFlags) error {
	st, err := box.ParseState(c.state)
	if err != nil {
		return err
	}
	body, err := boxBody(c)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(box.PostReq{Key: c.key, State: st, Msg: c.msg, Body: body, From: boxFrom(c.session)})
	if err != nil {
		return err
	}
	m, err := transport.RPC(c.session, wire.Msg{Action: "msg-post", Res: raw})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	var rep box.PostReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	boxPrint([]box.Entry{rep.Entry}, 0)
	return nil
}

// boxListVerb prints the box newest first, or the reply unchanged on --json.
// With --after the rows are what the cursor has not seen, and a dropped gap is
// reported on stderr.
func boxListVerb(c *cmdFlags) error {
	raw, err := json.Marshal(box.ListReq{
		Key: c.key, KeyPrefix: c.keyPrefix,
		Limit: c.limit, AfterSeq: c.boxCursor(), From: boxFrom(c.session),
	})
	if err != nil {
		return err
	}
	m, err := transport.RPC(c.session, wire.Msg{Action: "msg-list", Res: raw})
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
	var rep box.ListReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	boxPrint(rep.Entries, boxTermWidth())
	if rep.Trimmed {
		fmt.Fprintln(os.Stderr, "dm: entries between the cursor and the oldest kept row are gone")
	}
	return nil
}

// boxShowVerb prints one entry's body to stdout, raw, or the entry's summary
// line when it carries no body. The pick is deterministic: the topic's newest
// entry, or the one --seq names, or the newest one --from posted. Naming both
// a seq and a pane is ambiguous, so it is refused.
func boxShowVerb(c *cmdFlags) error {
	if c.key == "" {
		return fmt.Errorf("msg show needs --key")
	}
	if c.seq >= 0 && c.from != "" {
		return fmt.Errorf("msg: --seq and --from are exclusive")
	}
	// The whole topic, always: a show picks one entry, so an explicit
	// --limit must not be able to hide the row --seq or --from is after.
	raw, err := json.Marshal(box.ListReq{Key: c.key})
	if err != nil {
		return err
	}
	m, err := transport.RPC(c.session, wire.Msg{Action: "msg-list", Res: raw})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	var rep box.ListReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	e, found := pickShowEntry(rep.Entries, c.seq, c.from)
	if !found {
		switch {
		case c.seq >= 0:
			return fmt.Errorf("box: key %q has no entry at seq %d", c.key, c.seq)
		case c.from != "":
			return fmt.Errorf("box: key %q has no entry from %q", c.key, c.from)
		default:
			return fmt.Errorf("box: key %q has no entry", c.key)
		}
	}
	text := showText(e)
	fmt.Print(text)
	if !strings.HasSuffix(text, "\n") {
		fmt.Println()
	}
	return nil
}

// showText is the text a show prints: the body raw, or the summary line when
// the entry carries no body.
func showText(e box.Entry) string {
	if e.Body != "" {
		return e.Body
	}
	return e.Msg
}

// pickShowEntry chooses the entry a show prints: the seq's row, the newest row
// the pane posted, or the topic's newest row. Entries arrive newest first.
func pickShowEntry(entries []box.Entry, seq int, from string) (box.Entry, bool) {
	for _, e := range entries {
		if seq >= 0 && e.Seq != seq {
			continue
		}
		if from != "" && e.From != from {
			continue
		}
		return e, true
	}
	return box.Entry{}, false
}

// boxWaitVerb blocks until the read finds an entry and prints it. Without
// --after it waits for the topic's newest entry to carry --state, which is the
// status board. With --after it returns the oldest entry past the cursor, so a
// subscriber sees every message in order. The reply arrives only when the wait
// ends, so it needs a deadline of its own: the one rpc sets is ten seconds,
// and a wait may run for an hour.
func boxWaitVerb(c *cmdFlags) error {
	var st box.State
	if c.state != "" {
		parsed, err := box.ParseState(c.state)
		if err != nil {
			return err
		}
		st = parsed
	}
	raw, err := json.Marshal(box.WaitReq{
		Key: c.key, KeyPrefix: c.keyPrefix,
		State: st, TimeoutMs: int(c.timeout / time.Millisecond),
		AfterSeq: c.boxCursor(), From: boxFrom(c.session),
	})
	if err != nil {
		return err
	}
	m, err := rpcWithin(c.session, wire.Msg{Action: "msg-wait", Res: raw}, c.timeout+time.Minute)
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
	var rep box.WaitReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	boxPrint([]box.Entry{rep.Entry}, 0)
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
	var st box.State
	if c.state != "" {
		parsed, err := box.ParseState(c.state)
		if err != nil {
			return err
		}
		st = parsed
	}
	// From travels the way list and wait send it, so a sub inside a pane of
	// the target session is a watcher of the filter it names.
	req := box.SubReq{
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
func boxSubStream(session string, req *box.SubReq, jsonOut bool) (bool, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return false, err
	}
	c, err := transport.Dial(session)
	if err != nil {
		return false, errBoxSubEnded
	}
	defer func() { _ = c.Close() }()
	if err := c.Send(wire.Msg{Type: "req", Action: "msg-sub", ID: 1, Res: raw}); err != nil {
		return true, errBoxSubEnded
	}
	for {
		m, err := wire.ReadMsg(c.Conn)
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
		var frame box.SubFrame
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
			boxPrint([]box.Entry{e}, 0)
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

// boxClearVerb clears one key, or every key under a prefix, or the whole box
// when neither is given, and prints how many entries went.
func boxClearVerb(c *cmdFlags) error {
	if c.key != "" && c.keyPrefix != "" {
		return fmt.Errorf("box: key and keyPrefix are exclusive")
	}
	raw, err := json.Marshal(box.ClearReq{Key: c.key, KeyPrefix: c.keyPrefix})
	if err != nil {
		return err
	}
	m, err := transport.RPC(c.session, wire.Msg{Action: "msg-clear", Res: raw})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	var rep box.ClearReply
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
	m, err := transport.RPC(c.session, wire.Msg{Action: "msg-watchers"})
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
	var rep box.WatchersReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	boxPrintWatchers(rep.Watchers)
	return nil
}

// boxPrintWatchers writes one row per watch, each column padded to the widest
// row. The filter is what the reader asked for: a key, or a prefix and a star.
func boxPrintWatchers(ws []box.Watch) {
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
func boxFilter(w box.Watch) string {
	if w.KeyPrefix != "" {
		return w.KeyPrefix + "*"
	}
	return w.Key
}

// boxSchemaVerb prints the schema the running server serves, so the CLI holds
// no copy of the wire.
func boxSchemaVerb(c *cmdFlags) error {
	m, err := transport.RPC(c.session, wire.Msg{Action: "msg-schema"})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	var rep box.SchemaReply
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
func rpcWithin(name string, req wire.Msg, wait time.Duration) (wire.Msg, error) {
	c, err := transport.Ensure(name)
	if err != nil {
		return wire.Msg{}, err
	}
	defer func() { _ = c.Close() }()
	req.Type = "req"
	req.ID = 1
	if err := wire.WriteMsg(c, req); err != nil {
		return wire.Msg{}, err
	}
	_ = c.SetReadDeadline(time.Now().Add(wait))
	for {
		got, err := wire.ReadMsg(c)
		if err != nil {
			return wire.Msg{}, err
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
func boxPrint(entries []box.Entry, width int) {
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

// runReset discards sessions for a full restart: it stops each server, reaps
// every pane and its shell, and removes the session directory. Without
// --force it prints the plan and refuses, because the discard is final.
func runReset(c *cmdFlags) error {
	names := []string{c.session}
	if c.all {
		sessions, err := transport.ListSessions()
		if err != nil {
			return err
		}
		names = names[:0]
		for _, s := range sessions {
			names = append(names, s.Name)
		}
	}
	if len(names) == 0 {
		fmt.Println("nothing to reset")
		return nil
	}
	if !c.force {
		for _, name := range names {
			fmt.Printf("would reset %s\n", name)
		}
		return fmt.Errorf("reset discards the session and its panes; pass --force")
	}
	failed := 0
	for _, name := range names {
		if err := transport.ResetSession(name); err != nil {
			fmt.Printf("error resetting %s: %v\n", name, err)
			failed++
			continue
		}
		fmt.Printf("reset %s\n", name)
	}
	if failed > 0 {
		return fmt.Errorf("%d session(s) not reset", failed)
	}
	return nil
}

// runPurge removes dead sessions whose server and panes are gone. A session
// that still answers on its socket, or whose server or a pane process is
// alive, is left alone and reported.
func runPurge(c *cmdFlags) error {
	sessions, err := transport.ListSessions()
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

// snapshotOf asks a live session for its pane tree.
func snapshotOf(session string) (*wire.Snapshot, error) {
	m, err := transport.RPC(session, wire.Msg{Action: "list"})
	if err != nil {
		return nil, err
	}
	if !m.OK {
		return nil, fmt.Errorf("%s", m.Error)
	}
	var res struct {
		Snapshot *wire.Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		return nil, err
	}
	if res.Snapshot == nil {
		return nil, fmt.Errorf("no snapshot")
	}
	return res.Snapshot, nil
}

func dashRows(session string) ([]string, error) {
	snap, err := snapshotOf(session)
	if err != nil {
		return nil, err
	}
	var rows []string
	for _, p := range snap.All {
		mark := " "
		if p.Focus {
			mark = ">"
		}
		rows = append(rows, p.ID+" "+mark+" "+paneLabel(p))
	}
	return rows, nil
}

// runList prints the pane tree. Without --json it is a table, one row per
// pane, so the ids and their meaning are visible at a glance.
func runList(c *cmdFlags) error {
	m, err := transport.RPC(c.session, wire.Msg{Action: "list"})
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
	var res struct {
		Snapshot *wire.Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		return err
	}
	if res.Snapshot == nil {
		return fmt.Errorf("no snapshot")
	}
	printPaneTable(res.Snapshot.All)
	return nil
}

// printPaneTable prints one row per pane: its id, workspace, window, app,
// status, cwd and branch. A star marks the focused pane.
func printPaneTable(panes []wire.AllPane) {
	if len(panes) == 0 {
		fmt.Println("no panes")
		return
	}
	rows := make([][]string, 0, len(panes))
	for _, p := range panes {
		id := p.ID
		if p.Focus {
			id += "*"
		}
		app := p.App
		if app == "" {
			if p.Agent {
				app = "agent"
			} else {
				app = "shell"
			}
		}
		status := p.Status
		if status == "" || status == "unknown" {
			status = "-"
		}
		rows = append(rows, []string{id, p.Workspace, p.Window, app, status, dashIfEmpty(p.Cwd), dashIfEmpty(p.Branch)})
	}
	printTable([]string{"PANE", "WS", "WINDOW", "APP", "STATUS", "CWD", "BRANCH"}, rows)
}

// runLs prints one row per session: its state, counts, project and the
// focused pane's cwd. A dead session is read from its state file, so ls never
// starts a server.
func runLs(c *cmdFlags) error {
	sessions, err := transport.ListSessions()
	if err != nil {
		return err
	}
	if c.jsonOut {
		if sessions == nil {
			sessions = []transport.SessionInfo{}
		}
		b, _ := json.Marshal(sessions)
		fmt.Println(string(b))
		return nil
	}
	if len(sessions) == 0 {
		fmt.Println("no sessions")
		return nil
	}
	rows := make([][]string, 0, len(sessions))
	for _, s := range sessions {
		rows = append(rows, sessionSummary(s))
	}
	printTable([]string{"NAME", "STATE", "PANES", "WINDOWS", "WORKSPACES", "PROJECT", "CWD"}, rows)
	return nil
}

// sessionSummary is one dm ls row. A live session answers with its snapshot;
// a dead one is read from state.json.
func sessionSummary(s transport.SessionInfo) []string {
	state := "dead"
	if s.Live {
		state = "live"
	}
	panes, windows, workspaces := 0, 0, 0
	project, cwd := "-", "-"
	if s.Live {
		if snap, err := snapshotOf(s.Name); err == nil {
			panes = len(snap.All)
			wsSet := map[string]bool{}
			winSet := map[string]bool{}
			for _, p := range snap.All {
				wsSet[p.Workspace] = true
				winSet[p.Window] = true
				if p.WorkspaceName == snap.Workspace && project == "-" {
					project = dashIfEmpty(p.Project)
				}
				if p.ID == snap.Focus {
					cwd = dashIfEmpty(p.Cwd)
				}
			}
			workspaces = len(wsSet)
			windows = len(winSet)
		}
	} else if st, err := session.LoadState(s.Name); err == nil && st != nil {
		workspaces = len(st.Workspaces)
		for _, ws := range st.Workspaces {
			windows += len(ws.Windows)
		}
		panes = len(st.Panes)
		if st.Active >= 0 && st.Active < len(st.Workspaces) {
			ws := st.Workspaces[st.Active]
			project = dashIfEmpty(ws.Project)
			if ws.Active >= 0 && ws.Active < len(ws.Windows) {
				w := ws.Windows[ws.Active]
				if ps := st.Panes[w.Focus]; ps != nil {
					cwd = dashIfEmpty(ps.Cwd)
				}
			}
		}
	}
	return []string{s.Name, state, strconv.Itoa(panes), strconv.Itoa(windows), strconv.Itoa(workspaces), project, cwd}
}

// printTable prints a header and rows with two spaces between columns. The
// last column is not padded, so no row carries trailing spaces.
func printTable(header []string, rows [][]string) {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = cells.Cells(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], cells.Cells(cell))
		}
	}
	printRow := func(row []string) {
		var b strings.Builder
		for i, cell := range row {
			if i > 0 {
				b.WriteString("  ")
			}
			if i == len(row)-1 {
				b.WriteString(cell)
				continue
			}
			b.WriteString(cells.Pad(cell, widths[i]))
		}
		fmt.Println(strings.TrimRight(b.String(), " "))
	}
	printRow(header)
	for _, row := range rows {
		printRow(row)
	}
}

// dashIfEmpty prints a dash for an empty cell, so a missing value is visible
// instead of a gap.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// paneLabel names a pane in a palette row: its name when it has one, then its
// live state, branch and worktree. The id already leads the row, so the label
// never repeats it, and the row ends with the workspace and window it lives
// in.
func paneLabel(p wire.AllPane) string {
	name := p.Name
	if name == "" {
		name = p.App
	}
	if name == "" {
		if p.Agent {
			name = p.ID
		} else {
			name = "(shell)"
		}
	}
	if p.Status != "" && p.Status != "unknown" {
		name += "  " + p.Status
	}
	if p.Message != "" {
		name += "  " + p.Message
	}
	if p.Branch != "" {
		name += "  " + p.Branch
	}
	if p.Worktree != "" {
		name += "  " + p.Worktree
	}
	if p.WorkspaceName != "" {
		name += "  " + p.WorkspaceName + " > " + p.WindowName
	}
	return name
}

// dash prints the rows a palette jump uses, one per line.
func dash(session string) error {
	rows, err := dashRows(session)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fmt.Println(r)
	}
	return nil
}

// yankLines returns the output a pane produced since the last Enter, the same
// rows prefix y copies. A pane that has never been sent an Enter has nothing
// to cut at, so it has no output to print.
func yankLines(session, target string) ([]string, error) {
	m, err := transport.RPC(session, wire.Msg{Action: "yank", Target: target})
	if err != nil {
		return nil, err
	}
	if !m.OK {
		return nil, fmt.Errorf("%s", m.Error)
	}
	var res struct {
		Lines  []string `json:"lines"`
		Start  int      `json:"start"`
		Rows   int      `json:"rows"`
		Anchor bool     `json:"anchor"`
	}
	if len(m.Res) > 0 {
		if err := json.Unmarshal(m.Res, &res); err != nil {
			return nil, err
		}
	}
	if !res.Anchor {
		return nil, fmt.Errorf("pane %s has no Enter yet", target)
	}
	if res.Start < 0 || res.Start > res.Rows || res.Rows > len(res.Lines) {
		return nil, fmt.Errorf("yank reply out of range")
	}
	lines := res.Lines[res.Start:res.Rows]
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

// waitResult is a wait's ending: the command's exit code when the pane could
// observe one, the reason the wait ended, and whether the quiet-idle timeout
// fired first.
type waitResult struct {
	Exit     *int   `json:"exit"`
	Reason   string `json:"reason"`
	TimedOut bool   `json:"timed_out"`
}

// runWait dials the pane socket directly, sends one wait request, writes the
// bytes frames to stdout as they arrive and returns the CLI exit code for the
// ending. The timeout is a quiet-idle deadline: every frame that arrives
// extends it, so a streaming command is not cut off mid-stream for exceeding
// the total, and only real silence ends it with 124.
func runWait(c *cmdFlags) (int, error) {
	if len(c.args) < 1 {
		return 0, fmt.Errorf("wait needs a pane id")
	}
	conn, err := net.DialTimeout("unix", paths.PaneSock(c.session, c.args[0]), time.Second)
	if err != nil {
		return 0, fmt.Errorf("pane %s: %v", c.args[0], err)
	}
	defer func() { _ = conn.Close() }()
	req := wire.Msg{Type: "req", Action: "wait", ID: 1}
	if err := wire.WriteMsg(conn, req); err != nil {
		return 0, fmt.Errorf("pane %s: %v", c.args[0], err)
	}
	extend := func() { _ = conn.SetReadDeadline(time.Now().Add(c.timeout)) }
	extend()
	tailed := false
	for {
		m, err := wire.ReadMsg(conn)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return waitExit(nil, true), waitReport(c, waitResult{Reason: "timeout", TimedOut: true})
			}
			return 0, fmt.Errorf("pane %s: %v", c.args[0], err)
		}
		if m.Type == "resp" {
			if !m.OK {
				return 0, fmt.Errorf("%s", m.Error)
			}
			if m.WaitReason != "" {
				// The final reply names how the wait ended.
				res := waitResult{Exit: m.Exit, Reason: m.WaitReason}
				return waitExit(m.Exit, false), waitReport(c, res)
			}
			// Every other resp is the yank-shaped tail the wait opens with:
			// the output between the Enter anchor and the wait's connect.
			// The pane also broadcasts that window as bytes frames, so
			// nothing prints from the stream until the tail lands; after it
			// the guarantee is strict ordering.
			var tail struct {
				Lines []string `json:"lines"`
				Start int      `json:"start"`
				Rows  int      `json:"rows"`
			}
			if err := json.Unmarshal(m.Res, &tail); err != nil {
				return 0, fmt.Errorf("pane %s: %v", c.args[0], err)
			}
			if err := printTail(tail.Lines, tail.Start, tail.Rows); err != nil {
				return 0, err
			}
			tailed = true
			extend()
			continue
		}
		extend()
		if tailed && m.Type == "bytes" {
			if _, err := os.Stdout.Write(m.Data); err != nil {
				return 0, err
			}
		}
	}
}

// printTail prints the tail's window of screen rows, trimming the trailing
// empty rows a prompt leaves behind. It refuses a window that does not fit,
// the way yank refuses one.
func printTail(lines []string, start, rows int) error {
	if start < 0 || start > rows || rows > len(lines) {
		return fmt.Errorf("tail reply out of range")
	}
	out := lines[start:rows]
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) > 0 {
		fmt.Println(strings.Join(out, "\n"))
	}
	return nil
}

// waitStatus renders the status line a wait ends on. Dead and unknown stand
// apart from done so a caller skimming the line sees the shape at once.
func waitStatus(res waitResult) string {
	switch {
	case res.TimedOut:
		return "* timeout"
	case res.Reason == "dead":
		return "* dead"
	case res.Exit != nil:
		return fmt.Sprintf("* done, exit %d", *res.Exit)
	default:
		return fmt.Sprintf("* done, exit unknown (%s)", res.Reason)
	}
}

// waitExit maps a wait's ending to the CLI's own exit code: the command's
// code, 124 after the quiet-idle timeout, and 0 when no code was observed, so
// a caller distinguishes a command that gave no code from a failed wait; the
// status line carries the reason.
func waitExit(exit *int, timedOut bool) int {
	switch {
	case timedOut:
		return 124
	case exit != nil:
		return *exit
	}
	return 0
}

// waitReport prints the final answer: one JSON object on stdout with --json,
// one status line on stderr without.
func waitReport(c *cmdFlags, res waitResult) error {
	if c.jsonOut {
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	fmt.Fprintln(os.Stderr, waitStatus(res))
	return nil
}

// peek prints the screen a palette row shows.
func peek(session, target string, lines int) error {
	m, err := transport.RPC(session, wire.Msg{Action: "read", Target: target})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	var res struct {
		Lines []string `json:"lines"`
	}
	_ = json.Unmarshal(m.Res, &res)
	out := res.Lines
	if lines > 0 && len(out) > lines {
		out = out[len(out)-lines:]
	}
	fmt.Println(strings.Join(out, "\n"))
	return nil
}
