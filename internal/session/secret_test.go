package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/paths"
)

func TestSecretValidate(t *testing.T) {
	t.Parallel()
	good := []*Secret{
		{Name: "gh", Tool: ToolOp, Reference: "op://Vault/gh/token", Upstream: "https://api.github.com", Header: "Authorization: Bearer {}"},
		{Name: "npm", Tool: ToolPass, Reference: "npm/token"},
		{Name: "plain", Tool: ToolPass, Reference: "plain"},
	}
	for _, s := range good {
		if err := s.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", s, err)
		}
	}
	bad := []*Secret{
		{Name: "", Tool: ToolOp, Reference: "op://v/a/b"},
		{Name: "x", Tool: "bw", Reference: "whatever"},
		{Name: "x", Tool: ToolOp, Reference: "vault/item/field"},
		{Name: "x", Tool: ToolPass, Reference: ""},
		{Name: "x", Tool: ToolPass, Reference: "/etc/passwd"},
		{Name: "x", Tool: ToolPass, Reference: "item", Header: "Authorization: Bearer"},
	}
	for _, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", s)
		}
	}
}

func TestSecretLookup(t *testing.T) {
	t.Parallel()
	st := NewStateAt(t.TempDir(), "look")
	if st.Secret("gh") != nil {
		t.Fatal("empty registry answered a lookup")
	}
	st.Secrets = []*Secret{{Name: "gh", Tool: ToolOp, Reference: "op://v/a/b"}}
	if got := st.Secret("gh"); got == nil || got.Tool != ToolOp {
		t.Fatalf("lookup = %+v, want the gh secret", got)
	}
	if st.Secret("nope") != nil {
		t.Fatal("lookup answered an unknown name")
	}
}

// TestSecretsRoundTrip proves a registered secret, including the optional
// upstream and header template, survives a save and load byte for byte.
func TestSecretsRoundTrip(t *testing.T) {
	t.Parallel()
	st := NewStateAt(t.TempDir(), "sec")
	st.Secrets = []*Secret{
		{Name: "gh", Tool: ToolOp, Reference: "op://Vault/gh/token", Upstream: "https://api.github.com", Header: "Authorization: Bearer {}"},
		{Name: "npm", Tool: ToolPass, Reference: "npm/token"},
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(st.Dir(), "sec")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Secrets) != 2 {
		t.Fatalf("loaded %d secrets, want 2", len(got.Secrets))
	}
	if s := got.Secret("gh"); s == nil || s.Upstream != "https://api.github.com" || s.Header != "Authorization: Bearer {}" {
		t.Fatalf("gh secret = %+v", s)
	}
	if s := got.Secret("npm"); s == nil || s.Upstream != "" || s.Header != "" {
		t.Fatalf("npm secret = %+v", s)
	}
}

// TestSecretsAbsentOnOldState proves a state file written before the registry
// existed loads with an empty registry, and no value-shaped field anywhere.
func TestSecretsAbsentOnOldState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const raw = `{
  "name": "old",
  "workspaces": [
    {"id": "s1", "name": "main", "windows": [], "active": 0}
  ],
  "active": 0,
  "cols": 80,
  "rows": 24,
  "panes": {},
  "nextWorkspace": 1,
  "nextWindow": 1,
  "idVersion": 2,
  "rail": 30,
  "railSet": true
}`
	if err := os.MkdirAll(paths.SessionDirAt(root, "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePathAt(root, "old"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "old")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Secrets) != 0 {
		t.Fatalf("old state loaded with %d secrets, want 0", len(got.Secrets))
	}
	if got.Secret("anything") != nil {
		t.Fatal("old state answered a secret lookup")
	}
}

