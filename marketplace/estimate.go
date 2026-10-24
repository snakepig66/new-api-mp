package marketplace

// estimate.go — 长文本 token 预估 (§3.7): bodies over 64KB are estimated by
// segmented sampling; a full synchronous tokenize before relaying is
// forbidden (禁止转发前同步全量分词).

import (
	"math"

	"github.com/QuantumNous/new-api/tokenkit"
)

const (
	// EstimateSampleThresholdBytes is the request-body size above which the
	// input estimate switches from the exact upstream estimator to segmented
	// sampling (§3.7: 超过 64KB 分段采样估算).
	EstimateSampleThresholdBytes = 64 << 10
	// estimateSampleSegments is the number of text segments tokenized per
	// sampled estimate (§3.7: 采样至少 3 段): head, middle and tail.
	estimateSampleSegments = 3
	// estimateSampleSegmentBytes is the size of each sampled segment.
	estimateSampleSegmentBytes = 16 << 10
)

// EstimateTokensSampled estimates the prompt token count of text by
// tokenizing three segments (head / middle / tail) and extrapolating by
// length, rounding up. Texts small enough to tokenize directly (at most one
// byte per segment per segment count) are counted exactly. The estimator is
// deliberately conservative: overestimating the pre-freeze is safe (the
// settle refunds the difference), underestimating is not.
func EstimateTokensSampled(model, text string) int {
	if len(text) <= estimateSampleSegments*estimateSampleSegmentBytes {
		return tokenkit.Count(model, text)
	}
	total := 0
	sampled := 0
	for i := range estimateSampleSegments {
		start := i * (len(text) - estimateSampleSegmentBytes) / (estimateSampleSegments - 1)
		seg := text[start : start+estimateSampleSegmentBytes]
		// A segment boundary may split a UTF-8 sequence; the tokenizer
		// tolerates the replacement rune and the estimate stays unbiased.
		total += tokenkit.Count(model, seg)
		sampled += len(seg)
	}
	if sampled == 0 || total == 0 {
		return 0
	}
	// est = total · len(text) / sampled, rounded up (integer math).
	est := (int64(total)*int64(len(text)) + int64(sampled) - 1) / int64(sampled)
	if est > int64(math.MaxInt) {
		return math.MaxInt
	}
	return int(est)
}

// modelMaxOutputTokens is the v1 hardcoded table of each model's maximum
// output tokens, used to estimate the pre-freeze when the request omits
// max_tokens (§3.7: 按模型最大输出估算). TODO: move to a configurable
// per-model setting once more models are onboarded.
var modelMaxOutputTokens = map[string]int64{
	"deepseek-flash":  64 << 10,
	"deepseek-v4-pro": 64 << 10,
}

// defaultModelMaxOutputTokens applies to marketplace models missing from the
// table; 64K is the vendors' common maximum output.
const defaultModelMaxOutputTokens = 64 << 10

// ModelMaxOutputTokens returns the model's maximum output tokens for
// pre-freeze estimation (§3.7).
func ModelMaxOutputTokens(model string) int64 {
	if v, ok := modelMaxOutputTokens[model]; ok {
		return v
	}
	return defaultModelMaxOutputTokens
}
