package marketplace

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/QuantumNous/new-api/model"
)

// settleFixture runs the BeginDistribute → PrepareBilling prefix of a request
// so SettleRelay/FailRelay can be exercised against a real session.
func settleFixture(t *testing.T, buyerQuota int, multiplierMilli int64) (*relayFixture, *gin.Context, *relaycommon.RelayInfo, Listing) {
	t.Helper()
	return settleFixtureWithGift(t, buyerQuota, 0, multiplierMilli)
}

// settleFixtureWithGift is settleFixture with an initial gift_quota.
func settleFixtureWithGift(t *testing.T, buyerQuota, buyerGift int, multiplierMilli int64) (*relayFixture, *gin.Context, *relaycommon.RelayInfo, Listing) {
	t.Helper()
	f := newRelayFixtureWithGift(t, buyerQuota, buyerGift)
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

// newResponsesInfo builds the relay info of a /v1/responses buyer request.
func newResponsesInfo(modelName string, maxTokens *uint) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: modelName,
		RelayFormat:     types.RelayFormatOpenAIResponses,
		Request:         &dto.OpenAIResponsesRequest{Model: modelName, MaxOutputTokens: maxTokens},
	}
}

// newMessagesInfo builds the relay info of a /v1/messages buyer request.
func newMessagesInfo(modelName string, maxTokens *uint) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: modelName,
		RelayFormat:     types.RelayFormatClaude,
		Request:         &dto.ClaudeRequest{Model: modelName, MaxTokens: maxTokens},
	}
}

// settleFixtureEndpoint runs the BeginDistribute → PrepareBilling prefix on
// path, so settlement can be exercised per protocol.
func settleFixtureEndpoint(t *testing.T, path string, info *relaycommon.RelayInfo, body []byte) (*relayFixture, *gin.Context, *relaycommon.RelayInfo, Listing) {
	t.Helper()
	f := newRelayFixture(t, 10_000_000)
	l := f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, body)
	c.Request.URL.Path = path
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	require.Nil(t, PrepareBilling(c, info, stubEstimator(1000)))
	return f, c, info, l
}

func buyerGiftQuotaIn(t *testing.T, db *gorm.DB, buyerID int64) int {
	t.Helper()
	var gift int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", buyerID).
		Select("gift_quota").Scan(&gift).Error)
	return gift
}

func countRequests(t *testing.T, db *gorm.DB, requestID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&Request{}).Where("request_id = ?", requestID).Count(&n).Error)
	return n
}

// addListingForVendor creates one live listing for an arbitrary whitelisted
// vendor, so a test can exercise a hosting channel (e.g. volcengine-ark) whose
// upstream name differs from the standard model id.
func addListingForVendor(t *testing.T, f *relayFixture, vendorID, model string, multiplierMilli int64) Listing {
	t.Helper()
	key := fmt.Sprintf("sk-relayhook-test-key-%d", addListingKeySeq.Add(1))
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        vendorID,
		APIKey:          key,
		Models:          []string{model},
		MultiplierMilli: multiplierMilli,
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).Update("status", ListingLive).Error)
	listing.Status = ListingLive
	return listing
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
	assert.True(t, credit.AvailableAt.After(time.Now().Add(DefaultMaturity-24*time.Hour)))

	// Concurrency slot released at request end (§3.5).
	assert.Equal(t, 0, f.selector.Slots.InFlight(l.ID))

	// Idempotent: a second settle replays the same amount, no new rows.
	again := SettleRelay(c, info, usage)
	assert.Equal(t, micro, again)
	assert.Equal(t, int64(1), countRequests(t, f.db, "req_relayhook_test"))
	assert.Len(t, ledgerRows(t, f.db, "req_relayhook_test"), 3)
}

