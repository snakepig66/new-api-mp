package marketplace

// record.go — 结算接市场流水 (§3.7/§3.8/§3.9): SettleRelay prices the actual
// usage through the single Quote function at the original freeze instant,
// writes the one mp_requests row and the idempotent mp_ledger entries, and
// returns the 微元 amount the upstream seam settles into the BillingSession.
// FailRelay writes the all-failed record (§3.7 全部失败 ¥0) and releases the
// concurrency slot.

import (
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// marketplaceUsage is the normalized token breakdown of one upstream
// response, mapped onto the §3.2 price-book dimensions.
type marketplaceUsage struct {
	inputTokens  int64
	outputTokens int64
	cacheRead    int64
	cacheWrite   int64
	estimated    bool // upstream returned no usage; tokens estimated (§3.7)
}

// extractMarketplaceUsage normalizes the relay-layer usage onto the
// marketplace price dimensions: cache reads and writes price separately, so
// the plain-input count is the remainder (§3.2); reasoning tokens bill at the
// output price — OpenAI-style completion_tokens already include them, so they
// are only substituted when completion is 0 and reasoning was reported
// standalone. A nil usage means the upstream returned none (truncated stream,
// empty response): the prompt is estimated from the pre-freeze estimate and
// the output is 0, which the caller treats as a failed request (§3.7).
func extractMarketplaceUsage(info *relaycommon.RelayInfo, usage *dto.Usage) marketplaceUsage {
	if usage == nil {
		estimated := int64(0)
		if info != nil {
			estimated = int64(info.GetEstimatePromptTokens())
		}
		return marketplaceUsage{inputTokens: estimated, estimated: true}
	}
	cacheRead := int64(usage.PromptTokensDetails.CachedTokens)
	if cacheRead == 0 {
		// DeepSeek's native prompt_cache_hit_tokens, normalized by the
		// adaptor into CachedTokens; keep the raw field as a fallback.
		cacheRead = int64(usage.PromptCacheHitTokens)
	}
	cacheWrite := int64(usage.PromptTokensDetails.CacheCreationTokensTotal())
	input := int64(usage.PromptTokens) - cacheRead - cacheWrite
	if input < 0 {
		input = 0
	}
	output := int64(usage.CompletionTokens)
	if output == 0 {
		output = int64(usage.CompletionTokenDetails.ReasoningTokens)
	}
	return marketplaceUsage{
		inputTokens:  input,
		outputTokens: output,
		cacheRead:    cacheRead,
		cacheWrite:   cacheWrite,
	}
}

// SettleRelay is the settlement seam (§4.4 E), called once per request from
// service.PostTextConsumeQuota — the single funnel shared by the chat,
// responses and messages protocol paths, so the §3.7 zero-output protection
// below covers all three. It returns the 微元 amount to settle into the
// BillingSession (marketplace 微元 == quota 1:1, §4.5); the upstream quota
// math must not charge the request again.
//
// §3.7 failure protection: 0 output tokens — empty response, truncated
// stream, or an upstream that ended normally without producing anything —
// marks the request failed, settles 0 (the whole pre-freeze refunds) and
// records mp_requests as failed.
func SettleRelay(ctx *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) int {
	s := CurrentRelaySession(ctx)
	if s == nil {
		return 0
	}
	// Idempotency: a second call (defensive; the seam calls once) replays the
	// first call's amount. settledMicro is published together with settled
	// under the lock, so a concurrent replay never reads a stale amount; a
	// truly concurrent double-run is benign — the record write, the ledger
	// PostSettlement, the breaker hand-back and the slot release are all
	// idempotent, and BillingSession.Settle ignores the duplicate.
	s.mu.Lock()
	if s.settled || s.failed {
		// A request already failed (FailRelay) settles nothing, ever.
		micro := s.settledMicro
		s.mu.Unlock()
		if v, err := microToInt(micro); err == nil {
			return v
		}
		return 0
	}
	s.mu.Unlock()

	db, cal := relayState()
	u := extractMarketplaceUsage(info, usage)

	// §3.4: per-call counts come from the usage JSON only when they are
	// readable (读到才计, 读不到则 0); inbound stripping (hook G) keeps
	// unpriceable tools out of the request in the first place.
	var searchCount, fetchCount int64
	if usage != nil {
		if raw, err := common.Marshal(usage); err == nil {
			searchCount, fetchCount, _ = ExtractServerToolCounts(raw)
		}
	}

	listing := s.CurrentListing()
	freezeAt := s.FreezeAt()

	failed := u.outputTokens <= 0
	failEvent := HopEventZeroOutput
	if !failed {
		// §10-1 暂定: 流式中途断开（客户端取消）不收费; §3.7: 截断的流判失
		// 败. The upstream stream handler returns a (possibly estimated) usage
		// and a nil error on these paths, so without this check they would be
		// charged; the stream's end reason is the reliable signal (a
		// client-gone reason that actually completed is corrected to done by
		// the scanner before the handler returns).
		if info != nil && info.IsStream && info.StreamStatus != nil {
			switch info.StreamStatus.EndReason {
			case relaycommon.StreamEndReasonClientGone,
				relaycommon.StreamEndReasonTimeout,
				relaycommon.StreamEndReasonScannerErr,
				relaycommon.StreamEndReasonPanic,
				relaycommon.StreamEndReasonPingFail,
				// An upstream that ends the stream without the [DONE]
				// terminator (bare EOF) is a truncated stream; the usage on
				// record is at best an estimate and must not be billed (§3.7).
				relaycommon.StreamEndReasonEOF:
				failed = true
				failEvent = HopEventStreamCut
			}
		}
	}
	var quote QuoteResult
	var quoteErr error
	if !failed {
		if db == nil || cal == nil {
			quoteErr = errors.New("marketplace is not initialized")
		} else {
			quote, quoteErr = Quote(db, cal, QuoteInput{
				Model:           s.model,
				Vendor:          listing.Vendor,
				MultiplierMilli: listing.MultiplierMilli,
				// 整条请求（含重试链）用首次预冻结时刻重算 (§3.3/§4.3).
				At:               freezeAt,
				InputTokens:      u.inputTokens,
				OutputTokens:     u.outputTokens,
				CacheReadTokens:  u.cacheRead,
				CacheWriteTokens: u.cacheWrite,
				SearchCount:      searchCount,
				FetchCount:       fetchCount,
			})
		}
	}

	status := RequestSuccess
	var total Micro
	switch {
	case failed:
		status = RequestFailed
		s.addHop(failEvent, streamEndReasonDetail(info))
	case quoteErr != nil:
		// A pricing failure must never silently bill the buyer: fail the
		// request, refund the pre-freeze, alert ops.
		status = RequestFailed
		common.SysError(common.LogText("marketplace: settle quote failed (request %s): %s", s.RequestID(), quoteErr.Error()))
		s.addHop(HopEventError, "settle_quote_error")
	default:
		total = quote.TotalDebit
	}

	if db != nil {
		s.writeRecordOnce(db, u, searchCount, fetchCount, quote, total, status)
		if status == RequestSuccess && total > 0 {
			settlement := Settlement{
				RequestID:       s.RequestID(),
				BuyerUserID:     int64(s.buyerUserID),
				SellerUserID:    listing.SellerID,
				ListingID:       listing.ID,
				PriceVersion:    quote.PriceVersion,
				MultiplierMilli: listing.MultiplierMilli,
				TokenDebit:      quote.TokenDebit,
				ToolDebit:       quote.ToolDebit,
				Now:             time.Now(),
			}
			if err := PostSettlement(db, settlement); err != nil && !errors.Is(err, ErrSettlementConflict) {
				common.SysError(common.LogText("marketplace: post settlement (request %s): %s", s.RequestID(), err.Error()))
			}
		}
	}

	// A completed attempt (even a zero-output one) is not a 429: hand the
	// breaker's half-open probe back (§3.5).
	DefaultSelector().Breaker.RecordSuccess(listing.ID)
	s.releaseSlot()

	s.mu.Lock()
	s.settledMicro = total
	s.settled = true
	s.mu.Unlock()
	v, err := microToInt(total)
	if err != nil {
		common.SysError(common.LogText("marketplace: settle amount out of range (request %s)", s.RequestID()))
		return 0
	}
	return v
}

// streamEndReasonDetail renders the stream end reason for the retry-chain
// event detail (semantics only); empty for non-stream or normal ends.
func streamEndReasonDetail(info *relaycommon.RelayInfo) string {
	if info == nil || info.StreamStatus == nil {
		return ""
	}
	return string(info.StreamStatus.EndReason)
}

// FailRelay handles the all-failed path (§3.7 全部失败 ¥0), called from
// relay.RefundFailedRequestBilling after the final failure: it writes the
// failed mp_requests row (one per request_id, retry chain folded in) and
// releases the concurrency slot. The pre-freeze itself is returned by the
// BillingSession's Refund right after. No-op when the request already
// settled (a settled session never refunds again).
func FailRelay(c *gin.Context, info *relaycommon.RelayInfo) {
	s := CurrentRelaySession(c)
	if s == nil {
		return
	}
	s.mu.Lock()
	s.failed = true
	s.mu.Unlock()
	if info != nil && info.LastError != nil {
		s.addHop(HopEventError, fmt.Sprintf("status=%d code=%s", info.LastError.StatusCode, info.LastError.GetErrorCode()))
	}
	db, _ := relayState()
	if db != nil {
		s.writeRecordOnce(db, marketplaceUsage{}, 0, 0, QuoteResult{}, 0, RequestFailed)
	}
	s.releaseSlot()
}

// writeRecordOnce persists the single mp_requests row of the request (§3.8).
// It is idempotent: the first writer wins, later calls (a settle racing a
// failure path, or a retried settlement) are no-ops; the request_id unique
// index is the database backstop.
func (s *RelaySession) writeRecordOnce(db *gorm.DB, u marketplaceUsage, searchCount, fetchCount int64, quote QuoteResult, total Micro, status string) {
	s.mu.Lock()
	if s.recordWritten {
		s.mu.Unlock()
		return
	}
	s.recordWritten = true
	s.mu.Unlock()

	fee, credit, err := SplitPlatformFee(total)
	if err != nil {
		fee, credit = 0, 0
	}
	// The record pins the price identity of the first pre-freeze; a
	// successful settle re-quotes at the same instant (§3.3), so tier and
	// version are identical.
	tier := quote.Tier
	priceVersion := quote.PriceVersion
	official := quote.OfficialCost
	s.mu.Lock()
	if tier == "" {
		tier = s.preFreeze.Tier
	}
	if priceVersion == 0 {
		priceVersion = s.preFreeze.PriceVersion
	}
	if official == 0 {
		official = s.preFreeze.OfficialCost
	}
	listing := s.listing
	s.mu.Unlock()

	hops, merr := common.Marshal(s.HopEvents())
	if merr != nil {
		hops = []byte("[]")
	}
	rec := Request{
		RequestID:         s.RequestID(),
		BuyerUserID:       int64(s.buyerUserID),
		BuyerTokenID:      int64(s.tokenID),
		SellerID:          listing.SellerID,
		ListingID:         listing.ID,
		Model:             s.model,
		Tier:              tier,
		PriceVersion:      priceVersion,
		MultiplierMilli:   listing.MultiplierMilli,
		InputTokens:       u.inputTokens,
		OutputTokens:      u.outputTokens,
		CacheReadTokens:   u.cacheRead,
		CacheWriteTokens:  u.cacheWrite,
		SearchCount:       searchCount,
		FetchCount:        fetchCount,
		TokenDebitMicro:   int64(quote.TokenDebit),
		ToolDebitMicro:    int64(quote.ToolDebit),
		TotalDebitMicro:   int64(total),
		PlatformFeeMicro:  int64(fee),
		SellerCreditMicro: int64(credit),
		OfficialCostMicro: int64(official),
		UsageEstimated:    u.estimated,
		Status:            status,
		RetryChain:        string(hops),
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rec).Error; err != nil {
		common.SysError(common.LogText("marketplace: write request record (request %s): %s", s.RequestID(), err.Error()))
	}
}
