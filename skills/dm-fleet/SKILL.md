---
name: dm-fleet
description: "Use when you run, supervise or fan out agents in dm's panes: choosing between a split, a new window and a plan graph, writing a brief a lane can finish alone, reading box wake-ups instead of polling a pane, and deciding when a lane is blocked, done or looping. Names the lane key lane/<encounter>/<minion>, DM_PANE and DM_SESSION, and the rule that a lane never commits."
---

# dm-fleet

You are one agent among terminals. This skill holds the judgement. The
mechanical half belongs to `dm llms`: ids, verbs, flags, JSON shapes. Run it
when this page is not enough.

## Gate

```sh
test -n "${DM_SESSION:-}"
```

If the gate fails, you are not inside dm. Say so and stop.

## Know where you stand

`DM_SESSION` names the session. `DM_PANE` names your pane. A pane id is its
window plus `p` plus a sequence: `w2p3`. Ids are constructible and a counter
only moves forward, so a closed id is never a target.

Read the tree. Do not guess an id, and do not copy an example id:

```sh
dm list --json | jq -r '.snapshot.all[] | [.id, .cwd, .agent, .status] | @tsv'
```

`dm status` prints the OSC 7501 state a surface draws. It is the cheapest true
thing about a lane, and reading it costs that lane no turn.

## Shape the work

Pick the smallest shape that holds the work.

| The work | Shape | Why |
|---|---|---|
| One edit you can finish now | no pane | A pane is a context you must watch |
| Independent, blocks your next step, one command proves it | split a pane | Width without losing your own screen |
| Another cwd, branch or repo, or a long build | new window | Separation, not width. One window per line of work |
| Order, review, a merge gate, or a bug that must be proved | plan graph | The graph holds the order and the proof |

Close calls:

- Split for width. `dm split <pane> --dir right` is right when you want both
  results in view at once.
- Open a window for separation. Use `dm new-window` when the work has its own
  branch or checkout. A lane with its own branch inside your window invites a
  stray write into the wrong pane.
- Reach for a plan graph when the work needs a second pass, a reviewer, or an
  order you cannot hold in your head. More than about two dependent chunks is a
  plan, not a fan-out.
- Every pane you open owes you a report. A pane with no definition of done is
  tokens burning.

## When a lane is worth it

A lane pays for itself when all three hold:

1. You can name what finished looks like, and one command proves it.
2. The work touches no file you hold, and no file another lane holds.
3. You will read its report and its diff.

If one fails, do the work yourself. A lane whose output you rewrite line by
line is slower than the work, and it hides your own progress.

## Write a brief a lane can finish alone

The lane starts with your brief and nothing else. It does not remember your
conversation.

- Name the files it owns and the files it must not touch.
- Name the accept command and the report path.
- Give the path of every report and spec it needs. Do not make it search.
- One concern. If the goal needs an "and", write two briefs.

## The lane protocol

A lane talks on the box, under `lane/<encounter>/<minion>`. The key carries the
encounter, because two encounters may both run a minion called `w1`.

The states that matter here:

- `working`: alive and on it. Do not help it, and do not type into it. Text
  lands in its prompt and corrupts the turn.
- `blocked`: it cannot proceed without a decision. The entry names the decision
  and what it already tried. A blocked lane waits, and a decision you can make
  is a decision you should make now.
- `done`: the work is in the worktree and its accept command passed. The entry
  names the report path. Nothing else is proof.

A lane never commits. It leaves uncommitted changes in its worktree and hands
over a report. The parent commits, on the lane branch, after the accept command
passes and the diff is read. A lane that commits has taken a decision the
parent owns.

A wake-up is not a verdict. A `done` entry means a lane stopped talking. It
does not mean the work is done. Settle the work yourself: run the accept command
and read the report. A summary that sounds finished is not evidence.

Read every report for three things: what changed, what it ran, and what it did
not do. A report that claims a test passed without the command and its exit code
is a claim, not a result.

## Read a lane without burning it

One look, one command, then decide.

```sh
dm wait w2p3 --timeout 60                            # the pane's command ended
dm msg wait --key lane/provision/w3 --timeout 60      # a box entry arrived
```

Do not poll a pane with `dm read` in a loop. A sleep loop of `dm read` spends a
turn per tick and hides the thing you are looking for: a lane that repeats the
same command, re-reads the same file, or retries an accept that already failed.
Read the pane once per look and compare it with the last look.

A timeout is not a failure. It is a cue to look: check `dm status`, then read
the pane of every lane that still says `working`.

Next to a box watch, do your own work: read reports, answer blocked lanes, split
units. Do not idle on a long wait.

## Do not

- Do not commit a lane's work.
- Do not type into a working lane.
- Do not claim a lane passed without its accept output.
- Do not poll a pane with `dm read`.
- Do not build a pane id by hand.
- Do not keep a stuck lane alive to save face. Kill it and write a fresh brief.