// TestSecretsDropBadOnLoad proves a hand-edited state file costs its bad
// secret entries, not the whole load, and the repaired registry is written
// back so the next load is clean.
func TestSecretsDropBadOnLoad(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const raw = `{
  "name": "dirty",
  "workspaces": [
    {"id": "s1", "name": "main", "windows": [], "active": 0}
  ],
  "active": 0,
  "cols": 80,
  "rows": 24,
  "panes": {},
  "nextWorkspace": 1,
  "nextWindow": 1,
  "idVersion": 2,
  "rail": 30,
  "railSet": true,
  "secrets": [
    {"name": "good", "tool": "op", "reference": "op://v/i/f", "upstream": "https://api.example.com", "header": "Authorization: Bearer {}"},
    {"name": "", "tool": "op", "reference": "op://v/i/f"},
    {"name": "tool", "tool": "bw", "reference": "x"},
    {"name": "uri", "tool": "op", "reference": "not-a-uri"},
    {"name": "emptypass", "tool": "pass", "reference": ""},
    {"name": "abspass", "tool": "pass", "reference": "/abs"},
    {"name": "slot", "tool": "pass", "reference": "i", "header": "X: no slot"},
    {"name": "good", "tool": "pass", "reference": "dup"}
  ]
}`
	if err := os.MkdirAll(paths.SessionDirAt(root, "dirty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePathAt(root, "dirty"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "dirty")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Secrets) != 1 || got.Secrets[0].Name != "good" {
		t.Fatalf("kept %+v, want only the good entry", got.Secrets)
	}
	// The repair is persisted: a second load stays clean.
	if _, err := LoadStateAt(root, "dirty"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(paths.SessionDirAt(root, "dirty"), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), `"name"`); n != 3 {
		t.Fatalf("persisted state has %d name keys, want 3 (state, workspace, one secret)", n)
	}
}

// TestSecretsNeverCarryValues pins the safe-data rule: the persisted form of
// a secret round trip holds no value field, whatever a hand-edited file tries.
func TestSecretsNeverCarryValues(t *testing.T) {
	t.Parallel()
	st := NewStateAt(t.TempDir(), "novault")
	st.Secrets = []*Secret{{Name: "gh", Tool: ToolOp, Reference: "op://v/i/f", Header: "Authorization: Bearer {}"}}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(st.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"value", "secret_value", "password"} {
		if strings.Contains(string(b), needle) {
			t.Fatalf("state file mentions %q", needle)
		}
	}
	// A value field in a hand-edited file is ignored by unmarshal and dropped
	// as noise; the registry keeps only known fields.
	const raw = `{
  "name": "probe",
  "workspaces": [], "active": 0, "cols": 80, "rows": 24, "panes": {},
  "nextWorkspace": 1, "nextWindow": 1, "idVersion": 2, "rail": 30, "railSet": true,
  "secrets": [{"name": "gh", "tool": "pass", "reference": "gh", "value": "hunter2"}]
}`
	root := filepath.Join(t.TempDir(), "probe-root")
	if err := os.MkdirAll(paths.SessionDirAt(root, "probe"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePathAt(root, "probe"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "probe")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Secrets) != 1 {
		t.Fatalf("loaded %d secrets, want 1", len(got.Secrets))
	}
	if err := got.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(paths.StatePathAt(root, "probe"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "hunter2") {
		t.Fatal("a value from a hand-edited file survived a save")
	}
}

func TestSecretEnvValidation(t *testing.T) {
	t.Parallel()
	good := []*Secret{
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"PASSWORD_STORE_DIR=/home/b/.pass"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"GNUPGHOME=/x", "LANG=C.UTF-8"}},
		{Name: "gh", Tool: ToolOp, Reference: "op://v/i/f", Env: []string{"OP_ACCOUNT=my.1password.com"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"HOME=/home/b", "XDG_CONFIG_HOME=/home/b/.config"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"GNUPGHOME="}},
	}
	for _, s := range good {
		if err := s.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", s, err)
		}
	}
	bad := []*Secret{
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"NOEQUALS"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"=value"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"LD_PRELOAD=/tmp/evil.so"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"LD_LIBRARY_PATH=/tmp"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"PATH=/repo/bin"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"BASH_ENV=/tmp/x"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"ENV=/tmp/x"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"SHELLOPTS=xtrace"}},
		{Name: "gh", Tool: ToolOp, Reference: "op://v/i/f", Env: []string{"DB_PASS=x"}},
		{Name: "gh", Tool: ToolOp, Reference: "op://v/i/f", Env: []string{"OP_SESSION_me=x"}},
		{Name: "gh", Tool: ToolOp, Reference: "op://v/i/f", Env: []string{"OP_SERVICE_ACCOUNT_TOKEN=x"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"api-key=x"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"HOME=/a", "HOME=/b"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"X=1\nY=2"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"home=/a"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{"HOME_EXTRA=/a"}},
		{Name: "gh", Tool: ToolPass, Reference: "dev/t", Env: []string{" HOME=/a"}},
	}
	for _, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", s)
		}
	}
}

