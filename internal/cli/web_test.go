package cli

// The web listen address: the flag, the config node it falls back on, and the
// line that says where the address came from.

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/keymap"
	"dm/internal/paths"
	"dm/internal/wire"
)

// webFlags parses a bare `dm web` against a config that holds src. An empty
// src writes no config file, so the run is a fresh install. It sets
// XDG_CONFIG_HOME, so the calling test must stay serial.
func webFlags(t *testing.T, src string, args ...string) *cmdFlags {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if src != "" {
		cfgDir := filepath.Join(dir, "dm")
		if err := os.MkdirAll(cfgDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, "config.kdl"), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := newFlagsAt(t.TempDir(), "web")
	if err := c.parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return c
}

// TestWebAddrDefault proves a bare dm web with no config file listens on the
// built-in loopback address and reports that the config did not supply it.
func TestWebAddrDefault(t *testing.T) {
	c := webFlags(t, "")
	addr, fromConfig, err := webAddr(c)
	if err != nil {
		t.Fatalf("webAddr: %v", err)
	}
	if addr != keymap.DefaultWebAddr {
		t.Fatalf("addr = %q, want %q", addr, keymap.DefaultWebAddr)
	}
	if fromConfig {
		t.Fatal("fromConfig = true, want false: no config file names the node")
	}
}

// TestWebAddrFromConfig proves the key drives a bare dm web and is reported as
// the source, which is the case the brief is about: the tailnet address needs
// no retyped --addr.
func TestWebAddrFromConfig(t *testing.T) {
	c := webFlags(t, `web-addr "you.ts.net:8099"`+"\n")
	addr, fromConfig, err := webAddr(c)
	if err != nil {
		t.Fatalf("webAddr: %v", err)
	}
	if addr != "you.ts.net:8099" {
		t.Fatalf("addr = %q, want the configured you.ts.net:8099", addr)
	}
	if !fromConfig {
		t.Fatal("fromConfig = false, want true")
	}
	var out bytes.Buffer
	announceWebAddr(&out, addr, fromConfig)
	if got := out.String(); !strings.Contains(got, "you.ts.net:8099") || !strings.Contains(got, "web-addr") {
		t.Fatalf("announce line = %q, want the configured address and the key", got)
	}
}

// TestWebAddrFlagWins proves an explicit --addr beats the config, including a
// value equal to the built-in default: the flag's Changed bit decides, so
// `dm web --addr 127.0.0.1:8099` is respected over a tailnet key.
func TestWebAddrFlagWins(t *testing.T) {
	c := webFlags(t, `web-addr "you.ts.net:8099"`+"\n", "--addr", keymap.DefaultWebAddr)
	addr, fromConfig, err := webAddr(c)
	if err != nil {
		t.Fatalf("webAddr: %v", err)
	}
	if addr != keymap.DefaultWebAddr {
		t.Fatalf("addr = %q, want the flag's %q", addr, keymap.DefaultWebAddr)
	}
	if fromConfig {
		t.Fatal("fromConfig = true, want false: the flag supplied the address")
	}
}

// TestWebAddrFlagWinsWithAnotherValue proves the flag wins when it names a
// value the config does not.
func TestWebAddrFlagWinsWithAnotherValue(t *testing.T) {
	c := webFlags(t, `web-addr "you.ts.net:8099"`+"\n", "--addr", "127.0.0.1:9100")
	addr, fromConfig, err := webAddr(c)
	if err != nil {
		t.Fatalf("webAddr: %v", err)
	}
	if addr != "127.0.0.1:9100" {
		t.Fatalf("addr = %q, want the flag's 127.0.0.1:9100", addr)
	}
	if fromConfig {
		t.Fatal("fromConfig = true, want false: the flag supplied the address")
	}
}

// TestWebAddrEmptyKeepsDefault proves an empty node keeps the built-in default
// and does not claim to be the source.
func TestWebAddrEmptyKeepsDefault(t *testing.T) {
	c := webFlags(t, `web-addr ""`+"\n")
	addr, fromConfig, err := webAddr(c)
	if err != nil {
		t.Fatalf("webAddr: %v", err)
	}
	if addr != keymap.DefaultWebAddr {
		t.Fatalf("addr = %q, want %q", addr, keymap.DefaultWebAddr)
	}
	if fromConfig {
		t.Fatal("fromConfig = true, want false: an empty node keeps the default")
	}
}

// TestWebAddrRefused proves a malformed value refuses the load with the key
// named and the value quoted, so a listener never silently falls back.
func TestWebAddrRefused(t *testing.T) {
	c := webFlags(t, `web-addr "not-an-address"`+"\n")
	addr, _, err := webAddr(c)
	if err == nil {
		t.Fatalf("webAddr = %q, want a refusal", addr)
	}
	if !strings.Contains(err.Error(), "web-addr") {
		t.Fatalf("error = %q, want the key named", err)
	}
	if !strings.Contains(err.Error(), `"not-an-address"`) {
		t.Fatalf("error = %q, want the value quoted", err)
	}
}

// TestWebTokenFromConfig proves a bare dm web serves the pinned token, which is
// the whole point of the node: a phone's URL keeps working across restarts and
// new session directories.
func TestWebTokenFromConfig(t *testing.T) {
	c := webFlags(t, `web-token "phonepin0001"`+"\n")
	token, fromConfig := webToken(c)
	if token != "phonepin0001" {
		t.Fatalf("token = %q, want the config's phonepin0001", token)
	}
	if !fromConfig {
		t.Fatal("fromConfig = false, want true: the config supplied the token")
	}
}

// TestWebTokenFlagWins proves --token beats the node, so a one-off run never
// rewrites the pinned token.
func TestWebTokenFlagWins(t *testing.T) {
	c := webFlags(t, `web-token "phonepin0001"`+"\n", "--token", "oneoff0002")
	token, fromConfig := webToken(c)
	if token != "oneoff0002" {
		t.Fatalf("token = %q, want the flag's oneoff0002", token)
	}
	if fromConfig {
		t.Fatal("fromConfig = true, want false: the flag supplied the token")
	}
}

// TestWebTokenAbsent proves a config without the node names no token, so the
// session file keeps whatever the last run used.
func TestWebTokenAbsent(t *testing.T) {
	c := webFlags(t, "")
	token, fromConfig := webToken(c)
	if token != "" {
		t.Fatalf("token = %q, want empty: the session file decides", token)
	}
	if fromConfig {
		t.Fatal("fromConfig = true, want false: no node names the token")
	}
}

// TestAnnounceWebAddrSilent proves the line is printed once and never when the
// address did not come from the config.
func TestAnnounceWebAddrSilent(t *testing.T) {
	var out bytes.Buffer
	announceWebAddr(&out, "you.ts.net:8099", false)
	if out.Len() != 0 {
		t.Fatalf("announce printed %q, want nothing", out.String())
	}
	announceWebAddr(&out, "you.ts.net:8099", true)
	if got := strings.Count(out.String(), "\n"); got != 1 {
		t.Fatalf("announce printed %d lines, want one: %q", got, out.String())
	}
}

// TestDaemonWebAsks proves the probe reads web-info from a daemon that answers
// and reports no answer when no daemon listens. Asking must never start one:
// dm web has to work before the first attach.
func TestDaemonWebAsks(t *testing.T) {
	const reply = `{"auto":true,"url":"http://127.0.0.1:9/?t=abc","addr":"127.0.0.1:9"}`
	startFakeServer(t, "web", func(_ net.Conn, req wire.Msg) (wire.Msg, bool) {
		if req.Action != "web-info" {
			return wire.Msg{OK: false, Error: "unknown action"}, false
		}
		return wire.Msg{OK: true, Res: json.RawMessage(reply)}, true
	})

	info, answered := daemonWeb(paths.StateDir(), "web")
	if !answered {
		t.Fatal("daemonWeb answered nothing for a live daemon")
	}
	if !info.Auto || info.URL != "http://127.0.0.1:9/?t=abc" || info.Addr != "127.0.0.1:9" {
		t.Fatalf("web-info = %+v", info)
	}
	if _, answered := daemonWeb(paths.StateDir(), "ghost"); answered {
		t.Fatal("daemonWeb answered for a session with no daemon")
	}
	if _, err := os.Stat(paths.SessionDirAt(paths.StateDir(), "ghost")); !os.IsNotExist(err) {
		t.Fatalf("the probe created a session directory: %v", err)
	}
}

// TestWebHandledByDaemon pins who owns the listener: the daemon only when the
// command asked for nothing and the daemon reports the key on with a URL.
func TestWebHandledByDaemon(t *testing.T) {
	const url = "http://127.0.0.1:8099/?t=abc"
	cases := []struct {
		name     string
		explicit bool
		info     webInfo
		answered bool
		served   bool
	}{
		{"daemon serves it", false, webInfo{Auto: true, URL: url}, true, true},
		{"auto off", false, webInfo{Auto: false}, true, false},
		{"bind failed", false, webInfo{Auto: true, Error: "bind: address already in use"}, true, false},
		{"auto on, no url", false, webInfo{Auto: true}, true, false},
		{"no daemon", false, webInfo{}, false, false},
		{"explicit addr", true, webInfo{Auto: true, URL: url}, true, false},
		{"explicit addr, no daemon", true, webInfo{}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, served := webHandledByDaemon(c.explicit, c.info, c.answered)
			if served != c.served {
				t.Fatalf("served = %v, want %v", served, c.served)
			}
			if served != (got != "") {
				t.Fatalf("url = %q with served = %v", got, served)
			}
			if c.served && got != url {
				t.Fatalf("url = %q, want %q", got, url)
			}
		})
	}
}

// TestAnnounceWebServed proves the delegated run prints the daemon's link and
// says the daemon serves it, so an operator is not left waiting for a listener
// this command never bound.
func TestAnnounceWebServed(t *testing.T) {
	var out bytes.Buffer
	announceWebServed(&out, "main", "http://127.0.0.1:8099/?t=abc")
	got := out.String()
	for _, want := range []string{"session main", "http://127.0.0.1:8099/?t=abc", "already serves it"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q does not name %q", got, want)
		}
	}
}
