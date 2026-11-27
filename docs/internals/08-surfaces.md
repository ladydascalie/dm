# Surfaces

Read this page to find the three surfaces a pane reports through: the message
log, the notification renderer and the pane status report. Each one is a place
to add an entry field, a state or a rule.

## The message log

One session keeps one message log: an append-only list of entries that any pane
can post into and any caller can wait on. The log lives in `state.json` as
`State.Box` (`internal/session/state.go#State.Box`). A poster is any caller that
appends an entry: a pane, the CLI, a plugin or the server. A poster names a key
and a state, and the server stamps the sequence and the time.

Eight CLI verbs reach it, and seven wire actions serve them
(`internal/clidoc/clidoc.go#MsgSubcommands`):

| verb | wire action | what it does |
| --- | --- | --- |
| `dm msg post` | `msg-post` | append one entry and print it |
| `dm msg list` | `msg-list` | print every matching entry, newest first |
| `dm msg show` | `msg-list` | print one entry's body, or its summary line |
| `dm msg wait` | `msg-wait` | block until one entry matches |
| `dm msg sub` | `msg-sub` | stream every matching entry on one connection |
| `dm msg clear` | `msg-clear` | drop the entries the key or the branch names |
| `dm msg watchers` | `msg-watchers` | list every reader position, sorted by pane, key and prefix |
| `dm msg schema` | `msg-schema` | print the wire as JSON Schema |

A list or a wait reply carries a `trimmed` mark. A true one says the cursor sits
behind the oldest row the log still keeps, so the entries in between are gone.

Two caps bound the log: `MaxEntries` 1000 and `MaxBytes` 256 KiB
(`internal/box/box.go#MaxEntries`, `internal/box/box.go#MaxBytes`).
`State.TrimBox` drops the oldest entries until both fit
(`internal/session/state.go#State.TrimBox`). `msg-clear` is the only other
removal, and a clear never resets the sequence
(`internal/box/box.go#ClearReq`).

## The entry fields

`box.Entry` is one entry (`internal/box/box.go#Entry`). Every field travels,
because the tags carry no `omitempty`, so a reader can tell an empty key from a
missing one.

| field | meaning | rule |
| --- | --- | --- |
| `seq` | the log sequence, from 1 | set by the server, and it only grows |
| `at` | the post time in UTC | set by the server, cut to the second |
| `from` | the id of the posting pane, `w3p1` | empty unless the poster is a pane, else the shape check is `box.PaneIDShape` (`internal/box/box.go#PaneIDShape`) |
| `key` | the topic | 128 bytes at most |
| `state` | what the entry reports | one of five, see below |
| `msg` | the summary line a list prints | 2000 bytes at most |
| `body` | the long text `dm msg show` prints raw | 16 KiB at most |

`CheckText` caps the bytes, demands valid UTF-8 and refuses control characters,
because an entry prints on one row (`internal/box/box.go#CheckText`).
`CheckBody` allows newlines in the body and refuses the rest
(`internal/box/box.go#CheckBody`).

## The five states

| state | means | desktop popup |
| --- | --- | --- |
| `note` | carries no progress meaning | never |
| `working` | the work is running | never |
| `blocked` | the work waits on someone else | on a change |
| `done` | the work finished well | never on the desktop; toasts in the client |
| `error` | the work finished badly | on a change |

`box.States` is the one list (`internal/box/box.go#States`). `box.ParseState`,
its error text and the schema all read that slice
(`internal/box/box.go#ParseState`, `internal/box/box.go#Schema`). There is no
second list to keep in step. `clear` is a pane status and not a box state, and
post rejects it.

## Keys and branches

A key is a flat string of 128 bytes at most, and the code never parses a
hierarchy. A branch is a prefix of a key, such as `lane/`. A read names one
exact key or one branch, and naming both is an error
(`internal/box/box.go#CheckTopic`). `box.Match` applies the key, the branch and
the cursor, and changes nothing (`internal/box/box.go#Match`).

Five branches are convention:

| branch | who posts | who reads |
| --- | --- | --- |
| `lane/<plan>/<worker>` | a worker agent reports its progress | the supervising agent waiting on it |
| `msg/<name>` | one poster reaches every worker | each worker, from its own cursor |
| `reply/<pane>` | a worker answers the orchestrator | the orchestrator, in order |
| `notify/<pane>` | the server reports a pane status | the renderer, which may pop |
| `plugin/<name>/...` | a plugin names its own topic | that plugin, at any depth |

`box.NotifyPrefix` is `notify/`, and `box.IsNotifyTopic` is the check a renderer
runs (`internal/box/box.go#NotifyPrefix`,
`internal/box/box.go#IsNotifyTopic`). A renderer turns entries into what the
operator sees: a client toast, a sidebar row, or a desktop notification.

## The store

The server holds the log under `s.mu` (`internal/server/server_core.go#server`).
`boxStoreLocked` appends, trims, wakes the waiters, saves and broadcasts
`{"type":"box"}` (`internal/server/server_box.go#server.boxStoreLocked`). It
redacts the message and the body with the live secret values before the entry is
stored. A failed save keeps the entry in memory and returns the error.

