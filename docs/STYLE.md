# Documentation style

This page gives the rules for writing and reviewing dm's pages. Read it before
you touch one.

## Who reads what

Each page has one reader. Write for that reader and for nobody else.

| page | reader | question it answers |
| --- | --- | --- |
| `README.md` | the operator who installs and drives dm | how do I do this now |
| `PLUGINS.md` | a program author | how does my program talk to dm |
| `docs/internals/` | a Go contributor who has never seen this repo | where does this live, and where does my change go |
| `docs/releases/` | a reader asking what a version shipped | what changed, frozen at the tag |
| `skills/dm-*/` | an agent that starts with no memory | what do I do in this situation |
| `.agents/skills/` | the operator's agent running a runbook | what is the next command |
| `docs/STYLE.md` | a contributor writing or reviewing a page | how do I write this page |

The README is the operator's page. Every sentence must let the operator do
something at the terminal. A package name, a paint order or a byte count
belongs in `docs/internals`, not here.

`docs/internals` is the contributor's page. Every claim names the file and the
declaration behind it, and each page ends by naming where the next change
goes. [docs/internals/README.md](docs/internals/README.md) holds the citation
format and the reading order.

The skill pages are an agent's page. An agent reads one page, in one session,
with no memory of the last one. Every step names a command it can run.

`docs/releases` is history. The page records what a tag shipped. Do not edit it
when a later version fixes the claim. Write the next page instead.

## The rules

### 1. Name the actor

Give every sentence a subject that acts, and put the verb in the active voice.

Bad, `docs/internals/12-release.md`: "`docs/releases/<tag>.md` is copied into
the release verbatim when it exists, and only a release without it falls back
to the commit range."

Write: "The workflow copies `docs/releases/<tag>.md` into the release verbatim
when the file exists. A release without one falls back to the commit range."

### 2. One fact a sentence

One fact or one instruction per sentence. Split at a colon, a semicolon, a dash
or a comma splice.

Bad, `docs/internals/08-surfaces.md`: "A post nudges each waiter into a
one-slot channel; a full slot drops the nudge, so the re-check plus
`boxPollWait` (`internal/server/server_box.go#boxPollWait`) bounds the loss: a
missed nudge costs one 100ms poll, and the same poll peeks at the unix socket
the client attached on, so a client that hung up frees its waiter."

Write: "A post nudges each waiter into a one-slot channel. A full slot drops
the nudge, so a missed nudge costs one 100ms poll. The re-check plus
`boxPollWait` (`internal/server/server_box.go#boxPollWait`) bounds that loss.
The same poll peeks at the unix socket the client attached on, so a client that
hung up frees its waiter."

### 3. Twenty words, no more

Aim under 20 words a sentence. A sentence that needs a second reading is two
sentences.

Bad, `docs/internals/05-pane.md`: "It joins autowrapped rows into logical lines
and wraps them again at the new width, trimming trailing blanks, moving the
cursor with its line, and keeping rows that fall off the top in the ring." That
is 35 words.

Write: "It joins autowrapped rows into logical lines and wraps them at the new
width. It trims trailing blanks, moves the cursor with its line, and keeps rows
that fall off the top in the ring."

### 4. The plain noun

