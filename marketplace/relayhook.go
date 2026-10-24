package marketplace

// relayhook.go — 请求链路上游接缝的市场侧实现 (§4.3/§4.4). Upstream code calls
// into this file from the MP-HOOK seams; everything request-scoped lives on
// the RelaySession carried in the gin context, so no upstream structure
// changes. Import direction is strictly one-way: this package never imports
// middleware/controller/relay/service, so the seam files can import
// marketplace without creating a cycle.

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// maxListingHops is the §3.7 换家上限: a request may switch to at most two
// other listings; when none is left and the failure was transient it retries
// the current listing once in place (原地重试 1 次).
const maxListingHops = 2

// relaySessionContextKey is the gin context key holding the *RelaySession.
// Its presence is the single marker that the request is a marketplace
// request; every hook no-ops when it is absent (MP_ENABLED=false or a
// non-marketplace model), keeping upstream behavior byte-identical.
const relaySessionContextKey = "mp_relay_session"

// Relay hop events folded into mp_requests.retry_chain (§3.8). Details carry
// semantics only — never key material (§2.3).
const (
	HopEventSelect       = "select"
	HopEventSwitch       = "switch"
	HopEventInPlaceRetry = "inplace_retry"
	HopEventError        = "error"
	HopEventStripTools   = "strip_tools"
	HopEventBreaker429   = "breaker_429"
	HopEventAutoPause    = "auto_pause"
	HopEventZeroOutput   = "zero_output"
	HopEventStreamCut    = "stream_cut"
)

// RelayHop is one event of one marketplace request's retry chain (§3.8).
type RelayHop struct {
	Seq       int    `json:"seq"`
	Event     string `json:"event"`
	ListingID int64  `json:"listing_id,omitempty"`
	Detail    string `json:"detail,omitempty"`
	At        string `json:"at"`
}

// RelaySession is the per-request marketplace state (§4.3): the picked
// listing, the pricing instant (fixed at the first pre-freeze for the whole
// retry chain, §3.3), the hop history and the slot/bookkeeping flags. All
// methods are safe for concurrent use (a streaming settle can race a timeout
// refund).
type RelaySession struct {
	mu                 sync.Mutex
	requestID          string
	buyerUserID        int
	tokenID            int
	model              string
	maxMultiplierMilli int64
	freezeAt           time.Time
	listing            Listing
	// realKey is the current listing's decrypted seller key. It is only ever
	// written into the channel context key for the relay adaptor; never
	// logged, never returned (§2.3).
	realKey         string
	triedListingIDs []int64
	hops            []RelayHop
	hopCount        int
	inPlaceRetried  bool
	// Pre-freeze parameters captured at PrepareBilling so a retry hop
	// re-freezes the same quantities against the new listing's multiplier
	// (§4.3 step 4: 换家时重新 Quote, 时刻仍用首次预冻结时刻).
	estInputTokens  int64
	maxOutputTokens int64
	searchCap       int64
	fetchCap        int64
	preFreeze       QuoteResult
	strippedTools   int
	slotListingID   int64
	slotHeld        bool
	settled         bool
	settledMicro    Micro
	failed          bool
	recordWritten   bool
}

// ---------------------------------------------------------------------------
// package-level relay state (started by Init, §4.4 H)
// ---------------------------------------------------------------------------

var (
	relayStateMu    sync.Mutex
	relayDB         *gorm.DB
	defaultSelector *Selector
	defaultCalendar *Calendar
)

// initRelayState starts the package-level relay singletons (§4.5). Called
// once from Init after migrations and seeds.
func initRelayState(db *gorm.DB) {
	relayStateMu.Lock()
	defer relayStateMu.Unlock()
	relayDB = db
	if defaultSelector == nil {
		defaultSelector = NewSelector()
	}
	if cal, err := LoadCalendar(db); err != nil {
		common.SysError(common.LogText("marketplace: load holiday calendar: %s", err.Error()))
	} else {
		defaultCalendar = cal
	}
}

// DefaultSelector returns the process-wide listing Selector (§4.4 F),
// lazily created so unit tests and early callers never see nil.
func DefaultSelector() *Selector {
	relayStateMu.Lock()
	defer relayStateMu.Unlock()
	if defaultSelector == nil {
		defaultSelector = NewSelector()
	}
	return defaultSelector
}

