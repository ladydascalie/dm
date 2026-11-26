# Shipping: the release, the stamp and the update check

dm ships as a GitHub release of four tarballs, one per platform, beside a
`checksums.txt`. The linker stamps the tag and the commit into every binary, so
`dm version` reports what was built, `dm upgrade` replaces the running file with
a newer release, and an attach prints one line when a newer tag exists. This
page answers how a release is cut, where the stamp lives, what the two verbs do,
and what the boot check reads and writes.

Four packages carry it. `internal/version` holds the stamp and orders two
tags (`internal/version/version.go#package`). `internal/release` is the GitHub
side and the file work an upgrade needs (`internal/release/release.go#package`).
The verbs sit in `internal/cli` (`internal/cli/upgrade.go#runUpgrade`) and the
boot notice sits in `internal/client`
(`internal/client/update.go#updateNotice`).

## Cutting a release

The runbook is a repo skill, `.agents/skills/dm-release/SKILL.md`, which pi
loads when it works in this checkout. It carries the checklist, the commands
and the recovery paths. This page carries the design.

Cut the tag on `main` and push it:

```sh
git tag v0.4.0
git push origin v0.4.0
```

A pushed tag matching `v<major>.<minor>.<patch>` starts
`.github/workflows/release.yaml`. The workflow's `tag shape` step checks the
name against a stricter grammar than `version.IsRelease` accepts
(`internal/version/version.go#IsRelease`): semver identifiers only, and no
leading zero in a numeric field. It accepts nothing IsRelease rejects, because
the tag it passes is stamped into the binary and `dm upgrade` refuses a version
that is not a release. A
`workflow_dispatch` run builds the same four archives and publishes nothing: it
leaves them on the run.

The matrix has four rows, and `internal/release` holds the same list
(`internal/release/release.go#matrix`): linux/amd64, linux/arm64, darwin/amd64
and darwin/arm64. There is no windows build. Each row builds with
`CGO_ENABLED=0`, strips the binary, and tars one file:

| Asset | Platform |
| --- | --- |
| `dm_linux_amd64.tar.gz` | linux, amd64 |
| `dm_linux_arm64.tar.gz` | linux, arm64 |
| `dm_darwin_amd64.tar.gz` | darwin, amd64 |
| `dm_darwin_arm64.tar.gz` | darwin, arm64 |

`AssetName` is the Go side that spells these names
(`internal/release/release.go#AssetName`); the workflow and `Makefile` build the
same names from `${GOOS}` and `${GOARCH}`. A pair outside the matrix is an
error that names the pair and lists the four dm ships.

Each archive holds exactly one regular file, `dm`, at the archive root
(`internal/release/extract.go#Extract`). The publish job refuses to run unless
four tarballs arrived. It then writes `checksums.txt` in sha256sum format,
`sha256sum dm_*.tar.gz > checksums.txt`, and checks it with `sha256sum -c`
before anything is uploaded. The reader accepts that format and refuses a
missing line, a duplicate and a digest of the wrong shape
(`internal/release/download.go#Checksum`).

The release notes come from the commits between the previous tag and this one.
`scripts/release-notes.sh` prints `## New in <tag>` and then the groups that
hold a commit, in this order: Features, Fixes, Performance, Docs, Other. One
bullet per commit, oldest first, the short sha in parentheses, and the
`type:` or `type(scope):` prefix stripped. A merge or a landing commit keeps
its subject whole under Other, because that subject is the record of what
landed. The workflow resolves the previous tag as the nearest tag behind the

A curated body wins over the generated one. `docs/releases/<tag>.md` is copied
into the release verbatim when it exists, and only a release without it falls
back to the commit range. The first tag needs one, because there is no
previous tag to bound the list, and any release whose story is not its commit
list deserves one (`docs/releases/v0.1.0.md`).
new one and passes both to the script.

`make dist` builds the same matrix locally, so a lane can prove the pipeline
without GitHub: it stamps `VERSION` or `git describe --tags --always`, writes
the four tarballs and `checksums.txt` into `out/`, and `make release-check` runs
the release packages' tests first.

## The stamp

The stamp is two package-level strings in `internal/version`
(`internal/version/version.go#Version`, `internal/version/version.go#Commit`).
An unstamped build, the one `go build .` makes, reports `dev` and `none`
(`internal/version/version.go#DevVersion`,
`internal/version/version.go#DevCommit`). The release workflow and `make dist`
write both with the linker:

