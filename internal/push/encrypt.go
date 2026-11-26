package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// The aes128gcm framing of RFC 8188 and the Web Push derivation of RFC 8291.
const (
	// recordSize is the rs field: the size of one encrypted record, tag
	// included.
	recordSize = 4096
	// saltSize is the salt field: the HKDF salt for the content keys.
	saltSize = 16
	// keyIDSize is the length of the ephemeral public key that travels as the
	// keyid field.
	keyIDSize = 65
	// headerSize is salt, rs, idlen and the keyid together.
	headerSize = saltSize + 4 + 1 + keyIDSize
	// tagSize is the AES-128-GCM authentication tag.
	tagSize = 16
	// MaxPayload is the most plaintext one message may carry. The push
	// services cap the entire body at 4096 bytes, and the header, the tag and
	// the record delimiter come out of that budget.
	MaxPayload = recordSize - headerSize - tagSize - 1
)

// encryptPayload returns the aes128gcm body for plaintext, addressed to the
// subscription's public key and auth secret: the RFC 8291 derivation feeds the
// content keys, and RFC 8188 frames one record with the last-record delimiter.
func encryptPayload(uaPublic, authSecret, plaintext []byte) ([]byte, error) {
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("push: subscription p256dh: %w", err)
	}
	if len(authSecret) != 16 {
		return nil, fmt.Errorf("push: subscription auth is %d bytes, want 16", len(authSecret))
	}
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("push: generate ephemeral key: %w", err)
	}
	asPublic := ephemeral.PublicKey().Bytes()
	shared, err := ephemeral.ECDH(ua)
	if err != nil {
		return nil, fmt.Errorf("push: ECDH with subscription p256dh: %w", err)
	}

	// RFC 8291 section 3.3: the auth secret salts the first extract. The info
	// string binds the derived key to both public keys, the phone's and the
	// ephemeral one, so a body cannot be re-addressed.
	prkKey, err := hkdf.Extract(sha256.New, shared, authSecret)
	if err != nil {
		return nil, fmt.Errorf("push: derive IKM: %w", err)
	}
	keyInfo := "WebPush: info\x00" + string(uaPublic) + string(asPublic)
	ikm, err := hkdf.Expand(sha256.New, prkKey, keyInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("push: derive IKM: %w", err)
	}

	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("push: salt: %w", err)
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, fmt.Errorf("push: derive content keys: %w", err)
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, fmt.Errorf("push: derive content key: %w", err)
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, fmt.Errorf("push: derive nonce: %w", err)
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("push: aes128gcm cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("push: aes128gcm mode: %w", err)
	}
	// One record, no padding: the delimiter byte marks the end of the plaintext
	// and 0x02 marks it as the last record.
	padded := make([]byte, len(plaintext)+1)
	copy(padded, plaintext)
	padded[len(plaintext)] = 0x02
	sealed := gcm.Seal(nil, nonce, padded, nil)

	body := make([]byte, 0, headerSize+len(sealed))
	body = append(body, salt...)
	body = binary.BigEndian.AppendUint32(body, recordSize)
	body = append(body, keyIDSize)
	body = append(body, asPublic...)
	body = append(body, sealed...)
	return body, nil
}

// subscriptionKey decodes one of the two base64url values a browser reports.
func subscriptionKey(value, name string) ([]byte, error) {
	raw, err := decodeBase64URL(value)
	if err != nil {
		return nil, fmt.Errorf("push: subscription %s: %w", name, err)
	}
	return raw, nil
}
