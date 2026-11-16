package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dm/internal/session"
	"dm/internal/wire"
)

// secretUpstream is a plain upstream the proxy pins: it records the paths
// and Authorization headers it saw and answers a fixed body.
type secretUpstream struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
	auths []string
}

func newSecretUpstream(t *testing.T) *secretUpstream {
	t.Helper()
	u := &secretUpstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.paths = append(u.paths, r.URL.Path)
		u.auths = append(u.auths, r.Header.Get("Authorization"))
		u.mu.Unlock()
		fmt.Fprint(w, "upstream-body")
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *secretUpstream) seen() (paths, auths []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.paths, u.auths
}

// secretServer is a queue fixture with the fetch stubbed and one pane
// spied on.
func secretServer(t *testing.T, name string, value string, err error) (*server, *client, net.Conn, chan wire.Msg) {
	t.Helper()
	s, cl, conn, _, _ := rpcServer(t, name)
	s.fetch = func(sec *session.Secret) (string, error) { return value, err }
	ch := spyPane(t, s, "w1p1")
	return s, cl, conn, ch
}

// secretCall sends one secret action through handleReq and reads the reply.
func secretCall(t *testing.T, s *server, cl *client, conn net.Conn, m wire.Msg) wire.Msg {
	t.Helper()
	s.handleReq(cl, m)
	return rpcRead(t, conn, m)
}

// rpcRead takes the reply one handleReq call produced.
func rpcRead(t *testing.T, conn net.Conn, m wire.Msg) wire.Msg {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("%s reply: %v", m.Action, err)
	}
	return got
}

// secretGet issues one GET and returns the response with the body read.
func secretGet(t *testing.T, url string) (int, string) {
	t.Helper()
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, string(b)
}

