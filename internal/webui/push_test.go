package webui

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"dm/internal/paths"
	"dm/internal/push"
)

// pushServer serves one session over a test server and returns it with the
// session directory, so a test can read the files the handlers wrote.
func pushServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "t"}
	srv := httptest.NewServer(handler(cfg))
	t.Cleanup(srv.Close)
	return srv, paths.SessionDirAt(cfg.Root, cfg.Session)
}

// subPoint is a valid p256dh: base64url of a 65-byte uncompressed point.
func subPoint() string {
	point := make([]byte, 65)
	point[0] = 0x04
	for i := 1; i < len(point); i++ {
		point[i] = byte(i)
	}
	return base64.RawURLEncoding.EncodeToString(point)
}

// subAuth is a valid auth secret: base64url of 16 bytes.
func subAuth() string {
	return base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
}

// subBody is one subscribe request as the page sends it.
func subBody(endpoint, point, auth string) map[string]string {
	return map[string]string{"endpoint": endpoint, "p256dh": point, "auth": auth}
}

// readStore loads the session's subscription file the way the daemon does.
func readStore(t *testing.T, dir string) []push.Subscription {
	t.Helper()
	store, err := push.Load(push.StorePath(dir))
	if err != nil {
		t.Fatalf("load %s: %v", push.StorePath(dir), err)
	}
	return store.List()
}

// config reads /api/push/config with the token.
func config(t *testing.T, srv *httptest.Server) pushConfigReply {
	t.Helper()
	res := get(t, srv.URL+"/api/push/config", "?t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("config status %d, want 200", res.StatusCode)
	}
	var out pushConfigReply
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("config body: %v", err)
	}
	return out
}

// TestPushConfigKeepsOneKey proves the endpoint reports a usable key and that a
// second call reports the same one: a key that changed would invalidate every
// subscription the phone already holds.
func TestPushConfigKeepsOneKey(t *testing.T) {
	srv, dir := pushServer(t)
	first := config(t, srv)
	if !first.Enabled {
		t.Fatal("enabled = false, want true")
	}
	point, err := base64.RawURLEncoding.DecodeString(first.PublicKey)
	if err != nil {
		t.Fatalf("publicKey is not base64url: %v", err)
	}
	if len(point) != 65 || point[0] != 0x04 {
		t.Fatalf("publicKey is %d bytes starting %#x, want a 65-byte point", len(point), point[0])
	}
	second := config(t, srv)
	if second.PublicKey != first.PublicKey {
		t.Fatalf("second call reported %q, want %q", second.PublicKey, first.PublicKey)
	}
	// The pair is on disk where the daemon looks for it.
	if _, err := os.Stat(push.KeysPath(dir)); err != nil {
		t.Fatalf("key file: %v", err)
	}
}

// TestPushConfigWithoutAKeyFile proves a session that refuses the write still
// answers, with enabled false and no path or error text.
func TestPushConfigWithoutAKeyFile(t *testing.T) {
	srv, dir := pushServer(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	out := config(t, srv)
	if out.Enabled {
		t.Fatal("enabled = true, want false when the pair cannot be created")
	}
	if out.PublicKey != "" {
		t.Fatalf("publicKey = %q, want empty", out.PublicKey)
	}
	res := get(t, srv.URL+"/api/push/config", "?t=t")
	text := body(t, res)
	if strings.Contains(text, dir) {
		t.Fatalf("config body names the session directory: %s", text)
	}
}

// TestPushSubscribeStoresThePhone proves one subscription lands in the file the
// daemon reads.
func TestPushSubscribeStoresThePhone(t *testing.T) {
	srv, dir := pushServer(t)
	endpoint := "https://push.example.com/abc"
	res := post(t, srv.URL+"/api/push/subscribe?t=t", subBody(endpoint, subPoint(), subAuth()))
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, want 204: %s", res.StatusCode, body(t, res))
	}
	subs := readStore(t, dir)
	if len(subs) != 1 || subs[0].Endpoint != endpoint {
		t.Fatalf("store = %+v, want the one endpoint", subs)
	}
	if subs[0].Auth != subAuth() || subs[0].P256DH != subPoint() {
		t.Fatalf("store kept %+v, want the keys the page sent", subs[0])
	}
	if subs[0].At.IsZero() {
		t.Error("At is zero, want the time dm accepted the phone")
	}
}

