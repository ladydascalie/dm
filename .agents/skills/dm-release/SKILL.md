---
name: dm-release
description: "Cut a dm release: choose the next version, prove the artifacts locally, tag, watch the GitHub workflow, verify the published assets, and recover a failed publish. Use when asked to bump dm's version, ship a release, publish a tag, or check why a release run failed."
---

# dm release

This page is the runbook for cutting a dm release. A release is a tag, not a
file edit. Nothing in the tree holds the version. The workflow stamps the tag
into the binary with `-X dm/internal/version.Version`, so `vX.Y.Z` is the only
place the number lives. Pushing the tag builds four tarballs, one
`checksums.txt` and a release body from commits.

`docs/internals/12-release.md` is the design.

## Check the tree

```sh
git rev-parse --show-toplevel        # the dm checkout
git branch --show-current            # main
git status --porcelain               # empty
git fetch --tags --force
```

A release ships `main`. A dirty tree means the tag would describe code nobody
merged.

## Pick the number

```sh
prev="$(git describe --tags --abbrev=0 2>/dev/null || printf '')"
git log --oneline ${prev:+$prev..main} | head -40
```

Read the subjects since the last tag:

| what landed | next |
| --- | --- |
| `feat:` | minor |
| `fix:`, `perf:`, `docs:` only | patch |
| a change that breaks a verb, a flag, a state file or the config schema | major |
| nothing, but the workflow or the toolchain moved | patch |

Below `1.0.0`, a minor may break a caller. Say so in the release body. Never
re-cut a version that users already installed. If the number is wrong, move to
the next one.

## Prove the tarballs

```sh
make release-check                       # version + release tests, then the four builds
go test ./...                            # the repo check, golden docs included
(cd out && sha256sum -c checksums.txt)   # four OK
VERSION=<next> make dist                 # the same four with the real stamp
bash scripts/release-notes.sh "$prev" "v<next>"   # read the body before it is public
```

`make dist` writes `out/dm_<goos>_<goarch>.tar.gz` and `out/checksums.txt`, and
removes `out/dm`. A refused pair means the matrix disagrees with
`internal/release/release.go#matrix`.

The body has two sources, and the workflow picks one. `docs/releases/v<next>.md`
wins when it exists, and the workflow publishes it verbatim. Without it the body
is the commit range since the previous tag. Write the file and commit it before
tagging when the commit list is not the story. That covers the first tag always,
and any release that needs a headline or a migration note. With no previous tag
the generated body is the entire history.

The generated body groups by subject prefix: `feat`, `fix`, `perf`, `docs`, and
everything else under Other. Most subjects in this repo use an area prefix
(`webui:`, `notify:`, `upgrade:`), so they land in Other. That is fine for a
patch release. It is the second reason a release worth reading gets a curated
file. Write `fix(<area>):` when a shipped fix must group under Fixes.

Rehearse the workflow when the workflow or the release library changed since the
last tag. A `workflow_dispatch` run builds and publishes nothing:

```sh
gh workflow run release.yaml --ref main
gh run watch
```

The run ends with four tarballs on the run and no release.

## Cut the tag

```sh
git tag -a "v<next>" -m "dm v<next>"
git push origin "v<next>"
gh run watch
```

The tag shape is strict: `v<major>.<minor>.<patch>`, optional prerelease and
build suffixes, semver identifiers only, no leading zero. A name outside that
grammar stops the job before any tarball exists.

## Verify the release

```sh
gh release view "v<next>"
gh release download "v<next>" -D /tmp/dm-release -p 'dm_*' -p checksums.txt
(cd /tmp/dm-release && sha256sum -c checksums.txt)
tar xzf /tmp/dm-release/dm_linux_amd64.tar.gz -C /tmp/dm-release
/tmp/dm-release/dm version               # names v<next> and the commit
```

Check three things beyond a green run:

- four tarballs, `checksums.txt` and `LICENSE`, and no windows asset;
- the body is `docs/releases/v<next>.md` when you wrote one, else only the
  commits in `v<prev>..v<next>` grouped under Features, Fixes, Performance,
  Docs, Other;
- `dm version` inside the linux tarball names the tag.

Then move your own install and confirm the notice machinery agrees:

```sh
dm upgrade
dm version
dm upgrade --check        # "is current"
```

A live session keeps the old binary until it restarts.

## When it goes wrong

The publish job needs all four build jobs, so a build failure publishes nothing.
The tag stays. Fix the tree, then move the tag:

```sh
git tag -d "v<next>"
git push origin ":refs/tags/v<next>"      # only when no release exists yet
git tag -a "v<next>" -m "dm v<next>" <fixed-commit>
git push origin "v<next>"
```

A release that exists with wrong assets or wrong notes:

```sh
gh release edit "v<next>" --notes-file <file>   # notes only, no rebuild needed
gh release delete "v<next>" --yes --cleanup-tag # assets only, then re-tag
```

Read the failed step before acting. `gh run view --log-failed` names it.

Once `dm upgrade` has moved a machine onto a version, that version is spent.
Publish a new patch instead of rewriting history under an installed binary.

## Platform list

The matrix lives in three places and they must agree:

- `.github/workflows/release.yaml` (the build matrix),
- `Makefile` (`DIST_PAIRS`),
- `internal/release/release.go#matrix`, plus its tests.

Windows is deliberately absent and stays absent. An asset name is a contract
`dm upgrade` resolves, so a rename ships with the library change that reads it.

## Agent limits

An agent may prepare the branch, run every proof above, draft the release body
and rehearse the workflow. Tagging and pushing belong to the operator. Nothing
reaches the default branch or the release page without them.
