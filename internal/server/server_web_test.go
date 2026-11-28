package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/keymap"
	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/wire"
)

// webAutoServer is a pane-less server whose config carries src, with the
// always-on listener started. It binds no fixed port: the config in these
// tests asks for 127.0.0.1:0 and the kernel chooses. The returned root is the
// state tree the listener keeps its token in.
func webAutoServer(t *testing.T, src string) (*server, string) {
	t.Helper()
	cfg, err := keymap.ParseConfig(src)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	root := t.TempDir()
	s := railServer(session.NewStateAt(root, "webauto"))
	s.cfg = cfg
	s.startWeb(root)
	t.Cleanup(s.closeWeb)
	return s, root
}

// webReply sends one web-info request and decodes its answer.
func webReply(t *testing.T, s *server) webInfoReply {
	t.Helper()
	cl, conn := boxConn(t)
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "web-info"})
	if !m.OK {
		t.Fatalf("web-info failed: %s", m.Error)
	}
	var out webInfoReply
	if err := json.Unmarshal(m.Res, &out); err != nil {
		t.Fatalf("web-info reply: %v", err)
	}
	return out
}

// TestWebInfoServes proves web-auto on gives the daemon a listener it owns:
// web-info answers the URL with its token and the address the kernel picked,
// and the page on that link answers HTTP.
func TestWebInfoServes(t *testing.T) {
	s, root := webAutoServer(t, "prefix \"ctrl+b\"\nweb-auto true\nweb-addr \"127.0.0.1:0\"")
	got := webReply(t, s)
	if !got.Auto {
		t.Fatal("auto = false, want true")
	}
	if got.Error != "" {
		t.Fatalf("error = %q, want none", got.Error)
	}
	tok, err := os.ReadFile(filepath.Join(paths.SessionDirAt(root, "webauto"), "web.token"))
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	u, err := url.Parse(got.URL)
	if err != nil {
		t.Fatalf("url %q: %v", got.URL, err)
	}
	if u.Scheme != "http" || u.Host != got.Addr {
		t.Fatalf("url %q does not sit on addr %q", got.URL, got.Addr)
	}
	if got := u.Query().Get("t"); got == "" || got != strings.TrimSpace(string(tok)) {
		t.Fatalf("url token = %q, want the session's %q", got, strings.TrimSpace(string(tok)))
	}
	if _, port, err := net.SplitHostPort(got.Addr); err != nil || port == "0" {
		t.Fatalf("addr = %q, want the bound port", got.Addr)
	}

	res, err := http.Get(got.URL)
	if err != nil {
		t.Fatalf("GET %s: %v", got.URL, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
}

// TestWebInfoOffBindsNothing proves an absent web-auto answers auto false with
// no link and binds nothing.
func TestWebInfoOffBindsNothing(t *testing.T) {
	s, _ := webAutoServer(t, `prefix "ctrl+b"`)
	got := webReply(t, s)
	if got.Auto || got.URL != "" || got.Addr != "" || got.Error != "" {
		t.Fatalf("web-info = %+v, want every field empty", got)
	}
	s.mu.Lock()
	web, webURL := s.web, s.webURL
	s.mu.Unlock()
	if web != nil || webURL != "" {
		t.Fatalf("web = %v url = %q, want no listener", web, webURL)
	}
}

// TestWebInfoBindFailure proves a bind the address refuses is reported, with no
// link, and the session keeps its socket. The address under test is a listener
// this test holds, so the port is still one the kernel picked.
func TestWebInfoBindFailure(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold an address: %v", err)
	}
	t.Cleanup(func() { _ = held.Close() })
	s, _ := webAutoServer(t, "prefix \"ctrl+b\"\nweb-auto true\nweb-addr \""+held.Addr().String()+"\"")

	got := webReply(t, s)
	if got.Auto {
		t.Fatal("auto = true, want false: a failed bind holds no listener")
	}
	if got.Error == "" {
		t.Fatal("error is empty, want the bind failure")
	}
	if got.URL != "" || got.Addr != "" {
		t.Fatalf("web-info = %+v, want an empty link on a failed bind", got)
	}
}

// TestWebInfoFollowsTheLiveListener proves the answer describes the listener,
// not the flag. A reload swaps s.cfg and starts nothing, so turning web-auto off
// leaves the listener serving: web-info must keep naming it, or dm web would try
// to bind a port the daemon holds.
func TestWebInfoFollowsTheLiveListener(t *testing.T) {
	s, _ := webAutoServer(t, "prefix \"ctrl+b\"\nweb-auto true\nweb-addr \"127.0.0.1:0\"")
	live := webReply(t, s)
	if !live.Auto || live.URL == "" {
		t.Fatalf("a live listener answers %+v, want auto true with its URL", live)
	}
	// This is what a reload leaves behind: a new config and the same listener.
	s.mu.Lock()
	s.cfg = keymap.LoadDefault()
	s.mu.Unlock()
	got := webReply(t, s)
	if !got.Auto || got.URL != live.URL || got.Addr != live.Addr {
		t.Fatalf("after a reload off the answer = %+v, want the live listener %+v", got, live)
	}
}

// TestWebInfoOffAfterReloadOn proves the other direction: a daemon started with
// the key off holds no listener, and a reload that turns the key on cannot make
// web-info claim one.
func TestWebInfoOffAfterReloadOn(t *testing.T) {
	s, _ := webAutoServer(t, `prefix "ctrl+b"`)
	cfg, err := keymap.ParseConfig("prefix \"ctrl+b\"\nweb-auto true\nweb-addr \"127.0.0.1:0\"")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	got := webReply(t, s)
	if got.Auto || got.URL != "" || got.Addr != "" || got.Error != "" {
		t.Fatalf("web-info = %+v, want no listener and no error", got)
	}
}

// TestTerminateClosesWeb proves the listener goes with the daemon, so a later
// dm web on the same address binds instead of failing.
func TestTerminateClosesWeb(t *testing.T) {
	s, _ := webAutoServer(t, "prefix \"ctrl+b\"\nweb-auto true\nweb-addr \"127.0.0.1:0\"")
	got := webReply(t, s)
	if got.URL == "" {
		t.Fatal("web-info answers no url")
	}
	s.exit = func(int) {}
	s.terminate()

	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	res, err := client.Get(got.URL)
	if err == nil {
		_ = res.Body.Close()
		t.Fatalf("GET %s answered %d after terminate, want a closed listener", got.URL, res.StatusCode)
	}
}

// TestSnapshotCarriesWeb proves the snapshot tells the client where the
// listener stands. The client paints the status bar's web mark from this flag,
// so it must follow the listener and not the config: web-auto on carries true
// and an absent key carries false.
func TestSnapshotCarriesWeb(t *testing.T) {
	on, _ := webAutoServer(t, "prefix \"ctrl+b\"\nweb-auto true\nweb-addr \"127.0.0.1:0\"")
	if !snapshot(on).Web {
		t.Fatal("snapshot web = false, want true while the daemon serves the page")
	}
	off, _ := webAutoServer(t, `prefix "ctrl+b"`)
	if snapshot(off).Web {
		t.Fatal("snapshot web = true, want false with no listener")
	}
}