```sh
go build -ldflags "-X dm/internal/version.Version=0.4.0 \
                   -X dm/internal/version.Commit=abc1234"
```

`version.String` renders what the operator reads: the version alone when no
commit was stamped, otherwise `0.4.0+abc1234`
(`internal/version/version.go#String`). `version.IsRelease` accepts a bare
semantic version: three dot-separated integers, an optional `-prerelease` and an
optional `+build`, no leading `v` (`internal/version/version.go#IsRelease`).
`version.Compare` orders two of them: a prerelease sorts below the release it
belongs to, build metadata is ignored, and each numeric field compares as a
number, so 0.2.0 is below 0.10.0 (`internal/version/version.go#Compare`). When
either side does not parse, Compare falls back to string comparison rather than
guess.

The workflow does not trust a stripped tarball to prove its own stamp. Before
uploading, each build job also builds the runner's own binary unstripped and
asserts that `./dm version` names the tag, so a stamp that never reached the
binary fails the run with no artifact to publish.

## dm version

`dm version` prints `dm <version> (<goos>/<goarch>)`, and appends the commit as
`+<commit>` inside the version (`internal/cli/upgrade.go#runVersion`). A dev
build prints `dm dev (linux/amd64)`. `--json` prints one object and no wrapper:

```json
{"version":"0.4.0","commit":"abc1234","os":"linux","arch":"amd64"}
```

The object is `versionReport` (`internal/cli/upgrade.go#versionReport`), and the
row lives in the documentation group of the command table
(`internal/clidoc/clidoc.go#cliCommands`).

## dm upgrade

`dm upgrade` moves the running binary to the latest release. The target is the
running file resolved through every symlink
(`internal/cli/upgrade.go#installedBinary`), so an install that is a link into
a `bin` directory keeps the link: the resolved file is replaced, not the link.
A build that is not a release is refused before any network call, naming the
resolved path, unless `--force` is passed
(`internal/cli/upgrade.go#runUpgrade`).

The verb reads the newest release from the releases base and strips the leading
`v` from the tag (`internal/release/release.go#Reader.Latest`). It reports
`dm <version> is current` when the latest tag is not newer, and stops unless
`--force`. `--check` prints either `dm <version> is current` or
`dm <version> to <to> is available` and exits 0 without writing anything.

An install runs these steps in this order
(`internal/cli/upgrade.go#installRelease`):

1. Name the asset for this platform with `release.AssetName` and find it in the
   release's own asset list (`internal/cli/upgrade.go#findAsset`).
2. Fetch `checksums.txt` from that same release, not from another
   (`internal/cli/upgrade.go#fetchChecksums`,
   `internal/cli/upgrade.go#checksumAsset`).
3. Download the tarball into a temp directory outside the target's directory,
   hashing as it writes, and compare the digest with the checksums line
   (`internal/release/download.go#Download`). A mismatch, a short body or a
   failed request removes the file and names both digests.
4. Create a second temp directory beside the target and extract the `dm` from
   the verified tarball there (`internal/release/extract.go#Extract`). Extract
   refuses an entry that leaves the archive root, a second entry, a directory,
   a symlink and an archive with no `dm`; it writes the binary 0755.
5. Rename the fresh file over the target (`internal/release/replace.go#Replace`).
   It writes a temporary file, syncs it, chmods it 0755 and renames it over the
   target, so the target is the old binary or the new one and never half of
   each.

Nothing writes into the target's directory until the digest matched, and both
temp directories go in a deferred cleanup, so a failure leaves the old binary
where it was. On success the verb prints three lines: the move, the installed
path, and `live sessions keep the old binary until they restart`. That last
line is true of dm's architecture: each server and pane daemon was spawned from
its own copy of the executable, so none of them reloads the file on disk.

## The boot check

An attach tells the operator about a newer release without delaying the
session. This is the fourth process role, `dm __update-check`
(`main.go#main`, `internal/client/update.go#RunUpdateCheck`): one release read,
one cache write, exit 0. It takes the state root from `paths.StateDir`
(`internal/paths/paths.go#StateDir`) and holds the answer in `update.json`
(`internal/client/update.go#updateFile`):

```json
{"checkedAt":"…","latest":"0.4.0","notified":"0.4.0","notifiedAt":"…"}
```

