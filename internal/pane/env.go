package pane

import (
	"path/filepath"
	"strings"
)

// Env returns the environment a child dm starts may inherit: the server's
// environment passed through an allowlist, with the child's own pane and
// session ids set last. A pane's shell and a float both come through here, so
// one rule covers every shell the server starts.
//
// The need is not hypothetical. The server holds credentials of its own,
// OP_SERVICE_ACCOUNT_TOKEN for one, and a variable that reaches a shell is
// published twice over: on the screen, and in every read-back. The server's
// redaction only knows the values it registered, so an unregistered credential
// in the environment is output.
//
// So the rule is an allowlist. A name passes when it is in paneEnvKeep, a name
// a shell, a terminal or a dm command in the shell needs to start; when it
// starts with LC_ or XDG_; or when it is a socket exemption, a *_SOCK name
// whose value is an absolute path. Everything else is dropped, and the
// operator's own rc file can re-export whatever else a session needs.
//
// On the allowlist the name is the whole decision: the value is not
// inspected. A name that is not on the allowlist is judged only for the socket
// shape: the value must be an absolute path and the name must not be
// credential-shaped, so MY_SOCK=/run/x.sock passes, MY_SOCK=pass:secret does
// not, and MY_TOKEN_SOCK=/run/t.sock does not either. SSH_AUTH_SOCK and
// XAUTHORITY are on the allowlist, so they pass by name alone.
//
// This narrows a shell's environment for accidents. It is not a secrecy
// boundary: a child runs as the same user as the server, so it can read the
// daemon's /proc/<pid>/environ whatever this function does.
//
// DM_PANE and DM_SESSION are never inherited: they are set last, so a child
// always reports the ids it was spawned with and never a stale pair.
func Env(in []string, id, session string) []string {
	out := make([]string, 0, len(in)+2)
	for _, kv := range in {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if name == "DM_PANE" || name == "DM_SESSION" {
			continue
		}
		if !envAllowed(name, value) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "DM_PANE="+id, "DM_SESSION="+session)
}

// paneEnvKeep is the exact allowlist: the names a shell, a terminal or a dm
// command in the shell needs. Keep it short. Anything else the operator's rc
// file re-exports is theirs to choose. A name lands here when the child reads
// it before the first rc line, when a program it starts later reads a value
// the name holds (a socket path, a cookie path or a display), or when a dm
// command run inside the shell needs it.
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
	// The Wayland socket name, relative to XDG_RUNTIME_DIR. A Wayland client
	// started from a pane, and every tool that reads the selection, needs it:
	// without the name, wl-paste falls back to wayland-0, and a clipboard
	// holding an image reads as empty.
	"WAYLAND_DISPLAY": {},
	// The X display; with XAUTHORITY, an X client started from a pane.
	"DISPLAY": {},
	// The agent socket path; ssh-agent forwarding.
	"SSH_AUTH_SOCK": {},
	// The X cookie path; the other half of the DISPLAY exemption.
	"XAUTHORITY": {},
	// The dm state root. A dm command run inside the shell needs the same
	// root to find its session; DM_SESSION names no path on its own. It is a
	// path, not a credential.
	"DM_STATE": {},
}

// paneEnvPrefixes are the name families that pass whole: LC_* carries the
// locale categories a shell and every program it starts read, XDG_* the base
// directories a login session uses.
var paneEnvPrefixes = []string{"LC_", "XDG_"}

// envAllowed reports whether a name and value may be inherited.
func envAllowed(name, value string) bool {
	if _, ok := paneEnvKeep[name]; ok {
		return true
	}
	for _, prefix := range paneEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return envPathName(name, value)
}

// envPathName reports whether a name is the socket exemption: a *_SOCK name
// whose value is an absolute path to a permission-gated socket, and whose name
// is not credential-shaped. The value check keeps a credential pasted into a
// socket-shaped name out; the name check keeps a credential name from wearing
// the suffix. XAUTHORITY is not here: it is on the allowlist and passes by
// name alone.
func envPathName(name, value string) bool {
	return strings.HasSuffix(name, "_SOCK") && filepath.IsAbs(value) && !secretEnvName(name)
}

// secretEnvHints are the words that mark a variable name as a credential.
// The match runs against the upper-cased name with every run of
// non-alphanumeric characters turned into one underscore: API_KEY and
// api-key match, API__KEY, APIKEY and apiKey do not, because the tokenizer
// keeps no separator. The allowlist is what carries the rule; this match
// only stops a credential-shaped name from riding the socket exemption.
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
