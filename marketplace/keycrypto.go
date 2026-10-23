package marketplace

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

// Listing keys are encrypted with AES-256-GCM and fingerprinted with
// HMAC-SHA256 (§3.5). Both subkeys derive from the package-level anonSecret
// — the MP_KEY_SECRET of §4.5 — via SHA-256 with a domain-separation suffix,
// so the operator still backs up exactly one secret while the three uses
// (anonymous buyer codes, key encryption, key fingerprints) never share key
// material. Losing MP_KEY_SECRET makes every stored key undecryptable, which
// is the documented §4.5 trade-off.
//
// §2.3: no error, log or API return value from this file ever contains the
// key plaintext, the ciphertext or the full fingerprint.

// keyEncryptionKey returns SHA-256(secret || "|mp-key-encryption").
func keyEncryptionKey() ([]byte, error) {
	if len(anonSecret) == 0 {
		return nil, ErrNoAnonSecret
	}
	sum := sha256.Sum256(append(append([]byte{}, anonSecret...), "|mp-key-encryption"...))
	return sum[:], nil
}

// keyFingerprintKey returns SHA-256(secret || "|mp-key-fingerprint").
func keyFingerprintKey() ([]byte, error) {
	if len(anonSecret) == 0 {
		return nil, ErrNoAnonSecret
	}
	sum := sha256.Sum256(append(append([]byte{}, anonSecret...), "|mp-key-fingerprint"...))
	return sum[:], nil
}

// EncryptListingKey encrypts one seller API key for at-rest storage. The
// output is nonce||ciphertext with a fresh random nonce per call, so two
// encryptions of the same key never produce the same blob.
func EncryptListingKey(plaintext string) ([]byte, error) {
	key, err := keyEncryptionKey()
	if err != nil {
		return nil, err
	}
	gcm, err := newListingGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, errors.New("marketplace: cannot generate key nonce")
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// DecryptListingKey reverses EncryptListingKey. The plaintext is returned to
// the caller only; it must never be logged or included in errors (§2.3).
func DecryptListingKey(ciphertext []byte) (string, error) {
	key, err := keyEncryptionKey()
	if err != nil {
		return "", err
	}
	gcm, err := newListingGCM(key)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < gcm.NonceSize()+gcm.Overhead() {
		return "", errors.New("marketplace: stored key blob is malformed")
	}
	nonce, sealed := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		// Wrong secret or tampered blob; never echo the blob itself.
		return "", errors.New("marketplace: stored key cannot be decrypted")
	}
	return string(plain), nil
}

// ListingKeyFingerprint returns hex(HMAC-SHA256(fingerprintKey, plaintext)),
// 64 lowercase hex characters. The fingerprint is deterministic and
// irreversible; it backs the "one key listed once platform-wide" rule (§3.5)
// through the unique index on mp_listings.key_fingerprint.
func ListingKeyFingerprint(plaintext string) (string, error) {
	key, err := keyFingerprintKey()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(plaintext))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// newListingGCM builds the AES-256-GCM AEAD for a derived 32-byte key.
func newListingGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("marketplace: key cipher unavailable")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("marketplace: key cipher unavailable")
	}
	return gcm, nil
}
