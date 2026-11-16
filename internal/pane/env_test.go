package pane

import (
	"reflect"
	"strings"
	"testing"
)

// TestPaneEnvStripsSecrets proves the pane's child environment follows the
// strip rule: a credential the server holds, whatever its spelling, never
// reaches the pane, while the variables a shell needs and the pane's own
// identity do.
func TestPaneEnvStripsSecrets(t *testing.T) {
	t.Parallel()
	in := []string{
		"PATH=/usr/bin",
		"HOME=/home/b",
		"TERM=xterm-256color",
		"LANG=en_GB.UTF-8",
		"OP_SERVICE_ACCOUNT_TOKEN=secret-op",
		"AWS_SECRET_ACCESS_KEY=secret-aws",
		"GITHUB_TOKEN=secret-gh",
		"MY_PASSWORD=secret-pw",
		"appCredential=secret-app",
		"api-key=secret-api",
		"私人PASSWORD=secret-unicode",
		"DM_SESSION=stale",
		"GPG_KEY=ABCDEF12",
		"DM_STATE=/state",
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"XAUTHORITY=/home/b/.Xauthority",
	}
	got := paneEnv(in, "p1", "sess")

	var kept, stripped []string
	for _, kv := range in {
		name := kv[:strings.IndexByte(kv, '=')]
		if name == "DM_SESSION" {
			continue // replaced, judged below
		}
		if containsExact(got, kv) {
			kept = append(kept, name)
		} else {
			stripped = append(stripped, name)
		}
	}
	wantKept := []string{"PATH", "HOME", "TERM", "LANG", "GPG_KEY", "DM_STATE", "SSH_AUTH_SOCK", "XAUTHORITY"}
	wantStripped := []string{"OP_SERVICE_ACCOUNT_TOKEN", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "MY_PASSWORD", "appCredential", "api-key", "私人PASSWORD"}
	if !reflect.DeepEqual(kept, wantKept) {
		t.Fatalf("kept %q, want %q", kept, wantKept)
	}
	if !reflect.DeepEqual(stripped, wantStripped) {
		t.Fatalf("stripped %q, want %q", stripped, wantStripped)
	}
	if !containsExact(got, "DM_PANE=p1") {
		t.Fatalf("env lacks DM_PANE=p1: %q", got)
	}
	if !containsExact(got, "DM_SESSION=sess") {
		t.Fatalf("env lacks DM_SESSION=sess: %q", got)
	}
	if containsPrefix(got, "DM_SESSION=") && !containsExact(got, "DM_SESSION=sess") {
		t.Fatalf("stale DM_SESSION survived: %q", got)
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

func containsExact(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func containsPrefix(env []string, prefix string) bool {
	for _, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// TestPaneEnvKeepsSocketAndCookiePaths proves the path exemption outranks
// the AUTH hint: SSH_AUTH_SOCK and XAUTHORITY name permission-gated files,
// not secrets, and stripping them would break ssh-agent forwarding and X
// clients from panes.
func TestPaneEnvKeepsSocketAndCookiePaths(t *testing.T) {
	t.Parallel()
	got := paneEnv([]string{
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"XAUTHORITY=/home/b/.Xauthority",
		"GPG_AGENT_INFO=/tmp/gpg,S0:1",
		"DOCKER_AUTH_TOKEN=secret", // AUTH hint still strips plain credential names
	}, "p", "s")
	if !containsExact(got, "SSH_AUTH_SOCK=/tmp/agent.sock") {
		t.Fatalf("SSH_AUTH_SOCK stripped, want kept: %q", got)
	}
	if !containsExact(got, "XAUTHORITY=/home/b/.Xauthority") {
		t.Fatalf("XAUTHORITY stripped, want kept: %q", got)
	}
	if containsExact(got, "DOCKER_AUTH_TOKEN=secret") {
		t.Fatalf("DOCKER_AUTH_TOKEN kept, want stripped: %q", got)
	}
}
