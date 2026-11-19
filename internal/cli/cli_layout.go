package cli

// The layout verbs: a layout file is a named document that holds a session's
// shape. `dm layout save` captures that shape, from the live server when one
// answers and from the session's state.json when it does not, and `-L <name>`
// on attach, new and the bare word seeds a fresh session from the file.
//
// The layout verbs keep their own flag sets. The clidoc table is the
// parse-time source of truth for the verbs it lists, and it has no layout row,
// so the flags these verbs read are registered here rather than by the table.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"dm/internal/layoutdoc"
	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/transport"
	"dm/internal/wire"
)

// layoutSubcommands is the bare `dm layout` help: one line per subverb.
func layoutSubcommands() string {
	return "dm layout\n" +
		"  dm layout save <name> [-s session] [--force]  capture the session's shape under a name\n" +
		"  dm layout ls                                   one row per layout file with its counts\n" +
		"  dm layout show <name>                          the layout file on stdout\n" +
		"  dm layout rm <name>                            delete a layout file\n" +
		"\n  -L <name> on dm attach, dm new or the bare word seeds a fresh session from a layout.\n"
}

// runLayout dispatches the layout subverbs.
func runLayout(args []string) error {
	if len(args) == 0 {
		fmt.Print(layoutSubcommands())
		return nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(layoutSubcommands())
		return nil
	case "save":
		return layoutSave(args[1:])
	case "ls", "list":
		return layoutLs(args[1:])
	case "show":
		return layoutShow(args[1:])
	case "rm":
		return layoutRemove(args[1:])
	}
	return fmt.Errorf("layout: unknown verb %q: want save, ls, show or rm", args[0])
}

// newLayoutFlags builds one layout subverb's flag set over the shared cmdFlags,
// so -s resolves the same way it does everywhere else: the flag, then a live
// DM_SESSION, else main.
func newLayoutFlags(verb string) *cmdFlags {
	c := newFlagsAt(paths.StateDir(), verb)
	c.fs.Usage = func() { fmt.Fprint(os.Stderr, layoutVerbHelp(verb)) }
	return c
}

// layoutVerbHelp is the -h text for one layout subverb: its usage line and the
// flags its own set carries.
func layoutVerbHelp(verb string) string {
	switch verb {
	case "layout save":
		return "usage: dm layout save <name> [-s session] [--force]\n" +
			"  <name>        the layout name: letters, digits, _ or -, no leading -\n" +
			"  -s name       session the layout is captured from\n" +
			"  --force       overwrite an existing layout file\n"
	case "layout ls":
		return "usage: dm layout ls\n  one row per layout file, with its workspace, window and pane counts\n"
	case "layout show":
		return "usage: dm layout show <name>\n  print the layout file unchanged\n"
	}
	return "usage: dm layout rm <name>\n  delete the layout file\n"
}

// layoutParse parses one layout subverb's args. Flags may follow the
// positional name, the way every other verb in this CLI accepts them, so the
// argv is reordered before the parse.
func layoutParse(c *cmdFlags, args []string) error {
	if err := c.fs.Parse(reorder(args)); err != nil {
		return err
	}
	c.args = c.fs.Args()
	return nil
}

// layoutSave captures the shape of a session and writes it as the layout
// called name. An existing file is refused unless --force: a save is not a
// silent overwrite of something the operator may have hand-edited.
func layoutSave(args []string) error {
	c := newLayoutFlags("layout save")
	c.fs.StringVar(&c.session, "s", c.session, "session the layout is captured from")
	c.fs.BoolVar(&c.force, "force", false, "overwrite an existing layout file")
	if err := layoutParse(c, args); err != nil {
		return err
	}
	if len(c.args) == 0 {
		return fmt.Errorf("layout save needs a name")
	}
	if len(c.args) > 1 {
		return fmt.Errorf("layout save: unexpected argument %q", c.args[1])
	}
	name := c.args[0]
	path, err := layoutdoc.Path(name)
	if err != nil {
		return err
	}
	// Refuse an existing file before reading the session: a lost save must not
	// look at a stopped session for nothing.
	if !c.force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("layout %s already exists; pass --force to overwrite", name)
		}
	}
	c.resolveSession()
	doc, err := captureLayout(c)
	if err != nil {
		return err
	}
	// The file is named for the layout, so the document says so too.
	doc.Name = name
	// A save that Load would refuse is a trap, so refuse it here instead.
	if err := doc.Validate(); err != nil {
		return fmt.Errorf("layout %s: the session's shape is not a layout: %w", name, err)
	}
	if err := layoutdoc.Save(path, &doc); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// captureLayout reads one session's shape: the live server's reply when a
