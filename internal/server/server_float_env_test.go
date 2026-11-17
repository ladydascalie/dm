package server

import (
	"strings"
	"testing"
)

// envValues returns every value the env carries under name, in order.
func envValues(env []string, name string) []string {
	var vals []string
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k == name {
			vals = append(vals, v)
		}
	}
	return vals
}

// TestFloatEnvAllowlist proves a float's child gets the pane allowlist: the
// server's credentials do not reach it, and the float's own pane and session
// ids are set exactly once, with no inherited pair beside them.
func TestFloatEnvAllowlist(t *testing.T) {
	for _, kv := range []string{
		"OP_SERVICE_ACCOUNT_TOKEN=poc-op",
		"AWS_ACCESS_KEY_ID=poc-awsid",
		"AWS_SECRET_ACCESS_KEY=poc-awssecret",
		"MYSQL_PWD=poc-mysql",
		"DATABASE_URL=postgres://u:poc@h/db",
		"STRIPE_KEY=sk_poc",
		"KUBECONFIG=/home/b/.kube/config",
		"DM_STATE=/tmp/dmt-state",
		"DM_PANE=stale",
		"DM_SESSION=stale",
		"TERM=xterm-test",
	} {
		name, value, _ := strings.Cut(kv, "=")
		t.Setenv(name, value)
	}

	env := floatEnv("floatsess", "f1")

	for _, name := range []string{
		"OP_SERVICE_ACCOUNT_TOKEN", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"MYSQL_PWD", "DATABASE_URL", "STRIPE_KEY", "KUBECONFIG",
	} {
		if got := envValues(env, name); len(got) != 0 {
			t.Errorf("%s reached the float env: %q", name, got)
		}
	}
	if got := envValues(env, "DM_PANE"); len(got) != 1 || got[0] != "f1" {
		t.Errorf("DM_PANE = %q, want exactly [f1]", got)
	}
	if got := envValues(env, "DM_SESSION"); len(got) != 1 || got[0] != "floatsess" {
		t.Errorf("DM_SESSION = %q, want exactly [floatsess]", got)
	}
	if got := envValues(env, "TERM"); len(got) != 1 || got[0] != "xterm-test" {
		t.Errorf("TERM = %q, want exactly [xterm-test]", got)
	}
	// The dm command the float runs finds its session through the state root.
	if got := envValues(env, "DM_STATE"); len(got) != 1 || got[0] != "/tmp/dmt-state" {
		t.Errorf("DM_STATE = %q, want exactly [/tmp/dmt-state]", got)
	}
}

// TestFloatEnvDefaultTerm proves an empty server TERM is replaced by the
// fallback exactly once, so the child does not hold two TERM entries.
func TestFloatEnvDefaultTerm(t *testing.T) {
	t.Setenv("TERM", "")
	env := floatEnv("s", "f")
	if got := envValues(env, "TERM"); len(got) != 1 || got[0] != "xterm-256color" {
		t.Errorf("TERM = %q, want exactly [xterm-256color]", got)
	}
}