func TestSecretRegisterListsAndRevokes(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)

	rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh",
		Tool: "op", Ref: "op://Personal/gh/token",
	})
	if !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	sec := s.st.Secret("gh")
	if sec == nil || sec.Tool != "op" || sec.Reference != "op://Personal/gh/token" {
		t.Fatalf("state lost the secret: %+v", sec)
	}
	if strings.Contains(string(rep.Res), "value") {
		t.Fatalf("register reply carries value vocabulary: %s", rep.Res)
	}

	// The state file on disk carries the registration, never a value.
	b, err := os.ReadFile(filepath.Join(s.st.SessionDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"secrets"`) || strings.Contains(strings.ToLower(string(b)), `"value"`) {
		t.Fatalf("state file wrong: %s", b)
	}

	// A duplicate is refused.
	rep = secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	})
	wantErr(t, "secret-register", "already registered", rep)

	// The list prints names and references only.
	rep = secretCall(t, s, cl, conn, wire.Msg{Action: "secret-list"})
	if !rep.OK {
		t.Fatalf("list: %s", rep.Error)
	}
	if strings.Contains(string(rep.Res), "upstream") || strings.Contains(string(rep.Res), "header") {
		t.Fatalf("list reply carries more than names and references: %s", rep.Res)
	}
	var rows struct {
		Secrets []secretRow `json:"secrets"`
	}
	if err := json.Unmarshal(rep.Res, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows.Secrets) != 1 || rows.Secrets[0].Name != "gh" || rows.Secrets[0].Reference != "op://Personal/gh/token" {
		t.Fatalf("list rows = %+v", rows.Secrets)
	}

	// Revoke removes the entry and answers.
	rep = secretCall(t, s, cl, conn, wire.Msg{Action: "secret-revoke", Name: "gh"})
	if !rep.OK {
		t.Fatalf("revoke: %s", rep.Error)
	}
	if s.st.Secret("gh") != nil {
		t.Fatal("revoke left the secret in state")
	}
	rep = secretCall(t, s, cl, conn, wire.Msg{Action: "secret-revoke", Name: "gh"})
	wantErr(t, "secret-revoke", "no secret", rep)
}

func TestSecretRegisterRefusesBadShapes(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	cases := []wire.Msg{
		{Action: "secret-register", Name: "x", Tool: "vault", Ref: "r"},
		{Action: "secret-register", Name: "x", Tool: "op", Ref: "not-an-op-uri"},
		{Action: "secret-register", Name: " ", Tool: "op", Ref: "op://x/y"},
		{Action: "secret-register", Name: "x", Tool: "op", Ref: "op://x/y", Header: "Bearer {}"},
	}
	for _, m := range cases {
		rep := secretCall(t, s, cl, conn, m)
		if rep.OK {
			t.Fatalf("%s accepted %+v", m.Action, m)
		}
	}
	if len(s.st.Secrets) != 0 {
		t.Fatalf("bad shapes left entries: %+v", s.st.Secrets)
	}
}

func TestSecretUseRedactsPanesAndServesProxy(t *testing.T) {
	value := "s3cret-value-9f2"
	up := newSecretUpstream(t)
	s, cl, conn, ch := secretServer(t, "secrets", value, nil)
	m := wire.Msg{
		Action: "secret-register", Name: "gh",
		Tool: "op", Ref: "op://Personal/gh/token",
		Upstream: up.URL, Header: "Authorization: Bearer {}",
	}
	if rep := secretCall(t, s, cl, conn, m); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}

	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"})
	if !rep.OK {
		t.Fatalf("use: %s", rep.Error)
	}
	// The reply carries the endpoint alone; the value never reaches stdout
	// because this is all the verb prints.
	var res struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(rep.Res, &res); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rep.Res), value) || strings.Contains(res.Endpoint, value) {
		t.Fatalf("use reply leaks the value: %s", rep.Res)
	}
	if !strings.HasPrefix(res.Endpoint, "http://127.0.0.1:") {
		t.Fatalf("endpoint = %q", res.Endpoint)
	}

	// Every pane heard the redaction set.
	got := readSpyUntil(t, ch, "set-secrets")
	var payload struct {
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(got.Res, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Values) != 1 || payload.Values[0] != value {
		t.Fatalf("set-secrets values = %v", payload.Values)
	}

	// The endpoint serves the pinned upstream with the filled header.
	code, body := secretGet(t, res.Endpoint+"x")
	if code != 200 || body != "upstream-body" {
		t.Fatalf("endpoint gave %d %q", code, body)
	}
	paths, auths := up.seen()
	if len(paths) != 1 || paths[0] != "/x" || auths[0] != "Bearer "+value {
		t.Fatalf("upstream saw paths=%v auths=%v", paths, auths)
	}

	// A second use rotates the prefix and the old one dies.
	rep = secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"})
	if !rep.OK {
		t.Fatalf("second use: %s", rep.Error)
	}
	var res2 struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(rep.Res, &res2); err != nil {
		t.Fatal(err)
	}
	if res2.Endpoint == res.Endpoint {
		t.Fatal("second use kept the same endpoint")
	}
	readSpyUntil(t, ch, "set-secrets") // the second use's push
	if code, _ := secretGet(t, res.Endpoint+"x"); code != 404 {
		t.Fatalf("old endpoint answered %d, want 404", code)
	}

	// Revoke drops the value everywhere: the panes hear an empty set, the
	// prefix stops serving.
	rep = secretCall(t, s, cl, conn, wire.Msg{Action: "secret-revoke", Name: "gh"})
	if !rep.OK {
		t.Fatalf("revoke: %s", rep.Error)
	}
	got = readSpyUntil(t, ch, "set-secrets")
	payload.Values = nil
	if err := json.Unmarshal(got.Res, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Values) != 0 {
		t.Fatalf("revoke left values on the pane: %v", payload.Values)
	}
	if code, body := secretGet(t, res2.Endpoint+"x"); code != 404 || strings.Contains(body, value) {
		t.Fatalf("revoked endpoint gave %d %q", code, body)
	}
	if v, ok := s.secrets.Get("gh"); ok || v != "" {
		t.Fatalf("revoke left the value in the registry: %q", v)
	}
}

func TestSecretUseWithoutUpstreamActivatesRedactionOnly(t *testing.T) {
	value := "s3cret-no-upstream"
	s, cl, conn, ch := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "gh/token",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"})
	if !rep.OK {
		t.Fatalf("use: %s", rep.Error)
	}
	if strings.Contains(string(rep.Res), value) {
		t.Fatalf("use reply leaks the value: %s", rep.Res)
	}
	got := readSpyUntil(t, ch, "set-secrets")
	if !strings.Contains(string(got.Res), value) {
		t.Fatalf("the pane never heard the value: %s", got.Res)
	}
	if s.proxy != nil {
		t.Fatal("a secret without an upstream started the proxy")
	}
}

func TestSecretUseFetchFailure(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", fmt.Errorf("secrets: op exited 3"))
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"})
	wantErr(t, "secret-use", "op exited 3", rep)
	if len(s.secretActive) != 0 {
		t.Fatal("a failed fetch activated nothing")
	}
}

func TestSecretHydrateWritesOutOfBand(t *testing.T) {
	value := "s3cret-hydrate"
	s, cl, conn, _ := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	path := filepath.Join(t.TempDir(), "deep", "token")
	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-hydrate", Name: "gh", Path: path})
	if !rep.OK {
		t.Fatalf("hydrate: %s", rep.Error)
	}
	if strings.Contains(string(rep.Res), value) {
		t.Fatalf("hydrate reply leaks the value: %s", rep.Res)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != value {
		t.Fatalf("file = %q", b)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v err=%v", st.Mode(), err)
	}
	rep = secretCall(t, s, cl, conn, wire.Msg{Action: "secret-hydrate", Name: "gh"})
	wantErr(t, "secret-hydrate", "--to", rep)
}

func TestSecretTypeTypesThroughWriteTo(t *testing.T) {
	value := "s3cret-typed"
	s, cl, conn, ch := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-type", Name: "gh", Target: "w1p1"})
	if !rep.OK {
		t.Fatalf("type: %s", rep.Error)
	}
	if strings.Contains(string(rep.Res), value) {
		t.Fatalf("type reply leaks the value: %s", rep.Res)
	}
	// The redaction push lands before the keystrokes, so the echo is
	// scrubbed like any other output.
	seenScrub := false
	for {
		select {
		case m := <-ch:
			if m.Action == "set-secrets" {
				seenScrub = true
			}
			if m.Action == "write" {
				if m.Msg != value {
					t.Fatalf("pane got %q", m.Msg)
				}
				if !seenScrub {
					t.Fatal("the keystrokes left before the redaction push")
				}
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the pane never received the write")
		}
	}
}

func TestSecretTypeRefusesUnknownPaneAndName(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "v", nil)
	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-type", Name: "gh", Target: "w1p1"})
	wantErr(t, "secret-type", "no secret", rep)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	rep = secretCall(t, s, cl, conn, wire.Msg{Action: "secret-type", Name: "gh", Target: "nope"})
	wantErr(t, "secret-type", "no pane", rep)
}