// relayState returns the primary database handle and the holiday calendar.
// A nil calendar means Init has not completed; hooks treat it as a 500.
func relayState() (*gorm.DB, *Calendar) {
	relayStateMu.Lock()
	db, cal := relayDB, defaultCalendar
	relayStateMu.Unlock()
	if cal == nil && db != nil {
		if loaded, err := LoadCalendar(db); err == nil {
			relayStateMu.Lock()
			if defaultCalendar == nil {
				defaultCalendar = loaded
			}
			cal = defaultCalendar
			relayStateMu.Unlock()
		}
	}
	return db, cal
}

// ---------------------------------------------------------------------------
// session accessors
// ---------------------------------------------------------------------------

// IsRelayRequest reports whether c carries a marketplace relay session. Every
// upstream MP-HOOK gates on this (or on Enabled), so MP_ENABLED=false leaves
// upstream behavior untouched.
func IsRelayRequest(c *gin.Context) bool {
	return CurrentRelaySession(c) != nil
}

// CurrentRelaySession returns the request's marketplace session, or nil for a
// non-marketplace request.
func CurrentRelaySession(c *gin.Context) *RelaySession {
	if c == nil {
		return nil
	}
	if v, ok := c.Get(relaySessionContextKey); ok {
		if s, ok := v.(*RelaySession); ok {
			return s
		}
	}
	return nil
}

// RequestID returns the upstream request id the session was opened under.
func (s *RelaySession) RequestID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requestID
}

// FreezeAt returns the pricing instant of the whole request chain (§3.3).
func (s *RelaySession) FreezeAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.freezeAt
}

// CurrentListing returns a copy of the listing serving the current attempt.
func (s *RelaySession) CurrentListing() Listing {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listing
}

// HopEvents returns a copy of the retry-chain events recorded so far (§3.8).
func (s *RelaySession) HopEvents() []RelayHop {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.hops)
}

// StrippedTools returns how many server-side tool entries were stripped from
// the inbound request (§3.4).
func (s *RelaySession) StrippedTools() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.strippedTools
}

// addHop appends one retry-chain event. detail must never carry secrets.
func (s *RelaySession) addHop(event, detail string) {
	s.addHopForListing(event, detail, 0)
}

func (s *RelaySession) addHopForListing(event, detail string, listingID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if listingID == 0 {
		listingID = s.listing.ID
	}
	s.hops = append(s.hops, RelayHop{
		Seq:       len(s.hops) + 1,
		Event:     event,
		ListingID: listingID,
		Detail:    detail,
		At:        time.Now().Format(time.RFC3339),
	})
}

// releaseSlot hands the current listing's concurrency slot back (§3.5). It is
// idempotent: the slot may already have been released by a hop switch.
func (s *RelaySession) releaseSlot() {
	s.mu.Lock()
	id, held := s.slotListingID, s.slotHeld
	s.slotHeld = false
	s.mu.Unlock()
	if held && id > 0 {
		DefaultSelector().Slots.Release(id)
	}
}

// finishAttempt closes a failed attempt: the hop event is recorded and the
// listing's concurrency slot released before the next selection.
func (s *RelaySession) finishAttempt(apiErr *types.NewAPIError) {
	if apiErr != nil {
		s.addHop(HopEventError, fmt.Sprintf("status=%d code=%s", apiErr.StatusCode, apiErr.GetErrorCode()))
	}
	s.releaseSlot()
}

// ---------------------------------------------------------------------------
// Hook A — 市场选渠道 (middleware/distributor.go, §4.4)
// ---------------------------------------------------------------------------

// ErrModelNotAddedToKey marks the §3.6/§4.3-step-1 rejection: the buyer key
// has not added the requested model (mapped to a 400 by the caller).
var ErrModelNotAddedToKey = errors.New("model is not added to this key; add it to the key first")

// isMarketplaceRelayPath reports whether the path is one of the three
// protocols marketplace v1 serves (§3.10: chat / responses / messages).
func isMarketplaceRelayPath(path string) bool {
	switch path {
	case "/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/messages":
		return true
	}
	return false
}

