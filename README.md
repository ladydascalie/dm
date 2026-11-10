# dm

A small, durable terminal multiplexer. One static Go binary that carries the
server, the pane harness, the TUI client and the CLI.

Prototype status: M1 works end to end. Splits, windows, workspaces, OSC 7501
agent status, durable sessions and a JSON RPC are implemented. Scrollback search
is not implemented.

## Identifiers

Ids are addressable, not opaque counters behind a random string:

```
s1        workspace 1
w1        window 1
w2p3      window 2, pane 3
```

A pane id is its window id plus `p` and a per-window sequence that only grows.
The id tells you where the pane lives and can be typed without a lookup. A
window id is per session. A workspace id is per session.

Counters only move forward, so an id is never reused after a close. A pane that
is split off gets the next number in its window; the existing pane keeps its id.
Names and labels stay separate and editable: a window's name, a workspace's
name, a pane's recorded agent name. A name is a cosmetic label. Renaming a
workspace leaves the id alone, so `s1` stays `s1`, and the action takes the
id, so a background workspace can be renamed.

State written before this scheme is migrated once, in tree order, on load.

## Build

```sh
go build -o dm .
```

## Docs

The command table in `internal/clidoc/clidoc.go` is the source. `dm man`
renders it as a man page and `dm llms` renders it as a one-pass teaching
document. `dm html` renders the whole surface, config, keymaps and themes
included, as one self-contained page, and opens it in the browser. The page
paints itself from the built-in theme's palette. The committed files
`docs/dm.1`, `docs/llms.txt` and `docs/dm.html` are snapshots, and the gate
fails when one is stale.

## Code layout

`main.go` dispatches the internal verbs and hands everything else to the CLI.
`probe.go` is the `__probe` harness. `blackbox/` holds the integration tests and
`docs/` the generated snapshots.

```
internal/cli         the CLI verbs
internal/clidoc      the command table behind usage, dm man and dm llms
internal/client      the TUI: input, keys, frames, floats and the bar
internal/server      the session daemon: layout, focus, clients, state, the box
internal/pane        the pane daemon: PTY, VT screen, status and output ring
internal/session     the state file model, the id scheme and its migration
internal/wire        the socket wire contract
internal/transport   the client side of the session socket
internal/box         the message box types, bounds and schema
internal/osc         the OSC 7501 status filter and parser
internal/notify      the desktop notification policy
internal/layout      the split tree and pane geometry
internal/rail        the sidebar painter
internal/switcher    the dashboard model and painter
internal/boxview     the message box float
internal/keymapview  the keymap float
internal/keymap      the keymap model, the KDL loader and the help table
internal/theme       the palette, glyphs and theme file parser
internal/kdl         the KDL tokenizer shared by keymap and theme
internal/cells       cell-width measuring and clipping
internal/segment     a styled run of text
internal/panel       the bordered box painter
internal/vt          VT replay and screen serialisation
internal/paths       the session filesystem layout
internal/testutil    test helpers shared by more than one package
```

## Use

```sh
dm                      # attach to "main", creating it if missing
dm attach -s work       # resume a session (durable, see below)
dm ls                   # sessions with pane counts and project
dm list                 # panes as a table
dm new -s work          # start a session without attaching
dm kill -s work         # stop the server; panes keep running
dm purge                # remove dead sessions whose panes are gone
dm reset -s work --force  # discard a session and its panes for a full restart
dm man                  # the man page, docs/dm.1
dm llms                 # the one-pass teaching document, docs/llms.txt
dm html                 # the same surface as a page, opened in the browser
dm html -o - > dm.html  # or write the page to stdout
```

Keys inside the TUI. `ctrl+b` arms the prefix and shows the action box,
bottom right. It stays until an action runs or `esc` dismisses it. The box
sizes to its widest row. Its top border carries `prefix`, its bottom border
names `esc`, and a narrow screen drops the action column before it cuts a key.

| key | action |
| --- | --- |
| `ctrl+b` | prefix (press it twice to send a literal `ctrl+b`) |
| `\|` / `-` | split right / down |
| `<` / `^` | split left / up |
| `[` | scrollback (also the mouse wheel) |
| `h j k l` or arrows | focus pane left / down / up / right |
| `n` / `p` | next / previous pane |
| `x` | close pane |
| `X` | close window |
| `z` | zoom pane |
| `c` | new window |
| `w` / `W` | next / previous window |
| `0-9` | select window |
| `t` | new workspace |
| `s` | switch session (picker: j/k, enter, esc) |
| `a` | dashboard: every pane in a full-screen table |
| `m` | message box: the session's log as a floating pane |
| `?` | all keymaps, in a floating pane |
| `)` | next workspace |
| `r` | rename the active workspace |
| `b` | cycle the sidebar: full, thin, hidden |
| `ctrl+r` | reload config.kdl and its theme |
| `d` / `q` | detach |

`prefix r` opens the rename prompt as a line above the status bar. It starts
from the workspace's current name, so typing appends to it. Backspace deletes
one character and `ctrl+u` clears the line. `enter` commits, `esc` cancels, and
an empty name cancels too. The right-click menu offers the same row.

`prefix z` zooms the focused pane. The zoomed pane fills the window's pane
area, and the rail and the status bar stay in place. The program inside
reflows to that size. `prefix z` again restores the splits. The zoom clears
when the focus, the window or the workspace changes, and dm does not persist
it.

The binding comes from the built-in table. A `config.kdl` that leaves out
`zoom-pane` still gets `z`, because the file is a patch over the defaults. Bind
it to `none` to drop it, and `prefix z` then toasts `no binding`.

Drag a divider with the left mouse button to resize the split. The client
sends `resize-split` with the two panes beside the divider and the ratio, at
most every 30 ms plus a final call on release. Both sides keep at least three
cells. A divider always belongs to dm, even when a pane claims the mouse: the
app's PTY covers only its content rect, so the border press is not its event.

## Config

One file holds the config. dm reads `$HOME/.config/dm/config.kdl`, or
`$XDG_CONFIG_HOME/dm/config.kdl` when `XDG_CONFIG_HOME` is set. On first run dm
writes that file with the built-in values, then you edit it. Every node is
optional and each one overrides its built-in value.

