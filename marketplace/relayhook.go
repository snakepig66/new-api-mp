package marketplace

// relayhook.go — 请求链路上游接缝的市场侧实现 (§4.3/§4.4). Upstream code calls
// into this file from the MP-HOOK seams; everything request-scoped lives on
// the RelaySession carried in the gin context, so no upstream structure
// changes. Import direction is strictly one-way: this package never imports
// middleware/controller/relay/service, so the seam files can import
// marketplace without creating a cycle.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
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
	HopEventSelect           = "select"
	HopEventSwitch           = "switch"
	HopEventInPlaceRetry     = "inplace_retry"
	HopEventError            = "error"
	HopEventStripTools       = "strip_tools"
	HopEventBreaker429       = "breaker_429"
	HopEventAutoPause        = "auto_pause"
	HopEventModelUnavailable = "model_unavailable"
	// HopEventPlanQuotaExhausted records a plan channel whose quota window ran
	// out: every listing of the channel was suspended until the next reset
	// (MODEL-CHANNEL-POOL §4).
	HopEventPlanQuotaExhausted = "plan_quota_exhausted"
	HopEventZeroOutput         = "zero_output"
	HopEventStreamCut          = "stream_cut"
	HopEventEarlyExit          = "early_exit"
	// HopEventSettleShortfall records that the buyer's balance could not cover
	// the actual usage: the ledger charges the amount actually collected and
	// the difference is this event's detail (H3).
	HopEventSettleShortfall = "settle_shortfall"
	// HopEventSettleCapped records that the actual usage priced above the
	// pre-freeze: the buyer is charged only the frozen amount and the platform
	// absorbs the excess.
	HopEventSettleCapped = "settle_capped"
	// HopEventMaxOutputSkipped records that the affordable max-output estimate
	// could not be written back into the outbound request type (H3).
	HopEventMaxOutputSkipped = "max_output_writeback_skipped"
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
	mu          sync.Mutex
	requestID   string
	buyerUserID int
	tokenID     int
	model       string
	// endpointKind is the buyer's protocol, fixed for the whole retry chain so
	// a switch never lands on a channel that cannot speak it.
	endpointKind relayconstant.EndpointKind
	// backendType is the upstream protocol family (openai / anthropic) of the
	// current listing. A request carrying a thinking signature may only be
	// retried onto the same family (§3.7）。
	backendType string
	// previousResponseID is the buyer's `previous_response_id`, when present.
	// boundListingID pins the whole request to the listing that created that
	// response; a bound request never switches suppliers (§3.7 Responses 状态绑定).
	previousResponseID string
	boundListingID     int64
	// hasThinkingSignature records that the inbound body carries an Anthropic
	// thinking signature, which is only replayable to the same backend family.
	hasThinkingSignature bool
	maxMultiplierMilli   int64
	officialOnly         bool
	// affinitySource/affinityValue carry the session identifier extracted at
	// BeginDistribute (P4-A) so a successful settle can record the binding.
	// They are in-process only and never logged.
	affinitySource string
	affinityValue  string
	freezeAt       time.Time
	listing        Listing
	// realKey is the current listing's decrypted seller key. It is only ever
	// written into the channel context key for the relay adaptor; never
	// logged, never returned (§2.3).
	realKey         string
	triedListingIDs []int64
	hops            []RelayHop
	hopCount        int
	inPlaceRetried  bool
	// keyRetries and channelRetries are counted separately (§3.7): a retry
	// inside the current supplier (today the single key, reserved for future
	// multi-key listings) only raises keyRetries, while a supplier switch
	// raises channelRetries. hopCount remains the listing-switch counter used
	// by the hop budget.
	keyRetries     int
	channelRetries int
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
	// slotGroupID is the P4 group whose aggregate concurrency slot this
	// attempt holds. 0 means the listing is ungrouped; a group with no
	// configured limit never holds state, so releasing is a harmless no-op.
	slotGroupID int64
	slotHeld    bool
	// tookProbe/probeListingID track the half-open circuit probe slot this
	// attempt owns (H2). It must be handed back on every non-429 end.
	tookProbe      bool
	probeListingID int64
	// billing is the request's current pre-freeze session, kept here so the
	// deferred EndRelay can refund it even when GenRelayInfo never ran (H1).
	billing      *BillingSession
	settled      bool
	settledMicro Micro
	failed       bool
	// planQuotaExhausted records that this attempt hit a plan channel's quota
	// signature and suspended the whole channel. Re-selection may still switch
	// to a different channel, but it must never retry the exhausted plan
	// in place (MODEL-CHANNEL-POOL §4).
	planQuotaExhausted bool
	recordWritten      bool
	finished           bool
	// startedAt is when the marketplace took the request; the request record
	// stores the elapsed wall-clock duration for the square's latency metric.
	startedAt time.Time
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

// EndpointKind returns the buyer's protocol for the whole retry chain. It is
// empty only for the legacy /v1/completions path, which has no endpoint kind of
// its own and is billed like chat.
func (s *RelaySession) EndpointKind() relayconstant.EndpointKind {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endpointKind
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
	id, groupID, held := s.slotListingID, s.slotGroupID, s.slotHeld
	s.slotHeld = false
	s.slotGroupID = 0
	s.mu.Unlock()
	if held && id > 0 {
		DefaultSelector().Slots.Release(id)
	}
	if groupID > 0 {
		DefaultSelector().GroupSlots.Release(groupID)
	}
}

// renewSlot extends the current attempt's concurrency slot (§3.5). Called
// periodically while a streaming response is being written so a stream longer
// than SlotTimeout keeps its slot instead of silently losing the limit.
func (s *RelaySession) renewSlot(now time.Time) {
	s.mu.Lock()
	id, groupID, held := s.slotListingID, s.slotGroupID, s.slotHeld
	s.mu.Unlock()
	if held && id > 0 {
		DefaultSelector().Slots.Renew(id, now)
	}
	if groupID > 0 {
		DefaultSelector().GroupSlots.Renew(groupID, now)
	}
}

// takeProbe records that this attempt owns listingID's half-open probe slot.
func (s *RelaySession) takeProbe(listingID int64) {
	s.mu.Lock()
	s.tookProbe = true
	s.probeListingID = listingID
	s.mu.Unlock()
}

// handBackProbe resolves the half-open probe this attempt owns, if any.
// success=true closes the circuit; false releases the probe slot without
// resolving it (5xx/timeout/early exit), so the listing stays probeable and
// is never blacklisted forever (H2). 429 is handled by Record429 in
// ProcessRelayChannelError, which resets the state itself.
func (s *RelaySession) handBackProbe(success bool) {
	s.mu.Lock()
	id, took := s.probeListingID, s.tookProbe
	s.tookProbe = false
	s.probeListingID = 0
	s.mu.Unlock()
	if !took || id <= 0 {
		return
	}
	if success {
		DefaultSelector().Breaker.RecordSuccess(id)
		return
	}
	DefaultSelector().Breaker.ReleaseProbe(id)
}

// finishAttempt closes a failed attempt: the hop event is recorded, the
// listing's concurrency slot released and a half-open probe handed back before
// the next selection (H2). A 429 has already re-broken the circuit.
func (s *RelaySession) finishAttempt(apiErr *types.NewAPIError) {
	if apiErr != nil {
		s.addHop(HopEventError, fmt.Sprintf("status=%d code=%s", apiErr.StatusCode, apiErr.GetErrorCode()))
	}
	if apiErr != nil && apiErr.StatusCode == http.StatusTooManyRequests {
		// Record429 already reset the breaker state; nothing to hand back.
		s.mu.Lock()
		s.tookProbe = false
		s.probeListingID = 0
		s.mu.Unlock()
	} else {
		s.handBackProbe(false)
	}
	s.releaseSlot()
}

// EndRelay is the deferred request-session closer installed by the distributor
// seam (H1). It runs for every marketplace request, including the paths that
// never reach PrepareBilling (bad request body, GenRelayInfo failure), the
// 402/400 pre-freeze failures and handler panics. A session that neither
// settled nor failed is finalized as failed: the pre-freeze is refunded, the
// concurrency slot and half-open probe are released and one mp_requests
// failed row is written. It is idempotent — SettleRelay/FailRelay already
// close the session, and EndRelay only fills in the gaps.
//
// A panic is re-raised so gin's recovery middleware keeps its normal behavior;
// the cleanup happens on the way out.
func EndRelay(c *gin.Context) {
	recovered := recover()
	if s := CurrentRelaySession(c); s != nil {
		s.endRelay(c)
	}
	if recovered != nil {
		panic(recovered)
	}
}

func (s *RelaySession) endRelay(c *gin.Context) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	requestID := s.requestID
	settled, failed := s.settled, s.failed
	billing := s.billing
	s.mu.Unlock()
	// P5-C: the request is over (settled, failed, or cleaned up here), so it
	// must no longer appear as an active freeze.
	DefaultSessionRegistry().Unregister(requestID)
	if settled || failed {
		// SettleRelay/FailRelay already released the slot and probe; these are
		// idempotent safety nets.
		s.releaseSlot()
		s.handBackProbe(false)
		if settled && !failed {
			// The response reached the buyer; remember where it was created so a
			// later `previous_response_id` request is pinned to the same listing
			// (§3.7 Responses 状态绑定). A failed request records no binding.
			db, _ := relayState()
			if db != nil {
				s.recordResponseBinding(c, db)
				s.recordAffinity(db)
			}
		}
		return
	}
	if billing != nil {
		billing.Refund(c)
	}
	// Record the hop before the row is serialized, so the early-exit event is
	// part of the retry chain of the failed record.
	s.addHop(HopEventEarlyExit, "unsettled request")
	db, _ := relayState()
	if db != nil {
		s.writeRecordOnce(db, marketplaceUsage{}, 0, 0, QuoteResult{}, 0, RequestFailed)
	}
	s.releaseSlot()
	s.handBackProbe(false)
}

