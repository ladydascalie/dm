# The client

The client is one process of three: dm is one binary that runs the client, a session server and one pane daemon per pane (`main.go#main`). The client owns the terminal and talks to one session. The session is the session server: the daemon that owns the pane tree and listens on the session socket, `$XDG_STATE_HOME/dm/sessions/<name>/server.sock` (`internal/paths/paths.go#ServerSockAt`). "The server" on this page always means that daemon. A message is one `wire.Msg` on that socket: a length-prefixed JSON envelope with a `type`, read one at a time by `wire.ReadMsg` (`internal/wire/wire.go#Msg`).

A session holds a tree of workspaces, windows and panes (`internal/session/state.go#State`). A pane is one program with its own pty and its own screen, and the pane's daemon owns both, plus the scroll ring, the pane's history in raw bytes (`internal/pane/pane.go#paneProc`), so a pane outlives the client and the server. A workspace is a named group of windows, and it may carry a project name: workspaces that share a project name form one group in the rail. A window is a set of panes. The rail is the sidebar down the left edge that lists that tree, one project, workspace, window or pane row per line, and it is server state and client paint. The server gives every pane and the rail a rectangle, called a rect, and the arrangement of those rectangles is the layout.

The client runs no pane process and no shell, and owns no layout. It keeps a local terminal (`vt.Term`: a grid, a parser and the styles, `internal/vt/term.go#Term`) per visible pane, feeds each one the bytes the server sends, and paints the result plus its own chrome. The chrome is the furniture around the panes: the pane borders; the grips, the one-cell drag marks between two panes; the divider, the gap between two panes; the rail; the tab bar, the window tabs; and the bar, the status line. A frame is one paint: the bytes that one `draw` hands to the terminal.

The client paints on a damage diff. Damage is the set of rows a change touched, so a frame rewrites only the rows whose contents moved, plus the bands whose rows an overlay took or left; a band is one screen region painted from its own buffer. An overlay is a widget that paints over the panes and owns the keyboard, and the overlay table below lists them.

Every change of session state is a request to the server, never a local edit. What the client draws before the server agrees is only its own state: the selection (the text range the mouse dragged), the hover (the link under the pointer while ctrl is held) and whatever an overlay holds. This page answers how all of that works, and where you add a key action, an overlay or a band.

## File map

All paths are `internal/client/`. 24 files, 12833 lines without tests, measured at commit `9bd9849` (2026-11-23). Every claim cites `path#symbol`: the file from the module root, then the declaration in it. `damage_test.go`, `shot_oracle_test.go` and `pace_loop_test.go` hold the client to its behaviour: they build a `tui` (the client model, `internal/client/client_model.go#tui`) with its frame sink `t.out` pointed at a `bytes.Buffer`, and call `draw` or `tick` directly, so no terminal is involved.

| file | owns |
| --- | --- |
| `client_model.go` | the `tui` model, `Run`, `runSession`, the tick, `apply` |
| `client_input.go` | byte decoding, mouse, paste, scrollback, the inline inputs, `processInput` |
| `client_keys.go` | `runAction`, menus, picker, dashboard keys, reload, `action` |
| `client_view.go` | `draw`, the band and row diff, `writeFrame`, `cursorBlock` |
| `pacing.go` | the frame pacer |
| `render_pane.go`, `render_bar.go`, `render_tabbar.go`, `render_toast.go`, `render_modal.go` | pane content, the pane borders, the grips, the divider, the bar, the tab bar, toasts, pickers, help |
| `command_palette.go`, `layout_switcher.go` | the two palettes (a centred list of commands, and of layouts) |
| `client_floats.go`, `client_float.go`, `client_float_cmd.go` | the message view, keymap view, float stack, floating command |
| `client_notify.go` | the notify catch-up, its toasts, the bar badge and the recent cache |
| `client_ask.go` | the ask float: the focused pane's questions, its cursor, its answers and the `ask-answer` send |
| `client_body_reader.go`, `client_clip.go`, `scroll_search.go` | body reader, clipboard, scrollback search |
| `switcher_wire.go` | the dashboard (the full-screen table of every pane in the session, `internal/switcher/switcher.go#Switcher`): open, close, pick |
| `prefs.go` | the choices kept between runs in `prefs.json` under the state directory: load, atomic save, and the sort key's write |

