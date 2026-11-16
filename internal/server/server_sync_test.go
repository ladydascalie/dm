package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"dm/internal/pane"
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
file {
    path ".env"
}
file {
    path "sub/creds.env"
}
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
		{"unknown node", "file {\n    path \".env\"\n}\nsecret2 \"x\" {}", "unknown node"},
		{"secret without name", "secret {}", "exactly one quoted name"},
		{"secret with two names", "secret \"a\" \"b\" {}", "exactly one quoted name"},
		{"duplicate secret", "secret \"a\" {}\nsecret \"a\" {}", "appears twice"},
		{"duplicate file", "file {\n    path \"a\"\n}\nfile {\n    path \"a\"\n}", "appears twice"},
		{"unknown child", "secret \"a\" { colour \"red\" }", "unknown colour"},
		{"child without value", "secret \"a\" { tool }", "takes exactly one quoted value"},
		{"file with extra children", "file {\n    path \"a\"\n    tool \"op\"\n}", "file takes exactly one path child"},
		{"file with two paths", "file {\n    path \"a\" \"b\"\n}", "file takes exactly one path child"},
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
	path := writeMapping(t, dir, "secret \"gh-cli\" {\n  tool \"op\"\n  ref \"op://Personal/gh/token\"\n}\nfile {\n    path \"creds.env\"\n}\n")
	dotenv := writeFile(t, dir, "creds.env", "GH_TOKEN=supersecretvalue\nNOVALUE\nEMPTY=\nSHORT=ab\n\n")

	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path, Trust: true})
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
	path := writeMapping(t, dir, "secret \"gh-cli\" {\n  tool \"op\"\n  ref \"op://Personal/gh/token\"\n}\nfile {\n    path \"creds.env\"\n}\n")
	writeFile(t, dir, "creds.env", "GH_TOKEN=supersecretvalue\nOTHER=anothervalue\n")

	if m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path, Trust: true}); !m.OK {
		t.Fatalf("first sync: %s", m.Error)
	}
	// A second run with one line dropped stops redacting exactly that one.
	writeFile(t, dir, "creds.env", "GH_TOKEN=supersecretvalue\n")
	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path, Trust: true})
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
	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path, Trust: true})
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
	path := writeMapping(t, dir, "file {\n    path \"gone.env\"\n}\n")
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
	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path, Trust: true})
	if m.OK || !strings.Contains(m.Error, "go together") {
		t.Fatalf("reply %v, error %q", m.OK, m.Error)
	}
}

// TestParseSecretsMappingRefusesABareIdentAfterAMultilineString proves the
// review's counter-example is refused: a multiline string ends mid-line, so
// an identifier after it is a bare value, not the next node. A line
// comparison would miss it, because kdl.Scan stamps the string with its
// opening line.
func TestParseSecretsMappingRefusesABareIdentAfterAMultilineString(t *testing.T) {
	src := "secret \"api\" {\n\tref \"op://v\nw\" tool \"op\"\n}\n"
	_, err := parseSecretsMapping(src)
	if err == nil {
		t.Fatal("parse accepted a bare ident after a multiline string")
	}
	if !strings.Contains(err.Error(), "quoted values only") {
		t.Fatalf("error %q, want the bare-value refusal", err)
	}
}

