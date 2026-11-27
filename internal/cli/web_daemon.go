package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"dm/internal/paths"
	"dm/internal/wire"
)

// webInfo is one daemon's answer to the web-info action: whether it started
// the web listener on its own, the link it bound, and the bind failure when it
// did not.
type webInfo struct {
	Auto  bool   `json:"auto"`
	URL   string `json:"url"`
	Addr  string `json:"addr"`
	Error string `json:"error"`
}

// daemonWeb asks the session's daemon where its web listener stands. It dials
// only a daemon that already answers: asking must never start one, because
// `dm web` has to work before the first attach. A dial or wire failure reports
// no answer, and the caller keeps its own listener path.
func daemonWeb(root, name string) (webInfo, bool) {
	if !paths.ValidSessionName(name) {
		return webInfo{}, false
	}
	conn, err := net.DialTimeout("unix", paths.ServerSockAt(root, name), 300*time.Millisecond)
	if err != nil {
		return webInfo{}, false
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := wire.WriteMsg(conn, wire.Msg{Type: "req", Action: "web-info", ID: 1}); err != nil {
		return webInfo{}, false
	}
	for {
		m, err := wire.ReadMsg(conn)
		if err != nil {
			return webInfo{}, false
		}
		if m.Type != "resp" {
			continue
		}
		if !m.OK {
			return webInfo{}, false
		}
		var info webInfo
		if err := json.Unmarshal(m.Res, &info); err != nil {
			return webInfo{}, false
		}
		return info, true
	}
}

// webHandledByDaemon decides who owns the listener. The daemon keeps it only
// when the command asked for nothing and the daemon reports the key on with a
// URL: a second listener on the same address would only fail. An explicit
// --addr or --token, a daemon that is not running, and a daemon that bound
// nothing all leave the listener to this run.
func webHandledByDaemon(explicit bool, info webInfo, answered bool) (string, bool) {
	if explicit || !answered || !info.Auto || info.URL == "" {
		return "", false
	}
	return info.URL, true
}

// announceWebServed prints the link the daemon already answers on, and why
// this run binds nothing.
func announceWebServed(w io.Writer, session, url string) {
	fmt.Fprintf(w, "dm web: session %s\n  %s\n", session, url)
	fmt.Fprintln(w, "dm web: the session daemon already serves it: pass --addr or --token to bind your own")
}
