package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"dm/internal/pane"
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

func TestSecretUseLosesToConcurrentRevoke(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	s.fetch = func(*session.Secret) (string, error) {
		close(entered)
		<-release
		return "s3cret-race-value", nil
	}
	done := make(chan error, 1)
	go func() {
		s.handleReq(cl, wire.Msg{Action: "secret-use", Name: "gh"})
		done <- nil
	}()
	<-entered
	// The revoke completes while the fetch is parked, so the activation
	// must find the secret gone.
	if rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-revoke", Name: "gh"}); !rep.OK {
		t.Fatalf("revoke: %s", rep.Error)
	}
	close(release)
	<-done
	// secretCall already took the revoke's reply; the pipe holds the use's.
	rep := rpcRead(t, conn, wire.Msg{Action: "secret-use"})
	wantErr(t, "secret-use", "changed while the value was being fetched", rep)
	if v, ok := s.secrets.Get("gh"); ok || v != "" {
		t.Fatalf("the raced use left a value behind: %q", v)
	}
	s.mu.Lock()
	active, prefixes, proxy := len(s.secretActive), len(s.secretPrefixes), s.proxy
	s.mu.Unlock()
	if active != 0 || prefixes != 0 || proxy != nil {
		t.Fatalf("the raced use left state: active=%d prefixes=%d proxy=%v", active, prefixes, proxy)
	}
}

func TestConcurrentUseAndRevokeLeaveNoOrphans(t *testing.T) {
	value := "s3cret-concurrent"
	up := newSecretUpstream(t)
	s, cl, conn, _ := secretServer(t, "secrets", value, nil)
	s.fetch = func(*session.Secret) (string, error) { return value, nil }
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
		Upstream: up.URL, Header: "Authorization: Bearer {}",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(use bool) {
			defer wg.Done()
			if use {
				s.handleReq(cl, wire.Msg{Action: "secret-use", Name: "gh"})
				return
			}
			s.handleReq(cl, wire.Msg{Action: "secret-revoke", Name: "gh"})
		}(i%2 == 0)
	}
	wg.Wait()
	// The racing calls answered on the first client's pipe; the checks here
	// run on their own, so each reply is the one this call provoked.
	cl2, conn2 := boxConn(t)
	// Whatever the interleaving was, one final revoke leaves nothing live.
	if rep := secretCall(t, s, cl2, conn2, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
		Upstream: up.URL, Header: "Authorization: Bearer {}",
	}); !rep.OK {
		t.Fatalf("re-register: %s", rep.Error)
	}
	if rep := secretCall(t, s, cl2, conn2, wire.Msg{Action: "secret-use", Name: "gh"}); !rep.OK {
		t.Fatalf("final use: %s", rep.Error)
	}
	if rep := secretCall(t, s, cl2, conn2, wire.Msg{Action: "secret-revoke", Name: "gh"}); !rep.OK {
		t.Fatalf("final revoke: %s", rep.Error)
	}
	s.mu.Lock()
	active, prefixes := len(s.secretActive), len(s.secretPrefixes)
	p := s.proxy
	s.mu.Unlock()
	if active != 0 || prefixes != 0 {
		t.Fatalf("orphans after revoke: active=%d prefixes=%d", active, prefixes)
	}
	// Every prefix the proxy ever minted is dead: a request anywhere under
	// it answers 404, and the upstream saw nothing since the final use.
	code, body := secretGet(t, "http://"+p.Addr()+"/x")
	if code != 404 || body != "" {
		t.Fatalf("unknown path gave %d %q", code, body)
	}
}

func TestSecretTypeRefusesWhenThePaneMissedTheSet(t *testing.T) {
	value := "s3cret-missed-set"
	s, cl, conn, _ := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	// A pane whose socket is already dead: the push fails, so the type must
	// refuse rather than echo the value unredacted.
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	_ = srv.Close()
	s.mu.Lock()
	s.panes["w1p1"] = &paneConn{id: "w1p1", c: srv}
	s.mu.Unlock()
	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-type", Name: "gh", Target: "w1p1"})
	wantErr(t, "secret-type", "did not accept the redaction set", rep)
	if strings.Contains(rep.Error, value) {
		t.Fatalf("the refusal leaks the value: %s", rep.Error)
	}
}

