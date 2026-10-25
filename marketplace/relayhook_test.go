package marketplace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// relayFixture builds an isolated marketplace-enabled environment: full users
// and channels schema, seeded prices, the model package's global handle
// pointed at the test database, and the package-level relay singletons
// (enabled flag, selector, calendar, db) swapped in and restored on cleanup.
type relayFixture struct {
	db       *gorm.DB
	buyerID  int
	sellerID int64
	selector *Selector
	cal      *Calendar
}

func newRelayFixture(t *testing.T, buyerQuota int) *relayFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	require.NoError(t, SeedPrices(db))

	oldDB := model.DB
	model.DB = db
	oldEnabled := enabled
	enabled = true
	SetAnonSecret("relayhook-test-secret")

	relayStateMu.Lock()
	oldRelayDB, oldSelector, oldCal := relayDB, defaultSelector, defaultCalendar
	relayStateMu.Unlock()

	sel := NewSelector()
	cal, err := LoadCalendar(db)
	require.NoError(t, err)
	relayStateMu.Lock()
	relayDB, defaultSelector, defaultCalendar = db, sel, cal
	relayStateMu.Unlock()

	t.Cleanup(func() {
		model.DB = oldDB
		enabled = oldEnabled
		relayStateMu.Lock()
		relayDB, defaultSelector, defaultCalendar = oldRelayDB, oldSelector, oldCal
		relayStateMu.Unlock()
	})

	buyer := model.User{Username: "buyer", Password: "placeholder-pass", AffCode: "buyer", Quota: buyerQuota}
	require.NoError(t, db.Create(&buyer).Error)
	seller := model.User{Username: "seller", Password: "placeholder-pass", AffCode: "seller"}
	require.NoError(t, db.Create(&seller).Error)

	return &relayFixture{db: db, buyerID: buyer.Id, sellerID: int64(seller.Id), selector: sel, cal: cal}
}

// addListingKeySeq 让每次 addListing 的测试 key 唯一：time.Now().UnixNano() 在
// Windows 时钟粒度下可能两次相同，撞出 ErrDuplicateKey 的偶发失败。
var addListingKeySeq atomic.Int64

// addListing creates one live listing (with its disabled upstream channel)
// for deepseek-flash at the given multiplier. Each listing gets a unique key:
// one key can only be listed once platform-wide (§3.5).
func (f *relayFixture) addListing(t *testing.T, multiplierMilli int64) Listing {
	t.Helper()
	key := fmt.Sprintf("sk-relayhook-test-key-%d", addListingKeySeq.Add(1))
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        SeedVendorDeepSeek,
		APIKey:          key,
		Models:          []string{"deepseek-flash"},
		MultiplierMilli: multiplierMilli,
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).Update("status", ListingLive).Error)
	listing.Status = ListingLive
	return listing
}

// addKeyModel registers the model on the buyer's key (§3.6).
func (f *relayFixture) addKeyModel(t *testing.T, tokenID int, modelName string, capMilli int64) {
	t.Helper()
	require.NoError(t, f.db.Create(&KeyModel{TokenID: int64(tokenID), Model: modelName, PriceCapMilli: capMilli}).Error)
}

// newRelayContext builds a gin context for a POST /v1/chat/completions buyer
// request carrying the given body.
func (f *relayFixture) newRelayContext(t *testing.T, body []byte) *gin.Context {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("token_id", 77)
	common.SetContextKey(c, constant.ContextKeyUserId, f.buyerID)
	c.Set(common.RequestIdKey, "req_relayhook_test")
	return c
}

func newChatInfo(modelName string, maxTokens *uint) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: modelName,
		Request: &dto.GeneralOpenAIRequest{
			Model:     modelName,
			MaxTokens: maxTokens,
		},
	}
}

func stubEstimator(tokens int) func(*types.TokenCountMeta) (int, error) {
	return func(*types.TokenCountMeta) (int, error) { return tokens, nil }
}

func apiErrorWithStatus(code int) *types.NewAPIError {
	return types.NewOpenAIError(errors.New("upstream failure"), types.ErrorCodeBadResponseStatusCode, code)
}

