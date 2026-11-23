package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPageRecoversWithoutAToken proves a phone that opens the bare address
// gets a form that carries it back to the token, not a dead 401.
func TestPageRecoversWithoutAToken(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "s3cret"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	for _, query := range []string{"", "?t=nope"} {
		res, err := http.Get(srv.URL + "/" + query)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%q: status %d, want 401", query, res.StatusCode)
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%q: content type %q, want HTML", query, ct)
		}
		if got := res.Header.Get("WWW-Authenticate"); got != "" {
			t.Fatalf("%q: the recovery page asks for a password: %q", query, got)
		}
		page := body(t, res)
		for _, want := range []string{`id="t"`, `name="t"`, "<form", "encodeURIComponent", "/?t=", "dm web"} {
			if !strings.Contains(page, want) {
				t.Errorf("%q: page lacks %q", query, want)
			}
		}
	}
}

// TestPageWithTokenServesTheApp proves the recovery form is only the fallback:
// a token-bearing GET still answers the real page.
func TestPageWithTokenServesTheApp(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "s3cret"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	res := get(t, srv.URL, "/?t=s3cret")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	page := body(t, res)
	if !strings.Contains(page, "/static/app.js") {
		t.Fatal("no app script in the page")
	}
	if strings.Contains(page, "encodeURIComponent") {
		t.Fatal("the real page answered with the recovery form")
	}
}

// TestRecoveryIsPageGoOnly keeps the recovery form a page affordance: every
// other route, and every other method, keeps the plain 401.
func TestRecoveryIsPageGoOnly(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "s3cret"}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	cases := []struct {
		name, method, path string
	}{
		{name: "POST to the page", method: http.MethodPost, path: "/"},
		{name: "HEAD the page", method: http.MethodHead, path: "/"},
		{name: "GET an api route", method: http.MethodGet, path: "/api/fleet"},
		{name: "POST an api route", method: http.MethodPost, path: "/api/keys"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, srv.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", res.StatusCode)
			}
			if got := res.Header.Get("WWW-Authenticate"); got == "" {
				t.Fatal("no WWW-Authenticate challenge")
			}
			if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Fatalf("content type %q, want plain text", ct)
			}
			if page := body(t, res); strings.Contains(page, "<form") {
				t.Fatal("the recovery form answered a request it should refuse plainly")
			}
		})
	}
}

// TestReadOnlyRecovers proves read only mode locks the writes, not the page a
// phone needs to get back in.
func TestReadOnlyRecovers(t *testing.T) {
	cfg := Config{Session: "main", Root: root(t, "main"), Token: "s3cret", ReadOnly: true}
	srv := httptest.NewServer(handler(cfg))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", res.StatusCode)
	}
	if page := body(t, res); !strings.Contains(page, `id="t"`) {
		t.Fatal("read only mode answered without the recovery form")
	}
}