// TestParseSecretsMappingKeepsAChildAfterAMultilineString proves the same
// shape with the child on its own line still parses: the multiline
// reference is the value and tool is the next child.
func TestParseSecretsMappingKeepsAChildAfterAMultilineString(t *testing.T) {
	src := "secret \"api\" {\n\tref \"op://v\nw\"\n\ttool \"op\"\n}\n"
	mp, err := parseSecretsMapping(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(mp.Secrets) != 1 || mp.Secrets[0].Ref != "op://v\nw" || mp.Secrets[0].Tool != "op" {
		t.Fatalf("parsed %+v", mp.Secrets)
	}
}

// TestSecretSyncCountsCollisionsAndEmptyKeys proves the review's
// counter-example is counted and warned, never silent: a repeated key keeps
// its last donation and is named, an empty key registers nothing and is
// counted, and no dropped value reaches the reply.
func TestSecretSyncCountsCollisionsAndEmptyKeys(t *testing.T) {
	s, cl, conn := syncServer(t)
	dir := t.TempDir()
	path := writeMapping(t, dir, "file {\n    path \"creds.env\"\n}\n")
	writeFile(t, dir, "creds.env", "=tok1\n=tok2\nDUP=firstvalue\nDUP=secondvalue\n")

	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
	if !m.OK {
		t.Fatalf("secret sync failed: %s", m.Error)
	}
	var res syncReply
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("files %v", res.Files)
	}
	f := res.Files[0]
	if f.Values != 1 || f.EmptyKey != 2 || f.Bounds != 0 || len(f.Duplicate) != 1 || f.Duplicate[0] != "DUP" {
		t.Fatalf("file result %+v, want 1 value, 2 empty keys, DUP duplicated", f)
	}
	if !strings.Contains(res.Warning, "2 empty keys") {
		t.Fatalf("warning %q names no empty keys", res.Warning)
	}
	if !strings.Contains(res.Warning, "key DUP repeats; only its last value redacts") {
		t.Fatalf("warning %q names no collision", res.Warning)
	}
	name := filepath.Join(dir, "creds.env")
	if v, ok := s.secrets.Get(name + "#DUP"); !ok || v != "secondvalue" {
		t.Fatalf("registry holds %q=%v", name+"#DUP", v)
	}
	if _, ok := s.secrets.Get(name + "#"); ok {
		t.Fatal("an empty key registered under the degenerate name")
	}
	for _, leak := range []string{"tok1", "tok2", "firstvalue", "secondvalue"} {
		if strings.Contains(string(m.Res), leak) {
			t.Fatalf("reply leaks %q: %s", leak, m.Res)
		}
	}
}

// TestSecretSyncRefusesAnUntrustedSecretBlock proves the trust hole is
// closed: a mapping the client did not mark trusted may not pin a secret,
// and the refusal costs the whole mapping, so nothing registers and nothing
// is ingested.
func TestSecretSyncRefusesAnUntrustedSecretBlock(t *testing.T) {
	s, cl, conn := syncServer(t)
	dir := t.TempDir()
	path := writeMapping(t, dir, "secret \"gh-cli\" {\n  tool \"op\"\n  ref \"op://Personal/gh/token\"\n  upstream \"http://127.0.0.1:47811\"\n  header \"Authorization: Bearer {}\"\n}\nfile {\n    path \"creds.env\"\n}\n")
	writeFile(t, dir, "creds.env", "GH_TOKEN=supersecretvalue\n")

	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
	if m.OK {
		t.Fatal("an untrusted mapping registered a secret block")
	}
	for _, want := range []string{path, "$XDG_CONFIG_HOME/dm", "--trust"} {
		if !strings.Contains(m.Error, want) {
			t.Fatalf("error %q does not name %q", m.Error, want)
		}
	}
	if s.st.Secret("gh-cli") != nil {
		t.Fatal("the refused mapping registered a secret")
	}
	if len(s.secretFiles) != 0 {
		t.Fatalf("the refused mapping ingested files: %v", s.secretFiles)
	}
	if s.secrets != nil {
		if _, ok := s.secrets.Get(filepath.Join(dir, "creds.env") + "#GH_TOKEN"); ok {
			t.Fatal("the refused mapping activated a file value")
		}
	}
	if strings.Contains(string(m.Res), "supersecretvalue") || strings.Contains(m.Error, "supersecretvalue") {
		t.Fatal("the refusal echoed a value")
	}
}

// TestSecretSyncTrustsAFlaggedMapping proves --trust restores today's shape:
// the same repo mapping registers its secret and ingests its file block.
func TestSecretSyncTrustsAFlaggedMapping(t *testing.T) {
	s, cl, conn := syncServer(t)
	dir := t.TempDir()
	path := writeMapping(t, dir, "secret \"gh-cli\" {\n  tool \"op\"\n  ref \"op://Personal/gh/token\"\n}\nfile {\n    path \"creds.env\"\n}\n")
	writeFile(t, dir, "creds.env", "GH_TOKEN=supersecretvalue\n")

	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path, Trust: true})
	if !m.OK {
		t.Fatalf("trusted sync failed: %s", m.Error)
	}
	if s.st.Secret("gh-cli") == nil {
		t.Fatal("the trusted mapping registered nothing")
	}
	if v, ok := s.secrets.Get(filepath.Join(dir, "creds.env") + "#GH_TOKEN"); !ok || v != "supersecretvalue" {
		t.Fatalf("the trusted mapping ingested %q=%v", filepath.Join(dir, "creds.env")+"#GH_TOKEN", v)
	}
}

