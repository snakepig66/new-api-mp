package marketplace

// fingerprint.go — 渠道/模型认证探针与 tokenizer 指纹 (MODEL-CHANNEL-POOL §4).
//
// A channel enters the pool only after the platform key has listed its
// /models (VerifyChannelKey). A model qualification enters the pool after a
// tokenizer fingerprint comparison: the same verify prompt is sent to the
// official channel (platform key) and to the candidate channel (seller or
// platform key), and usage.prompt_tokens is compared. A ratio within 5% of
// 1.0 (or an absolute difference of at most 3 tokens) means both upstreams
// tokenize the text the same way, i.e. the candidate really serves the same
// model.
//
// The comparison is fail-closed: a result that is not Checked (transport
// error, non-200, missing usage, prompt too short) is NOT a pass. Callers
// must use Passed(), which requires a completed comparison inside the
// tolerance; a missing official reference therefore blocks certification.
//
// Keys used here never leave memory: PlatformKeyForVendor parses the
// MP_PLATFORM_KEYS environment variable on demand, stores nothing, logs
// nothing, and no FailReason ever embeds a key or an upstream response body
// (§2.3/§3.11).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// PlatformKeysEnv is the environment variable carrying the platform-owned
// vendor keys: {"deepseek":"sk-...","zhipu":"sk-..."}. It is read on every
// call so tests (and a secret manager that rewrites the process environment)
// see the current value; nothing is persisted.
const PlatformKeysEnv = "MP_PLATFORM_KEYS"

// fingerprintTolerance is the accepted relative difference between the
// candidate and official prompt-token counts (误差 ≤ 5%).
const fingerprintTolerance = 0.05

// fingerprintAbsoluteTolerance is the accepted absolute difference in
// prompt tokens. Small prompts tokenize to only a handful of tokens, where a
// relative bound alone would be too strict: |candidate-official| <= 3 passes
// even when the ratio is outside fingerprintTolerance.
const fingerprintAbsoluteTolerance = 3

// fingerprintMinPromptTokens is the smallest official prompt-token count that
// can be judged. Below it the sample is too small for either tolerance to be
// meaningful, so the comparison is reported as incomplete (Checked=false) and
// fails closed: it can never certify a model.
const fingerprintMinPromptTokens = 20

// defaultFingerprintPrompt is used when a model pool row has no verify prompt.
// It mixes Chinese, Latin, digits and punctuation so tokenizer differences
// between the official and candidate upstreams show up in prompt_tokens.
const defaultFingerprintPrompt = "请用一句话解释下面这段文本的含义，并保持原格式：The quick brown fox jumps over the lazy dog. " +
	"模型指纹检测需要覆盖中英文、数字 1234567890、标点符号与换行。\n第二行：稳定性、吞吐量、延迟。"

// fingerprintHTTPClient, when non-nil, overrides the client passed to
// CompareFingerprint. It exists for tests that need to redirect egress; nil
// means "use the caller's client".
var fingerprintHTTPClient *http.Client

const (
	// fingerprintCacheTTL is how long an official prompt_tokens result is
	// reused. The patrol job runs every 30 minutes, so one official call per
	// model/prompt per patrol round is enough.
	fingerprintCacheTTL = 30 * time.Minute
	// fingerprintCacheMaxEntries bounds the process-local cache. Eviction
	// first drops expired rows, then the oldest row, so it never grows without
	// limit.
	fingerprintCacheMaxEntries = 512
)

// fingerprintCacheEntry holds only token counts and timestamps. Keys and
// credentials are deliberately never stored.
type fingerprintCacheEntry struct {
	promptTokens int64
	createdAt    time.Time
	expiresAt    time.Time
}

// fingerprintTokenCache is the process-local official-side cache. Candidate
// calls are never cached: only the official tokenizer answer is stable across
// patrol rounds.
var fingerprintTokenCache = struct {
	mu      sync.Mutex
	entries map[string]fingerprintCacheEntry
}{entries: make(map[string]fingerprintCacheEntry)}

