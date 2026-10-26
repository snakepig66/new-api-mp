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

// ModelVerifyResult is one selected model's real callability check. A vendor
// can list a model in /models while refusing to serve it (unactivated
// Bailian/Volcengine models, missing inference endpoints), so listing
// presence alone is not enough: verification sends one max_tokens=1 request
// per selected model and records the outcome here.
type ModelVerifyResult struct {
	Model              string `json:"model"`
	UpstreamModel      string `json:"upstream_model"`
	OK                 bool   `json:"ok"`
	PromptCacheSupport bool   `json:"prompt_cache_support"`
	LatencyMs          int64  `json:"latency_ms"`
	FailReason         string `json:"fail_reason,omitempty"`
}

// VerifyResult is the outcome of one full key verification (§3.5).
type VerifyResult struct {
	OK                 bool
	KeyValid           bool
	BalanceMicro       *int64   // vendor account balance in 微元; nil = unreadable
	Models             []string // model ids the vendor returned for this key
	LatencyMs          int64    // /models round-trip latency
	PromptCacheSupport bool
	// ModelResults is the per-model call result for VerifyListing. The key
	// verification helper fills at most one entry (its explicit probe model).
	ModelResults []ModelVerifyResult
	// Fingerprints is the per-model fail-closed tokenizer comparison recorded
	// by VerifyListing (and the patrol). An empty entry or Checked=false means
	// the model was not certified against the official endpoint.
	Fingerprints []FingerprintModelResult
	FailReason   string // never carries the key or the upstream body (§3.11)
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

	// 3. Callability probe. NOTE: this call costs real vendor credit (one chat
	// completion with max_tokens=1); it runs only while the result can still
	// flip OK to true. probeModel is the vendor's upstream name.
	if len(res.Models) > 0 && probeModel != "" {
		mr := ModelVerifyResult{Model: probeModel, UpstreamModel: probeModel}
		mr.OK, mr.PromptCacheSupport, mr.LatencyMs, mr.FailReason = probeModelCall(ctx, client, base, probeModel, apiKey)
		res.ModelResults = []ModelVerifyResult{mr}
		res.PromptCacheSupport = mr.PromptCacheSupport
		if !mr.OK {
			// A key that cannot actually call its model is not verified: the
			// model may be listed while unactivated or lacking an endpoint.
			res.FailReason = mr.FailReason
		}
	}

	// 4. Verdict.
	res.OK = res.KeyValid && len(res.Models) > 0
	for _, mr := range res.ModelResults {
		if !mr.OK {
			res.OK = false
		}
	}
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

// probePromptCacheSupport is the prompt-cache-only view of probeModelCall,
// kept for the admin channel verifier. Probe failures are deliberately
// indistinguishable from "no cache support" there; listing verification uses
// probeModelCall so it can report the real reason.
func probePromptCacheSupport(ctx context.Context, client *http.Client, baseURL, model, apiKey string) bool {
	ok, cache, _, _ := probeModelCall(ctx, client, baseURL, model, apiKey)
	return ok && cache
}

// probeModelCall sends one minimal chat completion (max_tokens=1) and reports
// whether the model is actually callable, whether prompt caching is
// advertised (usage carries prompt_cache_hit_tokens or
// prompt_cache_miss_tokens), its latency, and a seller-readable failure
// reason. NOTE: this call costs real vendor credit.
func probeModelCall(ctx context.Context, client *http.Client, baseURL, model, apiKey string) (ok, cache bool, latencyMs int64, reason string) {
	reqBody, err := common.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1,
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
	})
	if err != nil {
		return false, false, 0, "无法构造模型探活请求"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", strings.NewReader(string(reqBody)))
	if err != nil {
		return false, false, 0, "上游地址格式不正确"
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := client.Do(req)
	latencyMs = time.Since(start).Milliseconds()
	if err != nil {
		if ctx.Err() != nil {
			return false, false, latencyMs, "模型探活超时"
		}
		return false, false, latencyMs, "模型探活失败：" + stripKey(err.Error(), apiKey)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return false, false, latencyMs, modelCallFailureReason(resp.StatusCode)
	}
	// HTTP 200 is only callability when the body is a real chat completion:
	// it must decode, carry at least one choice, non-empty usage and a
	// non-empty model id. A 200 with an error envelope or a truncated body is
	// a failed probe (fail-closed), never a served model, and the prompt-cache
	// fields remain informational only.
	var cr struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, maxVerifyBodyBytes), &cr); err != nil {
		return false, false, latencyMs, "模型返回了无法解析的响应"
	}
	if len(cr.Choices) == 0 || len(cr.Usage) == 0 {
		return false, false, latencyMs, "模型返回的响应缺少 choices 或 usage"
	}
	if strings.TrimSpace(cr.Model) == "" {
		return false, false, latencyMs, "模型返回的响应缺少 model 字段"
	}
	_, hit := cr.Usage["prompt_cache_hit_tokens"]
	_, miss := cr.Usage["prompt_cache_miss_tokens"]
	return true, hit || miss, latencyMs, ""
}

