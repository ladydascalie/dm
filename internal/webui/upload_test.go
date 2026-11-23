package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes is the smallest thing http.DetectContentType reads as a picture: an
// eight byte PNG signature.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// uploadServe starts the surface with no session behind it, because an upload
// touches the state tree and never the socket.
func uploadServe(t *testing.T, readOnly bool) (*httptest.Server, string) {
	t.Helper()
	dir := root(t, "main")
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t", ReadOnly: readOnly}))
	t.Cleanup(srv.Close)
	return srv, dir
}

// upload posts data to the upload endpoint and returns the reply.
func upload(t *testing.T, srv *httptest.Server, data []byte) *http.Response {
	t.Helper()
	res, err := http.Post(srv.URL+"/api/upload?t=t", "image/png", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestUploadStoresThePicture proves a photo lands in the session's own uploads
// folder, readable by the agent, and that the reply names the absolute path the
// page types into the pane.
func TestUploadStoresThePicture(t *testing.T) {
	t.Parallel()
	srv, dir := uploadServe(t, false)
	res := upload(t, srv, pngBytes)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var got struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "uploads")
	if filepath.Dir(got.Path) != want || !strings.HasSuffix(got.Path, ".png") {
		t.Fatalf("path = %q, want one in %q ending .png", got.Path, want)
	}
	if !filepath.IsAbs(got.Path) {
		t.Fatalf("path = %q, want absolute so the agent can open it", got.Path)
	}
	body, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, pngBytes) {
		t.Fatalf("stored %d bytes, want the %d the page sent", len(body), len(pngBytes))
	}
	info, err := os.Stat(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %v, want 0600: a photo is the operator's, not the session's", mode)
	}
}

// TestUploadIsItsOwnHash proves the same picture twice costs one file, so a
// retry after a dropped reply finds what it already sent.
func TestUploadIsItsOwnHash(t *testing.T) {
	t.Parallel()
	srv, _ := uploadServe(t, false)
	first := uploadPath(t, srv)
	second := uploadPath(t, srv)
	if first != second {
		t.Fatalf("second upload = %q, want the first path %q", second, first)
	}
}

// TestUploadRefuses proves the endpoint keeps the four picture types and
// nothing else, and that a read only page cannot write at all.
func TestUploadRefuses(t *testing.T) {
	t.Parallel()
	srv, _ := uploadServe(t, false)
	readOnly, _ := uploadServe(t, true)

	cases := []struct {
		name string
		srv  *httptest.Server
		data []byte
		want int
	}{
		{"a text file called a picture", srv, []byte("not a picture at all"), http.StatusBadRequest},
		{"an empty body", srv, nil, http.StatusBadRequest},
		{"a read only page", readOnly, pngBytes, http.StatusForbidden},
		{"past the cap", srv, append(pngBytes, make([]byte, maxUpload)...), http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		res := upload(t, c.srv, c.data)
		_ = res.Body.Close()
		if res.StatusCode != c.want {
			t.Fatalf("%s: status %d, want %d", c.name, res.StatusCode, c.want)
		}
	}
}

// uploadPath posts the standard picture and returns the path the reply names.
func uploadPath(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	res := upload(t, srv, pngBytes)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var got struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got.Path
}
