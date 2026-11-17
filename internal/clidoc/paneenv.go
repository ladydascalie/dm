package clidoc

// PaneEnvNote says what a pane's shell inherits. The rule itself lives in
// internal/pane/env.go; this is the operator-facing copy of it, and the man
// page, the HTML page and llms.txt render it so none of them states a
// different rule.
const PaneEnvNote = "A pane's shell inherits a short allowlist of the server's environment: PATH, HOME, USER, LOGNAME, SHELL, TERM, TERM_PROGRAM, COLORTERM, LANG, LC_*, TZ, TMPDIR, XDG_*, DISPLAY, SSH_AUTH_SOCK and XAUTHORITY. Everything else is dropped, and dm sets DM_PANE and DM_SESSION itself. Each entry is a name a shell, a terminal or an X client needs before the shell reads its rc file; the rc file can re-export what else a session needs."