// TestSettleRelayRecordsChannelIdentity covers the P3 shadow identity written
// onto mp_requests: channel_id mirrors the listing id, endpoint is the buyer
// protocol and model_multiplier_milli is the listing multiplier; key_id and
// converted stay zero/false in this stage.
func TestSettleRelayRecordsChannelIdentity(t *testing.T) {
	f, c, info, l := settleFixture(t, 10_000_000, 500)
	s := CurrentRelaySession(c)

	micro := SettleRelay(c, info, &dto.Usage{PromptTokens: 900, CompletionTokens: 80})
	require.Greater(t, micro, 0)

	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", s.RequestID()).Take(&rec).Error)
	assert.Equal(t, l.ID, rec.ChannelID)
	assert.Equal(t, int64(0), rec.KeyID)
	assert.Equal(t, string(relayconstant.EndpointOpenAIChat), rec.Endpoint)
	assert.Equal(t, int64(500), rec.ModelMultiplierMilli)
	assert.False(t, rec.Converted)
}

// The raw upstream model returned in the response body is persisted in
// mp_requests.upstream_model before the writer rewrites it to the standard id
// (§3.4).
func TestSettleRelayRecordsUpstreamModel(t *testing.T) {
	f, c, info, _ := settleFixture(t, 10_000_000, 500)

	_, err := c.Writer.Write([]byte(`{"model":"deepseek-flash-20251025","choices":[]}`))
	require.NoError(t, err)

	micro := SettleRelay(c, info, &dto.Usage{PromptTokens: 900, CompletionTokens: 80})
	require.Greater(t, micro, 0)

	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, "deepseek-flash", rec.Model, "the public model stays the standard id")
	assert.Equal(t, "deepseek-flash-20251025", rec.UpstreamModel)
	assert.False(t, rec.ModelFingerprintAnomaly)
}

// A response model that is neither the selected upstream name nor a
// date-suffixed variant is not rewritten and is flagged in mp_requests.
func TestSettleRelayRecordsModelFingerprintAnomaly(t *testing.T) {
	f, c, info, _ := settleFixture(t, 10_000_000, 500)

	_, err := c.Writer.Write([]byte(`{"model":"some-other-upstream-id","choices":[]}`))
	require.NoError(t, err)

	micro := SettleRelay(c, info, &dto.Usage{PromptTokens: 900, CompletionTokens: 80})
	require.Greater(t, micro, 0)

	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, "deepseek-flash", rec.Model)
	assert.Equal(t, "some-other-upstream-id", rec.UpstreamModel)
	assert.True(t, rec.ModelFingerprintAnomaly)
}

