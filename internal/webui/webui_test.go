package webui

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"dm/internal/ask"
	"dm/internal/box"
	"dm/internal/paths"
	"dm/internal/vt"
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
			// The cookie is what a bookmark and an installed app lean on, so
			// it has to outlive a month of not opening the page.
			if want := cookieDays * 24 * 60 * 60; c.MaxAge != want {
				t.Fatalf("cookie MaxAge = %d, want %d", c.MaxAge, want)
			}
			if cookieDays < 365 {
				t.Fatalf("cookieDays = %d, want a year or more", cookieDays)
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
	if got.MaxPictures != maxThumbs {
		t.Errorf("maxPictures = %d, want the stream's limit %d", got.MaxPictures, maxThumbs)
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

// TestListenBindsAndServes proves the split half: Listen binds the address,
// the URL carries the token, Addr reports the port the kernel gave, the page
// answers HTTP, and Close makes Serve return.
func TestListenBindsAndServes(t *testing.T) {
	dir := root(t, "main")
	srv, err := Listen(Config{Addr: "127.0.0.1:0", Session: "main", Root: dir})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	tcp, ok := srv.Addr().(*net.TCPAddr)
	if !ok || tcp.Port == 0 {
		t.Fatalf("Addr() = %v, want the bound 127.0.0.1:0 port", srv.Addr())
	}
	tok, err := os.ReadFile(filepath.Join(paths.SessionDirAt(dir, "main"), "web.token"))
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	url := srv.URL()
	if want := "http://127.0.0.1:" + fmt.Sprint(tcp.Port) + "/?t=" + strings.TrimSpace(string(tok)); url != want {
		t.Fatalf("URL() = %q, want %q", url, want)
	}

	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", res.StatusCode, body)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve = %v, want http.ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
}

// TestQRText proves the art is a square of equal width lines, one module per
// cell, and that a longer link needs a wider code.
func TestQRText(t *testing.T) {
	url := "http://you.ts.net:8099/?t=c33beba64055b013"
	art, err := QRArt(url)
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

// fakeHistory answers the history action a scrollback request sends, with the
// rows it is given. A request that names a count the rows do not fill says the
// pane held no more, which is what the daemon does.
func fakeHistory(t *testing.T, rootDir, session string, lines []string) {
	t.Helper()
	listenUnix(t, paths.ServerSockAt(rootDir, session), func(c net.Conn) {
		for {
			m, err := wire.ReadMsg(c)
			if err != nil {
				return
			}
			if m.Action != "history" {
				_ = wire.WriteMsg(c, wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: false, Error: "test: no action " + m.Action})
				continue
			}
			res, _ := json.Marshal(map[string]any{"lines": lines})
			_ = wire.WriteMsg(c, wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: true, Res: res})
		}
	})
}

// TestHistoryServesTheRowsAboveTheScreen proves the scrollback rides the same
// shaping as the screen, and that a reply which filled the request says there
// may be older rows behind it.
func TestHistoryServesTheRowsAboveTheScreen(t *testing.T) {
	session := "main"
	dir := root(t, session)
	rows := []string{"one", strings.Repeat("\u2500", 30), "", "", "two", "", ""}
	fakeHistory(t, dir, session, rows)
	srv := httptest.NewServer(handler(Config{Session: session, Root: dir, Token: "t"}))
	defer srv.Close()

	res := get(t, srv.URL, "/api/history?pane=w1p1&n=7&t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var got historyReply
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != 4 {
		t.Fatalf("lines = %q, want the long rule cut and the blank run collapsed", got.Lines)
	}
	if got.Lines[0] != "one" || got.Lines[3] != "two" {
		t.Fatalf("lines = %q, want the oldest row first", got.Lines)
	}
	if !got.More {
		t.Fatal("a reply that filled the request says there is nothing older")
	}

	// A reply shorter than the request ends the climb.
	res = get(t, srv.URL, "/api/history?pane=w1p1&n=200&t=t")
	defer func() { _ = res.Body.Close() }()
	var small historyReply
	if err := json.NewDecoder(res.Body).Decode(&small); err != nil {
		t.Fatal(err)
	}
	if small.More {
		t.Fatal("a short reply claims older rows behind it")
	}
}

// TestHistoryRefusesABadRequest proves the guards run before the daemon is
// asked: a pane id that is not one, and a count that is not a positive number.
func TestHistoryRefusesABadRequest(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "t"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()
	for _, q := range []string{
		"/api/history?pane=../x&t=t",
		"/api/history?pane=w1p1&n=zero&t=t",
		"/api/history?pane=w1p1&n=-3&t=t",
	} {
		res := get(t, srv.URL, q)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", q, res.StatusCode)
		}
	}
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

// TestShapeText proves the screen keeps what the pane said and loses the
// chrome a phone cannot use: long rules, blank runs, and trailing spaces.
func TestShapeText(t *testing.T) {
	const bar = "\u2500"
	const heavy = "\u2501"
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "a long rule becomes three characters of the same rule",
			in:   []string{"$ make test", strings.Repeat(bar, 80)},
			want: []string{"$ make test", strings.Repeat(bar, 3)},
		},
		{
			name: "a heavy rule keeps its own character",
			in:   []string{strings.Repeat(heavy, 12)},
			want: []string{strings.Repeat(heavy, 3)},
		},
		{
			name: "a run of four is not a rule",
			in:   []string{strings.Repeat(bar, 4)},
			want: []string{strings.Repeat(bar, 4)},
		},
		{
			name: "a spinner between two rules keeps its word",
			in:   []string{strings.Repeat(bar, 30) + " \u280b working " + strings.Repeat(bar, 30)},
			want: []string{strings.Repeat(bar, 3) + " \u280b working " + strings.Repeat(bar, 3)},
		},
		{
			name: "five blank lines become one",
			in:   []string{"first", "", "", "", "", "", "second"},
			want: []string{"first", "", "second"},
		},
		{
			name: "trailing spaces go",
			in:   []string{"ok  dm/internal/webui      ", "done   "},
			want: []string{"ok  dm/internal/webui", "done"},
		},
		{
			name: "a line of spaces is a blank line",
			in:   []string{"a", "     ", "       ", "b"},
			want: []string{"a", "", "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shapeText(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("lines %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("line %d is %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestTextPollOfARuledScreenIsQuiet proves the revision still hashes the text
// the page receives: a rule redrawn the same way is one change, then none.
func TestTextPollOfARuledScreenIsQuiet(t *testing.T) {
	session := "main"
	dir := root(t, session)
	screen := []string{strings.Repeat("\u2500", 80), "\u280b working", strings.Repeat("\u2500", 80)}
	fakeRead(t, dir, session, screen, wire.Status{State: "working"})
	srv := httptest.NewServer(handler(Config{Session: session, Root: dir, Token: "t"}))
	defer srv.Close()

	res := get(t, srv.URL, "/api/text?pane=w1p1&t=t")
	var first textReply
	if err := json.NewDecoder(res.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if !first.Changed || len(first.Lines) != 3 || first.Lines[0] != strings.Repeat("\u2500", 3) {
		t.Fatalf("first poll %+v", first)
	}

	res = get(t, srv.URL, "/api/text?pane=w1p1&since="+first.Rev+"&t=t")
	defer func() { _ = res.Body.Close() }()
	var second textReply
	if err := json.NewDecoder(res.Body).Decode(&second); err != nil {
		t.Fatal(err)
	}
	if second.Changed || len(second.Lines) != 0 {
		t.Fatalf("second poll %+v", second)
	}
	if second.Rev != first.Rev {
		t.Fatalf("revisions differ on one screen: %q and %q", first.Rev, second.Rev)
	}
}

// --- the command catalogue and the action endpoint ------------------------

// post sends one JSON body to url and returns the response.
func post(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(url, "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// body reads the whole response body as text.
func body(t *testing.T, res *http.Response) string {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sessionLog is a session that records every request it receives.
type sessionLog struct {
	mu  sync.Mutex
	got []wire.Msg
}

func (l *sessionLog) add(m wire.Msg) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, m)
}

// last returns the request that reached the session most recently.
func (l *sessionLog) last(t *testing.T) wire.Msg {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.got) == 0 {
		t.Fatal("no request reached the session")
	}
	return l.got[len(l.got)-1]
}

// recordSession serves one session that records every request and answers it
// with res, or with the daemon's refusal fail when that is set.
func recordSession(t *testing.T, dir, session, res, fail string) *sessionLog {
	t.Helper()
	log := &sessionLog{}
	listenUnix(t, paths.ServerSockAt(dir, session), func(c net.Conn) {
		for {
			m, err := wire.ReadMsg(c)
			if err != nil {
				return
			}
			log.add(m)
			out := wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: true, Res: json.RawMessage(res)}
			if fail != "" {
				out.OK, out.Error = false, fail
			}
			if err := wire.WriteMsg(c, out); err != nil {
				return
			}
		}
	})
	return log
}

// sampleTarget is a well formed id of the row's target kind.
func sampleTarget(kind targetKind) string {
	switch kind {
	case targetPane:
		return "w1p1"
	case targetWindow:
		return "w2"
	case targetWorkspace:
		return "s1"
	case targetAsk:
		return "a1"
	}
	return ""
}

// sampleAnswer is one well formed ask-answer body: the answer to the one
// question of the ask the page draws.
const sampleAnswer = `{"items":[{"questionIndex":0,"optionIndexes":[1]}]}`

// sampleRequest is one request body for a row: its id and a sample value for
// every field the row names. A field is a string except the answer body, which
// is the object the daemon decodes.
func sampleRequest(cmd command) map[string]any {
	body := map[string]any{"id": cmd.ID}
	for _, f := range cmd.Fields {
		switch f {
		case "target":
			body[f] = sampleTarget(cmd.Target)
		case "site":
			body[f] = "down"
		case "layout":
			body[f] = layouts[2]
		case "name":
			body[f] = "build"
		case "cwd":
			body[f] = "/tmp"
		case "msg":
			body[f] = "on my way"
		case "state":
			body[f] = "blocked"
		case "key":
			body[f] = "lane/x"
		case "answer":
			body[f] = json.RawMessage(sampleAnswer)
		default:
			panic("sampleRequest: no sample for field " + f)
		}
	}
	return body
}

// sampleText reads one sample field that carries a string.
func sampleText(req map[string]any, name string) string {
	s, _ := req[name].(string)
	return s
}

// TestEveryCommandReachesTheDaemon proves each catalogue row maps to one wire
// request: the action name is the row's id, the fields the row names reach the
// message, and no field the row does not name does.
func TestEveryCommandReachesTheDaemon(t *testing.T) {
	dir := root(t, "main")
	log := recordSession(t, dir, "main", `{"pane":"w1p9"}`, "")
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	defer srv.Close()

	for _, cmd := range commands {
		t.Run(cmd.ID, func(t *testing.T) {
			req := sampleRequest(cmd)
			res := post(t, srv.URL+"/api/action?t=t", req)
			if got := body(t, res); res.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", res.StatusCode, got)
			}
			got := log.last(t)
			if got.Action != cmd.ID {
				t.Fatalf("action %q, want %q", got.Action, cmd.ID)
			}
			if cmd.ID == "msg-post" {
				// The daemon's own decoder reads the payload, so the box request
				// is proved against the shape the server enforces.
				parsed, err := box.ParseReq(got.Action, got.Res)
				if err != nil {
					t.Fatalf("the daemon would refuse the payload: %v", err)
				}
				post := parsed.(*box.PostReq)
				if post.Key != sampleText(req, "key") || post.Msg != sampleText(req, "msg") || string(post.State) != sampleText(req, "state") {
					t.Fatalf("payload %+v", post)
				}
				if post.From != "" {
					t.Fatalf("from %q, want empty: the phone is not a pane", post.From)
				}
				return
			}
			if cmd.ID == "ask-answer" {
				// The same rule for an answer: the daemon's decoder reads the
				// body, so the form's payload is proved against it.
				if got.Target != sampleText(req, "target") {
					t.Fatalf("target %q, want the ask %q", got.Target, sampleText(req, "target"))
				}
				parsed, err := ask.ParseAnswer(got.Res)
				if err != nil {
					t.Fatalf("the daemon would refuse the answer: %v", err)
				}
				item := parsed.Items[0]
				if len(parsed.Items) != 1 || item.QuestionIndex != 0 ||
					len(item.OptionIndexes) != 1 || item.OptionIndexes[0] != 1 {
					t.Fatalf("answer reached the socket as %+v", parsed)
				}
				return
			}
			want := wire.Msg{Action: cmd.ID, Target: sampleText(req, "target"), Dir: sampleText(req, "site"),
				Name: sampleText(req, "name"), Layout: sampleText(req, "layout"), Cwd: sampleText(req, "cwd")}
			got.Type, got.ID, got.Res = "", 0, nil
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("request reached the socket as %+v, want %+v", got, want)
			}
		})
	}
}

// TestActionRefusesABadRequest proves the endpoint answers 400 with the reason:
// an unknown id, a field outside the published shape, a field the row does not
// name, a target on a row that takes none, and an id of the wrong shape.
func TestActionRefusesABadRequest(t *testing.T) {
	dir := root(t, "main")
	recordSession(t, dir, "main", `{}`, "")
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	defer srv.Close()

	cases := []struct {
		name string
		body map[string]string
		want string
	}{
		{"unknown id", map[string]string{"id": "nope"}, "unknown command nope"},
		{"unknown field", map[string]string{"id": "zoom-pane", "nope": "x"}, "unknown field"},
		{"field the row does not name", map[string]string{"id": "zoom-pane", "name": "x"}, "zoom-pane takes no name"},
		{"target on a targetless row", map[string]string{"id": "zoom-pane", "target": "w1p1"}, "zoom-pane takes no target"},
		{"bad pane id", map[string]string{"id": "focus", "target": "../x"}, "bad pane id"},
		{"bad window id", map[string]string{"id": "focus-window", "target": "w1p1"}, "bad window id"},
		{"bad workspace id", map[string]string{"id": "focus-workspace", "target": "s1x"}, "bad workspace id"},
		{"workspace id on a window row", map[string]string{"id": "focus-window", "target": "s1"}, "not a window id"},
		{"bad site", map[string]string{"id": "split", "site": "sideways"}, "bad site"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := post(t, srv.URL+"/api/action?t=t", tc.body)
			got := body(t, res)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d: %s", res.StatusCode, got)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("body %q does not name %q", got, tc.want)
			}
		})
	}
}

// TestCommandsRefuseReadOnly proves both endpoints refuse under --read-only: a
// menu of commands that cannot run is not offered.
func TestCommandsRefuseReadOnly(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "t", ReadOnly: true}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	res := get(t, srv.URL, "/api/commands?t=t")
	if got := body(t, res); res.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /api/commands: status %d: %s", res.StatusCode, got)
	}
	res = post(t, srv.URL+"/api/action?t=t", map[string]string{"id": "zoom-pane"})
	if got := body(t, res); res.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /api/action: status %d: %s", res.StatusCode, got)
	}
}

