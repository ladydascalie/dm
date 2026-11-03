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
| `r` | rename the active workspace |
| `b` | cycle the sidebar: full, thin, hidden |
| `d` / `q` | detach |

`prefix r` opens the rename prompt as a line above the status bar. It starts
from the workspace's current name, so typing appends to it. Backspace deletes
one character and `ctrl+u` clears the line. `enter` commits, `esc` cancels, and
an empty name cancels too. The right-click menu offers the same row.

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
pane count at the right edge in dim grey. The accent is one of six hues: a
stable hash of the workspace id picks the preferred one, and the rail gives up
to six live workspaces distinct hues. A workspace keeps its accent while the
workspace list holds. One blank row separates workspace blocks.

Each window follows with its fold caret and its folder: the pane's path below
the repository root, the folder's own name when the pane sits at the root, and
the folder name alone outside git. A pane that reports no directory falls back
to the window name. Two spaces follow with the git branch in dim grey when it
differs from the folder. The folder names the window, so it outranks the
branch: a narrow rail drops the branch whole before it cuts the folder. The
folder and the git state come from one pane: the window's focused pane when it
is in a repository, else its first pane in a repository, else the focused pane,
else the window's first pane. The right edge of that line carries the
uncommitted change count: an amber dot when the tree is dirty, then `+N` in
green and `-M` in red, with a zero side hidden, or `clean` in dim grey when
nothing changed. A pane outside a repository gets no stats tail: a directory
git never saw cannot be clean. A window whose pane has a status message gets a
dim second line under its folder.

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

A long label is cut with `…` so the right-aligned stats fit, and the branch is
dropped first. When even that cannot, the rail draws no tail at all, never half
a pair. The rail paints its
own dark background
and uses explicit RGB colours, not palette slots, so every label, glyph and
stat reaches WCAG AA contrast on both the rail and the focused row. Icons use
Nerd Font code points when `fc-list` finds a Nerd font; `DM_NERD=1` forces
them and `DM_NERD=0` falls back to the Unicode glyphs. The rail is 34 columns
by default, at most a third of the screen and never wider than 46 columns;
`set-rail` changes it. `prefix b` cycles the sidebar through three states:
full, thin, and hidden. The thin rail is four columns: the agent bar, one
state glyph per pane, and no names. It always lists panes, folded or not, and
packs one row per pane, with no header, note or gap row. A hidden or thin
sidebar remembers the last full width.

The rail's last column is a grab strip: drag it to resize the rail from four
columns up to a third of the screen, capped at 46. A drag to the left collapses
the rail to its thin form; hiding stays on `prefix b`. The rail's bottom row is
a control: it shows a collapse mark on a full rail and an expand mark on the
thin one. A click toggles full and thin, and expanding restores the last full
width.

In scrollback: `j`/`k` or arrows move a line, `PgUp`/`PgDn` a screen, `g`/`G`
jump to the top or the live view, `q`, `esc` or `[` return to live. The wheel
enters scrollback directly.

## Status bar

The last row of the screen is the status bar: one line of segments on explicit
RGB, one idea per segment, with a single gutter cell of the bar ground between
neighbours. The order is fixed: the session name, the workspace name, one chip
per window, the focus chip, then the pinned right block.

- The session name is railText, then the workspace name in railHeader.
- One chip per window, in window order, is `N name`, with the number being the
  window's 1-based position in the window list. A chip keeps its number when a
  neighbour is dropped. The active window's chip reads as active: a `▸` in
  barAccent, then the chip in bold railText, both on railFocusBG. The other
  chips are railDim on the ground.
- The focus chip sits on railFocusBG. It leads with the focused pane's state
  glyph in that state's colour, then the pane id in bold railText, then the
  pane name and the status word in railDim. A missing status reads `unknown`; a
  shell reports no state at all, so it shows `▶` in railDim instead of a glyph.
  The focused pane is the entry in the window's pane list whose id is the focus.

