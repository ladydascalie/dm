# dm

A small, durable terminal multiplexer. One static Go binary that carries the
server, the pane harness, the TUI client and the CLI.

Prototype status: M1 works end to end. Splits, windows, workspaces, OSC 7501
agent status, durable sessions and a JSON RPC are implemented. Mouse support,
scrollback search, session switching inside the TUI and layout resizing are not.

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
name, a pane's recorded agent name.

State written before this scheme is migrated once, in tree order, on load.

## Build

```sh
go build -o dm .
```

## Use

```sh
dm                      # attach to "main", creating it if missing
dm attach -s work       # resume a session (durable, see below)
dm ls                   # sessions and whether their server is live
dm new -s work          # start a session without attaching
dm kill -s work         # stop the server; panes keep running
```

Keys inside the TUI. `ctrl+b` arms the prefix and shows the action box,
bottom right. It stays until an action runs or `esc` dismisses it.

| key | action |
| --- | --- |
| `ctrl+b` | prefix (press it twice to send a literal `ctrl+b`) |
| `\|` / `-` | split right / down |
| `<` / `^` | split left / up |
| `[` | scrollback (also the mouse wheel) |
| `h j k l` or arrows | focus pane left / down / up / right |
| `n` / `p` | next / previous pane |
| `x` | close pane |
| `c` | new window |
| `w` / `W` | next / previous window |
| `0-9` | select window |
| `t` | new workspace |
| `s` | switch session (picker: j/k, enter, esc) |
| `a` | quick switch palette: panes, windows, workspaces |
| `)` | next workspace |
| `r` | cycle the sidebar: full, thin, hidden |
| `d` / `q` | detach |

Drag a divider with the left mouse button to resize the split. The client
sends `resize-split` with the two panes beside the divider and the ratio, at
most every 30 ms plus a final call on release. Both sides keep at least three
cells. A divider always belongs to dm, even when a pane claims the mouse: the
app's PTY covers only its content rect, so the border press is not its event.

## Palette

`prefix a` floats a quick switcher over the panes. It is television, run on its
own PTY, so the list is fuzzy matched: type an id or a name, then enter to land
there. One row per pane, leading with its id (`w2p3`), so an id can be typed
directly, and ending with the workspace and window it lives in. A workspace or
window row would only repeat the pane it holds, so the palette lists panes
alone. The preview
pane shows the live screen the row would land on. Press the prefix key again to
close it and change nothing. `esc` also closes it, but television 0.15.9 prints
the highlighted entry when it quits, so a bare `esc` lands on that row. `dm dash` prints the same rows outside the TUI, and `dm peek <id>`
prints the preview, so the palette can be driven from a shell.

The palette is client owned: it starts when the key is pressed and ends when
the choice is taken, so it never enters the session tree and never survives a
detach.

## Sidebar

The left columns of the screen show a sidebar when the session asks for one.
It is a tree of workspaces, windows and panes. A workspace row leads with a
bar in the workspace's own accent colour, then its name in bold, then the
pane count at the right edge in dim grey. The accent is one of six hues picked
by a stable hash of the workspace id, so a workspace keeps its hue for the
life of the session. One blank row separates workspace blocks.

Each window follows with its fold caret and a label: the linked worktree
folder when its pane has one, else the git branch, else the window name. The
git state comes from the window's focused pane, or from its first pane in a
repository. The right edge of that line carries the uncommitted change count:
an amber dot when the tree is dirty, then `+N` in green and `-M` in red, with
a zero side hidden, or `clean` in dim grey when nothing changed. A window
whose pane has a status message gets a dim second line under its label.

Windows start folded; click a window line or its second line to unfold or fold
it, and the fold is stored in the state file. An unfolded pane row labels
itself with its recorded name, then the `app=` it last reported, then the pane
id, with a state glyph in a colour per state beside the label. A pane counts
as an agent when it has a recorded name or has spoken the status protocol at
least once; the rest are shells. An agent row leads with a rocket icon (or a
`▎` bar without Nerd Fonts), a shell row with a terminal icon, and a dim shell
row otherwise leaves those two cells blank, so the state glyph keeps its
column. The report is sticky: `state=clear` does not demote an agent, and both
the mark and the last `app=` are stored in the state file. The focused pane's
row is highlighted, and a left click on a pane row focuses that pane.

A new window is named after the folder it starts in, and it starts in the
folder of the pane it was created from; `rename-window` overrides the name. A
window whose label is still the old positional number is renamed from its
pane's folder when the session loads.

A long label is cut with `…` before the right-aligned stats lose a cell, so
the git state always stays visible. The rail paints its own dark background
and uses explicit RGB colours, not palette slots, so every label, glyph and
stat reaches WCAG AA contrast on both the rail and the focused row. Icons use
Nerd Font code points when `fc-list` finds a Nerd font; `DM_NERD=1` forces
them and `DM_NERD=0` falls back to the Unicode glyphs. The rail is 34 columns
by default, at most a third of the screen and never wider than 46 columns;
`set-rail` changes it. `prefix r` cycles the sidebar through three states:
full, thin, and hidden. The thin rail is four columns: the agent bar, one
state glyph per pane, and no names. It always lists panes, folded or not, and
leaves a blank row where a workspace or window line would sit. A hidden or
thin sidebar remembers the last full width.

In scrollback: `j`/`k` or arrows move a line, `PgUp`/`PgDn` a screen, `g`/`G`
jump to the top or the live view, `q`, `esc` or `[` return to live. The wheel
enters scrollback directly.

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
heavy line glyphs and shows `drag to resize` in the status bar. The hover and the press resolve through the
same `padHit` zone, so what lights up is exactly what will drag. No-button
motion is the mux's own: dm uses it for that hover and does not forward it to
pane apps, which asked for button motion only.