// TestSecretSyncUntrustedFileBlockStillWorks proves the ordinary repo
// mapping keeps working without --trust: a .env beside the mapping is
// inside the mapping's own directory, so it reads.
func TestSecretSyncUntrustedFileBlockStillWorks(t *testing.T) {
	s, cl, conn := syncServer(t)
	dir := t.TempDir()
	path := writeMapping(t, dir, "file {\n    path \".env\"\n}\n")
	writeFile(t, dir, ".env", "API_TOKEN=supersecretvalue\n")

	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
	if !m.OK {
		t.Fatalf("sync failed: %s", m.Error)
	}
	var res syncReply
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if res.Warning != "" {
		t.Fatalf("warning %q, want none", res.Warning)
	}
	if v, ok := s.secrets.Get(filepath.Join(dir, ".env") + "#API_TOKEN"); !ok || v != "supersecretvalue" {
		t.Fatalf("registry holds %q=%v", filepath.Join(dir, ".env")+"#API_TOKEN", v)
	}
}

// TestSecretSyncConfinesUntrustedFileBlocks proves a file block may not
// leave the mapping's directory: a relative .., an absolute path elsewhere
// and a symlink out are each refused by name, and none registers a value.
func TestSecretSyncConfinesUntrustedFileBlocks(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := writeFile(t, root, "outside.env", "OUTSIDE=outsidevalue\n")
	if err := os.Symlink(outside, filepath.Join(repo, "link.env")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	cases := []struct{ name, block string }{
		{"relative dotdot", "../outside.env"},
		{"absolute", outside},
		{"symlink out", "link.env"},
	}
	for _, tc := range cases {
		s, cl, conn := syncServer(t)
		path := writeMapping(t, repo, "file {\n    path \""+tc.block+"\"\n}\n")
		m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
		if !m.OK {
			t.Fatalf("%s: sync failed: %s", tc.name, m.Error)
		}
		var res syncReply
		if err := json.Unmarshal(m.Res, &res); err != nil {
			t.Fatalf("%s: decode reply: %v", tc.name, err)
		}
		if len(res.Files) != 1 || !strings.Contains(res.Files[0].Error, "outside the mapping directory") {
			t.Fatalf("%s: file result %+v, want an outside-directory refusal", tc.name, res.Files)
		}
		if !strings.Contains(res.Warning, "outside.env") && !strings.Contains(res.Warning, "link.env") {
			t.Fatalf("%s: warning %q names no block", tc.name, res.Warning)
		}
		if len(s.secretFiles) != 0 {
			t.Fatalf("%s: the refused block ingested %v", tc.name, s.secretFiles)
		}
	}
}

// TestSecretSyncRefusesNonRegularFileBlocks proves a directory and a FIFO
// are refused by name before any read, so neither stalls the sync.
func TestSecretSyncRefusesNonRegularFileBlocks(t *testing.T) {
	cases := []struct{ name, block string }{
		{"directory", "dir"},
		{"fifo", "pipe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, cl, conn := syncServer(t)
			dir := t.TempDir()
			if tc.name == "directory" {
				if err := os.MkdirAll(filepath.Join(dir, "dir"), 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
				t.Skipf("mkfifo: %v", err)
			}
			path := writeMapping(t, dir, "file {\n    path \""+tc.block+"\"\n}\n")
			m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
			if !m.OK {
				t.Fatalf("sync failed: %s", m.Error)
			}
			var res syncReply
			if err := json.Unmarshal(m.Res, &res); err != nil {
				t.Fatalf("decode reply: %v", err)
			}
			if len(res.Files) != 1 || !strings.Contains(res.Files[0].Error, "is not a regular file") {
				t.Fatalf("file result %+v, want a non-regular refusal", res.Files)
			}
			if !strings.Contains(res.Warning, tc.block) {
				t.Fatalf("warning %q names no block", res.Warning)
			}
			if len(s.secretFiles) != 0 {
				t.Fatalf("the refused block ingested %v", s.secretFiles)
			}
		})
	}
}

// TestSecretSyncRefusesAnOversizeFileBlock proves a regular file past the
// 1 MiB cap is refused by name instead of read into the server.
func TestSecretSyncRefusesAnOversizeFileBlock(t *testing.T) {
	s, cl, conn := syncServer(t)
	dir := t.TempDir()
	path := writeMapping(t, dir, "file {\n    path \"big.env\"\n}\n")
	writeFile(t, dir, "big.env", "BIG="+strings.Repeat("x", 1<<20)+"\n")

	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
	if !m.OK {
		t.Fatalf("sync failed: %s", m.Error)
	}
	var res syncReply
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if len(res.Files) != 1 || !strings.Contains(res.Files[0].Error, "the limit is") {
		t.Fatalf("file result %+v, want an oversize refusal", res.Files)
	}
	if !strings.Contains(res.Warning, "big.env") {
		t.Fatalf("warning %q names no block", res.Warning)
	}
	if len(s.secretFiles) != 0 {
		t.Fatalf("the oversize block ingested %v", s.secretFiles)
	}
}

// TestSecretSyncRefusesANonRegularMapping proves a mapping path that names
// a device through a symlink is refused before the read, so the sync cannot
// be made to grow the server's heap.
func TestSecretSyncRefusesANonRegularMapping(t *testing.T) {
	s, cl, conn := syncServer(t)
	dir := t.TempDir()
	if err := os.Symlink("/dev/zero", filepath.Join(dir, ".dm-secrets.kdl")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: filepath.Join(dir, ".dm-secrets.kdl")})
	if m.OK || !strings.Contains(m.Error, "not a regular file") {
		t.Fatalf("reply %v, error %q", m.OK, m.Error)
	}
}