// TestPushSubscribeReplacesTheEndpoint proves a phone that subscribes again
// takes its own row rather than adding a second one.
func TestPushSubscribeReplacesTheEndpoint(t *testing.T) {
	srv, dir := pushServer(t)
	endpoint := "https://push.example.com/abc"
	first := post(t, srv.URL+"/api/push/subscribe?t=t", subBody(endpoint, subPoint(), subAuth()))
	_ = first.Body.Close()
	if first.StatusCode != http.StatusNoContent {
		t.Fatalf("first status %d", first.StatusCode)
	}
	other := base64.RawURLEncoding.EncodeToString([]byte("fedcba9876543210"))
	second := post(t, srv.URL+"/api/push/subscribe?t=t", subBody(endpoint, subPoint(), other))
	defer func() { _ = second.Body.Close() }()
	if second.StatusCode != http.StatusNoContent {
		t.Fatalf("second status %d", second.StatusCode)
	}
	subs := readStore(t, dir)
	if len(subs) != 1 {
		t.Fatalf("store = %d entries, want 1", len(subs))
	}
	if subs[0].Auth != other {
		t.Fatalf("store kept auth %q, want the newer one", subs[0].Auth)
	}
}

// TestPushUnsubscribeIsIdempotent proves the endpoint removes the row and that
// a second call is still a success, because a phone may unsubscribe twice.
func TestPushUnsubscribeIsIdempotent(t *testing.T) {
	srv, dir := pushServer(t)
	endpoint := "https://push.example.com/abc"
	sub := post(t, srv.URL+"/api/push/subscribe?t=t", subBody(endpoint, subPoint(), subAuth()))
	_ = sub.Body.Close()

	gone := post(t, srv.URL+"/api/push/unsubscribe?t=t", map[string]string{"endpoint": endpoint})
	if gone.StatusCode != http.StatusNoContent {
		t.Fatalf("unsubscribe status %d: %s", gone.StatusCode, body(t, gone))
	}
	_ = gone.Body.Close()
	if subs := readStore(t, dir); len(subs) != 0 {
		t.Fatalf("store = %+v, want no phones", subs)
	}
	again := post(t, srv.URL+"/api/push/unsubscribe?t=t", map[string]string{"endpoint": endpoint})
	defer func() { _ = again.Body.Close() }()
	if again.StatusCode != http.StatusNoContent {
		t.Fatalf("second unsubscribe status %d, want 204", again.StatusCode)
	}
}

