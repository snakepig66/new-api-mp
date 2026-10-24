package marketplace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/QuantumNous/new-api/tokenkit"
)

// Small texts are counted exactly, not sampled.
func TestEstimateTokensSampledExact(t *testing.T) {
	text := strings.Repeat("hello world ", 100)
	assert.Equal(t, tokenkit.Count("deepseek-flash", text), EstimateTokensSampled("deepseek-flash", text))
	assert.Equal(t, 0, EstimateTokensSampled("deepseek-flash", ""))
}

// Large texts are sampled from at least three segments and extrapolated; the
// estimate must stay deterministic and in a sane band around the exact count
// (§3.7: 采样至少 3 段，禁止全量同步分词).
func TestEstimateTokensSampledLarge(t *testing.T) {
	// > 3 * 16KB of repetitive prose.
	text := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 3000)
	exact := tokenkit.Count("deepseek-flash", text)
	est := EstimateTokensSampled("deepseek-flash", text)
	assert.Greater(t, est, 0)
	assert.Equal(t, est, EstimateTokensSampled("deepseek-flash", text))
	// Repetitive text: the sampled estimate should be within 20% of exact.
	lo, hi := int(float64(exact)*0.8), int(float64(exact)*1.2)+1
	assert.GreaterOrEqual(t, est, lo)
	assert.LessOrEqual(t, est, hi)
}

// Mixed-content text (code-ish head, prose tail) still estimates sanely.
func TestEstimateTokensSampledMixed(t *testing.T) {
	var b strings.Builder
	b.WriteString(strings.Repeat("func f() { return x+y; } ", 2000))
	b.WriteString(strings.Repeat("lorem ipsum dolor sit amet ", 2000))
	est := EstimateTokensSampled("deepseek-flash", b.String())
	assert.Greater(t, est, 0)
}

func TestModelMaxOutputTokens(t *testing.T) {
	assert.Equal(t, int64(64<<10), ModelMaxOutputTokens("deepseek-flash"))
	assert.Equal(t, int64(64<<10), ModelMaxOutputTokens("deepseek-v4-pro"))
	assert.Equal(t, int64(64<<10), ModelMaxOutputTokens("some-future-model"))
}