Colours are explicit RGB, never palette slots, so a theme cannot map them onto a
background they fail on: railBG (#1f1e27) for the ground and the gutter,
railFocusBG (#272633) for the active window chip and the focus chip, railText
(#e9eaf1), railHeader (#a6a9bd), railDim (#8f93a8), barAccent (#7ac0eb) for the
active mark, and the state colours of the rail glyph table. Every footer
foreground reaches WCAG AA, 4.5:1 or better, on both grounds it is painted on:
railDim on railFocusBG is the tightest at 4.89:1, and the barAccent mark reaches
7.49:1 on the focus fill it sits on, 8.30:1 on the ground.

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
offers split right/down/left/up, focus, close pane, scrollback, new window, new
workspace, rename workspace, switch session and detach. If the program in that
pane enabled mouse reporting itself, the event is forwarded to it instead.
That is how `pi`, `htop` and `vim` keep their own scrolling: they own the
wheel when they ask for it.

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

dm enables bracketed paste (2004) on attach and disables it again on detach, so
ctrl+shift+v inserts newlines literally. The client sends a paste to the focused
pane as one chunk and never reads a byte inside it as a dm key: an embedded
ctrl+b does not arm the prefix, and newlines do not submit. A paste also closes
an open menu, picker or scrollback first.

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

## Performance

A frame paints only the rows that changed. Each pane row is fingerprinted, so
a row is written again only when its glyphs or colours move; the sidebar, the
status bar and each overlay are compared as whole bands and written only when
their bytes differ. The client no longer clears the screen and repaints every
cell on every frame: a steady frame writes nothing. Every written frame is
wrapped in a synchronized update (`ESC [ ? 2026 h` to `ESC [ ? 2026 l`), so the
terminal applies it in one go. A frame that clears is a full repaint, and it
happens only when the picture is not stable: a resize, a pane added, closed or
moved, a focus change, or entering and leaving scrollback, the menu, the picker
or the palette.

## RPC

Every client speaks newline-free, length-prefixed JSON over the session socket
at `$XDG_STATE_HOME/dm/sessions/<name>/server.sock`. The CLI is a thin wrapper,
so anything one CLI verb does, an agent can do with one action.

```sh
dm exec '{"action":"split","target":"p1","dir":"left"}'
dm exec '{"action":"focus-dir","dir":"right"}'
dm exec '{"action":"new-window","name":"tests"}'
dm rename-workspace s1 api-work
dm write-to p1 --msg "go test ./..." --enter
dm read p1 --lines 20
dm status
```

Actions: `write-to`, `exec`, `read`, `history`, `status`, `keys`, `split`,
`focus`, `focus-dir`, `next-pane`, `prev-pane`, `close-pane`, `new-window`,
`next-window`, `prev-window`, `select-window`, `new-workspace`,
`next-workspace`, `prev-workspace`, `rename-window`, `rename-workspace`,
`toggle-fold`, `list`, `ping`, and the five box actions `msg-post`,
`msg-list`, `msg-wait`, `msg-clear` and `msg-schema` below. `toggle-fold`
takes a window id as `target` and replies with the new `unfolded` state.

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
dm split p1 --dir down
dm write-to p2 --msg "$PWD/dm __probe --hold 5s" --enter
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

Five actions. Each takes its request in the `res` field of the envelope.

| action | request | reply |
| --- | --- | --- |
| `msg-post` | `key`, `state`, `msg`, `from` | `{"entry":{...}}` |
| `msg-list` | `key`, `limit` | `{"entries":[...]}`, newest first |
| `msg-wait` | `key`, `state`, `timeoutMs` | `{"entry":{...}}` |
| `msg-clear` | `key` | `{"cleared":N}` |
| `msg-schema` | none | `{"schema":{...}}` |

`state` is required on post and wait, and `timeoutMs` on wait. `limit` clamps
to the box, and zero or less means the whole box. `timeoutMs` runs from 1 to
3600000. An unknown field, an unknown action, an unknown state or a `from`
outside the tree fails the request, so nothing is silently ignored.
`msg-schema` serves these shapes as JSON Schema, built from the Go types and
the one state enum.

The CLI:

```sh
dm msg post --state blocked --key lane/a --msg "ready for review"
dm msg list [--key lane/a] [--limit 20] [--json]
dm msg wait --key lane/a --state done [--timeout 5m]
dm msg clear [--key lane/a]
dm msg schema
```

`post` prints the stored entry as one row. `list` prints newest first as
`SEQ KEY STATE FROM MSG` and cuts only the message to the terminal width;
`--json` prints the reply unchanged. `clear` prints the count. `schema` prints
the schema, so `dm msg schema | jq .actions` reads. The wait timeout defaults
to 5 minutes and takes any duration up to one hour.

A wait matches the newest entry for its key, not any entry. It returns at once
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
