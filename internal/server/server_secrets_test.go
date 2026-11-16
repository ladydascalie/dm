package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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

	"dm/internal/box"
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
	// receive anything, so the use's push reaches it. The adopt's own push
	// is the empty set, which is the daemon's release to start its shell; it
	// is not the value's push, so the test steps past it.
	ch := adoptSpy(t, s, "w1p2")
	if m := readSpyUntil(t, ch, "set-secrets"); !strings.Contains(string(m.Res), `"values":[]`) {
		t.Fatalf("the adopt push should be the empty release set: %s", m.Res)
	}
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
// holding a push, and every secret action behind it, forever. The timeout
// tears the framing, so the conn is poisoned and closed: every later send
// refuses at once, nothing further is written into the stream, and the
// watcher's fresh dial is the pane's way back.
func TestPaneSendTimesOutOnAStalledPane(t *testing.T) {
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	// A reader that consumes exactly two of the four header bytes, then
	// stops: the deadline fires mid-frame, the tear the clean tests cannot
	// express.
	torn := make(chan struct{})
	go func() {
		defer close(torn)
		var b [2]byte
		_, _ = io.ReadFull(cli, b[:])
	}()
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
	<-torn
	if !pc.isDegraded() {
		t.Fatal("the torn conn is not marked degraded")
	}
	// The conn is closed: a write deadline no longer arms on it.
	if err := srv.SetWriteDeadline(time.Now().Add(time.Second)); err == nil {
		t.Fatal("the torn conn was not closed")
	}
	// The poisoned conn refuses every later send at once, from any pusher.
	start := time.Now()
	if err := pc.send(wire.Msg{Action: "set-secrets"}); !errors.Is(err, errPaneDegraded) {
		t.Fatalf("send after the tear = %v, want the degraded refusal", err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatalf("the poisoned send took %s, want an instant refusal", time.Since(start))
	}
	s := &server{panes: map[string]*paneConn{"stalled": pc}, clients: map[*client]bool{}}
	failed := map[string]error{}
	for id, err := range s.pushSecretScrub() {
		failed[id] = err
	}
	if !errors.Is(failed["stalled"], errPaneDegraded) {
		t.Fatalf("the poisoned pane failed with %v, want the degraded error", failed["stalled"])
	}
	// The reader's side is closed too: the tear never reaches a pane.
	if _, err := cli.Read(make([]byte, 1)); err == nil {
		t.Fatal("the torn conn still delivers bytes to the peer")
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
	// The live pane still learned the value, and the stalled conn is closed
	// and poisoned: the next use skips it without another timeout, and the
	// watcher's fresh dial is the pane's way back.
	got := readSpyUntil(t, ch, "set-secrets")
	if !strings.Contains(string(got.Res), value) {
		t.Fatalf("the live pane never learned the value: %s", got.Res)
	}
	if err := srv.SetWriteDeadline(time.Now().Add(time.Second)); err == nil {
		t.Fatal("the torn conn was not closed")
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

// fakePaneDaemon is a stand-in pane daemon on a real unix socket: it accepts
// like the daemon does, answers requests, records the redaction sets it
// receives per conn, and can park a conn so a server push backpressures.
type fakePaneDaemon struct {
	ln net.Listener
	mu sync.Mutex
	// seen lists one record per accepted conn, in accept order: the values
	// of the first set-secrets the conn carried, nil when none.
	seen [][]string
	// resets lists every set-secrets beyond the first conn, in arrival
	// order: the re-adoptions the watcher performed.
	resets      [][]string
	acceptClose bool
	parked      chan struct{} // closed to release the parked first conn
	parkAck     chan struct{} // closed when the first conn is parked
}

func startFakePaneDaemon(t *testing.T, st *session.State, id string) *fakePaneDaemon {
	t.Helper()
	sock := st.PaneSock(id)
	_ = os.Remove(sock)
	f := &fakePaneDaemon{parked: make(chan struct{}), parkAck: make(chan struct{})}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// The index is accept order, assigned here: handler goroutines
			// start in any order, and the test addresses conns by index.
			f.mu.Lock()
			idx := len(f.seen)
			f.seen = append(f.seen, nil)
			f.mu.Unlock()
			go f.serve(c, idx)
		}
	}()
	return f
}

func (f *fakePaneDaemon) serve(c net.Conn, idx int) {
	if f.acceptClose {
		// A wedged daemon shape: it accepts and kills every conn, so the
		// watcher's redials never stabilise.
		_ = c.Close()
		return
	}
	defer func() { _ = c.Close() }()
	first := idx == 0
	for {
		m, err := wire.ReadMsg(c)
		if err != nil {
			return
		}
		if m.Type == "req" {
			_ = wire.WriteMsg(c, wire.Msg{Type: "resp", ID: m.ID, OK: true, Cwd: "/tmp"})
			continue
		}
		f.mu.Lock()
		f.mu.Unlock()
		if m.Action == "set-secrets" {
			var p struct {
				Values []string `json:"values"`
			}
			_ = json.Unmarshal(m.Res, &p)
			f.mu.Lock()
			f.seen[idx] = p.Values
			if idx > 0 {
				f.resets = append(f.resets, p.Values)
			}
			f.mu.Unlock()
			// The adopt's empty set is the release, not a value push: a
			// daemon that parked on it would leave the value push unread and
			// hide the tear this test is about.
			if first && len(p.Values) > 0 {
				// Park: stop reading until the test releases, so a large
				// server push backpressures and tears.
				close(f.parkAck)
				<-f.parked
			}
		}
	}
}

func (f *fakePaneDaemon) connValues(idx int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[idx]
}

// resetValues returns the first post-tear set-secrets that carries values,
// or nil while none has arrived.
func (f *fakePaneDaemon) resetValues() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.resets {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}

func (f *fakePaneDaemon) connCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

// TestPaneSurvivesATornFrameAndIsReadopted drives the whole tear chain over a
// real unix socket: a stalled daemon conn, a push that times out mid-frame,
// the poisoned conn closing, and the watcher re-dialing the same daemon. The
// pane stays in the layout, the daemon keeps accepting, and the fresh conn
// carries the secret set again. A real socket death still prunes.
func TestPaneSurvivesATornFrameAndIsReadopted(t *testing.T) {
	value := "s3cret-torn-e2e"
	s, cl, conn, _ := secretServer(t, "secrets", value, nil)
	// w1p2 keeps the fixture's w1p1 spy out of the way.
	id := "w1p2"
	sock := s.st.PaneSock(id)

	// The fixture's screenPane socket is replaced by the fake daemon.
	os.Remove(sock)
	f := startFakePaneDaemon(t, s.st, id)

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial the fake daemon: %v", err)
	}
	if err := s.adoptPane(id, c); err != nil {
		t.Fatalf("adoptPane: %v", err)
	}
	s.watchPane(id)
	s.mu.Lock()
	pc := s.panes[id]
	pc.sendTimeout = 200 * time.Millisecond
	s.mu.Unlock()

	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	if rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"}); !rep.OK {
		t.Fatalf("use: %s", rep.Error)
	}
	select {
	case <-f.parkAck: // the fake conn parked after its first set-secrets
	case <-time.After(2 * time.Second):
		s.mu.Lock()
		ids := []string{}
		for k := range s.panes {
			ids = append(ids, k)
		}
		s.mu.Unlock()
		f.mu.Lock()
		f.mu.Unlock()
		t.Fatalf("the fake conn never received the set-secrets; panes=%v connCount=%d", ids, f.connCount())
	}
	if got := f.connValues(0); len(got) != 1 || got[0] != value {
		t.Fatalf("the daemon's first conn got %v", got)
	}

	// A paste-sized frame into a parked conn: the write tears mid-frame.
	big := wire.Msg{Action: "write", Data: make([]byte, 512<<10)}
	err = pc.send(big)
	var ne net.Error
	if err == nil || !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("the big send = %v, want a timeout", err)
	}
	if !pc.isDegraded() {
		t.Fatal("the torn conn is not poisoned")
	}

	// Release the daemon's conn: it sees the dead stream and drops it. The
	// watcher must re-dial the same daemon and re-adopt a fresh conn.
	close(f.parked)
	deadline := time.Now().Add(5 * time.Second)
	var fresh *paneConn
	for time.Now().Before(deadline) {
		s.mu.Lock()
		cur := s.panes[id]
		s.mu.Unlock()
		if cur != nil && cur != pc {
			fresh = cur
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if fresh == nil {
		t.Fatal("the watcher never re-adopted the pane")
	}
	// The fresh conn carries the secret set again.
	got := []string(nil)
	for i := 0; i < 40 && got == nil; i++ {
		got = f.resetValues()
		time.Sleep(50 * time.Millisecond)
	}
	if len(got) != 1 || got[0] != value {
		t.Fatalf("the fresh conn got %v, want the secret set", got)
	}
	// The pane is still in the layout, and the daemon still accepts.
	s.mu.Lock()
	inTree := s.paneInTreeLocked(id)
	s.mu.Unlock()
	if !inTree {
		t.Fatal("the surviving pane left the layout")
	}
	if f.connCount() < 2 {
		t.Fatal("the daemon stopped accepting")
	}
	if _, err := net.Dial("unix", sock); err != nil {
		t.Fatalf("the daemon refuses connections: %v", err)
	}

	// A real socket death still prunes: the daemon goes away, the watcher
	// finds the socket gone, and the pane leaves the layout.
	if err := f.ln.Close(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sock)
	s.mu.Lock()
	fresh.c.Close()
	s.mu.Unlock()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		inTree := s.paneInTreeLocked(id)
		s.mu.Unlock()
		if !inTree {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.mu.Lock()
	stillThere := s.paneInTreeLocked(id)
	s.mu.Unlock()
	if stillThere {
		t.Fatal("the pane survived its own daemon's death")
	}
}

// wedgeWait polls until the pane's wedge signal has landed in the box and
// returns how many wedge entries name the pane.
func wedgeWait(t *testing.T, s *server, id string, want int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	n := 0
	for time.Now().Before(deadline) {
		s.mu.Lock()
		n = 0
		for _, e := range s.st.Box {
			if e.Key == box.NotifyPrefix+id && e.State == box.StateError {
				n++
			}
		}
		s.mu.Unlock()
		if n >= want {
			return n
		}
		time.Sleep(50 * time.Millisecond)
	}
	return n
}

func TestWatcherWedgesAfterThreeFailedRedials(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	id := "w1p2"
	sock := s.st.PaneSock(id)
	os.Remove(sock)
	f := startFakePaneDaemon(t, s.st, id)
	f.mu.Lock()
	f.acceptClose = true
	f.mu.Unlock()

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial the fake daemon: %v", err)
	}
	if err := s.adoptPane(id, c); err != nil {
		t.Fatalf("adoptPane: %v", err)
	}
	s.watchPane(id)

	// The wedge signal lands once, the pane stays registered and in the
	// layout, and the watcher stops dialing.
	if n := wedgeWait(t, s, id, 1); n != 1 {
		t.Fatalf("wedge entries = %d, want 1", n)
	}
	s.mu.Lock()
	entries := 0
	for _, e := range s.st.Box {
		if e.Key == box.NotifyPrefix+id && e.State == box.StateError {
			entries++
			if !strings.Contains(e.Msg, "pane "+id) {
				t.Fatalf("the wedge message does not name the pane: %q", e.Msg)
			}
		}
	}
	pc := s.panes[id]
	inTree := s.paneInTreeLocked(id)
	s.mu.Unlock()
	if entries != 1 {
		t.Fatalf("wedge entries = %d, want exactly one", entries)
	}
	if pc == nil {
		t.Fatal("the wedged pane left the registry")
	}
	if !inTree {
		t.Fatal("the wedged pane left the layout")
	}
	s.mu.Lock()
	_, wedged := s.paneWatch[id]
	wedgedMark := wedged && s.paneWatch[id].wedged
	s.mu.Unlock()
	if !wedgedMark {
		t.Fatal("the pane is not marked wedged")
	}
	// The loop's fixed point is the stopped watcher: watching is false,
	// and at most one in-flight accept lands after the wedge.
	s.mu.Lock()
	watching := s.paneWatch[id].watching
	s.mu.Unlock()
	if watching {
		t.Fatal("the wedged pane's watcher is still running")
	}
	before := f.connCount()
	time.Sleep(300 * time.Millisecond)
	mid := f.connCount()
	time.Sleep(700 * time.Millisecond)
	if got := f.connCount(); got != mid || got > before+1 {
		t.Fatalf("the watcher kept dialing: %d conns after the wedge (was %d, mid %d)", got, before, mid)
	}
	_ = cl
	_ = conn
}

func TestWedgeRearmsOnASecretAction(t *testing.T) {
	value := "s3cret-rearm"
	s, cl, conn, _ := secretServer(t, "secrets", value, nil)
	id := "w1p2"
	sock := s.st.PaneSock(id)
	os.Remove(sock)
	f := startFakePaneDaemon(t, s.st, id)
	f.mu.Lock()
	f.acceptClose = true
	f.mu.Unlock()

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial the fake daemon: %v", err)
	}
	if err := s.adoptPane(id, c); err != nil {
		t.Fatalf("adoptPane: %v", err)
	}
	s.watchPane(id)
	if n := wedgeWait(t, s, id, 1); n != 1 {
		t.Fatalf("wedge entries = %d, want 1", n)
	}

	// The explicit secret action re-arms the watcher: the push reports the
	// pane, the watcher runs its capped loop again, and the wedge signal
	// fires once more. The pane is never pruned.
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "op", Ref: "op://x/y",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
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
	found := false
	for _, u := range res.Uncovered {
		if u == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("uncovered = %v, want the wedged pane", res.Uncovered)
	}
	if n := wedgeWait(t, s, id, 2); n != 2 {
		t.Fatalf("wedge entries = %d, want 2 after the re-arm", n)
	}
	s.mu.Lock()
	inTree := s.paneInTreeLocked(id)
	s.mu.Unlock()
	if !inTree {
		t.Fatal("the re-armed pane left the layout")
	}
}

