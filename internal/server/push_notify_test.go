package server

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"dm/internal/push"
)

// pushDelivery is one request the fake push service saw, copied out of the
// request so the handler can return before the test reads it.
type pushDelivery struct {
	encoding string
	auth     string
	bodyLen  int
}

// pushService is a fake push service: it records every request and answers
// with the status the test names.
type pushService struct {
	mu       sync.Mutex
	status   int
	delivers []pushDelivery
	srv      *httptest.Server
}

// newPushService starts the fake and returns it. Cleanup closes it.
func newPushService(t *testing.T, status int) *pushService {
	t.Helper()
	p := &pushService{status: status}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.delivers = append(p.delivers, pushDelivery{
			encoding: r.Header.Get("Content-Encoding"),
			auth:     r.Header.Get("Authorization"),
			bodyLen:  len(body),
		})
		status := p.status
		p.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// count reports how many deliveries arrived.
func (p *pushService) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.delivers)
}

// waitCount waits until the fake saw n deliveries, and fails after a bound.
func (p *pushService) waitCount(t *testing.T, n int) []pushDelivery {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		got := append([]pushDelivery(nil), p.delivers...)
		p.mu.Unlock()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("deliveries = %d, want %d", len(got), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// noDelivery fails when any delivery arrives within the settle window. The
// push runs off the post, so a gate that did not fire shows up late.
func (p *pushService) noDelivery(t *testing.T) {
	t.Helper()
	time.Sleep(500 * time.Millisecond)
	if n := p.count(); n != 0 {
		t.Fatalf("deliveries = %d, want none", n)
	}
}

// pushSubscriber returns a subscription a real browser could have reported: a
// P-256 public point and a 16-byte auth secret.
func pushSubscriber(t *testing.T, endpoint string) push.Subscription {
	t.Helper()
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return push.Subscription{
		Endpoint: endpoint,
		P256DH:   base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}
}

// pushTestServer is a box server whose session directory can hold the key pair
// and the subscription store. The pair is written unless the test wants the
// unsubscribed case.
func pushTestServer(t *testing.T) (*server, string) {
	t.Helper()
	t.Setenv("DM_NOTIFY", "0")
	s := boxServer(t)
	dir := s.st.SessionDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := push.LoadOrCreateKeys(push.KeysPath(dir)); err != nil {
		t.Fatalf("keys: %v", err)
	}
	return s, dir
}

// pushAddSubscriber writes one subscription through a Store, the same way dm
// web does.
func pushAddSubscriber(t *testing.T, dir string, sub push.Subscription) {
	t.Helper()
	store, err := push.Load(push.StorePath(dir))
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	if err := store.Add(sub); err != nil {
		t.Fatalf("add subscription: %v", err)
	}
}

// pushStoredSubscriptions reads the store file from disk.
func pushStoredSubscriptions(t *testing.T, dir string) []push.Subscription {
	t.Helper()
	store, err := push.Load(push.StorePath(dir))
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	return store.List()
}

// A blocked entry on a topic nobody watches is delivered once, as an RFC 8291
// body signed with the VAPID pair dm web generated.
func TestPushBlockedEntryDelivered(t *testing.T) {
	svc := newPushService(t, http.StatusCreated)
	s, dir := pushTestServer(t)
	pushAddSubscriber(t, dir, pushSubscriber(t, svc.srv.URL+"/push/one"))

	postQuiet(t, s, "notify/ghost", "blocked", "needs review")

	got := svc.waitCount(t, 1)
	time.Sleep(300 * time.Millisecond)
	if n := svc.count(); n != 1 {
		t.Fatalf("deliveries = %d, want exactly one", n)
	}
	if got[0].encoding != "aes128gcm" {
		t.Errorf("Content-Encoding = %q, want aes128gcm", got[0].encoding)
	}
	if want := "vapid t="; len(got[0].auth) < len(want) || got[0].auth[:len(want)] != want {
		t.Errorf("Authorization = %q, want a vapid token", got[0].auth)
	}
	if want := ",k="; !strings.Contains(got[0].auth, want) {
		t.Errorf("Authorization = %q, want %q in it", got[0].auth, want)
	}
	if got[0].bodyLen == 0 {
		t.Error("body is empty, want an encrypted payload")
	}
}

// A blocked entry for the pane the operator is looking at reaches no phone.
func TestPushSuppressedPaneDeliversNothing(t *testing.T) {
	svc := newPushService(t, http.StatusCreated)
	s, dir := pushTestServer(t)
	pushAddSubscriber(t, dir, pushSubscriber(t, svc.srv.URL+"/push/one"))
	cl, _ := boxConn(t)
	t.Cleanup(cl.shutdown)
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()
	focus := s.st.ActiveWindow().Focus

	postQuiet(t, s, "notify/"+focus, "blocked", "needs review")

	svc.noDelivery(t)
}

// A done entry fails the urgency gate the desktop pop applies, so no phone
// hears about it.
func TestPushDoneEntryDeliversNothing(t *testing.T) {
	svc := newPushService(t, http.StatusCreated)
	s, dir := pushTestServer(t)
	pushAddSubscriber(t, dir, pushSubscriber(t, svc.srv.URL+"/push/one"))

	postQuiet(t, s, "notify/ghost", "done", "built ok")

	svc.noDelivery(t)
}

// A session with a subscription but no key pair cannot sign a request, and a
// missing pair means dm web never minted one, so the daemon stays silent.
func TestPushNoKeysDeliversNothing(t *testing.T) {
	t.Setenv("DM_NOTIFY", "0")
	svc := newPushService(t, http.StatusCreated)
	s := boxServer(t)
	dir := s.st.SessionDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pushAddSubscriber(t, dir, pushSubscriber(t, svc.srv.URL+"/push/one"))

	postQuiet(t, s, "notify/ghost", "blocked", "needs review")

	svc.noDelivery(t)
}

// A 410 from the push service removes the dead subscription from the store
// file, so the next entry does not try it again.
func TestPushGonePruned(t *testing.T) {
	svc := newPushService(t, http.StatusGone)
	s, dir := pushTestServer(t)
	pushAddSubscriber(t, dir, pushSubscriber(t, svc.srv.URL+"/push/one"))

	postQuiet(t, s, "notify/ghost", "blocked", "needs review")

	svc.waitCount(t, 1)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if len(pushStoredSubscriptions(t, dir)) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("subscription is still in the store after a 410")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// One entry reaches at most eight phones, whatever the store collected.
func TestPushDeliveryCap(t *testing.T) {
	svc := newPushService(t, http.StatusCreated)
	s, dir := pushTestServer(t)
	for i := 0; i < 10; i++ {
		pushAddSubscriber(t, dir, pushSubscriber(t, svc.srv.URL+"/push/"+string(rune('a'+i))))
	}

	postQuiet(t, s, "notify/ghost", "blocked", "needs review")

	svc.waitCount(t, 8)
	time.Sleep(300 * time.Millisecond)
	if n := svc.count(); n != 8 {
		t.Fatalf("deliveries = %d, want 8", n)
	}
}

// A delivery error reaches the log without the endpoint or its host, so a
// failed push never prints the capability URL.
func TestPushErrorRedactsEndpoint(t *testing.T) {
	endpoint := "https://push.example.test/secret-capability-path"
	err := errors.New("push: send to push.example.test: dial tcp: connection refused")

	got := redactEndpoint(err, endpoint)

	for _, banned := range []string{"push.example.test", "secret-capability-path"} {
		if strings.Contains(got, banned) {
			t.Fatalf("redacted error = %q, want no %q", got, banned)
		}
	}
	if !strings.Contains(got, "connection refused") {
		t.Fatalf("redacted error = %q, want the cause kept", got)
	}
}
