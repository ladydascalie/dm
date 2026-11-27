# Secrets

This page answers how a value travels from a store into a program without
landing on an accidental channel. Read it before you touch the secret registry,
the pane's redaction stage, or anything that prints a fetched value.

Three nouns recur. A store is a program that prints a value on stdout: `op` or
`pass`. A pane runs one program in a pseudo terminal (pty) and keeps a VT screen
and a scrollback buffer for it. A child is any process the server or a pane
starts.

## The threat model

The person at the keyboard is not the adversary. Every program runs as the same
user, so any child can read the server's `/proc/<pid>/environ` and its memory.
`internal/pane/env.go#Env` states the limit: that rule narrows a shell's
environment for accidents, and is not a secrecy boundary.

This page guards the accidental channel, not a determined read. The channels are
the screen, the scrollback buffer, a shell history and argv. A child's
environment, a log line, the state file (`state.json`,
`internal/paths/paths.go#StatePathAt`) and a reply body are channels too. A
value on any of them is the loss.

The promise is narrow and exact: a value sits on no channel a program reads
unasked (`internal/secrets/secrets.go#package`). No rule here claims a value
cannot be found.

## The rules

Each rule protects one thing from one reader. Forgetting it puts a value on a
channel that reader already watches.

| rule | enforced by |
| --- | --- |
| A record holds an address and tool configuration, so a `state.json` reader learns where a value lives, never the value. | `internal/session/secret.go#Secret`, `internal/session/secret.go#secretEnvKeys` |
| A fetch passes one fixed argument template and named configuration, so argv and the child's environment yield no value. | `internal/secrets/secrets.go#Fetch` |
| A warning names the secret, the tool and a byte length, so a log a program reads yields no value. | `internal/server/server_secrets.go#secretBoundsWarning` |
| A pane inherits an allowlisted environment, so a shell never receives a credential the server holds. | `internal/pane/env.go#Env`, `internal/pane/env.go#paneEnvKeep` |
| A hydrate writes a fresh 0600 regular file through a rename, so a planted symlink cannot hand the value away. | `internal/secrets/secrets.go#Hydrate` |
| Redaction runs before publish, so the screen, the scrollback buffer and every read-back hold the replacement. | `internal/pane/pane.go#paneProc.publishLocked`, `internal/pane/pane.go#paneProc.readLoop` |
| A value under 4 bytes or over 4096 bytes stays out of the list, and the server names that secret. | `internal/pane/scrub.go#MinSecretLen`, `internal/server/server_secrets.go#secretBoundsWarning` |
| The list holds 256 values, registered names first, so a repo mapping cannot push a live value out. | `internal/secrets/redact.go#MaxRedactValues`, `internal/server/server_secrets.go#server.secretValuesLocked` |
| A typed value refuses a newline and every control byte, so no part of it runs as a command. | `internal/server/server_secrets.go#secretTypeShape` |
| The proxy fills one header for a pinned upstream and follows no redirect, so a 3xx carries the value nowhere. | `internal/proxy/proxy.go#Proxy.Register` |
| An untrusted mapping registers no secret and reads only under its own directory, so a repo file cannot pin an upstream. | `internal/server/server_sync.go#secretSyncTrusted`, `internal/server/server_sync.go#checkFileConfined` |
| The toggle gates the interactive socket only, so any program that sends the attach frame can reveal a pane. | `internal/server/server_rpc.go#server.handleReq` |

## What may be logged

No value, in any part. A line may name the secret and the shape of what it lost.
The table below is the whole answer. `internal/secrets/secrets.go#Fetch` is the
model: its error carries an exit code, never the tool's output.

| safe in a log line | never in a log line |
| --- | --- |
| the secret name | the value, any part of the value, or its length plus the value |
| the tool name, `op` or `pass` | the tool's stdout or stderr |
| the reference, which is an address such as `op://Personal/github/token` | an env entry whose key failed the allowlist |
| the value's length in bytes | a key or a value read from a `file` block |
| a count of values a cap dropped | the endpoint URL, or the proxy prefix in it |
| the record's upstream URL | a header template after the value is filled in |

A secret name is safe because the operator chose it. A reference is safe because
it is the address the store resolves. A file block's key is untrusted data and
can equal the value beside it. The sync reports counts, never keys
(`internal/server/server_sync.go#server.ingestSecretFile`). The proxy prefix
grants the value to any request that carries it. An endpoint in a log is the
value by another route.

## The forms

A registered secret is a record, not a value. The state file holds an address.
The value lives only in memory, in the registry
(`internal/secrets/secrets.go#Registry`), until a fetch fills it.

`session.Secret` (`internal/session/secret.go#Secret`) holds the name, the tool,
the reference, the upstream base and header template, and extra env entries. The
env entries are the configuration a store reads to find itself, such as
`GNUPGHOME` for `pass`. They answer to a positive allowlist
(`internal/session/secret.go#secretEnvKeys`), so an entry is never a value.

