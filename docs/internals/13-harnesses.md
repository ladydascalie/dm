# Harnesses: what installs where, and what a second one costs

dm teaches an agent harness in two artefacts: a skill directory the harness
reads, and a thin extension that registers dm's actions as tools. Only the
second one knows a harness's API. This page answers what the installer assumes,
what a new harness must provide, and what stays out of the engine on purpose, so
the cost of the second harness stays one file.

## The two artefacts

`dm provision` links two things from this checkout (`internal/provision/provision.go#kit`):

- **The skill**, `skills/dm-fleet`, mounts as `KindSkill`
  (`internal/provision/provision.go#KindSkill`). It is a directory holding
  `SKILL.md` in the Agent Skills layout, prose and `dm` verbs, no harness API.
  pi, Claude Code and opencode all read that layout today; only the directory
  differs.
- **The extension**, `skills/dm-agent/dm-agent.ts`, mounts as `KindExtension`
  (`internal/provision/provision.go#KindExtension`). It is pi's extension API:
  it imports `ExtensionAPI` and registers each tool with `pi.registerTool`
  (`skills/dm-agent/dm-agent.ts`). Its bodies are harness-neutral, though:
  every action builds a `dm ... --json` argv or a `dm exec` action body, and
  reads JSON back.

A harness with no extension concept mounts the skill alone. One mount is a
complete install.

## The seam

The installer is data plus link rules, and it names no harness:

- `Harness` (`internal/provision/provision.go#Harness`) carries a name, a root
  that must exist, and a list of `Mount` (`internal/provision/provision.go#Mount`),
  each a `Kind`, the harness directory the link lands in, and the leaf name it
  takes there.
- `New` (`internal/provision/provision.go#New`) turns those mounts into
  `Target`s through the `kit` table. It refuses an absent root, an empty mount
  list, an unknown kind, a mount with no directory or name, and two mounts on
  one path. Nothing else in the package asks which harness it holds.
- `Check`, `Apply` and `Remove` (`internal/provision/link.go#Check`,
  `internal/provision/link.go#Apply`, `internal/provision/link.go#Remove`)
  work on the plan's targets. Apply creates a mount's directory when it is
  absent; Remove deletes only a link that points inside the checkout and leaves
  a file it did not make.
- The adapter is one function, `Pi` (`internal/provision/adapter_pi.go#Pi`).
  It is the only file that spells `~/.pi/agent`.
- The verb (`internal/cli/provision.go#runProvision`) builds that one adapter
  and passes it to `New`. It is the only caller.

The guarantee is mechanical: `TestNewSecondHarness`
(`internal/provision/provision_test.go#TestNewSecondHarness`) plans, applies
and checks a harness that is not pi and mounts no extension. If that test needs
a change to the engine, the seam has leaked.

## Adding a harness

1. Read the harness's own docs for two facts: where it discovers
   `SKILL.md` directories, and whether it has an extension or plugin system and
   in what language. Do not guess a path; an install into a path the harness
   never reads is worse than no install.
2. Write `internal/provision/adapter_<name>.go`: one exported function
   returning a `Harness` with the root, the mounts, and the leaf names that
   harness expects. A skills-only harness mounts `KindSkill` and stops there.
3. Confirm the root exists on a machine that has the harness. `New` refuses an
   absent root and names it, so a wrong path is a refusal, not a silent no-op.
4. Give the verb a choice when a second harness exists: a `--harness` flag on
   the `provision` row (`internal/clidoc/clidoc.go#cliCommands`), defaulting to
   pi so today's command keeps working. One adapter and no flag is a
   one-harness package; two adapters and no flag is a guess.
5. Add the harness to `TestNewSecondHarness`'s shape if it is skills-only, or
   extend the table with its mount list, and make `make provision-check` name
   the harness it checked (`Makefile`) instead of assuming pi.
6. Say it in the README's harness section and in `docs/internals/README.md`.
   The page table is the map; a page nobody links is a page nobody finds.

## What does not transfer

The extension is glue, per harness. opencode plugins are JavaScript with their
own hook shape; Claude Code and Codex speak MCP. Porting `dm-agent.ts` is N
shims that rot on their own release schedule, so do not pretend the file is the
portable half.

Two honest options when a harness needs typed tools:

- **Skills only.** Mount the skill, let the agent run `dm` like any other
  process. `dm llms` is the mechanical surface and needs no install. This is
  the cheap path and it covers most of what the extension does.
- **One protocol instead of N shims.** A stdio MCP server in dm (`dm mcp`)
  exposing the same four groups, `dm_layout`, `dm_pane`, `dm_box` and
  `dm_turn`, because their bodies are already JSON actions over the RPC. Every
  harness that speaks MCP then loads one config entry, and provision's work per
  harness shrinks to writing that entry. That is not built, and it is an
  encounter, not a patch: it needs the four schemas, the exposure rules the
  extension already encodes, its output caps, its timeout defaults, and a
  parity test proving the server answers what the extension answers.

## Rules that keep it cheap

- No harness name in the neutral package. Not in a path, not in a branch, not
  in an error string. The error names the harness it was given
  (`internal/provision/provision.go#ErrNoHarness`), never a fixed one.
- Mounts are data, so a harness is a list, not a code path.
- No registry and no plugin loading while one harness exists. Two adapters and
  a switch is honest; a registry for one adapter is a guess about the second.
- Do not fork the skill per harness. If a harness needs a different sentence,
  the sentence belongs in that harness's extension, not in a copy of the skill
  that will drift.