// --- path /判定 -----------------------------------------------------------

func TestIsMarketplaceRelayPath(t *testing.T) {
	assert.True(t, isMarketplaceRelayPath("/v1/chat/completions"))
	assert.True(t, isMarketplaceRelayPath("/v1/completions"))
	assert.True(t, isMarketplaceRelayPath("/v1/responses"))
	assert.True(t, isMarketplaceRelayPath("/v1/messages"))
	assert.False(t, isMarketplaceRelayPath("/v1/embeddings"))
	assert.False(t, isMarketplaceRelayPath("/v1/images/generations"))
}

// --- key model caps --------------------------------------------------------

func TestKeyModelCapMilli(t *testing.T) {
	f := newRelayFixture(t, 0)
	f.addKeyModel(t, 77, "deepseek-flash", 300)

	cap, err := keyModelCapMilli(f.db, 77, "deepseek-flash")
	require.NoError(t, err)
	assert.Equal(t, int64(300), cap)

	// Model never added to the key → ErrModelNotAddedToKey (§3.6/§4.3 step 1).
	_, err = keyModelCapMilli(f.db, 77, "deepseek-v4-pro")
	assert.ErrorIs(t, err, ErrModelNotAddedToKey)

	// Per-model cap 0 inherits the key-level pref cap.
	f.addKeyModel(t, 88, "deepseek-flash", 0)
	require.NoError(t, f.db.Create(&KeyPref{TokenID: 88, Strategy: StrategyCheapest, PriceCapMilli: 700}).Error)
	cap, err = keyModelCapMilli(f.db, 88, "deepseek-flash")
	require.NoError(t, err)
	assert.Equal(t, int64(700), cap)
}

// --- BeginDistribute --------------------------------------------------------

func TestBeginDistributeNotMarketplace(t *testing.T) {
	f := newRelayFixture(t, 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))

	// No listing serves the model → not a marketplace request.
	ch, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	assert.False(t, handled)
	assert.Nil(t, ch)
	assert.False(t, IsRelayRequest(c))

	// Non-relay path → not handled even with a listing present.
	f.addListing(t, 500)
	w := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w)
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)
	c2.Set("token_id", 77)
	_, handled, _, err = BeginDistribute(c2, "deepseek-flash")
	require.NoError(t, err)
	assert.False(t, handled)
}

func TestBeginDistributeModelNotAdded(t *testing.T) {
	f := newRelayFixture(t, 0)
	f.addListing(t, 500)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))

	// The model has live listings but the key never added it → 400 (§3.6).
	_, handled, status, err := BeginDistribute(c, "deepseek-flash")
	require.Error(t, err)
	assert.True(t, handled)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, err.Error(), "not added to this key")
}

func TestBeginDistributeSelectsAndPins(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	l := f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hi"}]}`))

	ch, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	require.NotNil(t, ch)
	assert.Equal(t, int(l.ChannelID), ch.Id)

	// Session recorded with the hop chain started and the slot held.
	s := CurrentRelaySession(c)
	require.NotNil(t, s)
	assert.Equal(t, "req_relayhook_test", s.RequestID())
	assert.Equal(t, l.ID, s.CurrentListing().ID)
	assert.Equal(t, 1, f.selector.Slots.InFlight(l.ID))
	hops := s.HopEvents()
	require.Len(t, hops, 1)
	assert.Equal(t, HopEventSelect, hops[0].Event)

	// The placeholder key is replaced by the decrypted real key.
	ApplyRelayChannelKey(c)
	realKey, derr := DecryptListingKey(l.KeyCiphertext)
	require.NoError(t, derr)
	assert.Equal(t, realKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
}

func TestBeginDistributePriceCapFilters(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	f.addListing(t, 500) // multiplier 0.5x
	f.addKeyModel(t, 77, "deepseek-flash", 300)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))

	// Cap 0.3x < listing 0.5x → no supplier → 503 (§3.6 价格上限).
	_, handled, status, err := BeginDistribute(c, "deepseek-flash")
	require.Error(t, err)
	assert.True(t, handled)
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