// TestPushSubscribeRejectsMalformedBodies proves every body that is not a real
// browser subscription is a 400 with a short reason and no echo of the body.
func TestPushSubscribeRejectsMalformedBodies(t *testing.T) {
	srv, dir := pushServer(t)
	// A 64-byte point, and a point that does not start with the 0x04 tag.
	shortPoint := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	taggedPoint := base64.RawURLEncoding.EncodeToString(append([]byte{0x05}, make([]byte, 64)...))
	// 15 bytes of auth.
	shortAuth := base64.RawURLEncoding.EncodeToString(make([]byte, 15))

	cases := []struct {
		name string
		body string
	}{
		{name: "empty", body: ""},
		{name: "not json", body: "endpoint=https://push.example.com/a"},
		{name: "no endpoint", body: `{"p256dh":"` + subPoint() + `","auth":"` + subAuth() + `"}`},
		{name: "relative endpoint", body: `{"endpoint":"/push/a","p256dh":"` + subPoint() + `","auth":"` + subAuth() + `"}`},
		{name: "no scheme", body: `{"endpoint":"push.example.com/a","p256dh":"` + subPoint() + `","auth":"` + subAuth() + `"}`},
		{name: "plain http", body: `{"endpoint":"http://push.example.com/a","p256dh":"` + subPoint() + `","auth":"` + subAuth() + `"}`},
		{name: "other scheme", body: `{"endpoint":"ftp://push.example.com/a","p256dh":"` + subPoint() + `","auth":"` + subAuth() + `"}`},
		{name: "no p256dh", body: `{"endpoint":"https://push.example.com/a","auth":"` + subAuth() + `"}`},
		{name: "short p256dh", body: `{"endpoint":"https://push.example.com/a","p256dh":"` + shortPoint + `","auth":"` + subAuth() + `"}`},
		{name: "untagged p256dh", body: `{"endpoint":"https://push.example.com/a","p256dh":"` + taggedPoint + `","auth":"` + subAuth() + `"}`},
		{name: "p256dh not base64url", body: `{"endpoint":"https://push.example.com/a","p256dh":"!!!!","auth":"` + subAuth() + `"}`},
		{name: "short auth", body: `{"endpoint":"https://push.example.com/a","p256dh":"` + subPoint() + `","auth":"` + shortAuth + `"}`},
		{name: "auth not base64url", body: `{"endpoint":"https://push.example.com/a","p256dh":"` + subPoint() + `","auth":"!!!!"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := rawPost(t, srv.URL+"/api/push/subscribe?t=t", tc.body)
			text := body(t, res)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", res.StatusCode, text)
			}
			if strings.Contains(text, "push.example.com") {
				t.Fatalf("the refusal echoed the body: %s", text)
			}
			if len(strings.TrimSpace(text)) > 80 {
				t.Fatalf("refusal is %d bytes, want a short reason: %s", len(text), text)
			}
		})
	}
	if subs := readStore(t, dir); len(subs) != 0 {
		t.Fatalf("store holds %+v after every body was refused", subs)
	}
}

// TestPushUnsubscribeNeedsAnEndpoint proves a body with no endpoint is refused
// instead of quietly matching nothing.
func TestPushUnsubscribeNeedsAnEndpoint(t *testing.T) {
	srv, _ := pushServer(t)
	for _, raw := range []string{"", "not json", `{}`, `{"endpoint":""}`} {
		res := rawPost(t, srv.URL+"/api/push/unsubscribe?t=t", raw)
		text := body(t, res)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %q: status %d, want 400: %s", raw, res.StatusCode, text)
		}
	}
}

// TestPushSubscribeReportsAFailedWrite proves a store the session directory
// will not take is a 500, and that config keeps answering beside it.
func TestPushSubscribeReportsAFailedWrite(t *testing.T) {
	srv, dir := pushServer(t)
	// The pair is written first, so config has something to report while the
	// directory refuses the store write.
	if out := config(t, srv); !out.Enabled {
		t.Fatal("enabled = false before the directory was tightened")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if out := config(t, srv); !out.Enabled {
		t.Error("enabled = false, want the pair read from the file")
	}
	res := post(t, srv.URL+"/api/push/subscribe?t=t", subBody("https://push.example.com/a", subPoint(), subAuth()))
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", res.StatusCode, body(t, res))
	}
}

// TestPushEndpointsNeedTheToken proves the subscription endpoints stay behind
// the token the rest of the surface uses.
func TestPushEndpointsNeedTheToken(t *testing.T) {
	srv, _ := pushServer(t)
	cases := []struct {
		name string
		do   func() *http.Response
	}{
		{name: "config", do: func() *http.Response {
			return get(t, srv.URL+"/api/push/config", "")
		}},
		{name: "subscribe", do: func() *http.Response {
			return post(t, srv.URL+"/api/push/subscribe", subBody("https://push.example.com/a", subPoint(), subAuth()))
		}},
		{name: "unsubscribe", do: func() *http.Response {
			return post(t, srv.URL+"/api/push/unsubscribe", map[string]string{"endpoint": "https://push.example.com/a"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.do()
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", res.StatusCode)
			}
		})
	}
}

// rawPost sends one body verbatim, so a test can send what a browser should
// never send.
func rawPost(t *testing.T, url, text string) *http.Response {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return res
}