// A hosted channel settles through the standard model's original vendor price
// book and the channel coefficient, exactly like the pre-freeze (§3.5). Before
// the fix, Quote looked for a price book under the channel slug and failed with
// no price book for products such as 火山方舟.
func TestSettleRelayChannelOriginAndCoefficient(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)

	// Remove the hosted-channel price copy and set the channel coefficient to
	// 0.5; only deepseek's official book remains.
	require.NoError(t, f.db.Where("vendor = ? AND model = ?", "volcengine-ark", "deepseek-flash").
		Delete(&PriceBook{}).Error)
	require.NoError(t, f.db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", "volcengine-ark", "deepseek-flash").
		Updates(map[string]any{"coefficient_milli": 500, "verified": true, "fingerprint_ratio": 1.0}).Error)

	addListingForVendor(t, f, "volcengine-ark", "deepseek-flash", 1000)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","max_tokens":100}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	maxTokens := uint(100)
	info := newChatInfo("deepseek-flash", &maxTokens)
	require.Nil(t, PrepareBilling(c, info, stubEstimator(1000)))

	s := CurrentRelaySession(c)
	require.Equal(t, "volcengine-ark", s.CurrentListing().Vendor)

	// Actual usage equals the pre-freeze quantities, so the pre-freeze and the
	// settle must charge the same amount.
	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 100}
	micro := SettleRelay(c, info, usage)
	require.Greater(t, micro, 0, "a hosted channel with no own price book must settle via its origin vendor")

	expected, err := Quote(f.db, f.cal, QuoteInput{
		Model: "deepseek-flash", Vendor: "volcengine-ark",
		OriginVendor: SeedVendorDeepSeek, CoefficientMilli: 500,
		MultiplierMilli: 1000, At: s.FreezeAt(),
		InputTokens: 1000, OutputTokens: 100,
	})
	require.NoError(t, err)
	assert.Equal(t, int(expected.TotalDebit), micro)
	assert.Equal(t, expected.TotalDebit, s.preFreeze.TotalDebit,
		"pre-freeze and settlement use the same origin vendor + coefficient formula")
	assert.Equal(t, int64(500), expected.CoefficientMilli)

	// Coefficient 1000 prices the same usage at the official rate; 500 is half.
	full, err := Quote(f.db, f.cal, QuoteInput{
		Model: "deepseek-flash", Vendor: "volcengine-ark",
		OriginVendor: SeedVendorDeepSeek, CoefficientMilli: 1000,
		MultiplierMilli: 1000, At: s.FreezeAt(),
		InputTokens: 1000, OutputTokens: 100,
	})
	require.NoError(t, err)
	assert.Equal(t, (full.TotalDebit+1)/2, expected.TotalDebit, "coefficient 500 halves the official quote")

	// The settling charge is correctly booked.
	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, RequestSuccess, rec.Status)
	assert.Equal(t, int64(micro), rec.TotalDebitMicro)
	assert.Equal(t, int64(expected.OfficialCost), rec.OfficialCostMicro)
}

// The settlement is capped at the pre-freeze: usage above the frozen estimate
// is borne by the platform, never collected from the buyer. The record stores
// the capped charge in TotalDebitMicro and the absorbed amount in CappedMicro;
// the ledger still balances.
func TestSettleRelayCappedAtPreFreeze(t *testing.T) {
	f, c, info, _ := settleFixture(t, 10_000_000, 500)
	s := CurrentRelaySession(c)
	frozen := s.preFreeze.TotalDebit
	require.Greater(t, frozen, Micro(0))

	// Simulate the balance being spent elsewhere after the pre-freeze.
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.buyerID).Update("quota", 0).Error)

	// Output far above the pre-freeze's max_tokens=100.
	usage := &dto.Usage{PromptTokens: 900, CompletionTokens: 5000}
	micro := SettleRelay(c, info, usage)
	assert.Equal(t, int(frozen), micro, "the charge is capped at the frozen amount")

	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, RequestSuccess, rec.Status)
	assert.Equal(t, int64(frozen), rec.TotalDebitMicro)
	assert.Equal(t, rec.TotalDebitMicro, rec.TokenDebitMicro+rec.ToolDebitMicro)
	assert.Greater(t, rec.CappedMicro, int64(0), "the platform-borne excess is recorded")
	assert.Contains(t, rec.RetryChain, HopEventSettleCapped)

	rows := ledgerRows(t, f.db, "req_relayhook_test")
	require.NotEmpty(t, rows)
	var sum int64
	for _, r := range rows {
		sum += r.AmountMicro
	}
	assert.Zero(t, sum, "ledger signed sum stays zero")
	assert.Equal(t, 0, buyerQuota(t, int64(f.buyerID)), "the buyer is never charged beyond the freeze")
}