func TestBeginDistributeOfficialOnlyNoSupplierMessage(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	// The plan qualification is verified so the listing is sellable, but the
	// plan channel itself is not an official channel.
	require.NoError(t, f.db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", "volcengine-ark-coding-plan", "deepseek-v4-pro").
		Update("verified", true).Error)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        "volcengine-ark-coding-plan",
		APIKey:          uniqueTestKey("sk-official-only-plan"),
		Models:          []string{"deepseek-v4-pro"},
		MultiplierMilli: 100,
		// Plan channels require the shared-terms acknowledgement (§4).
		PlanTermsAccepted: true,
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).Update("status", ListingLive).Error)
	f.addKeyModel(t, 77, "deepseek-v4-pro", 0)
	require.NoError(t, UpsertKeyPref(f.db, 77, KeyPrefInput{
		Strategy:     StrategyBalanced,
		ModelScope:   ModelScopeSelected,
		OfficialOnly: true,
	}))
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-v4-pro"}`))

	_, handled, status, err := BeginDistribute(c, "deepseek-v4-pro")
	require.Error(t, err)
	assert.True(t, handled)
	assert.Equal(t, http.StatusServiceUnavailable, status, "status codes are unchanged")
	assert.Contains(t, err.Error(), "official-only", "the error names the official-only cause")
}

// A live listing on an unverified channel is not sellable, so the earlier
// "no live supply" rejection must also explain official-only routing.
func TestBeginDistributeOfficialOnlyUnsellableMessage(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	// CreateListing only accepts a verified qualification; drop the flag
	// afterwards to model a live listing whose channel lost certification.
	require.NoError(t, f.db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", "volcengine-ark-coding-plan", "deepseek-v4-pro").
		Update("verified", true).Error)
	listing, err := CreateListing(f.db, CreateListingInput{
		SellerID:        f.sellerID,
		VendorID:        "volcengine-ark-coding-plan",
		APIKey:          uniqueTestKey("sk-official-only-unverified"),
		Models:          []string{"deepseek-v4-pro"},
		MultiplierMilli: 100,
		// Plan channels require the shared-terms acknowledgement (§4).
		PlanTermsAccepted: true,
	})
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", listing.ID).Update("status", ListingLive).Error)
	require.NoError(t, f.db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", "volcengine-ark-coding-plan", "deepseek-v4-pro").
		Update("verified", false).Error)
	f.addKeyModel(t, 77, "deepseek-v4-pro", 0)
	require.NoError(t, UpsertKeyPref(f.db, 77, KeyPrefInput{
		Strategy:     StrategyBalanced,
		ModelScope:   ModelScopeSelected,
		OfficialOnly: true,
	}))
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-v4-pro"}`))

	_, handled, status, err := BeginDistribute(c, "deepseek-v4-pro")
	require.Error(t, err)
	assert.True(t, handled)
	assert.Equal(t, http.StatusServiceUnavailable, status, "status codes are unchanged")
	assert.Contains(t, err.Error(), "official-only")
}

// --- PrepareBilling ----------------------------------------------------------

func TestPrepareBillingPreFreezes(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	l := f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","max_tokens":100}`))

	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	maxTokens := uint(100)
	info := newChatInfo("deepseek-flash", &maxTokens)
	apiErr := PrepareBilling(c, info, stubEstimator(1000))
	require.Nil(t, apiErr)
	require.NotNil(t, info.Billing)

	s := CurrentRelaySession(c)
	expected, qerr := PreFreezeQuote(f.db, f.cal, l.Vendor, "deepseek-flash", 500, 1000, 100, 0, 0, s.FreezeAt())
	require.NoError(t, qerr)
	assert.Equal(t, int(expected.TotalDebit), info.Billing.GetPreConsumedQuota())
	assert.Equal(t, 10_000_000-int(expected.TotalDebit), buyerQuota(t, int64(f.buyerID)))
	assert.False(t, s.FreezeAt().IsZero())
}

