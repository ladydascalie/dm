# Adversarial review of dm at a783ac4

Commit: a783ac4 "Put the question float's toggle in the help table". Date: 2026-11-25.

Method: eight independent read-only hostile reviews, one per area: wire, concurrency, input, render, security, ask, persistence, gates.
Each used the same commit, never touched the live session, and ran every probe against a throwaway `DM_STATE` under `/tmp`.
Where a claim could be proved it was: race-detector probes, a scratch server, a re-implemented framing client, replayed screens, curl against loopback, tree copies for gate probes.
This report merges the eight, adds no claim, keeps each severity, confidence and evidence, and names the finding review.
Every P1 has probe or test evidence, so none was demoted.

## Verdict

`go build ./...` and `go vet ./...` are clean; `go test ./...` is red in three packages, inherited, not new.
Worst is R-01: any frame above 1 MiB is dropped in silence, so one large ask blanks every client.
R-03 is next: the keystroke path reads the pane map outside the lock, and Go aborts the whole server.
The red baseline hides new breaks: a help caveat was dropped and its frozen hash rewritten to agree.
Unsafe to ship: R-01, R-02 and R-03 kill or blank a live session; R-04, R-05 and R-08 cross a security boundary.

## Findings

### R-01 Frames above the 1 MiB outbound bound are dropped in silence

**Severity** P1, **confidence** high, **area** server transport, **evidence** `server_core.go:181`, `:255-268`; `server_rpc.go:210-223`; `server_view.go:1003-1009`; `ask.go:148-152`.
**Trigger** any snapshot or replay over 1,048,512 bytes; one ask with 2 questions x 64 options x 16000-byte previews is 2.05 MB and passes every cap.
**Impact** drop with a false OK: every snapshot stops, so the desk, the phone page and `dm list` go dark, and the stale flag re-dials every pane per resync with no backoff; the same bound kills scrollback.
**Suggested fix** chunk or budget the snapshot and ring separately, return an error on a drop, arm the resync hook once per overflow. **Verified** a 400 KB ring arrives, a 2.4 MB ring returns only `resp`, a 2 MB ask gave 199 screen frames in 3 s with no snapshot until it closed. **Found by** review-wire, review-ask.

### R-02 The ask store has no cap and never prunes a dead pane's ask

**Severity** P1, **confidence** high, **area** ask store, **evidence** `ask.go:146-152`; `server_ask.go:84`, `:226`; `server_mutate.go:1104`; `state.go:350`.
**Trigger** an agent loop that opens asks, or one bridge killed between open and close.
**Impact** unanswered asks live for the session, in `state.json` and every snapshot, and every save rewrites them; at 324 bytes per ask about 3,200 opens reach the R-01 wall; a dead pane's ask is a ghost that still holds its tool call id and is still answerable from the phone.
**Suggested fix** cap the asks and their total bytes, refuse an open past the cap, prune a pane's asks on close. **Verified** 61 opens accepted with no refusal, and after closing w1p1 its ask stayed in the snapshot and `state.json` and answered 200. **Found by** review-ask (P1), review-persistence (growth half P2).

### R-03 Request-path reads of the pane and state maps run outside `s.mu`

**Severity** P1, **confidence** high, **area** server concurrency, **evidence** `server_mutate.go:247`, `:293`, `:134-137`; `server_rpc.go:564`, `:587`, `:287`.
**Trigger** sync enabled plus a keystroke during any adopt, redial or close, or `dm pane <id>` during the same; adopt runs on every split and every pane reconnect.
**Impact** Go aborts with "concurrent map read and map write", so every client dies with the server; a torn `Cols`/`Rows` read also picks a wrong replay geometry.
**Suggested fix** build the destination slice and copy every field inside one `s.mu` section, send off the lock, return values not pointers from `findPane`. **Verified** race-detector probes fired `WARNING: DATA RACE ... mapaccess2_faststr` against a locked writer for `sendKeys` and for the `pane` action. **Found by** review-concurrency, review-wire.

### R-04 The rail paints pane-controlled text into the operator's terminal unescaped

**Severity** P1, **confidence** high, **area** client render, **evidence** `rail.go:805`, `:838`, `:643`; `seg.go:111`; `osc.go:376-379`; `pane.go:1331`.
**Trigger** a pane program sets its OSC 7501 status message or `app` to a string carrying ESC; a remote host over ssh can do it with ordinary output.
**Impact** the bytes reach the outer terminal (a probe painted `a\nEVIL\x1b[2Jm`), so a program can clear the screen, move the cursor, write the clipboard with OSC 52, leave the alternate screen or set the outer title.
**Suggested fix** hold every pane-controlled string to the `box.CheckText` rule where it is stored, or strip control bytes in `segment.Write`. **Verified** `TestProbeRailControlBytes` reported newline and ESC intact for message, app and name. **Found by** review-input.

### R-05 A forged paste end marker ends the paste and the rest runs as dm keys

**Severity** P1, **confidence** high, **area** client input, **evidence** `client_input.go:48`, `:552-555`.
**Trigger** paste `hello`, `ESC [ 2 0 1 ~`, `\x02x`, `ESC [ 2 0 1 ~`; clipboard text from a page, a log or another terminal can carry that.
**Impact** the remainder is fed through the key path, prefix search included; the probe armed the prefix and sent a `close-pane` request, so a clipboard can close a pane, splice a window or type into a shell.
**Suggested fix** treat a second marker pair inside the held buffer as text, or drop the paste and toast when the body carries an escape-led marker. **Verified** `TestProbePasteInjection` emitted the forged block as keys, then the close-pane request, then a stray marker. **Found by** review-input.

### R-06 `dm reset` and `dm purge` unlink a live server's lock, so two servers own one session

**Severity** P1, **confidence** high, **area** session lifecycle, **evidence** `transport.go:268`, `:285`; `server_core.go:579-586`; `server_view.go:553`.
**Trigger** `dm reset --force` or `dm purge` while a server starts, its pid file is missing, or its socket was removed; `stopServer` returns without signalling and `RemoveAll` ignores the flock.
**Impact** the directory and `state.json` are deleted under the live server, its flock sits on a deleted inode, and the next `dm new` starts a second server; the two race saves with last-rename-wins.
**Suggested fix** take `server.lock` with `LOCK_EX|LOCK_NB` in both verbs, refuse when held, treat a held lock as live. **Verified** `SIGSTOP`, remove pid and socket, `dm reset`: directory gone, server alive, then two `__server -s x` and an fd on a `(deleted)` lock. **Found by** review-persistence.

### R-07 A paste above one wire frame is dropped in silence after it is consumed

**Severity** P1, **confidence** high, **area** client input, **evidence** `client_input.go:261`, `:553-555`; `wire.go:319`, `:327-328`.
**Trigger** paste about 13 MiB; the keys field is base64 in JSON, so about 12 MiB already overflows `MaxFrame`.
**Impact** `Send` returns an error that is discarded and the paste is already gone from the input buffer, so nothing retries it and no toast appears.
**Suggested fix** chunk the paste into frames under the cap with the markers on the first and last chunk, or refuse it with a toast that names the limit. **Verified** a 13 MiB paste left `inbuf=0`, forwarded nothing and raised no toast; the frame check reported 16777241 bytes against 16777216. **Found by** review-input.

### R-08 Web write endpoints accept a same-site cross-origin POST that carries the cookie

**Severity** P1, **confidence** medium, **area** web security, **evidence** `webui.go:238`, `:265`, `:287`; `action.go:221`; `pane.go:33`; `keys.go:31`.
**Trigger** a page on any host the browser counts as same-site with dm, such as another tailnet MagicDNS host or another port, POSTs to `/api/msg` with `mode:no-cors` and `Content-Type: text/plain`; SameSite=Strict attaches the cookie, no preflight runs, and no handler reads Content-Type.
**Impact** arbitrary text plus Enter into a pane, so arbitrary commands as the operator; `/api/action` adds split, close, rename and ask answers, `/api/keys` sends Esc and Ctrl+C.
**Suggested fix** check Origin or Sec-Fetch-Site against the request Host on every POST, refuse a body that is not `application/json`. **Verified** a POST with `Origin: http://evil.example` and a text body returned `{"ok":true}`, the same with `Sec-Fetch-Site: cross-site` returned 200, a form body returned 400. **Found by** review-security.

