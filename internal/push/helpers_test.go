package push_test

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"dm/internal/push"
)

// uaKey is the phone side of a subscription: the P-256 key pair it keeps and
// the auth secret it generated. The test decrypts with it, not with the
// package's own helpers.
type uaKey struct {
	private *ecdh.PrivateKey
	auth    []byte
}

func newUA(t *testing.T) *uaKey {
	t.Helper()
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate phone key: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}
	return &uaKey{private: private, auth: auth}
}

// subscription builds the Subscription the browser would report for endpoint.
func (k *uaKey) subscription(endpoint string) push.Subscription {
	return push.Subscription{
		Endpoint: endpoint,
		P256DH:   base64.RawURLEncoding.EncodeToString(k.private.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(k.auth),
		At:       time.Now().UTC().Truncate(time.Second),
		Label:    "phone",
	}
}

// newKeys loads a fresh VAPID pair from a temp file.
func newKeys(t *testing.T) *push.Keys {
	t.Helper()
	keys, err := push.LoadOrCreateKeys(t.TempDir() + "/vapid.json")
	if err != nil {
		t.Fatalf("LoadOrCreateKeys: %v", err)
	}
	return keys
}

// decryptBody decrypts an aes128gcm body per RFC 8188 and RFC 8291 using only
// the phone's private key and auth secret. It returns the plaintext without the
// last-record delimiter.
func decryptBody(t *testing.T, body []byte, ua *uaKey) []byte {
	t.Helper()
	const headerSize = 16 + 4 + 1 + 65
	if len(body) < headerSize+16 {
		t.Fatalf("body is %d bytes, too short for a header and a tag", len(body))
	}
	salt := body[:16]
	if rs := binary.BigEndian.Uint32(body[16:20]); rs != 4096 {
		t.Errorf("record size = %d, want 4096", rs)
	}
	idLen := int(body[20])
	if idLen != 65 {
		t.Fatalf("keyid length = %d, want 65", idLen)
	}
	asPublic := body[21 : 21+idLen]
	ciphertext := body[21+idLen:]

	ephemeral, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		t.Fatalf("parse ephemeral key: %v", err)
	}
	shared, err := ua.private.ECDH(ephemeral)
	if err != nil {
		t.Fatalf("ECDH: %v", err)
	}
	prkKey, err := hkdf.Extract(sha256.New, shared, ua.auth)
	if err != nil {
		t.Fatalf("HKDF extract (auth): %v", err)
	}
	keyInfo := "WebPush: info\x00" + string(ua.private.PublicKey().Bytes()) + string(asPublic)
	ikm, err := hkdf.Expand(sha256.New, prkKey, keyInfo, 32)
	if err != nil {
		t.Fatalf("HKDF expand (IKM): %v", err)
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		t.Fatalf("HKDF extract (salt): %v", err)
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		t.Fatalf("HKDF expand (CEK): %v", err)
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		t.Fatalf("HKDF expand (nonce): %v", err)
	}
	gcm, err := newGCM(cek)
	if err != nil {
		t.Fatalf("aes128gcm: %v", err)
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		t.Fatalf("plaintext does not end with the last-record delimiter 0x02")
	}
	return plain[:len(plain)-1]
}

// newGCM builds the aes128gcm mode the test decrypts with.
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// verifyVAPID checks the RFC 8292 Authorization header: the shape, the key
// value, the ES256 signature in JOSE form and the three claims.
func verifyVAPID(t *testing.T, authorization string, keys *push.Keys, wantAud, wantSub string, now time.Time) {
	t.Helper()
	const prefix = "vapid "
	if !strings.HasPrefix(authorization, prefix) {
		t.Errorf("Authorization = %q, want the vapid scheme", authorization)
		return
	}
	var token, key string
	for _, param := range strings.Split(strings.TrimPrefix(authorization, prefix), ",") {
		switch {
		case strings.HasPrefix(param, "t="):
			token = strings.TrimPrefix(param, "t=")
		case strings.HasPrefix(param, "k="):
			key = strings.TrimPrefix(param, "k=")
		default:
			t.Errorf("Authorization carries an unknown parameter %q", param)
		}
	}
	if token == "" {
		t.Errorf("Authorization carries no token")
		return
	}
	if key != keys.PublicKey() {
		t.Errorf("Authorization k = %q, want the VAPID public key %q", key, keys.PublicKey())
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	rawHeader, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	if err := json.Unmarshal(rawHeader, &header); err != nil {
		t.Fatalf("parse header: %v", err)
	}
	if header.Alg != "ES256" || header.Typ != "JWT" {
		t.Errorf("header = %+v, want alg ES256 and typ JWT", header)
	}

	rawClaims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(rawClaims, &claims); err != nil {
		t.Fatalf("parse claims: %v", err)
	}
	if claims.Aud != wantAud {
		t.Errorf("aud = %q, want %q", claims.Aud, wantAud)
	}
	if claims.Sub != wantSub {
		t.Errorf("sub = %q, want %q", claims.Sub, wantSub)
	}
	switch {
	case claims.Exp <= now.Unix():
		t.Errorf("exp %d is not in the future", claims.Exp)
	case time.Unix(claims.Exp, 0).After(now.Add(12 * time.Hour)):
		t.Errorf("exp %d is more than 12 hours out", claims.Exp)
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if len(signature) != 64 {
		t.Fatalf("signature is %d bytes, want the JOSE 64-byte r||s form", len(signature))
	}
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(vapidPublic(t, keys.PublicKey()), digest[:], r, s) {
		t.Errorf("ES256 signature does not verify against the VAPID public key")
	}
}

// vapidPublic parses the base64url uncompressed point into the ecdsa form the
// verifier needs.
func vapidPublic(t *testing.T, encoded string) *ecdsa.PublicKey {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode VAPID public key: %v", err)
	}
	if len(raw) != 65 || raw[0] != 0x04 {
		t.Fatalf("VAPID public key is not a 65-byte uncompressed point")
	}
	return &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(raw[1:33]),
		Y:     new(big.Int).SetBytes(raw[33:65]),
	}
}
