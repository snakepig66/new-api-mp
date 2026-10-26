package marketplace

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrNoAnonSecret is returned when MP_KEY_SECRET is not configured but an
// anonymous buyer code is requested.
var ErrNoAnonSecret = errors.New("marketplace: MP_KEY_SECRET is not set")

// anonSecret derives per-seller anonymous buyer codes (§3.1). It is loaded
// from MP_KEY_SECRET at Init and never logged or returned by any API.
var anonSecret []byte

// SetAnonSecret configures the HMAC secret for anonymous buyer codes.
func SetAnonSecret(secret string) {
	anonSecret = []byte(secret)
}

// anonBuyerCode computes hex(HMAC-SHA256(secret, "sellerID|buyerID"))[:8].
// Each seller sees a different code for the same buyer, and the code cannot be
// reversed to the buyer id without the secret (后台反查走数据库 request 记录).
func anonBuyerCode(secret []byte, sellerID, buyerID int64) string {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "%d|%d", sellerID, buyerID)
	return hex.EncodeToString(mac.Sum(nil))[:8]
}

// AnonBuyerCode returns the buyer's anonymous code as seen by the given seller.
func AnonBuyerCode(sellerID, buyerID int64) (string, error) {
	if len(anonSecret) == 0 {
		return "", ErrNoAnonSecret
	}
	return anonBuyerCode(anonSecret, sellerID, buyerID), nil
}

// RequestUserPseudonym returns the upstream `user` identity the marketplace
// sends for one (seller, buyer) pair. It uses the same keyed HMAC as the
// anonymous buyer code but a different domain tag, so the value a seller sees
// in their console cannot be correlated with the upstream request field.
func RequestUserPseudonym(sellerID, buyerID int64) (string, error) {
	if len(anonSecret) == 0 {
		return "", ErrNoAnonSecret
	}
	mac := hmac.New(sha256.New, anonSecret)
	fmt.Fprintf(mac, "request-user|%d|%d", sellerID, buyerID)
	return "mp_" + hex.EncodeToString(mac.Sum(nil))[:16], nil
}
