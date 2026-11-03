package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

const usage = `dm - durable terminal multiplexer (prototype)

  dm [-s name]                 attach to a session, creating it if needed
  dm attach [-s name]          same
  dm new [-s name]             start a session without attaching
  dm ls                        list sessions and whether they are live
  dm kill [-s name]            stop a session's server (panes survive)
  dm exec '<json>' [-s name]   send an action as JSON
  dm write-to <pane> --msg T   type text into a pane (--enter to accept)
  dm read <pane> [--lines N]   print a pane's screen
  dm read <pane> --scrollback N  print N rows above the screen
  dm split <pane> --dir D      split a pane: left right up down
  dm focus <pane>              focus a pane
  dm next-pane | dm prev-pane
  dm status [pane]             show OSC 7501 agent status
  dm list                      print the pane tree as JSON
  dm dash [-s name]            print the palette rows
  dm peek <id> [--lines N]     print the screen a palette row lands on
  dm msg post --state S [--key K] [--msg T]
  dm msg list [--key K] [--limit N] [--json]
  dm msg wait --key K --state S [--timeout 5m]
  dm msg clear [--key K]
  dm msg schema

Actions are the same everywhere:
  {"action":"write-to","target":"p1","msg":"ls","enter":true}
`

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
			fmt.Print(usage)
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
	state      string
	limit      int
	timeout    time.Duration
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
	c.fs.StringVar(&c.state, "state", "", "box state: note working blocked done error")
	c.fs.IntVar(&c.limit, "limit", 0, "list rows, 0 for the whole box")
	c.fs.DurationVar(&c.timeout, "timeout", 5*time.Minute, "wait timeout")
	_ = c.fs.Parse(reorder(args))
	c.args = c.fs.Args()
}

// reorder moves flags before positionals, so -s may follow a pane id.
func reorder(args []string) []string {
	withValue := map[string]bool{"-s": true, "--s": true, "-msg": true, "--msg": true,
		"-action-json": true, "--action-json": true, "-dir": true, "--dir": true,
		"-lines": true, "--lines": true, "-scrollback": true, "--scrollback": true,
		"-key": true, "--key": true, "-state": true, "--state": true,
		"-limit": true, "--limit": true, "-timeout": true, "--timeout": true}
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
		return printRPC(c.session, Msg{Action: "split", Target: c.args[0], Dir: c.dir})

	case "focus":
		c := newFlags("focus")
		c.parse(argv[1:])
		if len(c.args) < 1 {
			return fmt.Errorf("focus needs a pane id")
		}
		return printRPC(c.session, Msg{Action: "focus", Target: c.args[0]})

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
		return fmt.Errorf("msg needs a verb: post, list, wait, clear or schema")
	}
	switch c.args[0] {
	case "post":
		return boxPostVerb(c)
	case "list":
		return boxListVerb(c)
	case "wait":
		return boxWaitVerb(c)
	case "clear":
		return boxClearVerb(c)
	case "schema":
		return boxSchemaVerb(c)
	}
	return fmt.Errorf("msg: unknown verb %q: want post, list, wait, clear or schema", c.args[0])
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
func boxListVerb(c *cmdFlags) error {
	raw, err := json.Marshal(listReq{Key: c.key, Limit: c.limit})
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
	return nil
}

// boxWaitVerb blocks until the key reaches the state and prints the entry. The
// reply arrives only when the wait ends, so it needs a deadline of its own: the
// one rpc sets is ten seconds, and a wait may run for an hour.
func boxWaitVerb(c *cmdFlags) error {
	st, err := parseState(c.state)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(waitReq{Key: c.key, State: st, TimeoutMs: int(c.timeout / time.Millisecond)})
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
	var rep waitReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return err
	}
	boxPrint([]entry{rep.Entry}, 0)
	return nil
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
// zero cuts the message to fit, with an ellipsis where it was cut.
func boxPrint(entries []entry, width int) {
	if len(entries) == 0 {
		return
	}
	seqW, keyW, stateW, fromW := 1, 0, 0, 0
	for _, e := range entries {
		seqW = max(seqW, len(strconv.Itoa(e.Seq)))
		keyW = max(keyW, boxCells(e.Key))
		stateW = max(stateW, boxCells(string(e.State)))
		fromW = max(fromW, boxCells(e.From))
	}
	msgW := -1 // no limit
	if width > 0 {
		msgW = max(width-(seqW+keyW+stateW+fromW+4), 0)
	}
	for _, e := range entries {
		msg := e.Msg
		if msgW >= 0 {
			msg = boxElide(msg, msgW)
		}
		line := fmt.Sprintf("%*d %s %s %s %s", seqW, e.Seq,
			boxPad(e.Key, keyW), boxPad(string(e.State), stateW), boxPad(e.From, fromW), msg)
		fmt.Println(strings.TrimRight(line, " "))
	}
}

// boxCells is how many terminal cells a string takes.
func boxCells(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// boxPad fills s on the right to w cells.
func boxPad(s string, w int) string {
	if n := boxCells(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// boxElide shrinks s to w cells and ends it with an ellipsis.
func boxElide(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if boxCells(s) <= w {
		return s
	}
	return barElideText(s, w)
}
