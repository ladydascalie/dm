package pane

import (
	"strings"
	"testing"
)

// TestPaneEnvMatrix proves the allowlist rule, name by name. The first block
// is the set a shell and a terminal must keep; the second is what the
// adversarial review proved reached a pane when the rule was a denylist; the
// last is everything else, which the rule now drops.
func TestPaneEnvMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		keep bool
	}{
		// A shell and a terminal need these.
		{"PATH", true},
		{"HOME", true},
		{"USER", true},
		{"LOGNAME", true},
		{"SHELL", true},
		{"TERM", true},
		{"TERM_PROGRAM", true},
		{"COLORTERM", true},
		{"LANG", true},
		{"LC_ALL", true},
		{"LC_CTYPE", true},
		{"TZ", true},
		{"TMPDIR", true},
		{"XDG_CONFIG_HOME", true},
		{"XDG_RUNTIME_DIR", true},
		{"DISPLAY", true},
		// The path exemptions: a socket path or a cookie path, not a value.
		{"SSH_AUTH_SOCK", true},
		{"XAUTHORITY", true},
		{"MY_SOCK", true},
		// Proved to reach a pane while nine hint words were the whole rule.
		{"AWS_ACCESS_KEY_ID", false},
		{"AWS_SECRET_ACCESS_KEY", false},
		{"MYSQL_PWD", false},
		{"DATABASE_URL", false},
		{"STRIPE_KEY", false},
		{"GIT_ASKPASS", false},
		{"KUBECONFIG", false},
		{"OP_SERVICE_ACCOUNT_TOKEN", false},
		{"GITHUB_TOKEN", false},
		{"PASSWORD", false},
		{"私人PASSWORD", false},
		// A credential-shaped name may not ride the socket exemption.
		{"AUTH_TOKEN_SOCK", false},
		{"COOKIEJAR_SOCK", false},
		// A shell does not need these, and DM_STATE is not inherited.
		{"WINDOWID", false},
		{"TMUX", false},
		{"MAIL", false},
		{"EDITOR", false},
		{"GPG_KEY", false},
		{"DM_STATE", false},
		{"DM_NERD", false},
		{"PWD", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := paneEnv([]string{c.name + "=value"}, "p1", "sess")
			if kept := containsExact(got, c.name+"=value"); kept != c.keep {
				t.Fatalf("%s: kept = %v, want %v (env: %q)", c.name, kept, c.keep, got)
			}
		})
	}
}

// TestPaneEnvMatrixNameToken proves the hint tokenizer behaves as its comment
// says: a separator makes a match, a missing or doubled one does not.
func TestPaneEnvMatrixNameToken(t *testing.T) {
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

// TestPaneEnvOverridesInherited proves an inherited DM_PANE or DM_SESSION is
// replaced, not duplicated: the pane reports the ids it was spawned with.
func TestPaneEnvOverridesInherited(t *testing.T) {
	t.Parallel()
	got := paneEnv([]string{"DM_PANE=old", "DM_SESSION=oldsess", "PATH=/bin"}, "new", "newsess")
	for _, kv := range got {
		if kv == "DM_PANE=old" || kv == "DM_SESSION=oldsess" {
			t.Fatalf("stale identity %q survived: %q", kv, got)
		}
	}
	if !containsExact(got, "DM_PANE=new") || !containsExact(got, "DM_SESSION=newsess") {
		t.Fatalf("env lacks the new identity: %q", got)
	}
}

// TestPaneEnvKeepsSocketAndCookiePaths proves the path exemption outranks the
// credential words: SSH_AUTH_SOCK and XAUTHORITY carry a permission-gated
// path, and stripping them breaks ssh-agent forwarding and X clients. A
// credential-shaped name still cannot use the exemption.
func TestPaneEnvKeepsSocketAndCookiePaths(t *testing.T) {
	t.Parallel()
	got := paneEnv([]string{
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"XAUTHORITY=/home/b/.Xauthority",
		"MY_SOCK=/run/my.sock",
		"DOCKER_AUTH_TOKEN=secret",
		"MY_TOKEN_SOCK=/run/token.sock",
	}, "p", "s")
	for _, keep := range []string{
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"XAUTHORITY=/home/b/.Xauthority",
		"MY_SOCK=/run/my.sock",
	} {
		if !containsExact(got, keep) {
			t.Errorf("%s stripped, want kept: %q", keep, got)
		}
	}
	for _, drop := range []string{"DOCKER_AUTH_TOKEN=secret", "MY_TOKEN_SOCK=/run/token.sock"} {
		if containsExact(got, drop) {
			t.Errorf("%s kept, want stripped: %q", drop, got)
		}
	}
}

// TestPaneEnvAllowlistIsSmall proves the allowlist stays short: a name lands
// on it when a shell or a terminal reads it before the first rc line runs,
// and every other need is the operator's rc file to re-export.
func TestPaneEnvAllowlistIsSmall(t *testing.T) {
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
