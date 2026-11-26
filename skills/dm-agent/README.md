# dm-agent

A pi extension that registers four tools over dm's own CLI surface: `dm_layout`,
`dm_pane`, `dm_box` and `dm_turn`. Each tool carries one action enum. Each
action runs one `dm` command through `pi.exec` and returns the JSON reply. No
action reads a terminal screen or parses prose.

Verb and flag names come from `internal/clidoc/clidoc.go`. Reply shapes come
from `README.md`, under RPC and The box.

## Install

Symlink the extension where pi loads extensions:

    ln -sf "$PWD/skills/dm-agent/dm-agent.ts" ~/.pi/agent/extensions/dm-agent.ts

`dm` must be on `PATH`. Every action uses the session named by `session`, else
`DM_SESSION`, else `main`.

## Tools

### dm_layout

| action | dm command | reply |
| --- | --- | --- |
| `list` | `dm list --json` | the snapshot |
| `split` | `dm split <pane> --dir D --json` | `{"pane":"w1p2"}` |
| `window` | `dm new-window --argv CMD...` | `{"window":"w2","pane":"w2p1"}` |
| `focus` | `dm focus <pane>` | the server reply |
| `kill` | `dm exec '{"action":"close-pane","target":P}'` | the server reply |

clidoc has no `--json` on `new-window` or `focus`, and no close verb at all.
The CLI prints the server's reply as JSON anyway, so the action reads it.

### dm_pane

| action | dm command | reply |
| --- | --- | --- |
| `write` | `dm write-to <pane> --msg T [--enter]` | the write acknowledgement |
| `read` | `dm exec '{"action":"read","target":P}'` | `{"lines":[...],"status":{...}}` |
| `read` with `scrollback` | `dm exec '{"action":"history","target":P,"count":N}'` | the same shape |
| `wait` | `dm wait <pane> --timeout D --json` | `{"exit":N,"reason":"mark","timed_out":false}` |
| `status` | `dm exec '{"action":"status","target":P}'` | the OSC 7501 status |

`read` takes the RPC because clidoc's `dm read` has no `--json`: it prints
screen text. `wait` exits with the code it observed, so a nonzero exit is a
result, not a failure. A dm that cannot reach the pane prints no JSON, and that
is the failure.

### dm_box

| action | dm command | reply |
| --- | --- | --- |
| `post` | `dm exec '{"action":"msg-post","res":{...}}'` | `{"entry":{...}}` |
| `list` | `dm msg list --json [--key K \| --prefix P] [--after N] [--limit N]` | `{"entries":[...],"trimmed":bool}` |
| `wait` | `dm msg wait --json [--key K \| --prefix P] [--state S] [--after N] [--timeout D]` | `{"entry":{...},"trimmed":bool}` |
| `sub` | `dm msg sub --json [--key K \| --prefix P] [--state S] [--after N]` | one NDJSON frame per entry |

`post` takes the RPC because clidoc's `msg post` prints one row, not JSON, and
the request body is a `res` field. `sub` streams until the deadline, so the
frames printed before it are the answer. `key` and `prefix` are exclusive, and
the tool refuses both before it calls dm.

### dm_turn

| action | dm command | reply |
| --- | --- | --- |
| `ask` | `dm exec '{"action":"ask-open","target":P,"res":{...}}'` | `{"id":"a1"}` |
| `answer` | `dm exec '{"action":"ask-answer","target":A,"res":{...}}'` | the stored answer |
| `await` | `dm ask await <id> --timeout D --json` | `{"pending":true}` or `{"answer":{...}}` |

clidoc's `ask open` and `ask answer` read their body on stdin. `pi.exec` gives
a child no stdin, so the tool sends the same request as an `ask-open` or
`ask-answer` action, with the body in `res`. The server decodes one contract
either way.

clidoc caps one await at 25 seconds. The tool chains calls until the caller's
own `timeout` runs out, and reports `pending` once it does. Give `pane` on
`ask`. Give `askId` on `answer` and `await`.

## Reaching the tools

`dm_turn` is `model-only`, pi's rule for a tool that asks the user. The other
three are `direct`: declared to the model and callable from a codemode script,
and pi appends the note that says so to their descriptions.

Scripting is not how a fleet is supervised. `dungeon_watch` already keeps the
cursor, resets a stale one and returns one line per lane event, so a hand-
written loop over `dm_box wait` would be the same job done worse.

A script earns its place on the question no digest answers: which panes sit in
this window, which changed branch, which carry a dirty tree and an open ask.
`dm list --json` is 7408 bytes on a quiet session, and a script returns the slice.

```js
const { snapshot } = await tools.dm_layout({ action: "list" });
text(snapshot.all
  .filter((p) => !p.shelf && p.inRepo)
  .map((p) => `${p.id} ${p.project} ${p.branch}${p.dirty ? " dirty" : ""}`)
  .join("\n"));
```


## Results

A success returns a short text line and the parsed reply in `details`, which is
also the `structuredContent` a script receives. A nonzero dm exit, a killed
command, or a reply that is not JSON returns a tool error carrying dm's own text.

## Test

    node --experimental-strip-types --test --test-timeout=15000 skills/dm-agent/dm-agent.test.ts

The test drives the extension against a fake pi object and a fake `dm` script
on `PATH`. The fake records every argv and answers from files beside itself. It
needs no session, no terminal and no dm binary.