func TestPrepareBillingInsufficientBalance402(t *testing.T) {
	f := newRelayFixture(t, 1) // 1 微元
	f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","max_tokens":100}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	maxTokens := uint(100)
	info := newChatInfo("deepseek-flash", &maxTokens)
	apiErr := PrepareBilling(c, info, stubEstimator(1000))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusPaymentRequired, apiErr.StatusCode)
	assert.Nil(t, info.Billing)
	assert.Equal(t, 1, buyerQuota(t, int64(f.buyerID)))
}

func TestPrepareBillingDefaultMaxOutputClampedToBalance(t *testing.T) {
	// Balance covers far less than 64K output tokens → the estimate clamps
	// down to what the balance can pay (§3.7 不超过余额).
	f := newRelayFixture(t, 1000)
	l := f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	info := newChatInfo("deepseek-flash", nil) // no max_tokens
	apiErr := PrepareBilling(c, info, stubEstimator(10))
	require.Nil(t, apiErr)
	require.NotNil(t, info.Billing)

	s := CurrentRelaySession(c)
	assert.Less(t, s.maxOutputTokens, int64(64<<10))
	assert.LessOrEqual(t, info.Billing.GetPreConsumedQuota(), 1000)
	// H3: the affordable cap is written back into the outbound request so the
	// upstream cannot generate more than the buyer can pay for.
	req := info.Request.(*dto.GeneralOpenAIRequest)
	require.NotNil(t, req.MaxTokens)
	assert.Equal(t, uint(s.maxOutputTokens), *req.MaxTokens)

	// The clamp used the listing's actual output price at the freeze instant.
	pb, perr := PriceAt(f.db, "deepseek-flash", l.Vendor, f.cal.TierAt(s.FreezeAt()), s.FreezeAt())
	require.NoError(t, perr)
	assert.Greater(t, pb.OutputMicroPerMTok, int64(0))
}

// H3: a balance that cannot cover even the estimated input is a 402, not a
// pass-through with a zero output cap.
func TestPrepareBillingBalanceBelowInput402(t *testing.T) {
	f := newRelayFixture(t, 1) // cannot cover any non-trivial input
	f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	info := newChatInfo("deepseek-flash", nil) // no max_tokens → affordableMaxOutput path
	apiErr := PrepareBilling(c, info, stubEstimator(100_000))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusPaymentRequired, apiErr.StatusCode)
	assert.Nil(t, info.Billing)
	assert.Equal(t, 1, buyerQuota(t, int64(f.buyerID)))
}

// H3: the old remaining*1e6*1000 arithmetic overflowed int64 above ~¥9,223 and
// clamped the output budget to zero. A large balance must still produce a
// positive affordable output.
func TestAffordableMaxOutputLargeBalanceNoOverflow(t *testing.T) {
	f := newRelayFixture(t, 20_000_000_000) // ¥20,000
	l := f.addListing(t, 500)
	s := &RelaySession{
		buyerUserID:        f.buyerID,
		model:              "deepseek-flash",
		listing:            l,
		freezeAt:           time.Now(),
		maxMultiplierMilli: 500,
	}
	out, err := s.affordableMaxOutput(f.db, f.cal, s.freezeAt, 10, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, ModelMaxOutputTokens("deepseek-flash"), out,
		"a large balance can afford the model's full output")
}

// H3: the affordable max-output is written into whichever v1 protocol request
// carries it.
func TestApplyMaxOutputWritesBack(t *testing.T) {
	s := &RelaySession{}
	maxCompletion := uint(7)
	chat := &dto.GeneralOpenAIRequest{MaxCompletionTokens: &maxCompletion}
	assert.True(t, s.applyMaxOutput(&relaycommon.RelayInfo{Request: chat}, 1234))
	assert.Equal(t, uint(1234), *chat.MaxCompletionTokens, "the field the client used is rewritten")

	plainChat := &dto.GeneralOpenAIRequest{}
	assert.True(t, s.applyMaxOutput(&relaycommon.RelayInfo{Request: plainChat}, 1234))
	require.NotNil(t, plainChat.MaxTokens)
	assert.Equal(t, uint(1234), *plainChat.MaxTokens)

	responses := &dto.OpenAIResponsesRequest{}
	assert.True(t, s.applyMaxOutput(&relaycommon.RelayInfo{Request: responses}, 1234))
	require.NotNil(t, responses.MaxOutputTokens)
	assert.Equal(t, uint(1234), *responses.MaxOutputTokens)

	claude := &dto.ClaudeRequest{}
	assert.True(t, s.applyMaxOutput(&relaycommon.RelayInfo{Request: claude}, 1234))
	require.NotNil(t, claude.MaxTokens)
	assert.Equal(t, uint(1234), *claude.MaxTokens)

	assert.False(t, s.applyMaxOutput(&relaycommon.RelayInfo{}, 1234), "unknown request types report failure so the hop event is recorded")
}

