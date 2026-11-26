package webui

import (
	"bytes"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pushAssetsServer serves one session, so the public assets and the token
// routes are answered by the same handler the phone reaches.
func pushAssetsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler(Config{Session: "main", Root: root(t, "main"), Token: "t"}))
	t.Cleanup(srv.Close)
	return srv
}

// fetch reads one URL without a token or a cookie and returns the response and
// its body. The caller closes nothing.
func fetch(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, b
}

// TestManifestDeclaresTheApp proves the manifest carries what an install needs,
// and that both icons it offers are the sizes it claims.
func TestManifestDeclaresTheApp(t *testing.T) {
	srv := pushAssetsServer(t)
	res, raw := fetch(t, srv.URL+"/manifest.webmanifest")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Errorf("content type %q, want application/manifest+json", ct)
	}
	var m struct {
		Name            string `json:"name"`
		ShortName       string `json:"short_name"`
		StartURL        string `json:"start_url"`
		Scope           string `json:"scope"`
		Display         string `json:"display"`
		BackgroundColor string `json:"background_color"`
		ThemeColor      string `json:"theme_color"`
		Icons           []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Type    string `json:"type"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	want := []struct {
		field string
		got   string
		want  string
	}{
		{"name", m.Name, "dm"},
		{"short_name", m.ShortName, "dm"},
		{"start_url", m.StartURL, "/"},
		{"scope", m.Scope, "/"},
		{"display", m.Display, "standalone"},
		{"background_color", m.BackgroundColor, "#101418"},
		{"theme_color", m.ThemeColor, "#101418"},
	}
	for _, w := range want {
		if w.got != w.want {
			t.Errorf("%s = %q, want %q", w.field, w.got, w.want)
		}
	}
	if len(m.Icons) != 2 {
		t.Fatalf("icons = %d, want 2", len(m.Icons))
	}
	sizes := map[string]string{m.Icons[0].Src: m.Icons[0].Sizes, m.Icons[1].Src: m.Icons[1].Sizes}
	if sizes["/icon-192.png"] != "192x192" || sizes["/icon-512.png"] != "512x512" {
		t.Fatalf("icons = %+v, want 192 and 512", m.Icons)
	}
	for _, icon := range m.Icons {
		if icon.Type != "image/png" {
			t.Errorf("%s type = %q, want image/png", icon.Src, icon.Type)
		}
		if icon.Purpose != "any maskable" {
			t.Errorf("%s purpose = %q, want any maskable", icon.Src, icon.Purpose)
		}
	}
}

// TestServiceWorkerIsFresh proves the worker is served at the root, with a
// JavaScript type and no cache, and that it carries both listeners and no
// fetch handler: a worker that cached would hand the phone yesterday's page.
func TestServiceWorkerIsFresh(t *testing.T) {
	srv := pushAssetsServer(t)
	res, raw := fetch(t, srv.URL+"/sw.js")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("content type %q, want application/javascript", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache control %q, want no-store", cc)
	}
	body := string(raw)
	for _, want := range []string{
		`addEventListener("push"`,
		`addEventListener("notificationclick"`,
		"skipWaiting",
		"clients.claim",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("worker has no %s", want)
		}
	}
	for _, unwanted := range []string{`addEventListener("fetch"`, "caches.open", "caches.match"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("worker carries %s, want no cache and no fetch handler", unwanted)
		}
	}
}

// TestIconsAreDrawnAtTheRightSize proves each route answers a real PNG of its
// own size, and that the same request returns the same bytes.
func TestIconsAreDrawnAtTheRightSize(t *testing.T) {
	srv := pushAssetsServer(t)
	cases := []struct {
		path string
		size int
	}{
		{path: "/icon-180.png", size: 180},
		{path: "/icon-192.png", size: 192},
		{path: "/icon-512.png", size: 512},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			res, raw := fetch(t, srv.URL+tc.path)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200", res.StatusCode)
			}
			if ct := res.Header.Get("Content-Type"); ct != "image/png" {
				t.Fatalf("content type %q, want image/png", ct)
			}
			img, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := img.Bounds().Size().X; got != tc.size {
				t.Errorf("width %d, want %d", got, tc.size)
			}
			if got := img.Bounds().Size().Y; got != tc.size {
				t.Errorf("height %d, want %d", got, tc.size)
			}
			_, again := fetch(t, srv.URL+tc.path)
			if !bytes.Equal(raw, again) {
				t.Error("two requests returned different bytes")
			}
		})
	}
}

// TestPublicAssetsSkipTheToken proves the five assets answer with no cookie,
// and that a page route beside them still refuses one.
func TestPublicAssetsSkipTheToken(t *testing.T) {
	srv := pushAssetsServer(t)
	for _, path := range []string{"/sw.js", "/manifest.webmanifest", "/icon-192.png", "/icon-512.png", "/icon-180.png"} {
		t.Run(path, func(t *testing.T) {
			res, _ := fetch(t, srv.URL+path)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200 without a token", res.StatusCode)
			}
		})
	}
	res, _ := fetch(t, srv.URL+"/api/push/config")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("config status %d, want 401 without a token", res.StatusCode)
	}
}
