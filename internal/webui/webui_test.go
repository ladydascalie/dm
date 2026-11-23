package webui

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/paths"
	"dm/internal/wire"
)

// root builds a temp state tree with a session directory, so the handler's
// socket paths resolve inside the test.
func root(t *testing.T, session string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(paths.SessionDirAt(dir, session), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// listenUnix serves every connection on sock with handle, until the test ends.
func listenUnix(t *testing.T, sock string, handle func(net.Conn)) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
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
			go func() {
				defer func() { _ = c.Close() }()
				handle(c)
			}()
		}
	}()
}

// fakeSession answers the list and msg-list actions a fleet request sends.
func fakeSession(t *testing.T, rootDir, session string, snap wire.Snapshot, entries []box.Entry) {
	t.Helper()
	list, err := json.Marshal(map[string]any{"snapshot": snap})
	if err != nil {
		t.Fatal(err)
	}
	listBox, err := json.Marshal(box.ListReply{Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	listenUnix(t, paths.ServerSockAt(rootDir, session), func(c net.Conn) {
		for {
			m, err := wire.ReadMsg(c)
			if err != nil {
				return
			}
			var res []byte
			switch m.Action {
			case "list":
				res = list
			case "msg-list":
				res = listBox
			default:
				_ = wire.WriteMsg(c, wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: false, Error: "test: no action " + m.Action})
				continue
			}
			_ = wire.WriteMsg(c, wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: true, Res: res})
		}
	})
}

// handler serves cfg over a test server.
func handler(cfg Config) http.Handler { return NewHandler(cfg) }