// TestCommandsNeedTheToken proves the catalogue sits behind the same check as
// every other route.
func TestCommandsNeedTheToken(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "t"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	res := get(t, srv.URL, "/api/commands")
	_ = body(t, res)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", res.StatusCode)
	}
	res = post(t, srv.URL+"/api/action", map[string]string{"id": "zoom-pane"})
	_ = body(t, res)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", res.StatusCode)
	}
}

// TestActionEmptyTargetIsTheDaemonsCall proves an empty target is not guessed
// here: the empty target reaches the socket and the daemon's refusal is the
// answer the page gets.
func TestActionEmptyTargetIsTheDaemonsCall(t *testing.T) {
	dir := root(t, "main")
	log := recordSession(t, dir, "main", "", `no pane ""`)
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	defer srv.Close()

	res := post(t, srv.URL+"/api/action?t=t", map[string]string{"id": "focus"})
	got := body(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d: %s", res.StatusCode, got)
	}
	if !strings.Contains(got, `no pane ""`) {
		t.Fatalf("body %q does not carry the daemon's reason", got)
	}
	if sent := log.last(t); sent.Target != "" {
		t.Fatalf("target %q reached the socket, want the empty one the page sent", sent.Target)
	}
}

// boxSession serves one session with a box that holds what msg-post stores, so
// a test can post through the action endpoint and read the entry back from the
// fleet's box list.
func boxSession(t *testing.T, dir, session string) {
	t.Helper()
	var mu sync.Mutex
	entries := []box.Entry{}
	listenUnix(t, paths.ServerSockAt(dir, session), func(c net.Conn) {
		for {
			m, err := wire.ReadMsg(c)
			if err != nil {
				return
			}
			out := wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: true}
			switch m.Action {
			case "list":
				out.Res, _ = json.Marshal(map[string]any{"snapshot": wire.Snapshot{Session: session}})
			case "msg-list":
				mu.Lock()
				out.Res, _ = json.Marshal(box.ListReply{Entries: append([]box.Entry{}, entries...)})
				mu.Unlock()
			case "msg-post":
				parsed, err := box.ParseReq(m.Action, m.Res)
				if err != nil {
					out.OK, out.Error = false, err.Error()
					break
				}
				post := parsed.(*box.PostReq)
				mu.Lock()
				e := box.Entry{Seq: len(entries) + 1, At: time.Now().UTC().Truncate(time.Second),
					From: post.From, Key: post.Key, State: post.State, Msg: post.Msg, Body: post.Body}
				entries = append(entries, e)
				mu.Unlock()
				if err := e.Validate(); err != nil {
					out.OK, out.Error = false, err.Error()
					break
				}
				out.Res, _ = json.Marshal(box.PostReply{Entry: e})
			default:
				out.OK, out.Error = false, "test: no action "+m.Action
			}
			if err := wire.WriteMsg(c, out); err != nil {
				return
			}
		}
	})
}

