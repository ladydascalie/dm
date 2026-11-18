package pane

import (
	"fmt"
	"strings"
	"testing"
)

// TestEnvMatrix proves the allowlist rule, name by name. The first block is
// the set a shell and a terminal must keep; the second is what the adversarial
// review proved reached a pane when the rule was a denylist; the last is
// everything else, which the rule now drops.
func TestEnvMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value string
		keep  bool
	}{
		// A shell and a terminal need these.
		{"PATH", "/usr/bin", true},
		{"HOME", "/home/b", true},
		{"USER", "b", true},
		{"LOGNAME", "b", true},
		{"SHELL", "/bin/sh", true},
		{"TERM", "xterm-256color", true},
		{"TERM_PROGRAM", "dm", true},
		{"COLORTERM", "truecolor", true},
		{"LANG", "en_GB.UTF-8", true},
		{"LC_ALL", "en_GB.UTF-8", true},
		{"LC_CTYPE", "en_GB.UTF-8", true},
		{"TZ", "Europe/London", true},
		{"TMPDIR", "/tmp", true},
		{"XDG_CONFIG_HOME", "/home/b/.config", true},
		{"XDG_RUNTIME_DIR", "/run/user/1000", true},
		{"WAYLAND_DISPLAY", "wayland-1", true},
		{"DISPLAY", ":0", true},
		// A dm command run inside the shell needs the state root, which
		// DM_SESSION does not carry.
		{"DM_STATE", "/tmp/state", true},
		// The path exemptions: an absolute socket path or the cookie path.
		{"SSH_AUTH_SOCK", "/tmp/agent.sock", true},
		{"XAUTHORITY", "/home/b/.Xauthority", true},
		{"MY_SOCK", "/run/my.sock", true},
		// Proved to reach a pane while nine hint words were the whole rule.
		{"AWS_ACCESS_KEY_ID", "poc-awsid", false},
		{"AWS_SECRET_ACCESS_KEY", "poc-awssecret", false},
		{"MYSQL_PWD", "poc-mysql", false},
		{"DATABASE_URL", "postgres://u:poc@h/db", false},
		{"STRIPE_KEY", "sk_poc", false},
		{"GIT_ASKPASS", "/usr/bin/askpass", false},
		{"KUBECONFIG", "/home/b/.kube/config", false},
		{"OP_SERVICE_ACCOUNT_TOKEN", "poc-op", false},
		{"GITHUB_TOKEN", "poc-gh", false},
		{"PASSWORD", "poc-pw", false},
		{"私人PASSWORD", "poc-unicode", false},
		// A credential-shaped name may not ride the socket exemption, and
		// the exemption needs an absolute path value.
		{"AUTH_TOKEN_SOCK", "/run/token.sock", false},
		{"COOKIEJAR_SOCK", "/run/cookie.sock", false},
		{"MY_TOKEN_SOCK", "/run/token.sock", false},
		{"MY_SOCK", "pass:supersecret", false},
		{"MY_SOCK", "", false},
		// A shell does not need these.
		{"WINDOWID", "12345", false},
		{"TMUX", "/tmp/tmux-1000/default,1,0", false},
		{"MAIL", "/var/mail/b", false},
		{"EDITOR", "vi", false},
		{"GPG_KEY", "ABCDEF12", false},
		{"DM_NERD", "1", false},
		{"PWD", "/home/b", false},
	}
	for _, c := range cases {
		c := c
		t.Run(fmt.Sprintf("%s=%v", c.name, c.keep), func(t *testing.T) {
			t.Parallel()
			kv := c.name + "=" + c.value
			got := Env([]string{kv}, "p1", "sess")
			if kept := containsExact(got, kv); kept != c.keep {
				t.Fatalf("%s: kept = %v, want %v (env: %q)", kv, kept, c.keep, got)
			}
		})
	}
}

// TestEnvMatrixNameToken proves the hint tokenizer behaves as its comment
// says: a separator makes a match, a missing or doubled one does not.
func TestEnvMatrixNameToken(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"API_KEY", "api_key", "api-key"} {
		if !secretEnvName(name) {
			t.Errorf("%s: not a credential name, want one", name)
		}
	}
	for _, name := range []string{"API__KEY", "APIKEY", "apiKey"} {
		if secretEnvName(name) {
			t.Errorf("%s: a credential name, want none", name)
		}
	}
}

// TestEnvOverridesInherited proves an inherited DM_PANE or DM_SESSION is
// replaced, not duplicated: the child reports the ids it was spawned with.
func TestEnvOverridesInherited(t *testing.T) {
	t.Parallel()
	got := Env([]string{"DM_PANE=old", "DM_SESSION=oldsess", "PATH=/bin"}, "new", "newsess")
	for _, kv := range got {
		if kv == "DM_PANE=old" || kv == "DM_SESSION=oldsess" {
			t.Fatalf("stale identity %q survived: %q", kv, got)
		}
	}
	if !containsExact(got, "DM_PANE=new") || !containsExact(got, "DM_SESSION=newsess") {
		t.Fatalf("env lacks the new identity: %q", got)
	}
	if n := countPrefix(got, "DM_PANE="); n != 1 {
		t.Fatalf("env holds %d DM_PANE entries, want 1: %q", n, got)
	}
	if n := countPrefix(got, "DM_SESSION="); n != 1 {
		t.Fatalf("env holds %d DM_SESSION entries, want 1: %q", n, got)
	}
}

// TestEnvKeepsSocketAndCookiePaths proves the socket exemption outranks the
// credential words only for a real path: SSH_AUTH_SOCK and XAUTHORITY carry a
// permission-gated path, MY_SOCK with an absolute value is the same shape, and
// a credential-shaped name, or a value that is not an absolute path, is not.
func TestEnvKeepsSocketAndCookiePaths(t *testing.T) {
	t.Parallel()
	got := Env([]string{
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"XAUTHORITY=/home/b/.Xauthority",
		"MY_SOCK=/run/my.sock",
		"DOCKER_AUTH_TOKEN=secret",
		"MY_TOKEN_SOCK=/run/token.sock",
		"MY_SOCK=pass:supersecret",
	}, "p", "s")
	for _, keep := range []string{
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"XAUTHORITY=/home/b/.Xauthority",
	} {
		if !containsExact(got, keep) {
			t.Errorf("%s stripped, want kept: %q", keep, got)
		}
	}
	if !containsExact(got, "MY_SOCK=/run/my.sock") {
		t.Errorf("MY_SOCK with an absolute value stripped, want kept: %q", got)
	}
	for _, drop := range []string{
		"DOCKER_AUTH_TOKEN=secret",
		"MY_TOKEN_SOCK=/run/token.sock",
		"MY_SOCK=pass:supersecret",
	} {
		if containsExact(got, drop) {
			t.Errorf("%s kept, want stripped: %q", drop, got)
		}
	}
}

// TestEnvAllowlistIsSmall proves the allowlist stays short: a name lands on it
// when a shell or a terminal reads it before the first rc line runs, and every
// other need is the operator's rc file to re-export.
func TestEnvAllowlistIsSmall(t *testing.T) {
	t.Parallel()
	const limit = 20
	if len(paneEnvKeep) > limit {
		t.Fatalf("allowlist has %d names, want at most %d: %v", len(paneEnvKeep), limit, paneEnvKeep)
	}
	for name := range paneEnvKeep {
		if name != strings.ToUpper(name) {
			t.Errorf("allowlist name %q is not upper case", name)
		}
	}
}

func containsExact(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

// countPrefix counts entries whose name has prefix.
func countPrefix(env []string, prefix string) int {
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}