| verb | action | what it leaves behind |
| --- | --- | --- |
| `dm secret register` | `secret-register` (`internal/server/server_secrets.go#server.secretRegister`) | one record in `state.json` |
| `dm secret list` | `secret-list` (`internal/server/server_secrets.go#server.secretList`) | a reply of name and reference, no value |
| `dm secret revoke` | `secret-revoke` (`internal/server/server_secrets.go#server.secretRevoke`) | one record and every live copy of its value, gone |
| `dm secret use` | `secret-use` (`internal/server/server_secrets.go#server.secretUse`) | a live value in the registry and every pane's list, and an endpoint when the record names an upstream |
| `dm secret hydrate` | `secret-hydrate` (`internal/server/server_secrets.go#server.secretHydrate`) | a 0600 file at `--to`, and the same live value |
| `dm secret type` | `secret-type` (`internal/server/server_secrets.go#server.secretType`) | keystrokes in a pane's pty, and nothing durable |
| `dm secret sync` | `secret-sync` (`internal/server/server_sync.go#server.secretSync`) | records and values from a `.dm-secrets.kdl` mapping |

`checkSecretShape` (`internal/server/server_secrets.go#checkSecretShape`) runs
`session.Secret.Validate` and `proxy.Validate` before anything is stored. It
refuses a bad reference, a missing `{}` slot in the header template, and a
one-sided upstream and header. A bad shape costs no fetch. A sync block answers
to the same check.

The mapping is KDL, the node format the keymap and theme files also use
(`internal/kdl`). A `secret` block is a top-level node naming one record and
carrying `tool`, `ref`, `env`, `upstream` and `header`. A `file` block names one
path to read, and donates each line's text after the first `=` to redaction
(`internal/server/server_sync.go#parseSecretsMapping`). A sync never fetches,
and never starts the proxy.

## Where redaction runs

Redaction is a stage in the pane's output path: the last filter a chunk passes
before publish. A chunk is one read of the pty of at most 32 KiB
(`internal/pane/pane.go#paneProc.readLoop`). The OSC filter runs first and
decodes the terminal escapes (`internal/osc/osc.go#Filter`). The redaction stage
runs next, and publish follows (`internal/pane/pane.go#paneProc.publishLocked`).
The screen, the scrollback buffer, a broadcast and a read-back all draw replaced
bytes.

Replacement keeps the byte length, one `*` per value byte
(`internal/secrets/redact.go#writeRedacted`). A published byte count, the anchor
a client's copy starts from and the offsets a yank returns are positions in that
stream. A shorter replacement would move every mark.

Matching is leftmost-longest. A proper prefix of a longer value is held back
until the next chunk (`RedactInto`, `internal/secrets/redact.go#RedactInto`). A
value with a newline is matched in its CRLF form too. The pty rewrites LF to
CRLF on the way out (`valueForms`,
`internal/secrets/redact.go#valueForms`).

The OSC 7501 status message and its app name are redacted outside the stream, by
`redactText` (`internal/pane/scrub.go#scrubber.redactText`). The filter decodes
them first.

One matcher serves every surface. A surface that holds text another program
reads unasked redacts it with the same needles (`RedactText`,
`internal/secrets/redact.go#RedactText`), as the message log and the ask store
do.

The server also pushes the list to every sibling session's server. The proxy
answers on loopback, so a pane in any session can echo the value
(`internal/server/server_secrets.go#server.coverSiblings`). A surface outside
that push holds the value.

## The proxy

`internal/proxy` binds a loopback port the kernel picks (`127.0.0.1:0`,
`internal/proxy/proxy.go#New`) and mints a random 16-byte prefix per secret
(`internal/proxy/proxy.go#Proxy.Register`). The server holds the value, and the
header template names its slot, `Authorization: Bearer {}`.

The upstream is pinned: a request goes only to the base URL stored at register
time. No code puts the endpoint into a child. `secret-use` replies with the URL
(`internal/server/server_secrets.go#server.secretUse`), and `dm secret use`
prints it on the operator's stdout (`internal/cli/cli.go#secretUseVerb`). The
operator exports it into a shell under a name the program reads.

A request to that URL is the proxied request. `forward`
(`internal/proxy/proxy.go#entry.forward`) rebuilds the upstream URL from the
pinned base plus the path after the prefix. It copies the inbound headers except
the injected one, and fills the header.

The upstream must be https, or plain http to the loopback
(`checkUpstream`, `internal/proxy/proxy.go#checkUpstream`). The template carries
one header, exactly one `{}` and no CR or LF (`fillTemplate`,
`internal/proxy/proxy.go#fillTemplate`).

## The sync trust rule

`dm secret sync` reads `./.dm-secrets.kdl` unless `--from` names a path. It
sends the server an absolute path plus a trust verdict: a boolean saying the
mapping may register secrets and read files anywhere
(`internal/cli/cli.go#secretSyncVerb`).

The CLI computes the verdict with `mappingTrusted`
(`internal/cli/cli.go#mappingTrusted`). It is true when the operator typed
`--trust`, or when the mapping resolves under the config directory. That
directory is `$XDG_CONFIG_HOME/dm` when set and `~/.config/dm` otherwise
(`mappingUnderConfigDir`, `internal/server/server_sync.go#mappingUnderConfigDir`).

