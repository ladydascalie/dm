# ask-bridge

A pi extension that registers the tool `ask_user_question` and answers it with
dm's ask store. The question lands in the session state every dm surface draws:
the client float, the web form, any other pane. Nothing renders in pi.

## Install

Symlink the extension where pi loads extensions:

    ln -sf "$PWD/skills/ask-bridge/ask-bridge.ts" ~/.pi/agent/extensions/ask-bridge.ts

The tool name must be registered once. Remove `@juicesharp/rpiv-ask-user-question`
from the `extensions` list in `~/.pi/agent/settings.json`, then restart pi. This
repo does not edit that file.

## Run

The bridge uses the store when both hold:

- `dm` is on `PATH`.
- `DM_PANE` is set. A pi started inside a dm pane inherits `DM_PANE` and
  `DM_SESSION`; a pi in a plain terminal does not.

One call runs `dm ask open --pane $DM_PANE --tool-call <id> --json` with
`{questions}` on stdin, then `dm ask await <id> --json` until an answer or a
cancel arrives, then `dm ask close <id> --json`.

The reply is the envelope `ask_user_question` always returned: the answered
prefix, one `"question"="answer"` segment per question, `selected preview:`,
`user notes:`, `unanswered:`, `global note:`, and the trailing sentence. A
cancel, and an empty result, both read `User declined to answer questions`.
`details` carries `answers`, `cancelled`, `globalNote`, `unanswered`, `path`
(`dm` or `tui`), `askId` and, on the fallback, `fallbackReason`.

A daemon refusal (`dm: ask: E_EXISTS: ...`, `ask: invalid_answer: ...`) comes
back as a tool error with the code in the text and in `details.error`.

## Fallback

With no `DM_PANE`, no `dm` on `PATH`, or a daemon that does not answer, the
bridge walks the questions with `ctx.ui.select` and `ctx.ui.input`, so a plain
pi in a terminal still asks. `details.path` says which path ran. Escape at any
dialog declines the whole ask.

## Limits

One ask carries 1 to 4 questions of 2 to 4 options, a 16 character header and a
60 character label, checked before any dm call. The reserved labels `Other`,
`Type something.` and `Next` are rejected, the same as rpiv.

## Test

    node --experimental-strip-types --test skills/ask-bridge/ask.test.ts

The test drives the extension against a fake pi object and a fake `dm` script
on `PATH`. It needs no session and no terminal.
