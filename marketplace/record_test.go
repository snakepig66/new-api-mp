package marketplace

import (
	"errors"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/QuantumNous/new-api/model"
)

// settleFixture runs the BeginDistribute → PrepareBilling prefix of a request
// so SettleRelay/FailRelay can be exercised against a real session.
func settleFixture(t *testing.T, buyerQuota int, multiplierMilli int64) (*relayFixture, *gin.Context, *relaycommon.RelayInfo, Listing) {
	t.Helper()
	f := newRelayFixture(t, buyerQuota)
	l := f.addListing(t, multiplierMilli)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","max_tokens":100}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	maxTokens := uint(100)
	info := newChatInfo("deepseek-flash", &maxTokens)
	require.Nil(t, PrepareBilling(c, info, stubEstimator(1000)))
	return f, c, info, l
}

func countRequests(t *testing.T, db *gorm.DB, requestID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&Request{}).Where("request_id = ?", requestID).Count(&n).Error)
	return n
}

func ledgerRows(t *testing.T, db *gorm.DB, requestID string) []LedgerEntry {
	t.Helper()
	var rows []LedgerEntry
	require.NoError(t, db.Where("request_id = ?", requestID).Find(&rows).Error)
	return rows
}

// Successful settle: Quote prices the actual usage at the freeze instant, the
// buyer is charged exactly that, mp_requests and mp_ledger balance out (§3.7-
// §3.9).
func TestSettleRelaySuccess(t *testing.T) {
	f, c, info, l := settleFixture(t, 10_000_000, 500)
	s := CurrentRelaySession(c)

	usage := &dto.Usage{
		PromptTokens:     900,
		CompletionTokens: 80,
	}
	usage.PromptTokensDetails.CachedTokens = 100

	micro := SettleRelay(c, info, usage)
	require.Greater(t, micro, 0)

	// Expected amount from the single Quote function at the freeze instant:
	// input = 900-100, output = 80 (reasoning folded), cache_read = 100.
	expected, err := Quote(f.db, f.cal, QuoteInput{
		Model: "deepseek-flash", Vendor: l.Vendor, MultiplierMilli: 500, At: s.FreezeAt(),
		InputTokens: 800, OutputTokens: 80, CacheReadTokens: 100,
	})
	require.NoError(t, err)
	assert.Equal(t, int(expected.TotalDebit), micro)

	// The upstream seam settles the same amount into the session.
	require.NoError(t, info.Billing.Settle(micro))
	assert.Equal(t, 10_000_000-micro, buyerQuota(t, int64(f.buyerID)))

	// One mp_requests row, success, with the full breakdown (§3.8).
	require.Equal(t, int64(1), countRequests(t, f.db, "req_relayhook_test"))
	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, RequestSuccess, rec.Status)
	assert.Equal(t, int64(800), rec.InputTokens)
	assert.Equal(t, int64(80), rec.OutputTokens)
	assert.Equal(t, int64(100), rec.CacheReadTokens)
	assert.Equal(t, int64(micro), rec.TotalDebitMicro)
	assert.Equal(t, int64(expected.OfficialCost), rec.OfficialCostMicro)
	assert.Equal(t, l.ID, rec.ListingID)
	assert.Equal(t, int64(f.buyerID), rec.BuyerUserID)
	assert.Equal(t, f.sellerID, rec.SellerID)
	assert.Equal(t, rec.PlatformFeeMicro+rec.SellerCreditMicro, rec.TotalDebitMicro)
	assert.False(t, rec.UsageEstimated)
	assert.Contains(t, rec.RetryChain, HopEventSelect)

	// Ledger: buyer_debit + tool_debit = seller_credit + platform_fee, signed
	// sum zero (§3.9); seller credit pending with a T+7 available_at.
	rows := ledgerRows(t, f.db, "req_relayhook_test")
	require.Len(t, rows, 3) // tool debit 0 → no tool_debit row
	var sum int64
	var credit *LedgerEntry
	for i := range rows {
		sum += rows[i].AmountMicro
		if rows[i].EntryType == EntrySellerCredit {
			credit = &rows[i]
		}
	}
	assert.Equal(t, int64(0), sum)
	require.NotNil(t, credit)
	assert.Equal(t, LedgerPending, credit.Status)
	require.NotNil(t, credit.AvailableAt)
	assert.True(t, credit.AvailableAt.After(time.Now().Add(6*24*time.Hour)))

	// Concurrency slot released at request end (§3.5).
	assert.Equal(t, 0, f.selector.Slots.InFlight(l.ID))

	// Idempotent: a second settle replays the same amount, no new rows.
	again := SettleRelay(c, info, usage)
	assert.Equal(t, micro, again)
	assert.Equal(t, int64(1), countRequests(t, f.db, "req_relayhook_test"))
	assert.Len(t, ledgerRows(t, f.db, "req_relayhook_test"), 3)
}

