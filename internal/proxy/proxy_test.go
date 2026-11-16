package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// upstream records what one fake upstream received.
type upstream struct {
	*httptest.Server
	mu     sync.Mutex
	paths  []string
	auths  []string // Authorization header values seen
	bodies []string
}

func newUpstream(t *testing.T, respond func(w http.ResponseWriter)) *upstream {
	t.Helper()
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.paths = append(u.paths, r.URL.Path)
		u.auths = append(u.auths, r.Header.Get("Authorization"))
		u.bodies = append(u.bodies, string(b))
		u.mu.Unlock()
		if respond != nil {
			respond(w)
			return
		}
		w.Header().Set("X-Upstream", "yes")
		fmt.Fprint(w, "upstream-body")
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *upstream) seen() (paths, auths, bodies []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.paths, u.auths, u.bodies
}

// runProxy starts a proxy with an audit sink and returns the base URL.
func runProxy(t *testing.T, audit func(Audit)) (*Proxy, string) {
	t.Helper()
	p, err := New(audit)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go p.Serve()
	t.Cleanup(func() { p.Close() })
	return p, "http://" + p.Addr()
}

func get(t *testing.T, url string, header ...string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestForwarding(t *testing.T) {
	up := newUpstream(t, nil)
	proxy, base := runProxy(t, nil)

	pfx, err := proxy.Register("tok", up.URL, "Authorization: Bearer {}", "v3ry-s3cret")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	resp := get(t, base+pfx+"v1/models?x=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := readBody(t, resp); got != "upstream-body" {
		t.Fatalf("body = %q", got)
	}
	if got := resp.Header.Get("X-Upstream"); got != "yes" {
		t.Fatalf("X-Upstream = %q", got)
	}
	paths, auths, _ := up.seen()
	if len(paths) != 1 || paths[0] != "/v1/models" {
		t.Fatalf("upstream paths = %v, want [/v1/models]", paths)
	}
	if len(auths) != 1 || auths[0] != "Bearer v3ry-s3cret" {
		t.Fatalf("upstream auth = %v, want [Bearer v3ry-s3cret]", auths)
	}
}

func TestForwardingKeepsQueryAndTrailingSlash(t *testing.T) {
	up := newUpstream(t, nil)
	proxy, base := runProxy(t, nil)
	pfx, err := proxy.Register("tok", up.URL, "Authorization: Bearer {}", "v3ry-s3cret")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	get(t, base+pfx+"v1/dir/?x=1")
	paths, _, _ := up.seen()
	if len(paths) != 1 || paths[0] != "/v1/dir/" {
		t.Fatalf("upstream paths = %v, want [/v1/dir/]", paths)
	}
}

func TestHeaderInjectionReplacesClientHeader(t *testing.T) {
	up := newUpstream(t, nil)
	proxy, base := runProxy(t, nil)
	pfx, err := proxy.Register("tok", up.URL, "Authorization: Bearer {}", "v3ry-s3cret")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	resp := get(t, base+pfx+"x", "Authorization", "client-sent")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	_, auths, _ := up.seen()
	if len(auths) != 1 || auths[0] != "Bearer v3ry-s3cret" {
		t.Fatalf("upstream auth = %v, want the injected value only", auths)
	}
}

func TestPrefixIsolation(t *testing.T) {
	upA := newUpstream(t, nil)
	upB := newUpstream(t, nil)
	proxy, base := runProxy(t, nil)

	pfxA, err := proxy.Register("a", upA.URL, "Authorization: Bearer {}", "value-a")
	if err != nil {
		t.Fatalf("register a: %v", err)
	}
	pfxB, err := proxy.Register("b", upB.URL, "Authorization: Bearer {}", "value-b")
	if err != nil {
		t.Fatalf("register b: %v", err)
	}
	if pfxA == pfxB {
		t.Fatal("prefixes must differ")
	}

	if got := readBody(t, get(t, base+pfxA+"x")); got != "upstream-body" {
		t.Fatalf("a body = %q", got)
	}
	if got := readBody(t, get(t, base+pfxB+"y")); got != "upstream-body" {
		t.Fatalf("b body = %q", got)
	}

	pathsA, authsA, _ := upA.seen()
	if len(pathsA) != 1 || pathsA[0] != "/x" || len(authsA) != 1 || authsA[0] != "Bearer value-a" {
		t.Fatalf("upstream a saw paths=%v auths=%v", pathsA, authsA)
	}
	pathsB, authsB, _ := upB.seen()
	if len(pathsB) != 1 || pathsB[0] != "/y" || len(authsB) != 1 || authsB[0] != "Bearer value-b" {
		t.Fatalf("upstream b saw paths=%v auths=%v", pathsB, authsB)
	}
}

func TestUnknownPrefix404(t *testing.T) {
	up := newUpstream(t, nil)
	proxy, base := runProxy(t, nil)
	pfx, err := proxy.Register("tok", up.URL, "Authorization: Bearer {}", "v3ry-s3cret")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	for _, path := range []string{
		"/",
		"/nope/x",
		pfx[:len(pfx)-1],                     // prefix without the trailing slash
		strings.ToUpper(pfx[:10]) + pfx[10:], // wrong case
	} {
		resp := get(t, base+path)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, resp.StatusCode)
		}
		if got := readBody(t, resp); strings.Contains(got, "v3ry-s3cret") {
			t.Fatalf("404 body for %s leaks the value", path)
		}
	}
	paths, _, _ := up.seen()
	if len(paths) != 0 {
		t.Fatalf("upstream saw %v, want nothing", paths)
	}
}

