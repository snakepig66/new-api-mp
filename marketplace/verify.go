package marketplace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// Verification of a listing key (§3.5): the wizard submits key + models once
// and the platform checks the key is valid, reads the vendor balance, lists
// the available models, measures /models latency and probes prompt-cache
// support. Verification and patrol traffic runs on its own rate limit and
// concurrency budget so it never consumes buyer-facing slots (§3.5 独立限流
// 桶). Per §2.3 no error message, FailReason or probe detail from this file
// ever contains the API key.

// ProbeTypeVerify marks an mp_probes row written by verification (§4.2).
const ProbeTypeVerify = "verify"

// Defaults for the verification limiter (§3.5): a small concurrency cap and a
// low per-minute budget keep patrol/verify traffic well clear of buyers.
const (
	VerifyMaxConcurrent = 3
	VerifyPerMinute     = 20
)

// maxVerifyBodyBytes caps upstream response bodies read during verification;
// a whitelisted vendor should answer in kilobytes, so a megabyte is generous.
const maxVerifyBodyBytes = 1 << 20

// VerifyResult is the outcome of one full key verification (§3.5).
type VerifyResult struct {
	OK                 bool
	KeyValid           bool
	BalanceMicro       *int64   // vendor account balance in 微元; nil = unreadable
	Models             []string // model ids the vendor returned for this key
	LatencyMs          int64    // /models round-trip latency
	PromptCacheSupport bool
	FailReason         string // never carries the key or the upstream body (§3.11)
}

// VerifyListingKey runs one complete verification of a seller's key against
// the whitelisted vendor. The caller injects the http.Client: production
// passes EgressHTTPClient() (§3.11 whitelist + dial-time IP validation),
// tests pass an httptest client. ctx bounds the whole check (callers should
// allow ~30s).
//
// Flow: GET /models (timed; gates everything) → GET /user/balance
// (best-effort) → POST /chat/completions with max_tokens=1 to detect prompt
// caching. The last step spends real vendor credit — it is skipped whenever
// the result can no longer turn OK (invalid key or zero models).
func VerifyListingKey(ctx context.Context, client *http.Client, vendor Vendor, apiKey string) VerifyResult {
	probeModel := ""
	if len(vendor.Models) > 0 {
		probeModel = vendor.Models[0]
	}
	return VerifyListingKeyWithModel(ctx, client, vendor, apiKey, probeModel)
}

// VerifyListingKeyWithModel is VerifyListingKey with an explicit upstream
// probe model. Callers resolve it from the channel_model_map, so the paid
// minimal chat call names a model the vendor really exposes (the platform
// model id and the upstream name differ on hosted channels).
func VerifyListingKeyWithModel(ctx context.Context, client *http.Client, vendor Vendor, apiKey, probeModel string) VerifyResult {
	var res VerifyResult
	base := VendorOpenAIBase(vendor)

	// 1. Key validity + model list + latency.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		res.FailReason = "vendor base url is malformed"
		return res
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	start := time.Now()
	resp, err := client.Do(req)
	res.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		// Transport errors carry method+URL but never headers, so no key.
		res.FailReason = "vendor /models request failed: " + stripKey(err.Error(), apiKey)
		return res
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// §3.11: the upstream body is drained, never echoed.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		res.FailReason = "vendor /models returned " + httpStatusMeaning(resp.StatusCode)
		return res
	}
	res.KeyValid = true
	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, maxVerifyBodyBytes), &modelsResp); err != nil {
		res.FailReason = "vendor /models returned malformed json"
	} else {
		for _, m := range modelsResp.Data {
			if m.ID != "" && !containsString(res.Models, m.ID) {
				res.Models = append(res.Models, m.ID)
			}
		}
		if len(res.Models) == 0 {
			res.FailReason = "vendor returned an empty model list"
		}
	}

	// 2. Balance (DeepSeek format). Best-effort: 404 / non-200 / malformed
	// all yield BalanceMicro=nil and never fail the verification.
	res.BalanceMicro = fetchBalanceMicro(ctx, client, base, apiKey)

	// 3. Prompt-cache probe. NOTE: this call costs real vendor credit (one
	// chat completion with max_tokens=1); it runs only while the result can
	// still flip OK to true. probeModel is the vendor's upstream name.
	if len(res.Models) > 0 && probeModel != "" {
		res.PromptCacheSupport = probePromptCacheSupport(ctx, client, base, probeModel, apiKey)
	}

	// 4. Verdict.
	res.OK = res.KeyValid && len(res.Models) > 0
	res.FailReason = stripKey(res.FailReason, apiKey)
	return res
}