| node | value | default | does |
| --- | --- | --- | --- |
| `prefix` | one key sequence | `"ctrl+b"` | Set the prefix key that opens the main keymap. |
| `default-fold` | `"fold"` or `"unfold"` | `"fold"` | Set whether a new window's rail starts folded or unfolded. |
| `tab-min-width` | an integer of at least 3 cells | `8` | Set the smallest tab width in the window tab bar. |
| `tab-max-width` | an integer of at least 3 cells, not below `tab-min-width` | `20` | Set the largest tab width before the label is cut. |
| `padding` | an integer from 0 to 4 cells | `0` | Set the blank cells between a pane's border and its content. A change resizes every pane on reload. |
| `glyphs` | `"auto"`, `"ascii"`, `"unicode"` or `"nerd"` | `"auto"` | Choose the glyph vocabulary the chrome draws with. |
| `window-label` | a quoted Go template | the id and folder template | Set the template that renders a window label. The Window tab bar section carries the default and its data. |
| `dashboard-layout` | `"full"` or `"float"` | `"full"` | Draw the dashboard over the whole screen, or as a palette centred over the panes. A float leaves the panes live under it. |
| `dashboard-preview` | an integer from 0 to 100 | `60` | Set the share of the dashboard body the preview band opens at. Zero opens on the table alone; the peek key still shows the band, and `+`/`-` resize it. |
| `theme` | a quoted theme name matching `[A-Za-z0-9_-]+` | none | Select a theme from the themes directory beside `config.kdl`. No node keeps the built-in theme. |
| `keymap` | a quoted mode name and a `{ "action" "key" }` block | the built-in table | Patch one keymap mode; an action the file leaves out keeps its built-in key. |

### What a reload picks up

`ctrl+r` re-reads `config.kdl` and the theme file it names, and applies every
node. It also asks the server to re-read the same file, so the server's layout
follows `padding` and `default-fold` too. A failed read on either side keeps the
previous values. A padding change resizes every pane, so a reflow follows.

A theme file is `$XDG_CONFIG_HOME/dm/themes/<name>.kdl`, or
`$HOME/.config/dm/themes/<name>.kdl` when `XDG_CONFIG_HOME` is unset. That is
the `themes/` directory beside `config.kdl`.

dm reads these environment variables:

| variable | effect |
| --- | --- |
| `DM_STATE` | State directory; overrides `XDG_STATE_HOME`. |
| `XDG_STATE_HOME` | State directory base; dm uses `$XDG_STATE_HOME/dm`. |
| `XDG_CONFIG_HOME` | Config directory base; dm reads `$XDG_CONFIG_HOME/dm/config.kdl`. |
| `HOME` | Home directory base; the fallback for both XDG paths. |
| `DM_SESSION` | The session a pane belongs to; dm refuses to attach that session from inside it. |
| `DM_PANE` | The pane id a pane's shell runs in. |
| `DM_NERD` | `1` forces Nerd Font glyphs, `0` forces the Unicode fallback. |
| `DM_NOTIFY` | `0` turns desktop notifications off. |
| `DM_STATS` | Non-empty prints style-cache and frame-pacing stats to stderr on exit. |
| `COLUMNS` | Terminal width when the client cannot ask the terminal. |
| `SHELL` | The shell a new pane runs. |
| `TMUX` | Set inside tmux; dm uses it to pick its terminal behaviour. |
| `ESCTEST_DIR` | Test only: the esctest2 directory the conformance harness searches. |

`dm html` opens every table here, with the keymaps and the theme keys, as one
page in the browser. `dm html -o -` prints it instead, and `dm html -o path`
writes it.

## Keybindings

The key table above is the default. dm reads the keymap from
`$HOME/.config/dm/config.kdl`, or `$XDG_CONFIG_HOME/dm/config.kdl` when
`XDG_CONFIG_HOME` is set. On first run dm writes that file with the current
table, then you edit it.

```kdl
prefix "ctrl+a"

keymap "main" {
    "split-right" "S"
    "detach" "D"
}
```

The file is a patch over the built-in table. An action the file names takes
its key, and an action it leaves out keeps the built-in key. Bind an action to
`none` to unbind it. The prefix line sets the prefix key; a file without one
keeps `ctrl+b`. Each keymap block maps actions to keys, and a mode the file
leaves out keeps the built-in mode. `prefix ctrl+r` reloads `config.kdl` and its
theme without restarting the session. A broken config or theme file leaves the
running settings in place and toasts the error.

A binding line in the main keymap may carry a help tag:

```kdl
keymap "main" {
    "zoom-pane" "z" help "Layout"
}
```

The tag moves that row to the named section of the armed help box. An
untagged row keeps the built-in section. The five built-in names are
Layout, Pane, Window, Session and Utility, and any other name is a custom
section.

An old config without tags renders the same help box. The tag moves a row the
help box already lists. It does not add a row.

One mode is one keymap.

- `main` is the table the prefix key opens.
- `menu` is the right-click menu.
- `picker` is the session picker.
- `switcher` is the dashboard.
- `scroll` is scrollback.
- `rename` is the rename prompt.
- `box` is the message box.
- `help` is the keymap view.

The prefix then `?` opens the keymap view. It lists every mode the config
defines, in the order above, with each action and its key. `j`/`k` and
`PgUp`/`PgDn` scroll, `g` and `G` jump to the ends, and `esc` or `q` closes.
`/` searches the rows: type to match a key or action, `n` and `N` move between matches, `enter` keeps the query and `esc` while typing clears it. The active match is yellow and the others are white. The prefix key and `?` close it too. A config written before this mode falls
back to the built-in help bindings, so the view always scrolls and closes.

A key is one key. Arrows and `PgUp`/`PgDn` are named sequences: `up`, `down`,
`left`, `right`, `pgup`, `pgdn`. The dashboard binds its own letters and digits,
and takes every other printable key as query text. The rename prompt keeps
every printable key as text and binds only its control keys. The keymap view,
the armed help box and the right-click menu take their keys from the config, so
a remap shows there too.

## Themes

A theme sets the RGB palette and the glyphs dm paints. `config.kdl` selects one
with a top-level node:

```kdl
theme "night"
```

dm reads that theme from `$XDG_CONFIG_HOME/dm/themes/night.kdl`, or
`$HOME/.config/dm/themes/night.kdl` when `XDG_CONFIG_HOME` is unset. That is the
`themes/` directory beside `config.kdl`. At most one `theme` node, and its name
is letters, digits, `_` and `-`. No node selects the built-in theme. A theme edit
is picked up by the reload key, so a theme change does not need a quit and
reattach.

Every block and every key is optional. An omitted key keeps the built-in
default, so a theme file names only what it changes. These fail the load, and
the error names the line: an unknown or duplicate block or key, a colour that
is not `#rrggbb`, and a glyph that is not one rune of one cell.

The whole shape, at the built-in values:

```kdl
palette {
    "rail-bg"       "#14131a"
    "rail-focus-bg" "#1f1e27"
    "rail-text"     "#e9eaf1"
    "rail-dim"      "#8f93a8"
    "rail-header"   "#a6a9bd"
    "rail-window"   "#9599ae"
    "rail-diff-add" "#78dc82"
    "rail-diff-del" "#ff7d7d"
    "rail-working"  "#f5be50"
    "bar-accent"    "#7ac0eb"
}

accents {
    "accent-1" "#bb9af7"
    "accent-2" "#6edceb"
    "accent-3" "#f08cd7"
    "accent-4" "#7ea9ff"
    "accent-5" "#f0a05a"
    "accent-6" "#8cd79b"
}

glyphs {
    "grip"                "•"
    "divider-row"         "━"
    "divider-col"         "┃"
    "border-top-left"     "┌"
    "border-top-right"    "┐"
    "border-bottom-left"  "└"
    "border-bottom-right" "┘"
    "border-horizontal"   "─"
    "border-vertical"     "│"
    "border-tee-left"     "├"
    "border-tee-right"    "┤"
    "fold-open"           "▾"
    "fold-closed"         "▸"
    "active-mark"         "▸"
    "workspace-bar"       "▌"
    "dirty"               "●"
}

states {
    "working" {
        "glyph"  "●"
        "colour" "#f5be50"
    }
    "idle" {
        "glyph"  "○"
        "colour" "#78c8e1"
    }
    "blocked" {
        "glyph"  "▲"
        "colour" "#ff6e6e"
    }
    "done" {
        "glyph"  "✓"
        "colour" "#6ee191"
    }
    "error" {
        "glyph"  "✕"
        "colour" "#ff82a0"
    }
    "unknown" {
        "glyph"  "?"
        "colour" "#8f93a8"
    }
    "shell" {
        "glyph"  "▶"
        "colour" "#8f93a8"
    }
}
```

Pasting that block paints the Unicode glyphs even with Nerd Fonts on: naming a
glyph replaces its Nerd Font form too.

A colour is `#rrggbb`. A glyph is one cell wide. A minimal theme names a few
keys and inherits the rest:

```kdl
palette {
    "rail-bg" "#101014"
}

glyphs {
    "grip" "+"
}

states {
    "working" {
        "glyph"  "◐"
        "colour" "#ffcc66"
    }
}
```

Where an item has a Nerd Font icon, an explicit theme glyph wins over it. A
theme's state glyph replaces both the Unicode and the Nerd form. Role icons,
the agent bar, robot and terminal marks, stay compiled in and are not themeable.

A missing or malformed theme file toasts the error, and dm runs on the built-in
theme. The built-in theme reaches WCAG AA on every ground it paints. dm does
not check a user theme: its contrast is the author's.

## Dashboard

`prefix a` opens the dashboard: a table of every pane in the session, one row
per pane. It is native: no child process and no PTY. It takes the whole screen
by default; `dashboard-layout "float"` draws it as a palette centred over the
panes instead, and leaves the panes live under it. The default order is recent,
the newest agent status change first.

The columns are the jump key, the project, the agent label, git, the status,
the time since the last status change and the OSC window title. The label is
the recorded name, else the reporting app, else the pane id. Git reads `+N -N`
for the lines the tree gains and loses, or `clean`, and a dash outside a
repository. The status cell carries the state glyph and its word; a pane that
never reported a state reads as a shell. The age is seconds under a minute,
then minutes, hours and days, and a dash when the server has no stamp. The
title is the program's own window title, elided to its column.

The letters are keys. The digits pick: `1` to `9` select that row and go to
its pane. `j` and `k`, the arrows, or ctrl+n and ctrl+p move the selection, and
enter goes to the selected pane. The prefix key again, ctrl+c or `esc` closes
without picking. It takes no mouse input.

`/` starts filtering, and the query row then takes every printable key, so a
search holds `d`, `s`, `i` and the digits without an action firing mid word.
The query matches a name, the app, the title, the branch, the worktree, the
folder or the project. Backspace edits it and ctrl+l clears it. In filter mode
`esc` unwinds: the first press clears the query, the next leaves the mode and
the table shows every pane again, and only the next one closes the dashboard.
The arrows and ctrl+n and ctrl+p still move the selection while filtering.

`s` cycles the ordering: recent, then project, then status. Recent puts the
newest status change first. Project groups the rows by project name, with the
panes outside a project last. Status puts the states that want attention first:
blocked, error, working, done, idle, unknown, then a shell.

The right of the screen previews the selected pane's live screen: from the
local VT in the active window, and from a screen request elsewhere. A read
paints the far pane first, then the client asks the server for that pane's
screen every 250 ms while it stays selected. ctrl+u and ctrl+d scroll it toward
older and newer rows, `+` and `-` resize the band, and `p` peeks: it hides the
band so the table takes the full width. A float under 10 rows or 60 columns
drops the band whatever the toggle says. The `dashboard-preview` config node
sets the share the band opens at, from 0 for the table alone to 100; the band
keeps its own 20 to 85 range, and the peek key uses the configured share.

`i` sends every key to the selected pane until esc. The dashboard stays up and
the answer shows in the preview, and the footer says `input` while the mode is
on. `d` shows the selected pane's uncommitted diff in a read-only pager: `j`
and `k` scroll a row, `pgup` and `pgdn` a page, `g` and `G` jump to the ends,
and `q` or `esc` closes. The pager border names the worktree.

The dashboard is client owned: it starts when the key is pressed and ends when
the choice is taken, so it never enters the session tree and never survives a
detach. `dm dash` prints the pane rows outside the TUI, and `dm peek <id>`
prints the screen a row shows, so the dashboard can be driven from a shell.

## Sidebar

The left columns of the screen show a sidebar when the session asks for one.
It is a tree: a project header, then its workspaces, then each workspace's
windows, then each window's panes. A workspace starts unfolded. A workspace row
leads with a caret, then its name in bold, then the pane count at the right
edge in dim grey. One blank row separates workspace blocks.

A workspace may carry a project name. Workspaces that share a name form one
group, and the rail paints the group as one block. A group opens with a project
header row: the bar in column 0, the project name in the project's accent and
bold, then the group's workspace count at the right edge. Every row of the
group paints the same bar in column 0, the header included. A row outside a
project leaves that cell blank, so the fold carets keep one x for the whole
rail. The bar is the tie. The header is the name.

A project name hashes to one of six accents, with the collision avoidance the
rail uses elsewhere: a preferred slot, then the next free slot. Two projects in
one session never share a hue. The project owns the colour, so a workspace name
drops its own accent: the active workspace name is bright, the rest are header
grey, both bold.

Workspaces that share a project stay contiguous. The state slice is the order,
and a group keeps the position of its first workspace; order inside a group
holds. The rail never re-sorts: it paints the slice order it is given, and
`next-workspace` and `prev-workspace` walk the same slice, so the keys and the
rail agree on where the next workspace is. A new workspace lands at the end of
its project's block, and inherits the project of the workspace it was created
from. `dm set-project <ws> <name>` tags a workspace; an empty name clears the
tag. The workspace row menu offers `Set project`, which opens the rename prompt
with the current project as its draft.

