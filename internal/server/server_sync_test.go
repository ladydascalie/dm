package server

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/wire"
)

// writeMapping writes one .dm-secrets.kdl fixture into dir and returns its
// path.
func writeMapping(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, ".dm-secrets.kdl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	return path
}

// writeFile writes one mapping file fixture and returns its path.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// TestParseSecretsMappingAcceptsTheShape proves the reader takes the shape
// the POC drafted: secret blocks with tool, ref, repeatable env, upstream
// and header, and file blocks with one path.
func TestParseSecretsMappingAcceptsTheShape(t *testing.T) {
	src := `
// the mapping comment
secret "gh-cli" {
    tool "op"
    ref "op://Personal/gh/token"
    env "OP_PLUGIN_HOME=/tmp/x"
    env "OTHER=1"
    upstream "https://api.github.com"
    header "Authorization: Bearer {}"
}
file ".env"
file "sub/creds.env"
`
	mp, err := parseSecretsMapping(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(mp.Secrets) != 1 || len(mp.Files) != 2 {
		t.Fatalf("parse got %d secrets, %d files", len(mp.Secrets), len(mp.Files))
	}
	b := mp.Secrets[0]
	if b.Name != "gh-cli" || b.Tool != "op" || b.Ref != "op://Personal/gh/token" ||
		b.Upstream != "https://api.github.com" || b.Header != "Authorization: Bearer {}" {
		t.Fatalf("secret block parsed %+v", b)
	}
	if len(b.Env) != 2 || b.Env[0] != "OP_PLUGIN_HOME=/tmp/x" {
		t.Fatalf("env parsed %v", b.Env)
	}
	if mp.Files[0].Path != ".env" || mp.Files[1].Path != "sub/creds.env" {
		t.Fatalf("file blocks parsed %+v", mp.Files)
	}
}

// TestParseSecretsMappingRefusesBadShapes proves every malformed mapping is
// refused with the rule it broke.
func TestParseSecretsMappingRefusesBadShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"unknown node", "file \".env\"\nsecret2 \"x\" {}", "unknown node"},
		{"secret without name", "secret {}", "exactly one quoted name"},
		{"secret with two names", "secret \"a\" \"b\" {}", "exactly one quoted name"},
		{"duplicate secret", "secret \"a\" {}\nsecret \"a\" {}", "appears twice"},
		{"duplicate file", "file \"a\"\nfile \"a\"", "appears twice"},
		{"unknown child", "secret \"a\" { colour \"red\" }", "unknown colour"},
		{"child without value", "secret \"a\" { tool }", "takes exactly one quoted value"},
		{"file with children", "file \"a\" { tool \"op\" }", "takes no children"},
		{"file with two paths", "file \"a\" \"b\"", "exactly one quoted path"},
		{"bare value", "secret gh {}", "quoted values only"},
		{"unclosed block", "secret \"a\" {", "not closed"},
	}
	for _, tc := range cases {
		_, err := parseSecretsMapping(tc.src)
		if err == nil {
			t.Fatalf("%s: parse succeeded, want %q", tc.name, tc.want)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error %q, want it to name %q", tc.name, err, tc.want)
		}
	}
}

// TestParseSecretsMappingNamesTheLine proves a bad child carries its own
// line, not the parent's.
func TestParseSecretsMappingNamesTheLine(t *testing.T) {
	_, err := parseSecretsMapping("secret \"a\" {\n    tool \"op\"\n    colour \"red\"\n}")
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("error %v, want line 3", err)
	}
}

// syncReply is the secret-sync reply body the tests decode. The value
// fields stay out by construction; the test proves they do.
type syncReply struct {
	Mapping    string       `json:"mapping"`
	Registered []string     `json:"registered"`
	Kept       []string     `json:"kept"`
	Files      []fileResult `json:"files"`
	Warning    string       `json:"warning"`
	Uncovered  []string     `json:"uncovered"`
}

// syncServer builds the fixture: one server, one connection, no panes.
func syncServer(t *testing.T) (*server, *client, net.Conn) {
	t.Helper()
	s, cl, conn, _, _ := rpcServer(t, "main")
	return s, cl, conn
}