### R-09 A `null` workspace or window panics the loader and the server

**Severity** P1, **confidence** high, **area** persistence, **evidence** `state.go:489`, `:456`, `:640`, `:643`.
**Trigger** a state file with `"workspaces":[null]` or `"windows":[null]`; JSON produces the nil element and the loader never checks it.
**Impact** `dm ls` and `dm __server` die with a nil pointer stack trace instead of a diagnostic, and the session cannot be listed or started.
**Suggested fix** reject or skip nil workspaces and windows at the top of `LoadStateAt` and return an error naming the index. **Verified** all three shapes panicked through `dm ls`, at `GroupWorkspaces`, `LoadStateAt` and `MigrateIDs`. **Found by** review-persistence.

### R-10 The help overlay lost its toggle-secrets caveat, and the gate was rewritten to bless it

**Severity** P1, **confidence** high, **area** help and gates, **evidence** `config_model.go:605`, `:622`; `help_view_test.go:193-196`, `:500`; `config_test.go:2585`; `secrettoggle.go:7`.
**Trigger** press the prefix then `?`; `helpNotes` is empty while its doc comment says it holds the caveat, and `clidoc.ToggleSecretsNote` still ships it to the man page, the HTML page and llms.txt.
**Impact** the surface where `ctrl+g` is learned no longer says the toggle is not a security control; three tests are red at HEAD, and the frozen hash moved in the same commit so the gate pins the caveat-less box.
**Suggested fix** decide the note one way, restore it or delete the three tests, then regenerate the six goldens and both frames in one `-update` run. **Verified** `go test ./...` fails in `internal/keymap` and `internal/client`, the same tests pass at `735cfe8~1`, and a printed committed frame shows the caveat row that the live box lacks. **Found by** review-render, review-gates, review-ask.

### R-11 Every state save marshals and fsyncs under the server mutex

**Severity** P2, **confidence** high, **area** server concurrency, **evidence** `server_view.go:768-772`, `:871`; `server_mutate.go:1185`; `state.go:350`.
**Trigger** a window drag (one frame per pointer step), a pane `cd` sampled every 500 ms, a rail drag, a box post, an ask save. **Impact** one full marshal plus one disk barrier holds the global lock, so no pane byte flows and no snapshot is built; measured 1.17 ms per save on btrfs and 0.45 ms on tmpfs.
**Suggested fix** marshal under the lock and write, sync and rename off it, or flush periodically behind a dirty flag. **Verified** `TestProbeSaveCost` reported 1.174 ms mean over 20 saves. **Found by** review-concurrency.

### R-12 Every push marshals the whole snapshot once per client under the server mutex

**Severity** P2, **confidence** high, **area** server concurrency, **evidence** `server_core.go:255`, `:411-424`; `server_view.go:590`.
**Trigger** any ordinary push (status, mode, cwd, title, ask change) with several clients and a snapshot of a few hundred KB. **Impact** the global lock is held for O(clients x snapshot bytes) of JSON encoding, so pane output, exit, resize and save queue behind it; measured 22 ms of extra work per push at 1.03 MB with three clients.
**Suggested fix** encode the snapshot once per broadcast and carry its byte cost on the frame. **Verified** 100 `set-pane-name` round trips took 27.2 ms each small and 49.8 ms at 1.03 MB. **Found by** review-wire, review-concurrency.

### R-13 The client connection has no read or write deadline

**Severity** P2, **confidence** high, **area** server transport, **evidence** `server_view.go:808`; `wire.go:346-352`; `server_core.go:317`.
**Trigger** a peer sends a 4-byte header and stops, or attaches and stops reading. **Impact** the whole body is allocated before one payload byte arrives, so one goroutine and 16 MiB are held until the peer leaves (40 connections reserved 672 MiB of `VmSize`); a hung client also pins its writer, queue and `s.clients` entry; same-user only.
**Suggested fix** a per-frame read deadline for the body and a write deadline that shuts the client down and unregisters it. **Verified** 40 stalled connections moved `VmSize` from 2.56 GB to 3.25 GB and it stayed there. **Found by** review-wire.

### R-14 Pane daemons are never reaped, so every close leaves a zombie

**Severity** P2, **confidence** high, **area** server lifecycle, **evidence** `server_core.go:903-907`.
**Trigger** any pane daemon that exits while the server lives: every close-pane, kill and shell exit. **Impact** each dead pane leaves a zombie child for the server's lifetime and its stale pid file still matches `isDMProcess`; six split and close cycles produced six zombies.
**Suggested fix** keep the `*exec.Cmd` and `Wait` for it, or run one reaper goroutine per spawn. **Verified** `awk '{print $3}' /proc/$p/stat` reported `zombie=6 alive=1`, all parented by the server. **Found by** review-concurrency.

### R-15 Nothing deletes a closed pane's pid and log, stale tmp saves, or uploads

**Severity** P2, **confidence** high, **area** persistence, **evidence** `server_mutate.go:1163-1167`; `pane.go:1761-1767`; `server_view.go:377`; `state.go:362`; `upload.go:64`.
**Trigger** every close-pane, a crash during a save, a long-lived session, any upload. **Impact** two files per closed pane stay forever and `reapPanes` later walks them; `state-*.tmp`, `server.log`, pane logs and uploads grow until the whole session is purged, the only cleaner.
**Suggested fix** remove the pid file with the socket in `closePane`, delete old tmp files on start, cap or rotate logs, expire uploads, prune orphan `Panes` keys. **Verified** six cycles left seven pid files with one live pane, and a planted `state-orphan.tmp` survived a full start and SIGTERM. **Found by** review-concurrency, review-persistence.

### R-16 The loser of a cold-start race fails instead of attaching

**Severity** P2, **confidence** high, **area** transport, **evidence** `transport.go:117-127`; `server_core.go:589`.
**Trigger** two clients start one session at once, for example `dm web` and `dm attach`, or a script racing the TUI. **Impact** the loser exits with `session "main" already has a server` and never retries, so the operator sees a failure on a session that is coming up.
**Suggested fix** treat that child error as a reason to keep dialling until the retry budget runs out. **Verified** 6 of 6 rounds failed with two concurrent clients on a fresh state. **Found by** review-concurrency.

### R-17 An unterminated OSC or DCS freezes a pane's display for good

**Severity** P2, **confidence** high, **area** terminal parser, **evidence** `parser.go:307-320`, `:316-318`.
**Trigger** a pane program writes `ESC ] 0 ;` and never terminates it, then keeps printing. **Impact** every later byte is eaten as the body, so nothing paints again: no prompt, no output, no recovery; DCS, SOS, PM and APC behave the same.
**Suggested fix** when the body passes `maxString`, drop the sequence and return to ground, or discard to the next ST and print. **Verified** after an unterminated OSC the screen stayed `|||` and the parser stayed in state 3; only a BEL restored it. **Found by** review-input.

### R-18 Keystrokes dropped by a full pty write queue are never reported

**Severity** P2, **confidence** medium, **area** pane, **evidence** `pane.go:149`, `:1204-1208`, `:1213`.
**Trigger** a pane program stops reading stdin (a pager, a stopped job) while the operator holds a key or pastes, until the 256-slot queue fills. **Impact** input silently never arrives and no count reaches a status, a toast or the log; one large paste can block the single drain goroutine.
**Suggested fix** surface the drop count, and chunk a paste so one write cannot block the queue. **Verified** reasoned from code. **Found by** review-input.