Right click opens the pane menu, even over a program that owns the mouse: it is
a window-manager gesture, not the app's. Move with `j`/`k` or the arrows,
`enter` or a digit to run, `esc` or `q` to close. With the mouse, hovering
highlights the row under the pointer, a left click on a row runs it, and a
right press, drag and release runs the row under the release. The release of
the press that opened the menu, and clicks on the title or border, do not
dismiss it; a click outside does. The menu
offers split right/down/left/up, focus, close pane, scrollback, new window, new
workspace, switch session and detach. If the program in that pane enabled mouse reporting
itself, the event is forwarded to it instead. That is how `pi`, `htop` and
`vim` keep their own scrolling: they own the wheel when they ask for it.

Alternate-screen programs have no terminal scrollback to show. Their screen is
painted in place, not scrolled, so dm refuses to open its own scrollback for
them and tells you to use the program's. The pane daemon owns this fact: it
reports `alt` and `mouse` in every snapshot, so a client that attached late
still knows even when the enabling escape sequence has left the replay ring.

Scrollback is built on demand: the client keeps the pane's last 512 KiB of raw
output, replays it into a tall terminal (2000 rows), and draws a window into it
at the current offset. It is a frozen snapshot taken when you scroll, so new
output does not move the view. The replay is approximate for full-screen
programs; for streamed output it is exact.

Mouse reporting means normal drag no longer selects text. Hold Shift while
dragging to use the terminal's own selection.

dm selects instead. A left drag inside a pane highlights the row-major range
in reverse video, clamped to the pane it started in, and the release copies it
with OSC 52, so the text lands on the terminal's clipboard and a `copied N
chars` toast confirms it. A press and release in the same cell is a click and
still reaches the app that claims the mouse; dm only takes the drag. Trailing
spaces go and lines join with a newline. An app's own drag selection loses to
dm's while dm owns the gesture, which is why Shift exists.

dm enables bracketed paste (2004) on attach and disables it again on detach, so
ctrl+shift+v inserts newlines literally. The client sends a paste to the focused
pane as one chunk and never reads a byte inside it as a dm key: an embedded
ctrl+b does not arm the prefix, and newlines do not submit. A paste also closes
an open menu, picker or scrollback first.

Small actions answer with a toast, not a suffix on the status bar. The client
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

## RPC

Every client speaks newline-free, length-prefixed JSON over the session socket
at `$XDG_STATE_HOME/dm/sessions/<name>/server.sock`. The CLI is a thin wrapper,
so anything one CLI verb does, an agent can do with one action.

```sh
dm exec '{"action":"split","target":"p1","dir":"left"}'
dm exec '{"action":"focus-dir","dir":"right"}'
dm exec '{"action":"new-window","name":"tests"}'
dm write-to p1 --msg "go test ./..." --enter
dm read p1 --lines 20
dm status
```

Actions: `write-to`, `exec`, `read`, `history`, `status`, `keys`, `split`,
`focus`, `focus-dir`, `next-pane`, `prev-pane`, `close-pane`, `new-window`,
`next-window`, `prev-window`, `select-window`, `new-workspace`,
`next-workspace`, `prev-workspace`, `rename-window`, `toggle-fold`, `list`,
`ping`. `toggle-fold` takes a window id as `target` and replies with the new
`unfolded` state.

`history` takes `target` and an integer `lines` and replies with
`{"lines": ["..."]}`: up to `lines` text rows immediately above the live
screen, oldest first, trailing blanks trimmed. `lines` must be positive.
`dm read <pane> --scrollback N` prints the same rows.

The visible screen is a snapshot, not a replay. Each pane daemon serialises its
VT to canonical ANSI (`screen`), and the client writes that into a fresh
terminal at the pane's rectangle. The output ring travels separately as
`replay` and feeds only the scrollback history, so a client never rebuilds the
picture from raw output.

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
dm split p1 --dir down
dm write-to p2 --msg "$PWD/dm __probe --hold 5s" --enter
dm status
```

## Durability

Two processes per session:

```
dm __server   owns layout, focus, clients; persists state.json on every change
dm __pane     one per pane; owns the PTY, VT screen, status and 512 KiB output ring
```

Each pane daemon has its own unix socket and is started with `setsid`, so it
survives the server. The server, on start, reads `state.json` and **adopts** the
still-running pane daemons instead of respawning them.

- Kill the client: nothing is lost, reattach.
- More than one client may attach. They share one layout: the largest attached
  terminal sets the size, and when it detaches the next largest takes over, so
  a small client cannot shrink everyone. A smaller client shows the layout
  clipped to its own screen.
- A pane exports `DM_SESSION` to its shell. `dm` refuses to attach that same
  session from inside one of its own panes, because the mirror is useless and
  competes for the shared size. Use `-s` for another session.
- Kill the server (`kill -9`, crash, logout): panes keep running, including
  their shells. The next CLI call or attach restarts the server and reconnects
  the same pane processes. Content and status are still there.
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
sessions/<name>/state.json      layout, focus, names, cwd
sessions/<name>/server.sock     RPC and client socket
sessions/<name>/panes/p1.sock   one per pane
sessions/<name>/panes/p1.log    pane daemon stderr
```

## Tests

```sh
go test ./...
```

The integration tests run the built binary against a temp state dir. They cover
the pane-survives-server-kill path, split/close layout, window and workspace
switching, the JSON action path and OSC 7501 status.