func TestSecretUseWarnsWhenThePanesCannotRedact(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	for _, c := range []struct {
		name  string
		value string
		want  string
	}{
		{"pin", "123", "under 4 bytes"},
		{"long", strings.Repeat("x", 5000), "over 4096 bytes"},
	} {
		if rep := secretCall(t, s, cl, conn, wire.Msg{
			Action: "secret-register", Name: c.name, Tool: "pass", Ref: "dev/" + c.name,
		}); !rep.OK {
			t.Fatalf("register %s: %s", c.name, rep.Error)
		}
		s.fetch = func(*session.Secret) (string, error) { return c.value, nil }
		rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: c.name})
		if !rep.OK {
			t.Fatalf("use %s: %s", c.name, rep.Error)
		}
		var res struct {
			Warning string `json:"warning"`
		}
		if err := json.Unmarshal(rep.Res, &res); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Warning, c.want) || !strings.Contains(res.Warning, "secret "+c.name) {
			t.Fatalf("use %s warning = %q, want it to name the secret and %q", c.name, res.Warning, c.want)
		}
		if strings.Contains(res.Warning, c.value) {
			t.Fatalf("the warning leaks the value: %s", res.Warning)
		}
	}
}

func TestSecretRegisterRefusesOneSidedProxyShapes(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	cases := []wire.Msg{
		{Action: "secret-register", Name: "a", Tool: "op", Ref: "op://x/y", Upstream: "https://api.github.com"},
		{Action: "secret-register", Name: "b", Tool: "op", Ref: "op://x/y", Header: "Authorization: Bearer {}"},
		{Action: "secret-register", Name: "c", Tool: "op", Ref: "op://x/y", Upstream: "https://api.github.com", Header: "Bad Name: {}"},
		{Action: "secret-register", Name: "d", Tool: "op", Ref: "op://x/y", Upstream: "http://example.com", Header: "Authorization: {}"},
		{Action: "secret-register", Name: "e", Tool: "op", Ref: "op://x/y", Upstream: "https://api.github.com", Header: "Authorization: {} {}", Env: []string{"PATH=/x"}},
		{Action: "secret-register", Name: "f", Tool: "op", Ref: "op://x/y", Env: []string{"MY_TOKEN=x"}},
		{Action: "secret-register", Name: "g", Tool: "op", Ref: "op://x/y", Env: []string{"NOEQUALS"}},
		{Action: "secret-register", Name: "h", Tool: "op", Ref: "op://x/y", Env: []string{"HOME=/a", "HOME=/b"}},
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
	// A well-shaped env entry registers and reaches the state file.
	rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "ok", Tool: "pass", Ref: "dev/t",
		Env: []string{"PASSWORD_STORE_DIR=/home/b/.pass"},
	})
	if !rep.OK {
		t.Fatalf("register with env: %s", rep.Error)
	}
	sec := s.st.Secret("ok")
	if sec == nil || len(sec.Env) != 1 || sec.Env[0] != "PASSWORD_STORE_DIR=/home/b/.pass" {
		t.Fatalf("state lost the env: %+v", sec)
	}
	tool := secretTool(sec)
	if len(tool.Inherit) == 0 || len(tool.Env) != 1 {
		t.Fatalf("tool carries inherit=%v env=%v", tool.Inherit, tool.Env)
	}
	opSec := &session.Secret{Name: "o", Tool: "op", Reference: "op://x/y"}
	if opTool := secretTool(opSec); !slices.Contains(opTool.Inherit, "OP_SERVICE_ACCOUNT_TOKEN") {
		t.Fatalf("op tool inherits %v", opTool.Inherit)
	}
}

func TestSecretUseNamesThePanesThatMissedTheSet(t *testing.T) {
	value := "s3cret-uncovered"
	s, cl, conn, _ := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	// One live pane and one whose socket is gone.
	spyPane(t, s, "w1p2")
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	_ = srv.Close()
	s.mu.Lock()
	s.panes["w1p3"] = &paneConn{id: "w1p3", c: srv}
	s.mu.Unlock()
	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"})
	if !rep.OK {
		t.Fatalf("use: %s", rep.Error)
	}
	var res struct {
		Uncovered []string `json:"uncovered"`
	}
	if err := json.Unmarshal(rep.Res, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Uncovered) != 1 || res.Uncovered[0] != "w1p3" {
		t.Fatalf("uncovered = %v, want w1p3", res.Uncovered)
	}
}