// TestRedialCountsOnlyForTheOwner proves two racing redials on one conn
// count once: the loser of the ownership check commits nothing, so the cap
// counts three real redials, not two watchers' double-count.
func TestRedialCountsOnlyForTheOwner(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	id := "w1p2"
	sock := s.st.PaneSock(id)
	os.Remove(sock)
	f := startFakePaneDaemon(t, s.st, id)
	f.mu.Lock()
	f.acceptClose = true
	f.mu.Unlock()

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial the fake daemon: %v", err)
	}
	if err := s.adoptPane(id, c); err != nil {
		t.Fatalf("adoptPane: %v", err)
	}
	s.mu.Lock()
	pc := s.panes[id]
	s.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.redialPane(id, pc)
		}()
	}
	wg.Wait()
	s.mu.Lock()
	failed := s.paneWatch[id].failed
	s.mu.Unlock()
	if failed != 1 {
		t.Fatalf("failed = %d after two racing redials, want 1", failed)
	}
	_ = cl
	_ = conn
}

// TestWatchPaneDedupesOnTheWatchingFlag proves a second watchPane call on a
// pane stacks nothing: one watcher runs, the wedge signal fires once, and the
// wedge leaves no second loop behind.
func TestWatchPaneDedupesOnTheWatchingFlag(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	id := "w1p2"
	sock := s.st.PaneSock(id)
	os.Remove(sock)
	f := startFakePaneDaemon(t, s.st, id)
	f.mu.Lock()
	f.acceptClose = true
	f.mu.Unlock()

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial the fake daemon: %v", err)
	}
	if err := s.adoptPane(id, c); err != nil {
		t.Fatalf("adoptPane: %v", err)
	}
	s.watchPane(id)
	s.watchPane(id) // the second caller must dedupe, not stack

	if n := wedgeWait(t, s, id, 1); n != 1 {
		t.Fatalf("wedge entries = %d, want exactly 1", n)
	}
	s.mu.Lock()
	ws := s.paneWatch[id]
	watching, wedged := ws.watching, ws.wedged
	inTree := s.paneInTreeLocked(id)
	s.mu.Unlock()
	if watching {
		t.Fatal("the watcher kept running after the wedge")
	}
	if !wedged {
		t.Fatal("the pane is not marked wedged")
	}
	if !inTree {
		t.Fatal("the wedged pane left the layout")
	}
	before := f.connCount()
	time.Sleep(300 * time.Millisecond)
	if got := f.connCount(); got > before+1 {
		t.Fatalf("the dedupe failed: %d conns after the wedge, want at most %d+1", got, before)
	}
	_ = cl
	_ = conn
}