`CheckedAt` is when the check last ran, `Latest` is the newest tag with its `v`
stripped, and `Notified` and `NotifiedAt` record the last version announced and
when (`internal/client/update.go#updateCache`). The cache is written through a
temporary file and a rename (`internal/client/update.go#writeUpdateCache`), so a
reader sees the old file or the new one. A missing, unreadable or corrupt cache
is simply a cache that is not there (`internal/client/update.go#readUpdateCache`).

`RunWithLayout` runs the check on every attach, after the tty check and before
the alternate screen is entered (`internal/client/client_model.go#RunWithLayout`).
It prints the notice to stderr, so the line survives the frame that follows and
is still there when the client exits. When the cache is stale it leaves one
detached refresh behind: the child gets its own session
(`internal/client/update_unix.go#detachUpdate`, and the browser build's
`internal/client/update_js.go#detachUpdate` does nothing), its stdin, stdout and
stderr are `/dev/null`, and the attach does not wait for it
(`internal/client/update.go#spawnUpdateCheck`). A spawn that fails is ignored;
the next attach tries again.

Two TTLs bound how often that happens. A successful answer stays fresh for 24
hours (`internal/client/update.go#updateTTL`); a failed answer, one where the
release read left `Latest` empty, stays fresh for one hour
(`internal/client/update.go#updateFailTTL`), so an offline start does not retry
on every attach. `updateFresh` is the rule; an absent `CheckedAt` is stale
(`internal/client/update.go#updateFresh`).

The notice is `dm <latest> is out, run dm upgrade`. It is quiet unless both the
running version and the cached latest are releases, the cached latest is newer,
and that version was not already announced within 24 hours
(`internal/client/update.go#updateNotice`,
`internal/client/update.go#updateNoticeTTL`). Announcing records the version and
the day in the cache, so the line lands once per version per day rather than
once per attach. A cache write that fails is ignored.

The refresh is skipped, and `update.json` is left alone, when any of these
holds (`internal/client/update.go#shouldCheck`):

- `DM_NO_UPDATE_CHECK` is set (`internal/client/update.go#updateSkipEnv`).
- `CI` is set (`internal/client/update.go#ciEnv`). A CI machine is not an
  operator waiting for a notice.
- the running build is not a release, so a `dev` build never checks
  (`internal/version/version.go#IsRelease`).
- the cache is still fresh.

`DM_NO_UPDATE_CHECK` stops the refresh. It does not reach the notice itself: a
cache that already holds a newer release still prints its line until that
version's day is up. To silence the line as well, remove `update.json` or wait
out the window.

## DM_RELEASE_API

`DM_RELEASE_API` replaces the releases base the reader asks for; the default is
`https://api.github.com/repos/ladydascalie/dm/releases`
(`internal/release/release.go#DefaultAPI`,
`internal/release/release.go#APIEnv`). `NewReader` reads it once
(`internal/release/release.go#NewReader`), trims a trailing slash and a trailing
`/latest`, because `Latest` appends that segment. Both `dm upgrade` and the boot
check read through this one seam, and a release response carries each asset's
own download URL, so a stub server can point a test anywhere. A body over 1 MiB
is refused (`internal/release/release.go#maxBody`), and one request is bounded
by a 30 second timeout (`internal/release/release.go#Timeout`).

## Where the next thing goes

- A new platform is a row in three places: the workflow's matrix, the
  `DIST_PAIRS` list in `Makefile`, and `matrix`
  (`internal/release/release.go#matrix`). Add it to all three, or `AssetName`
  will refuse a platform the release publishes.
- A new cache field is `updateCache`
  (`internal/client/update.go#updateCache`) plus the refresh that fills it
  (`internal/client/update.go#RunUpdateCheck`), and the two TTL rules if it
  needs one.
- A new verb over the release library goes through the CLI table, as
  [Config and the CLI surface](10-config.md) says; the release library returns
  values and errors and prints nothing
  (`internal/release/release.go#package`).

## See also

- [Entry point](01-entry-point.md) for the four process roles and the sentinel
  switch.
- [Config and the CLI surface](10-config.md) for the command table and the
  environment.
- [The layers](11-layers.md) for where `internal/version` and
  `internal/release` sit.
- [The repository README](../../README.md) for the install and update steps.