func TestSecretTypeRefusesWhenASyncedSiblingMissedTheSet(t *testing.T) {
	value := "s3cret-synced"
	s, cl, conn, ch := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	// The window is synced: a write to the target fans out to w1p2. The
	// target's push lands, the sibling's socket is dead.
	s.mu.Lock()
	s.st.ActiveWindow().Sync = true
	s.mu.Unlock()
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	_ = srv.Close()
	s.mu.Lock()
	s.panes["w1p2"] = &paneConn{id: "w1p2", c: srv}
	s.mu.Unlock()
	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-type", Name: "gh", Target: "w1p1"})
	wantErr(t, "secret-type", "did not accept the redaction set", rep)
	if !strings.Contains(rep.Error, "w1p2") {
		t.Fatalf("the refusal does not name the sibling: %s", rep.Error)
	}
	if strings.Contains(rep.Error, value) {
		t.Fatalf("the refusal leaks the value: %s", rep.Error)
	}
	// Nothing was typed anywhere: the set-secrets push is expected, a
	// write is not.
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case m := <-ch:
			if m.Action == "write" {
				t.Fatalf("the target received a write after a refused type")
			}
			continue
		case <-deadline:
		}
		break
	}
}

// adoptSpy adopts one pane through the real adoptPane path over a pipe and
// returns the frames the pane receives.
func adoptSpy(t *testing.T, s *server, id string) chan wire.Msg {
	t.Helper()
	srv, cli := net.Pipe()
	ch := make(chan wire.Msg, 16)
	t.Cleanup(func() { _ = srv.Close(); _ = cli.Close() })
	go func() {
		for {
			m, err := wire.ReadMsg(cli)
			if err != nil {
				return
			}
			ch <- m
		}
	}()
	if err := s.adoptPane(id, srv); err != nil {
		t.Fatalf("adoptPane: %v", err)
	}
	return ch
}

func TestAdoptPushCarriesTheLiveSet(t *testing.T) {
	value := "s3cret-adopt"
	s, cl, conn, _ := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	// Adopt first, activate second: the pane is registered before it can
	// receive anything, so the use's push reaches it.
	ch := adoptSpy(t, s, "w1p2")
	if rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"}); !rep.OK {
		t.Fatalf("use: %s", rep.Error)
	}
	got := readSpyUntil(t, ch, "set-secrets")
	if !strings.Contains(string(got.Res), value) {
		t.Fatalf("the adopted pane never learned the value: %s", got.Res)
	}

	// Activate a second secret, then adopt another pane: its first set
	// carries both values, not the one-value set that was live before.
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "second", Tool: "pass", Ref: "dev/x",
	}); !rep.OK {
		t.Fatalf("register second: %s", rep.Error)
	}
	if rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "second"}); !rep.OK {
		t.Fatalf("use second: %s", rep.Error)
	}
	ch2 := adoptSpy(t, s, "w1p3")
	got = readSpyUntil(t, ch2, "set-secrets")
	var payload struct {
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(got.Res, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Values) != 2 {
		t.Fatalf("the adopted pane got %d values, want both", len(payload.Values))
	}
}