func TestPrepareBillingSampledEstimateForLargeBody(t *testing.T) {
	f := newRelayFixture(t, 100_000_000)
	f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	// > 64KB body → the sampled estimator path (the stub estimator must NOT be
	// consulted: it would be a full synchronous tokenize, §3.7).
	bigContent := string(bytes.Repeat([]byte("a"), 128<<10))
	big := `{"model":"deepseek-flash","max_tokens":10,"messages":[{"role":"user","content":"` + bigContent + `"}]}`
	c := f.newRelayContext(t, []byte(big))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	maxTokens := uint(10)
	info := newChatInfo("deepseek-flash", &maxTokens)
	// In production info.Request is parsed from the body, so CombineText
	// carries the large prompt.
	info.Request.(*dto.GeneralOpenAIRequest).Messages = []dto.Message{
		{Role: "user", Content: bigContent},
	}
	apiErr := PrepareBilling(c, info, func(*types.TokenCountMeta) (int, error) {
		return 0, errors.New("must not be called for >64KB bodies")
	})
	require.Nil(t, apiErr)
	assert.Greater(t, info.GetEstimatePromptTokens(), 0)
}

func TestPrepareBillingForcesIncludeUsage(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","stream":true,"max_tokens":100}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	maxTokens := uint(100)
	info := newChatInfo("deepseek-flash", &maxTokens)
	stream := true
	info.Request.(*dto.GeneralOpenAIRequest).Stream = &stream
	require.Nil(t, PrepareBilling(c, info, stubEstimator(1000)))

	// §3.7: 流式请求强制带 stream_options.include_usage.
	got := info.Request.(*dto.GeneralOpenAIRequest)
	require.NotNil(t, got.StreamOptions)
	assert.True(t, got.StreamOptions.IncludeUsage)

	// Non-stream requests are untouched.
	info2 := newChatInfo("deepseek-flash", &maxTokens)
	require.Nil(t, PrepareBilling(c, info2, stubEstimator(1000)))
	assert.Nil(t, info2.Request.(*dto.GeneralOpenAIRequest).StreamOptions)
}

// --- ReselectChannel / MayRetry ----------------------------------------------