// TestMsgPostLandsInTheBox proves a phone can post to the box: the action
// endpoint stores one entry through the session and the fleet lists it back.
func TestMsgPostLandsInTheBox(t *testing.T) {
	dir := root(t, "main")
	boxSession(t, dir, "main")
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	defer srv.Close()

	res := post(t, srv.URL+"/api/action?t=t", map[string]string{
		"id": "msg-post", "key": "lane/x", "state": "blocked", "msg": "waiting on you",
	})
	got := body(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, got)
	}
	var reply box.PostReply
	if err := json.Unmarshal([]byte(got), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Entry.Key != "lane/x" || reply.Entry.Msg != "waiting on you" || reply.Entry.From != "" {
		t.Fatalf("posted %+v", reply.Entry)
	}

	res = get(t, srv.URL, "/api/fleet?t=t")
	defer func() { _ = res.Body.Close() }()
	var fleet fleetReply
	if err := json.NewDecoder(res.Body).Decode(&fleet); err != nil {
		t.Fatal(err)
	}
	if fleet.Error != "" {
		t.Fatalf("error %q", fleet.Error)
	}
	if len(fleet.Box) != 1 {
		t.Fatalf("box %+v, want the posted entry", fleet.Box)
	}
	if b := fleet.Box[0]; b.Key != "lane/x" || b.State != "blocked" || b.Msg != "waiting on you" || b.From != "" {
		t.Fatalf("box entry %+v", b)
	}
}