func TestAdoptRacingAUseNeverLandsAStaleSet(t *testing.T) {
	value := "s3cret-adoprace"
	s, cl, conn, ch := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	useStop := make(chan struct{})
	drainStop := make(chan struct{})
	drainDone := make(chan struct{})
	// The pipe has no buffer: a use storm blocks on delivery unless
	// something drains the replies. The drain owns the read side until the
	// use loops are done. The w1p1 spy's channel needs the same, or its
	// reader stalls and the pushes block on its pipe.
	go func() {
		for {
			select {
			case <-ch:
			case <-drainStop:
				return
			}
		}
	}()
	go func() {
		defer close(drainDone)
		for {
			select {
			case <-drainStop:
				return
			default:
			}
			_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			if _, err := wire.ReadMsg(conn); err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					continue
				}
				return
			}
		}
	}()
	var adoptWG, useWG sync.WaitGroup
	for i := 0; i < 4; i++ {
		adoptWG.Add(1)
		go func(n int) {
			defer adoptWG.Done()
			id := fmt.Sprintf("w2p%d", n)
			for {
				select {
				case <-useStop:
					return
				default:
				}
				srv, cli := net.Pipe()
				// The pane side must drain, or a send to the unbuffered
				// pipe blocks the adopt forever.
				go func() {
					for {
						if _, err := wire.ReadMsg(cli); err != nil {
							return
						}
					}
				}()
				_ = s.adoptPane(id, srv)
				// Drop the conn, so the next round adopts again and the
				// storm keeps exercising the registration path.
				s.mu.Lock()
				delete(s.panes, id)
				s.mu.Unlock()
				_ = cli.Close()
				_ = srv.Close()
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		useWG.Add(1)
		go func() {
			defer useWG.Done()
			for {
				select {
				case <-useStop:
					return
				default:
				}
				s.handleReq(cl, wire.Msg{Action: "secret-use", Name: "gh"})
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)
	close(useStop)
	useWG.Wait()
	adoptWG.Wait()
	close(drainStop)
	<-drainDone
	// Whatever the interleaving, the pane that is adopted now holds the
	// full live set, never a stale one.
	ch2 := adoptSpy(t, s, "w2p1")
	got := readSpyUntil(t, ch2, "set-secrets")
	var payload struct {
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(got.Res, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Values) != 1 || payload.Values[0] != value {
		t.Fatalf("the adopted pane holds %v", payload.Values)
	}
}

func TestSecretUseRefusesAReRegisteredRecord(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "dev/a",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	s.fetch = func(*session.Secret) (string, error) {
		close(entered)
		<-release
		return "s3cret-old-value", nil
	}
	done := make(chan error, 1)
	go func() {
		s.handleReq(cl, wire.Msg{Action: "secret-use", Name: "gh"})
		done <- nil
	}()
	<-entered
	// Revoke and re-register under the same name while the fetch holds the
	// old record: the new shape must not serve the old value.
	if rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-revoke", Name: "gh"}); !rep.OK {
		t.Fatalf("revoke: %s", rep.Error)
	}
	up := newSecretUpstream(t)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "dev/b",
		Upstream: up.URL, Header: "Authorization: Bearer {}",
	}); !rep.OK {
		t.Fatalf("re-register: %s", rep.Error)
	}
	close(release)
	<-done
	rep := rpcRead(t, conn, wire.Msg{Action: "secret-use"})
	wantErr(t, "secret-use", "changed while the value was being fetched", rep)
	if v, ok := s.secrets.Get("gh"); ok || v != "" {
		t.Fatalf("the old value stayed in the registry: %q", v)
	}
	s.mu.Lock()
	active, prefixes, proxy := len(s.secretActive), len(s.secretPrefixes), s.proxy
	s.mu.Unlock()
	if active != 0 || prefixes != 0 || proxy != nil {
		t.Fatalf("the mismatched activation left state: active=%d prefixes=%d proxy=%v", active, prefixes, proxy)
	}
	// The re-registered secret still works on a fresh use, with its own
	// value.
	s.fetch = func(*session.Secret) (string, error) { return "s3cret-new-value", nil }
	rep = secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"})
	if !rep.OK {
		t.Fatalf("use after re-register: %s", rep.Error)
	}
}

func TestSecretUseNamesTheSecretsTheCapDropped(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	s.fetch = func(sec *session.Secret) (string, error) {
		return "s3cret-" + sec.Name, nil
	}
	// The fixture's spy would fill its channel long before 257 pushes; the
	// uses run with no pane listening, and a fresh adopt inspects the set.
	s.mu.Lock()
	delete(s.panes, "w1p1")
	s.mu.Unlock()
	for i := 0; i < pane.MaxSecrets+1; i++ {
		name := fmt.Sprintf("s%03d", i)
		if rep := secretCall(t, s, cl, conn, wire.Msg{
			Action: "secret-register", Name: name, Tool: "pass", Ref: "dev/" + name,
		}); !rep.OK {
			t.Fatalf("register %s: %s", name, rep.Error)
		}
		if rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: name}); !rep.OK {
			t.Fatalf("use %s: %s", name, rep.Error)
		}
	}
	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "s256"})
	if !rep.OK {
		t.Fatalf("use: %s", rep.Error)
	}
	var res struct {
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal(rep.Res, &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Warning, "these ride unredacted until some are revoked: s256") {
		t.Fatalf("warning = %q, want it to name the dropped secret", res.Warning)
	}
	if strings.Contains(res.Warning, "s3cret-") {
		t.Fatalf("the warning leaks a value: %q", res.Warning)
	}
	// The push the pane received carried exactly the cap, in name order,
	// and the dropped value is not in it.
	ch := adoptSpy(t, s, "w1p2")
	got := readSpyUntil(t, ch, "set-secrets")
	var payload struct {
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(got.Res, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Values) != pane.MaxSecrets {
		t.Fatalf("the push carried %d values, want %d", len(payload.Values), pane.MaxSecrets)
	}
	for _, v := range payload.Values {
		if v == "s3cret-s256" {
			t.Fatal("the dropped value rode the push")
		}
	}
	for i := 1; i < len(payload.Values); i++ {
		if payload.Values[i] == payload.Values[i-1] {
			t.Fatalf("the push carries a duplicate at %d", i)
		}
	}
}

// TestPaneSendTimesOutOnAStalledPane proves a pane that stops reading fails
// its push within the send timeout and releases the caller, instead of
// holding a push, and every secret action behind it, forever. The conn
// survives the timeout: the pane stays registered and alive, the redaction
// pushes skip it while it is degraded, and a send that completes clears the
// mark.
func TestPaneSendTimesOutOnAStalledPane(t *testing.T) {
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	// Nobody ever reads srv: the first write blocks until the deadline.
	pc := &paneConn{id: "stalled", c: srv, sendTimeout: 100 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- pc.send(wire.Msg{Action: "set-secrets"}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the send to a stalled pane succeeded")
		}
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("the send failed with %v, want a timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the send did not time out")
	}
	if !pc.isDegraded() {
		t.Fatal("the timed-out conn is not marked degraded")
	}
	// The conn is not closed: a write deadline still arms on it.
	if err := srv.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("the timed-out conn was closed: %v", err)
	}
	// The redaction push skips a degraded conn and reports it failed.
	failed := map[string]error{}
	s := &server{panes: map[string]*paneConn{"stalled": pc}, clients: map[*client]bool{}}
	for id, err := range s.pushSecretScrub() {
		failed[id] = err
	}
	if !errors.Is(failed["stalled"], errPaneDegraded) {
		t.Fatalf("the degraded pane failed with %v, want the degraded error", failed["stalled"])
	}
	// The pane drains, the next send completes, and the mark clears.
	go func() {
		for {
			if _, err := wire.ReadMsg(cli); err != nil {
				return
			}
		}
	}()
	if err := pc.send(wire.Msg{Action: "set-secrets"}); err != nil {
		t.Fatalf("send after recovery: %v", err)
	}
	if pc.isDegraded() {
		t.Fatal("a completed send did not clear the degraded mark")
	}
	failed = map[string]error{}
	for id, err := range s.pushSecretScrub() {
		failed[id] = err
	}
	if _, ok := failed["stalled"]; ok {
		t.Fatalf("the recovered pane still failed its push: %v", failed["stalled"])
	}
}

