package marketplace

// fingerprint.go — 渠道/模型认证探针与 tokenizer 指纹 (MODEL-CHANNEL-POOL §4).
//
// A channel enters the pool only after the platform key has listed its
// /models (VerifyChannelKey). A model qualification enters the pool after a
// tokenizer fingerprint comparison: the same verify prompt is sent to the
// official channel (platform key) and to the candidate channel (seller or
// platform key), and usage.prompt_tokens is compared. A ratio within 2% of
// 1.0 means both upstreams tokenize the text the same way, i.e. the candidate
// really serves the same model.
//
// Keys used here never leave memory: PlatformKeyForVendor parses the
// MP_PLATFORM_KEYS environment variable on demand, stores nothing, logs
// nothing, and no FailReason ever embeds a key or an upstream response body
// (§2.3/§3.11).

import (
	"context"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// PlatformKeysEnv is the environment variable carrying the platform-owned
// vendor keys: {"deepseek":"sk-...","zhipu":"sk-..."}. It is read on every
// call so tests (and a secret manager that rewrites the process environment)
// see the current value; nothing is persisted.
const PlatformKeysEnv = "MP_PLATFORM_KEYS"

// fingerprintTolerance is the accepted relative difference between the
// candidate and official prompt-token counts (误差 ≤ 2%).
const fingerprintTolerance = 0.02

// defaultFingerprintPrompt is used when a model pool row has no verify prompt.
// It mixes Chinese, Latin, digits and punctuation so tokenizer differences
// between the official and candidate upstreams show up in prompt_tokens.
const defaultFingerprintPrompt = "请用一句话解释下面这段文本的含义，并保持原格式：The quick brown fox jumps over the lazy dog. " +
	"模型指纹检测需要覆盖中英文、数字 1234567890、标点符号与换行。\n第二行：稳定性、吞吐量、延迟。"

// fingerprintHTTPClient, when non-nil, overrides the client passed to
// CompareFingerprint. It exists for tests that need to redirect egress; nil
// means "use the caller's client".
var fingerprintHTTPClient *http.Client

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

// FingerprintResult is one tokenizer comparison.
type FingerprintResult struct {
	PromptTokensOfficial  int64
	PromptTokensCandidate int64
	Ratio                 float64 // candidate/official, 1.0 = identical
	WithinTolerance       bool    // |ratio-1| <= 0.02
	FailReason            string  // short English; never contains a key or upstream body
}

// CompareFingerprint sends the same prompt to the official and candidate
// OpenAI-compatible /chat/completions endpoints (each with its own upstream
// model name) and compares usage.prompt_tokens. It always spends two minimal
// completions (max_tokens=1, temperature=0); failures are reported in
// FailReason and never echo a key or the upstream body.
func CompareFingerprint(ctx context.Context, client *http.Client,
	official Vendor, officialKey, officialUpstreamModel string,
	candidate Vendor, candidateKey, candidateUpstreamModel string,
	prompt string) FingerprintResult {
	if fingerprintHTTPClient != nil {
		client = fingerprintHTTPClient
	}
	if client == nil {
		client = http.DefaultClient
	}

	var res FingerprintResult
	officialTokens, fail := fingerprintPromptTokens(ctx, client, official, officialKey, officialUpstreamModel, prompt)
	if fail != "" {
		res.FailReason = fingerprintFailReason("official", fail)
		return res
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
	ratio := float64(candidateTokens) / float64(officialTokens)
	res.Ratio = math.Round(ratio*10000) / 10000
	res.WithinTolerance = math.Abs(ratio-1) <= fingerprintTolerance
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
		client = http.DefaultClient
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