### R-19 A held paste or a partial colour reply has no size cap and freezes input

**Severity** P2, **confidence** high, **area** client input, **evidence** `client_input.go:71-79`, `:302-313`, `:545-551`.
**Trigger** a paste source sends `ESC [ 2 0 0 ~` and streams past 500 ms, or a colour reply trickles in unfinished; the deadline re-arms on every read. **Impact** no key reaches any pane while the head grows, the buffer holds the whole stream, and the block flushes as ordinary keys with markers when it stops.
**Suggested fix** cap the held buffer at what the frame cap can carry and treat an over-cap paste as an error. **Verified** after 610 ms the hold had not expired, `inbuf=15366`, nothing forwarded, a typed key was swallowed. **Found by** review-input.

### R-20 A tab inside ask text is silently eaten

**Severity** P2, **confidence** high, **area** render, **evidence** `ask.go:472`; `panel.go:173-186`.
**Trigger** an agent sends a question, description or preview containing a tab; code previews are the obvious source. **Impact** `a<TAB>b` paints as `ab` and an indented preview loses its columns; `cells.Cells("\t")` is 0, so the wrap math does not compensate.
**Suggested fix** expand a tab to the next stop in the chromeless painters, or reject it in `checkLong`. **Verified** a painted ask with `"a\tb"` replayed as `ab` with no 0x09 in the frame. **Found by** review-render.

### R-21 On a short screen the ask float paints everything but the question

**Severity** P2, **confidence** high, **area** render, **evidence** `client_ask.go:1479-1491`, `:1456-1458`, `:820`.
**Trigger** a terminal of 12 rows or fewer; 14 or fewer also loses every option row. **Impact** the reader sees the count, a rule and the key rows with no question and no options while j/k and enter still act, so the answer is given blind; the doc comment claims the keys ride the border where no clamp can drop them.
**Suggested fix** give the question block its own floor, or elide the key rows behind a count, and fix both comments. **Verified** a row sweep found `rows=8 question=false`, `rows=10 question=false`, `rows=12 question=true list=false`. **Found by** review-render.

### R-22 Save never fsyncs the session directory

**Severity** P2, **confidence** high, **area** persistence, **evidence** `state.go:350-390`.
**Trigger** power loss or kernel panic after a save returns. **Impact** a crash after the rename before the directory flush can roll the rename back, losing the newest layout, box entry, ask or cwd; a lost first save leaves the next start to seed fresh state.
**Suggested fix** open the session directory after the rename, sync it, treat a failed directory sync as a save failure. **Verified** reasoned; the file sync and unique temp name are present, no directory sync exists. **Found by** review-persistence.

### R-23 A failed box save is still delivered and its seq is reused after a restart

**Severity** P2, **confidence** high, **area** persistence, **evidence** `server_box.go:50-57`.
**Trigger** a normal post into a read-only or full session directory. **Impact** `dm msg post` exits 1 yet the entry lists and waiters wake on it; if the process dies before a good save the entry is gone and `BoxSeq` rolls back, so the next post reuses the seq and a `--after` subscriber misses it.
**Suggested fix** save first and publish only after success, or persist `boxSeq` ahead of the entry. **Verified** a post into a 0500 directory failed with EACCES and the entry still listed as seq 2 while `state.json` held `boxSeq=1`. **Found by** review-persistence.

### R-24 Most save paths swallow the error, so a full disk loses state silently

**Severity** P2, **confidence** high, **area** persistence, **evidence** `server_view.go:71`, `:215`, `:421`, `:771`; `server_mutate.go:40`, `:48`, `:1185`; `server_shelf.go:173`.
**Trigger** ENOSPC, EACCES or a read-only state tree on a split, fold, cwd, rail, status or size fit. **Impact** the session accepts the change, reports success and discards it on restart with no log line; a startup save failure returns after the panes have spawned, leaving them with no server.
**Suggested fix** log a save failure once and surface it in the snapshot, and move the startup save ahead of the spawn or roll the panes back. **Verified** reasoned, with the same EACCES proven reachable on the box path. **Found by** review-persistence.

### R-25 A corrupt `state.json` is reported as a dead zero-pane session

**Severity** P2, **confidence** high, **area** persistence, **evidence** `cli.go:2403`; `server_layout.go:27-40`.
**Trigger** a zero-byte or truncated state file, or a torn write from a foreign writer. **Impact** `dm ls` prints a dead row with `0 0 0` and exit 0, indistinguishable from an empty session, and no verb says "corrupt" while the only working remedy is destructive.
**Suggested fix** keep the load error and print it in the `dm ls` row, and add a recover verb that renames the bad file aside. **Verified** a zero-byte file printed the dead row with exit 0, the server printed the JSON error and left the file at 0 bytes. **Found by** review-persistence.

### R-26 The loader trusts ids and counters, so duplicate ids load silently

**Severity** P2, **confidence** high, **area** persistence, **evidence** `state.go:618-630`, `:410-428`.
**Trigger** a state file whose counters lag its ids, or one that already carries a duplicate id. **Impact** the next create reuses an id; targeting, `findPane`, fold and close act on the first match, and a duplicate pane id splits one pane's exit across two leaves and leaves a ghost leaf that keeps the session alive.
**Suggested fix** walk the tree after load, reject or repair duplicate ids, and lift every counter to the highest id in use. **Verified** workspaces `s1` and `s2` with no counters produced a second `s1` and `w1` after one `new-workspace`; two `w1p1` leaves both survived. **Found by** review-persistence.

### R-27 Terminal geometry has no upper bound, so a persisted size can OOM every start

**Severity** P2, **confidence** medium, **area** persistence, **evidence** `state.go:396-475`; `server_core.go:625-630`; `grid.go:146-166`.
**Trigger** a hand-edited state with a 19-digit `cols`, or an attach claim of enormous size. **Impact** a valid file loads and becomes the layout rect, so every pane is resized to it on every start and the allocation can be an OOM kill before a client can correct it.
**Suggested fix** clamp persisted and attached `Cols`/`Rows` to a sane maximum at load and attach, and refuse a state outside it. **Verified** the loader accepted `cols=9223372036854775807` and `dm ls` read it; the allocation path is reasoned. **Found by** review-persistence.

### R-28 `dm kill` unlinks the socket before the server stops

**Severity** P2, **confidence** high, **area** lifecycle, **evidence** `transport.go:257-259`; `docs/internals/02-lifecycle.md:159-160`.
**Trigger** `dm kill` on a stopped or wedged server, then `dm new`, `dm attach` or any dial. **Impact** the socket is gone so nothing can attach, and the old server still holds the flock, so a replacement exits with `already has a server`; the doc promises a restart.
**Suggested fix** wait for the pid to exit, as `stopServer` does, and remove the socket only after it is gone. **Verified** with the server in `SIGSTOP`, `dm kill` exited 0 and left no socket, and `dm new` failed with `already has a server`. **Found by** review-persistence.

### R-29 The loader saves without the flock, so `dm ls` can overwrite a live server's state

**Severity** P2, **confidence** high, **area** persistence, **evidence** `state.go:469-471`; `cli.go:2403`; `cli_layout.go:163`; `docs/internals/02-lifecycle.md:68`.
**Trigger** any load that sets `changed`: id migration, a dropped bad ask or secret, a missing `railSet`, a shelf prune, a numeric window rename, while a server starts or saves. **Impact** a read-only verb rewrites `state.json`, the loader's older snapshot can win, and box entries, asks, cwd and layout are lost; the doc's only-writer invariant is false.
**Suggested fix** never save from `LoadStateAt`; return the repaired state and let the server write it under its lock, or take the shared lock for loads. **Verified** `dm ls` on an old-schema state rewrote the file, renumbered ids, added `railSet` and moved the mtime from 2020 to now, with no server involved. **Found by** review-persistence.

### R-30 `secretPush` is held across serial pane writes and a sibling fan-out