Use the plain word for an ordinary thing. A term is allowed only when the page
defines it in one plain sentence at first use. Section [Words we ban](#words-we-ban)
holds the list.

Bad, `docs/internals/03-protocol.md`: "a box watcher is one reader's place in it
(pane, key, cursor, parked)"

Write: "A watcher is one reader's place in the message log: its pane, its key,
its cursor and whether it is waiting."

### 5. The reader's task

Say what the reader does and what happens. Never say what the code used to do.

Bad, `README.md`, under Rendering: "It replaces the offset and selection
arithmetic each float and table writes for itself."

Write: "Draw a list through it and skip your own offset arithmetic."

### 6. No diary

Delete the author's diary: a milestone, a prototype, `no longer`, `now`,
`still`, `as of`, a release note, and a note about the commit you just made.

Bad, `README.md`, first screen: "Prototype status: M1 works end to end."

Write: cut the line. The page then opens with the product sentence and the
site URL.

Bad, `README.md`, under Performance: "The client no longer clears the screen and
repaints every cell on every frame: a steady frame writes nothing."

Write: "A steady frame writes nothing."

### 7. Numbers that act

Delete a number no reader can act on. A hex colour, a cell index, a byte
ceiling and a build time belong in `docs/internals`, and only where a
contributor needs them.

Bad, `README.md`, under Status bar: "railHoverBG (#21202d) for the row the
pointer rests on, railText (#e9eaf1), railHeader (#a6a9bd), railDim (#8f93a8),
barAccent (#7ac0eb)"

Write: "A user theme sets its own colour for each of these keys. The built-in
values live in `internal/theme/theme.go`."

### 8. No hedging

Cut `seems`, `probably`, `should`, `may want to` and `might`. Say what happens,
or cut the sentence.

Bad, `README.md`, under Topics and subscriptions: "A subscriber that sees it has
missed messages and should resynchronise."

Write: "A subscriber that sees it has missed messages. Read from the oldest row
the log still keeps."

### 9. No filler

Cut `simply`, `just`, `note that`, `of course`, `obviously` and `it is worth
noting`. The sentence reads better without the word.

Bad, `docs/internals/12-release.md`: "A missing, unreadable or corrupt cache is
simply a cache that is not there."

Write: "A missing, unreadable or corrupt cache reads as no cache at all."

### 10. No dashes

Never use an em dash. Never use an exclamation mark. The repo holds none of
either, and the count stays at zero.

A colon or a semicolon does the work a dash would do, and it hides a second
fact. Split the sentence instead.

Bad, `docs/internals/06-client.md`: "The rail is server state and client paint:
the server publishes the width (`Rail`, `RailWant`) and the panes the rows are
built from (`Snapshot.All`, `internal/wire/wire.go#Snapshot.All`), and enforces
everything; the client groups those panes into project, workspace, window and
pane rows, and the thin rail keeps one pane row per pane and no group row."

Write: "The rail is server state and client paint. The server publishes the
width (`Rail`, `RailWant`) and the panes the rows are built from
(`Snapshot.All`, `internal/wire/wire.go#Snapshot.All`), and enforces everything.
The client groups those panes into project, workspace, window and pane rows.
The thin rail keeps one pane row per pane and no group row."

### 11. Contract is literal

A command, a flag, a key, a path, an action name and a state name are contract.
Copy them byte for byte from the code. Never paraphrase one, and never restate a
constant as a sum.

Bad, `docs/internals/03-protocol.md`: "a quarter of the 1 MiB a single outbound
frame may hold". The bound in the code is not 1 MiB.

Write: "`internal/wire/wire.go#wire.MaxFrame` sets the bound at 16 MiB." Run
`go test ./internal/clidoc/...` after you change one.

### 12. Short headings

A heading is two to four words and names the thing a reader looks for. No
sentence headings, and no heading that carries a claim the prose repeats.

Bad, `docs/internals/01-entry-point.md`: "# Entry point: one binary, four
processes". The page then lists five processes. Bad,
`docs/internals/06-client.md`: "## The model a frame is built from".

Write: "# Entry point" and "## The frame model", with the process count fixed
in the prose.

### 13. Lists are tables

A list of keys, flags, states or actions is a table, not prose. A table gets a
column for the name and a column for what it does.

Bad, `README.md`, under Keybindings: "- `main` is the table the prefix key
opens. - `menu` is the right-click menu." Eight such lines, and `layout` is
missing from them.

Write: a two-column table of mode and what it remaps, with all nine modes:
`main`, `menu`, `picker`, `switcher`, `layout`, `scroll`, `rename`, `box`,
`help`.

### 14. Open with purpose

A page opens with one sentence that says what the page is for and who reads it.

Bad, `docs/internals/06-client.md`: the page purpose arrives five paragraphs in,
as "This page answers how all of that works, and where you add a key action, an
overlay or a band."

Write: "The client draws dm's screen and turns your keys into requests. Read
this page to add a key action, an overlay or a band, and to find the file that
answers for a paint."

## Words we ban

Each of these names an ordinary thing with a private word. Use the plain word
in the middle column. Keep a coined word only where the product prints it, and
then define it in one sentence at first use.

| word | plain word | printed by the product as |
| --- | --- | --- |
| box | message log | keymap mode `box`, `dm msg ...`, the `msg-*` actions, `box.Watch`, `notify/`, `lane/` |
| rail | sidebar | `set-rail`, `Rail`, `RailWant`, `rail-bg`, `rail-hover-bg`, `rail-focus-bg`, `rail-cols`, `rail-width` |
| lane | worker agent | `lane/<plan>/<worker>`, `dm-fleet`, `dungeon` |
| fleet | group of agents | `dm-fleet`, `dungeon` |
| float | floating pane | `float-open`, `float-close`, the `float` config key, the `floats` keymap mode |
| grip | drag handle | none |
| caret | cursor | none |
| fold | collapsed group | none |
| accent | group colour | `accents` |
| band | screen strip | none |
| scrub | redact | none |
| the ride | proxied request | none |
| picture | screen snapshot | none |
| ring | scrollback buffer | none |
| shelf | shelved pane | `shelve-pane`, `pop-pane`, `shelfActive`, `WS shelf` |
| summon | restore | none |
| desk | screen | none |
| rollup | group summary | none |
| the wire | socket protocol | none |
| stamp | version string | `dm version`, `DM_NO_UPDATE_CHECK` |
| boot check | startup update check | none |
| seam | extension point | none |
| artefact | file | none |
| kit | install set | none |
| gate | pre-check | none |
| encounter | plan | none |
| minion | worker | none |
| the ask | question | none |
| the set | secret list | none |
| bridge | producer | none |
| watch | reader position | `msg-watchers`, the `watch` frame |

### Words the product prints

Prose uses the plain word in the middle column. A token in the third column
survives only where the reader types it, or where the product prints it on
screen. Copy the token byte for byte. When one sentence needs both, use the
plain word for the concept and quote the token: "`set-rail` sets the width of
the sidebar."

Two rulings the column cannot carry alone:

- A pane the reader puts on the shelf is a shelved pane. Prose never calls it
  parked. Only a watch that blocks is parked, and `Watch.Parked` is the whole
  of that word.
- The box key is `lane/<plan>/<worker>`. Every page teaches that shape.

### Release pages

`docs/releases/` is history, so a release page is exempt. Rule 6 (no diary) and
rule 12 (short headings) do not apply to it, and neither does the ban table. A
release page records what shipped, in the words the tag shipped it in.

One claim on a release page is still wrong: a sentence that is false about what
shipped, such as a command the tag never had. Fix that sentence and nothing
else. Leave the rest of the page alone, including every word the ban table now
renames.

## Worked passage

The paragraph below is `README.md`, under Performance. It runs 88 words. The
rewrite runs 45.

Before:

> The client no longer clears the screen and repaints every cell on every
> frame: a steady frame writes nothing. Every written frame is wrapped in a
> synchronized update (`ESC [ ? 2026 h` to `ESC [ ? 2026 l`), so the terminal
> applies it in one go. A frame that clears is a full repaint, and it happens
> only when the picture is not stable: a resize, a pane added, closed or moved,
> a focus change, or entering and leaving scrollback, the menu, the picker or
> the palette.

After:

> A steady frame writes nothing. A written frame sits in one synchronized
> update, so the terminal applies it whole. The client repaints only when the
> screen is not stable: a resize, a pane added, closed or moved, a focus
> change, or a change of view.

What the rewrite did: cut the diary ("no longer"), named the actor on the
repaint, and dropped the sentence about a frame that clears because the last
sentence already covers it. The `ESC [ ? 2026 h` and `ESC [ ? 2026 l` markers are
a terminal contract and they belong in `docs/internals/06-client.md`.

## Reviewer checklist

Run every line against the page before you approve it.

1. Does the first sentence say what the page is for and who reads it?
2. Does the page sit with the right reader, per [Who reads what](#who-reads-what)?
3. Does every sentence name an actor and use the active voice?
4. Does any sentence carry two facts? Split it.
5. Does any sentence run past 20 words, or need a second reading? Split it.
6. Does every coined word appear in [Words we ban](#words-we-ban), or get one plain sentence at first use?
7. Does the page record a milestone, a date, an old behaviour or a release? Cut it.
8. Does the page carry a hex colour, a cell index, a byte ceiling or a build time? Move it to `docs/internals` or cut it.
9. Does any sentence hedge, or start with a filler word? Cut the word or say the fact.
10. Does the page hold an em dash or an exclamation mark? Remove it.
11. Does every command, flag, key, path, action name and state name match the code byte for byte? Run `go test ./internal/clidoc/...`.
12. Is every heading two to four words, and a noun phrase?
13. Is every list of keys, flags, states or actions a table?
14. Does the page repeat a table, a flag list or a sample? `dm man`, `dm llms` and `dm html` already print those. Keep one copy and point at the command.
15. Does `docs/internals` carry a citation for every claim, and pass `go test ./internal/archdoc/...`?
16. Could a person who never used dm, and cannot ask us, do the right thing after reading it? If not, rewrite it or delete it.
