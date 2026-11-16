// Package proxy serves the loopback capability endpoint.
//
// One listener on 127.0.0.1 exposes a random path prefix per secret. A
// request under a prefix is forwarded to that secret's pinned upstream URL
// with the header template filled from the held value. The response streams
// back untouched. No endpoint returns the value: the value only leaves as a
// request header to the upstream, never in a response or an audit record.
package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// placeholder marks the spot in a header template where the value goes.
const placeholder = "{}"

// prefixLen is the random bytes behind each path prefix. 16 bytes make a
// prefix unguessable and collision-free in practice.
const prefixLen = 16

// Audit is one proxied request. Secret is the secret name, empty for a
// request to an unknown prefix. Path is the request path as received; it
// carries the prefix, never the value.
type Audit struct {
	Secret string
	Method string
	Path   string
	Status int
}

// AuditFunc receives one Audit per request.
type AuditFunc func(Audit)

type entry struct {
	name     string
	upstream *url.URL
	header   string // canonical header name
	value    string // header value with the template already filled
	client   *http.Client
}

// Proxy is the loopback capability endpoint. Create one with New, register
// secrets, then serve it.
type Proxy struct {
	mu       sync.RWMutex
	byPrefix map[string]*entry
	audit    AuditFunc

	ln  net.Listener
	srv *http.Server
}

// New binds a listener on 127.0.0.1 and returns a proxy that is not yet
// serving. Call Serve to start. audit may be nil.
func New(audit AuditFunc) (*Proxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("proxy: listen: %w", err)
	}
	p := &Proxy{
		byPrefix: make(map[string]*entry),
		audit:    audit,
		ln:       ln,
	}
	p.srv = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	return p, nil
}

// Addr is the listen address, "127.0.0.1:port".
func (p *Proxy) Addr() string {
	return p.ln.Addr().String()
}