// server answers, else the session's state.json. A save never spawns a server,
// because that would start every pane of a session the operator stopped.
func captureLayout(c *cmdFlags) (layoutdoc.Doc, error) {
	if envSessionLive(c.root, c.session) {
		return captureLayoutLive(c.session)
	}
	st, err := session.LoadStateAt(c.root, c.session)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return layoutdoc.Doc{}, fmt.Errorf(
				"layout save: session %q has no live server and no state file", c.session)
		}
		return layoutdoc.Doc{}, fmt.Errorf("layout save: %w", err)
	}
	return *layoutdoc.Project(st), nil
}

// captureLayoutLive asks a live server for its shape.
func captureLayoutLive(name string) (layoutdoc.Doc, error) {
	m, err := transport.RPC(name, wire.Msg{Action: "layout"})
	if err != nil {
		return layoutdoc.Doc{}, err
	}
	if !m.OK {
		return layoutdoc.Doc{}, fmt.Errorf("%s", m.Error)
	}
	var res struct {
		Layout layoutdoc.Doc `json:"layout"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		return layoutdoc.Doc{}, fmt.Errorf("layout save: bad reply: %w", err)
	}
	return res.Layout, nil
}

// layoutLs lists the layout files, newest column last. A broken file keeps its
// row and prints its error where the counts would be, so one hand edit does
// not hide the others.
func layoutLs(args []string) error {
	c := newLayoutFlags("layout ls")
	if err := layoutParse(c, args); err != nil {
		return err
	}
	if len(c.args) > 0 {
		return fmt.Errorf("layout ls: unexpected argument %q", c.args[0])
	}
	entries, err := layoutdoc.List()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("no layouts")
		return nil
	}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		mod := e.ModTime.Format(time.DateTime)
		if e.Err != nil {
			rows = append(rows, []string{e.Name, e.Err.Error(), "", "", mod})
			continue
		}
		rows = append(rows, []string{
			e.Name, strconv.Itoa(e.Workspaces), strconv.Itoa(e.Windows), strconv.Itoa(e.Panes), mod,
		})
	}
	printTable([]string{"NAME", "WORKSPACES", "WINDOWS", "PANES", "MODIFIED"}, rows)
	return nil
}

// layoutShow prints the file unchanged, so a hand edit round-trips.
func layoutShow(args []string) error {
	c := newLayoutFlags("layout show")
	if err := layoutParse(c, args); err != nil {
		return err
	}
	if len(c.args) != 1 {
		return fmt.Errorf("layout show needs a name")
	}
	path, err := layoutdoc.Path(c.args[0])
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Print(string(b))
	return nil
}

// layoutRemove deletes one layout file.
func layoutRemove(args []string) error {
	c := newLayoutFlags("layout rm")
	if err := layoutParse(c, args); err != nil {
		return err
	}
	if len(c.args) != 1 {
		return fmt.Errorf("layout rm needs a name")
	}
	return layoutdoc.Remove(c.args[0])
}

// startLayout resolves -L for a verb that starts or attaches a session. With
// no layout it hands the name back untouched and no path, and refuses --force:
// a bare --force would read as a reset, which is dm reset --force's job.
//
// The file is resolved, read and validated before anything spawns, so a bad
// name or a malformed document fails as the CLI's own error instead of the
// server's. The session defaults to the layout name, so attaching the same
// layout twice reattaches instead of stacking sessions; an explicit -s or a
// positional name wins. --force resets the session first, through the same path
// dm reset --force uses, so the layout has a fresh session to seed.
func startLayout(c *cmdFlags, session string) (layoutPath, name string, err error) {
	if c.layout == "" {
		if c.force {
			return "", "", fmt.Errorf(
				"--force needs -L <name>: use dm reset --force to reset a session")
		}
		return "", session, nil
	}
	path, err := layoutdoc.Path(c.layout)
	if err != nil {
		return "", "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("layout %s: %w", c.layout, err)
	}
	if _, err := layoutdoc.Load(abs); err != nil {
		return "", "", fmt.Errorf("layout %s: %w", c.layout, err)
	}
	name = session
	if !c.sessionFlag && len(c.args) == 0 {
		name = c.layout
	}
	if c.force {
		if _, err := os.Stat(paths.SessionDirAt(c.root, name)); err == nil {
			// The bound root, not paths.StateDir: --force must reset the
			// session the flags point at, and nothing else.
			if err := transport.ResetSessionAt(c.root, name); err != nil {
				return "", "", err
			}
		}
	}
	return abs, name, nil
}

// layoutFlagNames are the flag names -L registers. Both spellings set one
// field, so the short and the long form cannot disagree.
var layoutFlagNames = []string{"L", "layout"}

// registerLayoutFlags adds -L, its --layout long form and --force to a verb's
// set. The clidoc table lists none of them for attach and new, so the layout
// path registers them itself. -L is the layout a start or attach seeds a fresh
// session from, and --force resets the session first.
func registerLayoutFlags(c *cmdFlags) {
	for _, name := range layoutFlagNames {
		c.fs.StringVar(&c.layout, name, "", "start from a layout file")
	}
	c.fs.BoolVar(&c.force, "force", false, "reset the session before applying the layout")
}
