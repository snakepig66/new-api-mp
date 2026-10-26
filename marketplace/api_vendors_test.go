package marketplace

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSellerCommonAddresses covers GET /seller/common-addresses: only
// host-exact, trusted rows are offered as wizard prefill addresses, with the
// vendor display name, kind and endpoint set the badge needs.
func TestSellerCommonAddresses(t *testing.T) {
	f := newSellerAPIFixture(t)

	// A watch row must never be advertised as a common address.
	require.NoError(t, f.db.Create(&TrustPool{
		Host:       "watch-only.example.com",
		VendorSlug: "watch-vendor",
		Kind:       TrustKindThirdParty,
		Trust:      TrustWatch,
		Endpoints:  `["openai_chat"]`,
		Source:     "manual",
	}).Error)

	w := f.do(t, http.MethodGet, "/api/marketplace/seller/common-addresses", nil, int(f.sellerID))
	require.Equal(t, http.StatusOK, w.Code)
	rows, ok := decodeSellerBody(t, w)["data"].([]any)
	require.True(t, ok)

	var deepseek map[string]any
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		require.True(t, ok)
		assert.NotEqual(t, "watch-only.example.com", row["host"])
		if row["host"] == "api.deepseek.com" {
			deepseek = row
		}
	}
	require.NotNil(t, deepseek, "the seeded DeepSeek address is a trusted common address")
	assert.Equal(t, "DeepSeek", deepseek["vendor_name"])
	assert.Equal(t, TrustKindOfficial, deepseek["kind"])
	endpoints, ok := deepseek["endpoints"].([]any)
	require.True(t, ok)
	assert.Contains(t, endpoints, "openai_chat")
}

// TestSellerCommonAddressesRequiresAuth locks the endpoint behind the seller
// console login.
func TestSellerCommonAddressesRequiresAuth(t *testing.T) {
	f := newSellerAPIFixture(t)
	w := f.do(t, http.MethodGet, "/api/marketplace/seller/common-addresses", nil, 0)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