// TestSecretSyncRegistersAndIngests proves one run registers the secret
// blocks, ingests the file lines, and answers with the counts and the
// warning shape. The value itself never appears anywhere in the reply.
func TestSecretSyncRegistersAndIngests(t *testing.T) {
	s, cl, conn := syncServer(t)
	dir := t.TempDir()
	path := writeMapping(t, dir, "secret \"gh-cli\" {\n  tool \"op\"\n  ref \"op://Personal/gh/token\"\n}\nfile \"creds.env\"\n")
	dotenv := writeFile(t, dir, "creds.env", "GH_TOKEN=supersecretvalue\nNOVALUE\nEMPTY=\nSHORT=ab\n\n")

	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
	if !m.OK {
		t.Fatalf("secret sync failed: %s", m.Error)
	}
	var res syncReply
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if len(res.Registered) != 1 || res.Registered[0] != "gh-cli" || len(res.Kept) != 0 {
		t.Fatalf("registered %v, kept %v", res.Registered, res.Kept)
	}
	if len(res.Files) != 1 {
		t.Fatalf("files %v", res.Files)
	}
	f := res.Files[0]
	if f.Path != filepath.Join(dir, "creds.env") || f.Values != 1 || f.Skipped != 1 || f.Empty != 1 || f.Bounds != 1 {
		t.Fatalf("file result %+v, want 1 value, 1 skipped, 1 empty, 1 bounds from %s", f, dotenv)
	}
	// The secret is registered with the shape the mapping carried.
	sec := s.st.Secret("gh-cli")
	if sec == nil || sec.Tool != "op" || sec.Reference != "op://Personal/gh/token" {
		t.Fatalf("registered secret %+v", sec)
	}
	// The donated value is active under its derived name.
	name := filepath.Join(dir, "creds.env") + "#GH_TOKEN"
	if v, ok := s.secrets.Get(name); !ok || v != "supersecretvalue" {
		t.Fatalf("registry holds %q=%v", name, v)
	}
	if names := s.secretFiles[filepath.Join(dir, "creds.env")]; len(names) != 1 || names[0] != name {
		t.Fatalf("secretFiles %v", s.secretFiles)
	}
	// The warnings name the skipped and empty lines, the short value and
	// the bounds; none of them carries a value.
	if !strings.Contains(res.Warning, "1 lines without =") || !strings.Contains(res.Warning, "1 empty values") {
		t.Fatalf("warning %q", res.Warning)
	}
	if !strings.Contains(res.Warning, "secret "+filepath.Join(dir, "creds.env")+"#SHORT: value is 2 bytes") {
		t.Fatalf("warning %q names no bounds line", res.Warning)
	}
	if strings.Contains(string(m.Res), "supersecretvalue") || strings.Contains(res.Warning, "supersecretvalue") {
		t.Fatalf("reply leaks the value: %s", m.Res)
	}
}

// TestSecretSyncIsIdempotent proves a re-run keeps the already-registered
// secrets and replaces exactly the values the files donated before.
func TestSecretSyncIsIdempotent(t *testing.T) {
	s, cl, conn := syncServer(t)
	dir := t.TempDir()
	path := writeMapping(t, dir, "secret \"gh-cli\" {\n  tool \"op\"\n  ref \"op://Personal/gh/token\"\n}\nfile \"creds.env\"\n")
	writeFile(t, dir, "creds.env", "GH_TOKEN=supersecretvalue\nOTHER=anothervalue\n")

	if m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path}); !m.OK {
		t.Fatalf("first sync: %s", m.Error)
	}
	// A second run with one line dropped stops redacting exactly that one.
	writeFile(t, dir, "creds.env", "GH_TOKEN=supersecretvalue\n")
	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
	if !m.OK {
		t.Fatalf("second sync: %s", m.Error)
	}
	var res syncReply
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if len(res.Registered) != 0 || len(res.Kept) != 1 || res.Kept[0] != "gh-cli" {
		t.Fatalf("registered %v, kept %v", res.Registered, res.Kept)
	}
	name := filepath.Join(dir, "creds.env")
	if got := s.secretFiles[name]; len(got) != 1 || got[0] != name+"#GH_TOKEN" {
		t.Fatalf("secretFiles %v, want only GH_TOKEN", got)
	}
	if _, ok := s.secrets.Get(name + "#OTHER"); ok {
		t.Fatal("the dropped line still redacts")
	}
}

// TestSecretSyncRefusesBadShapes proves a bad block costs nothing: the
// mapping is refused whole and nothing registers.
func TestSecretSyncRefusesBadShapes(t *testing.T) {
	s, cl, conn := syncServer(t)
	path := writeMapping(t, t.TempDir(), "secret \"gh-cli\" {\n  tool \"vault\"\n  ref \"r\"\n}\n")
	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
	if m.OK {
		t.Fatal("secret sync accepted an unknown tool")
	}
	if !strings.Contains(m.Error, "tool") {
		t.Fatalf("error %q names nothing useful", m.Error)
	}
	if s.st.Secret("gh-cli") != nil {
		t.Fatal("the refused block registered")
	}
}

// TestSecretSyncRefusesAMissingMapping proves a path that does not exist
// answers an error, not a silent empty run.
func TestSecretSyncRefusesAMissingMapping(t *testing.T) {
	s, cl, conn := syncServer(t)
	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: "/nonexistent/.dm-secrets.kdl"})
	if m.OK || !strings.Contains(m.Error, "secret sync") {
		t.Fatalf("reply %v, error %q", m.OK, m.Error)
	}
}

// TestSecretSyncReportsAMissingFile proves a file block whose path does not
// exist counts as a warning and registers nothing from it.
func TestSecretSyncReportsAMissingFile(t *testing.T) {
	s, cl, conn := syncServer(t)
	dir := t.TempDir()
	path := writeMapping(t, dir, "file \"gone.env\"\n")
	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
	if !m.OK {
		t.Fatalf("secret sync failed: %s", m.Error)
	}
	var res syncReply
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if !strings.Contains(res.Warning, "file "+filepath.Join(dir, "gone.env")) {
		t.Fatalf("warning %q", res.Warning)
	}
	if len(s.secretFiles) != 0 {
		t.Fatalf("secretFiles %v", s.secretFiles)
	}
}

// TestSecretSyncRegistersTheRegisterShape proves a mapping block the
// register verb would refuse for its proxy shape refuses here too.
func TestSecretSyncRegistersTheRegisterShape(t *testing.T) {
	s, cl, conn := syncServer(t)
	path := writeMapping(t, t.TempDir(), "secret \"gh-cli\" {\n  tool \"op\"\n  ref \"op://x/y\"\n  upstream \"https://api.github.com\"\n}\n")
	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
	if m.OK || !strings.Contains(m.Error, "go together") {
		t.Fatalf("reply %v, error %q", m.OK, m.Error)
	}
}