// TestCommandsListsTheCatalogue proves the endpoint answers one row per command
// with the id, label and group the page builds its menus from.
func TestCommandsListsTheCatalogue(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "t"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	res := get(t, srv.URL, "/api/commands?t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var got catalogueReply
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Commands) != len(commands) {
		t.Fatalf("%d rows, want %d", len(got.Commands), len(commands))
	}
	for i, c := range got.Commands {
		want := commands[i]
		if c.ID != want.ID || c.Label != want.Label || c.Group != want.Group || c.Target != want.Target {
			t.Fatalf("row %d is %+v, want %+v", i, c, want)
		}
		if len(c.Fields) != len(want.Fields) {
			t.Fatalf("%s names %v, want %v", c.ID, c.Fields, want.Fields)
		}
	}
}

// --- the picture stream ---------------------------------------------------

// fakePane answers a screen subscription with the screens it is given, then
// closes. It is the pane daemon's side of the picture stream.
func fakePane(t *testing.T, sock string, screens ...string) {
	t.Helper()
	listenUnix(t, sock, func(c net.Conn) {
		for {
			m, err := wire.ReadMsg(c)
			if err != nil {
				return
			}
			if m.Action != "screen-sub" {
				continue
			}
			for _, text := range screens {
				snap := testScreen(text)
				_ = wire.WriteMsg(c, wire.Msg{Type: "screen", VTSnap: snap})
			}
			return
		}
	})
}

// silentPane accepts a screen subscription and answers nothing, the way a pane
// daemon older than this build does. It holds the connection until the page
// closes it.
func silentPane(t *testing.T, sock string) {
	t.Helper()
	listenUnix(t, sock, func(c net.Conn) {
		buf := make([]byte, 1)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	})
}

// testScreen builds a one line snapshot carrying text.
func testScreen(text string) *vt.Snapshot {
	term := vt.NewTerm(len(text)+2, 1)
	term.Write([]byte(text))
	term.Flush()
	return term.Snapshot()
}