// H3: when the buyer's balance can no longer cover the settle top-up, the
// ledger books only what was actually collected and the difference is recorded
// in the retry chain — never a seller credit for money the platform never got.
func TestSettleRelayShortfallBooksCollected(t *testing.T) {
	f, c, info, _ := settleFixture(t, 10_000_000, 500)
	// Simulate the balance being spent elsewhere after the pre-freeze.
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.buyerID).Update("quota", 0).Error)

	// Output far above the pre-freeze's max_tokens=100, so the actual quote
	// exceeds the pre-consumed amount and the top-up cannot be collected.
	usage := &dto.Usage{PromptTokens: 900, CompletionTokens: 5000}
	micro := SettleRelay(c, info, usage)
	require.Greater(t, micro, 0, "the pre-freeze was still collectable")

	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, RequestSuccess, rec.Status)
	assert.Equal(t, int64(micro), rec.TotalDebitMicro, "the record books the collected amount")
	assert.Equal(t, rec.TotalDebitMicro, rec.TokenDebitMicro+rec.ToolDebitMicro)
	assert.Contains(t, rec.RetryChain, HopEventSettleShortfall)

	rows := ledgerRows(t, f.db, "req_relayhook_test")
	require.NotEmpty(t, rows)
	var sum int64
	for _, r := range rows {
		sum += r.AmountMicro
	}
	assert.Zero(t, sum, "ledger signed sum stays zero")
	assert.Equal(t, 0, buyerQuota(t, int64(f.buyerID)), "the balance is drained, never negative")
}

// §3.7: 上游正常结束但 0 输出 token → 判失败不扣费 (covers chat / responses /
// messages — all three funnel through PostTextConsumeQuota → SettleRelay).
func TestSettleRelayZeroOutputFailsFree(t *testing.T) {
	f, c, info, l := settleFixture(t, 10_000_000, 500)

	usage := &dto.Usage{PromptTokens: 900, CompletionTokens: 0}
	micro := SettleRelay(c, info, usage)
	assert.Equal(t, 0, micro)

	// The seam settles 0 → the whole pre-freeze refunds (失败不扣费).
	require.NoError(t, info.Billing.Settle(0))
	assert.Equal(t, 10_000_000, buyerQuota(t, int64(f.buyerID)))

	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, RequestFailed, rec.Status)
	assert.Equal(t, int64(0), rec.TotalDebitMicro)
	assert.Contains(t, rec.RetryChain, HopEventZeroOutput)
	assert.Empty(t, ledgerRows(t, f.db, "req_relayhook_test"))
	assert.Equal(t, 0, f.selector.Slots.InFlight(l.ID))
}

// §3.7: 上游没返回 usage（截断的流/空响应）→ 估算标记 + 判失败不扣费.
func TestSettleRelayNilUsageFailsFree(t *testing.T) {
	f, c, info, _ := settleFixture(t, 10_000_000, 500)

	micro := SettleRelay(c, info, nil)
	assert.Equal(t, 0, micro)
	require.NoError(t, info.Billing.Settle(0))
	assert.Equal(t, 10_000_000, buyerQuota(t, int64(f.buyerID)))

	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, RequestFailed, rec.Status)
	assert.True(t, rec.UsageEstimated)
	assert.Equal(t, int64(1000), rec.InputTokens) // 预冻结的输入估算
	assert.Empty(t, ledgerRows(t, f.db, "req_relayhook_test"))
}