**Severity** P2, **confidence** medium, **area** server secrets, **evidence** `server_secrets.go:163-172`, `:208-240`, `:337-364`; `server_core.go:106`, `:946`; `pane.go:815`.
**Trigger** any secret action while one pane is wedged (5 s per conn) or a sibling session is slow (2 s per sibling). **Impact** the secret reply waits and an adopt push queues behind it; enough wedged panes push the adopt past 30 s, so the pane starts with an empty redaction list and its first output is unscrubbed.
**Suggested fix** send to panes concurrently under one total deadline, and take the sibling fan-out out of `secretPush`. **Verified** reasoned; the mutual wait is bounded, so it stalls rather than deadlocks. **Found by** review-concurrency.

### R-31 A tool call id is a session-wide namespace reserved forever

**Severity** P2, **confidence** medium, **area** ask store, **evidence** `server_ask.go:78-81`, `:226`.
**Trigger** two agents in one session send the same tool call id, or a bridge dies before close and the model retries the identical call. **Impact** the second open is refused with `E_EXISTS` so the agent cannot ask at all, and the reservation outlives its owner because an unanswered ask is never reaped.
**Suggested fix** scope the duplicate check to the pane, or release the id when the asking connection drops. **Verified** two opens with `--tool-call q2`; the second returned `E_EXISTS: ask "a3" already covers tool call "q2"`. **Found by** review-ask.

### R-32 A snapshot during an in-flight answer drops the reader's draft

**Severity** P2, **confidence** medium, **area** client ask, **evidence** `client_ask.go:183`, `:618`, `:643-651`.
**Trigger** press enter, and any other state change pushes a snapshot before the refusal reply lands. **Impact** the float closes with every pick, note and typed answer, the refusal code is never shown, and the next tick reopens an empty form.
**Suggested fix** close only when the ask is gone or answered, and let the reply decide a refusal. **Verified** reasoned; the window is milliseconds and was not raced. **Found by** review-ask.

### R-33 An ask on an unfocused pane has no cue anywhere

**Severity** P2, **confidence** high, **area** client ask, **evidence** `client_ask.go:27`, `:262`, `:266`; `wire.go`.
**Trigger** an agent asks on w1p2 while focus is w1p1 in the same window. **Impact** the pane looks blocked with no reason; there is no rail badge and no key that names the ask, so it waits until the pane happens to be focused, though two comments promise the badge.
**Suggested fix** draw the rail mark and the strip chip from the whole snapshot ask list, and correct the comments. **Verified** reasoned, plus `grep -rn Ask internal/client` finding only `client_ask.go` and the bar strip. **Found by** review-ask.

### R-34 Redaction stops at the pane's output: the box, the transcript and the OSC title carry the value raw

**Severity** P2, **confidence** high, **area** secrets, **evidence** `server_box.go:35`; `session.go:55`; `pane.go:1311`, `:1389`; `wire.go:175`; `docs/internals/09-secrets.md:21`.
**Trigger** with the value live, post it into the box through `/api/action`, read a transcript that contained it, or let a program set its window title to it. **Impact** the value lands in `state.json`, in `/api/fleet` and in the desk rail unredacted, and stays after the registry is dropped.
**Suggested fix** scrub `Msg` and `Body` in `boxStoreLocked`, scrub the ask text, carry the value list into the transcript path, run `redactText` in `sampleTitle`. **Verified** a fake value posted through `/api/action` appeared once in `state.json` and in `/api/fleet`; the other two are reasoned. **Found by** review-security.

### R-35 Any socket client can register a secret with its own upstream

**Severity** P2, **confidence** medium, **area** secrets, **evidence** `server_rpc.go:719`, `:731`, `:270`.
**Trigger** from a pane, register a secret with `--upstream https://attacker.example` and use it. **Impact** the daemon fills the operator's 1Password item into a header aimed at the attacker's host; the doc accepts the gap, but no gate stops it.
**Suggested fix** require the attach frame and a client kind an agent cannot claim for the verbs that mint a ride, and freeze upstream and header after register. **Verified** register and hydrate succeeded from a plain client with no attach frame; the exfiltration step is reasoned. **Found by** review-security.

### R-36 Upload writes through a planted symlink and is not atomic at the content address

**Severity** P2, **confidence** high, **area** web upload, **evidence** `upload.go:56`, `:61-65`; `secrets.go:119`.
**Trigger** a program running as the operator plants `uploads/<hash>.png` as a symlink, then the phone uploads bytes with that hash; or two uploads of one picture overlap. **Impact** `os.WriteFile` follows the link and truncates the target keeping its mode, and a reader can see a short or empty file at the content address.
**Suggested fix** write a temp file in the uploads dir, sync it, rename over the hash path, as `secrets.Hydrate` does. **Verified** a planted link turned `/tmp/victim.txt` into image bytes with mode unchanged; the atomicity half is reasoned. **Found by** review-security, review-persistence.

### R-37 Upload accepts a 69-byte 60000x60000 PNG

**Severity** P2, **confidence** high, **area** web upload, **evidence** `upload.go:47`.
**Trigger** `POST /api/upload` with a 69-byte PNG whose IHDR claims 60000x60000; `DetectContentType` reads magic bytes only. **Impact** dm stores 69 bytes, then the agent's read tool decodes 14.4 GB of pixels, so the process that dies is the agent's.
**Suggested fix** decode the image config for the four accepted types and refuse a pixel count above a fixed budget. **Verified** the upload returned 200 and wrote 69 bytes; `file` reported 60000 x 60000 RGBA. **Found by** review-security.

### R-38 The session reader has no size or regular-file gate

**Severity** P2, **confidence** medium, **area** web session, **evidence** `session.go:55`, `:270`.
**Trigger** a pane points its OSC 7502 session file at a FIFO or a 4 GB JSONL, then a token holder loads `/api/session?pane=w1p1`. **Impact** `io.ReadAll` on an open with no stat and no cap: a FIFO blocks the handler with no context, a huge file allocates until the web process dies; the tree confinement holds.
**Suggested fix** stat after open, require a regular file, cap the read. **Verified** reasoned; the confinement was read, not bypassed. **Found by** review-security.

### R-39 The snapshot contract comment contradicts `wireAsks`

**Severity** P2, **confidence** high, **area** protocol docs, **evidence** `wire.go:282-285`; `server_ask.go:261`; `client_ask.go:184`.
**Trigger** any settled ask. **Impact** the doc says the snapshot carries open and answered-but-unclosed asks while `wireAsks` drops settled ones, so a maintainer takes the wrong branch on success versus refusal, which is exactly the R-32 failure.
**Suggested fix** state in `wire.go` that the snapshot lists open asks only, and rewrite the client comment to match. **Verified** reasoned and confirmed against a live snapshot: an answered ask never appeared while `dm ask list` still listed it. **Found by** review-ask.

### R-40 148 of 289 docs/internals citations point at the wrong line

**Severity** P2, **confidence** high, **area** docs, **evidence** `docs/internals/*.md` against `internal/**`.
**Trigger** a reader follows a citation to learn how a thing works. **Impact** no citation dangles, but 51 percent land on unrelated code: `State.Save` cited at `state.go:216` (actual 350), `endSession` at `server_view.go:462` (524), `runPurge` at `cli.go:1802` (2160); median drift +8, max +855.
**Suggested fix** anchor by symbol name instead of line number, or check that the cited line still holds the symbol. **Verified** a python scan over every page plus 14 hand-confirmed citations. **Found by** review-gates.

### R-41 Mutation results are stale, incomplete and unreproducible at HEAD

**Severity** P2, **confidence** high, **area** gates, **evidence** `gremlins-results/*`; `Makefile:26`.
**Trigger** `make gremlins`, or anyone citing "all packages clean". **Impact** the results are 12 days older than HEAD, 16 of 44 packages have none including redact, secrets and proxy, two logs disagree with their own json, and gremlins cannot start at HEAD because `internal/keymap` is red.
**Suggested fix** re-run on a green HEAD and record the git sha beside the results, or drop the claim. **Verified** file dates, `go list ./internal/...` against the jsons, and a gremlins run that failed with `failed to gather coverage`. **Found by** review-gates.

