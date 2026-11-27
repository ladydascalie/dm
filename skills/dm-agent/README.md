# dm-agent

A pi extension that registers four tools over dm's own CLI surface: `dm_layout`,
`dm_pane`, `dm_box` and `dm_turn`. Each tool carries one action enum. Each
action runs one `dm` command through `pi.exec` and returns the JSON reply. No
action reads a terminal screen or parses prose.

Verb and flag names come from `internal/clidoc/clidoc.go`. Reply shapes come
from `docs/internals/03-protocol.md` and `dm man`.

## Install into pi

Symlink the extension where pi loads extensions:

    ln -sf "$PWD/skills/dm-agent/dm-agent.ts" ~/.pi/agent/extensions/dm-agent.ts

`dm` must be on `PATH`. Every action uses the session named by `session`, else
`DM_SESSION`, else `main`.

## The four tools

### dm_layout

| action | dm command | reply |
| --- | --- | --- |
| `list` | `dm list --json` | one line per pane, and the snapshot in `details` |
| `split` | `dm split <pane> --dir D --json` | `{"pane":"w1p2"}` |
| `window` | `dm new-window --argv CMD...` | `{"window":"w2","pane":"w2p1"}` |
| `focus` | `dm focus <pane>` | the server reply |
| `kill` | `dm exec '{"action":"close-pane","target":P}'` | the server reply |

`list` prints one line per pane. The line reads a focus star, the id, the
project, the branch, the status, then the shelf mark, the dirty counts and the
app. `full: true` prints dm's raw snapshot instead. The snapshot always rides in
`details` and `structuredContent`, so a script reads the whole object without
asking for it.

clidoc has no `--json` on `new-window` or `focus`, and no close verb. The CLI
prints the server's reply as JSON anyway, so the action reads it.

### dm_pane

| action | dm command | reply |
| --- | --- | --- |
| `write` | `dm write-to <pane> --msg T [--enter]` | the write acknowledgement |
| `read` | `dm exec '{"action":"read","target":P}'` | `{"lines":[...],"status":{...}}` |
| `read` with `scrollback` | `dm exec '{"action":"history","target":P,"count":N}'` | the same shape |
| `wait` | `dm wait <pane> --timeout D --json` | `{"exit":N,"reason":"mark","timed_out":false}` |
| `status` | `dm exec '{"action":"status","target":P}'` | the OSC 7501 status |

`read` takes the RPC because clidoc's `dm read` has no `--json`. That verb
prints screen text. `wait` exits with the code it observed, so a nonzero exit is
a result, not a failure. A dm that cannot reach the pane prints no JSON, and
that is the failure.

### dm_box

| action | dm command | reply |
| --- | --- | --- |
| `post` | `dm exec '{"action":"msg-post","res":{...}}'` | `{"entry":{...}}` |
| `list` | `dm msg list --json [--key K \| --prefix P] [--after N] [--limit N]` | `{"entries":[...],"trimmed":bool}` |
| `wait` | `dm msg wait --json [--key K \| --prefix P] [--state S] [--after N] [--timeout D]` | `{"entry":{...},"trimmed":bool}` |
| `sub` | `dm msg sub --json [--key K \| --prefix P] [--state S] [--after N]` | one NDJSON frame per entry |

`post` takes the RPC because clidoc's `msg post` prints one row, not JSON. The
request body is also a `res` field. `sub` streams until the deadline, so the
frames printed before it are the answer. `key` and `prefix` are exclusive, and
the tool refuses both before it calls dm.

### dm_turn

| action | dm command | reply |
| --- | --- | --- |
| `ask` | `dm exec '{"action":"ask-open","target":P,"res":{...}}'` | `{"id":"a1"}` |
| `answer` | `dm exec '{"action":"ask-answer","target":A,"res":{...}}'` | the stored answer |
| `await` | `dm ask await <id> --timeout D --json` | `{"pending":true}` or `{"answer":{...}}` |

clidoc's `ask open` and `ask answer` read their body on stdin. `pi.exec` gives a
child no stdin. The tool sends the same request as an `ask-open` or `ask-answer`
action, with the body in `res`. The server decodes one contract either way.

clidoc caps one await at 25 seconds. The tool chains calls until the caller's
own `timeout` runs out, and reports `pending` once it does. Give `pane` on
`ask`. Give `askId` on `answer` and `await`.

Name the pane you run in. An ask gives that pane the focus, so the question
stands in front of the operator wherever they were. The question panel holds the
keys until they answer or decline it.

## Reaching the tools

`dm_turn` is `model-only`, pi's rule for a tool that asks the user. The other
three are `direct`, which declares a tool to the model and makes it callable
from a codemode script. pi appends the note that says so to their
descriptions.

Scripting is not how a group of agents is supervised. A loop over `dm_box wait`
would keep the cursor itself and reset it after a restart.

A script earns its place on the question no digest answers. That question is
which panes sit in this window, which changed branch, and which carry a dirty
tree and an open ask. `dm list --json` answers all three.

```js
const { snapshot } = await tools.dm_layout({ action: "list" });
text(snapshot.all
  .filter((p) => !p.shelf && p.inRepo)
  .map((p) => `${p.id} ${p.project} ${p.branch}${p.dirty ? " dirty" : ""}`)
  .join("\n"));
```

## Tool results

A success returns a short text line and the parsed reply in `details`. That
reply is also the `structuredContent` a script receives. A nonzero dm exit, a
killed command, or a reply that is not JSON returns a tool error carrying dm's
own text.

## Test the extension

    node --experimental-strip-types --test --test-timeout=15000 skills/dm-agent/dm-agent.test.ts

The test drives the extension against a fake pi object and a fake `dm` script
on `PATH`. The fake records every argv and answers from files beside itself. It
needs no session, no terminal and no dm binary.