// TestSecretEnvErrorNeverCarriesTheValue pins the second half of the
// contract: a refusal names the entry by index, or the key by name, plus a
// length, and never the KEY=VALUE text. A value typed into --env must not
// reach the CLI's stderr or a sync reply through the error string.
func TestSecretEnvErrorNeverCarriesTheValue(t *testing.T) {
	t.Parallel()
	const value = "poc-token-envval"
	cases := []struct {
		name string
		env  []string
		want string
	}{
		{"no equals", []string{"env " + value}, "env entry 0"},
		{"line break", []string{"TOKEN=" + value + "\nX=1"}, "env entry 0"},
		{"not allowlisted", []string{"DB_PASS=" + value}, "DB_PASS"},
		{"empty key", []string{"=" + value}, "env entry 0"},
	}
	for _, tc := range cases {
		s := &Secret{Name: "s", Tool: ToolPass, Reference: "item", Env: tc.env}
		err := s.Validate()
		if err == nil {
			t.Fatalf("%s: Validate accepted %q", tc.name, tc.env)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("%s: error carries the value: %q", tc.name, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error %q, want it to name %q", tc.name, err, tc.want)
		}
		if !strings.Contains(err.Error(), "bytes") {
			t.Fatalf("%s: error %q carries no length", tc.name, err)
		}
	}
}

// TestSecretEnvRefusesALongKeyValue pins the key echo's bound: a key that is
// really a pasted value is cut and its length kept, so the whole string does
// not travel into the error.
func TestSecretEnvRefusesALongKeyValue(t *testing.T) {
	t.Parallel()
	key := strings.Repeat("poc-token-key", 16)
	s := &Secret{Name: "s", Tool: ToolPass, Reference: "item", Env: []string{key + "=1"}}
	err := s.Validate()
	if err == nil {
		t.Fatal("Validate accepted a key that is a pasted value")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("the error carries the whole key: %q", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("(%d bytes)", len(key))) {
		t.Fatalf("the error does not carry the key's length: %q", err)
	}
}

// TestSecretEnvRefusedOnLoadKeepsNoValue proves an old state file that carried
// a credential-named env entry loads without it, and the rewritten state file
// holds no value: the allowlist is enforced at rest, not only at register.
func TestSecretEnvRefusedOnLoadKeepsNoValue(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const value = "poc-db-pass-1234"
	st := NewStateAt(root, "envold")
	st.Secrets = []*Secret{
		{Name: "ok", Tool: ToolPass, Reference: "dev/t", Env: []string{"PASSWORD_STORE_DIR=/p"}},
		{Name: "old", Tool: ToolOp, Reference: "op://v/i/f", Env: []string{"DB_PASS=" + value}},
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "envold")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Secrets) != 1 || got.Secrets[0].Name != "ok" {
		t.Fatalf("load kept %+v, want only the ok entry", got.Secrets)
	}
	b, err := os.ReadFile(got.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), value) {
		t.Fatalf("the rewritten state file still carries the value: %s", b)
	}
}

// TestSecretsDropNilOnLoad is the review-session counter-example: a state
// file containing a JSON null secret entry loads without panic, the nil
// counts as a dropped bad entry, and the good entries survive.
func TestSecretsDropNilOnLoad(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const raw = `{
  "name": "nulls",
  "workspaces": [
    {"id": "s1", "name": "main", "windows": [], "active": 0}
  ],
  "active": 0,
  "cols": 80,
  "rows": 24,
  "panes": {},
  "nextWorkspace": 1,
  "nextWindow": 1,
  "idVersion": 2,
  "rail": 30,
  "railSet": true,
  "secrets": [
    null,
    {"name": "good", "tool": "pass", "reference": "item"}
  ]
}`
	if err := os.MkdirAll(paths.SessionDirAt(root, "nulls"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePathAt(root, "nulls"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "nulls")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Secrets) != 1 || got.Secrets[0].Name != "good" {
		t.Fatalf("kept %+v, want only the good entry", got.Secrets)
	}
	// The repair is persisted: the second load sees no nil and stays clean.
	if _, err := LoadStateAt(root, "nulls"); err != nil {
		t.Fatal(err)
	}
}
