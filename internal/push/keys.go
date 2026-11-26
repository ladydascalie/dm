package push

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"strings"
)

// errNoKeys reports a Sender without a usable pair.
var errNoKeys = errors.New("push: no VAPID keys")

// Keys is the VAPID key pair: one P-256 key that signs every push request and
// names the applicationServerKey a browser subscribes with.
//
// A rotated pair silently invalidates every subscription a phone already
// holds, so the pair is generated once and reused for the life of the file.
type Keys struct {
	private []byte // the 32-byte P-256 scalar
	public  []byte // the 65-byte uncompressed point
	signer  *ecdsa.PrivateKey
}

// keyFile is the on-disk form of Keys: both halves base64url encoded.
type keyFile struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

// LoadOrCreateKeys returns the VAPID key pair stored at path. It generates a
// P-256 pair on first use and writes it through a temp file and a rename at
// mode 0600. A pair already on disk is reused, never replaced: a rotation would
// invalidate every subscription the phone holds. A file whose mode was widened
// is pulled back to 0600. A file that exists but does not parse is an error,
// not an invitation to overwrite it.
//
// The read-or-create runs under the lock file beside path, so two processes
// that start together cannot both find no pair and both generate one: the later
// one reads the pair the earlier one wrote.
func LoadOrCreateKeys(path string) (*Keys, error) {
	lock, err := lockFile(path)
	if err != nil {
		return nil, err
	}
	defer lock.release()
	return loadOrCreateKeys(path)
}

// loadOrCreateKeys reads the pair, or generates one when the file is absent.
// It runs with the lock held, so a pair another process just wrote is the one
// it returns.
func loadOrCreateKeys(path string) (*Keys, error) {
	keys, err := readKeys(path)
	if errors.Is(err, fs.ErrNotExist) {
		return generateKeys(path)
	}
	return keys, err
}

// LoadKeys returns the VAPID key pair stored at path. It never creates one: a
// missing file reports fs.ErrNotExist, so a daemon that only delivers cannot
// mint a pair for a phone that never subscribed. It takes no lock, because it
// never writes the file, and it repairs a widened mode like every other read.
func LoadKeys(path string) (*Keys, error) { return readKeys(path) }

// readKeys reads and parses the pair at path, pulling a widened mode back to
// 0600. A missing file reports fs.ErrNotExist through the wrap.
func readKeys(path string) (*Keys, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("push: read VAPID keys %s: %w", path, err)
	}
	if err := tightenMode(path); err != nil {
		return nil, err
	}
	var file keyFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("push: parse VAPID keys %s: %w", path, err)
	}
	keys, err := decodeKeys(file)
	if err != nil {
		return nil, fmt.Errorf("push: VAPID keys %s: %w", path, err)
	}
	return keys, nil
}

// tightenMode pulls a secrets file back to 0600. The key file and the store are
// written at 0600, but a copy, an editor or a backup tool can widen the mode; a
// file another user can read exposes the VAPID private key, or every phone's
// auth secret and p256dh.
func tightenMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("push: stat %s: %w", path, err)
	}
	if info.Mode().Perm() == 0o600 {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("push: set mode %s: %w", path, err)
	}
	return nil
}

// PublicKey returns the base64url uncompressed point: the applicationServerKey
// a browser passes to pushManager.subscribe. It is the k that travels in the
// RFC 8292 Authorization header.
func (k *Keys) PublicKey() string {
	if k == nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(k.public)
}

// generateKeys makes a fresh pair, writes it to path and returns it.
func generateKeys(path string) (*Keys, error) {
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("push: generate VAPID keys: %w", err)
	}
	keys, err := newKeys(ephemeral.Bytes(), ephemeral.PublicKey().Bytes())
	if err != nil {
		return nil, fmt.Errorf("push: generate VAPID keys: %w", err)
	}
	file := keyFile{
		Private: base64.RawURLEncoding.EncodeToString(keys.private),
		Public:  base64.RawURLEncoding.EncodeToString(keys.public),
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("push: encode VAPID keys: %w", err)
	}
	raw = append(raw, '\n')
	if err := writeFileAtomic(path, raw); err != nil {
		return nil, err
	}
	return keys, nil
}

// decodeKeys turns the on-disk form into a pair and checks the two halves
// match, so a half-edited file fails instead of signing with one key and
// naming another.
func decodeKeys(file keyFile) (*Keys, error) {
	private, err := decodeBase64URL(file.Private)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	public, err := decodeBase64URL(file.Public)
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	return newKeys(private, public)
}

// newKeys validates both raw halves and builds the signing key beside them.
func newKeys(private, public []byte) (*Keys, error) {
	if len(private) != 32 {
		return nil, fmt.Errorf("private key is %d bytes, want 32", len(private))
	}
	if len(public) != 65 || public[0] != 0x04 {
		return nil, fmt.Errorf("public key is %d bytes, want a 65-byte uncompressed point", len(public))
	}
	ecdhKey, err := ecdh.P256().NewPrivateKey(private)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	// The private half derives its own public point, so the comparison also
	// rejects a public half that is not a point on P-256.
	if !bytes.Equal(ecdhKey.PublicKey().Bytes(), public) {
		return nil, errors.New("public key does not belong to the private key")
	}
	signer, err := ecdsaKey(private, public)
	if err != nil {
		return nil, err
	}
	return &Keys{
		private: append([]byte(nil), private...),
		public:  append([]byte(nil), public...),
		signer:  signer,
	}, nil
}

// ecdsaKey builds the ES256 signing key the VAPID token needs. crypto/ecdh
// does the payload exchange and crypto/ecdsa signs the token; both read the
// same scalar.
func ecdsaKey(private, public []byte) (*ecdsa.PrivateKey, error) {
	curve := elliptic.P256()
	x := new(big.Int).SetBytes(public[1:33])
	y := new(big.Int).SetBytes(public[33:65])
	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("public key is not a point on P-256")
	}
	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y},
		D:         new(big.Int).SetBytes(private),
	}, nil
}

// decodeBase64URL decodes the raw, unpadded base64url a browser reports. It
// also accepts padding, because a client that re-encodes the value may add it.
func decodeBase64URL(s string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, fmt.Errorf("not base64url: %w", err)
	}
	return raw, nil
}
