# dm in a browser

A spike, not a feature. It answers one question: does `internal/client`, which
owns a terminal on a real machine, run with no terminal at all?

The client is the shipped one. `client.Attach` is the seam: it takes a
connection, a writer, a key stream, a resize channel and a size function, and
runs one attached session over them. `Run` keeps its terminal and calls the
same code.

What is fake is the session on the other end. The model is real enough for a
tour: workspaces `s1` and `s2` holding windows `w1`, `w2`, ..., each window with
its own layout tree, focus, preset and synchronize flag, and a pretend shell in
every pane (`w1p1`, `w1p2`, ...). There is no pty, no pane daemon, no server and
no state file. The fake lives in `internal/websession` and answers the real wire
messages; `main_js.go` keeps only the page bridge.

It serves new-window and new-workspace (naming no argv or cwd), select-window by
index, next and previous window, next and previous workspace, close-window and
close-workspace, focus on any pane in the session, the rail's fold triangles for
a window or a workspace, the rail width, splits in four directions, close pane,
zoom, swap by direction, next and previous pane, synchronize panes, and the five
layout presets including the cycle. A focus on a pane in another window or
workspace makes both active, so a rail click, a tab click and a key agree.

A close never ends the session the tour is teaching: the last window, the last
pane and the last workspace are refused. Anything else is still unserved and
answers with an empty reply.

The fake uses the same helpers the server uses rather than its own arithmetic:
`layout.SplitLeaf`, `layout.PruneLeaf`, `layout.Swap`, `layout.Neighbor` and
`layout.ApplyPreset`. Pane ids only move forward per window, as on a machine.
Where a helper does not exist, that is a sign the fake is standing in for
something that belongs in the tree.

## Scrollback

Each pane keeps a bounded raw ring of the bytes it printed, 512 KiB, the way a
pane daemon does. A resize repaint goes into the ring too, because those bytes
really printed. A `replay` request returns the ring for a pane, and the client
builds scrollback by replaying it through `vt.ReplayRows` at the pane's own
rectangle. After enough output a wheel into history shows rows the screen no
longer holds, and the replay ends with the live screen.

## The lesson

The track and its rules live in `internal/lesson`, in Go, and the page holds
none of it. `lesson.Start(track)` is fed every event, `Feed` reports whether a
step finished, and the program pushes `lesson.step` or `lesson.finish` for the
page to draw. A step's rule names an action, a command or a pane count, and a
track can therefore be played in a Go test with no browser at all.

## Events, for a lesson

`dm.onEvent(fn)` receives what the operator did, as the session sees it:

| Event | Data | When |
| --- | --- | --- |
| `action` | `{name}` | a keybinding action ran, such as `split-right` or `focus-left` |
| `command` | `{line}` | the pretend shell answered a line |
| `state` | `{panes, focus, rail, zoom, sync}` | the session changed shape |

Three sources, one stream: `action` from the client, which is the only place
that knows which action a key ran, `state` from the client once per change, and
`command` from the fake shell. A step is data on the page and a check against these events, as the
three-step card in `index.html` shows. Events that arrive before the page
registers are held and delivered in order, and an event with no reader is
dropped so a slow card never stalls the session.

The page turns on the mouse modes, so a click on a rail row reaches the client
the way it does on a machine. `dm.last()` returns the last request the fake
handled, which is how a click can be traced to the message it sent.

The pretend shell is `internal/webshell`: a line buffer over a small file tree,
with `echo`, `clear`, `help`, `pwd`, `cd`, `ls`, `cat` and three git verbs. There
is one project, `~/projects/hello`, a Go repository with four commits and a
modified `greet.go`, and every command reads the same two maps, so `cd`, `ls`,
`cat` and `git status` cannot contradict each other. Anything else is refused:
`git push` says it is not part of the pretend repository, and a command that is
not there is not found. A half working command reads as broken, a refusal reads
as a limit.

The shell keeps its screen as a list of lines, and a pane draws it again
whenever the window changes size. A terminal cannot rewrap bytes it already
printed, so without that a split clipped the right half of every line and a zoom
never brought it back. This is the one place the pretend shell does more work
than a real one: a real shell repaints because the program behind it gets
SIGWINCH, and here the shell owns the screen.

`ctrl+c`, `ctrl+d`, `ctrl+l` and `ctrl+u` do what they do at a live prompt,
except that `ctrl+d` prints `exit` and hands back a prompt, because the pane
must not end. Arrow keys are swallowed whole rather than left on the line. The
package has no build tags and is tested natively, which is where a fixture
belongs.

Build it:

```sh
cmd/dm-wasm/build.sh out
python3 -m http.server -d out 8765
```

Open <http://127.0.0.1:8765>. Type, press `ctrl+b`, watch the prefix panel. The
page loads xterm.js from a CDN, so it needs the network; the program needs
nothing but the files in `out`.

The page gives the terminal the keyboard at boot and after any click on the page, because
a browser takes `ctrl+b` for its own bookmarks bar otherwise and the prefix never reaches
the client. The bar under the screen says when the browser holds the keys instead.

The page plays the first track. Name another one by its id, for example
<http://127.0.0.1:8765/?track=windows>, until the page grows a track list.

## What the seam is

| Real machine | Here |
| --- | --- |
| `transport.Dial` opens a unix socket | the harness passes a `net.Pipe` end |
| the pty supplies keys and size | xterm.js supplies both |
| `SIGWINCH` | the page sends on the resize channel |
| `os.Stdout` | a `js.FuncOf` callback into the page |

Two build-tagged files make the package compile for `js/wasm`:
`internal/transport/detach_js.go` (there is no process to detach) and
`internal/client/resize_js.go` (there is no `SIGWINCH`). Everything else is the
shipped code.

## What this does not prove

The pane, server and session packages are not in the wasm yet. `creack/pty` and
`syscall.SysProcAttr.Setsid` do not build for js, so the next seams are the pty,
the pane's own `net.Listen("unix")`, and the state file. The fake persists
nothing, and it does not move panes or windows, rename anything, or answer the
separate list requests. Its tests drive the session over the wire and are named
for what they prove.