// ConvergeOutwardError maps a marketplace request's final relay error onto the
// §3.10 outward contract (M8): 400 and 402 are preserved, every other status —
// including an upstream 401 from a seller's invalid key — becomes 503. The
// message is the fixed §3.10 wording; the upstream text has already been
// written to the retry chain and the server log by the controller, and must
// never reach the buyer (§2.3).
func ConvergeOutwardError(apiErr *types.NewAPIError) *types.NewAPIError {
	if apiErr == nil {
		return nil
	}
	status := http.StatusServiceUnavailable
	switch apiErr.StatusCode {
	case http.StatusBadRequest:
		status = http.StatusBadRequest
	case http.StatusPaymentRequired:
		status = http.StatusPaymentRequired
	}
	spec := apiErrorSpecs[status]
	return types.WithOpenAIError(types.OpenAIError{
		Message: spec.Message,
		Type:    spec.Type,
		Code:    spec.Code,
	}, status)
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

// endpointKindForPath maps a buyer path to the upstream endpoint the relay will
// call. /v1/completions has no endpoint kind of its own; it stays unrestricted
// (empty kind) rather than being forced onto the chat base.
func endpointKindForPath(path string) relayconstant.EndpointKind {
	switch path {
	case "/v1/chat/completions":
		return relayconstant.EndpointOpenAIChat
	case "/v1/responses":
		return relayconstant.EndpointOpenAIResponses
	case "/v1/messages":
		return relayconstant.EndpointAnthropicMessages
	}
	return ""
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
	endpointKind := endpointKindForPath(c.Request.URL.Path)
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	if tokenID <= 0 {
		// No API token (e.g. playground session auth): not a buyer request.
		return nil, false, 0, nil
	}
	db, _ := relayState()
	if db == nil {
		return nil, false, 0, nil
	}
	// MODEL-CHANNEL-POOL §3.1: normalize an alias (glm5.3 / GLM-5.3 /
	// zhipu/glm-5.3) to the standard model id once, before any lookup. Every
	// later step — key check, selection, pricing, records — uses the standard
	// id, so bills only ever name it.
	requestedModel := modelName
	if standard, ok := ModelPoolResolveAlias(db, modelName); ok {
		modelName = standard
	}
	anyListing, qerr := hasAnyListingForModel(db, modelName)
	if qerr != nil {
		common.SysError(common.LogText("marketplace: query listings for model %s: %s", modelName, qerr.Error()))
		return nil, true, http.StatusServiceUnavailable, errors.New("marketplace: failed to query listings")
	}
	if !anyListing {
		// A typo close to a catalog model gets a helpful 400 instead of
		// falling through to a generic upstream "model not found". Anything
		// unrelated still belongs to the regular relay.
		if suggestion := ModelPoolSuggestion(db, modelName); suggestion != "" {
			return nil, true, http.StatusBadRequest,
				fmt.Errorf("unknown model %q; did you mean %q?", requestedModel, suggestion)
		}
		return nil, false, 0, nil
	}
	officialOnly := keyOfficialOnly(db, tokenID)
	capMilli, qerr := keyModelCapMilli(db, tokenID, modelName)
	if errors.Is(qerr, ErrModelNotAddedToKey) && requestedModel != modelName {
		// Keys created before alias normalization may hold the alias spelling.
		capMilli, qerr = keyModelCapMilli(db, tokenID, requestedModel)
	}
	if errors.Is(qerr, ErrModelNotAddedToKey) {
		return nil, true, http.StatusBadRequest, fmt.Errorf("model %q %s", requestedModel, ErrModelNotAddedToKey.Error())
	}
	if qerr != nil {
		common.SysError(common.LogText("marketplace: query key models: %s", qerr.Error()))
		return nil, true, http.StatusServiceUnavailable, errors.New("marketplace: failed to query key models")
	}
	buyerUserID := int64(common.GetContextKeyInt(c, constant.ContextKeyUserId))
	// P4-A buyer-side precheck: a non-positive buyers.quota cannot pay for any
	// request, so the buyer is refused before listing selection spends work.
	// This is deliberately coarse — the precise pre-freeze still happens in
	// PrepareBilling, because the quote depends on the selected listing's
	// multiplier and channel coefficient.
	if buyerUserID > 0 {
		balance, berr := model.GetUserQuota(int(buyerUserID), true)
		if berr != nil {
			common.SysError(common.LogText("marketplace: read buyer balance: %s", berr.Error()))
			return nil, true, http.StatusServiceUnavailable, errors.New("marketplace: failed to read buyer balance")
		}
		if balance <= 0 {
			return nil, true, http.StatusPaymentRequired, ErrInsufficientBalance
		}
	}
	// MP: 买家 RPM 限制 (§3.6 每个买家有自己的 RPM 限制), counted per buyer
	// user across all of that buyer's keys (M13), checked after the
	// deterministic model-added rejection so a refused request never spends
	// budget. §3.10's outward error list has no 429; per the 402/403 待定
	// precedent this is mapped to 400 with a rate-limited message — §10 待确认.
	// M13/§3.6: the key preference may lower the limit, but a key without an
	// explicit limit uses the platform-wide buyer default instead of being
	// unlimited.
	rpm := buyerRPMLimit(db, tokenID)
	if rpm <= 0 {
		rpm = DefaultBuyerRPM()
	}
	if rpm > 0 && !BuyerRPMAllow(buyerUserID, rpm, time.Now()) {
		return nil, true, http.StatusBadRequest, errors.New("rate limited: this key exceeds its requests-per-minute limit, retry later")
	}
	// §3.7 Responses 状态绑定: parse the inbound id/signature once. A
	// `previous_response_id` must stay on the supplier that created it, and an
	// Anthropic thinking signature is only replayable to the same backend
	// family.
	previousResponseID, hasThinkingSignature := relayRequestMetadata(c)
	if previousResponseID != "" {
		if binding, found, berr := lookupResponseBinding(db, previousResponseID, time.Now()); berr != nil {
			common.SysError(common.LogText("marketplace: read response binding %s: %s", previousResponseID, berr.Error()))
			return nil, true, http.StatusServiceUnavailable, errPreviousResponseUnavailable
		} else if found {
			channel, qerr = resumeBoundListing(c, db, binding, endpointKind, modelName,
				capMilli, officialOnly, hasThinkingSignature)
			if qerr != nil {
				common.SysError(common.LogText("marketplace: bound response %s unavailable: %s", previousResponseID, qerr.Error()))
				return nil, true, http.StatusServiceUnavailable, errPreviousResponseUnavailable
			}
			return channel, true, 0, nil
		}
		// A previous response must be served by the upstream that created it.
		// Without a live binding (expired, or created outside the marketplace) no
		// supplier can be trusted with the id, so reject instead of silently
		// re-routing.
		return nil, true, http.StatusServiceUnavailable, errPreviousResponseUnavailable
	}
	has, qerr := hasLiveListingForModel(db, modelName, endpointKind)
	if qerr != nil {
		common.SysError(common.LogText("marketplace: query listings for model %s: %s", modelName, qerr.Error()))
		return nil, true, http.StatusServiceUnavailable, errors.New("marketplace: failed to query listings")
	}
	if !has {
		// Listings exist but none is certified/sellable right now: reject
		// instead of falling through to a regular upstream channel
		// (MODEL-CHANNEL-POOL 验收: 未认证的渠道或模型后端直接拒绝).
		return nil, true, http.StatusServiceUnavailable, noSupplierError(officialOnly)
	}
	// P4-A 亲和: a session identifier pins this request to the listing that
	// served the session, but only as a ranking preference — the selector still
	// applies every filter. Affinity is best-effort: a lookup failure must not
	// fail the request.
	var affinityBody []byte
	if storage, serr := common.GetBodyStorage(c); serr == nil && storage != nil {
		affinityBody, _ = storage.Bytes()
	}
	affinitySource, affinityValue, hasAffinity := ExtractAffinityIdentifier(c, affinityBody)
	var preferredListingID int64
	if hasAffinity {
		if binding, berr := LookupAffinity(db, int(buyerUserID), affinitySource, affinityValue, string(endpointKind)); berr != nil {
			common.SysError(common.LogText("marketplace: read affinity binding: %s", berr.Error()))
		} else if binding != nil {
			// A binding is a preference, not a guarantee: if the seller disabled
			// the model on that listing, ignore the binding and rank normally.
			if listingServesModelEnabled(db, binding.ListingID, modelName) {
				preferredListingID = binding.ListingID
			}
		}
	}
	listing, tookProbe, qerr := DefaultSelector().SelectWithProbe(db, SelectInput{
		Model:              modelName,
		BuyerID:            buyerUserID,
		MaxMultiplierMilli: capMilli,
		OfficialOnly:       officialOnly,
		EndpointKind:       endpointKind,
		PreferredListingID: preferredListingID,
	})
	if errors.Is(qerr, ErrNoListingAvailable) {
		return nil, true, http.StatusServiceUnavailable, noSupplierError(officialOnly)
	}
	if qerr != nil {
		common.SysError(common.LogText("marketplace: select listing: %s", qerr.Error()))
		return nil, true, http.StatusServiceUnavailable, errors.New("marketplace: failed to select a listing")
	}
	channel, qerr = startRelaySession(c, db, listing, tookProbe, endpointKind, modelName,
		capMilli, officialOnly, "", hasThinkingSignature)
	if qerr != nil {
		return nil, true, http.StatusServiceUnavailable, qerr
	}
	if hasAffinity {
		if s := CurrentRelaySession(c); s != nil {
			s.mu.Lock()
			s.affinitySource, s.affinityValue = affinitySource, affinityValue
			s.mu.Unlock()
		}
	}
	return channel, true, 0, nil
}

// errPreviousResponseUnavailable is the fixed §3.10 wording for a
// `previous_response_id` whose bound upstream cannot serve the follow-up. It
// is never replaced by a silent switch to another supplier (§3.7).
var errPreviousResponseUnavailable = errors.New("previous response's upstream is unavailable")

// startRelaySession loads the acquired listing's key and channel and installs
// the request-scoped marketplace session. It releases the concurrency slot and
// half-open probe on failure, so callers never leak them. previousResponseID
// is non-empty only for a response-bound request; the bound listing is then
// recorded on the session so no retry may switch suppliers.
func startRelaySession(
	c *gin.Context,
	db *gorm.DB,
	listing Listing,
	tookProbe bool,
	endpointKind relayconstant.EndpointKind,
	modelName string,
	capMilli int64,
	officialOnly bool,
	previousResponseID string,
	hasThinkingSignature bool,
) (*model.Channel, error) {
	release := func() {
		DefaultSelector().Slots.Release(listing.ID)
		if listing.GroupID > 0 {
			DefaultSelector().GroupSlots.Release(listing.GroupID)
		}
		if tookProbe {
			DefaultSelector().Breaker.ReleaseProbe(listing.ID)
		}
	}
	if err := hydrateListingKey(db, &listing); err != nil {
		common.SysError(common.LogText("marketplace: load listing %d key: %s", listing.ID, err.Error()))
		release()
		return nil, errors.New("marketplace: listing key unavailable")
	}
	realKey, err := DecryptListingKey(listing.KeyCiphertext)
	if err != nil {
		// §2.3: the decryption error never names the key.
		common.SysError(common.LogText("marketplace: decrypt listing %d key: %s", listing.ID, err.Error()))
		release()
		return nil, errors.New("marketplace: listing key unavailable")
	}
	channel, err := lookupRelayChannel(listing.ChannelID)
	if err != nil {
		common.SysError(common.LogText("marketplace: load listing channel %d: %s", listing.ChannelID, err.Error()))
		release()
		return nil, errors.New("no supplier available for this model")
	}
	s := &RelaySession{
		requestID:            c.GetString(common.RequestIdKey),
		buyerUserID:          common.GetContextKeyInt(c, constant.ContextKeyUserId),
		tokenID:              common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		model:                modelName,
		endpointKind:         endpointKind,
		backendType:          BackendTypeForEndpoint(endpointKind),
		previousResponseID:   previousResponseID,
		boundListingID:       listing.ID,
		hasThinkingSignature: hasThinkingSignature,
		maxMultiplierMilli:   capMilli,
		officialOnly:         officialOnly,
		listing:              listing,
		realKey:              realKey,
		triedListingIDs:      []int64{listing.ID},
		slotListingID:        listing.ID,
		slotGroupID:          listing.GroupID,
		slotHeld:             true,
		tookProbe:            tookProbe,
		startedAt:            time.Now(),
	}
	if previousResponseID == "" {
		s.boundListingID = 0
	}
	// MODEL-CHANNEL-POOL §3.4: rewrite the response's upstream model name back
	// to the standard id. The set is refreshed on every channel switch.
	if cm, ok := ChannelModelFor(db, listing.Vendor, modelName); ok {
		installModelRewrite(c, modelName, cm.UpstreamModel)
	} else {
		installModelRewrite(c, modelName)
	}
	if tookProbe {
		s.probeListingID = listing.ID
	}
	c.Set(relaySessionContextKey, s)
	attachRelayTimeoutProfile(c, endpointKind, modelName)
	installRelayResponseObserver(c, s)
	detail := ""
	if previousResponseID != "" {
		detail = "previous_response_id"
	}
	s.addHop(HopEventSelect, detail)
	return channel, nil
}

// resumeBoundListing re-acquires the listing recorded for a follow-up
// `previous_response_id` request, bypassing the ordinary selector so the
// response's upstream never changes (§3.7). Every failure — paused/banned
// listing, lost key, full concurrency, open circuit — is reported to the buyer
// as errPreviousResponseUnavailable instead of a silent re-route.
func resumeBoundListing(
	c *gin.Context,
	db *gorm.DB,
	binding ResponseBinding,
	endpointKind relayconstant.EndpointKind,
	modelName string,
	capMilli int64,
	officialOnly bool,
	hasThinkingSignature bool,
) (*model.Channel, error) {
	listing, err := GetListing(db, binding.ListingID)
	if err != nil {
		return nil, fmt.Errorf("load bound listing %d: %w", binding.ListingID, err)
	}
	if listing.Status != ListingLive && listing.Status != ListingCanary {
		return nil, fmt.Errorf("bound listing %d is %s", listing.ID, listing.Status)
	}
	if !slices.Contains(listing.Models, modelName) {
		return nil, fmt.Errorf("bound listing %d no longer serves %s", listing.ID, modelName)
	}
	if binding.Endpoint != "" && binding.Endpoint != string(endpointKind) {
		return nil, fmt.Errorf("bound endpoint %s cannot serve %s", binding.Endpoint, endpointKind)
	}
	// The upstream key must be the one that created the response: a re-keyed
	// listing no longer recognizes the id, so it counts as "key unavailable"
	// rather than silently sending the follow-up to a different credential.
	if binding.KeyFingerprint != "" && binding.KeyFingerprint != listing.KeyFingerprint {
		return nil, errors.New("bound listing key changed")
	}
	if hasThinkingSignature && !thinkingSignatureCompatible(binding.BackendType, endpointKind) {
		return nil, errors.New("thinking signature requires the bound backend family")
	}
	now := time.Now()
	groupOK, gerr := DefaultSelector().AcquireGroupLimits(db, listing.GroupID, now)
	if gerr != nil {
		return nil, fmt.Errorf("bound listing %d group limits: %w", listing.ID, gerr)
	}
	if !groupOK {
		return nil, fmt.Errorf("bound listing %d group limits are exhausted", listing.ID)
	}
	if !DefaultSelector().Slots.Acquire(listing.ID, listing.ConcurrencyLimit, now) {
		DefaultSelector().GroupSlots.Release(listing.GroupID)
		return nil, fmt.Errorf("bound listing %d has no free concurrency slot", listing.ID)
	}
	allowed, tookProbe := DefaultSelector().Breaker.allow(listing.ID, now)
	if !allowed {
		DefaultSelector().Slots.Release(listing.ID)
		DefaultSelector().GroupSlots.Release(listing.GroupID)
		return nil, fmt.Errorf("bound listing %d circuit is open", listing.ID)
	}
	return startRelaySession(c, db, listing, tookProbe, endpointKind, modelName,
		capMilli, officialOnly, binding.ResponseID, hasThinkingSignature)
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
	key, listing, modelName := s.realKey, s.listing, s.model
	s.mu.Unlock()
	common.SetContextKey(c, constant.ContextKeyChannelKey, key)

	// Per-model pass-through (§3.10): a raw body may only be forwarded for a
	// model whose upstream id the channel's mapping knows, because the body
	// rewrite pins that id. Every other model goes through the adaptor's
	// conversion path. The channel row no longer decides this platform-wide.
	db, _ := relayState()
	if db == nil {
		return
	}
	settings, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	if !ok {
		return
	}
	passthrough := false
	if pool, found := ChannelPoolBySlug(db, listing.Vendor); found {
		passthrough = pool.PassthroughBody
	} else if vendor, found := VendorByID(listing.Vendor); found {
		// Registry-only vendors (MP_EXTRA_VENDORS) have no pool row.
		passthrough = vendor.PassthroughBody
	}
	eligible := passthrough
	if eligible {
		_, eligible = ChannelModelFor(db, listing.Vendor, modelName)
	}
	settings.PassThroughBodyEnabled = eligible
	common.SetContextKey(c, constant.ContextKeyChannelSetting, settings)
}

// ForceClaudeAdaptor routes a marketplace /v1/messages request through the
// native Claude adaptor even when the listing channel's ApiType is OpenAI or
// DeepSeek. The buyer speaks Anthropic; letting an OpenAI-format adaptor parse
// an Anthropic response loses the message_start / message_delta usage and
// cache token accounting.
func ForceClaudeAdaptor(c *gin.Context, info *relaycommon.RelayInfo) {
	if info == nil || CurrentRelaySession(c) == nil {
		return
	}
	if info.RelayFormat != types.RelayFormatClaude {
		return
	}
	info.ApiType = constant.APITypeAnthropic
}

// hasAnyListingForModel reports whether any live/canary listing mentions the
// model, certified or not. It decides whether the marketplace owns the model
// at all: if it does, an uncertified supply must be rejected rather than
// silently served by a regular upstream channel.
func hasAnyListingForModel(db *gorm.DB, modelName string) (bool, error) {
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

// hasLiveListingForModel reports whether any live/canary listing serves
// modelName with a verified, active sellable qualification on an active
// channel (MODEL-CHANNEL-POOL §3.2). An optional endpoint kind restricts the
// check to channels configured for that protocol; callers that do not care
// (e.g. the key preference check) omit it. The models column is JSON text, so
// the filter runs in Go; the live listing set is small in v1.
func hasLiveListingForModel(db *gorm.DB, modelName string, endpointKinds ...relayconstant.EndpointKind) (bool, error) {
	var endpointKind relayconstant.EndpointKind
	if len(endpointKinds) > 0 {
		endpointKind = endpointKinds[0]
	}
	var rows []Listing
	if err := db.Where("status IN ?", []string{ListingLive, ListingCanary}).
		Select("id", "vendor", "models").Find(&rows).Error; err != nil {
		return false, err
	}
	ids := make([]int64, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	overrides := loadListingModelOverrides(db, ids, time.Now())
	eligible := loadVerifiedChannels(db, modelName, endpointKind)
	for i := range rows {
		if !slices.Contains(rows[i].Models, modelName) {
			continue
		}
		if !overrides.modelEnabled(rows[i].ID, modelName) {
			continue
		}
		if _, ok := eligible[rows[i].Vendor]; ok {
			return true, nil
		}
	}
	return false, nil
}

// listingServesModelEnabled reports whether one listing currently serves the
// model: it lists the model and has no disabled override for it. It is the
// affinity re-check: a binding must never pin a session to a model the seller
// turned off.
func listingServesModelEnabled(db *gorm.DB, listingID int64, modelName string) bool {
	if db == nil || listingID <= 0 {
		return false
	}
	var row Listing
	if err := db.Select("id", "models").Where("id = ?", listingID).Take(&row).Error; err != nil {
		return false
	}
	if !slices.Contains(row.Models, modelName) {
		return false
	}
	return !listingModelDisabled(db, listingID, modelName)
}

// keyModelCapMilli resolves the buyer key's price cap for one model (§3.6):
// the per-model mp_key_models cap when set, otherwise the key-level
// mp_key_prefs cap; 0 means no cap. ErrModelNotAddedToKey when the model was
// never added to the key (§4.3 step 1).
func keyModelCapMilli(db *gorm.DB, tokenID int, modelName string) (int64, error) {
	var pref KeyPref
	prefErr := db.Where("token_id = ?", tokenID).Take(&pref).Error
	if prefErr != nil && !errors.Is(prefErr, gorm.ErrRecordNotFound) {
		return 0, prefErr
	}
	// A key created with model_scope=all may call any marketplace model; the
	// marketplace catalog and live-listing checks happen before this function.
	// Its per-model cap is the key-level cap.
	if prefErr == nil && pref.ModelScope == ModelScopeAll {
		return pref.PriceCapMilli, nil
	}
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
	if prefErr == nil {
		return pref.PriceCapMilli, nil
	}
	return 0, nil
}

// buyerRPMLimit returns the buyer KeyPref's per-buyer RPM limit (0 = no
// limit). Read failures are treated as unlimited: the request flow queries
// the same database immediately afterwards and fails there with the real
// error, and the RPM check is best-effort.
func buyerRPMLimit(db *gorm.DB, tokenID int) int {
	var pref KeyPref
	if err := db.Select("rpm").Where("token_id = ?", tokenID).Take(&pref).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.SysError(common.LogText("marketplace: read key pref for buyer rpm: %s", err.Error()))
		}
		return 0
	}
	return pref.RPM
}

// noSupplierError keeps the 503 contract but tells the buyer when the empty
// supplier set is a consequence of the key's official-only routing.
func noSupplierError(officialOnly bool) error {
	if officialOnly {
		return errors.New("no supplier available for this model: official-only routing is enabled and no official channel can serve it")
	}
	return errors.New("no supplier available for this model")
}

// keyOfficialOnly reports the buyer key's "只用官方渠道" switch
// (MODEL-CHANNEL-POOL §3.2). A missing row means the switch is off.
func keyOfficialOnly(db *gorm.DB, tokenID int) bool {
	var pref KeyPref
	if err := db.Select("official_only").Where("token_id = ?", tokenID).Take(&pref).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.SysError(common.LogText("marketplace: read key pref for official-only: %s", err.Error()))
		}
		return false
	}
	return pref.OfficialOnly
}