// An official channel may spend gifted balance; the recorded gift_debit_micro
// is the gifted part of the charge and both balances drop by it.
func TestSettleRelayGiftDebitOnOfficialChannel(t *testing.T) {
	f, c, info, _ := settleFixtureWithGift(t, 10_000_000, 10_000_000, 500)

	usage := &dto.Usage{PromptTokens: 900, CompletionTokens: 80}
	micro := SettleRelay(c, info, usage)
	require.Greater(t, micro, 0)

	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
	assert.Equal(t, RequestSuccess, rec.Status)
	assert.Equal(t, int64(micro), rec.GiftDebitMicro, "the whole charge fits in the gift balance")
	assert.Equal(t, int64(0), rec.CappedMicro)

	require.NoError(t, info.Billing.Settle(micro))
	assert.Equal(t, int64(10_000_000-micro), int64(buyerQuota(t, int64(f.buyerID))))
	assert.Equal(t, 10_000_000-micro, buyerGiftQuotaIn(t, f.db, int64(f.buyerID)))

	// A 冲正 returns the money to the original funding pools: paid and gift.
	require.NoError(t, Reverse(f.db, "req_relayhook_test", time.Now()))
	assert.Equal(t, 10_000_000, buyerQuota(t, int64(f.buyerID)), "the whole debit is refunded")
	assert.Equal(t, 10_000_000, buyerGiftQuotaIn(t, f.db, int64(f.buyerID)), "the gifted part returns to gift_quota")
}

// Gift-funded seller credit stays in the ledger for audit but is never
// withdrawable: the platform never collected real money for it.
func TestSettleRelayGiftFundedIncomeNotWithdrawable(t *testing.T) {
	f, c, info, l := settleFixtureWithGift(t, 10_000_000, 10_000_000, 500)

	micro := SettleRelay(c, info, &dto.Usage{PromptTokens: 900, CompletionTokens: 80})
	require.Greater(t, micro, 0)
	require.NoError(t, info.Billing.Settle(micro))

	_, err := PromoteAvailable(f.db, time.Now().Add(DefaultMaturity+time.Hour))
	require.NoError(t, err)

	bal, err := AvailableBalance(f.db, l.SellerID)
	require.NoError(t, err)
	assert.Zero(t, bal, "income funded by gift balance is not withdrawable")

	// The ledger still records the seller credit for the request.
	var credit int64
	require.NoError(t, f.db.Model(&LedgerEntry{}).
		Where("request_id = ? AND entry_type = ?", "req_relayhook_test", EntrySellerCredit).
		Select("amount_micro").Scan(&credit).Error)
	assert.Greater(t, credit, int64(0))
}