// fetchBalanceMicro reads the vendor balance endpoint and converts the first
// balance entry from yuan to 微元. Any failure (network, non-200, malformed
// json, unparseable amount) returns nil — an unreadable balance is not a
// verification failure (§3.5).
func fetchBalanceMicro(ctx context.Context, client *http.Client, baseURL, apiKey string) *int64 {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/user/balance", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil
	}
	// DeepSeek shape: {"balance_infos":[{"currency":"CNY","total_balance":"12.34"}]}
	var br struct {
		BalanceInfos []struct {
			Currency     string `json:"currency"`
			TotalBalance string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, maxVerifyBodyBytes), &br); err != nil {
		return nil
	}
	if len(br.BalanceInfos) == 0 {
		return nil
	}
	return yuanStringToMicro(br.BalanceInfos[0].TotalBalance)
}

// yuanStringToMicro converts a decimal yuan string ("12.34") to integer 微元
// (×1e6) with exact arithmetic — floats are banned from money paths (§2.1).
// Sub-micro fractions truncate toward zero; unparseable or int64-overflowing
// inputs return nil instead of panicking.
func yuanStringToMicro(s string) *int64 {
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(s))
	if !ok {
		return nil
	}
	rat.Mul(rat, big.NewRat(1_000_000, 1))
	micro, rem := new(big.Int).QuoRem(rat.Num(), rat.Denom(), new(big.Int))
	_ = rem // truncation toward zero is intended for sub-micro dust
	if !micro.IsInt64() {
		return nil
	}
	v := micro.Int64()
	return &v
}

// probePromptCacheSupport sends a minimal chat completion (max_tokens=1, one
// short user message) and reports whether the response usage carries the
// vendor's prompt-cache fields (prompt_cache_hit_tokens or
// prompt_cache_miss_tokens). NOTE: this call costs real vendor credit; it is
// the §3.5 automatic probe shown to the seller in the listing wizard. Any
// failure — network, non-200, missing usage — reports false and never fails
// the verification.
func probePromptCacheSupport(ctx context.Context, client *http.Client, baseURL, model, apiKey string) bool {
	reqBody, err := common.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1,
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
	})
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", strings.NewReader(string(reqBody)))
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return false
	}
	// Field presence, not value, signals support: a vendor that knows prompt
	// caching always reports hit/miss counters, even when both are zero.
	var cr struct {
		Usage map[string]any `json:"usage"`
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, maxVerifyBodyBytes), &cr); err != nil {
		return false
	}
	if cr.Usage == nil {
		return false
	}
	_, hit := cr.Usage["prompt_cache_hit_tokens"]
	_, miss := cr.Usage["prompt_cache_miss_tokens"]
	return hit || miss
}

// httpStatusMeaning renders the semantics of an upstream failure without
// copying the upstream body (§3.11 上游错误不原样回显).
func httpStatusMeaning(code int) string {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Sprintf("key rejected (http %d)", code)
	case code == http.StatusTooManyRequests:
		return "rate limited (http 429)"
	case code >= 500:
		return fmt.Sprintf("vendor server error (http %d)", code)
	default:
		return fmt.Sprintf("http %d", code)
	}
}

// stripKey is the §2.3 belt-and-braces guard: even if some upstream error
// text ever embedded the key, it leaves this package redacted.
func stripKey(s, apiKey string) string {
	if s == "" || apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, apiKey, "<redacted>")
}

