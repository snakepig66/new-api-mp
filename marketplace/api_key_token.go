package marketplace

// api_key_token.go — buyer key limits: expiration time and IP whitelist.
//
// Both fields live on the upstream token row, not on mp_key_prefs. They get a
// dedicated update endpoint instead of PUT /token because that endpoint
// requires echoing the whole token back; a partial body would silently clear
// quota, status and routing fields the console never edits.
//
// The IP format matches middleware token auth (model.Token.GetIpLimits +
// common.IsIpInCIDRList): one plain IP or CIDR per line.

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// keyNoExpiry is the upstream convention for "never expires".
const keyNoExpiry int64 = -1

// normalizeKeyExpiry accepts -1/0 as "never" and any future Unix second.
func normalizeKeyExpiry(expiredTime int64) (int64, error) {
	if expiredTime == 0 {
		return keyNoExpiry, nil
	}
	if expiredTime == keyNoExpiry {
		return keyNoExpiry, nil
	}
	if expiredTime < 0 || expiredTime <= common.GetTimestamp() {
		return 0, errors.New("expiration time must be in the future")
	}
	return expiredTime, nil
}

// normalizeKeyAllowIPs validates a newline/comma separated whitelist and
// returns the canonical newline form. Empty input means no restriction.
func normalizeKeyAllowIPs(raw string) (*string, error) {
	entries := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case '\n', '\r', ',', ' ', '\t', ';', '；':
			return true
		default:
			return false
		}
	})
	clean := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if net.ParseIP(entry) == nil {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return nil, fmt.Errorf("invalid IP or CIDR: %s", entry)
			}
		}
		if _, dup := seen[entry]; dup {
			continue
		}
		seen[entry] = struct{}{}
		clean = append(clean, entry)
	}
	if len(clean) == 0 {
		return nil, nil
	}
	joined := strings.Join(clean, "\n")
	return &joined, nil
}

func keyAllowIPsString(raw *string) string {
	if raw == nil {
		return ""
	}
	return *raw
}

type keyTokenUpdateRequest struct {
	TokenID     int64   `json:"token_id"`
	ExpiredTime int64   `json:"expired_time"`
	AllowIps    *string `json:"allow_ips"`
}

// putKeyTokenLimits answers PUT /key/token with the two token-row fields the
// console owns. Extending an expired key re-enables it; a manually disabled
// key stays disabled.
func putKeyTokenLimits(c *gin.Context) {
	userID, ok := keyAPIUserID(c)
	if !ok {
		return
	}
	var req keyTokenUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeKeyAPIBadRequest(c, "bad_request_body", "invalid request body: "+err.Error())
		return
	}
	if !requireOwnToken(c, userID, req.TokenID) {
		return
	}
	expiredTime, err := normalizeKeyExpiry(req.ExpiredTime)
	if err != nil {
		writeKeyAPIBadRequest(c, "invalid_expired_time", err.Error())
		return
	}
	allowIPs, err := normalizeKeyAllowIPs(keyAllowIPsString(req.AllowIps))
	if err != nil {
		writeKeyAPIBadRequest(c, "invalid_allow_ips", err.Error())
		return
	}
	token, err := model.GetTokenByIds(int(req.TokenID), userID)
	if err != nil {
		writeKeyAPINotFound(c, "token_not_found", "token not found")
		return
	}
	token.ExpiredTime = expiredTime
	token.AllowIps = allowIPs
	if token.Status == common.TokenStatusExpired && (expiredTime == keyNoExpiry || expiredTime > common.GetTimestamp()) {
		token.Status = common.TokenStatusEnabled
	}
	if err := token.Update(); err != nil {
		writeKeyAPIInternal(c, "update token limits: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"expired_time": token.ExpiredTime,
			"allow_ips":    keyAllowIPsString(token.AllowIps),
		},
	})
}