func TestReselectChannelSwitchesAndRefreezes(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	l1 := f.addListing(t, 500)
	l2 := f.addListing(t, 800)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","max_tokens":100}`))

	ch, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	assert.Equal(t, int(l1.ChannelID), ch.Id) // cheapest first

	maxTokens := uint(100)
	info := newChatInfo("deepseek-flash", &maxTokens)
	require.Nil(t, PrepareBilling(c, info, stubEstimator(1000)))
	s := CurrentRelaySession(c)
	freezeAt := s.FreezeAt()
	firstFreeze := info.Billing.GetPreConsumedQuota()
	billingBefore := info.Billing

	// First attempt fails with a transient 500 → switch to the second listing.
	info.LastError = apiErrorWithStatus(500)
	ch2, apiErr := ReselectChannel(c, info, func(*gin.Context, *model.Channel, string) *types.NewAPIError { return nil })
	require.Nil(t, apiErr)
	require.NotNil(t, ch2)
	assert.Equal(t, int(l2.ChannelID), ch2.Id)

	// Hop accounting: one switch, both listings tried, freeze instant kept.
	assert.Equal(t, 1, s.hopCount)
	assert.Equal(t, []int64{l1.ID, l2.ID}, s.triedListingIDs)
	assert.Equal(t, freezeAt, s.FreezeAt())
	assert.Equal(t, 0, f.selector.Slots.InFlight(l1.ID))
	assert.Equal(t, 1, f.selector.Slots.InFlight(l2.ID))

	// The old session was refunded and a new one frozen at the new multiplier.
	assert.NotEqual(t, billingBefore, info.Billing)
	expected, qerr := PreFreezeQuote(f.db, f.cal, l2.Vendor, "deepseek-flash", 800, 1000, 100, 0, 0, freezeAt)
	require.NoError(t, qerr)
	assert.Equal(t, int(expected.TotalDebit), info.Billing.GetPreConsumedQuota())
	assert.Equal(t, 10_000_000-int(expected.TotalDebit), buyerQuota(t, int64(f.buyerID)))
	assert.Greater(t, info.Billing.GetPreConsumedQuota(), firstFreeze)

	// Real key of the second listing pinned.
	realKey2, derr := DecryptListingKey(l2.KeyCiphertext)
	require.NoError(t, derr)
	assert.Equal(t, realKey2, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
	hops := s.HopEvents()
	require.Len(t, hops, 3) // select, error, switch
	assert.Equal(t, HopEventError, hops[1].Event)
	assert.Equal(t, HopEventSwitch, hops[2].Event)
}

func TestReselectChannelInPlaceRetry(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	l := f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","max_tokens":100}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	maxTokens := uint(100)
	info := newChatInfo("deepseek-flash", &maxTokens)
	require.Nil(t, PrepareBilling(c, info, stubEstimator(1000)))
	s := CurrentRelaySession(c)

	// Only one listing and a transient failure → 原地重试 1 次 (§3.7).
	info.LastError = apiErrorWithStatus(500)
	ch, apiErr := ReselectChannel(c, info, func(*gin.Context, *model.Channel, string) *types.NewAPIError { return nil })
	require.Nil(t, apiErr)
	assert.Equal(t, int(l.ChannelID), ch.Id)
	assert.True(t, s.inPlaceRetried)
	assert.Equal(t, 0, s.hopCount)

	// A second in-place retry is refused with 503 暂无可用供应商.
	info.LastError = apiErrorWithStatus(500)
	_, apiErr = ReselectChannel(c, info, func(*gin.Context, *model.Channel, string) *types.NewAPIError { return nil })
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
}

func TestReselectChannelNonTransientNoInPlace(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","max_tokens":100}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	maxTokens := uint(100)
	info := newChatInfo("deepseek-flash", &maxTokens)
	require.Nil(t, PrepareBilling(c, info, stubEstimator(1000)))

	// A 400 is not transient → no in-place retry, straight to 503.
	info.LastError = apiErrorWithStatus(400)
	_, apiErr := ReselectChannel(c, info, func(*gin.Context, *model.Channel, string) *types.NewAPIError { return nil })
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
}

func TestMayRetryHopBudget(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	// No session → upstream decision unchanged.
	assert.True(t, MayRetry(c, apiErrorWithStatus(500)))

	f := newRelayFixture(t, 10_000_000)
	f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c = f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	s := CurrentRelaySession(c)

	assert.True(t, MayRetry(c, apiErrorWithStatus(500)))
	s.hopCount = maxListingHops
	assert.True(t, MayRetry(c, apiErrorWithStatus(500)))  // in-place still open
	assert.False(t, MayRetry(c, apiErrorWithStatus(400))) // not transient
	s.inPlaceRetried = true
	assert.False(t, MayRetry(c, apiErrorWithStatus(500))) // budget exhausted
}

// M2: the marketplace retry budget is a package constant; toggling the mutable
// upstream global must not change it.
func TestRetryBudgetIgnoresGlobalRetryTimes(t *testing.T) {
	old := common.RetryTimes
	common.RetryTimes = 0
	t.Cleanup(func() { common.RetryTimes = old })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Equal(t, 0, RetryBudget(c, common.RetryTimes), "non-marketplace requests keep the upstream value")

	c.Set(relaySessionContextKey, &RelaySession{})
	assert.Equal(t, marketplaceRetryBudget, RetryBudget(c, common.RetryTimes))
	assert.Equal(t, 3, RetryBudget(c, common.RetryTimes), "2 switches + 1 in-place retry (§3.7)")
}

// H2: a half-open probe taken by an attempt that then fails with a non-429
// error (5xx/timeout/early exit) must be handed back, or the listing would stay
// blacklisted until the process restarts.
func TestProbeHandedBackOnNon429Failure(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	l := f.addListing(t, 500)
	now := time.Now()

	// Put the breaker past its break window so it is half-open.
	f.selector.Breaker.Record429(l.ID, now.Add(-CircuitBreakDuration-time.Second))

	s := &RelaySession{
		requestID:      "req_probe_finish",
		listing:        l,
		tookProbe:      true,
		probeListingID: l.ID,
	}
	s.finishAttempt(apiErrorWithStatus(http.StatusInternalServerError))
	assert.False(t, s.tookProbe, "finishAttempt hands the probe back")
	require.True(t, f.selector.Breaker.Allow(l.ID, now),
		"the listing admits the next probe instead of staying blacklisted (H2)")

	// The all-failed path does the same.
	s2 := &RelaySession{
		requestID:          "req_probe_fail",
		listing:            l,
		tookProbe:          true,
		probeListingID:     l.ID,
		buyerUserID:        f.buyerID,
		maxMultiplierMilli: 500,
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(relaySessionContextKey, s2)
	FailRelay(c, nil)
	assert.False(t, s2.tookProbe, "FailRelay hands the probe back")
	require.True(t, f.selector.Breaker.Allow(l.ID, now), "still probeable after the failed attempt")
}

func TestProcessRelayChannelError429Breaks(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	l := f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	ProcessRelayChannelError(c, apiErrorWithStatus(http.StatusTooManyRequests))
	assert.False(t, f.selector.Breaker.Allow(l.ID, time.Now()))
	// 429 只熔断，不暂停 (§3.5).
	reloaded, gerr := GetListing(f.db, l.ID)
	require.NoError(t, gerr)
	assert.Equal(t, ListingLive, reloaded.Status)
}

func TestProcessRelayChannelErrorAutoPause(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	l := f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	// Upstream 402 Insufficient Balance → 立即自动暂停 (§3.5).
	ProcessRelayChannelError(c, types.NewOpenAIError(errors.New("Insufficient Balance"), types.ErrorCodeBadResponseStatusCode, http.StatusPaymentRequired))
	reloaded, gerr := GetListing(f.db, l.ID)
	require.NoError(t, gerr)
	assert.Equal(t, ListingPaused, reloaded.Status)

	var probes int64
	require.NoError(t, f.db.Model(&Probe{}).Where("listing_id = ? AND type = ?", l.ID, ProbeTypePatrol).Count(&probes).Error)
	assert.Equal(t, int64(1), probes)
}

func TestProcessRelayChannelErrorNoSession(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	// Must not panic and must not touch the selector.
	ProcessRelayChannelError(c, apiErrorWithStatus(429))
}

// --- StripInboundServerTools ---------------------------------------------------

func TestStripInboundServerTools(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	body := []byte(`{"model":"deepseek-flash","messages":[],"tools":[{"type":"web_search"},{"type":"function","function":{"name":"get_weather"}}]}`)
	c := f.newRelayContext(t, body)
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	StripInboundServerTools(c)

	s := CurrentRelaySession(c)
	assert.Equal(t, 1, s.StrippedTools())
	storage, gerr := common.GetBodyStorage(c)
	require.NoError(t, gerr)
	stripped, berr := storage.Bytes()
	require.NoError(t, berr)
	assert.NotContains(t, string(stripped), "web_search")
	assert.Contains(t, string(stripped), "get_weather")

	// The strip event folds into the retry chain (§3.8).
	found := false
	for _, h := range s.HopEvents() {
		if h.Event == HopEventStripTools {
			found = true
		}
	}
	assert.True(t, found)

	// Idempotent: a second pass finds nothing to strip.
	StripInboundServerTools(c)
	assert.Equal(t, 1, s.StrippedTools())
}

func TestStripInboundServerToolsNoSession(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","tools":[{"type":"web_search"}]}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	StripInboundServerTools(c) // no session → no-op
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	raw, _ := storage.Bytes()
	assert.Contains(t, string(raw), "web_search")
}

// --- RelayHTTPClient ------------------------------------------------------------

func TestRelayHTTPClientTimeout(t *testing.T) {
	client := RelayHTTPClient()
	require.NotNil(t, client)
	transport, ok := client.Transport.(*firstByteTransport)
	require.True(t, ok, "marketplace client must enforce the first-data-block timeout on the body (M1)")
	base, ok := transport.base.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, RelayHeaderTimeout, base.ResponseHeaderTimeout,
		"headers get the long timeout: a non-stream upstream may generate before sending anything (M1)")
	assert.Equal(t, RelayFirstByteTimeout, transport.timeout,
		"the 20s budget applies to the first usable data block, not the headers (M1)")
}

// slowStreamingUpstream sends response headers immediately and then only SSE
// keep-alive comments: the M1 first-data-block reader must time out and hand
// back a retryable error instead of waiting forever.
func TestFirstByteTransportTimesOutOnKeepAliveOnly(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		if fl != nil {
			fl.Flush()
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
					return
				}
				if fl != nil {
					fl.Flush()
				}
			}
		}
	}))
	defer server.Close()
	defer close(done)

	client := &http.Client{Transport: &firstByteTransport{base: http.DefaultTransport, timeout: 120 * time.Millisecond}}
	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	assert.ErrorIs(t, err, ErrRelayFirstByteTimeout)
}

