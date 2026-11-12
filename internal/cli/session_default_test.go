package cli

// The session default: DM_SESSION decides when -s does not, but only when the
// session is live; a dead one falls back to main with one stderr line.

import (
	"net"
	"os"
	"strings"
	"testing"

	"dm/internal/paths"
)

// startFakeServers listens for several sessions under one DM_STATE, so their
// sockets share the state root the verbs resolve. The shared fake_server_test
// helper pins DM_STATE per call, so a second call would move the root.
func startFakeServers(t *testing.T, hs map[string]fakeHandler) {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	for name, h := range hs {
		if err := os.MkdirAll(paths.SessionDir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		sock := paths.ServerSock(name)
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
				go serveFake(c, h)
			}
		}()
	}
}

// TestEnvSessionLivenessDecidesTheDefault proves the env default survives only
// when the session answers: a live DM_SESSION keeps the verb on it, a dead one
// falls back to main with one stderr line naming both sessions.
// Stays serial: the fake servers and the stderr swap are process-wide.
func TestEnvSessionLivenessDecidesTheDefault(t *testing.T) {
	var live, main recorder
	startFakeServers(t, map[string]fakeHandler{
		"live": live.handler(),
		"main": main.handler(),
	})

	t.Run("live env session keeps the verb on it", func(t *testing.T) {
		t.Setenv("DM_SESSION", "live")
		var err error
		stderr := captureStderr(t, func() { err = Run([]string{"status"}) })
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if len(live.got) == 0 || live.last().Action != "status" {
			t.Fatalf("status never reached the live session: %v", live.got)
		}
		if len(main.got) != 0 {
			t.Fatalf("status leaked to main: %v", main.got)
		}
		if stderr != "" {
			t.Fatalf("stderr on the live path = %q, want empty", stderr)
		}
	})

	t.Run("dead env session falls back to main", func(t *testing.T) {
		t.Setenv("DM_SESSION", "doomed")
		var err error
		stderr := captureStderr(t, func() { err = Run([]string{"status"}) })
		if err != nil {
			t.Fatalf("status after fallback: %v", err)
		}
		if len(main.got) == 0 || main.last().Action != "status" {
			t.Fatalf("status never reached the fallback session: %v", main.got)
		}
		if strings.Count(stderr, "\n") != 1 {
			t.Fatalf("stderr lines = %d, want 1: %q", strings.Count(stderr, "\n"), stderr)
		}
		for _, want := range []string{"doomed", "main"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("stderr %q misses %q", stderr, want)
			}
		}
	})
}

// TestSessionFlagNeverConsultsTheEnv proves an explicit -s wins over a dead
// DM_SESSION and stays silent: the flag run never asks whether the env session
// is live.
// Stays serial: t.Setenv cannot run in a parallel test.
func TestSessionFlagNeverConsultsTheEnv(t *testing.T) {
	var live recorder
	startFakeServer(t, "live", live.handler())
	t.Setenv("DM_SESSION", "doomed")
	var err error
	stderr := captureStderr(t, func() { err = Run([]string{"status", "-s", "live"}) })
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(live.got) == 0 || live.last().Action != "status" {
		t.Fatalf("status never reached the flagged session: %v", live.got)
	}
	if stderr != "" {
		t.Fatalf("stderr with an explicit -s = %q, want empty", stderr)
	}
}

// TestResolveSessionChecksOnce proves the verdict is cached: a second resolve
// re-dials nothing and warns nothing, so one verb run checks liveness once.
func TestResolveSessionChecksOnce(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	t.Setenv("DM_SESSION", "doomed")
	c := newFlags("msg")
	stderr := captureStderr(t, func() { c.parse([]string{"list"}) })
	if c.session != "main" {
		t.Fatalf("session = %q, want main", c.session)
	}
	if !strings.Contains(stderr, "doomed") {
		t.Fatalf("parse stderr %q misses doomed", stderr)
	}
	again := captureStderr(t, func() { c.resolveSession() })
	if again != "" {
		t.Fatalf("second resolve wrote %q, want silence", again)
	}
}
