# The server

One server process per session accepts the wire, owns the state and runs the pane daemons. The wire
is one length-prefixed JSON envelope per call, `wire.Msg` (`internal/wire/wire.go#Msg`), whose `Type`
names the frame. This page covers what a session is, what the server owns, the twelve files, and
where to add code. A citation is `path#symbol`: the file from the module root, then the
declaration in it, and a bare `server_core.go` means `internal/server/server_core.go`.

A session is one named tree of panes with its own directory, `sessions/<name>` in the state root,
`~/.local/state/dm` (`internal/paths/paths.go#StateDir`), holding `state.json`, `server.lock`, the
server's socket and one socket per pane. The name defaults to `main`; the operator is the human who
attaches a client. One server per session: `Run` takes a `flock` on that lock.

## What the server owns

`Run` (`internal/server/server_core.go#Run`) starts the session's server and holds `s.mu`
(`internal/server/server_core.go#server.mu`), the main mutex. Four levels: a session holds workspaces; a workspace is a
named group of windows, optionally tagged with a project (a label several workspaces share); a
window holds a split tree of panes; a pane is one leaf; a pane daemon is the process behind it.

- The state, `s.st`: the tree, the focus (the pane keys go to), the rail (the sidebar), rail folds
  (a group collapsed) and accents (one hue per project), each pane's saved record, the box (the
  session's append-only message log) and the secret registry (names and fetch tools, never values).
  `State.Save` (`internal/session/state.go#State.Save`) writes it.
- Clients in `s.clients`, pane connections in `s.panes`, and the spawn, adopt and watch logic.
  Adopting a pane is dialling its daemon's socket and registering it, which is how a restarted
  server recovers a pane that outlived it.
- The shared view: the largest client's size (`fitToClientsLocked`,
  `internal/server/server_view.go#server.fitToClientsLocked`) and the snapshot (`snapshotLocked`). Every client draws the
  same layout, so the largest terminal sets the size.
