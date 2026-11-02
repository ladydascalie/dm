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
bottom right. The box clears after 3 seconds of no input.

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
| `)` | next workspace |
| `d` / `q` | detach |

Drag a divider with the left mouse button to resize the split. The ratio is
sent as `resize-split` at most every 30 ms, and both sides keep at least three
cells. A divider is ignored when the program on either side has claimed the
mouse.

In scrollback: `j`/`k` or arrows move a line, `PgUp`/`PgDn` a screen, `g`/`G`
jump to the top or the live view, `q`, `esc` or `[` return to live. The wheel
enters scrollback directly.

## Scrollback and mouse

Mouse reporting is always on, there is no setting (SGR 1000 + 1006, disabled
again on detach). Left click focuses the pane under the pointer and the wheel
scrolls the pane under the pointer. If the program in that pane enabled mouse reporting
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
`next-workspace`, `prev-workspace`, `rename-window`, `list`, `ping`.

`history` takes `target` and an integer `lines` and replies with
`{"lines": ["..."]}`: up to `lines` text rows immediately above the live
screen, oldest first, trailing blanks trimmed. `lines` must be positive.
`dm read <pane> --scrollback N` prints the same rows.

`dm exec` prints the response as JSON. A failed action exits non-zero.

## OSC 7501 agent status

`dm` implements the terminal side of the program status protocol
(<https://www.superlogical.com/rex/docs/build/program-status>). A program
reports on its own stdout:

```
ESC ] 7501 ; state=working:app=pi:msg=<base64> ESC \
```

- `state=` is `idle`, `working`, `blocked`, `done`, `error` or `clear`.
- `app=` and `kind=` are optional and only used for display.
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
