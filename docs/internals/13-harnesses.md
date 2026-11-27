# Teaching a harness

dm teaches an agent harness with a skill directory the harness reads and an
extension file that registers dm's actions as tools. Only the extension knows a
harness API. Read this page to see what the installer assumes, and what a
second harness costs.

## Two mounts

`dm provision` links two mounts from this checkout
(`internal/provision/provision.go#kit`):

- The skill, `skills/dm-fleet`, mounts as `KindSkill`
  (`internal/provision/provision.go#KindSkill`). It is a directory holding
  `SKILL.md` in the Agent Skills layout: prose and `dm` verbs, with no harness
  API. pi, Claude Code and opencode all read that layout. Only the directory
  differs.
- The extension, `skills/dm-agent/dm-agent.ts`, mounts as `KindExtension`
  (`internal/provision/provision.go#KindExtension`). It is pi's extension API:
  it imports `ExtensionAPI` and registers each tool with `pi.registerTool`
  (`skills/dm-agent/dm-agent.ts`). Its bodies are harness-neutral, though.
  Every action builds a `dm ... --json` argv or a `dm exec` action body, and
  reads JSON back.

A harness with no extension concept mounts the skill alone. One mount is a
complete install.

## The extension point

The installer is data plus link rules, and it names no harness:

- `Harness` (`internal/provision/provision.go#Harness`) carries a name, a root
  that must exist, and a list of `Mount`
  (`internal/provision/provision.go#Mount`). A mount names a `Kind`, the harness
  directory the link lands in, and the leaf name it takes there.
- `New` (`internal/provision/provision.go#New`) turns those mounts into
  `Target`s through the `kit` table. It refuses an absent root, an empty mount
  list, an unknown kind, a mount with no directory or name, and two mounts on
  one path. Nothing else in the package asks which harness it holds.
- `Check`, `Apply` and `Remove` (`internal/provision/link.go#Check`,
  `internal/provision/link.go#Apply`, `internal/provision/link.go#Remove`) work
  on the plan's targets. Apply creates a mount's directory when it is absent.
  Remove deletes only a link that points inside the checkout, and leaves a file
  it did not make.
- The adapter is one function, `Pi` (`internal/provision/adapter_pi.go#Pi`). It
  is the only file that spells `~/.pi/agent`.
- The verb (`internal/cli/provision.go#runProvision`) builds that one adapter
  and passes it to `New`. It is the only caller.

The guarantee is mechanical. `TestNewSecondHarness`
(`internal/provision/provision_test.go#TestNewSecondHarness`) plans, applies and
checks a harness that is not pi, and mounts no extension. If that test needs a
change to the engine, the extension point has leaked.

## Adding a harness

`.agents/skills/dm-add-harness/SKILL.md` is the checklist. It names the facts to
read from the harness docs, the adapter file to write, the mount list, the
`--harness` flag, the test to extend and the pages to update. This page carries
the design.

## What does not transfer

The extension is glue, per harness. opencode plugins are JavaScript with their
own hook shape. Claude Code and Codex speak MCP. Porting `dm-agent.ts` is N shims
that rot on their own release schedule, so do not pretend the file is the
portable half.

Two honest options when a harness needs typed tools:

- **Skills only.** Mount the skill and let the agent run `dm` like any other
  process. `dm llms` is the mechanical surface and needs no install. This is the
  cheap path, and it covers most of what the extension does.
- **One protocol instead of N shims.** A stdio MCP server in dm (`dm mcp`)
  exposes the same four groups, `dm_layout`, `dm_pane`, `dm_box` and `dm_turn`,
  because their bodies are already JSON actions over the RPC. Every harness that
  speaks MCP then loads one config entry, and provision's work per harness
  shrinks to writing that entry. That is not built. It is a plan, not a patch:
  it needs the four schemas, the exposure rules the extension already encodes,
  its output caps, its timeout defaults, and a parity test proving the server
  answers what the extension answers.

## Keeping it cheap

- The neutral package names no harness in a path, a branch or an error string.
  An error names the harness it was given
  (`internal/provision/provision.go#ErrNoHarness`).
- Mounts are data, so a harness is a list, not a code path.
- No registry and no plugin loading while one harness exists. Two adapters and
  a switch is honest. A registry for one adapter guesses at the second.
- Do not fork the skill per harness. A sentence that differs belongs in that
  harness's extension, not in a copy of the skill that will drift.