func TestSecretUseSurvivesAStalledPane(t *testing.T) {
	value := "s3cret-stalled"
	s, cl, conn, ch := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	// One stalled pane next to the live spy: the push fails for it within
	// the timeout and the use still answers.
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	s.mu.Lock()
	s.panes["w1p3"] = &paneConn{id: "w1p3", c: srv, sendTimeout: 100 * time.Millisecond}
	s.mu.Unlock()
	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"})
	if !rep.OK {
		t.Fatalf("use: %s", rep.Error)
	}
	var res struct {
		Uncovered []string `json:"uncovered"`
	}
	if err := json.Unmarshal(rep.Res, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Uncovered) != 1 || res.Uncovered[0] != "w1p3" {
		t.Fatalf("uncovered = %v, want the stalled pane", res.Uncovered)
	}
	// The live pane still learned the value, and the stalled conn is alive
	// but degraded: the next use skips it without another timeout.
	got := readSpyUntil(t, ch, "set-secrets")
	if !strings.Contains(string(got.Res), value) {
		t.Fatalf("the live pane never learned the value: %s", got.Res)
	}
	if err := srv.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("the stalled conn was closed: %v", err)
	}
	rep = secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"})
	if !rep.OK {
		t.Fatalf("second use: %s", rep.Error)
	}
	res.Uncovered = nil
	if err := json.Unmarshal(rep.Res, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Uncovered) != 1 || res.Uncovered[0] != "w1p3" {
		t.Fatalf("the degraded pane is not reported: %v", res.Uncovered)
	}
}

// TestSecretUseRefusesARevokedWhileFetchingSecret proves a revoke that lands
// between the activation and the reply reads as a refusal, not as a success
// with an empty endpoint.
func TestSecretUseRefusesARevokedWhileFetchingSecret(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "dev/t",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	s.fetch = func(*session.Secret) (string, error) { return "s3cret-revoked-midflight", nil }
	entered := make(chan struct{})
	s.afterActivate = func() {
		// The revoke lands after the activation, inside the use flow.
		s.handleReq(cl, wire.Msg{Action: "secret-revoke", Name: "gh"})
		close(entered)
	}
	go func() { s.handleReq(cl, wire.Msg{Action: "secret-use", Name: "gh"}) }()
	<-entered
	// Two replies: the revoke's, then the use's refusal.
	rep := rpcRead(t, conn, wire.Msg{Action: "secret-revoke"})
	if !rep.OK {
		t.Fatalf("revoke reply: %s", rep.Error)
	}
	rep = rpcRead(t, conn, wire.Msg{Action: "secret-use"})
	wantErr(t, "secret-use", "revoked while the value was being fetched or pushed", rep)
	if strings.Contains(rep.Error, "s3cret-revoked-midflight") {
		t.Fatalf("the refusal leaks the value: %s", rep.Error)
	}
	if v, ok := s.secrets.Get("gh"); ok || v != "" {
		t.Fatalf("the revoked use left a value behind: %q", v)
	}
}
