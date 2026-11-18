# dm in a browser

A spike, not a feature. It answers one question: does `internal/client`, which
owns a terminal on a real machine, run with no terminal at all?

The client is the shipped one. `client.Attach` is the seam: it takes a
connection, a writer, a key stream, a resize channel and a size function, and
runs one attached session over them. `Run` keeps its terminal and calls the
same code.

What is fake is the session on the other end. One window, several panes, a
pretend shell in each, no pty, no pane daemon, no server and no state file. The
fake lives in `main_js.go` and answers the real wire messages. It serves splits
in four directions, focus by direction or by rail row, next and previous pane,
close pane, zoom, swap by direction, synchronize panes, the rail's fold
triangles, the rail width, and the five layout presets including the cycle. New
window and new workspace are not served yet: they answer with an empty reply
and change nothing.

The fake uses the same helpers the server uses rather than its own arithmetic:
`layout.SplitLeaf`, `layout.PruneLeaf`, `layout.Swap`, `layout.Neighbor` and
`layout.ApplyPreset`. Where a helper does not exist, that is a sign the fake is
standing in for something that belongs in the tree.

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
the pane's own `net.Listen("unix")`, and the state file. Nothing here is a
lesson, an event stream or a fake shell worth teaching: a tour needs those, and
they do not exist yet.