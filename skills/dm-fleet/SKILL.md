---
name: dm-fleet
description: "Use when you run, supervise or fan out agents in dm's panes: choosing between a split, a new window and a plan graph, writing a brief a lane can finish alone, reading box wake-ups instead of polling a pane, and deciding when a lane is blocked, done or looping. Names the lane key lane/<encounter>/<minion>, DM_PANE and DM_SESSION, and the rule that a lane never commits."
---

# dm-fleet

You are one agent among other agents, each in a pane. This page carries the
judgement. It covers when to open a pane, how to brief the agent that works in
it, and how to read what it sends back. The mechanical half lives in `dm llms`:
ids, verbs, flags and JSON shapes.

A worker agent is one you start in a pane and supervise. The group of agents is
every worker agent you have running.

## Inside dm

```sh
test -n "${DM_SESSION:-}"
```

A failed test means you are not inside dm. Say so and stop.

## Where you stand

`DM_SESSION` names the session. `DM_PANE` names your pane. A pane id is a window
id, then `p`, then a sequence: `w2p3`. The counter only moves forward, so a
closed id never comes back.

Read the tree. Never guess an id, and never copy an example id:

```sh
dm list --json | jq -r '.snapshot.all[] | [.id, .cwd, .agent, .status] | @tsv'
```

`dm status` prints the OSC 7501 state that the pane's agent reported. It is the
cheapest true fact about a worker agent, and reading it costs that agent no
turn.

## Shape the work

Pick the smallest shape that holds the work. A split shares your window. A new
window holds its own layout. A plan graph is one graph of units, one worker per
unit, ordered by dependency. `dungeon init` writes a starter plan, and
`dungeon up` spawns one pane per worker.

| the work | shape | why |
| --- | --- | --- |
| One edit you can finish now | no pane | A pane is a context you then watch |
| Independent, blocks your next step, one command proves it | split a pane | You keep your screen and gain a second one |
| Another cwd, branch or repo, or a long build | new window | Separation, not width. One window per line of work |
| Order, review, a merge decision, or a bug that must be proved | plan graph | The graph holds the order and the proof |

Close calls:

- Split for width. Run `dm split <pane> --dir right` when you want both results
  in view at once.
- Open a window for separation. Run `dm new-window` when the work has its own
  branch or checkout. A worker agent on its own branch inside your window
  invites a stray write into the wrong pane.
- Open a plan graph when the work needs a second pass, a reviewer, or an order
  you cannot hold in your head. Two dependent chunks are a plan, not a fan-out.
- Every pane you open owes you a report. A pane without a definition of done
  burns tokens.

## Worth a worker agent

Open a worker agent only when all three hold:

1. You can name what finished looks like, and one command proves it.
2. The work touches no file you hold, and no file another worker agent holds.
3. You will read its report and its diff.

If one fails, do the work yourself. A worker agent whose output you rewrite line
by line is slower than the work, and it hides your own progress.

## The brief

The worker agent starts with your brief and nothing else. It does not remember
your conversation.

- Name the files it owns and the files it must not touch.
- Name the accept command and the report path.
- Give the path of every report and spec it needs. Do not make it search.
- One concern. If the goal needs an "and", write two briefs.

## The worker protocol

A plan is one graph of units, and a worker is one agent in it. The worker agent
talks on the message log, under the key `lane/<plan>/<worker>`. The key carries
the plan, because two plans may both run a worker called `w1`.

The three states that matter here:

| state | what it means | what you do |
| --- | --- | --- |
| `working` | alive and on it | Do not help it, and do not type into it. Typed text corrupts the turn |
| `blocked` | it cannot proceed without a decision | Decide now. The entry names the decision and what the worker agent already tried |
| `done` | the work is in the worktree and its accept command passed | Read the report path the entry names. Nothing else is proof |

A worker agent never commits. It leaves uncommitted changes in its worktree and
hands over a report. The parent runs the accept command and reads the diff, then
commits on the worker's branch. A worker agent that commits has taken a decision
the parent owns.

A wake-up is not a verdict. A `done` entry means a worker agent stopped talking,
and not that the work is done. Settle the work yourself: run the accept command
and read the report. A summary that sounds finished is not evidence.

Read every report for three things: what changed, what it ran, and what it did
not do. A report that claims a test passed without the command and its exit code
states a claim, not a result.

## Read a worker agent

One look, one command, then decide.

```sh
dm wait w2p3 --timeout 60                            # the pane's command ended
dm msg wait --key lane/provision/w3 --timeout 60      # a message log entry arrived
```

Do not poll a pane with `dm read` in a loop. A sleep loop spends a turn per
tick. It also hides the thing you look for. That is a worker agent that repeats
the same command, re-reads the same file, or retries an accept that already
failed. Read the pane once per look, and compare it with the last look.

A timeout is a cue to look, not a failure. Check `dm status`, then read the pane
of every worker agent that still says `working`.

While a watch runs, do your own work: read reports, answer blocked worker
agents, split units. Do not idle on a long wait.

## Do not

- Do not commit a worker agent's work.
- Do not type into a worker agent that is `working`.
- Do not claim a worker agent passed without its accept output.
- Do not poll a pane with `dm read`.
- Do not build a pane id by hand.
- Do not keep a stuck worker agent alive to save face. Kill it, then write a
  fresh brief.