## The loop

`Run` (`internal/client/client_model.go#Run`) sets raw mode, asks the outer terminal (the terminal emulator dm runs in) for its foreground and background colours (OSC 10 and OSC 11) and for its clipboard (OSC 52), starts the stdin reader and the `SIGWINCH` handler, and only then enters the alternate screen with mouse reporting and bracketed paste, a pasted block wrapped in markers. It then calls `runSession` in a loop. A session switch, which is the client leaving this session without ending it, returns a new name and the loop draws again with the same raw terminal.

`runSession` (`internal/client/client_model.go#runSession`) dials the server, sends `attach` (the first message: this client exists, and this is its terminal size), starts one reader goroutine over `wire.ReadMsg`, and selects on four sources: stdin bytes, messages, `SIGWINCH` and the pacer's ticker, the frame timer. A change sets `dirty`, the flag that says the screen needs a frame.

```mermaid
flowchart TD
    src["stdin, wire messages, SIGWINCH, ticker"] --> sel["select in runSession"]
    sel -->|"bytes"| pi["processInput"]
    sel -->|"wire.Msg"| ap["apply"]
    sel -->|"signal or tick"| wake["wake or tick"]
    pi --> wake
    ap --> wake
    wake --> due{"dirty and pacer allows"}
    due -->|"yes"| frame["frame: draw, writeFrame"]
    due -->|"no"| decay["pacer.decay"]
    frame --> off["stdout, one synchronized update"]
```