### R-42 The README key table omits three bound actions

**Severity** P2, **confidence** high, **area** docs, **evidence** `README.md:116-151`; `config_model.go` defaults.
**Trigger** a reader learns the keymap from README, the recommended front door. **Impact** `y`/`Y` copy output and screen, `P` set-project and `ctrl+g` toggle-secrets have no row while the man page lists them, and `ctrl+g` is the one an operator most needs to find.
**Suggested fix** add the three rows. **Verified** a dump of `LoadDefault()` against the README table and the dm.1 KEYS section. **Found by** review-gates.

### R-43 `WriteMsg` ignores the byte count and splits a frame into two writes

**Severity** P3, **confidence** high, **area** wire, **evidence** `wire.go:330-336`.
**Trigger** any `io.Writer` that short-writes without an error. **Impact** silent truncation, and a header can interleave with another writer's body; safe today only because every caller is a `net.Conn` with one writer. **Suggested fix** full-write via `io.Copy` plus a documented one-writer rule. **Verified** reasoned. **Found by** review-wire.

### R-44 A bare attach frame steals the sizing role and drops every claim

**Severity** P3, **confidence** high, **area** wire, **evidence** `server_view.go:813-819`; `server_sizer.go:28-35`.
**Trigger** `{"type":"attach"}` with no size, from a helper or a stale client. **Impact** the real TUI gets `sizer Sizing=false` and stops fitting its terminals, and panes fall back to layout rects until the stray client leaves. **Suggested fix** take the sizer role only when the attach carries a usable size. **Verified** a bare attach to a scratch server returned `type:sizer` on an untouched session. **Found by** review-wire.

### R-45 Nothing caps clients, parked waiters or watches

**Severity** P3, **confidence** high, **area** wire, **evidence** `server_core.go:711-726`; `server_ask.go:21`; `server_box.go:148`.
**Trigger** one same-user process opens many connections and parks a wait on each. **Impact** unbounded goroutines, timers and `s.clients` entries; a robustness note, not a boundary. **Suggested fix** cap concurrent clients and parked waiters. **Verified** reasoned. **Found by** review-wire.

### R-46 The secret-sync trust verdict is supplied by the caller it constrains

**Severity** P3, **confidence** high, **area** wire, **evidence** `server_sync.go:251-256`; `cli.go:624-628`.
**Trigger** `dm exec '{"action":"secret-sync","path":"<repo>/.dm-secrets.kdl","trust":true}'`. **Impact** the untrusted-mapping rule binds only callers that leave the flag alone, and reach is limited to a wider redaction set since a donated value cannot be hydrated. **Suggested fix** decide trust from the path alone, or document the flag as advisory. **Verified** reasoned. **Found by** review-wire.

### R-47 The proxy's loopback test is given host:port and matches by prefix

**Severity** P3, **confidence** high, **area** proxy, **evidence** `proxy.go:192-199`, `:248-251`.
**Trigger** a listen address other than `127.0.0.1:0`, or a client over IPv6 loopback. **Impact** `RemoteAddr` is `host:port`, so the exact matches never fire and only the `127.` prefix keeps IPv4 working; a `localhost` listener would 403 every `[::1]` request. Latent today. **Suggested fix** split the port and use `net.IP.IsLoopback`. **Verified** reasoned. **Found by** review-wire.

### R-48 Attach and every resync read pane screens serially, 2 s each

**Severity** P3, **confidence** high, **area** server, **evidence** `server_view.go:973-990`, `:1075-1100`, `:889`, `:1003`.
**Trigger** attaching to a session with an unresponsive pane, or a slow client overflowing its queue. **Impact** the new client paints nothing for up to 2 s per unresponsive pane, and a client that keeps overflowing re-dials every pane on every resync. **Suggested fix** fan the screen requests out with a WaitGroup under one total deadline, as `settleReflow` does. **Verified** reasoned; the path is synchronous on the serve goroutine. **Found by** review-concurrency.

### R-49 `paneExited` races `ensurePane` on the same pane id

**Severity** P3, **confidence** medium, **area** pane lifecycle, **evidence** `server_view.go:300`, `:315`, `:361-362`, `:416-417`; `server_core.go:855-868`.
**Trigger** a pane exit that starts just before a pop or another `ensurePane` for the same id. **Impact** `paneExited` deletes the spawn mutex while it can be held, so two dials or two daemons run for one pane, and it removes the socket path unconditionally, so it can delete a fresh daemon's socket. **Suggested fix** never delete the spawn mutex and remove the socket only when it is still the exiting daemon's. **Verified** reasoned; neither window was won. **Found by** review-concurrency, review-persistence.

### R-50 Two pane goroutines have no stop path

**Severity** P3, **confidence** high, **area** pane, **evidence** `pane.go:755-762`, `:800-806`, `:1202`, `:870-877`.
**Trigger** a caller that drives `pane.Run` in-process, and a pane whose shell never starts. **Impact** one leaked goroutine and one live signal registration per failed pane; the daemon's process exit hides both. **Suggested fix** select on `p.done` in the signal goroutine and close `p.writes` at shutdown. **Verified** reasoned; no WaitGroup exists in the package. **Found by** review-concurrency.

### R-51 A markless wait can poll the shell twice

**Severity** P3, **confidence** medium, **area** pane, **evidence** `pane.go:1755-1758`, `:1537-1545`.
**Trigger** a `reflow` arriving while a markless `wait` is in flight. **Impact** two 500 ms `/proc` samples and two racing replies per wait, with correctness resting on the `p.waiter != w` check. **Suggested fix** record that the wait already polls and start the poller only if it does not. **Verified** reasoned. **Found by** review-concurrency.

### R-52 Two tests fail on a loaded machine

**Severity** P3, **confidence** high, **area** tests, **evidence** `drain_test.go:144-162`; `sizer_test.go:523`, `:566`.
**Trigger** a busy machine, or the race detector's slowdown. **Impact** `TestStalledPtyRecoversWithoutLosingTheSocket` failed 2 of 15 runs and `TestClaimHeightReachesNonSizer` failed on a stale frame, so a real regression and the flake look the same and "only the help tests are red" is not safe. **Suggested fix** wait for the queue to drain or the frame to arrive instead of sleeping a fixed 50 or 60 ms. **Verified** the failure texts and standalone passes. **Found by** review-concurrency, review-gates.

### R-53 The VT parser mishandles C1 introducers, invalid UTF-8 and late combining marks

**Severity** P3, **confidence** high, **area** terminal parser, **evidence** `parser.go:105-118`; `cell.go:110-112`; `term.go:97`.
**Trigger** a program writes 8-bit CSI `0x9b 0x32 0x43`, an overlong or surrogate sequence, or a combining mark more than 16 ms after its base glyph. **Impact** each bad byte paints one U+FFFD glyph with no cursor move, so an 8-bit-control screen is garbage, and a late mark accents the next character. **Suggested fix** map the C1 introducers to their 7-bit forms and attach a leading combining cluster to the preceding cell. **Verified** probes printed `8-bit CSI: screen="�2CX||"` against `7-bit CSI: screen="  X||"`, and `cell1="́"` after an idle flush. **Found by** review-input.

### R-54 The keymap accepts an empty action name and a prefix of `esc`

**Severity** P3, **confidence** high, **area** keymap, **evidence** `config_parse.go:452-454`, `:866-872`; `client_input.go:627-636`.
**Trigger** `keymap "main" { "" "x" }`, or `prefix "esc"` then the up arrow. **Impact** the first binds nothing with no error where every other node names a line; the second strips `ESC` from `ESC [ A`, so the arrow types `A` and every escape-led binding dies. **Suggested fix** refuse both with a line number. **Verified** probes reported `empty action name ok` with no binding for `x`, and `prefix=esc ... keys=["A"]`. **Found by** review-input.

