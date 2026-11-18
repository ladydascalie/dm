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
in four directions, focus by direction, next and previous pane, and close pane.
Zoom, swap, cycle layout, and new window or workspace are not served yet: they
answer with an empty reply and change nothing.

The pretend shell has a line buffer and four commands: `echo`, `clear`, `help`,
and anything else is refused as not found. `ctrl+c`, `ctrl+d`, `ctrl+l` and
`ctrl+u` do what they do at a live prompt, except that `ctrl+d` prints `exit`
and hands back a prompt, because the pane must not end. Arrow keys are
swallowed whole rather than left on the line.

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