package release

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewReader(t *testing.T) {
	t.Setenv(APIEnv, "")
	r := NewReader()
	if r.API != DefaultAPI {
		t.Fatalf("API = %q, want %q", r.API, DefaultAPI)
	}
	if r.Client == nil || r.Client.Timeout != Timeout {
		t.Fatalf("Client = %v, want one with a %s timeout", r.Client, Timeout)
	}

	t.Setenv(APIEnv, "http://127.0.0.1:9/releases/")
	if got := NewReader().API; got != "http://127.0.0.1:9/releases" {
		t.Fatalf("API = %q, want the override without its trailing slash", got)
	}

	// A new reader picks up a new value: the variable is read at NewReader,
	// not at Latest.
	t.Setenv(APIEnv, "http://127.0.0.1:8/releases")
	if got := NewReader().API; got != "http://127.0.0.1:8/releases" {
		t.Fatalf("API = %q, want the new override", got)
	}

	// An override that names the latest release is the same base.
	t.Setenv(APIEnv, "http://127.0.0.1:8/releases/latest")
	if got := NewReader().API; got != "http://127.0.0.1:8/releases" {
		t.Fatalf("API = %q, want the release base", got)
	}
}

func TestLatest(t *testing.T) {
	const body = `{"tag_name":"v1.2.3","assets":[
		{"name":"dm_linux_amd64.tar.gz","size":42,"browser_download_url":"https://example.test/dm_linux_amd64.tar.gz"},
		{"name":"checksums.txt","size":7,"browser_download_url":"https://example.test/checksums.txt"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest" {
			t.Errorf("path = %q, want /latest", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); !strings.Contains(got, "github") {
			t.Errorf("Accept = %q, want the github media type", got)
		}
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	rel, err := Reader{API: srv.URL, Client: srv.Client()}.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v1.2.3" {
		t.Errorf("Tag = %q, want v1.2.3", rel.Tag)
	}
	if len(rel.Assets) != 2 {
		t.Fatalf("%d assets, want 2", len(rel.Assets))
	}
	want := Asset{Name: "dm_linux_amd64.tar.gz", URL: "https://example.test/dm_linux_amd64.tar.gz", Size: 42}
	if rel.Assets[0] != want {
		t.Errorf("asset = %+v, want %+v", rel.Assets[0], want)
	}
}

func TestLatestEmptyAssets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v0.1.0","assets":[]}`)
	}))
	defer srv.Close()
	rel, err := Reader{API: srv.URL, Client: srv.Client()}.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rel.Assets) != 0 {
		t.Fatalf("%d assets, want none", len(rel.Assets))
	}
}

func TestLatestRefusals(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantIn  string
		overCap bool
	}{
		{name: "not found", status: http.StatusNotFound, body: `{"message":"Not Found"}`, wantIn: "404"},
		{name: "server error", status: http.StatusInternalServerError, body: "boom", wantIn: "500"},
		{name: "not json", status: http.StatusOK, body: "<html>", wantIn: "invalid character"},
		{name: "over the cap", status: http.StatusOK, body: strings.Repeat("a", maxBody+1), wantIn: "over"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			_, err := Reader{API: srv.URL, Client: srv.Client()}.Latest(context.Background())
			if err == nil {
				t.Fatal("Latest returned no error")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("error = %q, want it to name %q", err, tc.wantIn)
			}
		})
	}
}

func TestLatestNoAPI(t *testing.T) {
	if _, err := (Reader{}).Latest(context.Background()); err == nil {
		t.Fatal("Latest with no API returned no error")
	}
}

func TestAssetName(t *testing.T) {
	for _, p := range matrix {
		got, err := AssetName(p.goos, p.goarch)
		if err != nil {
			t.Fatalf("AssetName(%s, %s): %v", p.goos, p.goarch, err)
		}
		want := "dm_" + p.goos + "_" + p.goarch + ".tar.gz"
		if got != want {
			t.Errorf("AssetName(%s, %s) = %q, want %q", p.goos, p.goarch, got, want)
		}
	}
	for _, pair := range [][2]string{{"windows", "amd64"}, {"linux", "386"}, {"linux", "amd64.exe"}, {"", ""}} {
		_, err := AssetName(pair[0], pair[1])
		if err == nil {
			t.Fatalf("AssetName(%q, %q) returned no error", pair[0], pair[1])
		}
		want := pair[0] + "/" + pair[1]
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", err, want)
		}
	}
}