// hydrateListingKey loads the winner's encrypted key column, which the
// listing-wide Select deliberately does not fetch (M7). Only the winner pays
// this extra targeted query, whose result lives for one request only.
func hydrateListingKey(db *gorm.DB, listing *Listing) error {
	if listing == nil || listing.ID == 0 || len(listing.KeyCiphertext) > 0 {
		return nil
	}
	var row Listing
	if err := db.Select("id", "key_ciphertext").Where("id = ?", listing.ID).Take(&row).Error; err != nil {
		return err
	}
	listing.KeyCiphertext = row.KeyCiphertext
	return nil
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
// Responses 状态绑定 (§3.7)
// ---------------------------------------------------------------------------

const (
	// responseBindingOpenAITTL mirrors upstream Responses retention: an id is
	// replayable through previous_response_id for 30 days.
	responseBindingOpenAITTL = 30 * 24 * time.Hour
	// responseBindingAnthropicTTL mirrors upstream Messages retention.
	responseBindingAnthropicTTL = time.Hour

	relayResponseObserverKey = "mp_relay_response_observer"
)

var (
	// responseIDPattern captures the upstream Responses id (`resp_…`) from
	// JSON bodies and SSE `data:` frames alike.
	responseIDPattern = regexp.MustCompile(`"id"\s*:\s*"(resp_[A-Za-z0-9_-]+)"`)
	// messageIDPattern captures an Anthropic message id (`msg_…`); it is used
	// only for the Messages endpoint so Responses output-item ids never leak
	// into the binding table.
	messageIDPattern = regexp.MustCompile(`"id"\s*:\s*"(msg_[A-Za-z0-9_-]+)"`)
	// thinkingSignaturePattern detects an Anthropic thinking block signature in
	// the raw inbound body.
	thinkingSignaturePattern = regexp.MustCompile(`"signature"\s*:\s*"[^"]+"`)
)

// BackendTypeForEndpoint maps the buyer's protocol to the upstream backend
// family recorded on a response binding. A thinking signature is only
// replayable inside one family (§3.7).
func BackendTypeForEndpoint(kind relayconstant.EndpointKind) string {
	if kind == relayconstant.EndpointAnthropicMessages {
		return BackendTypeAnthropic
	}
	return BackendTypeOpenAI
}

// thinkingSignatureCompatible reports whether a request carrying an Anthropic
// thinking signature may replay it to a candidate of the given backend family
// (§3.7). An empty backend type (rows/sessions written before the field
// existed) is unrestrained for backward compatibility.
func thinkingSignatureCompatible(backendType string, endpoint relayconstant.EndpointKind) bool {
	if backendType == "" {
		return true
	}
	return BackendTypeForEndpoint(endpoint) == backendType
}

// responseBindingTTL returns the retention window of the endpoint that
// created a response.
func responseBindingTTL(kind relayconstant.EndpointKind) time.Duration {
	if kind == relayconstant.EndpointAnthropicMessages {
		return responseBindingAnthropicTTL
	}
	return responseBindingOpenAITTL
}

// relayRequestMetadata reads the cached inbound body for a
// `previous_response_id` and an Anthropic thinking signature. Malformed or
// missing bodies simply yield the zero values; the request path then behaves
// exactly as before.
func relayRequestMetadata(c *gin.Context) (previousResponseID string, hasThinkingSignature bool) {
	if c == nil {
		return "", false
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil || storage == nil {
		return "", false
	}
	body, err := storage.Bytes()
	if err != nil || len(body) == 0 {
		return "", false
	}
	previousResponseID = strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
	hasThinkingSignature = thinkingSignaturePattern.Match(body)
	return previousResponseID, hasThinkingSignature
}

// lookupResponseBinding returns the live binding for one upstream response id.
// An expired row is removed lazily here, so no background sweep is needed.
func lookupResponseBinding(db *gorm.DB, responseID string, now time.Time) (ResponseBinding, bool, error) {
	if db == nil || strings.TrimSpace(responseID) == "" {
		return ResponseBinding{}, false, nil
	}
	var binding ResponseBinding
	err := db.Where("response_id = ?", responseID).Take(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ResponseBinding{}, false, nil
	}
	if err != nil {
		return ResponseBinding{}, false, err
	}
	if !binding.ExpiresAt.IsZero() && !binding.ExpiresAt.After(now) {
		if derr := db.Where("response_id = ?", responseID).Delete(&ResponseBinding{}).Error; derr != nil {
			common.SysError(common.LogText("marketplace: delete expired response binding %s: %s", responseID, derr.Error()))
		}
		return ResponseBinding{}, false, nil
	}
	return binding, true, nil
}

// recordResponseBinding persists response_id -> (listing, channel, endpoint,
// key, backend) after a response has been delivered to the buyer. It is
// idempotent: the upstream id is unique, so a duplicate settles on the last
// observed routing.
func (s *RelaySession) recordResponseBinding(c *gin.Context, db *gorm.DB) {
	if s == nil || c == nil || db == nil {
		return
	}
	value, ok := c.Get(relayResponseObserverKey)
	if !ok {
		return
	}
	observer, ok := value.(*relayResponseObserver)
	if !ok {
		return
	}
	responseID := observer.observedID()
	if responseID == "" {
		return
	}
	s.mu.Lock()
	listing := s.listing
	endpoint := s.endpointKind
	s.mu.Unlock()
	now := time.Now()
	binding := ResponseBinding{
		ResponseID:     responseID,
		ListingID:      listing.ID,
		ChannelID:      listing.ChannelID,
		Endpoint:       string(endpoint),
		KeyFingerprint: listing.KeyFingerprint,
		BackendType:    BackendTypeForEndpoint(endpoint),
		CreatedAt:      now,
		ExpiresAt:      now.Add(responseBindingTTL(endpoint)),
	}
	if err := db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&binding).Error; err != nil {
		common.SysError(common.LogText("marketplace: record response binding %s: %s", responseID, err.Error()))
	}
}

// relayResponseObserver wraps the gin writer to observe the upstream response
// id and to renew the listing's concurrency slot while a long stream is being
// written (§3.5/§3.7). It never changes the bytes: every write is forwarded
// unchanged to the wrapped writer.
type relayResponseObserver struct {
	gin.ResponseWriter
	session  *RelaySession
	endpoint relayconstant.EndpointKind

	mu         sync.Mutex
	pending    []byte
	responseID string
	lastRenew  time.Time
}

// Write observes data and forwards it unchanged.
func (o *relayResponseObserver) Write(data []byte) (int, error) {
	o.observe(data)
	return o.ResponseWriter.Write(data)
}

// WriteString observes s and forwards it unchanged.
func (o *relayResponseObserver) WriteString(s string) (int, error) {
	o.observe([]byte(s))
	return o.ResponseWriter.WriteString(s)
}

func (o *relayResponseObserver) observe(data []byte) {
	o.mu.Lock()
	// Only Responses/Messages carry a replayable upstream id. Chat and legacy
	// completions skip the scan so a large stream never pays the regex cost.
	if o.responseID == "" && o.endpoint != relayconstant.EndpointOpenAIChat && o.endpoint != "" {
		o.pending = append(o.pending, data...)
		if len(o.pending) > 16384 {
			// Keep a bounded tail: a response id split across two writes still
			// matches on the next chunk.
			o.pending = append([]byte(nil), o.pending[len(o.pending)-8192:]...)
		}
		pattern := responseIDPattern
		if o.endpoint == relayconstant.EndpointAnthropicMessages {
			pattern = messageIDPattern
		}
		if match := pattern.FindSubmatch(o.pending); match != nil {
			o.responseID = string(match[1])
			o.pending = nil
		}
	}
	now := time.Now()
	renew := o.session != nil && now.Sub(o.lastRenew) >= SlotRenewInterval
	if renew {
		o.lastRenew = now
	}
	o.mu.Unlock()
	if renew {
		o.session.renewSlot(now)
	}
}

// observedID returns the upstream response id seen so far, if any.
func (o *relayResponseObserver) observedID() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.responseID
}