// A seller (plan) channel may only spend paid balance: PrepareBilling returns
// 402 even though the total wallet, mostly gift, would cover the freeze.
func TestPrepareBillingRejectsGiftOnSellerChannel(t *testing.T) {
	f := newRelayFixtureWithGift(t, 10_000_000, 10_000_000)
	require.NoError(t, f.db.Model(&ChannelModel{}).
		Where("channel_slug = ?", "volcengine-ark-coding-plan").
		Update("verified", true).Error)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:          f.sellerID,
		VendorID:          "volcengine-ark-coding-plan",
		APIKey:            uniqueTestKey("sk-seller-gift-plan"),
		Models:            []string{"deepseek-v4-pro"},
		MultiplierMilli:   1000,
		PlanTermsAccepted: true,
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).Update("status", ListingLive).Error)
	f.addKeyModel(t, 77, "deepseek-v4-pro", 0)

	c := f.newRelayContext(t, []byte(`{"model":"deepseek-v4-pro","max_tokens":100}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-v4-pro")
	require.NoError(t, err)
	require.True(t, handled)
	maxTokens := uint(100)
	info := newChatInfo("deepseek-v4-pro", &maxTokens)
	apiErr := PrepareBilling(c, info, stubEstimator(1000))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusPaymentRequired, apiErr.StatusCode)
	assert.Equal(t, 10_000_000, buyerQuota(t, int64(f.buyerID)), "a refused freeze moves nothing")
	assert.Equal(t, 10_000_000, buyerGiftQuotaIn(t, f.db, int64(f.buyerID)))
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

// Usage extraction per protocol: the semantic recorded on the usage decides
// how the totals are read (Anthropic excludes cache from input_tokens, the
// OpenAI protocols include it), so a converted response settles on its
// upstream protocol's accounting (§3.2). The endpoint is only the fallback.
func TestExtractMarketplaceUsage(t *testing.T) {
	// Chat Completions: cache read/write split out of the inclusive prompt.
	chat := &dto.Usage{PromptTokens: 1000, CompletionTokens: 50}
	chat.PromptTokensDetails.CachedTokens = 300
	chat.PromptTokensDetails.CachedCreationTokens = 100
	u := extractMarketplaceUsage(nil, chat, relayconstant.EndpointOpenAIChat)
	assert.Equal(t, int64(600), u.inputTokens)
	assert.Equal(t, int64(50), u.outputTokens)
	assert.Equal(t, int64(300), u.cacheRead)
	assert.Equal(t, int64(100), u.cacheWrite)
	assert.False(t, u.estimated)

	// DeepSeek native prompt_cache_hit_tokens fallback.
	deepseek := &dto.Usage{PromptTokens: 1000, CompletionTokens: 50, PromptCacheHitTokens: 400}
	u = extractMarketplaceUsage(nil, deepseek, relayconstant.EndpointOpenAIChat)
	assert.Equal(t, int64(600), u.inputTokens)
	assert.Equal(t, int64(400), u.cacheRead)

	// OpenAI Responses: input_tokens/output_tokens with the cache under
	// input_tokens_details, and no PromptTokens/CompletionTokens set.
	responses := &dto.Usage{InputTokens: 1000, OutputTokens: 50}
	responses.InputTokensDetails = &dto.InputTokenDetails{CachedTokens: 300, CachedCreationTokens: 100}
	u = extractMarketplaceUsage(nil, responses, relayconstant.EndpointOpenAIResponses)
	assert.Equal(t, int64(600), u.inputTokens)
	assert.Equal(t, int64(50), u.outputTokens)
	assert.Equal(t, int64(300), u.cacheRead)
	assert.Equal(t, int64(100), u.cacheWrite)

	// Anthropic Messages: PromptTokens already excludes cache_read and
	// cache_creation, while InputTokens is the inclusive total. Reading it with
	// OpenAI semantics would subtract twice and under-bill (200 instead of 600).
	anthropic := &dto.Usage{
		PromptTokens: 600, InputTokens: 1000, CompletionTokens: 50,
		UsageSemantic: dto.BillingUsageSemanticAnthropic,
	}
	anthropic.PromptTokensDetails.CachedTokens = 300
	anthropic.PromptTokensDetails.CachedCreationTokens = 100
	u = extractMarketplaceUsage(nil, anthropic, relayconstant.EndpointAnthropicMessages)
	assert.Equal(t, int64(600), u.inputTokens)
	assert.Equal(t, int64(50), u.outputTokens)
	assert.Equal(t, int64(300), u.cacheRead)
	assert.Equal(t, int64(100), u.cacheWrite)

	// Converted anthropic usage behind a chat endpoint keeps Anthropic
	// semantics: the buyer's protocol must not change the upstream accounting.
	u = extractMarketplaceUsage(nil, anthropic, relayconstant.EndpointOpenAIChat)
	assert.Equal(t, int64(600), u.inputTokens, "anthropic semantics win over the chat endpoint")

	// Converted openai usage behind a messages endpoint keeps OpenAI semantics:
	// the inclusive total is the one that gets the cache subtraction.
	converted := &dto.Usage{
		PromptTokens: 1000, CompletionTokens: 50,
		UsageSemantic: dto.BillingUsageSemanticOpenAI,
	}
	converted.PromptTokensDetails.CachedTokens = 300
	converted.PromptTokensDetails.CachedCreationTokens = 100
	u = extractMarketplaceUsage(nil, converted, relayconstant.EndpointAnthropicMessages)
	assert.Equal(t, int64(600), u.inputTokens, "openai semantics win over the messages endpoint")

	// An unwrapped snapshot with only the BillingUsage semantic marker.
	sidecar := &dto.Usage{
		PromptTokens: 600, InputTokens: 1000, CompletionTokens: 50,
		BillingUsage: &dto.BillingUsage{Semantic: dto.BillingUsageSemanticAnthropic},
	}
	u = extractMarketplaceUsage(nil, sidecar, relayconstant.EndpointOpenAIChat)
	assert.Equal(t, int64(600), u.inputTokens)

	// Reasoning-only completion bills at the output price (§3.2).
	reasoning := &dto.Usage{PromptTokens: 10}
	reasoning.CompletionTokenDetails.ReasoningTokens = 42
	u = extractMarketplaceUsage(nil, reasoning, relayconstant.EndpointOpenAIResponses)
	assert.Equal(t, int64(42), u.outputTokens)

	// Overlapping cache counts never drive input negative.
	overlap := &dto.Usage{PromptTokens: 100, CompletionTokens: 1}
	overlap.PromptTokensDetails.CachedTokens = 150
	u = extractMarketplaceUsage(nil, overlap, relayconstant.EndpointOpenAIChat)
	assert.Equal(t, int64(0), u.inputTokens)

	// A nil usage keeps the pre-freeze prompt estimate (§3.7).
	info := newChatInfo("deepseek-flash", nil)
	info.SetEstimatePromptTokens(1234)
	u = extractMarketplaceUsage(info, nil, relayconstant.EndpointOpenAIChat)
	assert.True(t, u.estimated)
	assert.Equal(t, int64(1234), u.inputTokens)
	assert.Zero(t, u.outputTokens)
}

// Every v1 endpoint settles through the same Quote at the freeze instant, with
// the protocol's own usage fields read into the same price dimensions (§3.2).
func TestSettleRelayPerEndpointUsage(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		body       string
		info       func(*uint) *relaycommon.RelayInfo
		kind       relayconstant.EndpointKind
		usage      func() *dto.Usage
		wantInput  int64
		wantOutput int64
		wantCacheR int64
		wantCacheW int64
	}{
		{
			name: "openai chat native",
			path: "/v1/chat/completions", body: `{"model":"deepseek-flash","max_tokens":100}`,
			info: func(n *uint) *relaycommon.RelayInfo { return newChatInfo("deepseek-flash", n) },
			kind: relayconstant.EndpointOpenAIChat,
			usage: func() *dto.Usage {
				u := &dto.Usage{PromptTokens: 900, CompletionTokens: 80}
				u.PromptTokensDetails.CachedTokens = 100
				return u
			},
			wantInput: 800, wantOutput: 80, wantCacheR: 100,
		},
		{
			name: "openai responses native",
			path: "/v1/responses", body: `{"model":"deepseek-flash","max_output_tokens":100}`,
			info: func(n *uint) *relaycommon.RelayInfo { return newResponsesInfo("deepseek-flash", n) },
			kind: relayconstant.EndpointOpenAIResponses,
			usage: func() *dto.Usage {
				u := &dto.Usage{InputTokens: 900, OutputTokens: 80}
				u.InputTokensDetails = &dto.InputTokenDetails{CachedTokens: 100}
				return u
			},
			wantInput: 800, wantOutput: 80, wantCacheR: 100,
		},
		{
			name: "anthropic messages native",
			path: "/v1/messages", body: `{"model":"deepseek-flash","max_tokens":100,"messages":[]}`,
			info: func(n *uint) *relaycommon.RelayInfo { return newMessagesInfo("deepseek-flash", n) },
			kind: relayconstant.EndpointAnthropicMessages,
			usage: func() *dto.Usage {
				u := &dto.Usage{
					PromptTokens: 800, InputTokens: 950, CompletionTokens: 80,
					UsageSemantic: dto.BillingUsageSemanticAnthropic,
				}
				u.PromptTokensDetails.CachedTokens = 100
				u.PromptTokensDetails.CachedCreationTokens = 50
				return u
			},
			wantInput: 800, wantOutput: 80, wantCacheR: 100, wantCacheW: 50,
		},
		{
			name: "anthropic usage converted to a chat endpoint",
			path: "/v1/chat/completions", body: `{"model":"deepseek-flash","max_tokens":100}`,
			info: func(n *uint) *relaycommon.RelayInfo { return newChatInfo("deepseek-flash", n) },
			kind: relayconstant.EndpointOpenAIChat,
			usage: func() *dto.Usage {
				u := &dto.Usage{
					PromptTokens: 800, InputTokens: 950, CompletionTokens: 80,
					UsageSemantic: dto.BillingUsageSemanticAnthropic,
				}
				u.PromptTokensDetails.CachedTokens = 100
				u.PromptTokensDetails.CachedCreationTokens = 50
				return u
			},
			wantInput: 800, wantOutput: 80, wantCacheR: 100, wantCacheW: 50,
		},
		{
			name: "openai usage converted to a messages endpoint",
			path: "/v1/messages", body: `{"model":"deepseek-flash","max_tokens":100,"messages":[]}`,
			info: func(n *uint) *relaycommon.RelayInfo { return newMessagesInfo("deepseek-flash", n) },
			kind: relayconstant.EndpointAnthropicMessages,
			usage: func() *dto.Usage {
				u := &dto.Usage{
					PromptTokens: 900, CompletionTokens: 80,
					UsageSemantic: dto.BillingUsageSemanticOpenAI,
				}
				u.PromptTokensDetails.CachedTokens = 100
				return u
			},
			wantInput: 800, wantOutput: 80, wantCacheR: 100,
		},
		{
			name: "deepseek prompt_cache_hit_tokens",
			path: "/v1/chat/completions", body: `{"model":"deepseek-flash","max_tokens":100}`,
			info: func(n *uint) *relaycommon.RelayInfo { return newChatInfo("deepseek-flash", n) },
			kind: relayconstant.EndpointOpenAIChat,
			usage: func() *dto.Usage {
				return &dto.Usage{PromptTokens: 900, CompletionTokens: 80, PromptCacheHitTokens: 100}
			},
			wantInput: 800, wantOutput: 80, wantCacheR: 100,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			maxTokens := uint(100)
			info := tc.info(&maxTokens)
			f, c, info, l := settleFixtureEndpoint(t, tc.path, info, []byte(tc.body))
			s := CurrentRelaySession(c)
			require.Equal(t, tc.kind, s.EndpointKind())

			micro := SettleRelay(c, info, tc.usage())
			require.Greater(t, micro, 0)

			expected, err := Quote(f.db, f.cal, QuoteInput{
				Model: "deepseek-flash", Vendor: l.Vendor, MultiplierMilli: 500, At: s.FreezeAt(),
				InputTokens: tc.wantInput, OutputTokens: tc.wantOutput,
				CacheReadTokens: tc.wantCacheR, CacheWriteTokens: tc.wantCacheW,
			})
			require.NoError(t, err)
			assert.Equal(t, int(expected.TotalDebit), micro)

			var rec Request
			require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
			assert.Equal(t, RequestSuccess, rec.Status)
			assert.Equal(t, tc.wantInput, rec.InputTokens)
			assert.Equal(t, tc.wantOutput, rec.OutputTokens)
			assert.Equal(t, tc.wantCacheR, rec.CacheReadTokens)
			assert.Equal(t, tc.wantCacheW, rec.CacheWriteTokens)
		})
	}
}

// §3.7's zero-output rule is endpoint-aware: every v1 text endpoint bills
// generated output, so a zero-output response is free on all three.
func TestSettleRelayZeroOutputPerEndpoint(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		body  string
		info  func(*uint) *relaycommon.RelayInfo
		kind  relayconstant.EndpointKind
		usage *dto.Usage
	}{
		{
			name: "openai chat",
			path: "/v1/chat/completions", body: `{"model":"deepseek-flash","max_tokens":100}`,
			info:  func(n *uint) *relaycommon.RelayInfo { return newChatInfo("deepseek-flash", n) },
			kind:  relayconstant.EndpointOpenAIChat,
			usage: &dto.Usage{PromptTokens: 900},
		},
		{
			name: "openai responses",
			path: "/v1/responses", body: `{"model":"deepseek-flash","max_output_tokens":100}`,
			info:  func(n *uint) *relaycommon.RelayInfo { return newResponsesInfo("deepseek-flash", n) },
			kind:  relayconstant.EndpointOpenAIResponses,
			usage: &dto.Usage{InputTokens: 900},
		},
		{
			name: "anthropic messages",
			path: "/v1/messages", body: `{"model":"deepseek-flash","max_tokens":100,"messages":[]}`,
			info:  func(n *uint) *relaycommon.RelayInfo { return newMessagesInfo("deepseek-flash", n) },
			kind:  relayconstant.EndpointAnthropicMessages,
			usage: &dto.Usage{PromptTokens: 900, UsageSemantic: dto.BillingUsageSemanticAnthropic},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			maxTokens := uint(100)
			info := tc.info(&maxTokens)
			f, c, info, l := settleFixtureEndpoint(t, tc.path, info, []byte(tc.body))
			require.Equal(t, tc.kind, CurrentRelaySession(c).EndpointKind())

			assert.Zero(t, SettleRelay(c, info, tc.usage))
			require.NoError(t, info.Billing.Settle(0))
			assert.Equal(t, 10_000_000, buyerQuota(t, int64(f.buyerID)))

			var rec Request
			require.NoError(t, f.db.Where("request_id = ?", "req_relayhook_test").Take(&rec).Error)
			assert.Equal(t, RequestFailed, rec.Status)
			assert.Zero(t, rec.TotalDebitMicro)
			assert.Contains(t, rec.RetryChain, HopEventZeroOutput)
			assert.Empty(t, ledgerRows(t, f.db, "req_relayhook_test"))
			assert.Equal(t, 0, f.selector.Slots.InFlight(l.ID))
		})
	}
}

// TestSettleRelayUsesModelMultiplier proves the per-model override drives the
// pre-freeze, the charge and mp_requests.model_multiplier_milli, while the
// listing base multiplier stays in mp_requests.multiplier_milli for audit.
func TestSettleRelayUsesModelMultiplier(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	l := f.addListing(t, 500)
	require.NoError(t, f.db.Model(&ListingModel{}).
		Where("listing_id = ? AND model = ?", l.ID, "deepseek-flash").
		Update("multiplier_milli", 300).Error)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","max_tokens":100}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	maxTokens := uint(100)
	info := newChatInfo("deepseek-flash", &maxTokens)
	require.Nil(t, PrepareBilling(c, info, stubEstimator(1000)))

	micro := SettleRelay(c, info, &dto.Usage{PromptTokens: 900, CompletionTokens: 80})
	require.Greater(t, micro, 0)

	s := CurrentRelaySession(c)
	expected, err := Quote(f.db, f.cal, QuoteInput{
		Model: "deepseek-flash", Vendor: l.Vendor,
		MultiplierMilli: 500, ModelMultiplierMilli: 300, At: s.FreezeAt(),
		InputTokens: 900, OutputTokens: 80,
	})
	require.NoError(t, err)
	assert.Equal(t, int(expected.TotalDebit), micro)

	var rec Request
	require.NoError(t, f.db.Where("request_id = ?", s.RequestID()).Take(&rec).Error)
	assert.Equal(t, int64(300), rec.ModelMultiplierMilli, "the record carries the multiplier actually charged")
	assert.Equal(t, int64(500), rec.MultiplierMilli, "the listing base multiplier is kept for audit")

	var credit LedgerEntry
	require.NoError(t, f.db.Where("request_id = ? AND entry_type = ?", s.RequestID(), EntrySellerCredit).Take(&credit).Error)
	assert.Equal(t, int64(300), credit.ModelMultiplierMilli)
}
