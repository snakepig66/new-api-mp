package marketplace

// apierror.go — 对外错误码收敛 (§3.10). The marketplace's outward API surface
// (the buyer-facing relay path and any buyer-facing console endpoint that
// speaks to API clients) exposes exactly four HTTP statuses, always in the
// OpenAI error format {"error":{"message","type","code"}}:
//
//	401 无效 Key        authentication_error / invalid_api_key
//	402 余额不足        insufficient_quota   / insufficient_balance
//	400 参数错误        invalid_request_error / invalid_request
//	503 暂无可用供应商  server_error          / no_supplier_available
//
// Messages must never carry key material or internal detail (§2.3).

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// apiErrorSpec is the fixed §3.10 wording of one outwardly visible status.
type apiErrorSpec struct {
	Type    string
	Code    string
	Message string
}

// apiErrorSpecs is the §3.10 status → (type, code, default message) table.
var apiErrorSpecs = map[int]apiErrorSpec{
	http.StatusUnauthorized:       {Type: "authentication_error", Code: "invalid_api_key", Message: "Invalid API key"},
	http.StatusPaymentRequired:    {Type: "insufficient_quota", Code: "insufficient_balance", Message: "Insufficient balance"},
	http.StatusBadRequest:         {Type: "invalid_request_error", Code: "invalid_request", Message: "Invalid request parameters"},
	http.StatusServiceUnavailable: {Type: "server_error", Code: "no_supplier_available", Message: "No supplier available"},
}

// WriteAPIError writes one of the four §3.10 errors in OpenAI format and
// aborts the gin handler chain. msg overrides the fixed default wording when
// non-empty; it must carry semantics only (never key material, never internal
// detail, §2.3).
//
// A status outside the §3.10 table collapses to 503: no other status code may
// leak outward, and an unexpected status signals an internal failure, which
// the buyer can only retry later.
func WriteAPIError(c *gin.Context, status int, msg string) {
	spec, ok := apiErrorSpecs[status]
	if !ok {
		status = http.StatusServiceUnavailable
		spec = apiErrorSpecs[status]
	}
	if msg == "" {
		msg = spec.Message
	}
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{
			"message": msg,
			"type":    spec.Type,
			"code":    spec.Code,
		},
	})
}

// WriteMarketplaceError maps a marketplace-internal error onto the §3.10
// outward codes and writes it in OpenAI format:
//
//	ErrInsufficientBalance  → 402 余额不足
//	ErrModelNotAddedToKey   → 400 参数错误（文案提示先把模型加到 Key，§3.6）
//	ErrNoListingAvailable   → 503 暂无可用供应商
//	ErrNoPrice              → 503 暂无可用供应商（无覆盖价目 = 当前无可计费货源）
//	其他任何错误            → 503 固定文案（内部细节不外泄，§2.3/§3.10）
//
// ErrModelNotAddedToKey is the sentinel already defined in relayhook.go; it
// is reused here, not redefined.
func WriteMarketplaceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInsufficientBalance):
		WriteAPIError(c, http.StatusPaymentRequired, "")
	case errors.Is(err, ErrModelNotAddedToKey):
		WriteAPIError(c, http.StatusBadRequest, ErrModelNotAddedToKey.Error())
	case errors.Is(err, ErrNoListingAvailable):
		WriteAPIError(c, http.StatusServiceUnavailable, "")
	case errors.Is(err, ErrNoPrice):
		WriteAPIError(c, http.StatusServiceUnavailable, "")
	default:
		WriteAPIError(c, http.StatusServiceUnavailable, "")
	}
}