// BeginDistribute is the distributor seam (§4.4 A). It decides whether the
// request is a marketplace request — MP_ENABLED, a chat/responses/messages
// POST with a token, and at least one live/canary listing serving the model —
// and if so runs the full §4.3 step 1-2 flow: key-model check (400 when the
// model was never added to the key), price-cap resolution, listing selection
// and channel pinning. The returned channel is the listing's upstream channel
// row; the caller runs middleware.SetupContextForSelectedChannel on it and
// then ApplyRelayChannelKey, which replaces the placeholder key with the real
// one. handled=false means the request is not a marketplace request and must
// follow the untouched upstream path.
//
// The OpenAI-format error mapping follows §3.10: 400 model not added, 503 暂
// 无可用供应商, 500 for internal failures.
func BeginDistribute(c *gin.Context, modelName string) (channel *model.Channel, handled bool, statusCode int, err error) {
	if !Enabled() || c == nil || c.Request == nil {
		return nil, false, 0, nil
	}
	if c.Request.Method != http.MethodPost || !isMarketplaceRelayPath(c.Request.URL.Path) {
		return nil, false, 0, nil
	}
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	if tokenID <= 0 {
		// No API token (e.g. playground session auth): not a buyer request.
		return nil, false, 0, nil
	}
	db, _ := relayState()
	if db == nil {
		return nil, false, 0, nil
	}
	has, qerr := hasLiveListingForModel(db, modelName)
	if qerr != nil {
		common.SysError(common.LogText("marketplace: query listings for model %s: %s", modelName, qerr.Error()))
		return nil, true, http.StatusInternalServerError, errors.New("marketplace: failed to query listings")
	}
	if !has {
		return nil, false, 0, nil
	}
	capMilli, qerr := keyModelCapMilli(db, tokenID, modelName)
	if errors.Is(qerr, ErrModelNotAddedToKey) {
		return nil, true, http.StatusBadRequest, fmt.Errorf("model %q %s", modelName, ErrModelNotAddedToKey.Error())
	}
	if qerr != nil {
		common.SysError(common.LogText("marketplace: query key models: %s", qerr.Error()))
		return nil, true, http.StatusInternalServerError, errors.New("marketplace: failed to query key models")
	}
	// MP: 买家 RPM 限制 (§3.6 每个买家有自己的 RPM 限制), checked after the
	// deterministic model-added rejection so a refused request never spends
	// budget. §3.10's outward error list has no 429; per the 402/403 待定
	// precedent this is mapped to 400 with a rate-limited message — §10
	// 待确认.
	if !BuyerRPMAllow(int64(tokenID), time.Now()) {
		return nil, true, http.StatusBadRequest, errors.New("rate limited: this key exceeds its requests-per-minute limit, retry later")
	}
	listing, qerr := DefaultSelector().Select(db, SelectInput{Model: modelName, MaxMultiplierMilli: capMilli})
	if errors.Is(qerr, ErrNoListingAvailable) {
		return nil, true, http.StatusServiceUnavailable, errors.New("no supplier available for this model")
	}
	if qerr != nil {
		common.SysError(common.LogText("marketplace: select listing: %s", qerr.Error()))
		return nil, true, http.StatusInternalServerError, errors.New("marketplace: failed to select a listing")
	}
	channel, qerr = lookupRelayChannel(listing.ChannelID)
	if qerr != nil {
		common.SysError(common.LogText("marketplace: load listing channel %d: %s", listing.ChannelID, qerr.Error()))
		DefaultSelector().Slots.Release(listing.ID)
		return nil, true, http.StatusServiceUnavailable, errors.New("no supplier available for this model")
	}
	realKey, qerr := DecryptListingKey(listing.KeyCiphertext)
	if qerr != nil {
		// §2.3: the decryption error never names the key.
		common.SysError(common.LogText("marketplace: decrypt listing %d key: %s", listing.ID, qerr.Error()))
		DefaultSelector().Slots.Release(listing.ID)
		return nil, true, http.StatusInternalServerError, errors.New("marketplace: listing key unavailable")
	}
	s := &RelaySession{
		requestID:          c.GetString(common.RequestIdKey),
		buyerUserID:        common.GetContextKeyInt(c, constant.ContextKeyUserId),
		tokenID:            tokenID,
		model:              modelName,
		maxMultiplierMilli: capMilli,
		listing:            listing,
		realKey:            realKey,
		triedListingIDs:    []int64{listing.ID},
		slotListingID:      listing.ID,
		slotHeld:           true,
	}
	c.Set(relaySessionContextKey, s)
	s.addHop(HopEventSelect, "")
	return channel, true, 0, nil
}

// ApplyRelayChannelKey overwrites the channel-key context entry with the
// listing's decrypted key, so RelayInfo.InitChannelMeta picks up the real key
// instead of the placeholder stored on the disabled channel row (§4.4 A). It
// is a no-op for non-marketplace requests.
func ApplyRelayChannelKey(c *gin.Context) {
	s := CurrentRelaySession(c)
	if s == nil {
		return
	}
	s.mu.Lock()
	key := s.realKey
	s.mu.Unlock()
	common.SetContextKey(c, constant.ContextKeyChannelKey, key)
}

