package cli

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"dm/internal/testutil"
	"dm/internal/wire"
)

// TestSecretRegisterSendsTheShape proves the register verb forwards every
// field and accepts its positional.
func TestSecretRegisterSendsTheShape(t *testing.T) {
	rec := &recorder{}
	startFakeServer(t, "main", func(c net.Conn, req wire.Msg) (wire.Msg, bool) {
		rec.handler()(c, req)
		return wire.Msg{OK: true, Res: json.RawMessage(`{"name":"gh"}`)}, true
	})
	if err := Run([]string{"secret", "register", "gh", "--tool", "op", "--ref", "op://Personal/gh/token",
		"--upstream", "https://api.github.com", "--header", "Authorization: Bearer {}"}); err != nil {
		t.Fatalf("dm secret register: %v", err)
	}
	got := rec.last()
	if got.Action != "secret-register" || got.Name != "gh" || got.Tool != "op" ||
		got.Ref != "op://Personal/gh/token" || got.Upstream != "https://api.github.com" ||
		got.Header != "Authorization: Bearer {}" {
		t.Fatalf("register sent %+v", got)
	}
}

// TestSecretRegisterValidation proves the local checks fire before anything
// dials: no server, no socket, just the error.
func TestSecretRegisterValidation(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	cases := [][]string{
		{"secret", "register"},
		{"secret", "register", "gh"},
		{"secret", "register", "gh", "--tool", "vault", "--ref", "r"},
		{"secret", "register", "gh", "--tool", "op"},
		{"secret", "register", "gh", "--tool", "op", "--ref", "op://x/y", "extra"},
		{"secret", "hydrate", "gh"},
		{"secret", "type", "gh"},
		{"secret", "revoke"},
		{"secret", "use"},
		{"secret", "bogus"},
		{"secret", "list", "extra"},
	}
	for _, argv := range cases {
		if err := Run(argv); err == nil {
			t.Fatalf("dm %v succeeded, want a refusal", argv)
		}
	}
}

// TestSecretUsePrintsOnlyTheEndpoint proves the verb prints the endpoint
// alone: a reply that carries the value leaves nothing on stdout.
func TestSecretUsePrintsOnlyTheEndpoint(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"name":"gh","endpoint":"http://127.0.0.1:9123/abc/","value":"SUPERSECRET"}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"secret", "use", "gh"}); err != nil {
			t.Fatalf("dm secret use: %v", err)
		}
	})
	if strings.TrimSpace(out) != "http://127.0.0.1:9123/abc/" {
		t.Fatalf("dm secret use printed %q, want the endpoint alone", out)
	}
}

// TestSecretListPrintsNamesAndReferences proves the list verb prints the two
// columns only, even when the reply carries more.
func TestSecretListPrintsNamesAndReferences(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"secrets":[{"name":"gh","reference":"op://x/y","upstream":"https://api.github.com"}]}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"secret", "list"}); err != nil {
			t.Fatalf("dm secret list: %v", err)
		}
	})
	if out != "gh\top://x/y\n" {
		t.Fatalf("dm secret list printed %q", out)
	}
}

// TestSecretTypeSendsTarget proves the type verb rides Target.
func TestSecretTypeSendsTarget(t *testing.T) {
	rec := &recorder{}
	startFakeServer(t, "main", func(c net.Conn, req wire.Msg) (wire.Msg, bool) {
		rec.handler()(c, req)
		return wire.Msg{OK: true, Res: json.RawMessage(`{"pane":"w1p1"}`)}, true
	})
	if err := Run([]string{"secret", "type", "gh", "--to", "w1p1"}); err != nil {
		t.Fatalf("dm secret type: %v", err)
	}
	if got := rec.last(); got.Action != "secret-type" || got.Name != "gh" || got.Target != "w1p1" {
		t.Fatalf("type sent %+v", got)
	}
}

// TestSecretBarePrintsSubcommands proves the bare verb prints the list.
func TestSecretBarePrintsSubcommands(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"secret"}); err != nil {
			t.Fatalf("dm secret: %v", err)
		}
	})
	for _, want := range []string{"dm secret register", "dm secret list", "dm secret use"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dm secret printed %q, want %q", out, want)
		}
	}
}