// A normal stream whose first data frame arrives inside the budget must not be
// affected by the first-byte wrapper, keep-alives included.
func TestFirstByteTransportAllowsNormalStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		if fl != nil {
			fl.Flush()
		}
		_, _ = io.WriteString(w, ": keep-alive\n\n")
		if fl != nil {
			fl.Flush()
		}
		time.Sleep(40 * time.Millisecond)
		_, _ = io.WriteString(w, "data: hello\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	defer server.Close()

	client := &http.Client{Transport: &firstByteTransport{base: http.DefaultTransport, timeout: 500 * time.Millisecond}}
	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "data: hello")
}

// A non-streaming upstream may spend most of the header budget generating and
// then send the JSON body in one go; the body reader must accept it.
func TestFirstByteTransportAllowsDelayedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "  \n")
		time.Sleep(40 * time.Millisecond)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	client := &http.Client{Transport: &firstByteTransport{base: http.DefaultTransport, timeout: 500 * time.Millisecond}}
	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"ok":true`)
}

// --- Init ------------------------------------------------------------------------

func TestInitRaisesRetryTimes(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	t.Setenv("MP_ENABLED", "true")
	t.Setenv("MP_KEY_SECRET", "init-test-secret")

	oldRetry := common.RetryTimes
	common.RetryTimes = 0
	oldEnabled := enabled
	relayStateMu.Lock()
	oldRelayDB, oldSelector, oldCal := relayDB, defaultSelector, defaultCalendar
	relayStateMu.Unlock()
	t.Cleanup(func() {
		common.RetryTimes = oldRetry
		enabled = oldEnabled
		relayStateMu.Lock()
		relayDB, defaultSelector, defaultCalendar = oldRelayDB, oldSelector, oldCal
		relayStateMu.Unlock()
	})

	require.NoError(t, Init(db))
	assert.True(t, Enabled())
	assert.Equal(t, 2, common.RetryTimes) // §4.5: 只升不降
	assert.NotNil(t, DefaultSelector())
}

func TestInitDisabledKeepsUpstream(t *testing.T) {
	t.Setenv("MP_ENABLED", "false")
	oldRetry := common.RetryTimes
	common.RetryTimes = 5
	oldEnabled := enabled
	t.Cleanup(func() { common.RetryTimes = oldRetry; enabled = oldEnabled })

	require.NoError(t, Init(nil))
	assert.False(t, Enabled())
	assert.Equal(t, 5, common.RetryTimes)
}
