package cli

// The web listen address: the flag, the config node it falls back on, and the
// line that says where the address came from.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/keymap"
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
