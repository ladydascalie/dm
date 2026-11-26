package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	// defaultTTL is how long a push service holds a message when the Sender
	// names no TTL.
	defaultTTL = 24 * time.Hour
	// vapidExpiry bounds the VAPID token: RFC 8292 says no more than 24 hours,
	// and the brief says 12.
	vapidExpiry = 12 * time.Hour
	// drainLimit bounds how much of a response body is read to free the
	// connection.
	drainLimit = 512
)

// Sender delivers one Message to one Subscription: it encrypts the payload per
// RFC 8291 and signs the request per RFC 8292.
type Sender struct {
	// Keys signs the VAPID token and supplies its public half. It must not be
	// nil.
	Keys *Keys
	// Subject is the VAPID sub claim: a mailto: or https: URL the push service
	// operator can use to reach whoever runs dm. It must not be empty.
	Subject string
	// TTL is how long the push service should keep an undelivered message. A
	// zero duration means defaultTTL.
	TTL time.Duration
	// Urgency is the RFC 8030 Urgency header: very-low, low, normal or high.
	// Empty means normal.
	Urgency string
	// Client sends the request. A nil client means http.DefaultClient.
	Client *http.Client
}

// Result is the push service's answer to one Send. It is a value, not an
// error, so a caller that prunes dead phones can read Gone and carry on.
type Result struct {
	// Status is the HTTP status code the push service returned.
	Status int
	// Gone reports a subscription the push service has discarded: 404 or 410.
	// The caller removes that subscription.
	Gone bool
}

// Send encrypts msg for sub and posts it to the subscription endpoint. A 2xx
// answer returns a Result and no error. A 404 or 410 returns a Result with Gone
// set and no error. Every other non-2xx answer is an error that names the
// status and nothing from the response body: a push service can echo the
// endpoint, and the endpoint is the phone's capability URL.
func (s *Sender) Send(ctx context.Context, sub Subscription, msg Message) (Result, error) {
	if s.Keys == nil || s.Keys.signer == nil || len(s.Keys.public) != keyIDSize {
		return Result{}, errNoKeys
	}
	if s.Subject == "" {
		return Result{}, errors.New("push: sender has no subject")
	}
	endpoint, err := url.Parse(sub.Endpoint)
	if err != nil {
		return Result{}, fmt.Errorf("push: parse subscription endpoint: %w", withoutURL(err))
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return Result{}, errors.New("push: subscription endpoint has no scheme or host")
	}
	uaPublic, err := subscriptionKey(sub.P256DH, "p256dh")
	if err != nil {
		return Result{}, err
	}
	authSecret, err := subscriptionKey(sub.Auth, "auth")
	if err != nil {
		return Result{}, err
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return Result{}, fmt.Errorf("push: encode message: %w", err)
	}
	if len(payload) > MaxPayload {
		return Result{}, fmt.Errorf("push: message is %d bytes, the most one aes128gcm body carries is %d", len(payload), MaxPayload)
	}
	body, err := encryptPayload(uaPublic, authSecret, payload)
	if err != nil {
		return Result{}, err
	}
	token, err := s.vapidToken(endpoint)
	if err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("push: build request: %w", err)
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(int(s.ttl().Seconds())))
	req.Header.Set("Urgency", s.urgency())
	req.Header.Set("Authorization", "vapid t="+token+",k="+s.Keys.PublicKey())

	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// The client error usually wraps the request URL. The endpoint is a
		// capability URL, so it must not reach a log through the error.
		return Result{}, fmt.Errorf("push: send to %s: %w", endpoint.Host, withoutURL(err))
	}
	defer resp.Body.Close()

	result := Result{Status: resp.StatusCode}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		drain(resp.Body)
		return result, nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		drain(resp.Body)
		result.Gone = true
		return result, nil
	default:
		drain(resp.Body)
		return result, fmt.Errorf("push: %s answered %d %s",
			endpoint.Host, resp.StatusCode, http.StatusText(resp.StatusCode))
	}
}

// ttl returns the TTL header value in seconds, defaulting to defaultTTL.
func (s *Sender) ttl() time.Duration {
	if s.TTL <= 0 {
		return defaultTTL
	}
	return s.TTL
}

// urgency returns the Urgency header value, defaulting to normal.
func (s *Sender) urgency() string {
	if s.Urgency == "" {
		return "normal"
	}
	return s.Urgency
}

// vapidToken builds and signs the RFC 8292 token: an ES256 JWT whose aud is the
// endpoint's scheme and host, whose exp is vapidExpiry out, and whose sub is
// the sender's subject.
func (s *Sender) vapidToken(endpoint *url.URL) (string, error) {
	claims := struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}{
		Aud: endpoint.Scheme + "://" + endpoint.Host,
		Exp: time.Now().Add(vapidExpiry).Unix(),
		Sub: s.Subject,
	}
	header := struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}{
		Alg: "ES256",
		Typ: "JWT",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("push: encode VAPID header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("push: encode VAPID claims: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) +
		"." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	r, v, err := ecdsa.Sign(rand.Reader, s.Keys.signer, digest[:])
	if err != nil {
		return "", fmt.Errorf("push: sign VAPID token: %w", err)
	}
	// JOSE wants r and s side by side, each padded to 32 bytes, not DER.
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	v.FillBytes(signature[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// withoutURL drops the request URL from a net/url error: the subscription
// endpoint is a capability URL, so it belongs in neither a log nor an error
// string. The cause survives.
func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}

// drain reads a bounded prefix of a response body so the connection can be
// reused.
func drain(body io.Reader) {
	io.Copy(io.Discard, io.LimitReader(body, drainLimit))
}
