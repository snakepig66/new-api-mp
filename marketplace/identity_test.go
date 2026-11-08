package marketplace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// preserveAnonSecret snapshots the package-level anonSecret and restores it
// after the test. Tests in this file mutate that global, so they must not
// call t.Parallel().
func preserveAnonSecret(t *testing.T) {
	t.Helper()
	orig := anonSecret
	t.Cleanup(func() { anonSecret = orig })
}

func TestAnonBuyerCodeDeterministic(t *testing.T) {
	preserveAnonSecret(t)
	SetAnonSecret("test-secret")

	first, err := AnonBuyerCode(10023, 20001)
	require.NoError(t, err)
	second, err := AnonBuyerCode(10023, 20001)
	require.NoError(t, err)
	assert.Equal(t, first, second, "same (secret, seller, buyer) must produce a stable code")

	otherSeller, err := AnonBuyerCode(10024, 20001)
	require.NoError(t, err)
	assert.NotEqual(t, first, otherSeller, "each seller must see a different code for the same buyer (§3.1)")

	otherBuyer, err := AnonBuyerCode(10023, 20002)
	require.NoError(t, err)
	assert.NotEqual(t, first, otherBuyer, "different buyers must not share a code under the same seller")
}

func TestAnonBuyerCodeFormat(t *testing.T) {
	preserveAnonSecret(t)
	SetAnonSecret("test-secret")

	code, err := AnonBuyerCode(10023, 20001)
	require.NoError(t, err)
	assert.Len(t, code, 8, "code is the first 8 hex chars of the HMAC (§3.1)")
	assert.Regexp(t, "^[0-9a-f]{8}$", code, "code must be lowercase hex")

	// The package-internal helper follows the same contract for edge inputs.
	assert.Regexp(t, "^[0-9a-f]{8}$", anonBuyerCode([]byte("test-secret"), 0, 0))
}

func TestAnonBuyerCodeSecretRequired(t *testing.T) {
	preserveAnonSecret(t)

	anonSecret = nil
	_, err := AnonBuyerCode(10023, 20001)
	assert.ErrorIs(t, err, ErrNoAnonSecret)

	SetAnonSecret("")
	_, err = AnonBuyerCode(10023, 20001)
	assert.ErrorIs(t, err, ErrNoAnonSecret, "empty secret must behave as unconfigured")

	SetAnonSecret("test-secret")
	code, err := AnonBuyerCode(10023, 20001)
	require.NoError(t, err)
	assert.NotEmpty(t, code)
}
