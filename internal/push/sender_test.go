package push_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dm/internal/push"
)

// TestSendRoundTrip proves the whole wire: the request the sender builds, the
// token it signs and the payload it encrypts survive an independent decryption
// and an independent signature check.
func TestSendRoundTrip(t *testing.T) {
	keys := newKeys(t)
	ua := newUA(t)
	const subject = "mailto:ops@example.com"
	msg := push.Message{
		Title: "pane 3 needs you",
		Body:  "the build is waiting",
		Pane:  "3",
		Tag:   "pane-3",
		URL:   "/session/s1/pane/3",
	}
	const wantJSON = `{"title":"pane 3 needs you","body":"the build is waiting","pane":"3","tag":"pane-3","url":"/session/s1/pane/3"}`

	got := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		for header, want := range map[string]string{
			"Content-Encoding": "aes128gcm",
			"Content-Type":     "application/octet-stream",
			"TTL":              "3600",
			"Urgency":          "high",
		} {
			if value := r.Header.Get(header); value != want {
				t.Errorf("%s = %q, want %q", header, value, want)
			}
		}
		// aud is the endpoint's scheme and host, never its path.
		verifyVAPID(t, r.Header.Get("Authorization"), keys, "http://"+r.Host, subject, time.Now())
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		got <- decryptBody(t, body, ua)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sub := ua.subscription(srv.URL + "/push/one")
	sender := &push.Sender{Keys: keys, Subject: subject, TTL: time.Hour, Urgency: "high"}
	result, err := sender.Send(context.Background(), sub, msg)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Status != http.StatusCreated {
		t.Errorf("status = %d, want 201", result.Status)
	}
	if result.Gone {
		t.Errorf("Gone = true, want false")
	}
	plain := <-got
	if string(plain) != wantJSON {
		t.Errorf("payload = %s, want %s", plain, wantJSON)
	}
	var decoded push.Message
	if err := json.Unmarshal(plain, &decoded); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if decoded != msg {
		t.Errorf("message = %+v, want %+v", decoded, msg)
	}
}

// TestSendGone proves 404 and 410 are answers, not errors: the caller prunes
// the subscription and moves on.
func TestSendGone(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			keys := newKeys(t)
			ua := newUA(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()

			sender := &push.Sender{Keys: keys, Subject: "mailto:ops@example.com"}
			result, err := sender.Send(context.Background(), ua.subscription(srv.URL), push.Message{Title: "hi"})
			if err != nil {
				t.Fatalf("Send: %v, want no error for a dead subscription", err)
			}
			if !result.Gone {
				t.Errorf("Gone = false, want true for %d", status)
			}
			if result.Status != status {
				t.Errorf("status = %d, want %d", result.Status, status)
			}
		})
	}
}

// TestSendServerError proves a 500 is an error that names the status and holds
// no part of the response body: a push service can echo the endpoint, which is
// the phone's capability URL, and a 500 must not leak it.
func TestSendServerError(t *testing.T) {
	keys := newKeys(t)
	ua := newUA(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		// The body names the request endpoint, as a real service might.
		io.WriteString(w, "cannot deliver to https://"+r.Host+"/cap/secret-token\nsecond line must not appear\n")
	}))
	defer srv.Close()

	sub := ua.subscription(srv.URL + "/cap/secret-token")
	sender := &push.Sender{Keys: keys, Subject: "mailto:ops@example.com"}
	result, err := sender.Send(context.Background(), sub, push.Message{Title: "hi"})
	if err == nil {
		t.Fatal("Send returned no error for a 500")
	}
	if result.Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", result.Status)
	}
	if result.Gone {
		t.Errorf("Gone = true, want false")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not name the status", err)
	}
	for _, body := range []string{"cannot deliver to", "secret-token", "second line must not appear", sub.Endpoint, sub.Auth, sub.P256DH} {
		if strings.Contains(err.Error(), body) {
			t.Errorf("error carries %q: %v", body, err)
		}
	}
}

// TestSendOversize proves a message that cannot fit one aes128gcm body fails
// before a request is made.
func TestSendOversize(t *testing.T) {
	keys := newKeys(t)
	ua := newUA(t)
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sender := &push.Sender{Keys: keys, Subject: "mailto:ops@example.com"}
	msg := push.Message{Title: strings.Repeat("x", push.MaxPayload)}
	_, err := sender.Send(context.Background(), ua.subscription(srv.URL), msg)
	if err == nil {
		t.Fatal("Send returned no error for an oversize message")
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("the request reached the endpoint %d times, want 0", got)
	}
}

// TestSendRejectsIncompleteSender proves the sender names what it is missing.
func TestSendRejectsIncompleteSender(t *testing.T) {
	keys := newKeys(t)
	ua := newUA(t)
	sub := ua.subscription("https://push.example.com/ep")
	for name, sender := range map[string]*push.Sender{
		"no keys":    {Subject: "mailto:ops@example.com"},
		"no subject": {Keys: keys},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := sender.Send(context.Background(), sub, push.Message{Title: "hi"}); err == nil {
				t.Fatal("Send returned no error")
			}
		})
	}
}

// TestSendRejectsBadEndpoint proves a relative endpoint is refused.
func TestSendRejectsBadEndpoint(t *testing.T) {
	keys := newKeys(t)
	ua := newUA(t)
	sender := &push.Sender{Keys: keys, Subject: "mailto:ops@example.com"}
	if _, err := sender.Send(context.Background(), ua.subscription("/relative"), push.Message{Title: "hi"}); err == nil {
		t.Fatal("Send returned no error for an endpoint with no scheme or host")
	}
}

// TestSendParseErrorHidesEndpoint proves a malformed endpoint does not reach
// the error string: the endpoint is a capability URL and holds the phone's
// delivery token.
func TestSendParseErrorHidesEndpoint(t *testing.T) {
	keys := newKeys(t)
	ua := newUA(t)
	sender := &push.Sender{Keys: keys, Subject: "mailto:ops@example.com"}
	endpoint := "https://push.example.com/\x7fsecret-token"
	_, err := sender.Send(context.Background(), ua.subscription(endpoint), push.Message{Title: "hi"})
	if err == nil {
		t.Fatal("Send returned no error for a malformed endpoint")
	}
	for _, secret := range []string{"secret-token", "push.example.com"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error carries the endpoint %q: %v", secret, err)
		}
	}
}

// TestSendTransportErrorHidesEndpoint proves a failed connection reports the
// cause without the endpoint URL, which is a capability URL.
func TestSendTransportErrorHidesEndpoint(t *testing.T) {
	keys := newKeys(t)
	ua := newUA(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	endpoint := srv.URL + "/secret-token"
	srv.Close()

	sender := &push.Sender{Keys: keys, Subject: "mailto:ops@example.com"}
	_, err := sender.Send(context.Background(), ua.subscription(endpoint), push.Message{Title: "hi"})
	if err == nil {
		t.Fatal("Send returned no error for a closed server")
	}
	for _, secret := range []string{"secret-token", "http://"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error carries the endpoint %q: %v", secret, err)
		}
	}
}