// get sends one request with the token in the query.
func get(t *testing.T, url string, query string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestTokenRule(t *testing.T) {
	cfg := Config{Addr: "127.0.0.1:0", Session: "main", Root: root(t, "main"), Token: "s3cret"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	cases := []struct {
		name  string
		query string
		auth  string
		cook  string
		want  int
	}{
		{name: "no token", want: http.StatusUnauthorized},
		{name: "wrong token", query: "?t=nope", want: http.StatusUnauthorized},
		{name: "query token", query: "?t=s3cret", want: http.StatusOK},
		{name: "cookie", cook: "s3cret", want: http.StatusOK},
		{name: "wrong cookie", cook: "nope", want: http.StatusUnauthorized},
		{name: "bearer", auth: "Bearer s3cret", want: http.StatusOK},
		{name: "wrong bearer", auth: "Bearer nope", want: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL+"/"+tc.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			if tc.cook != "" {
				req.AddCookie(&http.Cookie{Name: tokenCookie, Value: tc.cook})
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != tc.want {
				t.Fatalf("status %d, want %d", res.StatusCode, tc.want)
			}
		})
	}
}

// TestIndexSetsCookie proves the first visit trades the token in the query for
// a cookie, so the stream and the fetches carry it without a URL.
func TestIndexSetsCookie(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "s3cret"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	res := get(t, srv.URL, "/?t=s3cret")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	for _, c := range res.Cookies() {
		if c.Name == tokenCookie {
			if c.Value != "s3cret" {
				t.Fatalf("cookie %q", c.Value)
			}
			return
		}
	}
	t.Fatal("no token cookie")
}

func TestFleetPaintsTheSession(t *testing.T) {
	session := "main"
	dir := root(t, session)
	snap := wire.Snapshot{
		Session: "main",
		Panes:   []wire.PaneInfo{{ID: "w1p1"}},
		All: []wire.AllPane{{
			ID: "w1p1", Name: "pi", App: "pi", Status: "blocked", Message: "may I?",
			Agent: true, Workspace: "s1", WorkspaceName: "dm", Window: "w1",
			WindowName: "main", Cwd: "/tmp/dm", Branch: "poc/web", Dirty: true,
			Added: 3, Deleted: 1, Focus: true,
		}},
	}
	entries := []box.Entry{{Seq: 7, From: "w1p2", Key: "lane/x", State: box.StateBlocked, Msg: "waiting"}}
	fakeSession(t, dir, session, snap, entries)

	cfg := Config{Session: session, Root: dir, Token: "t", ReadOnly: true}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	res := get(t, srv.URL, "/api/fleet?t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var got fleetReply
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.ReadOnly {
		t.Error("readOnly dropped")
	}
	if got.Error != "" {
		t.Fatalf("error %q", got.Error)
	}
	if len(got.Panes) != 1 {
		t.Fatalf("%d panes, want 1", len(got.Panes))
	}
	p := got.Panes[0]
	if p.ID != "w1p1" || p.Status != "blocked" || p.Message != "may I?" || !p.Active || !p.Agent {
		t.Errorf("pane %+v", p)
	}
	if p.WorkspaceName != "dm" || p.Branch != "poc/web" || !p.Dirty || p.Added != 3 || p.Deleted != 1 {
		t.Errorf("folder facts lost: %+v", p)
	}
	if len(got.Box) != 1 || got.Box[0].Key != "lane/x" || got.Box[0].State != "blocked" {
		t.Fatalf("box %+v", got.Box)
	}
}

// TestFleetReportsARefusedRequest proves a daemon that will not answer is a
// banner, not a 500 the page cannot read.
func TestFleetReportsARefusedRequest(t *testing.T) {
	session := "main"
	dir := root(t, session)
	listenUnix(t, paths.ServerSockAt(dir, session), func(c net.Conn) {
		for {
			m, err := wire.ReadMsg(c)
			if err != nil {
				return
			}
			_ = wire.WriteMsg(c, wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: false, Error: "the box is shut"})
		}
	})
	cfg := Config{Session: session, Root: dir, Token: "t"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	res := get(t, srv.URL, "/api/fleet?t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var got fleetReply
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "the box is shut" {
		t.Fatalf("error %q", got.Error)
	}
	if len(got.Panes) != 0 || got.Panes == nil {
		t.Fatalf("panes %#v, want an empty list", got.Panes)
	}
}

func TestURLFor(t *testing.T) {
	cases := []struct{ addr, want string }{
		{"127.0.0.1:8099", "http://127.0.0.1:8099/?t=k"},
		{"[::1]:8099", "http://[::1]:8099/?t=k"},
		{"0.0.0.0:8099", "http://127.0.0.1:8099/?t=k"},
		{"[::]:8099", "http://127.0.0.1:8099/?t=k"},
	}
	for _, tc := range cases {
		if got := urlFor(tc.addr, "k"); got != tc.want {
			t.Errorf("urlFor(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

func TestNewToken(t *testing.T) {
	a, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 {
		t.Fatalf("token %q is %d chars, want 32", a, len(a))
	}
	if a == b {
		t.Fatal("two tokens are equal")
	}
}

// TestFleetIgnoresUnknownPanes keeps the layout import honest: a snapshot with
// no panes paints no rows instead of failing.
func TestFleetEmptySession(t *testing.T) {
	session := "main"
	dir := root(t, session)
	fakeSession(t, dir, session, wire.Snapshot{Session: "main"}, nil)
	srv := httptest.NewServer(handler(Config{Session: session, Root: dir, Token: "t"}))
	defer srv.Close()

	res := get(t, srv.URL, "/api/fleet?t=t")
	defer func() { _ = res.Body.Close() }()
	var got fleetReply
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" {
		t.Fatalf("error %q", got.Error)
	}
	if len(got.Panes) != 0 || len(got.Box) != 0 {
		t.Fatalf("panes %v box %v", got.Panes, got.Box)
	}
}

// TestMain lets the test binary play the dm server: a fleet request on a state
// root with no socket finds nothing to dial, so transport.EnsureAt spawns
// os.Executable() with "__server" as its first argument.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__server" {
		serveSpawned(os.Args[2:])
		return
	}
	os.Exit(m.Run())
}

// serveSpawned is the server the test binary runs when transport spawns it. It
// resolves its state root from DM_STATE, which EnsureAt pins to the root that
// asked, listens on the session socket and answers an empty fleet.
func serveSpawned(args []string) {
	name := "main"
	for i, a := range args {
		if a == "-s" && i+1 < len(args) {
			name = args[i+1]
		}
	}
	sock := paths.ServerSock(name)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.Exit(1)
	}
	empty, err := json.Marshal(map[string]any{"snapshot": wire.Snapshot{Session: name}})
	if err != nil {
		os.Exit(1)
	}
	noBox, err := json.Marshal(box.ListReply{})
	if err != nil {
		os.Exit(1)
	}
	// The spawned server exits when its socket goes away, which is how the
	// test's temp root teardown ends it.
	go func() {
		for {
			if _, err := os.Stat(sock); err != nil {
				_ = ln.Close()
				os.Exit(0)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			os.Exit(0)
		}
		go func() {
			defer func() { _ = c.Close() }()
			for {
				m, err := wire.ReadMsg(c)
				if err != nil {
					return
				}
				res := empty
				if m.Action == "msg-list" {
					res = noBox
				}
				if err := wire.WriteMsg(c, wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: true, Res: res}); err != nil {
					return
				}
			}
		}()
	}
}

// TestFleetStartsASession proves dm web alone gives a working page: with an
// empty state root and no session running, the first fleet request starts the
// session and answers it. transport.EnsureAt already spawns a server on a dead
// socket, so the surface rides that path instead of needing a desk to start
// one first.
func TestFleetStartsASession(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	defer srv.Close()

	res := get(t, srv.URL, "/api/fleet?t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var got fleetReply
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" {
		t.Fatalf("error %q", got.Error)
	}
	if got.Session != "main" {
		t.Fatalf("session %q", got.Session)
	}
	if _, err := os.Stat(paths.ServerSockAt(dir, "main")); err != nil {
		t.Fatalf("the fleet request started no session: %v", err)
	}
}

// TestQRText proves the art is a square of equal width lines, one module per
// cell, and that a longer link needs a wider code.
func TestQRText(t *testing.T) {
	url := "http://100.71.227.52:8099/?t=c33beba64055b013"
	art, err := qrText(url)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(art, "\n"), "\n")
	wantW, wantH := qrSize(url)
	if len(lines) != wantH {
		t.Fatalf("%d lines, want %d", len(lines), wantH)
	}
	for i, line := range lines {
		if cells := strings.Count(line, " "); cells != wantW {
			t.Fatalf("line %d is %d cells, want %d", i, cells, wantW)
		}
		if !strings.HasSuffix(line, "\x1b[0m") {
			t.Fatalf("line %d does not reset its colour", i)
		}
	}
	if wantW < 21 {
		t.Fatalf("code is %d modules wide, smaller than a real QR", wantW)
	}
	if w, _ := qrSize(strings.Repeat("x", 400)); w <= wantW {
		t.Fatal("a longer link did not need a wider code")
	}
}

// fakeRead answers the read action a pane text request sends.
func fakeRead(t *testing.T, rootDir, session string, lines []string, st wire.Status) {
	t.Helper()
	listenUnix(t, paths.ServerSockAt(rootDir, session), func(c net.Conn) {
		for {
			m, err := wire.ReadMsg(c)
			if err != nil {
				return
			}
			if m.Action != "read" {
				_ = wire.WriteMsg(c, wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: false, Error: "test: no action " + m.Action})
				continue
			}
			res, _ := json.Marshal(map[string]any{"lines": lines, "status": st})
			_ = wire.WriteMsg(c, wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: true, Res: res})
		}
	})
}

// TestTextReadsThePane proves the pane view is the text the pane printed: blank
// rows under the prompt are dropped, and the status rides along.
func TestTextReadsThePane(t *testing.T) {
	session := "main"
	dir := root(t, session)
	fakeRead(t, dir, session, []string{"$ make test", "ok  dm/internal/webui", "", "", ""}, wire.Status{State: "done", Message: "all green"})
	srv := httptest.NewServer(handler(Config{Session: session, Root: dir, Token: "t"}))
	defer srv.Close()

	res := get(t, srv.URL, "/api/text?pane=w1p1&t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var got textReply
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Changed || got.Rev == "" {
		t.Fatalf("reply %+v", got)
	}
	if len(got.Lines) != 2 || got.Lines[1] != "ok  dm/internal/webui" {
		t.Fatalf("lines %q", got.Lines)
	}
	if got.State != "done" || got.Message != "all green" {
		t.Fatalf("facts %+v", got)
	}
}

// TestTextPollOnAQuietPaneIsEmpty proves the revision saves the body: a poll
// that names the current revision gets the status and no text.
func TestTextPollOnAQuietPaneIsEmpty(t *testing.T) {
	session := "main"
	dir := root(t, session)
	fakeRead(t, dir, session, []string{"steady"}, wire.Status{State: "working"})
	cfg := Config{Session: session, Root: dir, Token: "t"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	res := get(t, srv.URL, "/api/text?pane=w1p1&t=t")
	var first textReply
	if err := json.NewDecoder(res.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()

	res = get(t, srv.URL, "/api/text?pane=w1p1&since="+first.Rev+"&t=t")
	defer func() { _ = res.Body.Close() }()
	var second textReply
	if err := json.NewDecoder(res.Body).Decode(&second); err != nil {
		t.Fatal(err)
	}
	if second.Changed || len(second.Lines) != 0 {
		t.Fatalf("second poll %+v", second)
	}
	if second.State != "working" {
		t.Fatalf("status lost on an unchanged poll: %+v", second)
	}
}

// TestTextRefusesABadPaneID proves only a pane id reaches the socket path.
func TestTextRefusesABadPaneID(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "t"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	for _, id := range []string{"../x", "w1p1/../x", "", "W1P1"} {
		res := get(t, srv.URL, "/api/text?pane="+id+"&t=t")
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("pane %q: status %d, want 400", id, res.StatusCode)
		}
	}
}
