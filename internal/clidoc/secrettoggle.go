package clidoc

// ToggleSecretsNote says what the ctrl+g toggle is for and what it is not. The
// behaviour lives in internal/pane and internal/server; this is the
// operator-facing copy of it, and the man page, the HTML page and llms.txt
// render it so no one of them claims a control the toggle is not.
const ToggleSecretsNote = "The toggle-secrets action turns a pane's secret redaction off and on. It exists so a human can edit a secret in place: while it is off the pane's registered values are kept and the pane's output passes through untouched, and turning it back on scrubs those values again and repaints the pane's history and screen, so nothing the reveal drew is left behind for a read-back or a scrollback to serve. It is not a security control: an agent that can reach the dm socket can flip it, as it can already type keys and read panes. The toast names the state the pane is left in."