`wake` (`internal/client/client_model.go#tui.wake`) floors the pacer and draws at once when a frame is due. `tick` (`internal/client/client_model.go#tui.tick`) runs the expiry timers (a toast's lifetime, a stalled paste, a partial OSC reply, the dashboard's preview, a stalled key sequence and an overlay's lone escape), places a quiet pane's pending grapheme cluster, and draws at most one frame. The cluster is the parser's held rune: it waits for the next rune to prove it complete, so a program that writes once and waits would show one character short, and `flushIdle` places it after `idleFlush` (16ms, `internal/client/client_model.go#idleFlush`) of quiet. Both `wake` and `tick` end in `frame` (`internal/client/client_model.go#tui.frame`), which clears `dirty` and tells the pacer whether the frame wrote any bytes. The pacer is a value, not a goroutine (`internal/client/pacing.go#pacer`): it floors the interval at 16ms, doubles it on empty ticks to 250ms, and resets it on a wake, so a burst coalesces and an idle client almost never wakes. The `decay` node in the diagram is `pacer.decay` (`internal/client/pacing.go#pacer.decay`), the empty tick's doubling. A frame is delayed, never dropped: `dirty` is a flag, so the next frame carries the newest state. `apply` updates the model and marks it dirty, and the pacer decides when the pixels follow.

The second diagram is the same loop with the input half expanded. `stdinReader` (`internal/client/client_model.go#stdinReader`) writes each read into `keys`, the reader goroutine (`internal/client/client_model.go#runSession`) writes each message into `msgs` (256 buffered), and `transport.Conn` is the socket with a write mutex (`internal/transport/transport.go#Conn`). The model is `tui`, whose fields fall into the three groups of the next section, and `lastFrame` is the paint memory at `internal/client/client_model.go#lastFrame`.

```mermaid
flowchart TD
    keys["keys chan"] --> pi["processInput"]
    pi --> km["keymap.Match"]
    km -->|"action"| ra["runAction"]
    ra --> conn["transport.Conn"]
    pi --> conn
    conn --> msgs["msgs chan"] --> ap["apply"]
    ap --> t["tui model"]
    t --> draw["draw"]
    draw --> band["lastFrame"]
    draw --> sig["rowsig, laysig (fingerprints)"]
    draw --> out["writeFrame"]
```

## The model a frame is built from

`tui` (`internal/client/client_model.go#tui`) holds three groups.

- The server's truth: `snap` (a `wire.Snapshot` with the pane rects, the rail width, the focused pane, the zoom and the pane list, `internal/wire/wire.go#Snapshot`; zoom is the one pane drawn full screen) and `vts`, one `vt.Term` per visible pane. `hist` holds a pane's replay ring only after a scrollback entry asked for it: scrollback is the history above the live screen, entered by the scrollback action or the wheel, and the replay ring is the raw bytes of that history, which the server forwards untouched. The ring has three names here: the scroll ring the pane's daemon keeps, the replay ring the client receives, and the history ring a scrollback shows. `histMeta` records the rect and whether the reply landed.
- Local overlays: `floats` (a `[]*panel.FloatingPane` stack; a float is a pane-shaped box with its own content, painted over the layout at an arbitrary rectangle, in no tree, `internal/panel/float.go#FloatingPane`), `box` (the message view), `keys` (the keymap view), `lsw` and `cp` (the layout and command palettes), `sw` (the dashboard), `float` (the floating command), `ask` (the ask float, the focused pane's listed questions), `menu` (a row menu), `toasts` and `mode`, the string that says which one owns the keyboard. The `keys` field is not the `keys` channel, and the `float` field is not the `float` mode.
- Paint memory: `last` (`internal/client/client_model.go#tui.last`), one `paint` per band (the bytes a band wrote and the screen rows they cover), `rowsig` (one fingerprint per pane row) and `laysig` (the shape of the picture).

A frame reads nothing from the server: it is computed from those fields alone, which is why every test can draw one without a socket.

## How a frame reaches the terminal

`draw` (`internal/client/client_view.go#tui.draw`) renders every band into its own buffer: the pane borders, the grips, the divider, the rail, the tab bar, the bar, the toasts, the overlays, then the float stack. Each band keeps its last bytes in `last`; a band whose bytes did not change is not written.

Pane content is compared per row. `paneRowSigs` (`internal/client/client_view.go#paneRowSigs`) folds every cell, its colours and attributes, the selection, the hover and the scrollback row into one `uint64` per row, so `draw` repaints a row only when its fingerprint moved. `drawPaneRow` (`internal/client/render_pane.go#drawPaneRow`) writes a row with an absolute cursor move, which is how `bandRows` (`internal/client/client_view.go#bandRows`) recovers the screen rows a band touched. A band that moves the cursor another way breaks the diff, and `TestBandsUseAbsoluteCursorMoves` fails.

`layoutSig` (`internal/client/client_view.go#tui.layoutSig`) names the shape: the size, the rail, the zoom, every pane and float rect, the window list, the active window, the focus, the mode, the scrollback pane and the dashboard rect. When it changes the next frame repaints whole and may clear. The rest is damage: the rows whose fingerprints moved, and the rows an overlay took or left. `rowsHit` and `rowsOverlap` decide which bands must re-ink those rows.

The body ends with `cursorBlock` (`internal/client/client_view.go#tui.cursorBlock`). The frame opens with `ESC[?7l` to turn wrapping off while the body is written, and `ESC[?7h` restores it before `ESC[?2026l` closes the synchronized update that `ESC[?2026h` opened, so the terminal swaps the frame in whole. `writeFrame` (`internal/client/client_view.go#tui.writeFrame`) writes to `t.out`: a counting wrapper over `os.Stdout` in a session, a buffer in a test. Damage routing exists because a full repaint is both slow and visible, and the commit that introduced it says so: "Stop repainting the screen when one cell changed. This is the flicker." (`df1e39a`). A 60Hz client that cleared and repainted would flicker under every keystroke and through a nested pty: a pane's program runs on a pty, and that program can be dm. `TestFrameSteadyStateWritesNothing` and `TestFrameRepaintsOneRow` pin the win.

## Input decoding and the keymap handoff

`processInput` (`internal/client/client_input.go#tui.processInput`) is the only decoder. It works on `t.inbuf` and loops until the buffer stops making progress, so one read can carry a paste, a mouse event and a keystroke in any order. The order is fixed.

1. An OSC 10, 11 or 52 reply: the terminal's foreground colour, its background colour, or its clipboard. It never reaches a pane. A partial one waits up to 500ms (`pasteTimeout`, `internal/client/client_model.go#pasteTimeout`), then falls through to the steps below as ordinary input.
2. The dashboard, the command palette, the help view, the message view and the float, while each is up. Those own every key.
3. A bracketed paste, framed or bare (framed when the pane's program asked for bracketed paste, so the text arrives inside `ESC[200~` and `ESC[201~`; bare otherwise, `internal/wire/wire.go#PaneInfo.Paste`), which closes an overlay and goes to the focused pane as one `keys` message, or a mouse event via `parseMouse`, dispatched by `handleMouse` (`internal/client/client_input.go#tui.handleMouse`).
4. The mode handlers: `rename`, `search`, `sessions`, `menu`, `layout`, `scroll`. `menu` is the row menu: a right press on a pane or a rail row opens a list of actions at that point (`openMenu`, `internal/client/client_keys.go#tui.openMenu`, `openRailMenu` at `internal/client/client_keys.go#tui.openRailMenu`).
5. The prefix: the key that puts the client in front of the pane, `ctrl+b` in the default config (`internal/keymap/config_parse.go#defaultConfigKDL`). A prefix in the buffer arms the client (`armed`, `internal/client/client_keys.go#tui.arm`) and the bytes before it go to the pane. `expirePrefix` (`internal/client/client_input.go#tui.expirePrefix`) sends a stalled prefix to the pane as plain input.
6. Armed: `armedKey` (`internal/client/client_input.go#tui.armedKey`) asks the keymap for one action.

The handoff is one call: `t.km().Match("main", buf)` (`internal/keymap/config_model.go#Config.Match`). `"main"` is the keymap block name the client's own keys live in, and `km` is the keymap loaded from the config file `$XDG_CONFIG_HOME/dm/config.kdl` (written with the defaults on first run), or the built-in one when the file is missing. The call returns the action, the bytes consumed, and `Match`, `Partial` or `NoMatch`. `Partial` keeps the bytes and re-asks on the next read; `expireArmed` (`internal/client/client_input.go#tui.expireArmed`) drops a stalled sequence after `escTimeout` (40ms, `internal/client/client_model.go#escTimeout`). A matched action goes to `runAction` (`internal/client/client_keys.go#tui.runAction`), which either acts locally (`openBoxView`, `t.quit = true`, `disarm`) or sends a request through the functions `action` or `actionOn` (`internal/client/client_keys.go#tui.action`, `internal/client/client_keys.go#tui.actionOn`). Those two are functions, not the keymap action and not the `action` field of a request.

An unbound key notifies and disarms: it never reaches a pane, because dm is not a passthrough for keys it has claimed. Most overlays resolve keys through a keymap mode: `menu`, `picker`, `switcher`, `layout`, `scroll`, `rename`, `box` (the message view) and `help`. Two keymap mode names differ from the client's `mode` strings: the sessions picker is keymap mode `picker` and client mode `sessions`, and the dashboard is `switcher` and `dashboard`. The rest share one name: `menu`, `layout`, `scroll`, `rename`, `box` and `help`. The command palette has neither a `mode` nor a keymap mode, and the floating command and the ask float read their keys by hand, inside mode `float` and mode `ask`. A config file that predates a mode falls back to the built-in keymap, as `keymapKey` does for `help` (`internal/client/client_floats.go#tui.keymapKey`).

## Overlays and the float layer

Every overlay is one field, one open and close pair, one key handler, one paint function. The open sets a `mode` or a pointer, clears `armed`, drops buffered input and marks the frame dirty. The close clears both and drops `inbuf`, so a late escape never reaches a pane. The mode column is the client's `mode` string; the last column is the handler function, not a key.

| overlay | opened by | mode | key handler |
| --- | --- | --- | --- |
| message view | `openBoxView` (`internal/client/client_floats.go#openBoxView`) | `box` | `boxKey` |
| keymap view | `openKeymapView` (`internal/client/client_floats.go#openKeymapView`) | `help` | `keymapKey` |
| sessions picker | `openPicker` (`internal/client/client_input.go#tui.openPicker`) | `sessions` | `pickerKey` |
| command palette | `openCommandPalette` (`internal/client/command_palette.go#openCommandPalette`) | none | `commandPaletteKey` |
| layout palette | `openLayoutSwitcher` (`internal/client/layout_switcher.go#openLayoutSwitcher`) | `layout` | `layoutKey` |
| row menu | `openRailMenu` (`internal/client/client_keys.go#tui.openRailMenu`) | `menu` | `menuKey` |
| dashboard | `openSwitcher` (`internal/client/switcher_wire.go#openSwitcher`) | `dashboard` when full screen | `switcherKey` |
| floating command | `openFloatFor` (`internal/client/client_float_cmd.go#tui.openFloatFor`) | `float` | `floatKey` |
| scrollback | `startScroll` (`internal/client/client_input.go#tui.startScroll`) | `scroll` | `scrollKey` |
| rename prompt (a one-line input) | `openRenamePrompt` (`internal/client/client_input.go#tui.openRenamePrompt`) | `rename` | `renameKey` |
| ask float | `syncAsk` on a snapshot (`client_ask.go`) | `ask` | `askKey` |

The ask float is the one overlay nobody opens with a key: `syncAsk` (`client_ask.go`) runs on every snapshot, opens
the float on the focused pane's listed ask (`Snap.Ask`, `internal/wire/wire.go#Snapshot.Ask`), refreshes it, and closes it
when the ask is answered or gone. An ask that arrives while another overlay owns the keyboard waits
(`askDeferred`), and each overlay close re-checks, so a question never paints over a desk in use. The float shows
one question at a time and draws it from the snapshot alone: the header, the question, each option with its
description and preview, and on a single-select question a last row that opens the one-line input for a typed
answer (mode `rename` with kind `ask`, so the input has one implementation). A question on a pane that is not
focused paints no float at all: that pane's rail row carries a `?` badge, filled from the same ask list
(`rail.RailItem.Ask`). `enter` sends `ask-answer` with the items and `esc` sends it with `cancel:true`; the reply is
the daemon's verdict, and a refusal leaves the float open with the code on it (`takeAskAnswer`).

Two more keyboard owners are sub-modes, not overlays: the scrollback search (`openSearch`, `internal/client/client_input.go#tui.openSearch`, mode `search`) and the prefix help box (`arm`, `internal/client/client_keys.go#tui.arm`, no mode). Two overlays differ from the rest: the dashboard and the command palette are checked by pointer, not by `mode` (`processInput` tests `t.sw` and `t.cp` before it looks at `mode`), so they take the keyboard first. Every other overlay is checked by its `mode` string, which is the `mode` column above. The dashboard is the full-screen table of every pane in the session, with a preview band for the selected pane under the table or beside it on the right, and it takes its own `mode` only when it is full screen, because as a palette it leaves the panes live under it (`openSwitcher`, `internal/client/switcher_wire.go#openSwitcher`). Its ordering and the band's side are read when it opens: the side from the `dashboard-peek` node and the ordering from `prefs.json`, which the sort key writes best effort, so a restart opens in the mode the operator left. Shelved panes sit in their own section under a shelf header, and a pane with an open question leads its agent cell with the ask mark. It also keeps its own preview terminals (`swVT`) for panes outside the active window, filled by `screen` replies, because those panes have no bytes stream. Scrollback is a mode over a pane, not a float: `startScroll` asks for one `replay` ring and builds a tall `vt.Term` from it (`buildScroll`), a terminal as tall as the pane plus the history window in lines (`history-lines`, 10000 by default). The live pane keeps updating behind it and the tall terminal stays frozen, and `exitScroll` (`internal/client/client_input.go#tui.exitScroll`) drops the view, the offset and any search.

Every screen-owning mode hides the cursor; `cursorBlock` decides that from the mode, the float and the focused pane. `expireMode` (`internal/client/client_input.go#tui.expireMode`) closes any overlay whose lone escape stalled, so a lost byte cannot trap the keyboard. A float is a `panel.FloatingPane` in `t.floats`, painted by the same bands and keyed by id in `last.floats`; a toast is a message that expires after three seconds (`toastLifetime`, `internal/client/client_model.go#toastLifetime`). The floating command is the odd one: the server owns its pty, its bytes arrive as `bytes` messages with that float's id, and its exit arrives as `exit`. The client only routes keys to it, with a target.

## The rail

The rail is server state and client paint: the server publishes the width (`Rail`, `RailWant`) and the panes the rows are built from (`Snapshot.All`, every pane in the session, `internal/wire/wire.go#Snapshot.All`), and enforces everything; the client groups those panes into project, workspace, window and pane rows, and the thin rail keeps one pane row per pane and no group row. It draws them with `rail.DrawRail` (`internal/rail/rail.go#DrawRail`), hit-tests with `rail.RailItemAt` (`internal/rail/rail.go#RailItemAt`) and sends a request for every change. The rail hover is `railHoverRow` (`internal/client/client_input.go#tui.railHoverRow`), which turns the pointer cell into an item row for the paint. The client never reorders the rail locally: it paints the drop marker, the insertion line where a dragged row would land, and waits for the snapshot. A pane row whose pane the session lists a question on carries a `?` badge one cell left of its state, filled from the snapshot's asks, so a question in another window still says so without taking the desk. The full rail gives every row its state glyph in the last content cell, the one before the grip strip: a pane row shows its own state, a window row the worst of its panes, a workspace row the worst of its windows and a project row the worst of its workspaces, and an unknown state never wins a rollup. A count appears only on a folded project or workspace row, where it is the only evidence of what the fold hides, and never on a window row or an open one. A shelf row takes the pane row's shape under a header that counts its members only while the shelf is folded. A window row inside a repository carries a git field of two six cell columns, added lines then removed, one cell of mark and one cell of air before its state. The field stands down whole first and the mark last, so a narrow row elides its label and keeps the one cell that opens the facts box. The mark is painted when the worktree changes or the branch is ahead of or behind its upstream, and on a clean row under the pointer, so the facts box stays reachable.

A pane row carries a git mark of its own only when its repository root differs from its window's source pane, the pane the window row reads: `rail.RailItem.PaneMark` (`internal/rail/rail.go#RailItem`). The mark stands in the cell before the state, paints the same glyph in the same quiet accent, and answers `rail.RailPaneGitMarkAt` (`internal/rail/rail.go#RailPaneGitMarkAt`), which takes the press before the row does, so a click on it opens that pane's facts instead of focusing the pane. The mark drops before the label elides, and the label elides before the state goes, so the state is the last thing a narrow pane row gives up. Panes that share the source pane's repository carry no mark, so a window whose panes sit in one repository paints exactly what it always did.

The thin rail is five content cells and the grip strip (`layout.CollapsedRail`, `internal/layout/layout.go#CollapsedRail`): the project bar, the agent mark, the git cell, the state glyph and the question badge, in that order. The agent mark takes the rail's dim tone, so the state is the one coloured cell that says how the pane is doing; unknown and empty states leave it blank, as the full rail does. The git cell says what the full rail's mark says, and every pane row inside a repository carries one, because the thin rail has no window row to hoist the facts to. `rail.RailPaneGitMarkAt` answers the cell, so a pane's facts are one click away in either rail. The badge finally has a cell of its own rather than landing on the grip strip. The state keeps its column when the rail is stored at a width a narrower build wrote: `layout.EffectiveRail` (`internal/layout/layout.go#EffectiveRail`) lifts a stored positive width below the thin rail up to it, so a saved four opens as today's thin rail and not as a rail too narrow to paint one.

The facts box is keyed by pane, not by window: `gitFloat` (`internal/client/client_git.go#gitFloat`) remembers the pane whose facts it shows, and reads them from the row that pane gets, so the title is the pane's folder and every fact is the pane's own. A window mark opens the box on that window's source pane and hangs it from the window's row; a pane's own mark opens it on the pane and hangs it from the pane's row. One box stands at a time, and it closes when the mark that opened it stops being drawn.

The rail's list can be taller than the sidebar, so the client keeps its own scroll offset in `railOff`, the first item row the paint shows. The wheel over the rail's columns turns it (`railWheel`), `rail.ClampOffset` (`internal/rail/rail.go`) holds it inside the list, and every hit test takes the offset, so a click lands on the row the operator sees. The offset is client view state like the hover, never server state: a snapshot that shortens the list clamps it.

- Toggle: `toggleRail` (`internal/client/client_input.go#tui.toggleRail`) sends `set-rail` with the width `railToggleWidth` picks. `railWant` remembers the last expanded width so a hidden rail comes back at it.
- Grip: the last rail column is the drag strip (`railGripAt`), unrelated to the pane grips. A drag sends `set-rail`, throttled to one request per 30ms and forced on release (`railDragTo`).
- Scroll: a wheel turn over the rail moves its list three rows, in one page of `Rows-2` rows, and clamps at both ends. A drag in flight keeps the list still, so the drop boundary cannot move under the pointer.
- Clicks: a caret, the fold triangle in a project, workspace or window row, sends `toggle-fold`; a row sends `focus` for the first pane the row names; a double click opens the rename prompt; a row drag sends `move-window` or `move-workspace` with the direction; a right press opens the row menu (`openRailMenu`).

## Reconnect and detach

Detach is local: the `detach` action sets `t.quit` and `runSession` returns. The deferred close runs, the terminal modes are restored, and the shell returns. Panes and the session file survive, and the session file is the saved tree, `state.json` under the session's state directory (`paths.StatePath`, `internal/paths/paths.go#StatePathAt`), written by the server. A session switch is the same exit with a name: `pickSession` (`internal/client/client_input.go#tui.pickSession`) sets `switchTo`, and `Run` re-enters `runSession` under the new name with raw mode still on. There is no in-place redial. When the server closes the socket the reader goroutine returns, `msgs` is closed, and the client leaves the alternate screen. The server sends `close` when the last pane ends the session, so that path quits without a read error. To get back in, attach again. Panes keep running across a server restart, and re-attaching rebuilds the client from one snapshot and one screen per pane (`s.attach`, `internal/server/server_view.go#server.attach`). The one resync inside a connection is the server's: a client whose queue overflowed is rebuilt the same way (`resyncClient`, `internal/server/server_view.go#server.resyncClient`), and the client handles it as an ordinary `snapshot` plus `screen` message pair.

## The contract with the server

The client is a view with a request API. It sends four message types.

| sent | carries | meaning |
| --- | --- | --- |
| `attach` | `cols`, `rows` | this client exists and its size counts toward the shared layout, which the server sizes to the largest attached client (`internal/server/server_view.go#server.fitToClientsLocked`) |
| `resize` | `cols`, `rows` | the terminal changed size |
| `keys` | raw `keys`, optional `target` | typed bytes; only the floating command sets a target |
| `req` | `action` plus fields | the requests that change state (focus, split, close, move, zoom, layout, rename, the rail, the plugin and reload requests, `float-open` and `float-close` for the floating command, the secrets switch `toggle-secrets`: on, it masks the value of each registered secret (a credential the secrets store holds an address for) in a pane's screen and history; off, it reveals them) and the reads: `read` (a pane's recent lines), `screen` (a pane's visible screen), `replay` (a pane's history ring), `yank` (copy output) and the `msg-*` family (`msg-list`, `msg-clear`, `msg-watchers`) |

It receives ten types, each handled by `apply` (`internal/client/client_model.go#tui.apply`): `resp` (the reply to a `req`, with its action, an optional id and the result), `snapshot` (the whole chrome: the pane tree, every rect, the focus, the rail width), `screen` (one pane's visible screen, applied by target), `replay` (one pane's raw history ring, applied by target), `scrub` (a pane rewrote its ring and screen after `toggle-secrets` came back on), `bytes` (output for one pane, or for one float), `close` (the session ended), `exit` (a pane or a float ended), `box` (the message log changed) and `watch` (a watcher changed: a watcher is one pane's filtered read of the message log, its cursor is the highest sequence number served to it, and parked means a `msg-wait` (a reader's request that waits for the next matching entry) is outstanding on that filter; a watcher goes when its pane leaves the tree or, unparked, after `boxWatchTTL` (10 minutes, `internal/server/server_box.go#boxWatchTTL`) with no read).

The rules that hold it together:

- The server owns layout, focus, names, rail width and pane life. The client never edits them locally.
- The client numbers its own requests in `wire.Msg.ID` (`yankID` for `yank`, `toggleID` for `toggle-secrets`, `floatID` for `float-open`, `notifyID` for the `msg-*` reads) and matches a reply by action and id, so an older one is dropped and a late failure never toasts over newer state. An unnumbered failure toasts.
- `replay` is asked once per rect and the ring is cached by rect (`histMeta`). A `screen` request carries an id, and the server answers it twice: a `screen` message carrying the picture, which has no id and is applied by target, and a `resp` that the ordinary resp path sees.
- The dashboard re-asks every `previewEvery` (250ms) and caches the terminal by pane in `swVT`.
- The client never writes a pane's screen itself. It feeds the bytes into a local `vt.Term` and paints what the terminal holds.
- Bytes are authoritative. The client never replays bytes into a live terminal; a replay fills the history ring only. The snapshot is the whole truth for the chrome, and a message the client does not know is ignored.

The full action matrix is in [The wire protocol](03-protocol.md).

## Where to add the next thing

A new key action: bind it in the default `keymap "main"` block (`internal/keymap/config_parse.go#defaultConfigKDL`), add a case in `runAction` (`internal/client/client_keys.go#tui.runAction`), and send the request through `action` or `actionOn`. Every case ends the switch, so a case that handles the key locally must call `t.disarm()` itself before it returns. If the server needs an action that does not exist, add it in `handleReq` (`internal/server/server_rpc.go#server.handleReq`) first.

A new overlay: add a field to `tui` (`internal/client/client_model.go#tui`), write `openX` and `closeX`, give it a key handler, add it to the dispatch chain in `processInput` (`internal/client/client_input.go#tui.processInput`) and to `expireMode` (`internal/client/client_input.go#tui.expireMode`), add a `paint` to the `lastFrame` field `last` (`internal/client/client_model.go#lastFrame`), add a band to the `bands` slice (`internal/client/client_view.go#tui.draw`), draw it in a `render_*.go` file, and bind an action that opens it.

A new frame row or band: write a draw function that uses absolute cursor moves, add its `paint` to `last`, add a `band` entry in `draw` with the right `over` list and `overlay` flag, and add an oracle case in `damage_test.go`. `over` holds the indices of the earlier bands this band paints over: when one of them rewrites, this one re-inks the cells it took. If the band never moves independently, it belongs in an existing buffer instead.

## See also

- [Entry point and lifecycle](02-lifecycle.md), [The wire protocol](03-protocol.md), [The server](04-server.md), [The pane](05-pane.md)
- [Layout](07-layout.md), [The box and the notify bus](08-surfaces.md), [Secrets](09-secrets.md), [Config and keymap](10-config.md), [Layers](11-layers.md)
- [Asks](../../README.md#asks) for the feature from the operator's side: the three surfaces and the pi setup.