`LoadStateAt` drops the entries that fail `Entry.Validate` and keeps the rest
(`internal/session/state.go#LoadStateAt`).

## One wait, one stream

`boxWait` blocks with no lock held (`internal/server/server_box.go#server.boxWait`).
It reads the log under `s.mu`, registers a waiter before it unlocks, and
re-checks on every wakeup. A post nudges each waiter into a one-slot channel,
and a full slot drops the nudge. A missed nudge costs one 100ms poll
(`internal/server/server_box.go#boxPollWait`). The same poll peeks at the unix
socket the client attached on, so a client that hung up frees its waiter.

A wait has two shapes:

| read | request | reply |
| --- | --- | --- |
| status board | no `afterSeq` | the newest entry under the topic, when its state satisfies the wait |
| stream | `afterSeq` | the oldest entry past the cursor |

`boxSub` delivers the stream into the per-client queue
(`internal/server/server_box.go#server.boxSub`,
`internal/server/server_core.go#client.deliver`). A full queue rolls the cursor
back and ends the stream with `errBoxGone`, so a reconnect resumes at that entry
(`internal/server/server_box.go#errBoxGone`). The cursor advances over every
entry it scans, the state-skipped ones included. A gap in the log is reported
once, on the first frame. A parse error or a `from` outside the tree still
arrives as a normal reply with an error.

## Reader positions

A read from a pane leaves a reader position: the pane, its filter, the cursor
and whether it is waiting (`box.Watch`, `internal/box/box.go#Watch`). A read
with no `from` records nothing.

| field | meaning |
| --- | --- |
| `pane` | the pane that read |
| `key`, `keyPrefix` | the filter, one exact key or one branch |
| `cursor` | the highest `seq` served |
| `parked` | true while a wait or a stream on it still blocks |
| `at` | the last read, in UTC to the second |

The registry lives in server memory only. `boxExpireLocked` drops a position
whose pane left the tree (`internal/server/server_box.go#server.boxExpireLocked`).
It also drops a position that is not waiting and is older than `boxWatchTTL`,
ten minutes (`internal/server/server_box.go#boxWatchTTL`). A waiting position
never ages out.
A position that appears, advances, changes its waiting mark or drops pushes
`{"type":"watch"}`.

## The notify branch

A desktop notification is not a log feature. `notify/` keys are ordinary
entries, and the server's notification renderer turns them into desktop
notifications. The producer is `notifyPaneState`, which posts a pane's state on
`notify/<pane>` (`internal/server/notify_server.go#server.notifyPaneState`). The
topic holds one row per pane: a report replaces that pane's older row
(`internal/server/server_box.go#server.boxReplace`).

Three pre-checks stand in front of a popup:

| pre-check | rule | code |
| --- | --- | --- |
| transition | the new state differs from the old one, and is `blocked`, `error`, or `done` with a message | `internal/notify/notify.go#StateChange` |
| urgency | only `blocked` and `error` interrupt the operator | `internal/notify/notify.go#Urgent` |
| suppression | a `notify/<pane>` entry stays silent while that pane holds the focus of the active window | `internal/server/server_box.go#server.notifyBoxEntryLocked`, `internal/server/notify_server.go#server.operatorSeesLocked` |

A bare `notify/` names no pane, so the suppression check never silences it. The
pane connection reader `watchConn` applies the transition pre-check before it
posts, so a program that reports `blocked` every second posts once
(`internal/server/server_core.go#server.watchConn`). A repeat is not a change.

`notify.SystemNotify` runs `notify-send` (`internal/notify/notify.go#SystemNotify`).
A host with no `notify-send` logs the failure and keeps the entry. The phone
renderer applies the same two pre-checks
(`internal/server/push_notify.go#server.pushBoxEntryLocked`).

The client applies the same suppression rule to its toasts
(`internal/client/client_notify.go#tui.notifySuppress`). A toast is the popup
the client raises for a `notify/` entry it has not seen, and the focused pane
gets none.

## Pane status

An agent is a program running in a pane that reports its own state, such as
`pi`. It reports on its own stdout with an escape sequence: `ESC ] 7501 ;
state=working:app=pi:msg=<base64> ST`, terminated by `ST` or `BEL`. The payload
is `key=value` pairs separated by `:`, so `app` is the reporting program's name.
`msg` is base64 so that a message can carry a `:` without splitting the pair
list.

`osc.Filter.Feed` sits in the pane daemon's read loop
(`internal/osc/osc.go#Filter.Feed`, `internal/pane/pane.go#paneProc.readLoop`).
The filter holds a partial sequence between reads and passes every other byte
through. `handleOSC` matches `oscPrefix` and calls `Status`
(`internal/osc/osc.go#Filter.handleOSC`, `internal/osc/osc.go#oscPrefix`).

A program usually probes first with `ESC ] 7501 ; ? ST`, and the pane answers
with its current status (`internal/pane/pane.go#paneProc.replyQuery`). A
conforming program then switches reporting on.

