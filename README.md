# dm

dm is a terminal multiplexer whose panes outlive the server. Read this page to
install dm, drive a session, and find the command for each surface.

It runs several terminal programs in one window, keeps them running when your
client quits, and brings them back after a reboot. It is built for coding
agents: a pane reports its state, asks the reader a question, and posts to a
message log. Every surface drives one JSON action set, so a script or an agent
works the screen you work.

## Install

Download the tarball for your platform from the latest release:
`dm_linux_amd64.tar.gz`, `dm_linux_arm64.tar.gz`, `dm_darwin_amd64.tar.gz` or
`dm_darwin_arm64.tar.gz`. Untar it and put `dm` on your `PATH`. If you use pi,
the agent harness, run `dm provision` once to link its extension and the skill
into `~/.pi/agent`. Each release also carries `checksums.txt`, so `sha256sum -c`
proves the tarball before you install it.

This file is also published, with a sidebar and search, at
https://darkmage.dev/dm/. `PLUGINS.md` is the plugin contract by hand: the
declaration, the environment, the message log topics and the example under
`examples/`.

## Updating

An attach tells you when a newer release is out, twice: one line before the
session draws, and a toast in the session itself, both reading
`dm 1.4.0 is out, run dm upgrade`. `dm upgrade` installs the latest release over
the binary you ran, after checking the tarball's sha256, and `dm upgrade --check`
only reports. A session that is already running keeps the old binary until it
restarts, so upgrade at a moment you can detach. Set `DM_NO_UPDATE_CHECK=1` to
stop the check. It also stays quiet on CI and in a build that carries no version
string. A `dev` build has no version to compare, so `dm upgrade` treats it as
older than any release and installs the latest one; `--force` reinstalls when you
already run the latest.