// fingerprintCacheNow is a seam for tests that need to exercise TTL expiry
// without sleeping for 30 minutes.
var fingerprintCacheNow = time.Now

// fingerprintCacheKey identifies one official tokenization result by vendor,
// upstream model and a hash of the prompt. The prompt is hashed so the cache
// holds no potentially sensitive text.
func fingerprintCacheKey(vendorID, upstreamModel, prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return vendorID + "\x00" + upstreamModel + "\x00" + hex.EncodeToString(sum[:])
}

// fingerprintCacheGet returns a cached official prompt_tokens count. An
// expired row is removed on read.
func fingerprintCacheGet(key string, now time.Time) (int64, bool) {
	fingerprintTokenCache.mu.Lock()
	defer fingerprintTokenCache.mu.Unlock()
	entry, ok := fingerprintTokenCache.entries[key]
	if !ok {
		return 0, false
	}
	if !now.Before(entry.expiresAt) {
		delete(fingerprintTokenCache.entries, key)
		return 0, false
	}
	return entry.promptTokens, true
}

// fingerprintCachePut stores a successful official result, evicting expired
// rows first and then the oldest row while the cache is at its size cap.
func fingerprintCachePut(key string, promptTokens int64, now time.Time) {
	fingerprintTokenCache.mu.Lock()
	defer fingerprintTokenCache.mu.Unlock()
	entries := fingerprintTokenCache.entries
	if len(entries) >= fingerprintCacheMaxEntries {
		for k, entry := range entries {
			if !now.Before(entry.expiresAt) {
				delete(entries, k)
			}
		}
		for len(entries) >= fingerprintCacheMaxEntries {
			oldestKey := ""
			var oldest time.Time
			for k, entry := range entries {
				if oldestKey == "" || entry.createdAt.Before(oldest) {
					oldestKey, oldest = k, entry.createdAt
				}
			}
			delete(entries, oldestKey)
		}
	}
	entries[key] = fingerprintCacheEntry{
		promptTokens: promptTokens,
		createdAt:    now,
		expiresAt:    now.Add(fingerprintCacheTTL),
	}
}

// resetFingerprintCacheForTest clears the official-side cache. Tests call it
// to keep one case's cached token count from leaking into the next.
func resetFingerprintCacheForTest() {
	fingerprintTokenCache.mu.Lock()
	fingerprintTokenCache.entries = make(map[string]fingerprintCacheEntry)
	fingerprintTokenCache.mu.Unlock()
}

// PlatformKeyForVendor returns the platform key for vendor from
// MP_PLATFORM_KEYS. The map is parsed in memory and never logged or written to
// the database. An unset/empty/invalid variable, a missing vendor or an empty
// key all report ok=false.
func PlatformKeyForVendor(vendor string) (string, bool) {
	raw := strings.TrimSpace(os.Getenv(PlatformKeysEnv))
	if raw == "" {
		return "", false
	}
	var keys map[string]string
	if err := common.Unmarshal([]byte(raw), &keys); err != nil {
		return "", false
	}
	vendor = strings.TrimSpace(vendor)
	if vendor == "" {
		return "", false
	}
	if key, ok := keys[vendor]; ok {
		key = strings.TrimSpace(key)
		return key, key != ""
	}
	// Vendor ids are conventionally lower-case; accept an operator spelling
	// like "DeepSeek" without exposing the key through error text.
	for name, key := range keys {
		if strings.EqualFold(strings.TrimSpace(name), vendor) {
			key = strings.TrimSpace(key)
			return key, key != ""
		}
	}
	return "", false
}

// PlatformFingerprintConfigured reports whether the deployment declares
// platform vendor keys. A defined-but-empty/invalid MP_PLATFORM_KEYS counts as
// configured-but-unusable: verification then fails closed model by model
// instead of silently disabling the official reference. Only a completely
// unset variable disables fingerprinting for the deployment.
func PlatformFingerprintConfigured() bool {
	_, defined := os.LookupEnv(PlatformKeysEnv)
	return defined
}