### R-55 The help table names the ask toggle twice

**Severity** P3, **confidence** high, **area** render, **evidence** `config_model.go:611`; `render_modal.go:283-286`.
**Trigger** the help overlay at any width that shows the footer. **Impact** `A show / hide question` in the Utility column and `A ask` on the bottom border, the redundancy the new row was meant to remove. **Suggested fix** drop one of the two. **Verified** a printed help box at 120x30 shows both. **Found by** review-render.

### R-56 A description under a two-digit option sits one cell left

**Severity** P3, **confidence** high, **area** render, **evidence** `client_ask.go:1001`, `:1055`.
**Trigger** 10 or more options (the store allows 64). **Impact** description and note blocks under options 10 and up hang one column left of the label. **Suggested fix** indent by the cells of the printed number. **Verified** reasoned. **Found by** review-render.

### R-57 The rail band is unclipped, unlike every other band

**Severity** P3, **confidence** high, **area** render, **evidence** `client_view.go:101`; `layout.EffectiveRail`.
**Trigger** a snapshot whose `Rail` is at least `Cols`. **Impact** the rail paints past the right edge and over the panes; unreachable from the current server, so this is hardening. **Suggested fix** clamp the rail width to `win.Cols` where the band is built. **Verified** a probe at 30x20 with Rail=34 emitted 19 cursor moves past the edge. **Found by** review-render.

### R-58 Web token handling has three soft spots

**Severity** P3, **confidence** high, **area** web security, **evidence** `webui.go:149`, `:232`, `:80`; `cli.go:344`.
**Trigger** a same-user program plants the token path as a symlink; another http origin shadows the cookie; or a shared host reads `/proc/<pid>/cmdline`. **Impact** the file opens `O_TRUNC` with no `O_NOFOLLOW` so a link truncates a dotfile or supplies a token, the cookie has no `Secure` or `__Host-` so it crosses plain http, and the token sits in argv and on stdout. **Suggested fix** `O_NOFOLLOW` plus temp-and-rename, `Secure` and `__Host-` on a loopback listener, a token from stdin or `$DM_WEB_TOKEN`. **Verified** the `Set-Cookie` header showed no `Secure` and the printed URL carried the token; the symlink pattern was proven live on the upload path. **Found by** review-security.

### R-59 `internal/redact` is dead code

**Severity** P3, **confidence** high, **area** secrets, **evidence** `redact.go:1`; `arch.go:107`; `scrub.go`.
**Trigger** a future change wires the wrong matcher, or fixes a bug in the unused one. **Impact** two engines with different matching and bounds, and the reviewed one is on no path, so its tests prove nothing about the running system. **Suggested fix** delete the package, or move the pane scrubber into it. **Verified** `rg -n 'dm/internal/redact'` finds only the arch list, and its tests pass. **Found by** review-security.

### R-60 The UTF-8 gate cannot fire through JSON

**Severity** P3, **confidence** high, **area** ask, **evidence** `ask.go:468`.
**Trigger** an open whose raw body carries invalid bytes. **Impact** `encoding/json` replaces them with U+FFFD before `checkText` runs, so the promised gate never runs, invalid bytes are stored and shown, and two different invalid ids can collapse onto one. **Suggested fix** drop the claim, or check the raw bytes before decoding. **Verified** an open was accepted and listed as `"bad \ufffd\ufffd byte"`. **Found by** review-ask.

### R-61 Duplicate ask ids survive a hand-edited state.json

**Severity** P3, **confidence** high, **area** ask, **evidence** `state.go:544`.
**Trigger** duplicate an ask entry with a different tool call id. **Impact** two asks share one id, `AskIndex` answers and closes only the first, and the twin returns after a close. **Suggested fix** drop or renumber duplicates on load. **Verified** two `a9` entries listed, and the twin survived `dm ask close a9`. **Found by** review-ask.

### R-62 A newline in a question corrupts `dm ask list`

**Severity** P3, **confidence** high, **area** ask, **evidence** `cli.go:1987-1995`; `ask.go:461-475`.
**Trigger** an open whose first question contains a newline or tab. **Impact** the table breaks into an unaligned extra row, so a reader or script sees a row that is not an ask. **Suggested fix** fold whitespace to one line for the table, or quote the text. **Verified** `dm ask list | cat -A` printed a second raw line. **Found by** review-ask.

### R-63 The bridge's documented limits are schema-only

**Severity** P3, **confidence** high, **area** ask bridge, **evidence** `skills/ask-bridge/README.md:48`; `ask-bridge.ts:204`, `:590-600`.
**Trigger** a model call with a 200-character header or an empty question. **Impact** the README promises a 16 and 60 character pre-check, but `validateQuestions` checks counts, duplicates and reserved labels only, so the caps land as daemon refusals and the zero-option question cannot be expressed at all. **Suggested fix** enforce the caps and the blank check, and document or allow the freeform shape. **Verified** read of both files. **Found by** review-ask.

### R-64 `ask-await` polls the socket at 10 Hz per waiter

**Severity** P3, **confidence** high, **area** ask server, **evidence** `server_ask.go:21`.
**Trigger** several bridges awaiting at once, each in its own CLI process. **Impact** 10 lock acquisitions per waiter per second on the global lock for up to 25 s. **Suggested fix** keep the hangup check on a slow timer and let the wake channel carry the answer. **Verified** reasoned; load is small at desk scale. **Found by** review-ask.

### R-65 An ask-answer with no target reaches the daemon as an empty id

**Severity** P3, **confidence** high, **area** web action, **evidence** `action.go:355`.
**Trigger** `POST /api/action {"id":"ask-answer","answer":{"cancel":true}}`. **Impact** the daemon replies `not_found: no ask ""`, which reads as a store fault rather than a missing field. **Suggested fix** refuse an empty target on a row whose target kind is set. **Verified** the POST returned 400 with that message. **Found by** review-ask.

### R-66 Load repairs are not written back and unknown state fields are dropped

**Severity** P3, **confidence** high, **area** persistence, **evidence** `state.go:401`, `:406-416`.
**Trigger** invalid or over-cap box entries, a missing `railSet`, or a field written by a newer dm, on a load that makes no other change. **Impact** the same repairs run and log on every load and the file stays large, and a forward-written field disappears on the next rewrite without a warning. **Suggested fix** set `changed` when a repair removes anything, and preserve unknown fields as raw JSON or reject them naming the field. **Verified** 1,200 over-cap entries kept their size and mtime, and `futureField` vanished after a forced migration. **Found by** review-persistence.

### R-67 Two CLI entry paths skip the shared guard

**Severity** P3, **confidence** high, **area** cli, **evidence** `web.go:21`; `webui.go:125`; `cli.go:2199`, `:2217`.
**Trigger** `dm web -s ../../escaped`, or a stale pid file that names a recycled PID. **Impact** the first creates `web.token` and uploads outside the state tree, the second makes `dm purge` skip a dead session forever while blaming a live pane; `paths.ValidSessionName` and `isDMProcess` exist and both paths ignore them. **Suggested fix** validate the session name in `webui.Run`, and use `isDMProcess` in the purge check. **Verified** `dm web -s ../../../../dmrev-escape` tried to mkdir at the root; the purge half was read, not reproduced. **Found by** review-security, review-persistence, review-gates.

### R-68 Existing directories and files keep permissive modes

**Severity** P3, **confidence** high, **area** persistence, **evidence** `state.go:351`; `server_core.go:576`; `pane.go:739`; `upload.go:56`; `webui.go:146`.
**Trigger** a state root or session dir created with umask 022, or a pre-existing 0644 `state.json`. **Impact** `MkdirAll` and `WriteFile` never change an existing mode, so another user can list session and file names and a pre-existing permissive state file stays readable. **Suggested fix** chmod the session and pane directories after `MkdirAll`, and chmod the state file on every save. **Verified** a 0644 `state.json` stayed 0644 after `dm ls`, and 0755 directories stayed 0755 after a full `dm new`. **Found by** review-persistence.