// TestTraversalStaysPinned: a path with dot segments cannot leave the pinned
// upstream host. path.Join cleans the segments inside the upstream root, so
// the request lands on the pinned host with a cleaned path.
func TestTraversalStaysPinned(t *testing.T) {
	up := newUpstream(t, nil)
	proxy, base := runProxy(t, nil)
	pfx, err := proxy.Register("tok", up.URL, "Authorization: Bearer {}", "v3ry-s3cret")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	get(t, base+pfx+"a/../escape")
	paths, _, _ := up.seen()
	if len(paths) != 1 || paths[0] != "/escape" {
		t.Fatalf("upstream paths = %v, want the cleaned path on the pinned host", paths)
	}
}

func TestRevokeStopsServing(t *testing.T) {
	up := newUpstream(t, nil)
	proxy, base := runProxy(t, nil)
	pfx, err := proxy.Register("tok", up.URL, "Authorization: Bearer {}", "v3ry-s3cret")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if resp := get(t, base+pfx+"x"); resp.StatusCode != http.StatusOK {
		t.Fatalf("before revoke status = %d, want 200", resp.StatusCode)
	}

	proxy.Revoke(pfx)
	resp := get(t, base+pfx+"x")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after revoke status = %d, want 404", resp.StatusCode)
	}
	paths, _, _ := up.seen()
	if len(paths) != 1 {
		t.Fatalf("upstream saw %d requests, want 1", len(paths))
	}
}

func TestValueNeverAppears(t *testing.T) {
	const value = "v3ry-s3cret-value"

	up := newUpstream(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "echo-nothing")
	})
	var mu sync.Mutex
	var audits []Audit
	proxy, base := runProxy(t, func(a Audit) {
		mu.Lock()
		defer mu.Unlock()
		audits = append(audits, a)
	})
	pfx, err := proxy.Register("tok", up.URL, "Authorization: Bearer {}", value)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Happy path, unknown prefix, and dead upstream, all audited.
	resp := get(t, base+pfx+"x")
	if got := readBody(t, resp); strings.Contains(got, value) {
		t.Fatal("forwarded response leaks the value")
	}
	if got := readBody(t, get(t, base+"/unknown")); strings.Contains(got, value) {
		t.Fatal("404 response leaks the value")
	}

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // listener gone, so the proxy gets a dial error
	pfxDead, err := proxy.Register("dead", deadURL, "Authorization: Bearer {}", value)
	if err != nil {
		t.Fatalf("register dead: %v", err)
	}
	resp = get(t, base+pfxDead+"y")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("dead upstream status = %d, want 502", resp.StatusCode)
	}
	if got := readBody(t, resp); strings.Contains(got, value) {
		t.Fatal("502 response leaks the value")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, a := range audits {
		leak := fmt.Sprint(a)
		if strings.Contains(leak, value) {
			t.Fatalf("audit record leaks the value: %+v", a)
		}
	}
}

func TestAuditRecords(t *testing.T) {
	up := newUpstream(t, nil)
	var audits []Audit
	var mu sync.Mutex
	proxy, base := runProxy(t, func(a Audit) {
		mu.Lock()
		audits = append(audits, a)
		mu.Unlock()
	})

	pfx, err := proxy.Register("tok", up.URL, "Authorization: Bearer {}", "v3ry-s3cret")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	get(t, base+pfx+"x")
	get(t, base+"/nope")

	mu.Lock()
	defer mu.Unlock()
	if len(audits) != 2 {
		t.Fatalf("audits = %d, want 2", len(audits))
	}
	if audits[0].Secret != "tok" || audits[0].Method != http.MethodGet || audits[0].Path != pfx+"x" || audits[0].Status != http.StatusOK {
		t.Fatalf("audit[0] = %+v", audits[0])
	}
	if audits[1].Secret != "" || audits[1].Status != http.StatusNotFound {
		t.Fatalf("audit[1] = %+v", audits[1])
	}
}

func TestRegisterRejectsBadInput(t *testing.T) {
	proxy, _ := runProxy(t, nil)

	// Plain http to a non-loopback host must fail: TLS is the rule off-host.
	if _, err := proxy.Register("tok", "http://example.com/x", "Authorization: Bearer {}", "v"); err == nil {
		t.Fatal("plain http to a remote host accepted")
	}
	if _, err := proxy.Register("tok", "not a url", "Authorization: Bearer {}", "v"); err == nil {
		t.Fatal("garbage upstream accepted")
	}
	if _, err := proxy.Register("tok", "", "Authorization: Bearer {}", "v"); err == nil {
		t.Fatal("empty upstream accepted")
	}
	if _, err := proxy.Register("tok", "http://127.0.0.1:1", "Authorization Bearer {}", "v"); err == nil {
		t.Fatal("template without a colon accepted")
	}
	if _, err := proxy.Register("tok", "http://127.0.0.1:1", "Authorization: Bearer ", "v"); err == nil {
		t.Fatal("template without the placeholder accepted")
	}
	if _, err := proxy.Register("tok", "http://127.0.0.1:1", "Authorization: Bearer {} {} ", "v"); err == nil {
		t.Fatal("template with two placeholders accepted")
	}
	if _, err := proxy.Register("tok", "http://127.0.0.1:1", "Authorization: Bearer {}\r\nX-Evil: 1", "v"); err == nil {
		t.Fatal("template with CRLF accepted")
	}
	if _, err := proxy.Register("tok", "http://127.0.0.1:1", "Bad Header: Bearer {}", "v"); err == nil {
		t.Fatal("template with a space in the header name accepted")
	}
}

func TestPrefixesAreUnguessable(t *testing.T) {
	proxy, _ := runProxy(t, nil)
	pfx, err := proxy.Register("tok", "http://127.0.0.1:1", "Authorization: Bearer {}", "v")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(pfx) < 32 {
		t.Fatalf("prefix %q too short", pfx)
	}
}