// containsString reports whether xs already holds x (kept local to avoid
// pulling slices into the hot read for one membership check).
func containsString(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// VerifyLimiter is the §3.5 independent budget for verification and patrol
// traffic: a concurrency semaphore plus a token bucket, so probes never
// consume the buyer-facing concurrency/RPM of any listing. go.mod has no
// golang.org/x/time dependency and new ones are not allowed, so the bucket
// is hand-rolled here: tokens refill at perMinute/60 per second up to a
// perMinute burst, and reservations may drive the balance negative — a
// cancelled wait still spends its reservation, which is the conservative
// direction for a rate limiter.
type VerifyLimiter struct {
	sem chan struct{} // concurrency slots, capacity maxConcurrent

	mu       sync.Mutex
	tokens   float64
	capacity float64
	perSec   float64
	last     time.Time
}

// NewVerifyLimiter builds a limiter allowing maxConcurrent in-flight
// verifications and perMinute starts per minute. Values below 1 clamp to 1.
func NewVerifyLimiter(maxConcurrent, perMinute int) *VerifyLimiter {
	maxConcurrent = max(maxConcurrent, 1)
	perMinute = max(perMinute, 1)
	return &VerifyLimiter{
		sem:      make(chan struct{}, maxConcurrent),
		tokens:   float64(perMinute),
		capacity: float64(perMinute),
		perSec:   float64(perMinute) / 60,
		last:     time.Now(),
	}
}

// Acquire blocks until both a rate token and a concurrency slot are
// available, or ctx is done (returning ctx.Err()). The rate token is reserved
// before the semaphore wait: a token spent while queued behind a busy
// semaphore is simply conservative limiting.
func (l *VerifyLimiter) Acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if wait := l.reserveToken(); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	select {
	case l.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// reserveToken refills the bucket by elapsed wall time, spends one token and
// returns how long the caller must wait for the balance to cover the spend.
func (l *VerifyLimiter) reserveToken() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.tokens = min(l.capacity, l.tokens+now.Sub(l.last).Seconds()*l.perSec)
	l.last = now
	l.tokens--
	if l.tokens >= 0 {
		return 0
	}
	return time.Duration(-l.tokens / l.perSec * float64(time.Second))
}

// Release returns one concurrency slot. Releasing without a matching Acquire
// is a caller bug and panics.
func (l *VerifyLimiter) Release() {
	select {
	case <-l.sem:
	default:
		panic("marketplace: VerifyLimiter.Release without Acquire")
	}
}

// VerifyListing verifies one draft listing end to end: load → StartVerification
// (draft→verifying) → decrypt the stored key → pass the independent
// VerifyLimiter → VerifyListingKey against the whitelisted vendor → record an
// mp_probes row (type "verify") → on success PassVerification (→canary), on
// failure roll back to draft. The http.Client is injected exactly as for
// VerifyListingKey (EgressHTTPClient in production, httptest in tests).
//
// A failed verification is a result, not an error: it returns res.OK=false
// with a nil error and FailReason set. A missing listing yields
// ErrListingNotFound, a non-draft listing ErrInvalidTransition.
func VerifyListing(ctx context.Context, db *gorm.DB, client *http.Client, limiter *VerifyLimiter, listingID int64) (VerifyResult, error) {
	listing, err := GetListing(db, listingID)
	if err != nil {
		return VerifyResult{}, err
	}
	if listing.Status != ListingDraft {
		return VerifyResult{}, fmt.Errorf("%w: %s -> verifying", ErrInvalidTransition, listing.Status)
	}
	vendor, ok := lookupVendorForDB(db, listing.Vendor)
	if !ok {
		return VerifyResult{}, fmt.Errorf("%w: %q", ErrVendorNotWhitelisted, listing.Vendor)
	}
	// Resolve every selected model to its upstream name before touching the
	// key: a listing whose mapping is incomplete must never pass verification.
	upstreamByModel := make(map[string]string, len(listing.Models))
	for _, m := range listing.Models {
		cm, found := ChannelModelFor(db, listing.Vendor, m)
		if !found || strings.TrimSpace(cm.UpstreamModel) == "" {
			return VerifyResult{}, fmt.Errorf("%w: %q has no upstream mapping on channel %q", ErrVendorModelUnmapped, m, listing.Vendor)
		}
		upstreamByModel[m] = cm.UpstreamModel
	}
	if err := StartVerification(db, listingID); err != nil {
		return VerifyResult{}, err
	}

	apiKey, err := DecryptListingKey(listing.KeyCiphertext)
	if err != nil {
		// Nothing was probed; roll the status back before reporting.
		return VerifyResult{}, errors.Join(err, revertVerificationToDraft(db, listingID))
	}
	probeModel := ""
	for _, m := range listing.Models {
		if upstreamByModel[m] != "" {
			probeModel = upstreamByModel[m]
			break
		}
	}
	if err := limiter.Acquire(ctx); err != nil {
		return VerifyResult{}, errors.Join(err, revertVerificationToDraft(db, listingID))
	}
	res := VerifyListingKeyWithModel(ctx, client, vendor, apiKey, probeModel)
	limiter.Release()

	// The key must expose every selected model, not merely be valid. Cloud
	// upstreams often append a date to the model id, so compare by prefix in
	// both directions after normalizing separators.
	var missing []string
	for _, m := range listing.Models {
		if !keyExposesModel(res.Models, upstreamByModel[m]) {
			missing = append(missing, m)
		}
	}
	if res.OK && len(missing) > 0 {
		res.OK = false
		res.FailReason = "key cannot call the selected model(s): " + strings.Join(missing, ", ")
	}

	// Fingerprint spot check when the platform holds the origin vendor's key.
	// A mismatch keeps the listing in draft; it never bans the qualification.
	fingerprintChecked := false
	fingerprintOK := true
	fingerprintRatio := 0.0
	fingerprintReason := ""
	if res.OK {
		for _, m := range listing.Models {
			model, found := ModelPoolByName(db, m)
			if !found {
				continue
			}
			officialKey, hasKey := PlatformKeyForVendor(model.OriginVendor)
			if !hasKey {
				continue
			}
			officialVendor, foundVendor := lookupVendorForDB(db, model.OriginVendor)
			if !foundVendor {
				continue
			}
			officialUpstream := model.Name
			if cm, foundCM := ChannelModelFor(db, model.OriginVendor, model.Name); foundCM {
				officialUpstream = cm.UpstreamModel
			}
			prompt := model.VerifyPrompt
			if strings.TrimSpace(prompt) == "" {
				prompt = defaultFingerprintPrompt
			}
			fp := CompareFingerprint(ctx, client, officialVendor, officialKey, officialUpstream,
				vendor, apiKey, upstreamByModel[m], prompt)
			if !fp.Checked {
				continue
			}
			fingerprintChecked = true
			fingerprintOK = fp.WithinTolerance
			fingerprintRatio = fp.Ratio
			fingerprintReason = fp.FailReason
			if !fp.WithinTolerance {
				res.OK = false
				res.FailReason = "fingerprint mismatch: the channel is not serving the official model"
			}
			break
		}
	}

	// Record the probe (§4.2): the JSON detail summarizes the check and
	// never contains the key (§2.3).
	detailMap := map[string]any{
		"key_valid":    res.KeyValid,
		"models":       len(res.Models),
		"has_balance":  res.BalanceMicro != nil,
		"prompt_cache": res.PromptCacheSupport,
		"fail_reason":  res.FailReason,
	}
	if fingerprintChecked {
		detailMap["fingerprint_checked"] = true
		detailMap["fingerprint_ok"] = fingerprintOK
		detailMap["fingerprint_ratio"] = fingerprintRatio
		detailMap["fingerprint_reason"] = fingerprintReason
	}
	detail, err := common.Marshal(detailMap)
	if err != nil {
		detail = []byte("{}")
	}
	probe := Probe{
		ListingID: listingID,
		Type:      ProbeTypeVerify,
		OK:        res.OK,
		LatencyMs: res.LatencyMs,
		Detail:    string(detail),
	}
	if err := db.Create(&probe).Error; err != nil {
		return res, fmt.Errorf("marketplace: record verify probe: %w", err)
	}

	if res.OK {
		if err := PassVerification(db, listingID); err != nil {
			return res, err
		}
		return res, nil
	}
	if err := revertVerificationToDraft(db, listingID); err != nil {
		return res, err
	}
	return res, nil
}

// keyExposesModel reports whether the vendor's /models list contains the
// upstream id, accepting the date/suffix variants vendors add to model ids.
func keyExposesModel(available []string, upstream string) bool {
	upstream = strings.ToLower(strings.TrimSpace(upstream))
	if upstream == "" {
		return false
	}
	for _, name := range available {
		got := strings.ToLower(strings.TrimSpace(name))
		if got == upstream || strings.HasPrefix(got, upstream+"-") || strings.HasPrefix(got, upstream+".") ||
			strings.HasPrefix(got, upstream+"_") || strings.HasPrefix(got, upstream+"@") {
			return true
		}
	}
	return false
}

// revertVerificationToDraft is the verification-failure rollback edge of the
// §3.5 state machine: verifying → draft. listing.go deliberately exposes no
// named transition for it (a listing leaves verifying only via
// PassVerification or BanListing), so the edge lives here as a conditional
// UPDATE inside a transaction: exactly one affected row, or a concurrent
// transition won and the revert reports ErrInvalidTransition instead of
// clobbering the newer state.
func revertVerificationToDraft(db *gorm.DB, id int64) error {
	return db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&Listing{}).
			Where("id = ? AND status = ?", id, ListingVerifying).
			Update("status", ListingDraft)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("%w: verifying -> draft", ErrInvalidTransition)
		}
		return nil
	})
}