// FingerprintResult is one tokenizer comparison.
type FingerprintResult struct {
	PromptTokensOfficial  int64
	PromptTokensCandidate int64
	Ratio                 float64 // candidate/official, 1.0 = identical
	// Checked reports whether both sides returned usable prompt_tokens and a
	// comparison actually happened. Transport errors, non-200 responses,
	// missing usage and prompts too small to judge all leave it false. A
	// false Checked is a verification FAILURE (fail-closed), never an
	// implicit pass; use Passed() instead of reading this field directly.
	Checked bool
	// Comparable is a synonym for Checked kept for callers that spell the
	// "was this result a real comparison?" question that way.
	Comparable      bool
	WithinTolerance bool   // |ratio-1| <= 0.05 or |candidate-official| <= 3
	FailReason      string // short English; never contains a key or upstream body
}

// Passed is the only correct reading of a comparison: the comparison must
// have completed AND landed inside the tokenizer tolerance. Everything else
// (error, missing usage, too-short prompt, mismatch) is a failure.
func (r FingerprintResult) Passed() bool {
	return r.Checked && r.WithinTolerance
}

// FingerprintModelResult is one key×model fingerprint outcome recorded inside
// a verify/patrol probe (mp_probes.detail, no schema change). Checked=false
// is a failure, not "unknown": a probe only certifies a model when the
// comparison completed and Matched is true.
type FingerprintModelResult struct {
	Model         string  `json:"model"`
	UpstreamModel string  `json:"upstream_model"`
	Checked       bool    `json:"checked"`
	Matched       bool    `json:"matched"`
	Ratio         float64 `json:"ratio,omitempty"`
	FailReason    string  `json:"fail_reason,omitempty"`
}

// CompareFingerprint sends the same prompt to the official and candidate
// OpenAI-compatible /chat/completions endpoints (each with its own upstream
// model name) and compares usage.prompt_tokens. The official result is cached
// in-process for fingerprintCacheTTL, keyed by vendor + upstream model +
// prompt hash, so the patrol job does not hit the official endpoint once per
// listing. The candidate is always called; cache hits and failures never carry
// keys or upstream bodies. It always spends at most one candidate completion
// (max_tokens=1, temperature=0); failures are reported in FailReason. Only a
// result whose Passed() is true may certify a model; an incomplete comparison
// is a failure.
func CompareFingerprint(ctx context.Context, client *http.Client,
	official Vendor, officialKey, officialUpstreamModel string,
	candidate Vendor, candidateKey, candidateUpstreamModel string,
	prompt string) FingerprintResult {
	var res FingerprintResult
	if fingerprintHTTPClient != nil {
		client = fingerprintHTTPClient
	}
	if client == nil {
		// Fail closed: the marketplace egress clients refuse redirects and
		// validate dial-time IPs, while http.DefaultClient does neither. An
		// injected client is therefore mandatory and its absence is a failed
		// comparison, never a fallback to the default transport.
		res.FailReason = "no http client injected for fingerprint comparison"
		return res
	}
	now := fingerprintCacheNow()
	cacheKey := fingerprintCacheKey(official.ID, officialUpstreamModel, prompt)
	officialTokens, cached := fingerprintCacheGet(cacheKey, now)
	if !cached {
		var fail string
		officialTokens, fail = fingerprintPromptTokens(ctx, client, official, officialKey, officialUpstreamModel, prompt)
		if fail != "" {
			// Failures are never cached: the next patrol must retry the
			// official endpoint.
			res.FailReason = fingerprintFailReason("official", fail)
			return res
		}
		if officialTokens > 0 {
			fingerprintCachePut(cacheKey, officialTokens, now)
		}
	}
	candidateTokens, fail := fingerprintPromptTokens(ctx, client, candidate, candidateKey, candidateUpstreamModel, prompt)
	if fail != "" {
		res.FailReason = fingerprintFailReason("candidate", fail)
		return res
	}

	res.PromptTokensOfficial = officialTokens
	res.PromptTokensCandidate = candidateTokens
	if officialTokens <= 0 || candidateTokens <= 0 {
		res.FailReason = "missing prompt token usage"
		return res
	}
	if officialTokens < fingerprintMinPromptTokens {
		// Too few tokens to judge: a single tokenizer quirk would look like a
		// huge relative drift. The comparison did not complete, so it fails
		// closed rather than counting as a pass.
		res.FailReason = "prompt too short to compare"
		return res
	}
	ratio := float64(candidateTokens) / float64(officialTokens)
	diff := officialTokens - candidateTokens
	if diff < 0 {
		diff = -diff
	}
	res.Ratio = math.Round(ratio*10000) / 10000
	res.Checked = true
	res.Comparable = true
	res.WithinTolerance = math.Abs(ratio-1) <= fingerprintTolerance+1e-9 ||
		diff <= fingerprintAbsoluteTolerance
	if !res.WithinTolerance {
		res.FailReason = "prompt token mismatch"
	}
	return res
}