// serveSessionsSocket serves a server on its own socket the way Run does: one
// accept loop, one goroutine per conn, each conn speaking the plain request
// loop. A test needs it when one server dials another.
func serveSessionsSocket(t *testing.T, s *server) {
	t.Helper()
	if err := os.MkdirAll(s.st.SessionDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", s.st.ServerSock())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
}

func TestSecretHydrateActivatesAndPushes(t *testing.T) {
	value := "s3cret-hydrate-push"
	s, cl, conn, ch := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "gh/token",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	path := filepath.Join(t.TempDir(), "token")
	rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-hydrate", Name: "gh", Path: path})
	if !rep.OK {
		t.Fatalf("hydrate: %s", rep.Error)
	}
	if strings.Contains(string(rep.Res), value) {
		t.Fatalf("hydrate reply leaks the value: %s", rep.Res)
	}
	// A hydrate-only flow leaves every pane scrubbing: the value never rode
	// an endpoint or a keystroke, and the panes still heard it.
	got := readSpyUntil(t, ch, "set-secrets")
	if !strings.Contains(string(got.Res), value) {
		t.Fatalf("the pane never heard the value: %s", got.Res)
	}
	if !s.secretActive["gh"] {
		t.Fatal("hydrate left the value inactive")
	}
	// The hydrate never starts the proxy: the file is the ride.
	if s.proxy != nil {
		t.Fatal("a hydrate started the proxy for a secret without an upstream")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != value {
		t.Fatalf("file = %q err=%v", b, err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v err=%v", st.Mode(), err)
	}
}

func TestSecretHydrateStartsNoProxyForAnUpstreamSecret(t *testing.T) {
	up := newSecretUpstream(t)
	s, cl, conn, _ := secretServer(t, "secrets", "s3cret-hydrate-up", nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh",
		Tool: "op", Ref: "op://x/y",
		Upstream: up.URL, Header: "Authorization: Bearer {}",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-hydrate", Name: "gh", Path: filepath.Join(t.TempDir(), "token"),
	})
	if !rep.OK {
		t.Fatalf("hydrate: %s", rep.Error)
	}
	if s.proxy != nil {
		t.Fatal("a hydrate started the proxy")
	}
	if strings.Contains(string(rep.Res), "endpoint") {
		t.Fatalf("hydrate reply carries endpoint vocabulary: %s", rep.Res)
	}
}

func TestSecretUseCoversASiblingSession(t *testing.T) {
	value := "s3cret-cross-session"
	root := t.TempDir()
	s1 := railServer(session.NewStateAt(root, "poc"))
	s1.fetch = func(sec *session.Secret) (string, error) { return value, nil }
	s2 := railServer(session.NewStateAt(root, "sibling"))
	ch := spyPane(t, s2, "w1p1")
	serveSessionsSocket(t, s2)

	cl, conn := boxConn(t)
	if rep := secretCall(t, s1, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "gh/token",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	rep := secretCall(t, s1, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"})
	if !rep.OK {
		t.Fatalf("use: %s", rep.Error)
	}
	if strings.Contains(string(rep.Res), value) {
		t.Fatalf("use reply leaks the value: %s", rep.Res)
	}
	var res struct {
		Uncovered []string `json:"uncovered"`
	}
	if err := json.Unmarshal(rep.Res, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Uncovered) != 0 {
		t.Fatalf("use named %v uncovered, want a covered sibling", res.Uncovered)
	}
	// The sibling session's pane received the set: it scrubs the value even
	// though its own server never fetched it.
	got := readSpyUntil(t, ch, "set-secrets")
	if !strings.Contains(string(got.Res), value) {
		t.Fatalf("the sibling pane never heard the value: %s", got.Res)
	}
	// The revoke withdraws: the sibling's panes hear the set is gone.
	rep = secretCall(t, s1, cl, conn, wire.Msg{Action: "secret-revoke", Name: "gh"})
	if !rep.OK {
		t.Fatalf("revoke: %s", rep.Error)
	}
	got = readSpyUntil(t, ch, "set-secrets")
	if strings.Contains(string(got.Res), value) {
		t.Fatalf("the withdrawal left the value on the sibling pane: %s", got.Res)
	}
}

func TestSecretCoverMergesAndWithdraws(t *testing.T) {
	s, cl, conn, ch := secretServer(t, "secrets", "", nil)
	cover := func(session string, values ...string) wire.Msg {
		return wire.Msg{
			Action: "secret-cover",
			Res:    jsonRaw(map[string]any{"session": session, "values": values}),
		}
	}
	if rep := secretCall(t, s, cl, conn, cover("other", "foreign-value")); !rep.OK {
		t.Fatalf("cover: %s", rep.Error)
	}
	got := readSpyUntil(t, ch, "set-secrets")
	if !strings.Contains(string(got.Res), "foreign-value") {
		t.Fatalf("the pane never heard the foreign value: %s", got.Res)
	}
	// A pane adopted later hears the merged set too.
	ch2 := spyPane(t, s, "w1p2")
	s.adoptSecretScrub("w1p2")
	got = readSpyUntil(t, ch2, "set-secrets")
	if !strings.Contains(string(got.Res), "foreign-value") {
		t.Fatalf("the adopted pane never heard the foreign value: %s", got.Res)
	}
	// The foreign value rides memory only: the state file never sees it.
	if b, err := os.ReadFile(filepath.Join(s.st.SessionDir(), "state.json")); err == nil && strings.Contains(string(b), "foreign-value") {
		t.Fatalf("the state file carries a foreign value: %s", b)
	}
	// The withdrawal clears the pane's list.
	if rep := secretCall(t, s, cl, conn, cover("other")); !rep.OK {
		t.Fatalf("withdraw: %s", rep.Error)
	}
	got = readSpyUntil(t, ch, "set-secrets")
	if strings.Contains(string(got.Res), "foreign-value") {
		t.Fatalf("the withdrawal left the value on the pane: %s", got.Res)
	}
	// A session covers everything but itself, and names must be safe.
	wantErr(t, "secret-cover", "refuses session", secretCall(t, s, cl, conn, cover("secrets", "v")))
	wantErr(t, "secret-cover", "refuses session", secretCall(t, s, cl, conn, cover("../x", "v")))
	wantErr(t, "secret-cover", "needs a session", secretCall(t, s, cl, conn, wire.Msg{Action: "secret-cover"}))
}

func TestSecretUseNamesAServerlessSibling(t *testing.T) {
	value := "s3cret-orphan"
	root := t.TempDir()
	s := railServer(session.NewStateAt(root, "poc"))
	s.fetch = func(sec *session.Secret) (string, error) { return value, nil }
	// A sibling whose panes are live and whose server is gone: the push
	// cannot reach those panes, so the use names them.
	orphan := session.NewStateAt(root, "orphan")
	if err := os.MkdirAll(filepath.Join(orphan.SessionDir(), "panes"), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", orphan.PaneSock("w1p1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	// A sibling that is gone altogether: no sockets, no name.
	if _, err := session.NewStateAt(root, "gone"), error(nil); err != nil {
		t.Fatal(err)
	}

	cl, conn := boxConn(t)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "gh/token",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
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
	if len(res.Uncovered) != 1 || res.Uncovered[0] != "session orphan" {
		t.Fatalf("uncovered = %v, want the serverless sibling", res.Uncovered)
	}
}

// gateConn is a conn whose first write blocks until the test opens the gate,
// so a push can be held mid-flight while the test lands a concurrent action.
type gateConn struct {
	ready chan struct{} // closed when the first write starts
	gate  chan struct{} // closed to release the writes
	mu    sync.Mutex
	wrote bool
}

func newGateConn() *gateConn {
	return &gateConn{ready: make(chan struct{}), gate: make(chan struct{})}
}

func (c *gateConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	first := !c.wrote
	c.wrote = true
	c.mu.Unlock()
	if first {
		close(c.ready)
	}
	<-c.gate
	return len(b), nil
}

func (c *gateConn) Close() error                     { return nil }
func (c *gateConn) Read(b []byte) (int, error)       { return 0, io.EOF }
func (c *gateConn) LocalAddr() net.Addr              { return nil }
func (c *gateConn) RemoteAddr() net.Addr             { return nil }
func (c *gateConn) SetDeadline(time.Time) error      { return nil }
func (c *gateConn) SetReadDeadline(time.Time) error  { return nil }
func (c *gateConn) SetWriteDeadline(time.Time) error { return nil }

// TestSecretHydrateRefusesARevokedWhileFetchingSecret proves a revoke that
// lands during the fetch or the push reads as a refusal and never leaves the
// value on disk: the push now includes the sibling covers, so the window is
// not a sliver.
func TestSecretHydrateRefusesARevokedWhileFetchingSecret(t *testing.T) {
	value := "s3cret-hydrate-revoked"
	s, cl, conn, _ := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "gh/token",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	// A second pane whose push blocks until the gate opens: the hydrate is
	// stuck mid-push when the revoke lands.
	gc := newGateConn()
	s.mu.Lock()
	s.panes["w1p2"] = &paneConn{id: "w1p2", c: gc}
	s.mu.Unlock()
	path := filepath.Join(t.TempDir(), "token")
	done := make(chan wire.Msg, 1)
	go func() {
		s.handleReq(cl, wire.Msg{Action: "secret-hydrate", Name: "gh", Path: path})
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		m, err := wire.ReadMsg(conn)
		if err != nil {
			return
		}
		done <- m
	}()
	<-gc.ready
	cl2, _ := boxConn(t)
	go func() { s.handleReq(cl2, wire.Msg{Action: "secret-revoke", Name: "gh"}) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		gone := s.st.Secret("gh") == nil
		s.mu.Unlock()
		if gone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the revoke did not land while the push was in flight")
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(gc.gate)
	rep := <-done
	wantErr(t, "secret-hydrate", "revoked while the value was being fetched or pushed", rep)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the revoked secret left a file behind: %v", err)
	}
}

// TestSecretCoverReportsDroppedValues proves the cover reply counts the
// values the panes' cap dropped, instead of reporting success while the pane
// never holds them.
func TestSecretCoverReportsDroppedValues(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	s.fetch = func(sec *session.Secret) (string, error) { return "s3cret-" + sec.Name, nil }
	// The cap fills with own values; no pane listens during the fill, so
	// the pushes go nowhere and the spy channel stays clean.
	s.mu.Lock()
	delete(s.panes, "w1p1")
	s.mu.Unlock()
	for i := 0; i < pane.MaxSecrets; i++ {
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
	ch := spyPane(t, s, "w1p1")
	cover := func(values ...string) wire.Msg {
		return wire.Msg{
			Action: "secret-cover",
			Res:    jsonRaw(map[string]any{"session": "other", "values": values}),
		}
	}
	rep := secretCall(t, s, cl, conn, cover("foreign-value"))
	if !rep.OK {
		t.Fatalf("cover: %s", rep.Error)
	}
	var res struct {
		Panes   int `json:"panes"`
		Dropped int `json:"dropped"`
	}
	if err := json.Unmarshal(rep.Res, &res); err != nil {
		t.Fatal(err)
	}
	if res.Dropped != 1 {
		t.Fatalf("cover reply dropped = %d, want 1", res.Dropped)
	}
	// The pane holds the cap and not the foreign value.
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
		if v == "foreign-value" {
			t.Fatal("the dropped value rode the push")
		}
	}
	// The withdrawal answers dropped zero again.
	rep = secretCall(t, s, cl, conn, cover())
	if !rep.OK {
		t.Fatalf("withdraw: %s", rep.Error)
	}
	res.Dropped = -1
	if err := json.Unmarshal(rep.Res, &res); err != nil {
		t.Fatal(err)
	}
	if res.Dropped != 0 {
		t.Fatalf("withdraw reply dropped = %d, want 0", res.Dropped)
	}
}

// TestCoverRoundtripNamesDroppedValues proves the sender turns a nonzero
// dropped count into a problem report, and a zero count into silence.
func TestCoverRoundtripNamesDroppedValues(t *testing.T) {
	for _, c := range []struct {
		dropped int
		want    string
	}{
		{0, ""},
		{2, "2 of 5 values dropped"},
	} {
		srv, cli := net.Pipe()
		t.Cleanup(func() { _ = srv.Close(); _ = cli.Close() })
		go func(dropped int) {
			m, err := wire.ReadMsg(srv)
			if err != nil {
				return
			}
			_ = wire.WriteMsg(srv, wire.Msg{Type: "resp", ID: m.ID, OK: true,
				Res: jsonRaw(map[string]any{"panes": 3, "dropped": dropped})})
		}(c.dropped)
		err := coverRoundtrip(cli, "other", []string{"a", "b", "c", "d", "e"})
		if c.want == "" {
			if err != nil {
				t.Fatalf("dropped %d: %v", c.dropped, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("dropped %d: err = %v, want it to name %q", c.dropped, err, c.want)
		}
	}
}

// TestSecretUseWarnsWhenASiblingDropsTheSet proves neither side stays silent
// when a sibling's cap drops the covered values: the cover reply counts them,
// the sender names the sibling in uncovered and in the warning.
func TestSecretUseWarnsWhenASiblingDropsTheSet(t *testing.T) {
	value := "s3cret-cap-drop"
	root := t.TempDir()
	s1 := railServer(session.NewStateAt(root, "poc"))
	s1.fetch = func(sec *session.Secret) (string, error) { return value, nil }
	s2 := railServer(session.NewStateAt(root, "sibling"))
	serveSessionsSocket(t, s2)
	// The sibling's panes' cap is already full of its own values.
	s2.mu.Lock()
	s2.secretEnsure()
	for i := 0; i < pane.MaxSecrets; i++ {
		name := fmt.Sprintf("own-%03d", i)
		s2.secrets.Put(name, "own-"+name)
		s2.secretActive[name] = true
	}
	s2.mu.Unlock()

	cl, conn := boxConn(t)
	if rep := secretCall(t, s1, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "gh/token",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	rep := secretCall(t, s1, cl, conn, wire.Msg{Action: "secret-use", Name: "gh"})
	if !rep.OK {
		t.Fatalf("use: %s", rep.Error)
	}
	var res struct {
		Warning   string   `json:"warning"`
		Uncovered []string `json:"uncovered"`
	}
	if err := json.Unmarshal(rep.Res, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Uncovered) != 1 || res.Uncovered[0] != "session sibling" {
		t.Fatalf("uncovered = %v, want the capped sibling", res.Uncovered)
	}
	if !strings.Contains(res.Warning, "did not reach") || !strings.Contains(res.Warning, "session sibling") {
		t.Fatalf("warning = %q, want it to name the uncovered sibling", res.Warning)
	}
	if strings.Contains(res.Warning, value) {
		t.Fatalf("the warning leaks the value: %s", res.Warning)
	}
}

// TestSecretHydrateRefusesAReRegisteredRecord proves the hydrate guard checks
// the record's identity, not just the name: a revoke-and-re-register between
// the activation and the guard must not land the old value on disk under the
// new record.
func TestSecretHydrateRefusesAReRegisteredRecord(t *testing.T) {
	value := "s3cret-hydrate-reregistered"
	s, cl, conn, _ := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "gh/token",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	gc := newGateConn()
	s.mu.Lock()
	s.panes["w1p2"] = &paneConn{id: "w1p2", c: gc}
	s.mu.Unlock()
	path := filepath.Join(t.TempDir(), "token")
	done := make(chan wire.Msg, 1)
	go func() {
		s.handleReq(cl, wire.Msg{Action: "secret-hydrate", Name: "gh", Path: path})
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		m, err := wire.ReadMsg(conn)
		if err != nil {
			return
		}
		done <- m
	}()
	<-gc.ready
	cl2, _ := boxConn(t)
	go func() { s.handleReq(cl2, wire.Msg{Action: "secret-revoke", Name: "gh"}) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		gone := s.st.Secret("gh") == nil
		s.mu.Unlock()
		if gone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the revoke did not land while the push was in flight")
		}
		time.Sleep(2 * time.Millisecond)
	}
	// A fresh record takes the name before the gate opens: the guard must
	// not mistake it for the record the value was fetched for.
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "gh/other",
	}); !rep.OK {
		t.Fatalf("re-register: %s", rep.Error)
	}
	close(gc.gate)
	rep := <-done
	wantErr(t, "secret-hydrate", "revoked while the value was being fetched or pushed", rep)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the revoked secret left a file behind: %v", err)
	}
}

// TestAdoptPushNamesTheDroppedCoverValues proves an adopt push that drops
// covered values at the cap says so: the pane never holds them, and no reply
// would ever carry the news.
func TestAdoptPushNamesTheDroppedCoverValues(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	s.fetch = func(sec *session.Secret) (string, error) { return "s3cret-" + sec.Name, nil }
	s.mu.Lock()
	delete(s.panes, "w1p1")
	s.mu.Unlock()
	for i := 0; i < pane.MaxSecrets; i++ {
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
	// The cap is full: the cover is stored whole and dropped from the set.
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-cover",
		Res:    jsonRaw(map[string]any{"session": "other", "values": []string{"foreign-value"}}),
	}); !rep.OK {
		t.Fatalf("cover: %s", rep.Error)
	}
	w := log.Writer()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(w) })
	ch := spyPane(t, s, "w1p1")
	s.adoptSecretScrub("w1p1")
	got := readSpyUntil(t, ch, "set-secrets")
	var payload struct {
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(got.Res, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Values) != pane.MaxSecrets {
		t.Fatalf("the adopt push carried %d values, want %d", len(payload.Values), pane.MaxSecrets)
	}
	for _, v := range payload.Values {
		if v == "foreign-value" {
			t.Fatal("the dropped value rode the adopt push")
		}
	}
	if !strings.Contains(buf.String(), "the panes' cap dropped 1 value another session asked this one to cover") {
		t.Fatalf("the adopt drop stayed silent: %q", buf.String())
	}
}

// TestSecretUseRefusesAReRegisteredAfterTheActivation proves the use guard
// checks the record's identity too: a revoke-and-re-register after the
// activation must not report success under the new record.
func TestSecretUseRefusesAReRegisteredAfterTheActivation(t *testing.T) {
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	s.fetch = func(*session.Secret) (string, error) { return "s3cret-old-value", nil }
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "dev/a",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	entered := make(chan struct{})
	s.afterActivate = func() {
		// The revoke-and-re-register lands after the activation, inside
		// the use flow, before the guard runs.
		s.handleReq(cl, wire.Msg{Action: "secret-revoke", Name: "gh"})
		s.handleReq(cl, wire.Msg{Action: "secret-register", Name: "gh", Tool: "pass", Ref: "dev/b"})
		close(entered)
	}
	go func() { s.handleReq(cl, wire.Msg{Action: "secret-use", Name: "gh"}) }()
	<-entered
	rep := rpcRead(t, conn, wire.Msg{Action: "secret-revoke"})
	if !rep.OK {
		t.Fatalf("revoke reply: %s", rep.Error)
	}
	rep = rpcRead(t, conn, wire.Msg{Action: "secret-register"})
	if !rep.OK {
		t.Fatalf("register reply: %s", rep.Error)
	}
	rep = rpcRead(t, conn, wire.Msg{Action: "secret-use"})
	wantErr(t, "secret-use", "revoked while the value was being fetched or pushed", rep)
}

// slowConn is a conn whose writes take their time and succeed: a push to it
// stalls long enough for the test to land other actions inside the push, and
// never tears.
type slowConn struct{ delay time.Duration }

func (c *slowConn) Write(b []byte) (int, error)      { time.Sleep(c.delay); return len(b), nil }
func (c *slowConn) Close() error                     { return nil }
func (c *slowConn) Read(b []byte) (int, error)       { return 0, io.EOF }
func (c *slowConn) LocalAddr() net.Addr              { return nil }
func (c *slowConn) RemoteAddr() net.Addr             { return nil }
func (c *slowConn) SetDeadline(time.Time) error      { return nil }
func (c *slowConn) SetReadDeadline(time.Time) error  { return nil }
func (c *slowConn) SetWriteDeadline(time.Time) error { return nil }

// TestSecretHydratesSerializeOnAPath proves the write, its guards and the
// removal are one critical section: a hydrate the revoke catches in its push
// writes nothing and removes nothing, so a concurrent hydrate to the same
// path that already replied success keeps its file.
func TestSecretHydratesSerializeOnAPath(t *testing.T) {
	ghValue := "s3cret-gh-serialize"
	awsValue := "s3cret-aws-serialize"
	s, cl, conn, _ := secretServer(t, "secrets", "", nil)
	s.fetch = func(sec *session.Secret) (string, error) {
		if sec.Name == "gh" {
			return ghValue, nil
		}
		return awsValue, nil
	}
	for _, name := range []string{"gh", "aws"} {
		if rep := secretCall(t, s, cl, conn, wire.Msg{
			Action: "secret-register", Name: name, Tool: "pass", Ref: "dev/" + name,
		}); !rep.OK {
			t.Fatalf("register %s: %s", name, rep.Error)
		}
	}
	// A slow pane holds the gh hydrate inside its push: the write, the
	// guards and the removal all wait behind it.
	s.mu.Lock()
	s.panes["w1p2"] = &paneConn{id: "w1p2", c: &slowConn{delay: 150 * time.Millisecond}}
	s.mu.Unlock()
	path := filepath.Join(t.TempDir(), "token")
	done := make(chan wire.Msg, 1)
	go func() {
		s.handleReq(cl, wire.Msg{Action: "secret-hydrate", Name: "gh", Path: path})
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		m, err := wire.ReadMsg(conn)
		if err != nil {
			return
		}
		done <- m
	}()
	// The gh hydrate is now stalled in its push. The revoke lands while it
	// waits, so its critical section opens on a revoked record.
	time.Sleep(50 * time.Millisecond)
	if rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-revoke", Name: "gh"}); !rep.OK {
		t.Fatalf("revoke: %s", rep.Error)
	}
	// The aws hydrate completes whole, to the same path, while the gh
	// hydrate is still finishing its push.
	if rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-hydrate", Name: "aws", Path: path}); !rep.OK {
		t.Fatalf("hydrate aws: %s", rep.Error)
	}
	rep := <-done
	wantErr(t, "secret-hydrate", "revoked while the value was being fetched or pushed", rep)
	if strings.Contains(rep.Error, ghValue) {
		t.Fatalf("the refusal leaks the value: %s", rep.Error)
	}
	// The survivor's file stands: the refused hydrate wrote nothing and
	// removed nothing.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the surviving hydrate's file did not survive: %v", err)
	}
	if string(b) != awsValue {
		t.Fatalf("file = %q, want the surviving hydrate's value", b)
	}
}