- Subprocesses a client cannot start: pane daemons, floats (a command the client opens in a floating
  panel of its own, which it types into and which dies with it) and plugin panes (a pane a `plugin`
  declaration in the operator's config file launches).

## Concurrency

- One goroutine per client connection (`go s.serve(c)`, `internal/server/server_core.go#Run`), one writer per client
  (`writeLoop`, `internal/server/server_core.go#client.writeLoop`), one watcher per pane (`watcherLoop`, `internal/server/server_core.go#server.watcherLoop`). A
  watcher reads one pane's connection, fans its bytes out and re-dials when it dies. Three
  connections in a row that die before one survives two seconds mean the pane is wedged: the watcher
  stops, the pane stays registered, and the next write, resize or secret action re-arms it.

| lock | serialises | never held across |
| --- | --- | --- |
| `client.mu` | one client's queue, bounded at 1 MiB | a socket write |
| `paneConn.mu` | one frame to one pane's socket | a write over 5s (`paneSendTimeout`) |
| `s.mu` | state, panes, clients, floats, watches | a send, a dial, a spawn |
| `s.secretPush`, `s.branchMu` | the push that tells each pane which values to mask, and the git cache | any blocking call |

`...Locked` functions need `s.mu` and must not send, dial or spawn. `broadcastLocked`
(`internal/server/server_core.go#server.broadcastLocked`) enqueues via `client.deliver` (`internal/server/server_core.go#client.deliver`), so a stalled client
never blocks a pane read. Past the 1 MiB cap the client is marked stale and rebuilt from a snapshot
and a screen per pane (`resyncClient`, `internal/server/server_view.go#server.resyncClient`); a screen is a pane's terminal picture.

## The thirteen files

| file | owns |
| --- | --- |
| `server_core.go` | `server` and `client` types, `Run`, spawn, adopt, pane watcher, broadcast |
| `server_rpc.go` | `handleReq` (`internal/server/server_rpc.go#server.handleReq`), the action switch |
| `server_mutate.go` | layout changes, `mutation`, `resync`, `writeTo`, `sendKeys` |
| `server_view.go` | snapshot, size fit, `syncSizes`, screens, `reply`, `serve` |
| `server_box.go` | box writes, waiters, subscribers, watches, trimming |
| `server_ask.go` | the ask store: open, await, answer, close, list, the store's cap and pane prune, and the snapshot's flattened asks |
| `server_secrets.go` | registry, activation, proxy, cover (other sessions), type (into a pane), hydrate (to a file) |
| `server_sync.go` | the `.dm-secrets.kdl` file the `secret-sync` action is pointed at: secrets to register, files to ingest |
| `server_float.go` | floats and their lifetime |
| `server_labels.go` | git branch and uncommitted line counts |
| `server_plugin.go` | plugin declarations and the launch context |
| `server_state.go` | which accent hue each project gets |
| `notify_server.go` | notify producers and the operator-focus test |

## Component graph

```mermaid
flowchart LR
    C["client TUI"] -->|"attach, keys, resize, req"| S["server"]
    S -->|"snapshot, screen, bytes, box, exit"| C
    S -->|"write, resize, set-secrets, kill"| P["pane daemon"]
    P -->|"bytes, scrub (its screen after masking), status, cwd, title, exit"| S
    S -->|"Save"| ST["state.json"]
    S -->|"append, trim, wake"| B["box log (the box inside state.json)"]
```

## RPC handling

`serve` (`internal/server/server_view.go#server.serve`) switches on `Type`: `attach` registers, `keys` writes to a pane or a
float, `resize` refits, and `req` goes to `handleReq` (`internal/server/server_rpc.go#server.handleReq`), which switches on
`Action`: 71 `case` clauses, 76 actions, five cases naming two each. A branch answers through
`s.reply` (`internal/server/server_view.go#server.reply`); the `msg-sub` action streams box entries and never answers.

## Mutation

`mutation` (`internal/server/server_mutate.go#server.mutation`) runs the change function without holding `s.mu`, then takes it
to save. The change function locks `s.mu` where it needs it: `split` creates a pane, and a spawn
must not hold the lock. `afterMutation` (`internal/server/server_mutate.go#server.afterMutation`) drops the zoom (a pane shown alone
at full window size) and calls `resync` (`internal/server/server_mutate.go#server.resync`): resize, one snapshot, then a
screen and a replay (the pane's scrollback) per pane of the active window, the only one on screen. A
change that only repaints (a focus, a rename, a status) calls `pushSnapshot` (`internal/server/server_view.go#server.pushSnapshot`)
instead. A new pane, window or workspace id comes from the state (`internal/session/state.go#State.NewWorkspaceID`);
a client-supplied name goes through `checkName` (`internal/server/server_mutate.go#checkName`): 128 bytes, valid UTF-8, no
control characters, because it lands in `state.json`.

## View and snapshot sync

`snapshotLocked` (`internal/server/server_view.go#server.snapshotLocked`) is the client's whole picture: the session, workspace,
windows, focus, size, rail, zoom, padding, the tree and one `PaneInfo` per pane, the tree cloned so
no marshal races a split. It serialises as `wire.Snapshot` (`internal/wire/wire.go#Snapshot`).

`paneRectsLocked` (`internal/server/server_view.go#server.paneRectsLocked`) turns the tree into one rectangle per pane through
`layout.Layout` and `layout.InsetBox`, from the screen size, the rail and the padding (the margin a
pane keeps inside its rectangle). `syncSizes` (`internal/server/server_view.go#server.syncSizes`) writes each rectangle into its
pane state and sends a `resize`. Only a width change re-wraps lines, so a settle (`armReflowLocked`,
`internal/server/server_view.go#server.armReflowLocked`, `reflowSettle` after 150ms of quiet) asks each widened pane for a screen.

## The box

The box is the session's append-only log: one `box.Entry` (`internal/box/box.go#Entry`) per post.
`boxStoreLocked` (`internal/server/server_box.go#server.boxStoreLocked`) stamps `seq` and `at`, its number and time, trims the log,
wakes the waiters and saves, then broadcasts `{"box"}` so a view refetches. The `msg-wait` action
parks a waiter (`boxWaiter`, `internal/server/server_box.go#boxWaiter`) on the log and re-checks under `s.mu`.

## Notify

`notify.StateChange` (`internal/notify/notify.go#StateChange`) runs when the pane watcher reads a `status`
frame (`internal/server/server_core.go#server.watchConn`) and decides whether the pane's status moved into blocked, done or
error from a different state. `notifyBoxEntryLocked` (`internal/server/server_box.go#server.notifyBoxEntryLocked`) then decides whether the
entry also raises a desktop notification: the caller's suppress flag, computed by
`operatorSeesLocked` (`internal/server/notify_server.go#server.operatorSeesLocked`), true when a client is attached and focused there, and
`notify.Urgent` (`internal/notify/notify.go#Urgent`), which allows only blocked and error.

## Secrets and proxy registration

`secretUse` (`internal/server/server_secrets.go#server.secretUse`) fetches a secret's value and activates it
through `secretActivate` (`internal/server/server_secrets.go#server.secretActivate`), which checks the registry record's identity under `s.mu` so a
raced revoke wins. With an upstream (a host the value is spent on), it mints a URL prefix through
`secretProxyRegister` (`internal/server/server_secrets.go#server.secretProxyRegister`), which starts the loopback proxy: an HTTP proxy on 127.0.0.1 that
forwards a request under it to the upstream with the value in the header; the `secretUse` reply
names that prefix as the endpoint. The other uses are `secretHydrate` (`internal/server/server_secrets.go#server.secretHydrate`), which writes it to
the file `--to` names at 0600, and `secretType` (`internal/server/server_secrets.go#server.secretType`), which types it into a pane. The adopt send
(`adoptSecretScrub`, `internal/server/server_secrets.go#server.adoptSecretScrub`) is the mask list (a `set-secrets` frame) an adopted pane gets, even
when empty: it cues the pane to start the shell. `pushSecretScrub` (`internal/server/server_secrets.go#server.pushSecretScrub`) reaches every adopted
pane and every sibling session's server, which masks the value in panes this one cannot reach.

## Reload

`reloadConfig` (`internal/server/server_core.go#server.reloadConfig`) re-reads the operator's config file,
`$XDG_CONFIG_HOME/dm/config.kdl` or `~/.config/dm/config.kdl` (`internal/keymap/config_load.go#defaultConfigPath`),
swaps `s.cfg` and pushes the new history budget to every pane: how much scrollback each keeps. Only
the `reload` action calls it (`internal/server/server_rpc.go#server.handleReq`). The other keys are read where used: padding
(`internal/server/server_rpc.go#server.handleReq`), the diff command a float runs when it names none (`internal/server/server_rpc.go#server.handleReq`), plugins
(`internal/server/server_plugin.go#server.plugin`); `default-fold`, a restored window's rail fold, only at startup
(`applyDefaultFold`, `internal/server/server_core.go#applyDefaultFold`).

## What the server does not do

It does not parse terminal output, keep scrollback, redact text or render: the pane and the client
do. It does not start the proxy early, watch a file or reap a wedged pane; it posts a notify entry
and waits (`notifyPaneWedge`, `internal/server/server_core.go#server.notifyPaneWedge`).

## Where the next thing goes

- Next action. Add the field to `wire.Msg`, then a `case` to `handleReq` (`internal/server/server_rpc.go#server.handleReq`) that
  answers through `s.reply`. Wrap a state change in `s.mutation` (`internal/server/server_mutate.go#server.mutation`), then call
  `s.afterMutation` (`internal/server/server_mutate.go#server.afterMutation`) or `s.pushSnapshot`.
- Next store field. Add it to `session.State` (`internal/session/state.go#State`), write it in the
  mutator, and let `State.Save` persist it. A client-visible field also goes to `wire.Snapshot` and
  `snapshotLocked`.
- Next broadcast. Call `s.broadcastLocked` (`internal/server/server_core.go#server.broadcastLocked`) under `s.mu` with a new `Type`,
  and read it in `apply` (`internal/client/client_model.go#tui.apply`); a frame one client asked for goes
  through `cl.deliver` (`internal/server/server_core.go#client.deliver`), where `cl` is that client.

## See also

- [Entry point](01-entry-point.md), [Lifecycle](02-lifecycle.md), [The protocol](03-protocol.md)
- [The pane](05-pane.md), [The client](06-client.md), [Layout](07-layout.md)
- [Surfaces](08-surfaces.md), [Secrets](09-secrets.md), [Config and CLI](10-config.md)
