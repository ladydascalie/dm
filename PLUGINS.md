# dm plugins

This page is the v1 contract for dm plugins. Read it before you write one.

## What a plugin is

A plugin is a program in a pane. dm starts it, hands it a PTY and an invocation,
and gets out of the way. There is no SDK and no ABI: the `dm` CLI and the message
log are the whole API. A plugin can be a shell script, a TUI, a Go binary or
anything else that runs.

A plugin can:

- open beside the focused pane, or in a new window
- start from a key binding or from `dm plugin start`
- run the whole CLI, from `dm exec` to `dm msg`
- keep durable state in `DM_PLUGIN_STATE_DIR`
- report status on OSC 7501, and its structured session file on OSC 7502
- survive a client detach and a server restart

A plugin cannot:

- open in a floating pane. A floating pane ends with the client that opened it.
- set a `DM_` environment name. dm sets those.
- be stopped by a command. There is no `dm plugin stop`. Close the pane instead.
- be sandboxed or reviewed. dm validates the declaration and nothing else.
- get its declaration re-read on respawn. The pane records its argv at launch.
- be restarted after a crash. dm has no restart policy.

## Declare it

Add a `plugin` block to `$XDG_CONFIG_HOME/dm/config.kdl`:

```kdl
plugin "git-rail" {
    exec "/home/you/rail/run" "--watch"
    cwd "/home/you/rail"
    where "split"
    env "RAIL_DEPTH=2"
}
```

| key | value |
| --- | --- |
| `exec` | Required. The argv, program first. The program is an absolute path, and dm runs no shell, so there is no quoting and no expansion. A word may not be empty, and may not contain a comma or a NUL. The comma is the separator the pane state and the daemon flag already use. |
| `cwd` | The absolute folder the program runs in. Absent means the invoking pane's folder. |
| `where` | Required. `split` or `window`. dm refuses a floating pane, because a floating pane ends with the client that opened it and a plugin outlives a detach. |
| `env` | Optional `KEY=VALUE`, for the plugin's own configuration. dm applies it after the pane allowlist. A `DM_` name is refused, because dm sets those. A duplicate key is refused. |

Bind it in a keymap:

```kdl
keymap "main" {
    "plugin:git-rail" "g"
}
```

A binding to a name the same file does not declare fails at load, with the line.
The key and the action are separate. The binding names the plugin, not its argv.

From a shell:

```sh
dm plugin list
dm plugin start git-rail [--split|--window] [--target w1p1]
```

`dm plugin start` defaults to the declaration's `where` and the focused pane. The
server resolves the name from the config it already loads. Edit the file, reload
(`prefix ctrl+r` in the client), and the next launch sees it.

## What the pane gets

Every pane gets `DM_SESSION`, `DM_PANE` and `DM_STATE`. A plugin pane also gets:

| Name | Value |
| --- | --- |
| `DM_PLUGIN_ID` | the declared name |
| `DM_PLUGIN_ROOT` | the folder holding `exec` |
| `DM_PLUGIN_STATE_DIR` | `<state root>/plugins/<id>`, created at 0700 |
| `DM_PLUGIN_CONTEXT_JSON` | the invocation, below |

Keep durable state in `DM_PLUGIN_STATE_DIR`, not in `DM_PLUGIN_ROOT`. The root
is a checkout and belongs to the source. The state directory is yours: dm
creates it once, writes nothing else there and never deletes it.

The context is one JSON object, fixed at launch:

```json
{
  "session": "main",
  "source": "key",
  "plugin": "git-rail",
  "workspace": {"id": "s1", "name": "dm"},
  "window": {"id": "w1", "name": "shell"},
  "pane": {"id": "w1p1", "cwd": "/home/you/src/dm", "agent": "pi",
           "app": "pi", "status": "working"}
}
```

`source` is `key` when a keymap started it, `cli` from `dm plugin start`, and
absent from a raw `dm exec`. Every field is optional. The context is a snapshot.
A long-lived plugin reads `dm list --json`, `dm status` and `dm wait` for the
present.