### R-69 The layer gate ignores test imports

**Severity** P3, **confidence** high, **area** gates, **evidence** `arch.go`.
**Trigger** a low-layer `_test.go` imports a higher layer to reuse a helper. **Impact** the graph is built without `-test`, so the gate and the package page miss the import, and nothing states the exclusion. **Suggested fix** say in `docs/internals/11-layers.md` that the gate covers non-test imports only, and extend it or keep the exclusion explicit. **Verified** an upward import in a regular file fails the gate, and the same import in a `_test.go` passes. **Found by** review-gates.

### R-70 No `make test` and no CI, so every gate is opt-in

**Severity** P3, **confidence** high, **area** gates, **evidence** `Makefile`; repo root.
**Trigger** anyone landing a change. **Impact** the red baseline, doc snapshots, goldens, mutation run and esctest gate all depend on someone remembering to run them, which is how a red HEAD survived two commits; `-race` appears in no target and the tree has no fuzz targets. **Suggested fix** add `make test` running `go test ./...`, and name the opt-in gates in it. **Verified** Makefile and tree scan. **Found by** review-gates.

### R-71 The reference pages drift from the code that generates them

**Severity** P3, **confidence** high, **area** docs, **evidence** `docs/dm.1:1`; `man.go:15-16`, `:64`; `tables.go:10`; `README.md:384-406`.
**Trigger** a changed default, a new action, or a page edit without a date bump. **Impact** `manDate` still says 2026-11-19 while the body changed on 2026-11-24; `layout` is a live action in no reference and four more are missing from `manActionNames`; the config "default" column is a hand copy no test compares to `defaultConfigKDL`; README's config table omits `palette-rows`, `web-addr` and `plugin`. **Suggested fix** derive these checks from code, and bump or derive the page date. **Verified** diffs of the server case labels, `ConfigNodes`, the parser switch and the page header. **Found by** review-gates.

## Summary table

| R-nn | Sev | Area | Title |
|---|---|---|---|
| R-01 | P1 | transport | Frames above 1 MiB are dropped in silence; one large ask blanks every client |
| R-02 | P1 | ask store | The ask store has no cap and never prunes a dead pane's ask |
| R-03 | P1 | server | Request-path map reads run outside the mutex and abort the server |
| R-04 | P1 | render | The rail paints pane-controlled text into the outer terminal |
| R-05 | P1 | input | A forged paste end marker runs the remainder as dm keys |
| R-06 | P1 | lifecycle | reset and purge unlink a live lock, so two servers own one session |
| R-07 | P1 | input | A paste above one frame is dropped in silence after consumption |
| R-08 | P1 | web | Write endpoints accept a same-site cross-origin POST |
| R-09 | P1 | persistence | A null workspace or window panics the loader |
| R-10 | P1 | gates | The help caveat was dropped and the gate rewritten to bless it |
| R-11 | P2 | concurrency | Every state save marshals and fsyncs under the mutex |
| R-12 | P2 | concurrency | Every push marshals the snapshot per client under the mutex |
| R-13 | P2 | transport | The client connection has no read or write deadline |
| R-14 | P2 | lifecycle | Pane daemons are never reaped |
| R-15 | P2 | persistence | Nothing deletes pid files, logs, tmp saves or uploads |
| R-16 | P2 | transport | The cold-start loser fails instead of attaching |
| R-17 | P2 | parser | An unterminated OSC or DCS freezes a pane for good |
| R-18 | P2 | pane | Full pty queue drops keystrokes with no report |
| R-19 | P2 | input | A held paste or partial colour reply has no cap and freezes input |
| R-20 | P2 | render | A tab inside ask text is eaten |
| R-21 | P2 | render | On a short screen the ask float paints no question |
| R-22 | P2 | persistence | Save never fsyncs the session directory |
| R-23 | P2 | persistence | A failed box save is delivered and its seq is reused |
| R-24 | P2 | persistence | Most save paths swallow the error |
| R-25 | P2 | persistence | A corrupt state file is reported as a dead empty session |
| R-26 | P2 | persistence | Duplicate workspace, window and pane ids load silently |
| R-27 | P2 | persistence | Terminal geometry has no upper bound |
| R-28 | P2 | lifecycle | dm kill unlinks the socket before the server stops |
| R-29 | P2 | persistence | The loader saves without the flock |
| R-30 | P2 | secrets | secretPush is held across serial sends and a sibling fan-out |
| R-31 | P2 | ask store | A tool call id is reserved session-wide forever |
| R-32 | P2 | ask client | A snapshot mid-answer drops the draft |
| R-33 | P2 | ask client | An ask on an unfocused pane has no cue |
| R-34 | P2 | secrets | The box, transcript and title carry the value raw |
| R-35 | P2 | secrets | Any socket client can register a secret with its own upstream |
| R-36 | P2 | web upload | Upload follows a planted symlink and is not atomic |
| R-37 | P2 | web upload | A decompression bomb passes the sniff |
| R-38 | P2 | web session | The session reader takes any file or FIFO |
| R-39 | P2 | protocol | The snapshot comment contradicts wireAsks |
| R-40 | P2 | docs | Half of the internals citations point at the wrong line |
| R-41 | P2 | gates | Mutation results are stale and incomplete |
| R-42 | P2 | docs | The README key table omits three actions |
| R-43 | P3 | wire | WriteMsg ignores the write count and splits a frame |
| R-44 | P3 | wire | A bare attach steals the sizer and drops claims |
| R-45 | P3 | wire | Nothing caps clients, waiters or watches |
| R-46 | P3 | wire | The secret-sync trust verdict comes from the caller |
| R-47 | P3 | proxy | The loopback test receives host:port and matches by prefix |
| R-48 | P3 | server | Attach and resync read screens serially, 2 s each |
| R-49 | P3 | lifecycle | paneExited races ensurePane on one pane id |
| R-50 | P3 | pane | Two pane goroutines have no stop path |
| R-51 | P3 | pane | A markless wait can poll twice |
| R-52 | P3 | tests | Two tests fail on a loaded machine |
| R-53 | P3 | parser | C1 introducers, bad UTF-8 and late combining marks |
| R-54 | P3 | keymap | An empty action name and an esc prefix are accepted |
| R-55 | P3 | render | The help table names the ask toggle twice |
| R-56 | P3 | render | Two-digit option descriptions indent wrong |
| R-57 | P3 | render | The rail band is unclipped |
| R-58 | P3 | web | Web token handling has three soft spots |
| R-59 | P3 | secrets | internal/redact is dead code |
| R-60 | P3 | ask | The UTF-8 gate cannot fire through JSON |
| R-61 | P3 | ask | Duplicate ask ids survive a hand edit |
| R-62 | P3 | ask | A newline in a question corrupts the list |
| R-63 | P3 | ask bridge | The bridge's limits are schema-only |
| R-64 | P3 | ask | ask-await polls at 10 Hz per waiter |
| R-65 | P3 | web | An ask-answer with no target sends an empty id |
| R-66 | P3 | persistence | Load repairs are not written back; unknown fields vanish |
| R-67 | P3 | cli | dm web and dm purge skip the shared guard |
| R-68 | P3 | persistence | Existing files and directories keep permissive modes |
| R-69 | P3 | gates | The layer gate ignores test imports |
| R-70 | P3 | gates | No make test and no CI |
| R-71 | P3 | docs | The reference pages drift from their source |

## Fix batches

Eight batches, one reviewable change each. Batches 2, 3 and 5 touch the same packages, so land them
in this order, one diff each, and hand each over before the next. This is a proposal, not a plan that
runs.