// modelCallFailureReason translates an upstream status into a seller-facing
// reason (the body itself is never surfaced, §3.11).
func modelCallFailureReason(code int) string {
	switch {
	case code == http.StatusBadRequest:
		return "上游拒绝请求（HTTP 400）：模型可能未开通或参数不受支持"
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Sprintf("这个 Key 无权调用该模型（HTTP %d）", code)
	case code == http.StatusNotFound:
		return "模型不存在（HTTP 404）：百炼/火山等平台可能还需要单独开通模型或推理接入点"
	case code == http.StatusTooManyRequests:
		return "上游限流（HTTP 429），请稍后重新检测"
	case code >= 500:
		return fmt.Sprintf("上游服务异常（HTTP %d），请稍后重新检测", code)
	default:
		return fmt.Sprintf("模型探活失败（HTTP %d）", code)
	}
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
	if err := limiter.Acquire(ctx); err != nil {
		return VerifyResult{}, errors.Join(err, revertVerificationToDraft(db, listingID))
	}
	// Seller-defined upstreams mount /models either at the base or under /v1;
	// detect the answering form once and pin it (base + strip flag) so both
	// this verification and the relay dial the same path.
	if isCustomChannelSlug(listing.Vendor) {
		vendor, err = normalizeCustomChannel(ctx, client, db, &listing, vendor, apiKey)
		if err != nil {
			return VerifyResult{}, errors.Join(err, revertVerificationToDraft(db, listingID))
		}
	}
	// The key check itself (models + balance) runs without its single paid
	// probe; every selected model gets its own real max_tokens=1 call below.
	res := VerifyListingKeyWithModel(ctx, client, vendor, apiKey, "")
	base := VendorOpenAIBase(vendor)

	// Listing presence in /models is not callability: Bailian and Volcengine
	// can list a model that the account has not activated (Volcengine also
	// needs an inference endpoint). Probe each selected model individually so
	// a seller cannot list models that always fail for buyers.
	var passed []string
	for _, m := range listing.Models {
		mr := ModelVerifyResult{Model: m, UpstreamModel: upstreamByModel[m]}
		switch {
		case !res.KeyValid || len(res.Models) == 0:
			mr.FailReason = "Key 未能读取模型列表，未进行调用检测"
		case !keyExposesModel(res.Models, upstreamByModel[m]):
			mr.FailReason = "该模型不在这个 Key 的可用模型列表中"
		default:
			mr.OK, mr.PromptCacheSupport, mr.LatencyMs, mr.FailReason =
				probeModelCall(ctx, client, base, upstreamByModel[m], apiKey)
		}
		if mr.OK {
			passed = append(passed, m)
			if mr.PromptCacheSupport {
				res.PromptCacheSupport = true
			}
		}
		res.ModelResults = append(res.ModelResults, mr)
	}

	if res.KeyValid && len(res.Models) > 0 && len(passed) > 0 {
		// Only the callable models go live; the rest stay visible to the
		// seller in ModelResults with their individual reason.
		if err := setListingModelsForVerification(db, listingID, passed); err != nil {
			return res, errors.Join(err, revertVerificationToDraft(db, listingID))
		}
		listing.Models = passed
	}
	res.OK = res.KeyValid && len(res.Models) > 0 && len(passed) > 0
	if !res.OK && res.FailReason == "" {
		var failures []string
		for _, mr := range res.ModelResults {
			if !mr.OK && mr.FailReason != "" {
				failures = append(failures, mr.Model+"："+mr.FailReason)
			}
		}
		if len(failures) > 0 {
			res.FailReason = "选中的模型都无法调用：" + strings.Join(failures, "；")
		}
	}

	// Fingerprint comparison (fail-closed). Every model that survived the
	// callability probe must also match the platform's official endpoint: a
	// model is certified only when BOTH checks pass. A comparison that cannot
	// run — no official platform key for the origin vendor, an official
	// transport/status failure, missing usage, a prompt too short to judge —
	// is a verification failure, not an implicit pass.
	//
	// MP_PLATFORM_KEYS unset means the deployment holds no platform credential
	// at all: fingerprinting is disabled as a whole and the attempt is
	// recorded as not configured. Once any platform key exists, every listed
	// model is required to compare successfully (a missing key for one origin
	// vendor fails that model).
	fingerprintConfigured := PlatformFingerprintConfigured()
	var fingerprintFailures []string
	if res.OK {
		for _, m := range listing.Models {
			fr := FingerprintModelResult{Model: m, UpstreamModel: upstreamByModel[m]}
			if fingerprintConfigured {
				model, found := ModelPoolByName(db, m)
				if !found {
					fr.FailReason = "model is not in the platform pool; no official reference"
				} else if officialKey, hasKey := PlatformKeyForVendor(model.OriginVendor); !hasKey {
					fr.FailReason = "no platform key for origin vendor " + model.OriginVendor
				} else if officialVendor, foundVendor := lookupVendorForDB(db, model.OriginVendor); !foundVendor {
					fr.FailReason = "origin vendor " + model.OriginVendor + " is not whitelisted"
				} else {
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
					fr.Checked = fp.Checked
					fr.Matched = fp.Passed()
					fr.Ratio = fp.Ratio
					fr.FailReason = fp.FailReason
				}
			} else {
				fr.FailReason = "platform fingerprint keys are not configured"
			}
			if fingerprintConfigured && (!fr.Checked || !fr.Matched) {
				reason := fr.FailReason
				if reason == "" {
					reason = "fingerprint mismatch"
				}
				fingerprintFailures = append(fingerprintFailures, m+"："+reason)
			}
			res.Fingerprints = append(res.Fingerprints, fr)
		}
	}
	if len(fingerprintFailures) > 0 {
		res.OK = false
		res.FailReason = "fingerprint verification failed: " + strings.Join(fingerprintFailures, "；")
	}

	// Record the probe (§4.2): the JSON detail summarizes the check and
	// never contains the key (§2.3).
	detailMap := map[string]any{
		"key_valid":            res.KeyValid,
		"models":               len(res.Models),
		"has_balance":          res.BalanceMicro != nil,
		"prompt_cache":         res.PromptCacheSupport,
		"fail_reason":          res.FailReason,
		"model_results":        res.ModelResults,
		"listed_models":        listing.Models,
		"fingerprint_required": fingerprintConfigured,
	}
	if len(res.Fingerprints) > 0 {
		fingerprintChecked := true
		fingerprintOK := true
		for _, fr := range res.Fingerprints {
			fingerprintChecked = fingerprintChecked && fr.Checked
			fingerprintOK = fingerprintOK && fr.Matched
		}
		detailMap["fingerprints"] = res.Fingerprints
		detailMap["fingerprint_checked"] = fingerprintChecked
		if fingerprintChecked {
			detailMap["fingerprint_ok"] = fingerprintOK
		}
		if len(fingerprintFailures) > 0 {
			detailMap["fingerprint_reason"] = strings.Join(fingerprintFailures, "；")
		}
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
// upstream id. Only a snapshot date suffix is accepted: "glm-5" must not match
// "glm-5.3" (a different model), nor "glm-5-flash"/"glm-5@beta" (different
// variants). Vendors append -YYYYMMDD (e.g. -20251001), occasionally -YYMMDD,
// when reintroducing a dated snapshot.
func keyExposesModel(available []string, upstream string) bool {
	upstream = strings.ToLower(strings.TrimSpace(upstream))
	if upstream == "" {
		return false
	}
	for _, name := range available {
		got := strings.ToLower(strings.TrimSpace(name))
		if got == upstream {
			return true
		}
		suffix, found := strings.CutPrefix(got, upstream+"-")
		if found && isDateSuffix(suffix) {
			return true
		}
	}
	return false
}

// isDateSuffix reports whether s is a bare 6- or 8-digit date snapshot suffix.
func isDateSuffix(s string) bool {
	if len(s) != 6 && len(s) != 8 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// setListingModelsForVerification narrows a verifying listing to the models
// that passed their real probe. The status guard keeps a concurrent transition
// from being overwritten.
func setListingModelsForVerification(db *gorm.DB, id int64, models []string) error {
	// A raw column update bypasses the serializer:json tag, and binding the
	// []string itself makes GORM expand a multi-element slice into an invalid
	// row value. Encode the JSON array here and bind the string.
	encoded, err := common.Marshal(models)
	if err != nil {
		return err
	}
	res := db.Model(&Listing{}).
		Where("id = ? AND status = ?", id, ListingVerifying).
		Update("models", string(encoded))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("%w: verifying -> verifying (models)", ErrInvalidTransition)
	}
	return nil
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