// hasLiveListingForModel reports whether any live/canary listing serves
// modelName (§4.4 A 判定). The models column is JSON text, so the filter runs
// in Go; the live listing set is small in v1.
func hasLiveListingForModel(db *gorm.DB, modelName string) (bool, error) {
	var rows []Listing
	if err := db.Where("status IN ?", []string{ListingLive, ListingCanary}).
		Select("id", "models").Find(&rows).Error; err != nil {
		return false, err
	}
	for i := range rows {
		if slices.Contains(rows[i].Models, modelName) {
			return true, nil
		}
	}
	return false, nil
}

// keyModelCapMilli resolves the buyer key's price cap for one model (§3.6):
// the per-model mp_key_models cap when set, otherwise the key-level
// mp_key_prefs cap; 0 means no cap. ErrModelNotAddedToKey when the model was
// never added to the key (§4.3 step 1).
func keyModelCapMilli(db *gorm.DB, tokenID int, modelName string) (int64, error) {
	var km KeyModel
	err := db.Where("token_id = ? AND model = ?", tokenID, modelName).Take(&km).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrModelNotAddedToKey
		}
		return 0, err
	}
	if km.PriceCapMilli > 0 {
		return km.PriceCapMilli, nil
	}
	var pref KeyPref
	err = db.Where("token_id = ?", tokenID).Take(&pref).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return pref.PriceCapMilli, nil
}

// lookupRelayChannel loads the listing's upstream channel row. The row is
// disabled by design (traffic is gated by the listing, not the channel), so
// the normal channel-selection cache paths never surface it; CacheGetChannel
// still indexes disabled channels and is the hot-path read, with a direct DB
// fallback for the window before the cache syncs a freshly created listing.
func lookupRelayChannel(channelID int64) (*model.Channel, error) {
	channel, err := model.CacheGetChannel(int(channelID))
	if err == nil {
		return channel, nil
	}
	return model.GetChannelById(int(channelID), true)
}

// ---------------------------------------------------------------------------
// Hook B — 预冻结 (relay/request_billing.go, §4.4)
// ---------------------------------------------------------------------------