Under an unfolded workspace, each window gets a window row. It leads with a
caret at the second indent, then the rendered window label, then the branch in
dim grey, then the dirty counts at the right edge. A window with no rendered
label falls back to its folder, then to its name. The source pane is the
focused pane when it carries a branch, else the first pane in the window that
carries a branch, else the focused pane, else the window's first pane. When
the source pane has a commit message, a dim note row sits directly under the
window row. The window's pane rows follow when the window is unfolded.

Fold is two-level. A folded workspace hides its windows and panes and leaves
only the workspace row. A folded window keeps its row and hides only its panes
and note row. The caret on either row toggles that level.

An unfolded pane row labels itself with its recorded name, then the `app=` it
last reported, then the pane id, with a state glyph in a colour per state beside
the label. A pane counts as an agent when it has a recorded name or has spoken
the status protocol at least once; the rest are shells. An agent row leads with
a rocket icon (or a `▎` bar without Nerd Fonts), a shell row with a terminal
icon, and a dim shell row otherwise leaves those two cells blank, so the state
glyph keeps its column. The report is sticky: `state=clear` does not demote an
agent, and both the mark and the last `app=` are stored in the state file. The
focused pane's row is highlighted.

A left press on a project header row focuses the first pane of the group's
first workspace. A left press on a workspace row focuses the first available
pane in that workspace. A left press on the workspace caret toggles the
workspace fold without changing focus. A left press on a window row focuses the
first available pane in that window and makes that window the active window. A
left press on the window caret toggles the window fold without changing focus.
A left press on a pane row focuses that pane. A pane in another window switches
the active window as well. Double-clicking a workspace row opens the inline
rename prompt for that workspace. Double-clicking a window row opens the inline
rename prompt for that window, the same prompt the tab bar double-click opens.
Double-clicking a pane row opens the inline rename prompt for that pane. Right
click opens the row menu for the row under the pointer: rename, close, new,
fold and focus, as the row allows. The menu takes its keys from the main
keymap. The bottom row toggles the rail between full and thin.

A pane keeps the server told where its shell is, so a `cd` moves the folder the
rail shows and the folder the next pane, window or workspace opens in. The
folder follows the shell, not the launch: a window created after a `cd` belongs
where the shell went. A new window is named after the folder it starts in, and
it starts in the folder of the pane it was created from; `rename-window`
overrides the name. A window whose label is still the old positional number is
renamed from its pane's folder when the session loads.

A long label is cut with `…` so the right-aligned stats fit, and the branch is
dropped first. When even that cannot, the rail draws no tail at all, never half
a pair. The rail paints its own dark background. The built-in theme uses
explicit RGB colours, not palette slots, and every label and glyph of it
reaches WCAG AA contrast on both the rail and the focused row. Icons use Nerd
Font code points when `fc-list` finds a Nerd font; `DM_NERD=1` forces them and
`DM_NERD=0` falls back to the Unicode glyphs. The rail is 34 columns by
default, at most a third of the screen and never wider than 46 columns;
`set-rail` changes it. `prefix b` cycles the sidebar through three states:
full, thin, and hidden. The thin rail is four columns: the agent bar, one state
glyph per pane, and no names. It always lists panes, folded or not, and packs
one row per pane, with no header, note or gap row, and no project header or
bar. A hidden or thin sidebar remembers the last full width.

The rail's last column is a grab strip: drag it to resize the rail from four
columns up to a third of the screen, capped at 46. A drag to the left collapses
the rail to its thin form; hiding stays on `prefix b`.

## Window tab bar

One row sits above the pane area, to the right of the rail. It is always
visible. One tab per window in the active workspace, in window order. The tab
label and the sidebar window row's leading label come from the `window-label`
template. The active tab is highlighted. Clicking a tab selects that window.
Double-clicking a tab opens the inline rename prompt for that window. The
server reserves the tab bar row when it computes pane rectangles, so pane
boxes start below it.

A tab is its label plus one padding cell on each side, clamped to a minimum
and a maximum width. The bounds come from `config.kdl`:

```kdl
tab-min-width 8
tab-max-width 20
```

Both are cell counts and both default to the values above. A label wider than
the maximum is cut with `…`. Set the two equal for a constant tab width. A
minimum below 3 and a maximum below the minimum are config errors.

The label template is a Go `text/template`. It applies to the tab bar, the
sidebar window row's leading label and the status bar window chip, so one
window reads the same in every place. The data is a struct with these fields:

- `.ID`: the window id, e.g. `w1`.
- `.Name`: the window name, the label `rename-window` edits.
- `.Named`: true when the user set the name with `rename-window`.
- `.Cwd`: the source pane's working directory.
- `.Root`: the source pane's repository root, empty outside git.

The `base` function prints the last path element: `{{base .Root}}` is the
repository folder name. The default template shows the name of a named window
and the id plus the repository folder of an unnamed one:

```kdl
window-label "{{if .Named}}{{.Name}}{{else}}{{.ID}}{{with base .Root}} {{.}}{{end}}{{end}}"
```

An unnamed window in `.../dm` reads `w1 dm`. A window the user renamed to
`api` reads `api`. A template that does not parse, or that names an unknown
field or function, is a config error.

## Status bar

The last row of the screen is the status bar: one line of segments on explicit
RGB, one idea per segment, with a single gutter cell of the bar ground between
neighbours. The order is fixed: the session name, the workspace name, one chip
per window, the focus chip, then the pinned right block.

- The session name is railText, then the workspace name in railHeader.
- One chip per window, in window order, is `N label`, where the label comes
  from the `window-label` template. The number is the window's 1-based
  position in the window list. A chip keeps its number when a
  neighbour is dropped. The active window's chip reads as active: a `▸` in
  barAccent, then the chip in bold railText, both on railFocusBG. The other
  chips are railDim on the ground.
- The focus chip sits on railFocusBG. It leads with the focused pane's state
  glyph in that state's colour, then the pane id in bold railText, then the
  pane name and the status word in railDim. A missing status reads `unknown`; a
  shell reports no state at all, so it shows `▶` in railDim instead of a glyph.
  The focused pane is the entry in the window's pane list whose id is the focus.