// Serve runs the endpoint. It blocks until Close.
func (p *Proxy) Serve() error {
	err := p.srv.Serve(p.ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Close shuts the listener down and stops serving every prefix.
func (p *Proxy) Close() error {
	return p.srv.Close()
}

// Register mints a random path prefix for the named secret and pins it to
// upstream with headerTemplate. headerTemplate holds {} where the value
// goes, for example "Authorization: Bearer {}". It returns the prefix, a
// path such as "/<random>/". Requests under it forward to upstream.
func (p *Proxy) Register(name, upstream, headerTemplate, value string) (string, error) {
	u, err := url.Parse(upstream)
	if err != nil {
		return "", fmt.Errorf("proxy: bad upstream: %w", err)
	}
	if err := checkUpstream(u); err != nil {
		return "", err
	}
	header, filled, err := fillTemplate(headerTemplate, value)
	if err != nil {
		return "", err
	}
	e := &entry{
		name:     name,
		upstream: u,
		header:   header,
		value:    filled,
		client:   &http.Client{Transport: &http.Transport{}},
	}
	for {
		prefix, err := randomPrefix()
		if err != nil {
			return "", fmt.Errorf("proxy: mint prefix: %w", err)
		}
		p.mu.Lock()
		if _, taken := p.byPrefix[prefix]; taken {
			p.mu.Unlock()
			continue
		}
		p.byPrefix[prefix] = e
		p.mu.Unlock()
		return prefix, nil
	}
}

// Revoke stops serving the prefix and closes its idle upstream connections.
// In-flight requests finish. Unknown prefixes are ignored.
func (p *Proxy) Revoke(prefix string) {
	p.mu.Lock()
	e, ok := p.byPrefix[prefix]
	delete(p.byPrefix, prefix)
	p.mu.Unlock()
	if ok {
		e.client.CloseIdleConnections()
	}
}

// checkUpstream pins the upstream: https anywhere, plain http only to the
// loopback so tests can use httptest without TLS plumbing.
func checkUpstream(u *url.URL) error {
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopback(u.Hostname()):
	default:
		return fmt.Errorf("proxy: upstream must be https (or http to the loopback): %q", u.Host)
	}
	if u.Host == "" {
		return fmt.Errorf("proxy: upstream needs a host")
	}
	return nil
}

func isLoopback(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

// fillTemplate splits the template on the first colon, validates both sides,
// and fills {} with the value. CR and LF are refused so the template cannot
// smuggle headers.
func fillTemplate(template, value string) (string, string, error) {
	name, rest, found := strings.Cut(template, ":")
	if !found {
		return "", "", fmt.Errorf(`proxy: header template needs "Name: value with {}"`)
	}
	name = strings.TrimSpace(name)
	if !validHeaderName(name) {
		return "", "", fmt.Errorf("proxy: bad header name %q", name)
	}
	if strings.Count(rest, placeholder) != 1 {
		return "", "", fmt.Errorf("proxy: header template needs exactly one %q", placeholder)
	}
	if strings.ContainsAny(rest, "\r\n") {
		return "", "", fmt.Errorf("proxy: header template must not contain CR or LF")
	}
	return http.CanonicalHeaderKey(name), strings.ReplaceAll(rest, placeholder, value), nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

func randomPrefix() (string, error) {
	buf := make([]byte, prefixLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "/" + hex.EncodeToString(buf) + "/", nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r.RemoteAddr) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	p.mu.RLock()
	var matched *entry
	var matchedPrefix string
	for prefix, e := range p.byPrefix {
		if strings.HasPrefix(r.URL.Path, prefix) {
			matched = e
			matchedPrefix = prefix
			break
		}
	}
	p.mu.RUnlock()

	fw := newFlushWriter(w)
	rec := &statusRecorder{ResponseWriter: fw}
	if matched == nil {
		// No endpoint returns anything about the registry: an empty 404.
		rec.WriteHeader(http.StatusNotFound)
		p.auditRequest("", r, rec.status)
		return
	}

	resp, err := matched.forward(r, matchedPrefix)
	if err != nil {
		// The error text may name the upstream but never the value. Say
		// nothing anyway.
		rec.WriteHeader(http.StatusBadGateway)
		p.auditRequest(matched.name, r, rec.status)
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(rec, resp.Header)
	rec.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(rec, resp.Body)

	p.auditRequest(matched.name, r, rec.status)
}

func (p *Proxy) auditRequest(name string, r *http.Request, status int) {
	if p.audit != nil {
		p.audit(Audit{Secret: name, Method: r.Method, Path: r.URL.Path, Status: status})
	}
}

// forward builds the upstream request: the pinned URL, the path after the
// prefix, the inbound query, the inbound headers minus the injected one, and
// the header template filled with the value.
func (e *entry) forward(r *http.Request, prefix string) (*http.Response, error) {
	target := *e.upstream
	target.Path = joinPath(e.upstream.Path, strings.TrimPrefix(r.URL.Path, prefix))
	target.RawPath = ""
	target.RawQuery = r.URL.RawQuery

	out, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		return nil, err
	}
	for k, vs := range r.Header {
		if k == e.header {
			continue
		}
		out.Header[k] = vs
	}
	out.Header.Set(e.header, e.value)

	// Do not follow redirects: the pinned host is the only destination, and
	// the 3xx streams back untouched.
	return e.client.Do(out)
}

func joinPath(base, rest string) string {
	joined := path.Join("/", base, rest)
	if strings.HasSuffix(rest, "/") && !strings.HasSuffix(joined, "/") {
		joined += "/"
	}
	return joined
}

// hopHeaders never travel to the upstream or back.
var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func copyResponseHeaders(w http.ResponseWriter, src http.Header) {
	for _, h := range hopHeaders {
		src.Del(h)
	}
	for k, vs := range src {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
}

// statusRecorder captures the status code for the audit hook.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

// flushWriter flushes after every write so upstream responses stream.
type flushWriter struct {
	http.ResponseWriter
	flusher http.Flusher
}

func newFlushWriter(w http.ResponseWriter) *flushWriter {
	fw := &flushWriter{ResponseWriter: w}
	if f, ok := w.(http.Flusher); ok {
		fw.flusher = f
	}
	return fw
}

func (w *flushWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if w.flusher != nil {
		w.flusher.Flush()
	}
	return n, err
}