// PrepareBilling replaces upstream pre-consume for a marketplace request
// (§3.7): estimate input tokens (>64KB bodies by segmented sampling, never a
// full synchronous tokenize), substitute the model's maximum output when
// max_tokens is missing (clamped to what the balance covers), quote at the
// freeze instant and open a BillingSession that pre-freezes users.quota. The
// estimateFn callback is the upstream token estimator injected by the seam
// file, keeping this package free of the service import.
//
// Error mapping (§3.10): insufficient balance → 402; no covering price →
// 400; every other internal failure → 503 (only 401/402/400/503 ever surface).
func PrepareBilling(c *gin.Context, info *relaycommon.RelayInfo, estimateFn func(*types.TokenCountMeta) (int, error)) *types.NewAPIError {
	s := CurrentRelaySession(c)
	if s == nil || info == nil {
		return nil
	}
	db, cal := relayState()
	if db == nil || cal == nil {
		return types.NewError(errors.New("marketplace is not initialized"), types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
	}

	s.mu.Lock()
	if s.freezeAt.IsZero() {
		s.freezeAt = time.Now()
	}
	freezeAt := s.freezeAt
	listing := s.listing
	s.mu.Unlock()

	meta := &types.TokenCountMeta{TokenType: types.TokenTypeTokenizer}
	if info.Request != nil {
		meta = info.Request.GetTokenCountMeta()
		if meta == nil {
			meta = &types.TokenCountMeta{TokenType: types.TokenTypeTokenizer}
		}
	}

	// §3.7: 流式请求强制带 stream_options.include_usage — without the usage
	// frame there is nothing to settle against (the request would be free).
	// The mutation lands before the adaptor marshals the outbound body.
	if req, ok := info.Request.(*dto.GeneralOpenAIRequest); ok && req.Stream != nil && *req.Stream {
		if req.StreamOptions == nil {
			req.StreamOptions = &dto.StreamOptions{}
		}
		req.StreamOptions.IncludeUsage = true
	}

	// §3.7: bodies over 64KB are estimated by segmented sampling; smaller
	// bodies reuse the upstream estimator verbatim (禁止全量同步分词).
	estInput := 0
	bodySize := int64(-1)
	if storage, serr := common.GetBodyStorage(c); serr == nil && storage != nil {
		bodySize = storage.Size()
	}
	if bodySize > EstimateSampleThresholdBytes {
		estInput = EstimateTokensSampled(info.OriginModelName, meta.CombineText)
	} else if estimateFn != nil {
		tokens, terr := estimateFn(meta)
		if terr != nil {
			return types.NewError(terr, types.ErrorCodeCountTokenFailed, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
		}
		estInput = tokens
	}
	info.SetEstimatePromptTokens(estInput)

	searchCap := toolCapIfPriced(db, listing.Vendor, ToolItemSearch, freezeAt)
	fetchCap := toolCapIfPriced(db, listing.Vendor, ToolItemFetch, freezeAt)

	maxOut := int64(meta.MaxTokens)
	if maxOut <= 0 {
		maxOut = s.affordableMaxOutput(db, cal, freezeAt, int64(estInput), searchCap, fetchCap)
	}

	quote, qerr := PreFreezeQuote(db, cal, listing.Vendor, s.model, listing.MultiplierMilli,
		int64(estInput), maxOut, searchCap, fetchCap, freezeAt)
	if qerr != nil {
		common.SysError(common.LogText("marketplace: pre-freeze quote: %s", qerr.Error()))
		return types.NewError(errors.New("marketplace: no covering price for this request"), types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
	}
	session, qerr := NewBillingSession(db, int64(s.buyerUserID), quote.TotalDebit)
	if errors.Is(qerr, ErrInsufficientBalance) {
		// §10-3 暂定 402, OpenAI 错误格式, 语义为余额不足.
		return types.NewOpenAIError(ErrInsufficientBalance, types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired, types.ErrOptionWithSkipRetry())
	}
	if qerr != nil {
		common.SysError(common.LogText("marketplace: open billing session: %s", qerr.Error()))
		return types.NewError(qerr, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
	}
	info.Billing = session

	s.mu.Lock()
	s.estInputTokens = int64(estInput)
	s.maxOutputTokens = maxOut
	s.searchCap = searchCap
	s.fetchCap = fetchCap
	s.preFreeze = quote
	s.mu.Unlock()
	return nil
}

// affordableMaxOutput implements §3.7's "没传 max_tokens 时按模型最大输出估
// 算，但不超过余额": start from the model's maximum output and clamp it to the
// output tokens the buyer's current balance can still cover after the fixed
// part (estimated input + tool caps). The atomic reserve in NewBillingSession
// remains the authoritative balance check.
func (s *RelaySession) affordableMaxOutput(db *gorm.DB, cal *Calendar, freezeAt time.Time, estInput, searchCap, fetchCap int64) int64 {
	s.mu.Lock()
	listing := s.listing
	modelName := s.model
	buyerID := s.buyerUserID
	s.mu.Unlock()

	maxOut := ModelMaxOutputTokens(modelName)
	balance, err := model.GetUserQuota(buyerID, true)
	if err != nil || balance <= 0 {
		return maxOut
	}
	pb, err := PriceAt(db, modelName, listing.Vendor, cal.TierAt(freezeAt), freezeAt)
	if err != nil {
		return maxOut
	}
	fixed, err := PreFreezeQuote(db, cal, listing.Vendor, modelName, listing.MultiplierMilli, estInput, 0, searchCap, fetchCap, freezeAt)
	if err != nil {
		return maxOut
	}
	remaining := int64(balance) - int64(fixed.TotalDebit)
	if remaining <= 0 {
		return 0
	}
	// cost(out) = ceil(out·price·mult / (1e6·1000)) ≤ remaining
	//   ⟹ out ≤ remaining·1e6·1000 / (price·mult); the product stays well
	//   inside int64 for any realistic balance.
	denom := pb.OutputMicroPerMTok * listing.MultiplierMilli
	if denom <= 0 {
		return maxOut
	}
	affordable := remaining * MicroPerMillionTokens * 1000 / denom
	return min(maxOut, affordable)
}

// toolCapIfPriced returns the §3.4 per-request pre-freeze cap for one
// Search/Fetch item when an official unit price covers the freeze instant,
// and 0 otherwise — before the §3.4 switch-on date no per-call price exists,
// and quoting a positive cap would fail the whole request with ErrNoPrice.
func toolCapIfPriced(db *gorm.DB, vendor, item string, at time.Time) int64 {
	if _, _, err := PerCallPriceAt(db, vendor, item, at); err != nil {
		return 0
	}
	return int64(ToolPreFreezeCap())
}

// ---------------------------------------------------------------------------
// Hook C — 换家与重试语义 (controller/relay.go, §4.4)
// ---------------------------------------------------------------------------

// ReselectChannel is the retry-path channel picker for marketplace requests
// (§3.7/§4.3 step 4): it closes out the failed attempt, picks the next
// listing excluding the ones already tried (or, when none is left and the
// failure was transient, re-acquires the current listing for the single
// in-place retry), re-quotes at the ORIGINAL freeze instant, refunds the old
// pre-freeze and opens a fresh BillingSession, then pins the channel and the
// real key into the context via the injected setupFn
// (middleware.SetupContextForSelectedChannel — injected so this package never
// imports middleware).
func ReselectChannel(c *gin.Context, info *relaycommon.RelayInfo, setupFn func(*gin.Context, *model.Channel, string) *types.NewAPIError) (*model.Channel, *types.NewAPIError) {
	s := CurrentRelaySession(c)
	if s == nil || info == nil {
		return nil, types.NewError(errors.New("marketplace session missing"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
	}
	db, cal := relayState()
	if db == nil || cal == nil {
		return nil, types.NewError(errors.New("marketplace is not initialized"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
	}

	lastErr := info.LastError
	s.finishAttempt(lastErr)

	s.mu.Lock()
	hopCount := s.hopCount
	inPlaceRetried := s.inPlaceRetried
	excluded := slices.Clone(s.triedListingIDs)
	s.mu.Unlock()

	selector := DefaultSelector()
	var next Listing
	switched := false
	if hopCount < maxListingHops {
		listing, err := selector.Select(db, SelectInput{
			Model:              s.model,
			ExcludeListingIDs:  excluded,
			MaxMultiplierMilli: s.maxMultiplierMilli,
		})
		switch {
		case err == nil:
			next = listing
			switched = true
		case errors.Is(err, ErrNoListingAvailable):
			// Fall through to the single in-place retry below.
		default:
			common.SysError(common.LogText("marketplace: re-select listing: %s", err.Error()))
			return nil, types.NewError(errors.New("marketplace: failed to select a listing"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
		}
	}
	if !switched {
		// §3.7: 只剩一家且是瞬时失败（超时、5xx）→ 原地重试 1 次.
		if !inPlaceRetried && isTransientUpstreamError(lastErr) {
			current := s.CurrentListing()
			if selector.Slots.Acquire(current.ID, current.ConcurrencyLimit, time.Now()) {
				next = current
				s.mu.Lock()
				s.inPlaceRetried = true
				s.slotListingID = current.ID
				s.slotHeld = true
				s.mu.Unlock()
			}
		}
		if next.ID == 0 {
			return nil, types.NewOpenAIError(ErrNoListingAvailable, types.ErrorCodeGetChannelFailed, http.StatusServiceUnavailable)
		}
	}

	if switched {
		s.mu.Lock()
		s.slotListingID = next.ID
		s.slotHeld = true
		s.mu.Unlock()
	}

	realKey, err := DecryptListingKey(next.KeyCiphertext)
	if err != nil {
		common.SysError(common.LogText("marketplace: decrypt listing %d key: %s", next.ID, err.Error()))
		s.releaseSlot()
		return nil, types.NewError(errors.New("marketplace: listing key unavailable"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
	}

	// 换一家就重新计价、重新冻结；计价时刻仍是首次预冻结时刻 (§3.7/§4.3).
	s.mu.Lock()
	freezeAt := s.freezeAt
	estInput := s.estInputTokens
	maxOut := s.maxOutputTokens
	searchCap := s.searchCap
	fetchCap := s.fetchCap
	modelName := s.model
	s.mu.Unlock()

	if info.Billing != nil {
		info.Billing.Refund(c)
		info.Billing = nil
	}
	quote, err := PreFreezeQuote(db, cal, next.Vendor, modelName, next.MultiplierMilli, estInput, maxOut, searchCap, fetchCap, freezeAt)
	if err != nil {
		common.SysError(common.LogText("marketplace: re-freeze quote: %s", err.Error()))
		s.releaseSlot()
		return nil, types.NewError(errors.New("marketplace: no covering price for this request"), types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
	}
	session, err := NewBillingSession(db, int64(s.buyerUserID), quote.TotalDebit)
	if errors.Is(err, ErrInsufficientBalance) {
		s.releaseSlot()
		return nil, types.NewOpenAIError(ErrInsufficientBalance, types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired, types.ErrOptionWithSkipRetry())
	}
	if err != nil {
		common.SysError(common.LogText("marketplace: re-freeze session: %s", err.Error()))
		s.releaseSlot()
		return nil, types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
	}
	info.Billing = session

	channel, err := lookupRelayChannel(next.ChannelID)
	if err != nil {
		common.SysError(common.LogText("marketplace: load listing channel %d: %s", next.ChannelID, err.Error()))
		s.releaseSlot()
		return nil, types.NewOpenAIError(ErrNoListingAvailable, types.ErrorCodeGetChannelFailed, http.StatusServiceUnavailable)
	}
	if apiErr := setupFn(c, channel, info.OriginModelName); apiErr != nil {
		return nil, apiErr
	}

	s.mu.Lock()
	if switched {
		s.hopCount++
		s.triedListingIDs = append(s.triedListingIDs, next.ID)
	}
	s.listing = next
	s.realKey = realKey
	s.preFreeze = quote
	s.mu.Unlock()
	if switched {
		s.addHopForListing(HopEventSwitch, "", next.ID)
	} else {
		s.addHopForListing(HopEventInPlaceRetry, "", next.ID)
	}
	ApplyRelayChannelKey(c)
	return channel, nil
}

// MayRetry reports whether a marketplace request may consume one more retry
// (§3.7): switches are capped at maxListingHops, after which only the single
// in-place retry of a transient failure remains. Non-marketplace requests
// always get the upstream decision unchanged.
func MayRetry(c *gin.Context, apiErr *types.NewAPIError) bool {
	s := CurrentRelaySession(c)
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hopCount < maxListingHops {
		return true
	}
	return !s.inPlaceRetried && isTransientUpstreamError(apiErr)
}

// isTransientUpstreamError implements §3.7's 瞬时失败: timeouts and 5xx
// (network failures surface as 500 from the relay layer). A 429 is not
// transient here — it circuit-breaks the listing and switches away.
func isTransientUpstreamError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	code := err.StatusCode
	return code <= 0 || code == http.StatusRequestTimeout || code >= http.StatusInternalServerError
}

// ---------------------------------------------------------------------------
// Hook F — 熔断与自动暂停 (service/relay_error.go, §4.4)
// ---------------------------------------------------------------------------

// marketplacePauseKeywords extend operation_setting.AutomaticDisableKeywords
// with the v1 vendors' insufficient-balance / invalid-key wordings (§3.5).
var marketplacePauseKeywords = []string{
	"insufficient balance",
	"authentication fails",
	"invalid api key",
	"incorrect api key",
	"余额不足",
}

// ProcessRelayChannelError applies the §3.5 failure semantics to the listing
// behind the failed attempt: an upstream 429 circuit-breaks the listing for
// 60s (never pauses it), while an insufficient-balance or invalid-key signal
// auto-pauses the listing at once. It is a no-op for non-marketplace
// requests; the upstream auto-disable path stays disengaged for marketplace
// channels because their AutoBan is off.
func ProcessRelayChannelError(c *gin.Context, apiErr *types.NewAPIError) {
	s := CurrentRelaySession(c)
	if s == nil || apiErr == nil {
		return
	}
	listingID := s.CurrentListing().ID
	if listingID == 0 {
		return
	}
	if apiErr.StatusCode == http.StatusTooManyRequests {
		DefaultSelector().Breaker.Record429(listingID, time.Now())
		s.addHop(HopEventBreaker429, "")
		return
	}
	if shouldAutoPauseOnError(apiErr) {
		s.addHop(HopEventAutoPause, "")
		db, _ := relayState()
		if db == nil {
			return
		}
		if err := AutoPauseListing(db, listingID, "upstream reported insufficient balance or invalid key"); err != nil {
			common.SysError(common.LogText("marketplace: auto-pause listing %d: %s", listingID, err.Error()))
		}
	}
}

// shouldAutoPauseOnError mirrors the semantics of the upstream
// ShouldDisableChannel rule (status-code ranges + keyword match) without the
// service import: the configured disable ranges (default 401), the
// insufficient-balance statuses 402/403, and a case-insensitive keyword match
// over the operator-configured and marketplace vendor keywords.
func shouldAutoPauseOnError(err *types.NewAPIError) bool {
	code := err.StatusCode
	if operation_setting.ShouldDisableByStatusCode(code) {
		return true
	}
	if code == http.StatusPaymentRequired || code == http.StatusForbidden || code == http.StatusUnauthorized {
		return true
	}
	lower := strings.ToLower(err.Error())
	keywords := make([]string, 0, len(operation_setting.AutomaticDisableKeywords)+len(marketplacePauseKeywords))
	keywords = append(keywords, operation_setting.AutomaticDisableKeywords...)
	keywords = append(keywords, marketplacePauseKeywords...)
	for _, kw := range keywords {
		if kw = strings.ToLower(strings.TrimSpace(kw)); kw != "" && strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Hook G — 入站剥离 (controller/relay.go, §3.4/§4.4)
// ---------------------------------------------------------------------------

// vendorToolCountsReadable reports whether ExtractServerToolCounts can read
// the vendor's Search/Fetch call counts from upstream usage. v1 has no such
// vendor (DeepSeek does not report them), so every marketplace request gets
// its server tools stripped (§3.4: 读不到次数时不能估算收费).
func vendorToolCountsReadable(vendor string) bool {
	return false
}

// StripInboundServerTools removes server-side search/fetch tools from the raw
// inbound body before any parsing or format conversion (§3.4) when the
// current vendor's tool counts are not billable-readable. The stripped body
// replaces the cached body storage so every later parse (validation,
// conversion, pass-through) sees the stripped form; the event folds into the
// retry chain. No-op for non-marketplace requests.
func StripInboundServerTools(c *gin.Context) {
	s := CurrentRelaySession(c)
	if s == nil {
		return
	}
	if vendorToolCountsReadable(s.CurrentListing().Vendor) {
		return
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil || storage == nil {
		return
	}
	body, err := storage.Bytes()
	if err != nil || len(body) == 0 {
		return
	}
	stripped, removed, err := StripServerToolsFromRawRequest(body)
	if err != nil || removed == 0 {
		return
	}
	newStorage, err := common.CreateBodyStorage(stripped)
	if err != nil {
		common.SysError(common.LogText("marketplace: rebuild stripped body storage: %s", err.Error()))
		return
	}
	c.Set(common.KeyBodyStorage, newStorage)
	c.Request.Body = newStorage
	_ = storage.Close()

	s.mu.Lock()
	s.strippedTools += removed
	s.mu.Unlock()
	s.addHop(HopEventStripTools, fmt.Sprintf("removed=%d", removed))
}

// ---------------------------------------------------------------------------
// Hook D — 出网与首字节超时 (relay/channel/api_request.go, §3.7/§3.11)
// ---------------------------------------------------------------------------

// RelayFirstByteTimeout is the §3.7 first-byte timeout for marketplace
// upstream calls; a timeout switches to the next listing (counted in the
// retry budget).
const RelayFirstByteTimeout = 20 * time.Second

// relayHTTPClient is the shared marketplace relay client: the §3.11 egress
// guard on every dial (whitelist + per-IP validation, same dialer as
// EgressHTTPClient) with the §3.7 20s response-header timeout instead of the
// verification client's 30s. No proxy is honored — a proxy would bypass the
// dial-time IP validation.
var relayHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext:           egressDial,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: RelayFirstByteTimeout,
		ExpectContinueTimeout: time.Second,
	},
}

// relayHTTPClientMu guards relayHTTPClient so integration tests can swap it
// while the race detector watches request-path reads.
var relayHTTPClientMu sync.RWMutex

// RelayHTTPClient returns the marketplace egress-checked relay client.
func RelayHTTPClient() *http.Client {
	relayHTTPClientMu.RLock()
	defer relayHTTPClientMu.RUnlock()
	return relayHTTPClient
}

// SetRelayHTTPClientForTesting swaps the marketplace relay HTTP client and
// returns a function that restores the egress-checked default. It exists ONLY
// for integration tests: their mock upstreams are httptest servers on
// 127.0.0.1, a destination the §3.11 egress guard must keep refusing in
// production. Never call this from non-test code.
func SetRelayHTTPClientForTesting(c *http.Client) (restore func()) {
	relayHTTPClientMu.Lock()
	old := relayHTTPClient
	relayHTTPClient = c
	relayHTTPClientMu.Unlock()
	return func() {
		relayHTTPClientMu.Lock()
		relayHTTPClient = old
		relayHTTPClientMu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// misc
// ---------------------------------------------------------------------------

// microToInt converts a 微元 amount to the upstream quota int (§4.5: 1 微元
// == 1 quota), guarding the int64→int narrowing on 32-bit platforms.
func microToInt(m Micro) (int, error) {
	if int64(m) > int64(math.MaxInt) {
		return 0, fmt.Errorf("marketplace: amount %d out of int range", int64(m))
	}
	return int(m), nil
}