Colours are explicit RGB, never terminal palette slots. The built-in theme
paints them only on grounds it checks: railBG (#14131a) for the ground and the
gutter, railFocusBG (#1f1e27) for the active window chip and the focus chip,
railText (#e9eaf1), railHeader (#a6a9bd), railDim (#8f93a8), barAccent (#7ac0eb)
for the active mark, and the state colours of the `states` block in Themes. A
user theme names its own RGB, so it chooses its own grounds and answers for its
own contrast. Every footer foreground of the built-in theme reaches WCAG AA,
4.5:1 or better, on both grounds it is painted on: railDim on railFocusBG is the
tightest at 5.42:1, and the barAccent mark reaches 8.30:1 on the focus fill it
sits on, 9.29:1 on the ground.

At 80 columns, with the session `pi-footer-2026`, the workspace `main`, three
windows and the second one active, a blocked focus pane `w1p2` named `shell`,
the prefix unarmed, and a scroll position pinned at the right. State glyphs are
drawn here in their Unicode form, without Nerd Fonts:

```
pi-footer-2026 main 1 api ▸ 2 web 3 docs ▲ w1p2 shell blocked   SCROLL w1p2 +1/9
```

cells 1-14 the session name, 15 the gutter, 16-19 the workspace name, 20 the
gutter, 21-25 window 1, 26 the gutter, 27-28 the active window mark, 29-33 the
active window chip, 34 the gutter, 35-40 window 3, 41 the gutter, 42 the blocked
glyph, 43-47 the pane id, 48-53 the pane name, 54-61 the status word, 62-64
padding on the ground, 65-80 the pinned scroll info.

The pinned right block holds `PREFIX ` while `armed` is set, then the
transient info text when there is one: `SCROLL <pane> +<offset>/<max>` while
scrolling, or `drag to resize` while the pointer rests on a divider. It is
anchored to the last column, and one cell of ground always separates it from the
left block, even when the left block would otherwise fill the row. When the
pinned block cannot fit, its leftmost segment goes whole, `PREFIX ` first and
then the info, and the survivor is clipped at its right with `…`. The left block
is dropped entirely before a pinned segment is.
The bar never paints past the column count, and it pads the rest of its row on
the ground, so no cell of the previous frame shows through.

At 34 columns, with the session `work-2026`, the prefix armed and no scroll
position, the ladder runs through five rungs: the chips lose their names, the
outer chips go, both names shrink to `…`, and the focus chip drops its status
word but keeps its name. `PREFIX ` stays pinned at the right, seven cells clear
of the last chip:

```
… … ▸ 2 ▲ w1p2 shell       PREFIX 
```

cells 1 the session tail, 2 the gutter, 3 the workspace tail, 4 the gutter, 5-6
the active window mark, 7 its number, 8 the gutter, 9 the blocked glyph, 10-14
the pane id, 15-20 the pane name, 21-27 padding on the ground, 28-34 `PREFIX `.

The left block elides in a fixed order, stopping as soon as the line fits:

1. window chips lose their name and keep the number
2. trailing window chips go, never the active one
3. the workspace name shrinks with a trailing `…`
4. the session name shrinks with a trailing `…`
5. the focus chip drops the status word, then the pane name
6. the focus chip keeps its glyph and its pane id last

Width is cells, not runes. A wide rune takes two cells, and a wide rune that
would straddle a limit is dropped whole rather than split. A width of zero or
less paints nothing.

The toast stack sits above the bar, bottom right: the bar keeps its full row
while toasts float over the panes, and no toast is painted on the bar row.

## Scrollback and mouse

Mouse reporting is always on, there is no setting (SGR 1000 + 1002 + 1003 +
1006, disabled again on detach). Left click focuses the pane under the pointer,
the wheel scrolls the pane under the pointer, and dragging the divider between
two boxes resizes that split. Panes are boxed with no gap cell: neighbouring
boxes touch, so the divider is two adjacent border cells and the visual space
is one cell on each axis. Both sides of a drag keep at least three content
cells.

Every divider carries a dim grip dot pair at its midpoint, so the handle is
visible before the pointer arrives. Hovering repaints the whole divider with
heavy line glyphs and shows `drag to resize` in the pinned right block of the
status bar, flush with the last column. The hover and the press resolve through
the same `padHit` zone, so what lights up is exactly what will drag. No-button
motion is the mux's own: dm uses it for that hover and does not forward it to
pane apps, which asked for button motion only.

Right click opens the pane menu, even over a program that owns the mouse: it is
a window-manager gesture, not the app's. Move with `j`/`k` or the arrows,
`enter` or a digit to run, `esc` or `q` to close. With the mouse, hovering
highlights the row under the pointer, a left click on a row runs it, and a
right press, drag and release runs the row under the release. The release of
the press that opened the menu, and clicks on the title or border, do not
dismiss it; a click outside does. The menu
offers split right/down/left/up, zoom, focus, close pane, scrollback, new
window, new workspace, rename workspace, switch session and detach. If the
program in that pane enabled mouse reporting itself, the event is forwarded to
it instead. That is how `pi`, `htop` and `vim` keep their own scrolling: they
own the wheel when they ask for it.

A program sees only its own screen, so dm rewrites the point to the pane's
content rect before it forwards the event; a point on the border clamps to
the nearest edge cell.

Ctrl+click on a URL opens it with the system handler, and a toast names what
opened.

Alternate-screen programs have no terminal scrollback to show. Their screen is
painted in place, not scrolled, so dm refuses to open its own scrollback for
them and tells you to use the program's. The pane daemon owns this fact: it
reports `alt` and `mouse` in every snapshot, so a client that attached late
still knows even when the enabling escape sequence has left the replay ring.

Scrollback is built on demand: the first time you scroll a pane, the client asks
the server for that pane's output ring, replays it into a tall terminal (2000
rows), and draws a window into it at the current offset. The ring is cached per
pane for that layout, so a second scroll asks for nothing. It is a frozen
snapshot taken when you scroll, so new output does not move the view. The replay
is approximate for full-screen programs; for streamed output it is exact.

Mouse reporting means normal drag no longer selects text. Hold Shift while
dragging to use the terminal's own selection.

dm selects instead. A left drag inside a pane highlights the row-major range
in reverse video, clamped to the pane it started in, and the release copies it
with OSC 52, so the text lands on the terminal's clipboard and a `copied N
chars` toast confirms it. A press and release in the same cell is a click and
still reaches the app that claims the mouse; dm only takes the drag. Trailing
spaces go and lines join with a newline. An app's own drag selection loses to
dm's while dm owns the gesture, which is why Shift exists.

The selection is anchored to the pane's content, not to the cells a frame
shows. Turning the wheel while the left button is held scrolls the pane's
scrollback and extends the selection into older rows, even when the pointer
rests past the pane's top edge. The release copies the scrolled rows. A
selection made in scrollback copies the scrollback rows, and leaving its own
pane's scrollback drops it. New output that scrolls the live screen drops a
live selection, because the text under it has moved; output that does not
scroll leaves it alone. A geometry change drops the selection. An
alternate-screen pane has no scrollback, so the wheel keeps the selection
clamped and says why.

`prefix y` copies what the focused pane produced since the last Enter you sent
it, and `dm yank <pane>` prints the same rows. The anchor is local: dm records
the output stream position when it forwards an Enter, so a remote shell needs no
setup. A pane that has never been sent an Enter has no anchor: the copy falls
back to the visible screen with a toast that says so, and `dm yank` reports that
it has nothing to cut at. `prefix Y` copies the visible screen. Both keys write
OSC 52 and raise one `copied N chars` toast.

The copy holds the command's output only. The command line dm wrote is dropped
by matching the echoed text, and the line end the pty echoes at the anchor is
dropped by the newline rule. A shell that emits OSC 133 prompt marks gets the
exact range instead: the copy starts at the command start mark and ends at the
command end mark, so a prompt that repaints its line on Enter, a trailing
prompt and the blank rows around them never reach the copy. Add this line to
`~/.zshrc` to emit the marks:

    precmd() { print -Pn '\e]133;D\e\\\e]133;A\e\\' }; preexec() { print -Pn '\e]133;C\e\\' }

The marks travel over ssh like any other output, so the hook works on the
remote host too. Without marks, the copy ends at the last row with content and
the trailing prompt is included. The marks come from the semantic prompts
proposal:
<https://gitlab.freedesktop.org/Per_Bothner/specifications/blob/master/proposals/semantic-prompts.md>.
`dm man`, `dm html` and `dm llms` carry the same section.

dm enables bracketed paste (2004) on attach and disables it again on detach, so
ctrl+shift+v inserts newlines literally. The client sends a paste to the focused
pane as one chunk, or to the selected pane while the dashboard's input mode is
on, and never reads a byte inside it as a dm key: an embedded ctrl+b does not
arm the prefix, and newlines do not submit. A paste also closes an open menu,
picker or scrollback first.

The 200~ and 201~ markers go only to a program that asked for bracketed paste.
A program that never asked, such as a line editor without 2004, gets the text
alone: it would read the markers as keys and leave their tail on the line, which
is how a pasted mysql statement showed up as `0~SET GLOBAL ...;1~`.

Small actions answer with a toast, not text in the status bar: the bar's right
block is reserved for the scroll position and the divider hint. The client
keeps up to three, each with the time it was raised, and draws them bottom
right just above the bar, newest at the bottom: one row of bright text inside a
dim border, as wide as the longest text plus its two border cells, clamped to
the screen. A repeat of the newest text within one second is the same toast,
and every toast expires after three seconds. Copying raises one, as does a
failed request, an unknown key, an empty scrollback and a pane exit.

System notifications are for the events worth interrupting for. A pane that
enters `blocked`, `done` or `error`, or that exits,
reaches the desktop through `notify-send` when the pane is not the focused pane
of an attached client, because the operator is not looking at it. A focused
pane with a client attached stays quiet, `DM_NOTIFY=0` turns notifications off,
and a host without `notify-send` gets none.

## Rendering

A frame is an ordered stack of painters. Each painter writes into the caller's
buffer. `internal/client/render_pane.go` owns the pane painters: `drawPane`,
`drawPaneFrom` and `drawPaneRow`. The shared primitives are in `internal/cells`,
`internal/segment` and `internal/panel`.

`cells` measures and cuts: cell width, clipping, padding and elision. `segment`
is a styled run: text, foreground, background, weight and the pin mark.
`panel` builds a box: `PaintPanel` clips the interior to a rect and writes the
title and hint rows, and `DrawPaneBox` paints the border around content.

On a stable frame, the diff decides what is written. A layout change writes
every band and row. `PaintPanel` holds no state and keeps no buffer between
frames. A new overlay is a painter plus a band entry.

`internal/client/damage_test.go` keeps a refactor honest: it pins the steady
frame, the one-row repaint and the band cursor moves.

## Performance

The client no longer clears the screen and repaints every cell on every frame: a
steady frame writes nothing. Every written frame is wrapped in a synchronized
update (`ESC [ ? 2026 h` to `ESC [ ? 2026 l`), so the terminal applies it in one
go. A frame that clears is a full repaint, and it happens only when the picture
is not stable: a resize, a pane added, closed or moved, a focus change, or
entering and leaving scrollback, the menu, the picker or the palette.

## RPC

Every client speaks newline-free, length-prefixed JSON over the session socket
at `$XDG_STATE_HOME/dm/sessions/<name>/server.sock`. The CLI is a thin wrapper,
so anything one CLI verb does, an agent can do with one action.

```sh
dm exec '{"action":"split","target":"w1p1","dir":"left"}'
dm exec '{"action":"focus-dir","dir":"right"}'
dm exec '{"action":"new-window","name":"tests"}'
dm rename-workspace s1 api-work
dm write-to w1p1 --msg "go test ./..." --enter
dm read w1p1 --lines 20
dm yank w1p1
dm status
```

Actions: `ping`, `list` (alias `ls`), `read`, `history`, `yank`, `replay`,
`status`,
`write-to`, `exec`, `keys`, `split`, `zoom-pane`, `set-rail`, `resize-split`,
`focus`, `focus-dir`, `next-pane`, `prev-pane`, `close-pane`, `close-window`,
`new-window`, `next-window`, `prev-window`, `select-window`, `new-workspace`,
`next-workspace`, `prev-workspace`, `list-workspaces`, `list-panes`, `pane`,
`toggle-fold`, `set-pane-name`, `focus-window`, `focus-workspace`,
`close-workspace`, `rename-window`, `rename-workspace`, and the seven box
actions `msg-post`, `msg-list`, `msg-wait`, `msg-sub`, `msg-clear`,
`msg-watchers` and `msg-schema` below.
`toggle-fold` takes a window id as `target` and replies with the new `unfolded`
state.

`history` takes `target` and an integer `lines` and replies with
`{"lines": ["..."]}`: up to `lines` text rows immediately above the live
screen, oldest first, trailing blanks trimmed. `lines` must be positive.
`dm read <pane> --scrollback N` prints the same rows.

The visible screen is a snapshot, not a replay. Each pane daemon serialises its
VT to canonical ANSI (`screen`), and the client writes that into a fresh
terminal at the pane's rectangle. An attach carries those screens and no
history. The output ring travels separately as `replay`, sent only when the
client asks as scrollback opens, and feeds only the scrollback history, so a
client never rebuilds the picture from raw output.

`dm exec` prints the response as JSON. A failed action exits non-zero.

## OSC 7501 agent status

`dm` implements the terminal side of the program status protocol
(<https://www.superlogical.com/rex/docs/build/program-status>). A program
reports on its own stdout:

```
ESC ] 7501 ; state=working:app=pi:msg=<base64> ESC \
```

- `state=` is `idle`, `working`, `blocked`, `done`, `error` or `clear`.
- `app=` is optional and display only: `dm` keeps the last value per pane and
  uses it as a rail label when the pane has no recorded name. `kind=` is
  optional and unused.
- `msg=` is optional base64 UTF-8; `dm` decodes it for `dm status` and the bar.
- `state=clear` removes the record, shown as `unknown`.
- Both `BEL` and `ST` terminate the sequence.

Programs usually probe support first with `ESC ] 7501 ; ? ESC \` and wait for
the terminal to echo the body back. `dm` answers that query through the PTY, so
pi and other conforming programs switch reporting on. The older JSON
(`{"state":"working"}`) and bare-state forms are still accepted.

`dm __probe [-hold 2s]` is a test harness that speaks the whole handshake and
prints `PROBE-REPLY=ok`, `PROBE-REPORTED`, `PROBE-CLEARED` inside a pane. Run it
from a pane to check a build:

```sh
dm split w1p1 --dir down
dm write-to w1p2 --msg "$PWD/dm __probe --hold 5s" --enter
dm status
```

## Message box

One session has one message box: an append-only log that any pane can post
into and any caller can wait on. It is where a lane reports `blocked` and a
driver waits for `done`, with no screen polling.

An entry is
`{"seq":7,"at":"2026-11-03T14:40:12Z","from":"w3p1","key":"lane/a","state":"done","msg":"gate green"}`.
Every field is always present.

- `seq` starts at 1, only grows, and never repeats. A clear does not reset it.
- `at` is the post time, truncated to the second, in UTC.
- `from` is the posting pane's id. It is set when the poster runs in a pane of
  the target session, may be empty, and must name a pane in the session tree.
- `key` groups entries and may be empty. The cap is 128 bytes. An empty key
  on list, wait and clear means the whole log.
- `state` is one of five. `note` carries no progress meaning, `working` means
  the work is running, `blocked` means it waits on someone else, `done` means it
  finished well, and `error` means it finished badly. `clear` is an OSC 7501
  pane status, not a box state, and post rejects it.
- `msg` is the text. The cap is 2000 bytes.

Key and message must be valid UTF-8 with no control characters, because an
entry prints on one row.

The box is bounded at 1000 entries and 256 KiB. Past either bound the oldest
entries go. The log lives in `state.json`, so a restart keeps every entry, its
order and its `seq`.

Seven actions. Each takes its request in the `res` field of the envelope.

| action | request | reply |
| --- | --- | --- |
| `msg-post` | `key`, `state`, `msg`, `from` | `{"entry":{...}}` |
| `msg-list` | `key` or `keyPrefix`, `afterSeq`, `limit`, `from` | `{"entries":[...],"trimmed":false}`, newest first |
| `msg-wait` | `key` or `keyPrefix`, `state`, `afterSeq`, `timeoutMs`, `from` | `{"entry":{...},"trimmed":false}` |
| `msg-sub` | `key` or `keyPrefix`, `state`, `afterSeq`, `from` | one `{"entry":{...},"trimmed":false}` frame per matching entry, until the connection ends |
| `msg-clear` | `key` | `{"cleared":N}` |
| `msg-watchers` | none | `{"watchers":[...]}` |
| `msg-schema` | none | `{"schema":{...}}` |

`state` is required on post and optional on wait, where an empty state asks for
any state. `timeoutMs` is required on wait. `limit` clamps to the box, and zero
or less means the whole box. `timeoutMs` runs from 1 to 3600000. `key` and
`keyPrefix` are exclusive: a read names an exact topic or a branch, never both.
`afterSeq` takes 0 or more, and leaving it out is not the same as 0. An unknown
field, an unknown action, an unknown state or a `from` outside the tree fails
the request, so nothing is silently ignored. `msg-schema` serves these shapes as
JSON Schema, built from the Go types and the one state enum.

A `msg-sub` reply is a stream. The server sends one frame per matching entry
and no completion reply, so the connection stays open until either side drops
it. A parse error or a bad `from` still arrives as a normal reply with an
error.

On both reads `from` is the reading pane. An empty `from` records no watch, and
a `from` outside the tree fails the request.

Every list from a pane and every wait from a pane leaves a watch: the pane, its
filter and the highest seq it served. The registry lives in server memory only.
It has no `state.json` field and no save, so a restart begins empty and the
reads rebuild it. The server drops a watch when its pane leaves the tree, and
drops an unparked watch after 10 minutes without a fresh read. A parked watch
never ages out. A watch that appears, advances its cursor, changes its parked
state or drops pushes `{"type":"watch"}` to every attached client.

A pane P received entry E when a watch for P holds `cursor >= E.seq` and its
filter matches `E.key`. The server stores no delivery state, so that comparison
is the whole test. A watch is a read left behind, not a pending message. An ack
is still a post: a reader that wants to record handling posts, or reads again to
advance its cursor.

The CLI:

```sh
dm msg post --state blocked --key lane/a --msg "ready for review"
dm msg list [--key lane/a | --prefix lane/] [--after SEQ] [--limit 20] [--json]
dm msg wait [--key lane/a | --prefix lane/] [--state done] [--after SEQ] [--timeout 5m]
dm msg sub [--key K | --prefix P] [--state S] [--after N] [--json]
dm msg clear [--key lane/a]
dm msg watchers [--json]
dm msg schema
```

`post` prints the stored entry as one row. `list` prints newest first as
`SEQ KEY STATE FROM MSG` and cuts only the message to the terminal width;
`--json` prints the reply unchanged. `wait` prints the entry it found, or the
reply on `--json`. `sub` prints each frame as one row. With `--json` each frame
prints as its own JSON object on one line, so the output is NDJSON and an agent
spawns the sub once and reads lines. A server restart does not end the sub: it
redials with backoff and resumes at the last printed entry. A refused request
prints the error and exits non-zero. `clear` prints the count. `watchers` prints
one row per watch as `PANE FILTER CURSOR WAIT`: the filter is the key or the
prefix with a trailing star, and the wait mark is `wait` while a wait is parked
and `-` otherwise; `--json` prints the reply unchanged. `schema` prints the
schema, so `dm msg schema | jq .actions` reads. `list` and `wait` send `from`
for you when they run inside a pane, so a read in a pane records a watch. A
read from outside a pane sends no `from` and records nothing. The wait timeout
defaults to 5 minutes and takes any duration up to one hour. `--after` carries the cursor.
Leave it off to read the status board; `--after 0` starts at the first entry.

Without `afterSeq` a wait matches the newest entry under its topic, not any
entry. It returns at once
when that entry already carries the wanted state. Otherwise it parks until a
post changes the newest entry for the key, until a clear exposes an older one,
or until the timeout. Only the newest entry counts, so a newer `blocked`
outranks an older `done` on the same key. A timeout replies with
`box: wait timed out` plus the newest entry it saw, and exits 1.

A lane and a driver, in one session:

```sh
# in the lane's shell, a pane of the session
dm msg post --key lane/a --state blocked --msg "ready for review"

# in the driver: parks until the lane reports done
dm msg wait --key lane/a --state done --timeout 10m

# in the lane's shell, after the gate
dm msg post --key lane/a --state done --msg "gate green"
```

The wait returns the done entry:

```
$ dm msg wait --key lane/a --state done --timeout 10m
2 lane/a done w2p1 gate green
```

The box is not a queue. Nothing is consumed: a list, a wait and a repeat of
either read the same entries, and every reader sees the whole log for its
key. A clear is the only thing that removes entries, apart from the caps
dropping the oldest at the bound, and a clear never resets `seq`.

## Topics and subscriptions

The key is a topic, and it is a flat string, so a publisher names a branch:
`msg/broadcast`, `reply/w1p1`. A reader names an exact topic with `key` or a
branch with `keyPrefix`.

Two read shapes come from the one log.

- Without `afterSeq` a wait reads a status board. The newest entry under the
  topic is the state of the world, and only it counts, as described above.
- With `afterSeq` a wait reads a stream. It returns the oldest entry past the
  cursor that matches the topic and, when a state is named, that state. Nothing
  already handled comes back and nothing unseen is skipped. The cursor is the
  `seq` of the last entry the reader handled. The reader keeps it, so the
  server holds no subscription state.

`trimmed` on a list or wait reply says the cursor sits behind the oldest row
the box still keeps, so the entries in between are gone. A subscriber that sees
it has missed messages and should resynchronise.

An orchestrator and three workers:

```sh
# orchestrator, pane w1p0: one broadcast, no per-worker copy
dm msg post --key msg/broadcast --state note --msg "read tasklist.md"

# worker w1p1 follows the branch and keeps its own cursor
c=$(cat .cursor 2>/dev/null || echo 0)
entry=$(dm msg wait --prefix msg/ --after "$c" --timeout 1h --json)
jq -r '.entry.msg' <<<"$entry"
jq -r '.entry.seq' <<<"$entry" > .cursor

# the worker reports on its own reply topic
dm msg post --key reply/w1p1 --state done --msg "backend done"

# the orchestrator reads the reports in order, from its own cursor
dm msg wait --prefix reply/ --after "$(cat .orch.cursor)" --state done --timeout 1h
```

Every reader keeps its own cursor, so two orchestrators both see every report.
A post wakes every parked wait, and each wait re-reads the log from its own
cursor. The cursor lives with the reader: a restarted subscriber resumes where
its cursor file says.

Conventions the box does not enforce, and the ones to keep:

- Post a pointer, not a document. `read tasklist.md`, not the tasklist. An
  entry stays one row and the structured data stays in a file, passed once.
- A worker posts `working` before it starts, so a replayed cursor shows the
  work already began.
- Liveness is the wait timeout. A dead pane posts nothing, so the orchestrator
  treats a timeout as a lost worker.

`prefix m` floats the box over the panes. One row per entry, newest first, as
`SEQ STATE KEY FROM MSG`, with the state in the colour the rail gives that
status. `j`, `k` and the arrows scroll one row and the wheel scrolls three;
`g` and `G` jump to the newest and the oldest row. The box grows with the log
up to 22 rows and then scrolls, and its title names the rows in view. The title
also counts the parked watches as `N watching` and the entries that arrived
above a scrolled-back window as `+N`, and omits each part at zero. At 60 columns
or wider each entry row starts with a two-cell count of the watches that were
served that entry; a narrower view drops the marker whole, so the message keeps
its cells. `w` swaps the entry list for the watcher page: one row per watch as
`PANE FILTER @CURSOR`, with a `wait` mark while a wait is parked. A live `sub`
shows as parked too. A window the reader scrolled back stays on its top entry
as new entries arrive, so the text does not slide. A post or a clear anywhere
in the session refreshes an open view, and a change in the watch set pushes
`{"type":"watch"}`, which asks for the list again at most once per 100 ms. The
view follows a lane without a keypress. `esc`, `q` or the prefix key closes it.
It posts nothing and clears nothing.

## Subscriptions

A subscription is a stream, not a poll. `msg-sub` holds one connection open and
the server pushes each matching entry as a frame. The watch is parked for its
life, so a view shows a live sub as a reader that waits. The cursor advances
over every entry it scans, including the ones its state filter skips. A gap in
the log is reported once, on the first frame of the stream. A slow reader can
miss frames: a frame that cannot enter the client's queue ends the connection
instead of blocking the server. Reconnecting resumes at the last printed entry.

## Durability

Two processes per session:

```
dm __server   owns layout, focus, clients; persists state.json on every change
dm __pane     one per pane; owns the PTY, VT screen, status and a fixed 512 KiB ring
```

Each pane daemon has its own unix socket and is started with `setsid`, so it
survives the server. The server, on start, reads `state.json` and **adopts** the
still-running pane daemons instead of respawning them.

A pane daemon watches its own socket path. When the path is gone, or names a
different file, no client can reach that daemon, so it stops its shell and
exits. An intact path is the normal state while the server restarts, so the
daemon stays.

A pane daemon holds a fixed 512 KiB output ring, allocated once, and runs with
a 32 MiB memory ceiling. A pane is still its own Go process, and that process is
the floor: the ceiling bounds a burst, it does not lower the floor.

- Kill the client: nothing is lost, reattach.
- More than one client may attach. They share one layout: the largest attached
  terminal sets the size, and when it detaches the next largest takes over, so
  a small client cannot shrink everyone. A smaller client shows the layout
  clipped to its own screen.
- A pane exports `DM_SESSION` to its shell. `dm` refuses to attach that same
  session from inside one of its own panes, because the mirror is useless and
  competes for the shared size. Use `-s` for another session.
- Kill the server (`kill -9`, crash, logout): panes keep running, including
  their shells, because their sockets stay intact. The next CLI call or attach
  restarts the server and reconnects the same pane processes. Content and
  status are still there.
- A pane whose shell exits is removed from the layout and its split collapses.
  Focus moves to a live neighbour.
- The last pane of a window closes the window. The last window ends the session:
  the server removes its state directory and every attached client returns to
  its shell. This is the tmux behaviour, not a respawn.
- Reboot: pane processes are gone, but layout, focus, window names and cwd are
  in `state.json`. The server spawns fresh shells in the same places and marks
  them `restart: true`.

State lives in `$DM_STATE`, else `$XDG_STATE_HOME/dm`, else
`~/.local/state/dm`.

```
sessions/<name>/state.json           layout, focus, names, cwd, the box
sessions/<name>/server.sock          RPC and client socket
sessions/<name>/server.pid           the server process id
sessions/<name>/server.log           server stderr
sessions/<name>/panes/<id>.sock      one per pane
sessions/<name>/panes/<id>.log       pane daemon stderr
sessions/<name>/panes/<id>.sock.pid  the pane process id
```

## Tests

```sh
go test ./...
```

The integration tests in `blackbox/` run the built binary against a temp state
dir. They cover the pane-survives-server-kill path, split/close layout, zoom and
rail resize, window and workspace switching, the JSON action path, OSC 7501
status, the message box CLI and desktop notifications. Unit tests hold each
generated document to its renderer: `docs/dm.1` and `docs/llms.txt` fail the
gate when stale.