**1, green the baseline and the gates.** Touches `config_model.go`, the two `HelpNotes` tests,
`internal/shot/testdata/golden`, `testdata/frames`, `Makefile`, the doc anchors. Covers R-10, R-40,
R-41, R-42, R-69, R-70, R-71. Proof: `go test ./...` green twice; a regenerated golden and frame in
the same diff; a citation check that each cited line holds the symbol. Land this first, because every
later batch needs a truthful suite.

**2, transport budgets and deadlines.** Touches `server_core.go`, `server_rpc.go`, `server_view.go`,
`server_sizer.go`, `wire.go`. Covers R-01, R-13, R-43, R-44, R-45. Proof: `go test ./internal/server
./internal/wire -race`; a test that a 2 MB ask still delivers a snapshot; a test that a dropped frame
returns an error; a replay ring test above 1 MiB.

**3, server locking and broadcast cost.** Touches `server_mutate.go`, `server_rpc.go`,
`server_secrets.go`, `server_core.go`. Covers R-03, R-11, R-12, R-30. Proof: `go test
./internal/server -race` with the two race probes turned into tree tests, plus a test that runs
`sendKeys` against a concurrent adopt.

**4, ask store caps and pruning.** Touches `server_ask.go`, `server_mutate.go`, `ask.go`, `wire.go`,
`client_ask.go`, `cli.go`, the bridge. Covers R-02, R-31, R-32, R-33, R-39, R-60, R-61, R-62, R-63,
R-64, R-65. Proof: `go test ./internal/ask ./internal/server ./internal/cli ./internal/client -run
Ask`; tests that an open past the cap is refused and that `closePane` prunes.

**5, session lifecycle and durability.** Touches `transport.go`, `state.go`, `server_box.go`,
`server_view.go`, `server_mutate.go`, `cli.go`, `web.go`, `webui.go`. Covers R-06, R-09, R-14, R-15,
R-22, R-23, R-24, R-25, R-26, R-27, R-28, R-29, R-49, R-66, R-67, R-68. Proof: `go test
./internal/session ./internal/transport ./internal/server`; tests that reset refuses a held lock, that
a null workspace errors instead of panicking, that a close reaps the pid, and that save syncs the
directory.

**6, client input and the parser.** Touches `client_input.go`, `client_keys.go`, `parser.go`,
`cell.go`, `rail.go`, `segment/seg.go`, `config_parse.go`. Covers R-04, R-05, R-07, R-17, R-18, R-19,
R-53, R-54. Proof: `go test ./internal/client ./internal/vt ./internal/rail ./internal/keymap`; the
paste-injection, 13 MiB paste, rail control-byte and unterminated-OSC probes become tests.

**7, web surface.** Touches `webui.go`, `action.go`, `upload.go`, `session.go`, `server_box.go`,
`pane.go`, and drops `internal/redact`. Covers R-08, R-34, R-35, R-36, R-37, R-38, R-58, R-59. Proof:
`go test ./internal/webui ./internal/secrets`; curl probes for Origin, the planted-symlink upload, the
PNG bomb and the FIFO session.

**8, render polish.** Touches `ask.go`, `panel.go`, `client_ask.go`, `client_view.go`,
`render_modal.go`, `internal/layout`. Covers R-20, R-21, R-55, R-56, R-57, R-52. Proof: `go test
./internal/client ./internal/shot`; the row sweep must find the question at 8, 10 and 12 rows; a
tab-expansion test; two green full-suite runs on a loaded machine.

## Probed, held

These were attacked and held. The absence of a finding is evidence too.

- Framing abuse ended only its own connection: zero length, `0xFFFFFFFF`, `MaxFrame+1`, truncated
  JSON, a header then a dead peer, non-object JSON. A following ping answered in 1 to 3 ms.
- Unknown actions are refused by name, unknown fields are dropped as documented, and the box and ask
  decoders use `DisallowUnknownFields`, so skew is refused, not misread.
- A stalled client does not wedge the server: its backlog drops at 1 MiB and becomes a resync; 2 MB
  of output drained 73 frames plus the picture while another connection pinged in 1 ms.
- Ask and box waits release on a vanished peer within 100 ms, and an await over a closed ask answers
  `ask: not_found` in 2 s.
- No inverse lock cycle was found among `s.mu`, `c.mu`, `branchMu`, `paneConn.mu`, `p.mu` and
  `secretPush`, and no socket write happens under `s.mu` except `secretPush`.
- An endless split, close and attach loop left fds flat at 9 and one watcher goroutine per live pane;
  there is no loop-variable capture bug and no send on a closed channel.
- Answer validation refused every bad shape: out-of-range or repeated indexes, option plus text, blank text, cancel plus items, a double answer, unknown fields at every level, 1000 options, a 1 MB question.
- Every web route refuses an absent or bad token, path traversal under `/static` and in pane ids is
  refused, read-only refuses every write route, and GET on a write route is 405.
- Upload sniffing refused a text file named `.png`, an SVG, an empty body and 11 MB, and stored files
  at 0600.
- Markdown cannot run code: raw HTML is dropped, `javascript:` is blocked, and the page writes every ask field through `textContent`.
- Secrets stay out of argv and `state.json`; `Fetch` uses an env allowlist; `secret-type` refuses a
  control byte; the proxy pins its upstream and refuses CR/LF.
- Pane daemon shutdown has no double close and no WaitGroup reuse; a concurrent save and load see one
  complete file; two box posters do not lose entries and the box caps hold.
- The wasm session writes no disk, and a negative split weight cannot produce a negative rect.
- Layout and paint held: `PaintPanel` across nine degenerate rects, the ask float across 13 screen sizes, CJK and emoji width, damage accounting, and overlay removal.
- Out of scope: esctest2 was not run (`ESCTEST_DIR` unset), so the parser rests on unit probes, not on the 567-case baseline.

## Evidence index

The eight reports are under `/home/b/Code/Personal/dm/.dungeon/adversarial-review/reports/`.

- `review-wire.md`: `go test ./internal/wire/ ./internal/transport/ -race -count=1`; `go test ./internal/server/ -race -run Sync -count=1`; a re-implemented framing client under `/tmp/wireprobe` against a scratch server; `VmSize` measured with 40 stalled connections.
- `review-concurrency.md`: `go test -race -count=1 ./internal/server/ ./internal/websession/ ./internal/transport/`, then the same with `./internal/pane/`; two race probes and a save-cost probe in a /tmp copy; split, close and attach stress loops; 6 rounds of two concurrent clients.
- `review-input.md`: `go test ./internal/vt/ ./internal/keymap/ ./internal/client/`; `go test ./internal/esctest/` (skipped by design); probes in `/tmp/dmprobe` for rail, osc, client, keymap and vt.
- `review-render.md`: `go test ./internal/client/ ./internal/panel/ ./internal/layout/ ./internal/shot/`; `go test ./internal/keymap/`; `go test ./internal/client -run Golden -v`; frame replays and live renders at 120x30.
- `review-security.md`: curl against a loopback `dm web` with a temp `DM_STATE` and a test token; `go test -count=1 ./internal/webui/ ./internal/secrets/ ./internal/redact/`.
- `review-ask.md`: `go test ./internal/ask/ ./internal/server/ ./internal/webui/` and `-run Ask` for cli and client; `go build ./...`; live `dm ask` probes against a temp server; `/api/action` and `/api/fleet` curl probes.
- `review-persistence.md`: `go build -o /tmp/dm-review .`; `go test -count=1 ./internal/session/ ./internal/box/ ./internal/websession/`; corrupt and foreign state probes; `SIGSTOP` plus kill, reset, purge and flock probes; 20 concurrent posts.
- `review-gates.md`: `go test ./...`; `go build ./...`; `go vet ./...`; /tmp copies of `735cfe8~1` and `a783ac4~1`; `gremlins unleash` in a /tmp copy; a python scan of every internals citation.