// reset forgets a failed attempt's observed bytes/id before a retry hop.
func (o *relayResponseObserver) reset() {
	o.mu.Lock()
	o.pending = nil
	o.responseID = ""
	o.mu.Unlock()
}

// installRelayResponseObserver wraps c.Writer once per marketplace request,
// outside the model-rewrite writer so it sees the exact bytes sent to the
// buyer.
func installRelayResponseObserver(c *gin.Context, s *RelaySession) {
	if c == nil || c.Writer == nil {
		return
	}
	if _, ok := c.Get(relayResponseObserverKey); ok {
		return
	}
	observer := &relayResponseObserver{
		ResponseWriter: c.Writer,
		session:        s,
		endpoint:       s.endpointKind,
		lastRenew:      time.Now(),
	}
	c.Writer = observer
	c.Set(relayResponseObserverKey, observer)
}

// resetRelayResponseObserver clears any response id observed by a failed
// attempt, called before the retry re-selects (§3.7).
func resetRelayResponseObserver(c *gin.Context) {
	if c == nil {
		return
	}
	value, ok := c.Get(relayResponseObserverKey)
	if !ok {
		return
	}
	if observer, ok := value.(*relayResponseObserver); ok {
		observer.reset()
	}
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
	substituted := maxOut <= 0
	if substituted {
		var aerr error
		maxOut, aerr = s.affordableMaxOutput(db, cal, freezeAt, int64(estInput), searchCap, fetchCap)
		if errors.Is(aerr, ErrInsufficientBalance) {
			// H3: the balance cannot cover even the estimated input → 402
			// instead of letting the request through with a zero output cap.
			return types.NewOpenAIError(ErrInsufficientBalance, types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired, types.ErrOptionWithSkipRetry())
		}
		if aerr != nil {
			common.SysError(common.LogText("marketplace: affordable max output: %s", aerr.Error()))
			return types.NewError(errors.New("marketplace: no covering price for this request"), types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
		}
		if maxOut <= 0 {
			return types.NewOpenAIError(ErrInsufficientBalance, types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired, types.ErrOptionWithSkipRetry())
		}
		// H3: write the affordable cap into the outbound request, so the
		// upstream cannot produce more output than the buyer's balance covers.
		if !s.applyMaxOutput(info, maxOut) {
			s.addHop(HopEventMaxOutputSkipped, fmt.Sprintf("max_output=%d", maxOut))
		}
	}

	// §3.7/§3.10: a raw pass-through body must carry the same model id, usage
	// switch and output cap the parsed request was priced with. The rewrite
	// runs before the pre-freeze, so an unusable body fails without a charge.
	// Input-token estimation is unaffected: these fields never change the
	// prompt, and meta.MaxTokens is already the value used below.
	if apiErr := s.rewritePassthroughBody(c, info, listing, maxOut); apiErr != nil {
		return apiErr
	}
	if info.RelayFormat == types.RelayFormatClaude {
		s.filterAnthropicBeta(c, s.model)
	}

	quote, qerr := PreFreezeQuoteForListing(db, cal, listing, s.model,
		int64(estInput), maxOut, searchCap, fetchCap, freezeAt)
	if qerr != nil {
		// A model disabled after selection is not a pricing error: return a
		// retryable no-supplier response instead of charging the base multiplier.
		if errors.Is(qerr, ErrModelDisabled) {
			return types.NewError(errors.New("marketplace: model is disabled on this listing"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
		}
		common.SysError(common.LogText("marketplace: pre-freeze quote: %s", qerr.Error()))
		return types.NewError(errors.New("marketplace: no covering price for this request"), types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
	}
	session, qerr := NewBillingSessionWithPolicy(db, int64(s.buyerUserID), quote.TotalDebit,
		!channelAllowsGift(db, listing.Vendor))
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
	s.billing = session
	s.estInputTokens = int64(estInput)
	s.maxOutputTokens = maxOut
	s.searchCap = searchCap
	s.fetchCap = fetchCap
	s.preFreeze = quote
	s.mu.Unlock()
	// P5-C: publish the freeze so an operator can enumerate and force-release a
	// wedged request. s.requestID and s.buyerUserID are immutable.
	DefaultSessionRegistry().Register(SessionEntry{
		RequestID:   s.requestID,
		ListingID:   listing.ID,
		BuyerID:     s.buyerUserID,
		FrozenMicro: int64(session.GetPreConsumedQuota()),
		billing:     session,
	})
	return nil
}

// applyMaxOutput writes the affordable output cap back into the outbound
// request (H3), so an omitted max_tokens cannot let the upstream exceed the
// buyer's balance. It reports false for request types it cannot mutate; the
// caller records that fact in the retry chain. The relay always talks one of
// the three v1 protocols, but the same code path could see another type
// (e.g. Gemini) if the marketplace path is widened later — hence the event.
func (s *RelaySession) applyMaxOutput(info *relaycommon.RelayInfo, maxOut int64) bool {
	if info == nil || info.Request == nil || maxOut <= 0 {
		return false
	}
	n := uint(maxOut)
	switch req := info.Request.(type) {
	case *dto.GeneralOpenAIRequest:
		// The caller enriched max_tokens/max_completion_tokens from both
		// fields; write back whichever one the client actually used.
		if req.MaxCompletionTokens != nil {
			req.MaxCompletionTokens = &n
			return true
		}
		req.MaxTokens = &n
		return true
	case *dto.OpenAIResponsesRequest:
		req.MaxOutputTokens = &n
		return true
	case *dto.ClaudeRequest:
		req.MaxTokens = &n
		return true
	default:
		return false
	}
}

// affordableMaxOutput implements §3.7's "没传 max_tokens 时按模型最大输出估
// 算，但不超过余额": start from the model's maximum output and clamp it to the
// output tokens the buyer's current balance can still cover after the fixed
// part (estimated input + tool caps). It returns ErrInsufficientBalance when
// the balance cannot even cover the fixed part (H3: remaining <= 0 must be a
// 402, never a zero-output pass). The arithmetic runs in big.Int so a balance
// above ~¥9,223 no longer overflows int64 and collapses to zero (H3).
func (s *RelaySession) affordableMaxOutput(db *gorm.DB, cal *Calendar, freezeAt time.Time, estInput, searchCap, fetchCap int64) (int64, error) {
	s.mu.Lock()
	listing := s.listing
	modelName := s.model
	buyerID := s.buyerUserID
	s.mu.Unlock()

	maxOut := ModelMaxOutputTokens(modelName)
	balance, err := model.GetUserQuota(buyerID, true)
	if !channelAllowsGift(db, listing.Vendor) {
		// A seller channel may only spend paid balance; gifting must not let the
		// affordable output estimate promise quota the freezer will refuse.
		balance, err = model.GetUserPaidQuota(buyerID)
	}
	if err != nil || balance <= 0 {
		// Let the authoritative reserve in NewBillingSession produce the 402.
		return maxOut, nil
	}
	pb, err := PriceAt(db, modelName, listing.Vendor, cal.TierAt(freezeAt), freezeAt)
	if err != nil {
		return maxOut, nil
	}
	fixed, err := PreFreezeQuoteForListing(db, cal, listing, modelName, estInput, 0, searchCap, fetchCap, freezeAt)
	if err != nil {
		return maxOut, nil
	}
	remaining := int64(balance) - int64(fixed.TotalDebit)
	if remaining <= 0 {
		return 0, ErrInsufficientBalance
	}
	// cost(out) = ceil(out·price·mult / (1e6·1000)) ≤ remaining
	//   ⟹ out ≤ remaining·1e6·1000 / (price·mult), floored.
	multiplier := listingMultiplier(db, listing, modelName)
	denom := new(big.Int).Mul(big.NewInt(pb.OutputMicroPerMTok), big.NewInt(multiplier))
	if denom.Sign() <= 0 {
		return maxOut, nil
	}
	num := new(big.Int).Mul(big.NewInt(remaining), big.NewInt(MicroPerMillionTokens))
	num.Mul(num, big.NewInt(1000))
	affordable := new(big.Int).Div(num, denom)
	if !affordable.IsInt64() {
		return maxOut, nil
	}
	return min(maxOut, affordable.Int64()), nil
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
	// §3.7: once any response byte (or header) reached the buyer, another
	// supplier must not take over. The controller also guards this before
	// calling in, so the check here is defence in depth.
	if c.Writer != nil && c.Writer.Written() {
		return nil, types.NewError(errors.New("marketplace: response already started; not switching upstream"),
			types.ErrorCodeGetChannelFailed, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
	}
	// Forget any id observed by the failed attempt; the retry's response is a
	// different one and must not be bound to the wrong listing.
	resetRelayResponseObserver(c)
	s.finishAttempt(lastErr)

	s.mu.Lock()
	hopCount := s.hopCount
	inPlaceRetried := s.inPlaceRetried
	planQuotaExhausted := s.planQuotaExhausted
	excluded := slices.Clone(s.triedListingIDs)
	modelName := s.model
	endpointKind := s.endpointKind
	multiplierMilli := s.maxMultiplierMilli
	previousResponseID := s.previousResponseID
	hasThinkingSignature := s.hasThinkingSignature
	backendType := s.backendType
	s.mu.Unlock()

	selector := DefaultSelector()
	var next Listing
	tookProbe := false
	switched := false
	// A response-bound request may be retried in place, but never re-routed:
	// the new supplier would not recognize previous_response_id (§3.7).
	if previousResponseID == "" && hopCount < maxListingHops {
		listing, probe, err := selector.SelectWithProbe(db, SelectInput{
			Model:              modelName,
			BuyerID:            int64(s.buyerUserID),
			ExcludeListingIDs:  excluded,
			MaxMultiplierMilli: multiplierMilli,
			OfficialOnly:       s.officialOnly,
			EndpointKind:       endpointKind,
		})
		switch {
		case err == nil:
			next = listing
			tookProbe = probe
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
		// A plan-quota exhaustion is never retried in place: the whole plan
		// channel is suspended, so retrying the same upstream cannot succeed.
		if !inPlaceRetried && !planQuotaExhausted && isTransientUpstreamError(lastErr) {
			current := s.CurrentListing()
			if previousResponseID == "" || current.ID == s.boundListingID {
				now := time.Now()
				groupOK, gerr := selector.AcquireGroupLimits(db, current.GroupID, now)
				if gerr != nil {
					common.SysError(common.LogText("marketplace: in-place retry group limits: %s", gerr.Error()))
					groupOK = false
				}
				if groupOK && selector.Slots.Acquire(current.ID, current.ConcurrencyLimit, now) {
					next = current
					s.mu.Lock()
					s.inPlaceRetried = true
					// Same supplier/key in place: this is a key-level retry.
					s.keyRetries++
					s.slotListingID = current.ID
					s.slotGroupID = current.GroupID
					s.slotHeld = true
					s.mu.Unlock()
				} else if groupOK {
					selector.GroupSlots.Release(current.GroupID)
				}
			}
		}
		if next.ID == 0 {
			if previousResponseID != "" {
				return nil, types.NewError(errPreviousResponseUnavailable, types.ErrorCodeGetChannelFailed,
					types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
			}
			return nil, types.NewOpenAIError(ErrNoListingAvailable, types.ErrorCodeGetChannelFailed, http.StatusServiceUnavailable)
		}
	}

	// A thinking signature is upstream-specific: retrying it onto another
	// backend family would make the signature unverifiable (§3.7).
	if switched && hasThinkingSignature && !thinkingSignatureCompatible(backendType, endpointKind) {
		selector.Slots.Release(next.ID)
		selector.GroupSlots.Release(next.GroupID)
		if tookProbe {
			selector.Breaker.ReleaseProbe(next.ID)
		}
		return nil, types.NewError(errors.New("marketplace: thinking signature cannot be replayed to another backend"),
			types.ErrorCodeGetChannelFailed, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
	}

	if switched {
		s.mu.Lock()
		s.slotListingID = next.ID
		s.slotGroupID = next.GroupID
		s.slotHeld = true
		s.mu.Unlock()
		if err := hydrateListingKey(db, &next); err != nil {
			common.SysError(common.LogText("marketplace: load listing %d key: %s", next.ID, err.Error()))
			s.releaseSlot()
			if tookProbe {
				selector.Breaker.ReleaseProbe(next.ID)
			}
			return nil, types.NewError(errors.New("marketplace: listing key unavailable"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
		}
	}

	realKey, err := DecryptListingKey(next.KeyCiphertext)
	if err != nil {
		common.SysError(common.LogText("marketplace: decrypt listing %d key: %s", next.ID, err.Error()))
		s.releaseSlot()
		if tookProbe {
			selector.Breaker.ReleaseProbe(next.ID)
		}
		return nil, types.NewError(errors.New("marketplace: listing key unavailable"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
	}

	// 换一家就重新计价、重新冻结；计价时刻仍是首次预冻结时刻 (§3.7/§4.3).
	s.mu.Lock()
	freezeAt := s.freezeAt
	estInput := s.estInputTokens
	maxOut := s.maxOutputTokens
	searchCap := s.searchCap
	fetchCap := s.fetchCap
	s.mu.Unlock()

	// The new listing may map the model to a different upstream id, so the raw
	// body is rewritten again before the re-freeze. The rewrite is idempotent:
	// it always starts from the session's standard model name.
	if apiErr := s.rewritePassthroughBody(c, info, next, maxOut); apiErr != nil {
		s.releaseSlot()
		if tookProbe {
			selector.Breaker.ReleaseProbe(next.ID)
		}
		return nil, apiErr
	}
	quote, err := PreFreezeQuoteForListing(db, cal, next, modelName, estInput, maxOut, searchCap, fetchCap, freezeAt)
	if err != nil {
		common.SysError(common.LogText("marketplace: re-freeze quote: %s", err.Error()))
		s.releaseSlot()
		if tookProbe {
			selector.Breaker.ReleaseProbe(next.ID)
		}
		if errors.Is(err, ErrModelDisabled) {
			return nil, types.NewError(errors.New("marketplace: model is disabled on this listing"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
		}
		return nil, types.NewError(errors.New("marketplace: no covering price for this request"), types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
	}
	// §3.7: adjust the existing freeze by the difference instead of refunding
	// everything and freezing again. The billing session spans the whole retry
	// chain, so a hop to a cheaper listing releases only the difference and a
	// hop to a pricier one tops it up under the same atomic balance check.
	session, _ := info.Billing.(*BillingSession)
	if session == nil {
		s.mu.Lock()
		session = s.billing
		s.mu.Unlock()
	}
	newTotal := int(quote.TotalDebit)
	if session != nil {
		frozen := session.GetPreConsumedQuota()
		switch {
		case newTotal > frozen:
			if rerr := session.Reserve(newTotal); rerr != nil {
				s.releaseSlot()
				if tookProbe {
					selector.Breaker.ReleaseProbe(next.ID)
				}
				if errors.Is(rerr, ErrInsufficientBalance) {
					return nil, types.NewOpenAIError(ErrInsufficientBalance, types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired, types.ErrOptionWithSkipRetry())
				}
				common.SysError(common.LogText("marketplace: re-freeze reserve: %s", rerr.Error()))
				return nil, types.NewError(rerr, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
			}
		case newTotal < frozen:
			// A failed release leaves the larger freeze in place; settlement then
			// refunds the difference, so the buyer is never overcharged.
			if rerr := session.RefundDelta(frozen - newTotal); rerr != nil {
				common.SysError(common.LogText("marketplace: re-freeze release difference: %s", rerr.Error()))
			}
		}
	} else {
		session, err = NewBillingSessionWithPolicy(db, int64(s.buyerUserID), quote.TotalDebit,
			!channelAllowsGift(db, next.Vendor))
		if errors.Is(err, ErrInsufficientBalance) {
			s.releaseSlot()
			if tookProbe {
				selector.Breaker.ReleaseProbe(next.ID)
			}
			return nil, types.NewOpenAIError(ErrInsufficientBalance, types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired, types.ErrOptionWithSkipRetry())
		}
		if err != nil {
			common.SysError(common.LogText("marketplace: re-freeze session: %s", err.Error()))
			s.releaseSlot()
			if tookProbe {
				selector.Breaker.ReleaseProbe(next.ID)
			}
			return nil, types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry())
		}
	}
	info.Billing = session
	s.mu.Lock()
	s.billing = session
	s.mu.Unlock()
	// The freeze now belongs to the new listing (the session itself may be the
	// same one, adjusted by Reserve/RefundDelta). Keep the registry pointing at
	// the winning listing and the current amount.
	DefaultSessionRegistry().Register(SessionEntry{
		RequestID:   s.requestID,
		ListingID:   next.ID,
		BuyerID:     s.buyerUserID,
		FrozenMicro: int64(session.GetPreConsumedQuota()),
		billing:     session,
	})

	channel, err := lookupRelayChannel(next.ChannelID)
	if err != nil {
		common.SysError(common.LogText("marketplace: load listing channel %d: %s", next.ChannelID, err.Error()))
		// The request is about to fail; the deferred failure path refunds the
		// whole remaining freeze, so no refund happens here.
		s.releaseSlot()
		if tookProbe {
			selector.Breaker.ReleaseProbe(next.ID)
		}
		return nil, types.NewOpenAIError(ErrNoListingAvailable, types.ErrorCodeGetChannelFailed, http.StatusServiceUnavailable)
	}
	if apiErr := setupFn(c, channel, info.OriginModelName); apiErr != nil {
		s.releaseSlot()
		if tookProbe {
			selector.Breaker.ReleaseProbe(next.ID)
		}
		return nil, apiErr
	}

	s.mu.Lock()
	if switched {
		s.hopCount++
		// A supplier switch is a channel-level retry; an in-place retry above
		// counted as key-level. The two budgets stay independent (§3.7).
		s.channelRetries++
		s.triedListingIDs = append(s.triedListingIDs, next.ID)
	}
	s.listing = next
	s.realKey = realKey
	s.preFreeze = quote
	s.backendType = BackendTypeForEndpoint(endpointKind)
	s.mu.Unlock()
	// MODEL-CHANNEL-POOL §3.4: the new upstream may answer with another model
	// id; extend the rewrite set so the client still sees the standard name.
	if cm, ok := ChannelModelFor(db, next.Vendor, s.model); ok {
		updateModelRewrite(c, cm.UpstreamModel)
	}
	if tookProbe {
		s.takeProbe(next.ID)
	}
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
	// A response-bound request never switches suppliers, so its only remaining
	// hop is the single in-place retry (§3.7 Responses 状态绑定).
	if s.previousResponseID != "" {
		return !s.inPlaceRetried && isTransientUpstreamError(apiErr)
	}
	if s.hopCount < maxListingHops {
		return true
	}
	return !s.inPlaceRetried && isTransientUpstreamError(apiErr)
}

// marketplaceRetryBudget is the number of retries a marketplace request may
// consume: two listing switches plus one in-place retry (§3.7). It is a
// package constant on purpose — common.RetryTimes is a mutable global that
// SyncOptions periodically overwrites from the options table, so relying on it
// would silently disable switching when an operator saved RetryTimes=0 (M2).
const marketplaceRetryBudget = maxListingHops + 1

// RetryBudget returns the retry budget the relay loop and retry policy must
// use. Marketplace requests always get the package budget, independent of the
// mutable upstream setting; every other request keeps the upstream value.
func RetryBudget(c *gin.Context, upstream int) int {
	if CurrentRelaySession(c) == nil {
		return upstream
	}
	return marketplaceRetryBudget
}

// isSwitchableUpstream400Codes are upstream 400 codes that describe a channel
// or model incompatibility rather than a buyer mistake: another supplier can
// serve the request, so they are switchable (§3.7). Matching is done on both
// the structured code and the message wording.
var isSwitchableUpstream400Codes = []string{
	"channel_incompatible",
	"channel incompatible",
	"model_not_supported",
	"model not supported",
}

// isTransientUpstreamError implements §3.7's 瞬时失败: timeouts (408/504/524)
// and 5xx (network failures surface as 500 from the relay layer), plus a 400
// that names a channel/model incompatibility. A 429 is not transient here —
// it circuit-breaks the listing and switches away.
func isTransientUpstreamError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	code := err.StatusCode
	switch {
	case code <= 0, code == http.StatusRequestTimeout, code >= http.StatusInternalServerError:
		return true
	case code == http.StatusBadRequest:
		lowerCode := strings.ToLower(strings.TrimSpace(string(err.GetErrorCode())))
		if containsAnyKeyword(lowerCode, isSwitchableUpstream400Codes) {
			return true
		}
		return containsAnyKeyword(strings.ToLower(err.Error()), isSwitchableUpstream400Codes)
	default:
		return false
	}
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

// marketplaceContentKeywords mark moderation / buyer-side failures that must
// reach the buyer without taking the seller's listing out of rotation.
var marketplaceContentKeywords = []string{
	"content_filter",
	"content filter",
	"content policy",
	"moderation",
	"safety system",
	"flagged by",
}

// Vendor error codes that identify each §3.5 failure class. The HTTP status is
// only a fallback: OpenAI reports insufficient_quota as 429 and Zhipu reports
// balance exhaustion as 1113 under several statuses.
var (
	marketplaceBalanceErrorCodes = map[string]struct{}{
		"insufficient_quota":         {},
		"insufficient_balance":       {},
		"insufficient_funds":         {},
		"balance_not_enough":         {},
		"billing_hard_limit_reached": {},
		"1113":                       {}, // Zhipu: 余额不足
	}
	marketplaceAuthErrorCodes = map[string]struct{}{
		"invalid_api_key":        {},
		"incorrect_api_key":      {},
		"authentication_fails":   {},
		"invalid_authentication": {},
		"authentication_error":   {},
		"api_key_invalid":        {},
		"invalid_token":          {},
	}
	marketplaceRateLimitErrorCodes = map[string]struct{}{
		"rate_limit_exceeded": {},
		"rate_limit_error":    {},
		"rate_limited":        {},
		"too_many_requests":   {},
	}
	marketplaceContentErrorCodes = map[string]struct{}{
		"content_filter":           {},
		"content_policy_violation": {},
		"moderation_blocked":       {},
		"moderation_error":         {},
		"safety_violation":         {},
	}
	// Explicit model-availability codes. A generic "not support" wording is
	// deliberately absent: buyers can trigger it with an unsupported parameter.
	marketplaceModelUnavailableErrorCodes = map[string]struct{}{
		"model_not_found":     {},
		"model_not_supported": {},
		"model_not_exist":     {},
		"model_not_available": {},
		"unsupported_model":   {},
		"unsupportedmodel":    {},
		"invalid_model":       {},
		"invalidmodel":        {},
		"model.accessdenied":  {},
		"model.notexist":      {},
		"model_not_access":    {},
		"model_access_denied": {},
		"1211":                {}, // Zhipu: 模型不存在/未开通
		"modelnotfound":       {},
	}
)

// relayErrorClass is the §3.5 failure taxonomy. Only auth and balance
// exhaustion punish the listing; rate limits cool it down, while moderation
// and buyer errors are returned without touching the seller.
type relayErrorClass int

const (
	relayErrorClassUnknown relayErrorClass = iota
	relayErrorClassAuth
	relayErrorClassBalance
	relayErrorClassRateLimit
	relayErrorClassContent
	relayErrorClassModelUnavailable
)

// classifyRelayError reads the upstream's structured type/code before falling
// back to the HTTP status and message wording. An explicit vendor code always
// wins: a 429 carrying insufficient_quota is a balance failure, not a rate
// limit.
func classifyRelayError(err *types.NewAPIError) relayErrorClass {
	if err == nil {
		return relayErrorClassUnknown
	}
	code := strings.ToLower(strings.TrimSpace(string(err.GetErrorCode())))
	errType := ""
	switch relayErr := err.RelayError.(type) {
	case types.OpenAIError:
		errType = strings.ToLower(strings.TrimSpace(relayErr.Type))
	case types.ClaudeError:
		errType = strings.ToLower(strings.TrimSpace(relayErr.Type))
	}
	status := err.StatusCode

	switch {
	case inCodeSet(code, marketplaceModelUnavailableErrorCodes),
		errType == "model_not_found", errType == "model_not_supported":
		return relayErrorClassModelUnavailable
	case inCodeSet(code, marketplaceBalanceErrorCodes), errType == "insufficient_quota", errType == "billing_error":
		return relayErrorClassBalance
	case inCodeSet(code, marketplaceAuthErrorCodes), errType == "authentication_error", errType == "invalid_api_key":
		return relayErrorClassAuth
	case inCodeSet(code, marketplaceContentErrorCodes):
		return relayErrorClassContent
	case inCodeSet(code, marketplaceRateLimitErrorCodes), errType == "rate_limit_error", errType == "tokens":
		return relayErrorClassRateLimit
	}

	switch status {
	case http.StatusNotFound:
		return relayErrorClassModelUnavailable
	case http.StatusUnauthorized:
		return relayErrorClassAuth
	case http.StatusPaymentRequired:
		return relayErrorClassBalance
	case http.StatusTooManyRequests:
		return relayErrorClassRateLimit
	}

	lower := strings.ToLower(err.Error())
	keywords := make([]string, 0, len(operation_setting.AutomaticDisableKeywords)+len(marketplacePauseKeywords))
	keywords = append(keywords, operation_setting.AutomaticDisableKeywords...)
	keywords = append(keywords, marketplacePauseKeywords...)
	pausesKey := containsAnyKeyword(lower, keywords)
	if status == http.StatusBadRequest || status == http.StatusForbidden {
		// An explicit permission_error is the vendor naming a buyer-side access
		// failure; it must beat the generic operator keyword "Permission denied"
		// that otherwise reads as a dead key.
		if errType == "permission_error" {
			return relayErrorClassContent
		}
		if containsAnyKeyword(lower, marketplaceContentKeywords) {
			return relayErrorClassContent
		}
		if pausesKey {
			return relayErrorClassAuth
		}
		if errType == "invalid_request_error" {
			// A buyer request error never punishes the seller.
			return relayErrorClassContent
		}
	}
	// The operator-configured kill words and the marketplace vendor wordings
	// describe a dead key or an empty balance; classification above has
	// already taken explicit moderation errors out of this path.
	if pausesKey {
		return relayErrorClassAuth
	}
	return relayErrorClassUnknown
}

// inCodeSet reports whether code is one of the vendor error codes in set.
func inCodeSet(code string, set map[string]struct{}) bool {
	if code == "" {
		return false
	}
	_, ok := set[code]
	return ok
}

// containsAnyKeyword reports whether lower contains any non-empty keyword,
// case-insensitively.
func containsAnyKeyword(lower string, keywords []string) bool {
	for _, kw := range keywords {
		if kw = strings.ToLower(strings.TrimSpace(kw)); kw != "" && strings.Contains(lower, kw) {
			return true
		}
	}
	return false
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
	listing := s.CurrentListing()
	listingID := listing.ID
	if listingID == 0 {
		return
	}
	db, _ := relayState()
	// MODEL-CHANNEL-POOL §4: a plan channel reporting its quota-window
	// signature is not a failure. Suspend every listing of that channel until
	// the next boundary and treat this attempt as a retryable 503. The upstream
	// text is used for the signature match only and is never echoed to the
	// buyer: the replacement error carries a fixed message.
	if planQuotaExhaustedForListing(db, listing, apiErr) {
		s.mu.Lock()
		s.planQuotaExhausted = true
		s.mu.Unlock()
		s.addHop(HopEventPlanQuotaExhausted, "")
		*apiErr = *types.WithOpenAIError(types.OpenAIError{
			Message: "marketplace: supplier quota window exhausted; retry later",
			Type:    "upstream_error",
			Code:    PauseReasonPlanQuotaExhausted,
		}, http.StatusServiceUnavailable)
		return
	}
	class := classifyRelayError(apiErr)
	switch class {
	case relayErrorClassRateLimit:
		// §3.5: a rate limit cools the listing down for 60s; it is never a
		// balance failure and never pauses the listing.
		DefaultSelector().Breaker.Record429(listingID, time.Now())
		s.addHop(HopEventBreaker429, "")
		return
	case relayErrorClassModelUnavailable:
		// Only 404 / explicit vendor codes reach this class. Confirm with a
		// platform-owned minimal probe before disabling the model; the probe is
		// deduplicated per model for 10 minutes.
		s.addHop(HopEventModelUnavailable, s.model)
		if db == nil {
			return
		}
		if !allowModelProbe(listingID, s.model, time.Now()) {
			return
		}
		go verifyModelAvailabilityAsync(db, listingID, s.model)
		return
	case relayErrorClassAuth, relayErrorClassBalance:
		reason := "upstream rejected the key"
		if class == relayErrorClassBalance {
			reason = "upstream reported insufficient balance"
		}
		s.addHop(HopEventAutoPause, reason)
		if db == nil {
			return
		}
		if err := AutoPauseListing(db, listingID, reason); err != nil {
			common.SysError(common.LogText("marketplace: auto-pause listing %d: %s", listingID, err.Error()))
		}
	default:
		// Content moderation, buyer request errors and unknown failures never
		// punish the seller's listing; the error reaches the buyer unchanged.
	}
}

// planQuotaExhaustedForListing reports whether apiErr carries the exhaustion
// signature of a kind=plan channel behind listing, and suspends that one
// listing until the next quota-window boundary. The upstream must also have
// answered 429/402/403: a matching substring in an ordinary 5xx or network
// error must not take a healthy listing out of rotation.
//
// It is the classification hook used by ProcessRelayChannelError and returns
// false for non-plan channels, empty signatures, missing listings, non-quota
// status codes and invalid quota windows. The upstream message is never logged
// or returned.
func planQuotaExhaustedForListing(db *gorm.DB, listing Listing, apiErr *types.NewAPIError) bool {
	if db == nil || apiErr == nil || listing.ID == 0 {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusTooManyRequests, http.StatusPaymentRequired, http.StatusForbidden:
		// Only these statuses can mean "the plan's quota is used up".
	default:
		return false
	}
	ch, ok := ChannelPoolBySlug(db, listing.Vendor)
	if !ok || ch.Kind != ChannelKindPlan {
		return false
	}
	if !PlanQuotaExhausted(ch, apiErr.Error()) {
		return false
	}
	// Pause only the listing that hit the quota; other listings on the same
	// channel keep serving (they may use different keys/quotas).
	if _, err := SuspendPlanListing(db, listing.ID, time.Now()); err != nil {
		common.SysError(common.LogText("marketplace: suspend plan listing %d: %s", listing.ID, err.Error()))
		return false
	}
	return true
}

// shouldAutoPauseOnError reports whether a failed attempt justifies pausing
// the listing: only a dead key (auth failure) or an empty upstream balance may
// take supply out of rotation. A 429 only circuit-breaks, and moderation or
// buyer errors never punish the seller.
func shouldAutoPauseOnError(err *types.NewAPIError) bool {
	switch classifyRelayError(err) {
	case relayErrorClassAuth, relayErrorClassBalance:
		return true
	default:
		return false
	}
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
// Hook G2 — 直通请求体改写 (§3.7/§3.10)
// ---------------------------------------------------------------------------

// passthroughRewrite is the complete set of request-body changes the
// marketplace makes before forwarding a buyer's raw body upstream. Keeping it
// as data lets the JSON transform be tested without a database.
type passthroughRewrite struct {
	UpstreamModel   string
	MaxOutputTokens int64
	Stream          bool
	RelayFormat     types.RelayFormat
	ForceStoreFalse bool
	UserPseudonym   string
}

// rewritePassthroughJSON applies the rewrite to a raw JSON request body. It
// fails when the body is not a JSON object, so a malformed pass-through body
// is rejected instead of being forwarded unchanged.
func rewritePassthroughJSON(body []byte, in passthroughRewrite) ([]byte, error) {
	var probe map[string]any
	if err := common.Unmarshal(body, &probe); err != nil {
		return nil, err
	}
	out := body
	var err error
	if in.UpstreamModel != "" {
		if out, err = sjson.SetBytes(out, "model", in.UpstreamModel); err != nil {
			return nil, err
		}
	}
	// §3.7: a streaming chat request must ask for the usage frame, otherwise
	// there is nothing to settle against. Responses and Messages carry usage
	// natively.
	if in.Stream && in.RelayFormat != types.RelayFormatClaude &&
		in.RelayFormat != types.RelayFormatOpenAIResponses {
		if out, err = sjson.SetBytes(out, "stream_options.include_usage", true); err != nil {
			return nil, err
		}
	}
	if in.MaxOutputTokens > 0 {
		switch {
		case in.RelayFormat == types.RelayFormatOpenAIResponses:
			out, err = sjson.SetBytes(out, "max_output_tokens", in.MaxOutputTokens)
		case in.RelayFormat == types.RelayFormatClaude:
			out, err = sjson.SetBytes(out, "max_tokens", in.MaxOutputTokens)
		case gjson.GetBytes(out, "max_completion_tokens").Exists():
			// The client used the newer OpenAI field; keep its dialect.
			out, err = sjson.SetBytes(out, "max_completion_tokens", in.MaxOutputTokens)
		default:
			out, err = sjson.SetBytes(out, "max_tokens", in.MaxOutputTokens)
		}
		if err != nil {
			return nil, err
		}
	}
	if in.ForceStoreFalse {
		if out, err = sjson.SetBytes(out, "store", false); err != nil {
			return nil, err
		}
	}
	if in.UserPseudonym != "" {
		if gjson.GetBytes(out, "user").Exists() {
			if out, err = sjson.SetBytes(out, "user", in.UserPseudonym); err != nil {
				return nil, err
			}
		}
		if gjson.GetBytes(out, "metadata.user").Exists() {
			if out, err = sjson.SetBytes(out, "metadata.user", in.UserPseudonym); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// rewritePassthroughBody rewrites the cached buyer body when the listing's
// vendor forwards raw bodies and the requested model has an upstream mapping.
// The rewritten storage replaces the cached one, so the pass-through handler
// forwards the platform-priced request instead of the buyer's original bytes.
// It is a no-op for converted requests and for unknown models.
func (s *RelaySession) rewritePassthroughBody(c *gin.Context, info *relaycommon.RelayInfo, listing Listing, maxOut int64) *types.NewAPIError {
	if s == nil || info == nil || c == nil || c.Request == nil {
		return nil
	}
	db, _ := relayState()
	if db == nil {
		return nil
	}
	passthrough := false
	if pool, found := ChannelPoolBySlug(db, listing.Vendor); found {
		passthrough = pool.PassthroughBody
	} else if vendor, found := VendorByID(listing.Vendor); found {
		// Registry-only vendors (MP_EXTRA_VENDORS) have no pool row.
		passthrough = vendor.PassthroughBody
	}
	if !passthrough {
		return nil
	}
	s.mu.Lock()
	modelName := s.model
	buyerID := s.buyerUserID
	s.mu.Unlock()
	mapping, ok := ChannelModelFor(db, listing.Vendor, modelName)
	if !ok || strings.TrimSpace(mapping.UpstreamModel) == "" {
		// No mapping means no raw-body forwarding: the channel setting override
		// in ApplyRelayChannelKey has already turned conversion back on.
		return nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		common.SysError(common.LogText("marketplace: read body for rewrite: %s", err.Error()))
		return types.NewOpenAIError(err, types.ErrorCodeReadRequestBodyFailed, http.StatusServiceUnavailable)
	}
	if storage == nil {
		return nil
	}
	body, err := storage.Bytes()
	if err != nil {
		common.SysError(common.LogText("marketplace: read cached body for rewrite: %s", err.Error()))
		return types.NewOpenAIError(err, types.ErrorCodeReadRequestBodyFailed, http.StatusServiceUnavailable)
	}
	if len(body) == 0 {
		return nil
	}
	pseudonym := ""
	if gjson.GetBytes(body, "user").Exists() || gjson.GetBytes(body, "metadata.user").Exists() {
		pseudonym, err = RequestUserPseudonym(listing.SellerID, int64(buyerID))
		if err != nil {
			common.SysError(common.LogText("marketplace: derive request user pseudonym: %s", err.Error()))
			return types.NewOpenAIError(err, types.ErrorCodeReadRequestBodyFailed, http.StatusServiceUnavailable)
		}
	}
	rewritten, err := rewritePassthroughJSON(body, passthroughRewrite{
		UpstreamModel:   mapping.UpstreamModel,
		MaxOutputTokens: maxOut,
		Stream:          info.IsStream,
		RelayFormat:     info.RelayFormat,
		ForceStoreFalse: officialOpenAIChannel(db, listing.Vendor),
		UserPseudonym:   pseudonym,
	})
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest)
	}
	if bytes.Equal(rewritten, body) {
		return nil
	}
	newStorage, err := common.CreateBodyStorage(rewritten)
	if err != nil {
		common.SysError(common.LogText("marketplace: rebuild rewritten body storage: %s", err.Error()))
		return types.NewOpenAIError(err, types.ErrorCodeReadRequestBodyFailed, http.StatusServiceUnavailable)
	}
	c.Set(common.KeyBodyStorage, newStorage)
	c.Request.Body = newStorage
	_ = storage.Close()
	return nil
}

// officialOpenAIChannel reports whether a channel points at OpenAI's own API.
// Requests to it must carry store=false: the marketplace never lets a seller's
// key retain buyer conversation data on the vendor side.
func officialOpenAIChannel(db *gorm.DB, vendorSlug string) bool {
	if strings.EqualFold(strings.TrimSpace(vendorSlug), "openai") {
		return true
	}
	ch, ok := ChannelPoolBySlug(db, vendorSlug)
	if !ok {
		return false
	}
	bases := ParseChannelEndpointBases(ch.Protocols)
	for _, base := range []string{bases.ChatBase(), bases.ResponsesBase()} {
		u, err := url.Parse(strings.TrimSpace(base))
		if err != nil {
			continue
		}
		if strings.EqualFold(u.Hostname(), "api.openai.com") {
			return true
		}
	}
	return false
}

// filterAnthropicBeta prunes unknown anthropic-beta values and drops the 1M
// context beta unless the model's pooled context window actually covers 1M
// tokens (the price table only covers a window the pool advertises). It runs
// before the Claude adaptor builds the upstream headers.
func (s *RelaySession) filterAnthropicBeta(c *gin.Context, modelName string) {
	if c == nil || c.Request == nil {
		return
	}
	values := c.Request.Header.Values("anthropic-beta")
	if len(values) == 0 {
		return
	}
	allowContext1M := false
	if db, _ := relayState(); db != nil {
		if pool, ok := ModelPoolByName(db, modelName); ok {
			allowContext1M = pool.ContextLength >= 1_000_000
		}
	}
	kept := relayconstant.FilterAnthropicBetaValues(values, allowContext1M)
	c.Request.Header.Del("anthropic-beta")
	if len(kept) > 0 {
		c.Request.Header.Set("anthropic-beta", strings.Join(kept, ","))
	}
}

// ---------------------------------------------------------------------------
// Hook D — 出网与首字节超时 (relay/channel/api_request.go, §3.7/§3.11)
// ---------------------------------------------------------------------------

// Default and reasoning-model marketplace timeouts (§3.7). Reasoning models
// legitimately spend longer before producing headers and their first token,
// so they get a wider budget than chat models.
const (
	defaultRelayHeaderTimeout      = 60 * time.Second
	reasoningRelayHeaderTimeout    = 120 * time.Second
	defaultRelayFirstByteTimeout   = 20 * time.Second
	reasoningRelayFirstByteTimeout = 60 * time.Second
)

// RelayHeaderTimeoutForEndpoint returns the response-header budget of one
// marketplace upstream call. Inference/reasoning models (o1/o3/o4,
// deepseek-r1, …-thinking) are widened to reasoningRelayHeaderTimeout.
func RelayHeaderTimeoutForEndpoint(endpoint relayconstant.EndpointKind, model string) time.Duration {
	if isReasoningModel(model) {
		return reasoningRelayHeaderTimeout
	}
	return defaultRelayHeaderTimeout
}

// RelayFirstByteTimeoutForEndpoint returns the first-usable-payload budget of
// one marketplace upstream call (M1); reasoning models are widened like the
// header timeout.
func RelayFirstByteTimeoutForEndpoint(endpoint relayconstant.EndpointKind, model string) time.Duration {
	if isReasoningModel(model) {
		return reasoningRelayFirstByteTimeout
	}
	return defaultRelayFirstByteTimeout
}

// isReasoningModel reports whether the model name identifies an inference /
// reasoning model whose first output can legitimately take longer.
func isReasoningModel(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	switch {
	case strings.HasPrefix(name, "o1"), strings.HasPrefix(name, "o3"), strings.HasPrefix(name, "o4"):
		return true
	case strings.HasPrefix(name, "deepseek-r1"):
		return true
	case strings.Contains(name, "reasoning"), strings.Contains(name, "-thinking"):
		return true
	}
	return false
}

// relayTimeoutProfileKey carries the request's endpoint and model into the
// shared transport, so timeouts can differ per request without per-request
// clients. api_request.go builds the outbound request from c.Request.Context().
type relayTimeoutProfileKey struct{}

type relayTimeoutProfile struct {
	Endpoint relayconstant.EndpointKind
	Model    string
}

// attachRelayTimeoutProfile stamps the endpoint/model onto the inbound request
// context. The outbound upstream request inherits it, letting the transport
// choose per-endpoint/model timeouts (§3.7).
func attachRelayTimeoutProfile(c *gin.Context, endpoint relayconstant.EndpointKind, model string) {
	if c == nil || c.Request == nil {
		return
	}
	ctx := context.WithValue(c.Request.Context(), relayTimeoutProfileKey{}, relayTimeoutProfile{
		Endpoint: endpoint,
		Model:    model,
	})
	c.Request = c.Request.WithContext(ctx)
}

// relayHTTPClient is the shared marketplace relay client: the §3.11 egress
// guard on every dial (whitelist + per-IP validation, same dialer as
// EgressHTTPClient) and per-request header/first-data-block timeouts enforced
// by firstByteTransport (M1). No proxy is honored — a proxy would bypass the
// dial-time IP validation.
var relayHTTPClient = &http.Client{
	Transport: &firstByteTransport{
		base: &http.Transport{
			DialContext:           egressDial,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
			// ResponseHeaderTimeout is enforced per request in RoundTrip so a
			// reasoning model can wait longer than a chat model.
		},
		timeout: defaultRelayFirstByteTimeout,
	},
}

// ErrRelayFirstByteTimeout marks an upstream that produced response headers
// but no usable payload within the first-byte budget. It is a transient,
// retryable failure (§3.7).
var ErrRelayFirstByteTimeout = errors.New("marketplace: upstream first byte timeout")

// ErrRelayHeaderTimeout marks an upstream that did not send response headers
// within the endpoint/model budget. It is transient and retryable (§3.7).
var ErrRelayHeaderTimeout = errors.New("marketplace: upstream response header timeout")

// firstByteTransport wraps a RoundTripper and enforces per-request header and
// first-data-block timeouts (M1). The fallback timeout keeps directly built
// transports (tests) working without a request profile.
type firstByteTransport struct {
	base    http.RoundTripper
	timeout time.Duration
}

// timeouts resolves the header and first-byte budgets for one request.
func (t *firstByteTransport) timeouts(ctx context.Context) (header, firstByte time.Duration) {
	header = defaultRelayHeaderTimeout
	firstByte = t.timeout
	if firstByte <= 0 {
		firstByte = defaultRelayFirstByteTimeout
	}
	if profile, ok := ctx.Value(relayTimeoutProfileKey{}).(relayTimeoutProfile); ok {
		header = RelayHeaderTimeoutForEndpoint(profile.Endpoint, profile.Model)
		firstByte = RelayFirstByteTimeoutForEndpoint(profile.Endpoint, profile.Model)
	}
	return header, firstByte
}

func (t *firstByteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	headerTimeout, firstByteTimeout := t.timeouts(req.Context())
	ctx, cancel := context.WithCancel(req.Context())
	var timeoutMu sync.Mutex
	headerTimedOut := false
	timer := time.AfterFunc(headerTimeout, func() {
		timeoutMu.Lock()
		headerTimedOut = true
		timeoutMu.Unlock()
		cancel()
	})
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	timer.Stop()
	timeoutMu.Lock()
	timedOut := headerTimedOut
	timeoutMu.Unlock()
	if err != nil {
		cancel()
		if timedOut {
			return nil, ErrRelayHeaderTimeout
		}
		return resp, err
	}
	if resp == nil || resp.Body == nil {
		cancel()
		return resp, nil
	}
	streaming := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	resp.Body = &firstByteBody{
		rc:        resp.Body,
		cancel:    cancel,
		timeout:   firstByteTimeout,
		streaming: streaming,
	}
	return resp, nil
}

// firstByteBody enforces the first-data-block timeout lazily from the first
// Read. It ignores SSE keep-alive comment lines (`: keep-alive`) and
// whitespace-only non-stream bodies, matching the M1 definition of a usable
// first byte.
type firstByteBody struct {
	rc        io.ReadCloser
	cancel    context.CancelFunc
	timeout   time.Duration
	streaming bool

	mu       sync.Mutex
	started  bool
	seen     bool
	timedOut bool
	timer    *time.Timer
	// pending holds an incomplete SSE line between Reads.
	pending []byte
}

func (b *firstByteBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if !b.started {
		b.started = true
		b.timer = time.AfterFunc(b.timeout, func() {
			b.mu.Lock()
			if b.seen {
				b.mu.Unlock()
				return
			}
			b.timedOut = true
			b.mu.Unlock()
			b.cancel()
		})
	}
	b.mu.Unlock()

	n, err := b.rc.Read(p)
	if n > 0 {
		if b.streaming {
			b.scanSSE(p[:n])
		} else if containsNonSpace(p[:n]) {
			b.markSeen()
		}
	}
	if err != nil {
		b.mu.Lock()
		timedOut, seen := b.timedOut, b.seen
		b.mu.Unlock()
		if timedOut && !seen {
			err = ErrRelayFirstByteTimeout
		}
	}
	return n, err
}

// scanSSE consumes bytes into pending and marks the body as started once a
// non-empty `data:` line appears.
func (b *firstByteBody) scanSSE(chunk []byte) {
	b.mu.Lock()
	b.pending = append(b.pending, chunk...)
	for {
		idx := bytes.IndexByte(b.pending, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(string(b.pending[:idx]))
		b.pending = b.pending[idx+1:]
		if strings.HasPrefix(line, "data:") && strings.TrimSpace(line[len("data:"):]) != "" {
			b.seen = true
			break
		}
	}
	seen := b.seen
	if seen && b.timer != nil {
		b.timer.Stop()
	}
	b.mu.Unlock()
}

func (b *firstByteBody) markSeen() {
	b.mu.Lock()
	if !b.seen {
		b.seen = true
		if b.timer != nil {
			b.timer.Stop()
		}
	}
	b.mu.Unlock()
}

func containsNonSpace(p []byte) bool {
	for _, c := range p {
		switch c {
		case ' ', '\t', '\r', '\n':
		default:
			return true
		}
	}
	return false
}

func (b *firstByteBody) Close() error {
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
	}
	b.mu.Unlock()
	b.cancel()
	return b.rc.Close()
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
