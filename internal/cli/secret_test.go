package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"path/filepath"
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

// TestSecretRegisterForwardsEnv proves each --env occurrence rides the
// request as one entry.
func TestSecretRegisterForwardsEnv(t *testing.T) {
	rec := &recorder{}
	startFakeServer(t, "main", func(c net.Conn, req wire.Msg) (wire.Msg, bool) {
		rec.handler()(c, req)
		return wire.Msg{OK: true, Res: json.RawMessage(`{"name":"gh"}`)}, true
	})
	if err := Run([]string{"secret", "register", "gh", "--tool", "pass", "--ref", "dev/t",
		"--env", "PASSWORD_STORE_DIR=/p", "--env", "GNUPGHOME=/g"}); err != nil {
		t.Fatalf("dm secret register: %v", err)
	}
	got := rec.last()
	if len(got.Env) != 2 || got.Env[0] != "PASSWORD_STORE_DIR=/p" || got.Env[1] != "GNUPGHOME=/g" {
		t.Fatalf("register sent env %v", got.Env)
	}
}

// TestSecretUseWithoutUpstreamExplainsItself proves the redaction-only use
// says so on stderr instead of printing an empty endpoint.
func TestSecretUseWithoutUpstreamExplainsItself(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"name":"gh","endpoint":""}`))
	var err error
	var errOut string
	out := testutil.CaptureStdout(t, func() {
		errOut = captureStderr(t, func() {
			err = Run([]string{"secret", "use", "gh"})
		})
	})
	if err != nil {
		t.Fatalf("dm secret use: %v", err)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want nothing", out)
	}
	if !strings.Contains(errOut, "rides no proxy") {
		t.Fatalf("stderr = %q, want the explanation", errOut)
	}
}

// TestSecretUseSurfacesTheWarning proves a scrub warning reaches stderr and
// never stdout.
func TestSecretUseSurfacesTheWarning(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"name":"pin","endpoint":"http://127.0.0.1:1/x/","warning":"secret pin: value is 3 bytes; panes do not redact values under 4 bytes, so it will echo unredacted"}`))
	var err error
	var errOut string
	out := testutil.CaptureStdout(t, func() {
		errOut = captureStderr(t, func() {
			err = Run([]string{"secret", "use", "pin"})
		})
	})
	if err != nil {
		t.Fatalf("dm secret use: %v", err)
	}
	if strings.TrimSpace(out) != "http://127.0.0.1:1/x/" {
		t.Fatalf("stdout = %q, want the endpoint alone", out)
	}
	if !strings.Contains(errOut, "secret pin") {
		t.Fatalf("stderr = %q, want the warning", errOut)
	}
}

// TestSecretBarePrintsTheVerbs proves dm secret with no subverb prints the
// group's own usage: one row per subverb, so the group explains itself.
func TestSecretBarePrintsTheVerbs(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"secret"}); err != nil {
			t.Fatalf("dm secret: %v", err)
		}
	})
	for _, want := range []string{
		"dm secret register", "dm secret list", "dm secret revoke",
		"dm secret use", "dm secret hydrate", "dm secret type",
		"dm secret <verb> -h",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dm secret does not name %q: %q", want, out)
		}
	}
}

// TestSecretHelpPrintsTheVerbs proves dm secret -h prints the same subverb
// list the bare verb prints: the usage printer falls back to the group's
// list, because the table has no bare secret row of its own.
func TestSecretHelpPrintsTheVerbs(t *testing.T) {
	t.Parallel()
	c := newFlagsAt(t.TempDir(), "secret")
	c.parse(nil)
	var b bytes.Buffer
	c.fs.SetOutput(&b)
	c.fs.Usage()
	for _, want := range []string{
		"dm secret register", "dm secret list", "dm secret revoke",
		"dm secret use", "dm secret hydrate", "dm secret type",
		"dm secret <verb> -h",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("dm secret -h does not name %q: %q", want, b.String())
		}
	}
}

// TestSecretSyncSendsTheShape proves the sync verb sends an absolute mapping
// path: --from resolves against the working directory, and with no --from
// the mapping is the working directory's .dm-secrets.kdl.
func TestSecretSyncSendsTheShape(t *testing.T) {
	cases := []struct {
		argv    []string
		wantEnd string
	}{
		{[]string{"secret", "sync"}, ".dm-secrets.kdl"},
		{[]string{"secret", "sync", "--from", "cfg/secrets.kdl"}, filepath.Join("cfg", "secrets.kdl")},
	}
	for _, tc := range cases {
		rec := &recorder{}
		startFakeServer(t, "main", func(c net.Conn, req wire.Msg) (wire.Msg, bool) {
			rec.handler()(c, req)
			return wire.Msg{OK: true, Res: json.RawMessage(`{"mapping":"/x","registered":[],"kept":[],"files":[],"warning":"","uncovered":[]}`)}, true
		})
		if err := Run(tc.argv); err != nil {
			t.Fatalf("dm %v: %v", tc.argv, err)
		}
		got := rec.last()
		if got.Action != "secret-sync" {
			t.Fatalf("%v sent %+v", tc.argv, got)
		}
		if !filepath.IsAbs(got.Path) || !strings.HasSuffix(got.Path, tc.wantEnd) {
			t.Fatalf("%v sent path %q, want it to end in %q", tc.argv, got.Path, tc.wantEnd)
		}
	}
}

// TestSecretSyncRefusesExtraArguments proves the verb takes no positional.
func TestSecretSyncRefusesExtraArguments(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	if err := Run([]string{"secret", "sync", "extra"}); err == nil {
		t.Fatal("dm secret sync extra succeeded, want a refusal")
	}
}
