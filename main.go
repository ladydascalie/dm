package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
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
	_ = c.fs.Parse(reorder(args))
	c.args = c.fs.Args()
}

// reorder moves flags before positionals, so -s may follow a pane id.
func reorder(args []string) []string {
	withValue := map[string]bool{"-s": true, "--s": true, "-msg": true, "--msg": true,
		"-action-json": true, "--action-json": true, "-dir": true, "--dir": true,
		"-lines": true, "--lines": true, "-scrollback": true, "--scrollback": true}
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
