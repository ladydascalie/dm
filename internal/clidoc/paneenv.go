package clidoc

// PaneEnvNote says what a shell dm starts inherits. The rule itself lives in
// internal/pane/env.go; this is the operator-facing copy of it, and the man
// page and the HTML page render it so neither states a different rule.
const PaneEnvNote = "A shell dm starts, a pane's or a float's, inherits a short allowlist of the server's environment: PATH, HOME, USER, LOGNAME, SHELL, TERM, TERM_PROGRAM, COLORTERM, LANG, LC_*, TZ, TMPDIR, XDG_*, DISPLAY, SSH_AUTH_SOCK, XAUTHORITY and DM_STATE, plus a *_SOCK name with an absolute path value when the name is not credential-shaped. Everything else is dropped, and dm sets DM_PANE and DM_SESSION itself. Each entry is a name a shell, a terminal or an X client needs before the shell reads its rc file, or the dm state root that a dm command in the shell needs; the rc file can re-export what else a session needs."
