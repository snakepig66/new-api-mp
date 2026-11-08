package marketplace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file mutate the package-level anonSecret through
// SetAnonSecret and restore it via preserveAnonSecret (identity_test.go), so
// they must not call t.Parallel().

const keycryptoTestPlaintext = "sk-test-secret-key-12345"

func TestKeycryptoRoundtrip(t *testing.T) {
	preserveAnonSecret(t)
	SetAnonSecret("test-secret")

	ciphertext, err := EncryptListingKey(keycryptoTestPlaintext)
	require.NoError(t, err)
	require.NotEmpty(t, ciphertext)
	assert.NotContains(t, string(ciphertext), keycryptoTestPlaintext,
		"the ciphertext must never embed the plaintext")

	back, err := DecryptListingKey(ciphertext)
	require.NoError(t, err)
	assert.Equal(t, keycryptoTestPlaintext, back)
}

func TestKeycryptoRandomNonce(t *testing.T) {
	preserveAnonSecret(t)
	SetAnonSecret("test-secret")

	first, err := EncryptListingKey(keycryptoTestPlaintext)
	require.NoError(t, err)
	second, err := EncryptListingKey(keycryptoTestPlaintext)
	require.NoError(t, err)
	assert.NotEqual(t, first, second, "each encryption must use a fresh random nonce")

	// Both still decrypt to the same key.
	back, err := DecryptListingKey(second)
	require.NoError(t, err)
	assert.Equal(t, keycryptoTestPlaintext, back)
}

func TestKeycryptoFingerprint(t *testing.T) {
	preserveAnonSecret(t)
	SetAnonSecret("test-secret")

	fp1, err := ListingKeyFingerprint(keycryptoTestPlaintext)
	require.NoError(t, err)
	fp2, err := ListingKeyFingerprint(keycryptoTestPlaintext)
	require.NoError(t, err)
	assert.Equal(t, fp1, fp2, "the fingerprint must be deterministic")
	assert.Regexp(t, "^[0-9a-f]{64}$", fp1, "fingerprint is lowercase hex HMAC-SHA256")

	other, err := ListingKeyFingerprint("sk-other-key-67890")
	require.NoError(t, err)
	assert.NotEqual(t, fp1, other, "different keys must not share a fingerprint")
}

func TestKeycryptoFingerprintSecretBound(t *testing.T) {
	preserveAnonSecret(t)

	SetAnonSecret("test-secret")
	fp1, err := ListingKeyFingerprint(keycryptoTestPlaintext)
	require.NoError(t, err)

	SetAnonSecret("other-secret")
	fp2, err := ListingKeyFingerprint(keycryptoTestPlaintext)
	require.NoError(t, err)
	assert.NotEqual(t, fp1, fp2, "the fingerprint must be bound to MP_KEY_SECRET")

	// And a key encrypted under one secret does not decrypt under another.
	ciphertext, err := EncryptListingKey(keycryptoTestPlaintext)
	require.NoError(t, err)
	SetAnonSecret("test-secret")
	_, err = DecryptListingKey(ciphertext)
	assert.Error(t, err, "ciphertext from another secret must not decrypt")
}

func TestKeycryptoNoSecret(t *testing.T) {
	preserveAnonSecret(t)

	anonSecret = nil
	_, err := EncryptListingKey(keycryptoTestPlaintext)
	assert.ErrorIs(t, err, ErrNoAnonSecret)
	_, err = DecryptListingKey([]byte("whatever"))
	assert.ErrorIs(t, err, ErrNoAnonSecret)
	_, err = ListingKeyFingerprint(keycryptoTestPlaintext)
	assert.ErrorIs(t, err, ErrNoAnonSecret)

	SetAnonSecret("")
	_, err = EncryptListingKey(keycryptoTestPlaintext)
	assert.ErrorIs(t, err, ErrNoAnonSecret, "empty secret must behave as unconfigured")
}

func TestKeycryptoTamperedCiphertext(t *testing.T) {
	preserveAnonSecret(t)
	SetAnonSecret("test-secret")

	ciphertext, err := EncryptListingKey(keycryptoTestPlaintext)
	require.NoError(t, err)

	// Flip one bit in the sealed body (past the nonce).
	tampered := append([]byte{}, ciphertext...)
	tampered[len(tampered)-1] ^= 0x01
	_, err = DecryptListingKey(tampered)
	assert.Error(t, err, "GCM authentication must reject a tampered blob")

	// Truncated blobs are malformed, not decryptable.
	_, err = DecryptListingKey(ciphertext[:4])
	assert.Error(t, err)
	_, err = DecryptListingKey(nil)
	assert.Error(t, err)
}

// M5: surrounding whitespace is normalized away before fingerprinting and
// encryption, so the same upstream credential always maps onto one
// fingerprint and one stored secret. Interior whitespace and control
// characters are rejected outright.
func TestKeycryptoNormalizesSurroundingWhitespace(t *testing.T) {
	preserveAnonSecret(t)
	SetAnonSecret("test-secret")

	fp1, err := ListingKeyFingerprint("sk-abc")
	require.NoError(t, err)
	fp2, err := ListingKeyFingerprint("sk-abc ")
	require.NoError(t, err)
	fp3, err := ListingKeyFingerprint("\t sk-abc\r\n")
	require.NoError(t, err)
	assert.Equal(t, fp1, fp2, `"sk-abc" and "sk-abc " must share one fingerprint`)
	assert.Equal(t, fp1, fp3, "all surrounding whitespace is trimmed")

	ciphertext, err := EncryptListingKey("  sk-abc  ")
	require.NoError(t, err)
	back, err := DecryptListingKey(ciphertext)
	require.NoError(t, err)
	assert.Equal(t, "sk-abc", back, "the stored key is the normalized form")

	for _, bad := range []string{"", "   ", "\t\n", "sk-abc def", "sk-abc\nxyz", "sk\x00abc", "sk\u00a0abc"} {
		_, err := ListingKeyFingerprint(bad)
		assert.ErrorIs(t, err, ErrInvalidListingKey, "key %q must be rejected", bad)
		_, err = EncryptListingKey(bad)
		assert.ErrorIs(t, err, ErrInvalidListingKey, "key %q must be rejected", bad)
	}
}

func TestKeycryptoErrorsNeverLeakKey(t *testing.T) {
	preserveAnonSecret(t)
	SetAnonSecret("test-secret")

	ciphertext, err := EncryptListingKey(keycryptoTestPlaintext)
	require.NoError(t, err)
	tampered := append([]byte{}, ciphertext...)
	tampered[len(tampered)-1] ^= 0x01

	_, decErr := DecryptListingKey(tampered)
	require.Error(t, decErr)
	assert.NotContains(t, decErr.Error(), keycryptoTestPlaintext, "§2.3: errors must not contain the key")
	assert.NotContains(t, decErr.Error(), string(tampered), "§2.3: errors must not contain the ciphertext")

	_, decErr = DecryptListingKey([]byte("short"))
	require.Error(t, decErr)
	assert.NotContains(t, decErr.Error(), keycryptoTestPlaintext)
}
