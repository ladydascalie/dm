package webui

import (
	"bufio"
	"encoding/json"
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
	}
	return ""
}

// sampleRequest is one request body for a row: its id and a sample value for
// every field the row names.
func sampleRequest(cmd command) map[string]string {
	body := map[string]string{"id": cmd.ID}
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
		default:
			panic("sampleRequest: no sample for field " + f)
		}
	}
	return body
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
				if post.Key != req["key"] || post.Msg != req["msg"] || string(post.State) != req["state"] {
					t.Fatalf("payload %+v", post)
				}
				if post.From != "" {
					t.Fatalf("from %q, want empty: the phone is not a pane", post.From)
				}
				return
			}
			want := wire.Msg{Action: cmd.ID, Target: req["target"], Dir: req["site"],
				Name: req["name"], Layout: req["layout"], Cwd: req["cwd"]}
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
