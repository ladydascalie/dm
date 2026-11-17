package pane

import (
	"strings"
)

// paneEnv builds the child's environment by rule, not by inheritance. The
// pane runs the server's children, so inheriting the whole environment hands
// the shell every variable the server holds, including its own credentials.
// A variable that reaches a pane is published twice over, on the screen and
// in every read-back, and the server's redaction only knows the values it
// registered.
//
// So the rule is an allowlist. A variable passes only when it is one of the
// names in paneEnvKeep, a name a shell and a terminal need; or when it
// belongs to the LC_ or XDG_ name families; or when it is a path to a
// permission-gated socket or cookie file, SSH_AUTH_SOCK and its kin,
// XAUTHORITY. Everything else is dropped, and the operator's own rc file can
// re-export whatever else the session needs.
//
// The path exemption is narrow on purpose: a credential-shaped name may not
// ride it, so a name must clear the allowlist or be free of the credential
// words to pass. The allowlist itself is the rule; a name is not judged on
// its spelling once it is on it, and a name that is not on it is not judged
// at all.
//
// This narrows a shell's environment for accidents. It is not a secrecy
// boundary: a pane runs as the same user as the server, so it can read the
// daemon's /proc/<pid>/environ whatever this function does.
//
// DM_PANE and DM_SESSION are never inherited: the pane sets them last, so a
// pane always reports the ids it was spawned with and never a stale pair.
func paneEnv(in []string, id, session string) []string {
	out := make([]string, 0, len(in)+2)
	for _, kv := range in {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if name == "DM_PANE" || name == "DM_SESSION" {
			continue
		}
		if !paneEnvName(name) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "DM_PANE="+id, "DM_SESSION="+session)
}

// paneEnvKeep is the exact allowlist: the names a shell and a terminal need
// to start. Keep it short. Anything else the operator's rc file re-exports is
// theirs to choose; a name only lands here when a shell or a terminal reads
// it before the first rc line runs.
var paneEnvKeep = map[string]struct{}{
	// The command search path. Without it nothing runs.
	"PATH": {},
	// The home directory ~, rc files and history hang off.
	"HOME": {},
	// The identity a prompt and a tool read.
	"USER": {},
	// The other spelling of the login user; POSIX tools read it.
	"LOGNAME": {},
	// The login shell the operator chose.
	"SHELL": {},
	// The terminal type terminfo and curses look up.
	"TERM": {},
	// The terminal family; programs that adapt to it read it.
	"TERM_PROGRAM": {},
	// True colour support; the shell's colour helpers check it.
	"COLORTERM": {},
	// The default locale.
	"LANG": {},
	// The timezone date and the shell prompt read.
	"TZ": {},
	// Where mktemp and every temp file go.
	"TMPDIR": {},
	// The X display; with XAUTHORITY, an X client started from a pane.
	"DISPLAY": {},
	// The agent socket path; ssh-agent forwarding.
	"SSH_AUTH_SOCK": {},
	// The X cookie path; the other half of the DISPLAY exemption.
	"XAUTHORITY": {},
}

// paneEnvPrefixes are the name families that pass whole: LC_* carries the
// locale categories a shell and every program it starts read, XDG_* the base
// directories a login session uses.
var paneEnvPrefixes = []string{"LC_", "XDG_"}

// paneEnvName reports whether the pane may inherit a variable with this name.
func paneEnvName(name string) bool {
	if _, ok := paneEnvKeep[name]; ok {
		return true
	}
	for _, prefix := range paneEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return envPathName(name) && !secretEnvName(name)
}

// envPathName reports whether a variable name carries a path to a
// permission-gated socket or cookie file rather than a secret value. The
// suffix covers the socket conventions, SSH_AUTH_SOCK and its kin; the
// single name covers X's cookie path, XAUTHORITY. A credential-shaped name
// cannot use the exemption: the caller checks it against secretEnvName.
func envPathName(name string) bool {
	return name == "XAUTHORITY" || strings.HasSuffix(name, "_SOCK")
}

// secretEnvHints are the words that mark a variable name as a credential.
// The match runs against the upper-cased name with every run of
// non-alphanumeric characters turned into one underscore: API_KEY and
// api-key match, API__KEY, APIKEY and apiKey do not, because the tokenizer
// keeps no separator. The allowlist is what carries the rule; this match
// only stops a credential-shaped name from riding the socket path exemption.
// KEY alone is absent on purpose: it would catch ordinary names like
// GPG_KEY.
var secretEnvHints = []string{
	"TOKEN", "SECRET", "PASSWD", "PASSWORD", "CREDENTIAL",
	"API_KEY", "PRIVATE_KEY", "AUTH", "COOKIE",
}

// secretEnvName reports whether a variable name names a credential.
func secretEnvName(name string) bool {
	upper := strings.ToUpper(name)
	var b strings.Builder
	b.Grow(len(upper))
	for _, r := range upper {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	for _, hint := range secretEnvHints {
		if strings.Contains(s, hint) {
			return true
		}
	}
	return false
}