## Start here

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
dm web                  # serve the session to a browser; see Web below
```

`ctrl+b` arms the prefix and shows the prefix help box at the bottom right. The
box stays until an action runs or `esc` dismisses it. It sizes to its widest row,
its top border carries `prefix`, and a narrow screen drops the action column
before it cuts a key.

| key | action |
| --- | --- |
| `ctrl+b` | prefix (press it twice to send a literal `ctrl+b`) |
| `\|` / `-` / `_` | split right / down |
| `<` / `^` | split left / up |
| `[` | scrollback (also the mouse wheel) |
| `[` then `/` | search the scrollback, live screen included |
| `[` then `n` / `N` | older / newer search match, wrapping |
| `y` / `Y` | copy the focused pane's output / screen |
| `i` | copy the focused pane's id |
| `h j k l` or arrows | focus pane left / down / up / right |
| `n` / `p` | next / previous pane |
| `x` | close pane |
| `X` | close window |
| `f` | shelve the focused pane |
| `F` | restore the shelf as a floating pane, or put it away |
| `G` | pop the active shelf member back into the layout |
| `z` | zoom pane |
| `c` | new window |
| `w` / `W` | next / previous window |
| `o` / `O` | swap with the next / previous pane |
| `u` | cycle the window to the next preset layout |
| `L` | layout palette over the window's panes |
| `0-9` | select window |
| `t` | new workspace |
| `s` | switch session (picker: j/k, enter, esc) |
| `K` | command palette: type to filter every action, enter runs it, esc closes, ? explains the selection |
| `S` | synchronize panes |
| `ctrl+g` | toggle secret redaction; it is not a security control |
| `a` | dashboard: every pane in a full-screen table |
| `m` | message log: the session's entries as a floating pane |
| `N` | message log opened on the `notify/` entries, the notification filter on |
| `A` | show or hide the question panel, or reach the oldest one waiting elsewhere |
| `?` | all keymaps, in a floating pane |
| `)` | next workspace |
| `r` | rename the active workspace |
| `P` | set the active workspace's project, or clear it |
| `b` | cycle the sidebar: full, thin, hidden |
| `ctrl+r` | reload config.kdl and its theme |
| `d` / `q` | detach |
| `esc` | dismiss the prefix help box |

The status bar reads back three states you look for: `PREFIX ` while the prefix
is armed, `SCROLL <pane> +<offset>/<max>` while you scroll, and
` SEARCH <query> <i>/<n>` on an accepted query.

## Demos

Two recordings show the basics. A player opens on a still frame, so press play
once.

<div data-cast="docs/casts/split.cast"></div>

`ctrl+b` arms the prefix, and the box lists every key. `|` splits right, `h`
moves focus left, and `-` splits the left pane down. Every pane keeps its own
shell.

<div data-cast="docs/casts/project.cast" data-poster="npt:0:09"></div>

`ctrl+b P` opens the project prompt on the active workspace, and the sidebar
regroups that workspace under its project. `dm ls` prints the same project, so
the name lives in the session and not on the screen.

`scripts/casts/record.sh` records both casts against a scratch state and
config. The page plays whatever that script last wrote.

## Sessions and panes

Ids are addressable, not opaque counters behind a random string:

```
s1        workspace 1
w1        window 1
w2p3      window 2, pane 3
```

A pane id is its window id plus `p` and a per-window sequence that only grows.
The id tells you where the pane lives and can be typed without a lookup. A
window id and a workspace id are per session.

Counters only move forward, so an id is never reused after a close. A split
gives the new pane the next number in its window, and the existing pane keeps
its id. A window's name, a workspace's name and a pane's recorded agent name are
cosmetic labels that stay separate from the id and stay editable.

`d` or `q` detaches and loses nothing. The session, its panes, their processes
and their scrollback stay, and a reattach draws the screen as you left it. More
than one client may attach. They share one layout: the largest terminal
sets the size, and the next largest takes over when it detaches.

A pane exports `DM_SESSION` to its shell. `dm` refuses to attach that same
session from inside one of its own panes. `-s` attaches another session, but
only a live one.

The last pane of a window closes the window. The last window ends the session:
the server removes its state directory, and every attached client returns to
its shell.

### The sidebar

The left columns show a sidebar when the session asks for one. It is a tree: a
project header, then its workspaces, then each workspace's windows, then each
window's panes. Workspaces that share a project name form one block, and the
project name takes one of six group colours. An open pane row leads with an
icon, then its recorded name, the `app=` it last reported and the pane id, with
the state glyph in the last content cell in that state's colour. A pane with an
open question carries a `?` badge, and the shelf adds a collapsible section
after the last workspace.

A left press focuses that row's pane, and a pane in another window switches the
active window too. A press on the arrow toggles that collapsed group, and a
double-click opens the inline rename prompt. Right click opens the row menu:
rename, close, new, fold and focus.

The sidebar is 34 columns by default, at most a third of the screen and never
wider than 46 columns; `set-rail` changes it. The thin sidebar is six columns: a
drag strip and five content cells, which are the project bar, the agent mark,
one git cell, the state glyph and the question badge. It shows no names.
`prefix b` cycles full, thin and hidden.

## Scrollback and mouse

Mouse reporting is always on, and disabled again on detach. Left click focuses
the pane under the pointer, the wheel scrolls that pane, and dragging a divider
resizes the split. Both sides of a drag keep at least three content cells, and
hovering a divider shows `drag to resize` in the status bar.

Right click opens the pane menu, even over a program that owns the mouse: it is
a window-manager gesture, not the app's. Move with `j`/`k` or the arrows, and
`enter` or a digit runs the row. The menu offers split right/down/left/up, zoom,
focus, close pane, scrollback, new window, new workspace, rename workspace,
switch session and detach. A program that enabled mouse reporting itself keeps
the event instead, which is how `pi`, `htop` and `vim` keep their scrolling.

Ctrl+click on a URL opens it with the system handler. Alternate-screen programs
have no terminal scrollback to show, so dm refuses to open its own scrollback
for them and tells you to use the program's. Scrollback is built on demand: the
first scroll asks the server for that pane's scrollback buffer, replays it into
a tall terminal, and draws a window into it at the current offset. The tall
terminal is `history-lines` rows plus the pane's own height. The buffer is a
frozen snapshot, so new output does not move the view. The replay is approximate
for full-screen programs; for streamed output it is exact.

The window is a row count and the scrollback buffer is a byte budget, so a wide
pane holds fewer rows than `history-lines` and a narrow one holds more.

### Scrollback search

`/` opens a query row above the status bar. It takes every key: printable bytes
append, backspace deletes one character, `ctrl+u` clears the line, `enter`
accepts and `esc` cancels. No byte reaches the pane while the row is open. A
match is a literal substring, case-insensitive, and there is no regex. The
search reads the tall terminal's rows, so the live screen is part of it.

On accept, the view jumps to the nearest match at or above the top of the
screen, and that row lands at the top. `n` moves to the next older match and `N`
to the next newer one; both wrap. A query with no match raises `no match: <q>`.
With an accepted query, the first `esc` clears the search and keeps scrollback
open, and the next `esc` leaves.

## Copy and paste

### Drag to copy

dm turns mouse reporting on at attach, so every drag on a pane reaches dm and
never the terminal. A left press on pane content starts a selection. The drag
highlights the row-major range. The release copies it and raises a
`copied N chars` toast. The range stops at the pane the drag started in.

A press and release in the same cell stays a click. An app that claims the
mouse gets that click. A wheel turn during the drag scrolls that pane's
scrollback three rows a notch, so one drag reaches history. A pane on the
alternate screen keeps no history, and dm says so in a toast.

The copy is the rows the pane drew, not a logical line. A wrapped line arrives
in pieces, one per row. A two-column cell arrives once. Trailing spaces are
dropped. A geometry change drops the selection, and so does output that scrolls
the live screen. A selection in frozen scrollback holds until the pane leaves
scrollback.

dm claims the drag first, so the terminal's own selection needs its bypass
modifier. Hold Shift on most terminals and Option on iTerm2. When neither
works, the terminal's own mouse preference names the key.

### prefix keys

`prefix i` copies the focused pane's id, the handle every other dm surface names
a pane by. `prefix y` copies what the focused pane produced since the last Enter
you sent it, and `dm yank <pane>` prints the same rows. dm records the output
stream position when it forwards an Enter, so a remote shell needs no setup. A
pane that has never been sent an Enter falls back to the visible screen, with a
toast that says so. `prefix Y` copies the visible screen.

### The shell marks

A shell that emits OSC 133 prompt marks gives `prefix y` both edges exactly:
the command start mark and the command end mark. Without them the copy starts
after the echoed command line. It ends at the last row with content, so the
trailing prompt comes along. Add this line to `~/.zshrc`, above the line that
loads your prompt theme. The theme registers its own `precmd` hook, and the last
one wins:

    precmd() { print -Pn '\e]133;D\e\\\e]133;A\e\\' }; preexec() { print -Pn '\e]133;C\e\\' }

The marks are ordinary output, so they travel over ssh and a nested multiplexer
like any other bytes. A shell on a remote host needs the same line in its own
start file. A pane that runs a program instead of a shell has no prompt and no
marks.

### The clipboard

A copy writes OSC 52. dm probes the terminal once per attach with an OSC 52
read. A terminal silent for 300 ms gets the copy piped to `pbcopy`, `wl-copy` or
`xclip` instead. dm raises the toast only when the text landed. When the host
carries no clipboard tool, the toast names one to install.

dm enables bracketed paste on attach. A paste inserts newlines literally, and no
byte inside it is read as a dm key.

Copying raises a toast, as does a failed request, an unknown key, an empty
scrollback and a pane exit. The client keeps up to three toasts above the bar,
and each expires after three seconds.

### iTerm2 settings

iTerm2 drops OSC 52 writes until Preferences → General → Selection →
Applications in terminal may access clipboard is on. A dropped write looks the
same as a taken one, so turn that setting on before you depend on a copy there.
When dm's read probe goes unanswered, dm pipes the copy to `pbcopy` instead, and
that copy lands.

iTerm2 pastes with Command+V, and dm reads it as one bracketed paste. Its own
text selection needs Option (⌥) held, not Shift. iTerm2's shell integration
writes the same OSC 133 marks, so a pane that runs it gives `prefix y` both
edges without the line above.

## Windows and workspaces

`prefix c` adds a window to the active workspace, `prefix t` adds a workspace,
and `prefix 0-9`, `w` and `W` move between windows. `prefix X` closes a window.

A pane keeps the server told where its shell is, so a `cd` moves the folder the
sidebar shows and the folder the next pane, window or workspace opens in. A new
window is named after the folder it starts in, and `rename-window` overrides the
name. `prefix r` opens the rename prompt as a line above the status bar,
starting from the workspace's current name, so typing appends to it. Backspace
deletes one character and `ctrl+u` clears the line. `enter` commits, `esc`
cancels.

`prefix z` zooms the focused pane: it fills the window's pane area, the sidebar
and the status bar stay in place, and the program inside reflows to that size.
`prefix z` again restores the splits. The zoom clears when the focus, the window
or the workspace changes, and dm does not persist it.

A binding comes from the built-in table, so a `config.kdl` that leaves out
`zoom-pane` still gets `z`. Bind it to `none` to drop it, and `prefix z` then
toasts `no binding`.

## Window tab bar

One row sits above the pane area, to the right of the sidebar, and it is always
visible. One tab per window in the active workspace, in window order, with the
active tab highlighted. Clicking a tab selects that window, and double-clicking
one opens the inline rename prompt for it. The server reserves the tab bar row
when it computes pane rectangles, so pane frames start below it.

A tab is its label plus one padding cell on each side, clamped by two bounds in
`config.kdl`:

```kdl
tab-min-width 8
tab-max-width 20
```

Both are cell counts and default to the values above. A label wider than the
maximum is cut with `…`, a minimum below 3 is an error, and so is a maximum
below the minimum.

The label template is a Go `text/template`. It applies to the tab bar, the
sidebar window row and the status bar window chip, so one window reads the same
in every place. The data is a struct with these fields:

- `.ID`: the window id, e.g. `w1`.
- `.Name`: the window name, the label `rename-window` edits.
- `.Named`: true when the user set the name with `rename-window`.
- `.Cwd`: the source pane's working directory.
- `.Root`: the source pane's repository root, empty outside git.

The `base` function prints the last path element, so `{{base .Root}}` is the
repository folder name. The default template shows the name of a named window
and the id plus the repository folder of an unnamed one:

```kdl
window-label "{{if .Named}}{{.Name}}{{else}}{{.ID}}{{with base .Root}} {{.}}{{end}}{{end}}"
```

An unnamed window in `.../dm` reads `w1 dm`, and a window renamed to `api` reads
`api`. A template that does not parse, or names an unknown field, is an error.

## Layouts

A layout is a named JSON document that holds a session's shape: its workspaces,
their windows, the split trees, and each pane's launch cwd and argv. Files live
at `$XDG_CONFIG_HOME/dm/layouts/<name>.json`, or
`$HOME/.config/dm/layouts/<name>.json` when `XDG_CONFIG_HOME` is unset. The name
is letters, digits, `_` or `-`, the same rule as a session name.

```sh
dm layout save work -s go-backend   # capture the live shape under the name work
dm -L work                          # start the session "work" from the file
dm -L work -s other --force         # reset "other" and replay the file over it
dm layout ls                        # files with their workspace, window and pane counts
dm layout show work                 # the file unchanged
dm layout rm work                   # delete it
```

`dm -L` seeds an empty session and then attaches, so the first snapshot is the
whole layout. An existing session wins: a layout never rearranges a live one,
and `--force` is the only way to replay a file over a session that has state.
The session defaults to the layout name, so the same command twice reattaches.
A malformed file fails at the CLI, naming the file and the field.

One file: two panes in a column, the first running pi in the project.

```json
{
  "version": 1,
  "name": "go-backend",
  "workspaces": [{
    "name": "development",
    "project": "go-backend",
    "active": 0,
    "windows": [{
      "name": "api",
      "focus": "w1p1",
      "root": {
        "dir": "col",
        "sizes": [0.6, 0.4],
        "kids": [{"pane": "w1p1"}, {"pane": "w1p2"}]
      }
    }]
  }],
  "panes": {
    "w1p1": {"cwd": "/home/you/src/go-backend", "argv": ["pi"]},
    "w1p2": {"cwd": "/home/you/src/go-backend"}
  }
}
```

Pane ids inside a file are local to it, so the same file can seed any session.
Only the launch argv is durable, so a pane that began as a shell and now runs pi
reopens as a shell. The message log, the secrets, the agent state and the
sidebar width never travel.

## Layout palette

`prefix L` opens the layout palette: a panel centred over the live panes. Five
rows name the presets in cycle order (even-horizontal, even-vertical,
main-horizontal, main-vertical, tiled) and a last row names the window's custom
tree. The row matching the window's stored layout name carries a `now` mark; an
unnamed tree marks the custom row. `j` / `k` or the arrows move the selection and
wrap, `enter` picks, `q` and `esc` close, and the prefix key closes too. All of
it lives in the `layout` keymap mode, so `config.kdl` can patch it. The panel
previews the row the selection sits on, and the focused pane's border carries
the group colour. On a window with fewer than two panes
every preset row dims, the footer reads "one pane: every layout is this", and
picking a preset does nothing. Picking a preset sends `select-layout` with its
name; the custom row closes with nothing sent.

## Dashboard

`prefix a` opens the dashboard: a table of every pane in the session, one row
per pane, with no child process and no PTY. `dashboard-layout "full"` draws it
over the whole screen instead of a centred palette. The default order is recent,
the newest agent status change first, and `s` saves the mode it lands on in
`prefs.json` under the state directory.

The columns are the jump key, the project, the agent label, git, the status, the
time since the last status change and the OSC window title. A pane that never
reported a state reads as a shell, a pane with an open question leads its agent
cell with `?`, and shelved panes sit after every live pane. The digits pick: `1`
to `9` select that row and go to its pane. `j` and `k`, the arrows, or ctrl+n
and ctrl+p move the selection, and enter goes to the selected pane. The prefix
key again, ctrl+c or `esc` closes without picking. It takes no mouse input.

`/` filters, and the query row then takes every printable key, so a search holds
`d`, `s`, `i` and the digits without an action firing mid word. The query
matches a name, the app, the title, the branch, the worktree, the folder or the
project. In filter mode `esc` unwinds: the first press clears the query, the next
leaves the mode, and only the next one closes.

`s` cycles the ordering: recent, then project, then status. Status puts the
states that want attention first: blocked, error, working, done, idle, unknown,
then a shell. A preview strip under the table previews the selected pane's live
screen, and `dashboard-peek "right"` puts it beside the table instead. ctrl+u and
ctrl+d scroll it, `+` and `-` resize it, and `p` hides it. A floating pane under
10 rows or 60 columns drops the strip whatever the toggle says.

`i` copies the selected pane's id and leaves the dashboard up. `d` runs the diff
command, `hunk diff` by default, in a floating terminal in the selected pane's
folder, through `sh -c`. The floating pane takes every key while the command
runs, except the prefix: one press of it kills the pane and the command under it.
`D` runs the command in a split off the selected pane instead, and while a diff
pane is up `D` is the split chord.

The dashboard is client owned, so it never survives a detach. `dm dash` prints
the pane rows outside the TUI, and `dm peek <id>` prints one screen.

## Shelved panes

`prefix f` takes the focused pane off the grid and puts it on the shelf. The
pane keeps running: its process, its scrollback, its status and its id are
untouched, and it survives a detach and a server restart. It has no rectangle
and belongs to no window. `prefix F` restores the shelf as a floating pane, and
a second press puts it away. The floating pane carries a strip of one chip per
shelved pane, its state glyph and its name, with the active member's screen
under it. Keys go to the active member. The prefix is the only key the shelf
keeps, so `prefix n` and `prefix p` move between members and `prefix G` pops the
active member back into the layout. The mouse works inside the float: a chip
selects its member, a drag over the screen copies the selected text, a right
press opens the shelf's own menu, and a press anywhere else puts the shelf away
without touching the panes.

`prefix G` pops the active member: the pane returns to the split tree as a split
off the focused pane, and it takes the focus. The shelf keeps its ids, so a
shelved pane still answers `dm read`, `dm status` and `dm keys`, and `dm list`
prints it with `WS shelf`.

The shelf floats as a tray over the pane area, centred, at `shelf-size` percent
of it. `shelf-layout "pane"` lays it over the focused pane instead, inset one
cell so its border lands on the pane's own frame. The shelf is one list for the
whole session, and the server owns the active member.

A window never holds zero panes. Shelving the last pane of a window is refused,
and so is closing the last tree pane while the shelf holds one: pop a member
first. A pane that exits as the last pane of its window promotes the active shelf
member into that window instead.

## Config

One file holds the config. dm reads `$HOME/.config/dm/config.kdl`, or
`$XDG_CONFIG_HOME/dm/config.kdl` when `XDG_CONFIG_HOME` is set. On first run dm
writes that file with the built-in values, then you edit it. Every node is
optional and each one overrides its built-in value.

| node | value | default | does |
| --- | --- | --- | --- |
| `prefix` | one key sequence | `"ctrl+b"` | Set the prefix key that opens the main keymap. |
| `default-fold` | `"fold"` or `"unfold"` | `"unfold"` | Set whether a new window's sidebar starts collapsed or expanded. |
| `tab-min-width` | an integer of at least 3 cells | `8` | Set the smallest tab width in the window tab bar. |
| `tab-max-width` | an integer of at least 3 cells, not below `tab-min-width` | `20` | Set the largest tab width before the label is cut. |
| `padding` | an integer from 0 to 4 cells | `0` | Set the blank cells between a pane's border and its content. A change resizes every pane on reload. |
| `history-lines` | an integer from 100 to 32768 lines | `10000` | Set the scrollback window in rows and the buffer budget: about 256 bytes per line, at least 512 KiB and at most 8 MiB. |
| `glyphs` | `"auto"`, `"ascii"`, `"unicode"` or `"nerd"` | `"auto"` | Choose the glyph vocabulary the chrome draws with. |
| `window-label` | a quoted Go template | the id and folder template | Set the template that renders a window label. Window tab bar carries the default and its data. |
| `dashboard-layout` | `"full"` or `"float"` | `"float"` | Draw the dashboard over the whole screen, or as a palette centred over the panes. A floating pane leaves the panes live under it. |
| `dashboard-peek` | `"right"` or `"down"` | `"down"` | Set where the dashboard preview strip sits: under the table, or beside it on the right. The peek key still shows and hides the strip. |
| `shelf-layout` | `"centered"` or `"pane"` | `"centered"` | Place the restored shelf as a centred tray over the pane area, or over the focused pane. |
| `shelf-size` | an integer from 20 to 100 | `80` | Set the share of the pane area a centred shelf takes, in percent. The strip and at least three rows of screen are always kept. |
| `float-shadow` | `"off"` or `"on"` | `"off"` | Set the drop shadow the floating panes paint beside and under their border: the prefix help box, the rename prompt and every floating pane. |
| `dashboard-preview` | an integer from 0 to 100 | `40` | Set the share of the dashboard body the preview strip opens at. Zero opens on the table alone; the peek key still shows the strip, and `+`/`-` resize it. |
| `palette-rows` | an integer of at least 1 | `10` | Set how many entries the command palette shows before it scrolls. A screen too small for the cap holds fewer. |
| `ask-rows` | an integer from 8 to 64 | `30` | Set the fewest interior rows the question panel reserves. Its detail pane fills the rows the floor leaves. |
| `ask-cols` | an integer from 40 to 240 | `96` | Set the widest the question panel may stand, in columns. It still keeps six columns clear of the screen edge. |
| `ask-split` | an integer from 20 to 80 | `60` | Set the share of the question panel's width the detail column takes, in percent. |
| `diff-command` | a quoted shell command | `"hunk diff"` | Set the command the dashboard diff key runs in a floating pane, or in a split pane with the dashboard's diff-split key. It runs through `sh -c` in the selected pane's folder. |
| `web-addr` | a quoted host:port, as in `"you.ts.net:8099"` | `"127.0.0.1:8099"` | Set the host:port `dm web` listens on when `--addr` is not passed. An absent or empty node keeps the loopback default. |
| `web-token` | a quoted token of letters, digits, underscore or hyphen | none | Set the token `dm web` serves when `--token` is not passed. A pinned token never changes, so a bookmarked URL and an installed phone app keep working across restarts, new session directories and a moved state tree. An absent or empty node lets the session's `web.token` decide. |
| `theme` | a quoted theme name matching `[A-Za-z0-9_-]+` | none | Select a theme from the themes directory beside `config.kdl`. No node keeps the built-in theme. |
| `keymap` | a quoted mode name and a `{ "action" "key" }` block | the built-in table | Patch one keymap mode; an action the file leaves out keeps its built-in key. |
| `command` | a quoted name and a `{ run "..." }` block | none | Define a command a keymap can run; bind `command:<name>` to a key, and the row runs the run text in a floating pane. |
| `plugin` | a quoted name matching `[A-Za-z0-9_-]+` and a `{ exec ... where ... }` block | none | Declare a plugin a keymap can run; bind `plugin:<name>` to a key. The contract is in `PLUGINS.md`. |

### Commands

A command is a named shell text the config carries. Define it at the top level,
then bind `command:<name>` to a key. The key opens a floating pane on the
focused pane's folder, runs the run text through `sh -c`, and titles the panel
with the name:

```kdl
command "tests" {
    run "go test ./..."
}

keymap "main" {
    "command:tests" "t" help "Run"
}
```

The name is letters, digits, `_` and `-`, 1 to 32 bytes. `run` is required, one
quoted string, non-empty. Each of these is a config error that names its line: a
duplicate command name, a second `run` in one block, a missing or empty `run`,
an unknown key inside the block, and a name that collides with a built-in
action. A binding to a name the file does not define is an error too.

### Config reload

`ctrl+r` re-reads `config.kdl` and the theme file it names, and applies every
node. It also asks the server to re-read the same file, so the server's layout
follows `padding` and `default-fold` too. A failed read on either side keeps the
previous values. A padding change resizes every pane, so a reflow follows, and a
`history-lines` change resizes every scrollback buffer in place and drops the
cached scrollback.

dm reads these environment variables:

| variable | effect |
| --- | --- |
| `DM_STATE` | State directory; overrides `XDG_STATE_HOME`. |
| `XDG_STATE_HOME` | State directory base; dm uses `$XDG_STATE_HOME/dm`. |
| `XDG_CONFIG_HOME` | Config directory base; dm reads `$XDG_CONFIG_HOME/dm/config.kdl`. |
| `HOME` | Home directory base; the fallback for both XDG paths. |
| `DM_SESSION` | The session a pane belongs to. |
| `DM_PANE` | The pane id a pane's shell runs in. |
| `DM_NERD` | `1` forces Nerd Font glyphs, `0` forces the Unicode fallback. |
| `DM_NOTIFY` | `0` turns desktop notifications off. |
| `DM_STATS` | Non-empty prints style-cache and frame-pacing stats to stderr on exit. |
| `COLUMNS` | Terminal width when the client cannot ask the terminal. |
| `SHELL` | The shell a new pane runs. |
| `TMUX` | Set inside tmux; dm uses it to pick its terminal behaviour. |
| `ESCTEST_DIR` | Test only: the esctest2 directory the conformance harness searches. |

## Keybindings

The key table above is the built-in default. `config.kdl` patches it.

```kdl
prefix "ctrl+a"

keymap "main" {
    "split-right" "S"
    "detach" "D"
}
```

Each keymap block maps actions to keys, and a mode the file leaves out keeps the
built-in mode. A broken config or theme file leaves the running settings in
place and toasts the error.

A binding line in the main keymap may carry a help tag:

```kdl
keymap "main" {
    "zoom-pane" "z" help "Layout"
}
```

The tag moves that row to the named section of the armed prefix help box, and an
untagged row keeps the built-in section. The five built-in names are Layout,
Pane, Window, Session and Utility, and any other name is a custom section. The
tag moves a row the prefix help box already lists. It does not add a row.

One mode is one keymap.

| mode | what it remaps |
| --- | --- |
| `main` | the table the prefix key opens |
| `menu` | the right-click menu |
| `picker` | the session picker |
| `switcher` | the dashboard |
| `layout` | the layout palette |
| `scroll` | scrollback |
| `rename` | the rename prompt |
| `box` | the message log |
| `help` | the keymap view |

The prefix then `?` opens the keymap view: every mode the config defines, in the
order above, with each action and its key. `j`/`k` and `PgUp`/`PgDn` scroll, `g`
and `G` jump to the ends, `esc` or `q` closes, and `/` searches the rows with
`n` and `N` between matches.

A key name is one key. `esc`, `enter`, `tab`, `shift-tab`, `space`, `backspace`
and `prefix` are named keys, and the arrows and page keys are `up`, `down`,
`left`, `right`, `pgup` and `pgdn`. The dashboard takes every other printable key
as query text, and the rename prompt keeps every printable key as text.

## Themes

A theme sets the RGB palette and the glyphs dm paints. `config.kdl` selects one
with a top-level node:

```kdl
theme "night"
```

dm reads it from `$XDG_CONFIG_HOME/dm/themes/night.kdl`, or
`$HOME/.config/dm/themes/night.kdl` when `XDG_CONFIG_HOME` is unset: the
`themes/` directory beside `config.kdl`. At most one `theme` node, and its name
is letters, digits, `_` and `-`. No node selects the built-in theme.

Every block and every key is optional, so a theme file names only what it
changes. `dm html` prints every theme key with its block and its built-in
value. These fail the load, and the error names the line: an unknown or
duplicate key, a colour that is not `#rrggbb`, and a glyph that is not one rune
of one cell. The blocks are `palette` (the ground and text colours), `accents`
(the six group colours), `glyphs` (the chrome marks) and `states` (a glyph and a
colour for each of `working`, `idle`, `blocked`, `done`, `error`, `unknown` and
`shell`). A minimal theme:

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

A named glyph replaces that glyph's Nerd Font form too, so an explicit `grip`
paints even with Nerd Fonts on, and a state glyph replaces both the Unicode and
the Nerd form. Role icons, the agent bar, robot and terminal marks, stay
compiled in and are not themeable.

A missing or malformed theme file toasts the error, and dm runs on the built-in
theme. That theme reaches WCAG AA on every ground it paints. dm does not check a
user theme: its contrast is the author's.

## Web

`dm web` serves a session to a browser. The page is a view of the socket
contract, not a terminal: it reads panes through the same actions the CLI sends,
so it owns no state, and it never resizes a pane. The page is embedded in the
binary with no build step, and the session needs no attached client, so a phone
can watch a screen nobody sits at.

```sh
dm web                                        # loopback only
dm web --addr you.ts.net:8099                 # your own host, reachable from the phone
dm web --addr you.ts.net:8099 --read-only     # no input, no compose, no actions
```

The token is the whole access model. `dm web` prints the URL with the token on
it, and the page trades that token for a cookie on the first load. The cookie
lasts 400 days, so bookmark the bare address: after the first visit the token
never has to appear again. A listener that is not loopback also prints the link
as a QR code.

Pin the token in `config.kdl` with `web-token` and it never changes, which is
what keeps a bookmark and an installed app working across restarts, new session
directories and a moved state tree. Without the node the session keeps a
generated token in `sessions/<name>/web.token`, so it already survives a
restart; a new session directory starts a new one. `--token` overrides both for
a single run. A page that a rotated token locked out answers the next `GET /`
with a field for the current token, so a phone recovers without the old URL.

The page has five parts. The pane list shows every pane with its agent status,
message, branch and folder, and puts the panes that want a decision first. The
message log shows the newest entries, with a compose row that posts a key, a
state and a message through `msg-post`. One pane opens in one of three views, a
tap apart: the transcript of the agent's session file, the steps flattened to
one row each, and the screen the pane printed. The action sheet gives one large
row per command, with a target picker for panes, windows, workspaces, split sites
and layout presets. Its foot carries one row that reloads the page: an installed
app has no reload button, and the server sends the page and its script with no
cache, so that tap is the whole way a new build arrives. The screen grid paints
the active window's panes as blocks.
A tile is a link only when every tile is at least 44 points each way. A grid
too dense for that is a screen snapshot, and the pane list below it stays the
way in.

The page looks like the desk it mirrors: one mono face, square corners,
bracketed headers, and a state word in capitals. There is no second skin, so
nothing on the page is a switch between looks.

A pane that reported a session opens on the transcript and offers every view. A
pane that reported none, a shell for example, opens on the screen and offers no
other view. Agent prose is markdown, rendered with goldmark. Every view keeps the
input row that types into the pane, a `pic` button that uploads a photo to the
session's `uploads/` folder and types its path, and one row of three commands:
new window, rename, and close pane. While the pane is working or blocked the
header carries `stop`, which sends Escape through `/api/keys`: the same key
interrupts a running turn and declines a waiting question.

A pane whose agent asked the reader a question draws the ask form above its
view. Asks covers it. A command that fails paints the daemon's message in the
banner and changes nothing else.

`--read-only` refuses every write: pane input, the message log compose and every
action. The page hides the action sheet, the pane action row, the send row and
the compose. Everything that only reads stays: the pane list, the message log,
the three pane views, the collapse button and the screen grid.

### Push

Push is a second renderer of the `notify/` entry, beside the desktop pop, and
the daemon owns the decision. An entry reaches the phone when its state is
`blocked` or `error`, and not when it is `done`, and an entry for the pane an
attached client is looking at pops nothing. The title is the pane and the state, and a tap opens the page on
that pane. A browser subscribes only from a secure context, so `dm web` must
answer on an https name. `tailscale serve` terminates that name in front of the
loopback listener, and the tailnet certificate is enough; a plain `http://`
tailnet address is not. The daemon does the sending, so `dm web` has to be
running when a pane changes state. Each urgent entry costs one request per
subscribed device, at most eight, under a 15 second bound.

iOS ships push for a home-screen web app only, from 16.4 on, so the app is
installed once by hand:

1. Delete the icon already on the home screen. An icon added before this works
   opens a Safari tab and cannot subscribe.
2. Open `dm web`'s URL in Safari, then tap Share and Add to Home Screen. Another
   browser cannot add the app.
3. Open dm from the new icon, which runs standalone and may notify.
4. Tap `notify` in the page header and allow notifications when the phone asks.

The button reads `on` once the subscription is stored, `off` when the device
holds none, `denied` when permission was refused, and `unsupported` where the
browser has no push at all. A tap while it reads `on` unsubscribes.

The subscription and the key pair live in the session directory:

```
sessions/<name>/webpush.json   the subscriptions, one entry per device
sessions/<name>/webpush.key    the VAPID key pair, mode 0600
```

`webpush.key` signs every push. Deleting it invalidates every subscription
already handed out, so delete it only to revoke every device at once, and tap
`notify` on each device again afterwards. A push service that answers `410 Gone`
prunes that subscription from `webpush.json` on its own.

## Asks

An ask is a question set an agent puts to the reader from a pane. One ask
belongs to one tool call id, and carries one to many questions: a header, the
question text, a multi-select flag and options with a label, a description and
an optional preview. The store is session state, not pane state, so a client
that attaches mid-question sees the open ones, and the ids it hands out are
`a1`, `a2`, and so on. An answered ask stays listed until the agent closes it,
and the snapshot carries no answer, so a client that attaches between the answer
and the close draws the ask as open; `dm ask list` carries the answers
themselves. The store caps one question set at 64 questions of 64 options, one
session at 64 listed asks, and the listed text at 256 KiB.

- The TUI. The client paints the focused pane's ask as a floating pane, one
  question at a time: the header, the question, the options with their
  description and preview, and on a single select a last `Type something.` row.
  `j`/`k` or the arrows move, and `J`/`K` scroll the detail. A digit picks that
  option, and `space` picks on a single select and toggles on a multi-select.
  `t` types a custom answer, and `n` attaches a note to the option under the
  cursor. `tab` and shift-tab move between questions, and `A` puts the question
  panel away and brings it back. On a pane with no question of its own, `A`
  reaches the oldest question waiting on another pane. `enter` sends once every
  question is answered, and `esc` declines.
  Those keys take rows of their own above the closing rule, so every binding is
  on screen at any width. A second question waits in the store, and the sidebar
  marks its pane with a `?` badge. The status strip names a question the screen
  is not showing as `ask <pane>`, with the key that reaches it. While a question
  panel is up the strip stays quiet, because that key would hide the panel
  instead of reaching the other question.
- The phone. `dm web` paints the ask as a form above the pane's views: one
  block per question, options as tappable rows, the description inline and the
  preview behind its own toggle, a text field for something else on every
  question, and several picks on a multi-select. Send stays disabled until
  every question is answered. A refusal keeps its code in the banner and keeps
  every answer, and `--read-only` shows the questions with no controls.
- pi. The `dm-agent` extension registers `dm_turn`, so an agent asks through
  dm's own store: `dm ask open` with the pane and the tool call, then
  `dm ask await` for the answer. Nothing renders in pi, and the answer comes
  back as the store's JSON. Outside a dm session the agent asks in prose.

Esc declines an ask on every surface. The floating pane and the phone send
`cancel:true`, and a pi ask that is cancelled, or whose call is aborted, is
declined too. A decline is an answer: the agent reads it and stops waiting. An
ask takes the focus of the pane it belongs to, across windows and workspaces, so
the question panel stands in front of the reader instead of waiting on another
pane. Answering a question leaves the screen where the answer was sent. The next
question waits on the status strip, and `A` reaches it. The CLI is the store's
door: `dm ask list`, `dm ask open --pane <pane> --tool-call <id>`, `dm ask
await <id>`, `dm ask answer <id>` and `dm ask close <id>`. A question set is too
big for argv, so the bodies come on stdin.

## Message log

One session has one message log, and it is append-only. Any pane posts into it,
and any caller waits on it. It is where a worker agent reports `blocked` and the
agent supervising it waits for `done`, with no screen polling.

An entry is
`{"seq":7,"at":"2026-11-03T14:40:12Z","from":"w3p1","key":"lane/go-backend/api","state":"done","msg":"accept command green","body":""}`,
and every field is always present.

| field | is |
| --- | --- |
| `seq` | Starts at 1, only grows, never repeats, and a clear does not reset it. |
| `at` | The post time, truncated to the second, in UTC. |
| `from` | The posting pane's id, set when the poster runs in a pane of the session, may be empty, and must name a pane in the tree. |
| `key` | Groups entries, cap 128 bytes. An empty key on list, wait and clear means the whole log. |
| `state` | `note`, `working`, `blocked`, `done` or `error`. `blocked` waits on someone else, `done` finished well, `error` finished badly. `clear` is a pane status, not a message log state, and post rejects it. |
| `msg` | The text, cap 2000 bytes. |
| `body` | The long text under the message line, cap 16 KiB. It may carry newlines. |

Key and message must be valid UTF-8 with no control characters, because an entry
prints on one row. The log is bounded at 1000 entries and 256 KiB, and past
either bound the oldest entries go. The log lives in `state.json`, so a restart
keeps every entry, its order and its `seq`.

The message log is not a queue. Nothing is consumed, so every reader sees the
whole log for its key, and a clear never resets `seq`. Without `afterSeq` a wait
reads a status board, where the newest entry under the topic is the state of the
world, so a newer `blocked` outranks an older `done` on the same key. With
`afterSeq` a wait reads a stream: the oldest entry past the reader's cursor that
matches the topic and, when a state is named, that state. The reader keeps the
cursor, so the server holds no subscription state. A `trimmed` reply says the
cursor sits behind the oldest row the log keeps, so read from the oldest row it
still holds.

`dm man` names 71 of the 76 actions, with one example each. The fields are one
shared list, and `docs/internals/03-protocol.md` carries the action matrix.

The CLI:

```sh
dm msg post --state blocked --key lane/go-backend/api --msg "ready for review"
dm msg post --key lane/go-backend/api --state note --msg "log is long" --body-file notes.md
dm msg show --key lane/go-backend/api [--seq N | --from PANE]
dm msg list [--key lane/go-backend/api | --prefix lane/] [--after SEQ] [--limit 20] [--json]
dm msg wait [--key lane/go-backend/api | --prefix lane/] [--state done] [--after SEQ] [--timeout 5m]
dm msg sub [--key K | --prefix P] [--state S] [--after N] [--json]
dm msg clear [--key lane/go-backend/api]
dm msg watchers [--json]
dm msg schema
```

`post` prints the stored entry as one row, and `list` prints newest first as
`SEQ KEY STATE FROM MSG`. `wait` prints the entry it found, or the reply on
`--json`. `sub` prints each frame as one row, and with `--json` as NDJSON, so an
agent spawns the sub once and reads lines. `watchers` prints one row per reader
position as `PANE FILTER CURSOR WAIT`. `list` and `wait` send `from` for you
inside a pane. The wait timeout defaults to 5 minutes, up to one hour. `--after`
carries the cursor: leave it off to read the status board. A timeout replies
with `box: wait timed out` plus the newest entry it saw, and exits 1.

A worker agent and the agent supervising it, in one session:

```sh
# in the worker's shell, a pane of the session
dm msg post --key lane/go-backend/api --state blocked --msg "ready for review"

# in the parent: waits until the worker reports done
dm msg wait --key lane/go-backend/api --state done --timeout 10m

# in the worker's shell, after the accept command
dm msg post --key lane/go-backend/api --state done --msg "accept command green"
```

The key is a topic, and it is a flat string, so a publisher names a branch:
`msg/broadcast`, `reply/w1p1`. A reader names an exact topic with `key` or a
branch with `keyPrefix`, and keeps its own cursor, so two orchestrators both see
every report. Post a pointer, not a document, because an entry stays one row.
A dead pane posts nothing, so an orchestrator treats a timeout as a lost worker.

`prefix m` opens the message log in a floating pane over the panes, and `N` opens
the same pane on the `notify/` branch. One row per entry, newest first, as
`MARK COUNT SEQ STATE KEY FROM AGE MSG`, with the state in the colour the
sidebar gives that status. `j`, `k` and the arrows move the selection one row,
`PgUp` and `PgDn` one page, the wheel three rows, and `g` and `G` jump to the
newest and the oldest row. The floating pane grows with the log up to 22 rows
and then scrolls. `w` shows the reader positions, one row each as
`PANE FILTER @CURSOR`, with a `wait` mark while a wait is parked. `enter` opens
the selected entry, `m` toggles the notification filter, `p` focuses the pane
the entry is about, `x` clears the selected entry's key, and `X` clears what the
view shows. `esc`, `q` or the prefix key closes it.

A pane that enters `blocked` or `error`, or `done` with a message, or that exits,
is posted to the message log under its reserved topic `notify/<pane id>`. A
headless session renders each entry through `notify-send`, and an attached
client raises a toast for the entries it has not seen. An entry for the pane in
view is still logged but pops nothing.

## OSC 7501 agent status

`dm` implements the terminal side of the program status protocol. A program
reports on its own stdout:

```
ESC ] 7501 ; state=working:app=pi:msg=<base64> ESC \
```

- `state=` is `idle`, `working`, `blocked`, `done`, `error` or `unknown`. `clear`
  is the removal word. It drops the record, and the pane then reads `unknown`.
- `app=` is optional and display only: `dm` keeps the last value per pane and
  uses it as a sidebar label when the pane has no recorded name. `kind=` is
  optional and unused.
- `msg=` is optional base64 UTF-8; `dm` decodes it for `dm status` and the bar.
- Both `BEL` and `ST` terminate the sequence.

Programs usually probe support first with `ESC ] 7501 ; ? ESC \` and wait for
the terminal to echo the body back. `dm` answers that query through the PTY, so
pi and other conforming programs switch reporting on. The older JSON
(`{"state":"working"}`) and bare-state forms are accepted.

## Provision a harness

An agent harness runs in a pane and learns dm by reading. `dm provision`
installs the reading, and the tools, in one step:

```sh
dm provision              # link the extension and the skill into ~/.pi/agent
dm provision --check      # exit 1 unless both links are in place and inside this repo
dm provision --print      # say what a run would do, write nothing
dm provision --uninstall  # remove a link it made, never a file you put there
```

Both links point into this checkout, so an edit here is live in the harness at
once. `skills/dm-agent/dm-agent.ts` registers four tools, `dm_layout`,
`dm_pane`, `dm_box` and `dm_turn`, each an action enum over `dm ... --json`.
`dm_turn` is model-only: the model can call it, but a codemode script cannot,
because it asks the operator. The other three are direct, so a codemode script
can call them too. `skills/dm-fleet/SKILL.md` carries the judgement a tool schema
cannot: when to split, when a worker agent is worth it, and the
`lane/<plan>/<worker>` protocol.

`dm llms` carries the mechanical surface and needs no install. pi is the only
adapter dm ships.

## RPC

Every client speaks newline-free, length-prefixed JSON over the session socket
at `$XDG_STATE_HOME/dm/sessions/<name>/server.sock`. The CLI is a thin wrapper,
so anything one CLI verb does, an agent can do with one action.

```sh
dm exec '{"action":"split","target":"w1p1","dir":"left"}'
dm exec '{"action":"focus-dir","dir":"right"}'
dm exec '{"action":"new-window","name":"tests"}'
dm exec '{"action":"swap-panes","target":"w1p1","other":"w1p2"}'
dm exec '{"action":"swap-panes","target":"w1p1","dir":"right"}'
dm exec '{"action":"move-pane","target":"w1p1","other":"w1p2","dir":"right"}'
dm exec '{"action":"select-layout","layout":"tiled"}'
dm exec '{"action":"select-layout","layout":"next"}'
dm exec '{"action":"synchronize-panes","on":true}'
dm rename-workspace s1 api-work
dm set-project s1 go-backend
dm write-to w1p1 --msg "go test ./..." --enter
dm read w1p1 --lines 20
dm read w1p1 --scrollback 200
dm yank w1p1
dm status
```

A `split` request may carry an `argv` array: the program and its arguments the
new pane runs instead of a shell:

```sh
dm exec '{"action":"split","target":"w1p1","dir":"right","argv":["htop"]}'
```

`new-window` and `new-workspace` take the same field. An empty element or a NUL
byte is an error. The pane records its argv in the state file, so a server
restart that spawns the panes fresh respawns the same program.

`dm exec` prints the response as JSON, and a failed action exits non-zero.
`dm man` names 71 of the 76 actions, with one example each, and the fields are
one shared list. This page names the common ones only.

## Durability

A detach loses nothing: the panes, their processes and their scrollback stay,
and a reattach draws the screen as you left it.

Kill the server (`kill -9`, crash, logout) and the panes keep running, including
their shells, because each pane daemon has its own unix socket and is started
with `setsid`. The next CLI call or attach restarts the server, which reads
`state.json` and adopts the live pane daemons instead of respawning them. A pane
whose shell or argv program exits is removed from the layout and its split
collapses, and focus moves to a live neighbour. After a reboot the pane
processes are gone, but layout, focus, window names, cwd and each pane's argv
are in `state.json`. The server spawns fresh shells, or the recorded argv
program, in the same places and marks them `restart: true`.

A pane daemon watches its own socket path: when the path is gone, or names a
different file, no client can reach it, so it stops its shell and exits. It runs
with a memory ceiling 32 MiB above its scrollback budget.

State lives in `$DM_STATE`, else `$XDG_STATE_HOME/dm`, else
`~/.local/state/dm`.

```
sessions/<name>/state.json           layout, focus, names, cwd, the message log
sessions/<name>/webpush.json         the phone push subscriptions
sessions/<name>/webpush.key          the VAPID key pair that signs them
sessions/<name>/server.sock          RPC and client socket
sessions/<name>/server.lock          the flock that keeps one server per session
sessions/<name>/server.pid           the server process id
sessions/<name>/server.log           server stderr
sessions/<name>/panes/<id>.sock      one per pane
sessions/<name>/panes/<id>.log       pane daemon stderr
sessions/<name>/panes/<id>.sock.pid  the pane process id
```

## Tests and licence

```sh
go test ./...
```

The integration tests in `blackbox/` run the built binary against a temp state
dir. [docs/internals/README.md](docs/internals/README.md) lists the commands that
keep the tree honest, in reading order for a contributor.

dm is MIT. The text is in [LICENSE](LICENSE), and every release carries it
beside the tarballs.
