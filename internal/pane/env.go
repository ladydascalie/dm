package pane

import (
	"strings"
)

// paneEnv builds the child's environment by rule, not by inheritance. The
// pane daemon inherits the server's whole environment, and the server may
// hold credentials of its own, OP_SERVICE_ACCOUNT_TOKEN for one. None of
// those may reach a pane: everything the pane runs lands on its screen, its
// scrollback and every read-back, so a leaked variable is published output.
//
// The rule strips by name pattern. A variable whose upper-cased name
// contains one of the secretEnvHints words is a credential and is dropped,
// whatever its exact spelling; everything else a shell needs, PATH, HOME,
// TERM, LANG and the rest, passes through. DM_PANE and DM_SESSION are
// dropped from the inheritance and set last, so a pane always reports the
// ids it was spawned with and never a stale pair.
//
// One exemption runs before the strip rule, the same way bare KEY already
// escapes it: a name is kept when it names a socket path or a cookie file
// path. SSH_AUTH_SOCK and XAUTHORITY match the AUTH hint, but their values
// are filesystem paths gated by file permissions, not env secrets, and
// stripping them breaks ssh-agent forwarding and X clients from panes.
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
		if !envPathName(name) && secretEnvName(name) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "DM_PANE="+id, "DM_SESSION="+session)
}

// envPathName reports whether a variable name carries a path to a
// permission-gated socket or cookie file rather than a secret value. The
// suffix covers the socket conventions, SSH_AUTH_SOCK and its kin; the
// single name covers X's cookie path, XAUTHORITY.
func envPathName(name string) bool {
	return name == "XAUTHORITY" || strings.HasSuffix(name, "_SOCK")
}

// secretEnvHints are the words that mark a variable name as a credential.
// The match runs against the upper-cased name with every run of
// non-alphanumeric characters turned into one underscore, so api-key,
// apiKey and API_KEY all match API_KEY. KEY alone is absent on purpose: it
// would strip ordinary non-secret names like GPG_KEY.
var secretEnvHints = []string{
	"TOKEN", "SECRET", "PASSWD", "PASSWORD", "CREDENTIAL",
	"API_KEY", "PRIVATE_KEY", "AUTH", "COOKIE",
}

// secretEnvName reports whether a variable name matches the strip rule.
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