// TestSecretHydrateRevokeWaitsForTheWrite proves the guard, the write and the
// re-check are one critical section: a revoke aimed into the write cannot
// land inside it, so the hydrate succeeds and the file stands. The value is
// wide on purpose: the temp file is on disk while the bytes are still
// copying, so ordinary polling aims the revoke into the write. Under separate
// steps this ordering refused the hydrate and unlinked the file.
func TestSecretHydrateRevokeWaitsForTheWrite(t *testing.T) {
	value := strings.Repeat("s3cret-midwrite-", 1<<21)
	s, cl, conn, _ := secretServer(t, "secrets", value, nil)
	if rep := secretCall(t, s, cl, conn, wire.Msg{
		Action: "secret-register", Name: "gh", Tool: "pass", Ref: "gh/token",
	}); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	path := filepath.Join(t.TempDir(), "token")
	done := make(chan wire.Msg, 1)
	go func() {
		s.handleReq(cl, wire.Msg{Action: "secret-hydrate", Name: "gh", Path: path})
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		m, err := wire.ReadMsg(conn)
		if err != nil {
			return
		}
		done <- m
	}()
	// The temp file's appearance means the write is in flight, under the
	// critical section's hold.
	deadline := time.Now().Add(5 * time.Second)
	writing := false
	for !writing {
		if entries, err := os.ReadDir(filepath.Dir(path)); err == nil {
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".secrets-") {
					writing = true
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the hydrate never started writing")
		}
		if !writing {
			time.Sleep(time.Millisecond)
		}
	}
	// The revoke is issued inside the write: it waits for s.mu, so it lands
	// after the re-check, never between the looks.
	cl2, conn2 := boxConn(t)
	go func() { s.handleReq(cl2, wire.Msg{Action: "secret-revoke", Name: "gh"}) }()
	rep := <-done
	if !rep.OK {
		t.Fatalf("hydrate = refusal %q, want a success: the revoke landed inside the write", rep.Error)
	}
	rep = rpcRead(t, conn2, wire.Msg{Action: "secret-revoke"})
	if !rep.OK {
		t.Fatalf("revoke reply: %s", rep.Error)
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, []byte(value)) {
		t.Fatalf("file mismatch err=%v, want the hydrate's value", err)
	}
}