// TestSecretSyncFileValuesNeverEvictARegisteredSecret proves the eviction the
// review proved is closed: a mapping with more file values than the panes'
// list can hold keeps every registered secret first, fills only the room they
// leave, and reports the overflow as a count.
func TestSecretSyncFileValuesNeverEvictARegisteredSecret(t *testing.T) {
	s, cl, conn := syncServer(t)
	dir := t.TempDir()
	path := writeMapping(t, dir, "file {\n    path \"creds.env\"\n}\n")
	var env strings.Builder
	const donated = pane.MaxSecrets + 44
	for i := 0; i < donated; i++ {
		fmt.Fprintf(&env, "K%03d=value-%03d\n", i, i)
	}
	writeFile(t, dir, "creds.env", env.String())

	// Registered secrets sorts after every file name, which starts with its
	// absolute path: a single name sort would put all 300 file values first.
	const registered = 5
	s.mu.Lock()
	s.secretEnsure()
	for i := 0; i < registered; i++ {
		name := fmt.Sprintf("zzz-live-%d", i)
		s.secrets.Put(name, "live-value-"+name)
		s.secretActive[name] = true
	}
	s.mu.Unlock()
	ch := spyPane(t, s, "w1p1")

	m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path})
	if !m.OK {
		t.Fatalf("secret sync: %s", m.Error)
	}
	var res syncReply
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	wantDropped := donated - (pane.MaxSecrets - registered)
	if want := fmt.Sprintf("%d file values did not fit", wantDropped); !strings.Contains(res.Warning, want) {
		t.Fatalf("warning %q, want it to name %q", res.Warning, want)
	}
	for _, key := range []string{"K000", "K299"} {
		if strings.Contains(res.Warning, key) {
			t.Fatalf("warning %q names the file key %q", res.Warning, key)
		}
	}

	pushed := func() []string {
		t.Helper()
		got := readSpyUntil(t, ch, "set-secrets")
		var payload struct {
			Values []string `json:"values"`
		}
		if err := json.Unmarshal(got.Res, &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Values
	}
	first := pushed()
	if len(first) != pane.MaxSecrets {
		t.Fatalf("the push carried %d values, want %d", len(first), pane.MaxSecrets)
	}
	for i := 0; i < registered; i++ {
		want := fmt.Sprintf("live-value-zzz-live-%d", i)
		if first[i] != want {
			t.Fatalf("push value %d = %q, want the registered %q", i, first[i], want)
		}
	}
	for i := registered; i < len(first); i++ {
		if !strings.HasPrefix(first[i], "value-") {
			t.Fatalf("push value %d = %q, want a file donation after the registered secrets", i, first[i])
		}
	}

	// A re-run registers the same subset: the selection depends on the
	// names, not on the order the mapping was applied in.
	if m := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-sync", Path: path}); !m.OK {
		t.Fatalf("second sync: %s", m.Error)
	}
	if second := pushed(); !slices.Equal(first, second) {
		t.Fatal("the second sync changed which file values fit")
	}
}