// §10-1 暂定: 流式中途断开（客户端取消）不收费 — the upstream stream handler
// returns a usage and a nil error on client disconnect, so the protection
// lives in SettleRelay via the stream end reason.
func TestSettleRelayClientGoneStreamFree(t *testing.T) {
	f, c, info, _ := settleFixture(t, 10_000_000, 500)

	info.IsStream = true
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, errors.New("context canceled"))

	usage := &dto.Usage{PromptTokens: 900, CompletionTokens: 80}
	micro := SettleRelay(c, info, usage)
	assert.Equal(t, 0, micro)
	require.NoError(t, info.Billing.Settle(0))
	assert.Equal(t, 10_000_000, buyerQuota(t, int64(f.buyerID)))

	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, RequestFailed, rec.Status)
	assert.Contains(t, rec.RetryChain, HopEventStreamCut)
	assert.Empty(t, ledgerRows(t, f.db, "req_relayhook_test"))
}

// A normally ended stream (done) with output tokens is charged as usual.
func TestSettleRelayDoneStreamCharged(t *testing.T) {
	_, c, info, _ := settleFixture(t, 10_000_000, 500)

	info.IsStream = true
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)

	usage := &dto.Usage{PromptTokens: 900, CompletionTokens: 80}
	micro := SettleRelay(c, info, usage)
	assert.Greater(t, micro, 0)
}

// §3.7 全部失败 ¥0: FailRelay writes the single failed record and releases
// the slot; the BillingSession refund (invoked right after by the seam)
// restores the buyer.
func TestFailRelayWritesFailedRecord(t *testing.T) {
	f, c, info, l := settleFixture(t, 10_000_000, 500)
	s := CurrentRelaySession(c)

	info.LastError = apiErrorWithStatus(503)
	FailRelay(c, info)
	info.Billing.Refund(c)

	assert.Equal(t, 10_000_000, buyerQuota(t, int64(f.buyerID)))
	require.Equal(t, int64(1), countRequests(t, f.db, "req_relayhook_test"))
	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, RequestFailed, rec.Status)
	assert.Equal(t, int64(0), rec.TotalDebitMicro)
	assert.Contains(t, rec.RetryChain, HopEventError)
	assert.Equal(t, 0, f.selector.Slots.InFlight(l.ID))
	assert.Empty(t, ledgerRows(t, f.db, "req_relayhook_test"))

	// A second FailRelay does not double-write.
	FailRelay(c, info)
	assert.Equal(t, int64(1), countRequests(t, f.db, "req_relayhook_test"))
	_ = s
}

// A settle after FailRelay keeps the failed record (first writer wins) and
// charges nothing.
func TestFailRelayThenSettleKeepsFailed(t *testing.T) {
	f, c, info, _ := settleFixture(t, 10_000_000, 500)

	FailRelay(c, info)
	micro := SettleRelay(c, info, &dto.Usage{PromptTokens: 10, CompletionTokens: 5})
	assert.Equal(t, 0, micro)
	assert.Equal(t, int64(1), countRequests(t, f.db, "req_relayhook_test"))
	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, RequestFailed, rec.Status)
}

// Usage extraction: cache read/write split out of plain input; reasoning
// folds into output only when completion is empty (§3.2).
func TestExtractMarketplaceUsage(t *testing.T) {
	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 50}
	usage.PromptTokensDetails.CachedTokens = 300
	usage.PromptTokensDetails.CachedCreationTokens = 100
	u := extractMarketplaceUsage(nil, usage)
	assert.Equal(t, int64(600), u.inputTokens)
	assert.Equal(t, int64(50), u.outputTokens)
	assert.Equal(t, int64(300), u.cacheRead)
	assert.Equal(t, int64(100), u.cacheWrite)
	assert.False(t, u.estimated)

	// DeepSeek native cache-hit field fallback.
	usage2 := &dto.Usage{PromptTokens: 1000, CompletionTokens: 50, PromptCacheHitTokens: 400}
	u2 := extractMarketplaceUsage(nil, usage2)
	assert.Equal(t, int64(600), u2.inputTokens)
	assert.Equal(t, int64(400), u2.cacheRead)

	// Reasoning-only completion (§3.2 推理 tokens 并入输出价).
	usage3 := &dto.Usage{PromptTokens: 10, CompletionTokens: 0}
	usage3.CompletionTokenDetails.ReasoningTokens = 42
	u3 := extractMarketplaceUsage(nil, usage3)
	assert.Equal(t, int64(42), u3.outputTokens)

	// Overlapping cache counts never drive input negative.
	usage4 := &dto.Usage{PromptTokens: 100, CompletionTokens: 1}
	usage4.PromptTokensDetails.CachedTokens = 150
	u4 := extractMarketplaceUsage(nil, usage4)
	assert.Equal(t, int64(0), u4.inputTokens)
}