// fingerprintFailReason prefixes a transport/status failure with the role that
// failed. A missing/zero usage keeps its exact documented wording.
func fingerprintFailReason(role, fail string) string {
	if fail == "missing prompt token usage" {
		return fail
	}
	return role + " " + fail
}

// fingerprintPromptTokens performs one minimal chat completion and returns
// usage.prompt_tokens. A non-empty failure is short English and carries no
// key or upstream text; a missing/zero usage is reported as
// "missing prompt token usage" so the caller's prefix stays readable.
func fingerprintPromptTokens(ctx context.Context, client *http.Client,
	vendor Vendor, apiKey, upstreamModel, prompt string) (int64, string) {
	body, err := common.Marshal(map[string]any{
		"model":       upstreamModel,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens":  1,
		"temperature": 0,
	})
	if err != nil {
		return 0, "request could not be built"
	}
	base := VendorOpenAIBase(vendor)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return 0, "request could not be built"
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		// Transport errors carry method+URL but never headers, so no key leaks.
		return 0, "request failed"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return 0, "request returned " + httpStatusMeaning(resp.StatusCode)
	}

	var cr struct {
		Usage *struct {
			PromptTokens int64 `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, maxVerifyBodyBytes), &cr); err != nil {
		return 0, "missing prompt token usage"
	}
	if cr.Usage == nil || cr.Usage.PromptTokens <= 0 {
		return 0, "missing prompt token usage"
	}
	return cr.Usage.PromptTokens, ""
}

// VerifyChannelKey lists the upstream models a key may access on the channel:
// GET {OpenAI base}/models, timed. It is the §4 channel-path probe (平台 key)
// and does not spend chat credit. The caller injects the HTTP client exactly
// as for VerifyListingKeyWithModel.
func VerifyChannelKey(ctx context.Context, client *http.Client, channel Vendor, apiKey string) VerifyResult {
	var res VerifyResult
	if client == nil {
		// Same fail-closed rule as CompareFingerprint: never fall back to
		// http.DefaultClient, which follows redirects and skips the egress
		// guard.
		res.FailReason = "no http client injected for channel verification"
		return res
	}
	base := VendorOpenAIBase(channel)
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
		res.FailReason = "vendor /models request failed: " + stripKey(err.Error(), apiKey)
		return res
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
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
		return res
	}
	for _, m := range modelsResp.Data {
		if m.ID != "" && !containsString(res.Models, m.ID) {
			res.Models = append(res.Models, m.ID)
		}
	}
	if len(res.Models) == 0 {
		res.FailReason = "vendor returned an empty model list"
	}
	res.OK = res.KeyValid && len(res.Models) > 0
	res.FailReason = stripKey(res.FailReason, apiKey)
	return res
}