`osc.ParseStatus` accepts the keyed form, the JSON form and a bare state word
(`internal/osc/osc.go#ParseStatus`). `knownState` is the closed set a pane may
report (`internal/osc/osc.go#knownState`). `normaliseState` maps `clear` and any
unknown word to `unknown` (`internal/osc/osc.go#normaliseState`). A report can
never put free text into a word the sidebar prints. `clear` is the protocol's
removal word: nothing is left of the report, so the pane shows `unknown`.

Four sets sit close together and differ on purpose:

| set | words |
| --- | --- |
| what a pane reports | `idle`, `working`, `blocked`, `done`, `error`, `unknown` |
| what an entry carries | `note`, `working`, `blocked`, `done`, `error` |
| what a desktop popup admits | `blocked`, `error` |
| what a client toast admits | any posted entry, including `done` with a message |

`idle` and `clear` are not box states, and `note` is not a pane status. The
producer posts `blocked` and `error` on any change, and `done` only when it
carries a message.

## From report to glyph

```mermaid
flowchart TD
  A["agent prints OSC 7501 state=working on its stdout"] --> B["pane daemon: osc.Filter.Feed reads it out of the PTY"]
  B --> C["osc.ParseStatus normalises the state word"]
  C --> D["paneProc.setStatus redacts the text, then stores the status"]
  D --> E["server: watchConn reads the status frame"]
  E --> F["server: paneConn.setStatus, then pushSnapshot"]
  F --> G["client: the snapshot paints the sidebar row"]
  G --> H["rail.RailGlyph picks the glyph and its colour"]
  E --> I["notify.StateChange, then a post on notify/&lt;pane&gt;"]
  I --> J["urgency and suppression gates decide the popup"]
```

The pane daemon owns one pane's pty (`paneProc`,
`internal/pane/pane.go#paneProc`). It replaces every secret value the session
registered with a marker before it stores the status
(`internal/pane/scrub.go#scrubber.redactText`), and [Secrets](09-secrets.md)
owns how a value is registered.

`paneConn` is the server's record of the connection to that daemon.
`pushSnapshot` broadcasts the snapshot, the whole layout, focus and pane list a
client draws from (`internal/server/server_view.go#server.pushSnapshot`).
`RailGlyph` reads one glyph and one colour per state from `theme.States`
(`internal/rail/rail.go#RailGlyph`, `internal/theme/theme.go#Theme.States`). A
`states` block in the theme file overrides them
(`internal/theme/theme.go#LoadThemeFile`).

## Sidebar marks

A pane row is four cells of indent, one cell of agent mark, a space and the
pane's name (`internal/rail/rail.go#RailRow`). The state glyph sits in the
sidebar's last content cell. The agent mark takes an indent cell rather
than a column of its own. The name still starts at column 7, and the row costs
no extra width. A shell leaves that cell blank, because a shell is not an
agent. A project, workspace or window row carries the worst state of its
members in that same cell. A pane that never reported and a pane whose state is
unknown leave the cell blank.

`RailMark` draws the agent mark (`internal/rail/rail.go#RailMark`). It is an
agent icon, the bar glyph an agent gets on a plain terminal, or a shell icon for
a pane with no agent. Every state has a Nerd Font form except
`shell`, the state of a pane that runs a shell with no agent to report.

The thin sidebar is six columns ([Layout](07-layout.md#the-sidebar-column)).
Its five content cells are the project bar, the agent mark, the git cell, the
state glyph and the question badge. The agent mark takes the dim tone, so the
state glyph is the one coloured cell on the row. A
sidebar the screen caps below the thin width gives up cells in one order. The
badge goes first, then the git cell, then the agent mark and the bar. The state
goes last. A sidebar of two cells still says how the pane is doing. A sidebar
of one leaves the drag strip the only column and paints nothing.

Git facts are per pane (`internal/wire/wire.go#AllPane`), so the sidebar never
hoists them further than one row. A window row carries its source pane's facts,
and a thin row carries its own. The git facts panel reads one pane, whichever
mark opened it, and it stands on the row that mark was painted on
(`internal/client/client_git.go#gitFloat`).

## Where to add

| change | start at |
| --- | --- |
| a new box state | `box.States` (`internal/box/box.go#States`); the decoder, `ParseState`, its error text and `Schema` read that slice, and `ValidateReq` accepts the name (`internal/box/box.go#ValidateReq`) |
| a new pane status | `knownState` (`internal/osc/osc.go#knownState`), then a `states` entry in the theme file, then a decision about `notify.StateChange` |
| a new notification rule | `notify.Urgent` or `notify.StateChange` (`internal/notify/notify.go`), or `notifyBoxEntryLocked` (`internal/server/server_box.go#server.notifyBoxEntryLocked`), never `internal/box` |

## See also

- [The protocol](03-protocol.md) for the envelope every message log request rides in.
- [The server](04-server.md) for the lock `s.mu` and what it guards.
- [The pane](05-pane.md) for the read loop the status filter sits in.
- [The client](06-client.md) for its snapshot apply and the toast it raises.
- [Secrets](09-secrets.md) for the redaction a status message passes through.
