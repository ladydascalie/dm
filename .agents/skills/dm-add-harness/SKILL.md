---
name: dm-add-harness
description: Add support for another agent harness to dm provision, such as opencode, Claude Code or Codex, or decide whether the skill alone is enough. Use when asked to install dm into a different agent, to port the dm tools to another harness, or to check whether the provision seam still keeps a second harness cheap.
---

# Add a harness to dm provision

Read `docs/internals/13-harnesses.md` first. It carries the design: the two
artefacts, the mount model, what does not transfer, and the parity bar for a
protocol server. This page is the checklist.

## Decide the scope before writing code

Ask one question: does this harness need typed tools, or can it run commands?

- **Commands.** The fleet skill plus `dm llms` is the whole install. Mount the
  skill and stop. This is the default answer, and it covers most of what the pi
  extension does.
- **Typed tools.** Only then consider an extension shim for this harness, and
  read the parity section below before promising anything.

Do not port `skills/dm-agent/dm-agent.ts` into a second language because it
looks easy. It is pi's API, and every port is a shim with its own release
schedule.

## The checklist

1. Find the harness's own facts, from its docs, not from memory: the directory
   it discovers `SKILL.md` under, whether it has an extension or plugin system,
   and the language of that system. An install into a path the harness never
   reads is worse than no install.
2. Write `internal/provision/adapter_<name>.go`: one exported function
   returning `provision.Harness` with `Name`, `Dir`, and `Mounts`. Spell every
   path of that harness here and nowhere else.
3. Mount `KindSkill` for the skill directory. Mount `KindExtension` only when
   this harness really loads a dm extension, and give the link the leaf name
   the harness expects. A skills-only harness mounts one artefact and is done.
4. Confirm `Dir` exists on a machine with the harness installed. `New` refuses
   an absent root and names it, so a wrong path fails loudly.
5. Give `dm provision` a `--harness` flag only when a second adapter exists.
   Default to pi so today's command keeps working. Update the row in
   `internal/clidoc/clidoc.go`; the flag table is the parser.
6. Extend `TestNewSecondHarness` in `internal/provision/provision_test.go`
   with the new mount list. If that test needs an engine change, the seam
   leaked: fix the seam, not the test.
7. Make `make provision-check` name the harness it checked instead of assuming
   pi, and update the README's harness section and the page table in
   `docs/internals/README.md`.

## Invariants

- No harness name inside the neutral package. Not a path, not a branch, not an
  error string. Errors name the harness they were handed.
- Mounts are data. A harness that needs a special case in `New`, `Check`,
  `Apply` or `Remove` is a design smell, not a harness.
- No registry, no plugin loading and no config file that names adapters while
  one adapter exists.
- Never fork `skills/dm-fleet`. A harness-specific sentence belongs in that
  harness's extension, not in a copy of the skill.
- Links point into the checkout, so the skill is live-edited. Keep that: a copy
  goes stale the moment dm's verbs change.

## If typed tools are really needed

Two honest paths:

- For one harness, a shim that builds `dm ... --json` argv or a `dm exec`
  action body and parses JSON, exactly as the pi extension does. It must not
  parse prose, and it must not reimplement a verb.
- For several harnesses, propose `dm mcp` instead: one stdio MCP server over
  the same four groups, and one config entry per harness. That is an encounter.
  It needs the four schemas, the exposure rules (`direct` for three,
  `model-only` for `dm_turn`), the output caps and timeout defaults the
  extension uses, and a parity test that both answers agree.

Do not start the MCP server as a side effect of adding one harness. Propose it
with that cost written down, and let the operator choose.

## Prove it

```sh
go test ./internal/provision/ ./internal/cli/ ./internal/archdoc/
go run . provision --print          # the new harness's links, and pi's unchanged
go run . provision --check          # against the real home, for pi
```

Report the mount list, the test that proves the second harness, and anything
the harness's docs left ambiguous. Do not commit a symlink into a user's home
as part of the proof.