The server takes a sent verdict: `secretSyncTrusted` returns the wire value
whenever it is set (`internal/server/server_sync.go#secretSyncTrusted`), and
`Trust` is optional (`internal/wire/wire.go#Msg`). A request with no verdict is
decided by path. `dm exec` sends none, because it takes one raw JSON body from
its argument (`internal/cli/cli.go#Run`). Any socket client can therefore mark a
repo mapping trusted. The path rule is the CLI's default verdict, and the server
enforces whichever branch the verdict names.

An untrusted mapping registers no secret blocks, so it cannot give the proxy an
upstream to attach the operator's store value to. It reads files only under its
own directory (`checkFileConfined`,
`internal/server/server_sync.go#checkFileConfined`).

## What a pane inherits

The server builds a pane's environment once, in `paneCmd`
(`internal/pane/pane.go#paneCmd`), from an allowlist (`Env`,
`internal/pane/env.go#Env`). A name passes when it is on the keep list
(`paneEnvKeep`, `internal/pane/env.go#paneEnvKeep`): PATH, HOME, USER, LOGNAME,
SHELL, TERM, TERM_PROGRAM, COLORTERM, LANG, TZ, TMPDIR, WAYLAND_DISPLAY,
DISPLAY, SSH_AUTH_SOCK, XAUTHORITY and DM_STATE.

A name also passes when it starts with `LC_` or `XDG_`. It passes the socket
exemption when it ends in `_SOCK`, holds an absolute path, and is not
credential-shaped. Credential-shaped means the name carries a word from
`secretEnvHints` (`internal/pane/env.go#secretEnvHints`): TOKEN, SECRET, PASSWD,
PASSWORD, CREDENTIAL, API_KEY, PRIVATE_KEY, AUTH, COOKIE.

Everything else is dropped. A pane never inherits `OP_SERVICE_ACCOUNT_TOKEN`,
the 1Password token the server reads from its own environment to run `op`, nor
any registered value. A plugin pane is the exception. [PLUGINS.md](../../PLUGINS.md)
declares a plugin, a program the server starts, and its env pairs are appended
after the allowlist (`internal/pane/pane.go#paneCmd`). Only a `DM_` name is
refused (`internal/keymap/config_parse.go#checkPluginEnv`).

A pane never inherits `DM_PANE` or `DM_SESSION` either. `Env` drops both and
appends the pane's own ids last (`internal/pane/env.go#Env`), so the shell reads
the ids it was spawned with. The environment is built once per pane and never
rebuilt, so turning redaction off does not change it.

## The toggle

`toggle-secrets` is answered on both sockets: the server's own,
`$DM_STATE/sessions/<name>/server.sock` (`internal/paths/paths.go#ServerSockAt`),
and the pane's, `$DM_STATE/sessions/<name>/panes/<id>.sock`.

The server's case (`internal/server/server_rpc.go#server.handleReq`) refuses a
client that has not sent the attach frame, the interactive client's first
request (`internal/server/server_core.go#client`). It resolves the focused pane,
the pane a window marks as focus, where a window is a layout of panes
(`internal/session/state.go#Window`). It forwards the request to the pane and
answers with the state the pane landed on.

The pane flips the stage's `off` switch (`scrubber.toggle`,
`internal/pane/scrub.go#scrubber.toggle`). Off is a view of the pane: bytes pass
through untouched and the list stays. A push during the reveal updates the list
without turning the stage back on. On is the repair: the stage redacts from the
next chunk, and `scrubPaintedLocked`
(`internal/pane/pane.go#paneProc.scrubPaintedLocked`) rewrites what the reveal
published. It re-redacts the scrollback buffer and flushes the pending cluster,
the last grapheme the terminal has not placed yet. It redacts the grid and
broadcasts one `scrub` frame with a fresh screen snapshot, so every client
restores a redacted screen.

The shell's own history file holds whatever `dm secret type` typed, and no stage
rewrites it.

## Adding a tool

1. `internal/session/secret.go`: the constant beside `ToolOp`, and a `Validate`
   case for its shape.
2. `internal/server/server_secrets.go`: a case in `secretTool` with the binary,
   the argument template and its `Inherit` list. That list names the variables
   copied from the server's environment (`internal/secrets/secrets.go#Tool`). The
   value never goes in `Args`; `toolInherit` is the shared base set.
3. `internal/session/secret.go`: extend `secretEnvKeys` if the tool needs a
   configuration name. That list is a positive allowlist, so an entry is the
   permission.
4. `internal/cli/cli.go`: the tool check in `secretRegisterVerb`;
   `internal/clidoc/clidoc.go` holds the help.

## See also

- [The protocol](03-protocol.md), for the `set-secrets` push frame.
- [The server](04-server.md), for the action switch and the mutation path.
- [The pane](05-pane.md), for the PTY loop the redaction stage sits in.