// snapText joins a snapshot's rows into the text a renderer would paint.
func snapText(snap *vt.Snapshot) string {
	var b strings.Builder
	for _, row := range snap.Lines {
		for _, c := range row.Cells {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// TestThumbsStreamsEveryPane proves the picture stream multiplexes the panes in
// one response, so the page holds one connection for the grid.
func TestThumbsStreamsEveryPane(t *testing.T) {
	session := "main"
	dir := root(t, session)
	fakePane(t, paths.PaneSockAt(dir, session, "w1p1"), "left")
	fakePane(t, paths.PaneSockAt(dir, session, "w1p2"), "right")

	cfg := Config{Session: session, Root: dir, Token: "t"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	res := get(t, srv.URL, "/api/thumbs?panes=w1p1,w1p2&t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	seen := map[string]string{}
	scan := bufio.NewScanner(res.Body)
	for scan.Scan() {
		if d, ok := strings.CutPrefix(scan.Text(), "data: "); ok {
			var ev shotEvent
			if err := json.Unmarshal([]byte(d), &ev); err != nil {
				t.Fatal(err)
			}
			if ev.Snap != nil {
				seen[ev.Pane] = snapText(ev.Snap)
			}
		}
		if len(seen) == 2 {
			break
		}
	}
	if !strings.Contains(seen["w1p1"], "left") || !strings.Contains(seen["w1p2"], "right") {
		t.Fatalf("pictures %v", seen)
	}
}

// TestThumbsReportASilentPane proves a pane daemon that cannot answer paints as
// gone, so the tile says it has no picture instead of looking quiet.
func TestThumbsReportASilentPane(t *testing.T) {
	session := "main"
	dir := root(t, session)
	silentPane(t, paths.PaneSockAt(dir, session, "w1p1"))

	cfg := Config{Session: session, Root: dir, Token: "t"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	res := get(t, srv.URL, "/api/thumbs?panes=w1p1&t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	scan := bufio.NewScanner(res.Body)
	for scan.Scan() {
		d, ok := strings.CutPrefix(scan.Text(), "data: ")
		if !ok {
			continue
		}
		var ev shotEvent
		if err := json.Unmarshal([]byte(d), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Gone && ev.Pane == "w1p1" && ev.Snap == nil {
			return
		}
	}
	t.Fatal("the silent pane never reported gone")
}

// TestThumbsRefusesABadList proves only pane ids reach a socket path.
func TestThumbsRefusesABadList(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "t"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	for _, q := range []string{"", "panes=", "panes=../../etc", "panes=w1p1/../x"} {
		res := get(t, srv.URL, "/api/thumbs?"+q+"&t=t")
		_ = body(t, res)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", q, res.StatusCode)
		}
	}
}

// TestThumbsSilenceEdgeRace proves no send races the event channel's close: the
// page hangs up at the instant the silence reports come due, so each pane's
// report is in flight while the stream gives up its last subscription and
// closes the channel. Run it under -race, repeated, to catch the interleaving.
func TestThumbsSilenceEdgeRace(t *testing.T) {
	old := paneSilence
	paneSilence = time.Millisecond
	t.Cleanup(func() { paneSilence = old })

	session := "main"
	dir := root(t, session)
	ids := make([]string, 0, maxThumbs)
	for i := 1; i <= maxThumbs; i++ {
		id := fmt.Sprintf("w1p%d", i)
		ids = append(ids, id)
		// Silent, so every pane has a silence report due on the edge.
		silentPane(t, paths.PaneSockAt(dir, session, id))
	}
	srv := httptest.NewServer(handler(Config{Session: session, Root: dir, Token: "t"}))
	defer srv.Close()

	for i := 0; i < 100; i++ {
		res := get(t, srv.URL, "/api/thumbs?panes="+strings.Join(ids, ",")+"&t=t")
		if res.StatusCode != http.StatusOK {
			t.Fatalf("iteration %d: status %d", i, res.StatusCode)
		}
		// Hang up as the reports come due: the cancel, the drops and the timer
		// callbacks all land on the same edge.
		time.Sleep(paneSilence)
		_ = res.Body.Close()
	}
}

// TestThumbsSilenceReportWaitsForTheClose proves the stream's close waits for a
// silence report already on its way. The report is held at the edge, the pane
// hangs up so the stream reaches its close, and the report is let go last. A
// close that does not wait sends on the closed channel and panics the run.
func TestThumbsSilenceReportWaitsForTheClose(t *testing.T) {
	old := paneSilence
	paneSilence = time.Millisecond
	t.Cleanup(func() { paneSilence = old })

	session := "main"
	dir := root(t, session)
	// The pane subscribes and hangs up late enough for the silence report to be
	// on its way, and without ever sending a picture.
	listenUnix(t, paths.PaneSockAt(dir, session, "w1p1"), func(c net.Conn) {
		if _, err := wire.ReadMsg(c); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	})
	srv := httptest.NewServer(handler(Config{Session: session, Root: dir, Token: "t"}))
	defer srv.Close()

	gate := make(chan struct{})
	silenceGate = gate
	t.Cleanup(func() { silenceGate = nil })

	// The page stays connected: the report must meet the close, not a cancel.
	res := get(t, srv.URL, "/api/thumbs?panes=w1p1&t=t")
	// The timer has fired and the report is held at the gate; the pane has hung
	// up, so the stream reaches its close.
	time.Sleep(100 * time.Millisecond)
	close(gate)
	// Give a close that did not wait time to take the report down with it.
	time.Sleep(100 * time.Millisecond)
	rest, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatalf("read the stream: %v", err)
	}
	// Two reports: the pane's own hang-up, and the silence report the wait
	// carried to the page before the end. A close that does not wait loses the
	// second, or takes it down as a panic.
	text := string(rest)
	if n := strings.Count(text, "event: gone"); n != 2 {
		t.Fatalf("%d silence reports reached the page, want the pane's and the timer's: %q", n, text)
	}
	if gone, end := strings.Index(text, "event: gone"), strings.Index(text, "event: end"); end < 0 || gone > end {
		t.Fatalf("the stream ended before the report: %q", text)
	}
}

// TestThumbsTrimsToTheLimit proves a window with more panes than the stream
// carries keeps its first ones, as the limit's comment promises, instead of
// painting nothing at all.
func TestThumbsTrimsToTheLimit(t *testing.T) {
	session := "main"
	dir := root(t, session)
	ids := make([]string, 0, maxThumbs+1)
	for i := 1; i <= maxThumbs+1; i++ {
		id := fmt.Sprintf("w1p%d", i)
		ids = append(ids, id)
		fakePane(t, paths.PaneSockAt(dir, session, id), id)
	}
	srv := httptest.NewServer(handler(Config{Session: session, Root: dir, Token: "t"}))
	defer srv.Close()

	res := get(t, srv.URL, "/api/thumbs?panes="+strings.Join(ids, ",")+"&t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	seen := map[string]bool{}
	scan := bufio.NewScanner(res.Body)
	for scan.Scan() {
		d, ok := strings.CutPrefix(scan.Text(), "data: ")
		if !ok {
			continue
		}
		var ev shotEvent
		if err := json.Unmarshal([]byte(d), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Snap != nil {
			seen[ev.Pane] = true
		}
	}
	if len(seen) != maxThumbs {
		t.Fatalf("%d panes pictured, want the first %d: %v", len(seen), maxThumbs, seen)
	}
	for _, id := range ids[:maxThumbs] {
		if !seen[id] {
			t.Errorf("pane %s was dropped, want the first ones kept", id)
		}
	}
	if seen[ids[maxThumbs]] {
		t.Errorf("pane %s is past the limit and was still subscribed", ids[maxThumbs])
	}
}

// TestThumbsRefusesABadIDInAList proves a bad id refuses the whole list rather
// than vanishing, so a page that asks for a bad pane learns it.
func TestThumbsRefusesABadIDInAList(t *testing.T) {
	dir := root(t, "main")
	fakePane(t, paths.PaneSockAt(dir, "main", "w1p1"), "left")
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	defer srv.Close()

	for _, q := range []string{"panes=w1p1,../x", "panes=../x,w1p1", "panes=w1p1,,w1p2", "panes=w1p1,nope"} {
		res := get(t, srv.URL, "/api/thumbs?"+q+"&t=t")
		_ = body(t, res)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", q, res.StatusCode)
		}
	}
}

// getClose is get with a connection the server does not keep open, so a
// goroutine count is not polluted by a pooled connection.
func getClose(t *testing.T, url, query string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Close = true
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// waitGoroutines waits until the count falls to want, so a test can prove a
// request left nothing behind without racing the scheduler.
func waitGoroutines(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := runtime.NumGoroutine()
		if got <= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines still running, want no more than %d", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestThumbsRefusedLeavesNothing proves a refused pane list subscribes no pane:
// the count returns to where it stood before the server existed.
func TestThumbsRefusedLeavesNothing(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "t"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()
	// The baseline is the server at rest, so the count proves the refusals
	// added nothing rather than that the server later went away.
	base := runtime.NumGoroutine()

	for _, q := range []string{"", "panes=", "panes=../../etc", "panes=w1p1/../x", "panes=a,b"} {
		res := getClose(t, srv.URL, "/api/thumbs?"+q+"&t=t")
		_ = body(t, res)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%q: status %d, want 400", q, res.StatusCode)
		}
	}
	waitGoroutines(t, base)
}

// paneSockets serves panes for the stream tests and records every connection
// it accepts, so a handler that fails to hang up is unblocked at cleanup
// instead of wedging the suite.
type paneSockets struct {
	mu    sync.Mutex
	conns []net.Conn
}

// answer serves sock with one screen carrying text, then closes.
func (p *paneSockets) answer(t *testing.T, sock, text string) {
	t.Helper()
	listenUnix(t, sock, func(c net.Conn) {
		p.track(c)
		for {
			m, err := wire.ReadMsg(c)
			if err != nil {
				return
			}
			if m.Action != "screen-sub" {
				continue
			}
			_ = wire.WriteMsg(c, wire.Msg{Type: "screen", VTSnap: testScreen(text)})
			return
		}
	})
}

// silent serves sock and answers nothing, holding the connection open.
func (p *paneSockets) silent(t *testing.T, sock string) {
	t.Helper()
	listenUnix(t, sock, func(c net.Conn) {
		p.track(c)
		buf := make([]byte, 1)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	})
}

func (p *paneSockets) track(c net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conns = append(p.conns, c)
}

// closeAll hangs up every subscription the test opened.
func (p *paneSockets) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
}

// TestThumbsCancelLeavesNothing proves a page that walks away frees every
// subscription it held, the silent one included.
func TestThumbsCancelLeavesNothing(t *testing.T) {
	session := "main"
	dir := root(t, session)
	var panes paneSockets
	panes.answer(t, paths.PaneSockAt(dir, session, "w1p1"), "left")
	panes.silent(t, paths.PaneSockAt(dir, session, "w1p2"))

	srv := httptest.NewServer(handler(Config{Session: session, Root: dir, Token: "t"}))
	t.Cleanup(srv.Close)
	// Registered after the server, so it runs first: a stream this test fails
	// to close cannot hold the server's own Close open.
	t.Cleanup(panes.closeAll)
	// The baseline holds the two pane listeners and the server, so the only
	// goroutines the wait can see are the ones the stream opened.
	base := runtime.NumGoroutine()

	res := get(t, srv.URL, "/api/thumbs?panes=w1p1,w1p2&t=t")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	// Wait for the first picture, so both subscriptions are open when the page
	// leaves.
	scan := bufio.NewScanner(res.Body)
	for scan.Scan() {
		if strings.Contains(scan.Text(), `"pane":"w1p1"`) {
			break
		}
	}
	_ = res.Body.Close()
	waitGoroutines(t, base)
}

// pageSource is the page's markup as the build carries it. The fields a thumb
// focuses and the composer's two controls live there, so a test reads them
// where they live.
func pageSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read the page: %v", err)
	}
	return string(b)
}

// TestPageCarriesTheOutsideMarks proves the page has the two places a question
// shows outside the rows themselves: a favicon link the script swaps, and the
// rules that settle a form which closed under the reader.
func TestPageCarriesTheOutsideMarks(t *testing.T) {
	t.Parallel()
	page := pageSource(t)
	for _, want := range []string{
		`<link id="icon" rel="icon"`,
		"#ask .ask-gone",
		".ask.settled",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the page misses %q", want)
		}
	}
}

// TestPageReloadsItself proves the actions sheet carries the one row an
// installed app needs to pick up a new build. The page and its script are
// served with no cache, so the tap is the whole update path: a phone with no
// address bar and no reload button has no other way in.
func TestPageReloadsItself(t *testing.T) {
	t.Parallel()
	page := pageSource(t)
	for _, want := range []string{
		`<button id="app-reload"`,
		`reload the page`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the page misses %q", want)
		}
	}
	if src := appScript(t); !strings.Contains(src, `$("app-reload").addEventListener("click", () => location.reload());`) {
		t.Fatal("app.js does not reload the page from the sheet's own row")
	}
}

// TestPageIsOneLook proves the page carries the desk's own look and no switch
// between looks: the mono face is the page's, no root attribute selects a
// second skin, and the header holds no skin toggle. A plain page that came back
// as an option would bring its own rules with it.
func TestPageIsOneLook(t *testing.T) {
	t.Parallel()
	page := pageSource(t)
	for _, want := range []string{
		`--mono: "JetBrains Mono"`,
		`font: 13px/1.5 var(--mono)`,
		`button, input, select, textarea { font-family: var(--mono); }`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the page misses %q", want)
		}
	}
	for _, banned := range []string{
		"data-skin",
		"skin-toggle",
		"dm.coat",
	} {
		if strings.Contains(page, banned) {
			t.Fatalf("the page still carries %q: one look, no switch", banned)
		}
	}
	if src := appScript(t); strings.Contains(src, "setSkin") || strings.Contains(src, "SKIN_KEY") {
		t.Fatal("the page's script still carries the skin switch")
	}
}

// pageRule is one style rule's text, from the selector to its closing brace.
// The page's rules carry no nested braces, so the first closing brace at line
// end ends the rule.
func pageRule(t *testing.T, page, selector string) string {
	t.Helper()
	at := strings.Index(page, selector+" {")
	if at < 0 {
		t.Fatalf("no %s rule in the page", selector)
	}
	end := strings.Index(page[at:], "}")
	if end < 0 {
		t.Fatalf("the %s rule has no closing brace", selector)
	}
	return page[at : at+end]
}

// pageTag is one element's markup, from its opening tag to the tag that closes
// it.
func pageTag(t *testing.T, page, open string) string {
	t.Helper()
	at := strings.Index(page, open)
	if at < 0 {
		t.Fatalf("no %s in the page", open)
	}
	end := strings.Index(page[at:], ">")
	if end < 0 {
		t.Fatalf("%s has no end of tag", open)
	}
	return page[at : at+end+1]
}

// pageAttr is one attribute's value on an element's opening tag.
func pageAttr(t *testing.T, tag, name string) string {
	t.Helper()
	at := strings.Index(tag, name+`="`)
	if at < 0 {
		t.Fatalf("no %s on %s", name, tag)
	}
	rest := tag[at+len(name)+2:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("%s has no closing quote on %s", name, tag)
	}
	return rest[:end]
}

// TestPageFieldsStaySixteenOnAPhone proves the page never leaves a focused
// field under 16: a phone width rule lifts every field a thumb can focus to 16,
// while the desktop rules keep their tighter size.
func TestPageFieldsStaySixteenOnAPhone(t *testing.T) {
	page := pageSource(t)
	rule := pageRule(t, page, "@media (max-width: 900px)")
	for _, field := range []string{"#compose input", "#compose select", "#send textarea",
		"#ask .ask-text", "#ask .ask-note", ".targets select"} {
		if !strings.Contains(rule, field) {
			t.Fatalf("the phone rule lifts no %s to 16: %s", field, rule)
		}
	}
	if !strings.Contains(rule, "font-size: 16px;") {
		t.Fatalf("the phone rule carries no 16: %s", rule)
	}
	// The zoom is the symptom. Pinch stays enabled, so the viewport names no
	// scale a reader cannot undo.
	meta := pageTag(t, page, `<meta name="viewport"`)
	if got := pageAttr(t, meta, "content"); !strings.Contains(got, "width=device-width") {
		t.Fatalf("viewport %q", got)
	}
	for _, banned := range []string{"user-scalable=no", "maximum-scale"} {
		if strings.Contains(page, banned) {
			t.Fatalf("the page disables the reader's pinch with %s", banned)
		}
	}
}

// TestPageComposerButtonsCarryGlyphs proves the two composer controls carry a
// glyph instead of a word: each keeps its id, its 44 by 44 minimum, an
// aria-label and a title in words, and one decorative svg the reader never
// hears.
func TestPageComposerButtonsCarryGlyphs(t *testing.T) {
	page := pageSource(t)
	rule := pageRule(t, page, "#send button")
	for _, want := range []string{"min-height: 44px", "min-width: 44px"} {
		if !strings.Contains(rule, want) {
			t.Fatalf("the composer buttons lost their %s: %s", want, rule)
		}
	}
	for _, tc := range []struct{ id, label string }{
		{"send-attach", "attach a picture"},
		{"send-submit", "send"},
	} {
		tag := pageTag(t, page, `<button id="`+tc.id+`"`)
		if got := pageAttr(t, tag, "aria-label"); got != tc.label {
			t.Fatalf("%s aria-label %q, want %q", tc.id, got, tc.label)
		}
		if got := pageAttr(t, tag, "title"); got == "" {
			t.Fatalf("%s carries no title", tc.id)
		}
		at := strings.Index(page[strings.Index(page, `<button id="`+tc.id+`"`):], "<svg")
		if at < 0 {
			t.Fatalf("%s carries no glyph", tc.id)
		}
		svg := page[strings.Index(page, `<button id="`+tc.id+`"`)+at:]
		end := strings.Index(svg, "</svg>")
		if end < 0 {
			t.Fatalf("%s's glyph has no closing tag", tc.id)
		}
		if got := pageAttr(t, pageTag(t, svg[:end+len("</svg>")], "<svg"), "aria-hidden"); got != "true" {
			t.Fatalf("%s's glyph is not decorative: aria-hidden %q", tc.id, got)
		}
	}
	// No word is left inside the button, so a screen reader reads the label
	// once and a sighted reader sees the glyph alone.
	for _, id := range []string{"send-attach", "send-submit"} {
		at := strings.Index(page, `<button id="`+id+`"`)
		body := page[at+strings.Index(page[at:], ">")+1:]
		body = body[:strings.Index(body, "</button>")]
		if stem := strings.TrimSpace(body); !strings.HasPrefix(stem, "<svg") || !strings.HasSuffix(stem, "</svg>") {
			t.Fatalf("%s carries words beside its glyph: %s", id, stem)
		}
	}
}

// TestPageComposerKeepsItsRow proves the icons changed no behaviour: the whole
// row still leaves a read only page, and the field still takes the width the
// two words gave up.
func TestPageComposerKeepsItsRow(t *testing.T) {
	src := appScript(t)
	if !strings.Contains(src, `$("send").style.display = fleet && fleet.readOnly ? "none" : "";`) {
		t.Fatal("the composer row no longer hides on a read only page")
	}
	page := pageSource(t)
	rule := pageRule(t, page, "#send textarea")
	if !strings.Contains(rule, "flex: 1") || !strings.Contains(rule, "min-width: 0") {
		t.Fatalf("the field no longer takes the width of the row: %s", rule)
	}
}

// TestPageComposerIsATextarea proves the composer takes a block: it is a
// textarea one row tall at rest, its placeholder is unchanged, enter inserts a
// newline instead of sending, and the height it keeps at rest is the height the
// old input had.
func TestPageComposerIsATextarea(t *testing.T) {
	page := pageSource(t)
	tag := pageTag(t, page, `<textarea id="send-text"`)
	if got := pageAttr(t, tag, "rows"); got != "1" {
		t.Fatalf("the composer rests at %q rows, want 1", got)
	}
	if got := pageAttr(t, tag, "placeholder"); got != "type into the pane" {
		t.Fatalf("the placeholder = %q, want the one it always carried", got)
	}
	rule := pageRule(t, page, "#send textarea")
	for _, want := range []string{"min-height: 44px", "max-height: 30vh", "resize: none", "overflow-y: hidden"} {
		if !strings.Contains(rule, want) {
			t.Fatalf("the composer lost its %s: %s", want, rule)
		}
	}
	src := appScript(t)
	if strings.Contains(src, `$("send-text").addEventListener("keydown"`) {
		t.Fatal("the composer still intercepts a key, so enter no longer inserts a newline")
	}
	if !strings.Contains(src, `$("send-text").addEventListener("input", sendGrow);`) {
		t.Fatal("the composer does not refit itself as the reader types")
	}
}

// TestPageComposerGrowthRule proves the composer grows by its own row height to
// six rows, or stops at 30vh of the window, whichever comes first, and scrolls
// inside itself past that.
func TestPageComposerGrowthRule(t *testing.T) {
	src := appScript(t)
	for _, want := range []string{"const SEND_ROWS = 6;", "const SEND_VH = 0.3;"} {
		if !strings.Contains(src, want) {
			t.Fatalf("the composer's growth cap lost %q", want)
		}
	}
	body := funcSource(t, src, "sendGrow")
	for _, want := range []string{
		"Math.min(SEND_ROWS * line + pad + edge, window.innerHeight * SEND_VH)",
		`box.style.overflowY = want > cap ? "auto" : "hidden";`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sendGrow lost %q:\n%s", want, body)
		}
	}
}

// TestPageSendPastesABlock proves the send control's two shapes: a single line
// goes as one typed write with its enter, exactly as before, and a block with a
// newline goes as one paste followed by one enter, so the program takes the
// block whole and submits once. An empty composer sends nothing.
func TestPageSendPastesABlock(t *testing.T) {
	body := funcSource(t, appScript(t), "sendText")
	for _, want := range []string{
		"if (!pane || !text) return;",
		`if (text.includes("\n")) {`,
		`{ pane: pane.id, text, paste: true }`,
		`{ pane: pane.id, text: "", enter: true }`,
		`{ pane: pane.id, text, enter: true }`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sendText lost %q:\n%s", want, body)
		}
	}
	// A single line is sent once, not as a paste.
	at := strings.Index(body, "} else {")
	if at < 0 {
		t.Fatalf("sendText has no single line path:\n%s", body)
	}
	line := body[at+len("} else {"):]
	if strings.Contains(line[:strings.Index(line, "}")], "paste") {
		t.Fatalf("the single line path pastes:\n%s", line)
	}
}