## Plugin lifecycle

- The pane is server-owned. A live pane is adopted after a server restart, and
  a dead one respawns with the argv recorded at launch.
- Close the pane to stop the plugin.
- Updating the declaration does not change a running pane. Relaunch it.
- A plugin that must survive its own crash runs under a wrapper loop in the
  same pane.
- The plugin's `dm` calls fail while the server restarts. A `dm msg sub`
  connection ends, and the plugin re-establishes it from the last `seq`.

## Talk to the session

A plugin has the whole CLI: `dm exec`, `dm msg ...`, `dm read`, `dm write-to`,
`dm list`, `dm status`, `dm wait`. Run `dm llms` for the one-pass reference.

The message log is the event bus. `dm msg sub --prefix notify/ --json` streams
pane state changes until you kill it. It redials when the connection drops. The
one producer is `notify/<pane>`. For the plugin's own traffic:

| topic | use |
| --- | --- |
| `plugin/<name>/...` | the namespace the plugin reserves |
| `plugin/<name>/state` | a status board: the newest entry wins |
| `plugin/<name>/cmd/...` | a command channel |
| `plugin/<name>/events` | a stream |

One message log serves a session. It is append-only, capped at 1000 entries and
256 KiB. `seq` only grows. A reader keeps the last `seq` in its own state
directory and resumes with `--after`. Post on change, not on tick.

To report status to the sidebar, the status bar and the notifications, speak
OSC 7501:

```
ESC ] 7501 ; state=working:app=myapp:msg=<base64> ESC \
```

The README section "OSC 7501 agent status" has the full grammar.

A program can also name the session file it writes, so a viewer reads the
structured transcript instead of the screen. Speak OSC 7502:

```
ESC ] 7502 ; session=<base64 path> ; id=<id> ESC \
```

- `session=` is the path of the JSONL file the agent writes, base64 UTF-8
  because a path holds characters the frame cannot carry.
- `id=` is the session id in that file.
- `BEL` and `ST` terminate the sequence. The pane consumes the frame, so it
  never reaches the screen.
- A frame has three outcomes. A `session=` value that decodes names a session.
  An empty `session=` value clears it, because the agent that wrote the file is
  gone. A frame with no `session` key at all, or a value that does not decode, is
  ignored. The pane keeps the last good value.
- Send the clear frame when your agent exits. If you do not, the pane keeps the
  session it was given, and a viewer paints a transcript over a bare shell
  prompt.
- A session belongs to the running pane. dm never persists it. A restart or a
  respawn means the program starts a new session.
- A program is free to emit nothing. A shell pane has no session, and that is
  normal: a viewer falls back to the screen.

## Worked example

`examples/echo-plugin/echo.sh` posts `ready`, then echoes every command posted
to `plugin/echo/cmd/...` back on `plugin/echo/state`. It uses `dm` from `PATH`
and `jq`.

Make it executable, then declare it. The path must be absolute:

```kdl
plugin "echo" {
    exec "/home/you/src/dm/examples/echo-plugin/echo.sh"
    where "split"
}
keymap "main" {
    "plugin:echo" "e"
}
```

Reload, start it, and drive it:

```sh
dm plugin start echo
dm msg post --key plugin/echo/cmd/one --state note --msg hello
dm msg list --prefix plugin/echo/
```

The pane prints nothing but the shell's own output. The conversation is on the
message log, where any pane, agent or script can join it.

## Trust and limits

A plugin runs as you, with your environment and your access. dm validates the
declaration and nothing else. It does not sandbox or review plugin code. Install
or write only what you trust.

Known limits:

- A plugin's pane is a real pane. The layout holds it, and closing it ends the
  plugin.
- The message log is shared with the agents in other panes. A chatty plugin
  evicts their history.
- The context is a launch snapshot, not a live view.
- The pane's argv is pinned in `state.json`.
- A host-rendered surface, a status bar cell or a floating pane over a
  message-log topic, is not built. v1 gives the plugin its pane.
