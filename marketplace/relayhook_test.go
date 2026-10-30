package marketplace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
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
	return newRelayFixtureWithGift(t, buyerQuota, 0)
}

// newRelayFixtureWithGift is newRelayFixture with an initial gift_quota on the
// buyer, for the funded-balance policy tests.
func newRelayFixtureWithGift(t *testing.T, buyerQuota, buyerGift int) *relayFixture {
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

	buyer := model.User{Username: "buyer", Password: "placeholder-pass", AffCode: "buyer", Quota: buyerQuota, GiftQuota: buyerGift}
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
	return f.newRelayContextAt(t, "/v1/chat/completions", body)
}

// newRelayContextAt is newRelayContext for an explicit buyer path, so a test
// can exercise the Responses/Messages endpoints.
func (f *relayFixture) newRelayContextAt(t *testing.T, path string, body []byte) *gin.Context {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
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
	require.NoError(t, f.db.Create(&KeyPref{TokenID: 77, ModelScope: ModelScopeSelected}).Error)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))

	// The model has live listings but the key never added it → 400 (§3.6).
	_, handled, status, err := BeginDistribute(c, "deepseek-flash")
	require.Error(t, err)
	assert.True(t, handled)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, err.Error(), "not added to this key")
}

func TestBeginDistributeDefaultKeyAllowsAllModels(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	f.addListing(t, 500)
	// Default token with NO KeyPref and NO KeyModel:
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
	ch, handled, status, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	assert.True(t, handled)
	assert.Equal(t, 0, status)
	assert.NotNil(t, ch)
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
		MultiplierMilli: 300,
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
		MultiplierMilli: 300,
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

	// §3.7: the same session adjusts its freeze by the difference instead of a
	// full refund + re-freeze.
	assert.Same(t, billingBefore, info.Billing)
	expected, qerr := PreFreezeQuote(f.db, f.cal, l2.Vendor, "deepseek-flash", 800, 1000, 100, 0, 0, freezeAt)
	require.NoError(t, qerr)
	assert.Equal(t, int(expected.TotalDebit), info.Billing.GetPreConsumedQuota())
	assert.Equal(t, 10_000_000-int(expected.TotalDebit), buyerQuota(t, int64(f.buyerID)))
	assert.Greater(t, info.Billing.GetPreConsumedQuota(), firstFreeze)
	assert.Equal(t, 1, s.channelRetries, "a supplier switch is a channel-level retry")
	assert.Equal(t, 0, s.keyRetries)

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

	assert.Equal(t, 1, s.keyRetries, "an in-place retry is a key-level retry")
	assert.Equal(t, 0, s.channelRetries)

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
	assert.Zero(t, base.ResponseHeaderTimeout,
		"the header timeout is enforced per request so reasoning models can wait longer")
	assert.Equal(t, defaultRelayFirstByteTimeout, transport.timeout,
		"the fallback first-byte budget applies when no request profile is present (M1)")
}

// TestRelayTimeoutForEndpoint locks the per-endpoint/model timeout policy: the
// default is 60s/20s and reasoning models are widened (§3.7).
func TestRelayTimeoutForEndpoint(t *testing.T) {
	cases := []struct {
		model      string
		wantHeader time.Duration
		wantFirst  time.Duration
	}{
		{"deepseek-flash", defaultRelayHeaderTimeout, defaultRelayFirstByteTimeout},
		{"gpt-4o-mini", defaultRelayHeaderTimeout, defaultRelayFirstByteTimeout},
		{"o3-mini", reasoningRelayHeaderTimeout, reasoningRelayFirstByteTimeout},
		{"o1", reasoningRelayHeaderTimeout, reasoningRelayFirstByteTimeout},
		{"deepseek-r1", reasoningRelayHeaderTimeout, reasoningRelayFirstByteTimeout},
		{"claude-4-thinking", reasoningRelayHeaderTimeout, reasoningRelayFirstByteTimeout},
		{"", defaultRelayHeaderTimeout, defaultRelayFirstByteTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			assert.Equal(t, tc.wantHeader, RelayHeaderTimeoutForEndpoint("", tc.model))
			assert.Equal(t, tc.wantFirst, RelayFirstByteTimeoutForEndpoint("", tc.model))
		})
	}
}

// A request profile attached to the inbound context must reach the shared
// transport so the per-model budget is used instead of the fallback.
func TestFirstByteTransportUsesRequestProfile(t *testing.T) {
	transport := &firstByteTransport{timeout: 5 * time.Second}
	ctx := context.WithValue(context.Background(), relayTimeoutProfileKey{}, relayTimeoutProfile{Model: "o3-mini"})
	header, first := transport.timeouts(ctx)
	assert.Equal(t, reasoningRelayHeaderTimeout, header)
	assert.Equal(t, reasoningRelayFirstByteTimeout, first)

	header, first = transport.timeouts(context.Background())
	assert.Equal(t, defaultRelayHeaderTimeout, header)
	assert.Equal(t, 5*time.Second, first, "an explicit fallback is kept for tests")
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

// --- pass-through body rewrite (P1) ----------------------------------------

func TestRewritePassthroughJSON(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		in    passthroughRewrite
		check func(t *testing.T, got []byte)
	}{
		{
			name: "chat rewrite covers every billable field",
			body: `{"model":"buyer-model","stream":true,"user":"buyer-1","metadata":{"user":"buyer-1"},"messages":[]}`,
			in: passthroughRewrite{UpstreamModel: "upstream-model", MaxOutputTokens: 321, Stream: true,
				ForceStoreFalse: true, UserPseudonym: "mp_pseudo"},
			check: func(t *testing.T, got []byte) {
				assert.Equal(t, "upstream-model", gjson.GetBytes(got, "model").String())
				assert.True(t, gjson.GetBytes(got, "stream_options.include_usage").Bool())
				assert.Equal(t, int64(321), gjson.GetBytes(got, "max_tokens").Int())
				assert.False(t, gjson.GetBytes(got, "store").Bool())
				assert.Equal(t, "mp_pseudo", gjson.GetBytes(got, "user").String())
				assert.Equal(t, "mp_pseudo", gjson.GetBytes(got, "metadata.user").String())
			},
		},
		{
			name: "max_completion_tokens dialect is kept",
			body: `{"model":"m","max_completion_tokens":10}`,
			in:   passthroughRewrite{MaxOutputTokens: 99, RelayFormat: types.RelayFormatOpenAI},
			check: func(t *testing.T, got []byte) {
				assert.Equal(t, int64(99), gjson.GetBytes(got, "max_completion_tokens").Int())
				assert.False(t, gjson.GetBytes(got, "max_tokens").Exists())
			},
		},
		{
			name: "responses uses max_output_tokens and no chat usage switch",
			body: `{"model":"m","stream":true}`,
			in:   passthroughRewrite{MaxOutputTokens: 55, Stream: true, RelayFormat: types.RelayFormatOpenAIResponses},
			check: func(t *testing.T, got []byte) {
				assert.Equal(t, int64(55), gjson.GetBytes(got, "max_output_tokens").Int())
				assert.False(t, gjson.GetBytes(got, "stream_options").Exists())
			},
		},
		{
			name: "claude uses max_tokens",
			body: `{"model":"m","stream":true,"max_tokens":5}`,
			in:   passthroughRewrite{MaxOutputTokens: 77, Stream: true, RelayFormat: types.RelayFormatClaude},
			check: func(t *testing.T, got []byte) {
				assert.Equal(t, int64(77), gjson.GetBytes(got, "max_tokens").Int())
				assert.False(t, gjson.GetBytes(got, "stream_options").Exists())
			},
		},
		{
			name: "absent user is not invented",
			body: `{"model":"m"}`,
			in:   passthroughRewrite{UpstreamModel: "u", UserPseudonym: "mp_x"},
			check: func(t *testing.T, got []byte) {
				assert.False(t, gjson.GetBytes(got, "user").Exists())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rewritePassthroughJSON([]byte(tt.body), tt.in)
			require.NoError(t, err)
			tt.check(t, got)
		})
	}

	_, err := rewritePassthroughJSON([]byte(`{"model":`), passthroughRewrite{UpstreamModel: "u"})
	require.Error(t, err, "a malformed raw body must be rejected, not forwarded")
}

func TestRequestUserPseudonym(t *testing.T) {
	preserveAnonSecret(t)
	SetAnonSecret("pseudonym-test-secret")
	first, err := RequestUserPseudonym(1, 2)
	require.NoError(t, err)
	again, err := RequestUserPseudonym(1, 2)
	require.NoError(t, err)
	otherSeller, err := RequestUserPseudonym(2, 2)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(first, "mp_"))
	assert.Equal(t, first, again, "the same pair is stable")
	assert.NotEqual(t, first, otherSeller, "each seller sees a different value")

	SetAnonSecret("")
	_, err = RequestUserPseudonym(1, 2)
	assert.ErrorIs(t, err, ErrNoAnonSecret)
}

func TestPrepareBillingRewritesPassthroughBody(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	require.NoError(t, f.db.Model(&ChannelModel{}).
		Where("channel_slug = ? AND model = ?", SeedVendorDeepSeek, "deepseek-flash").
		Update("upstream_model", "deepseek-flash-renamed").Error)

	body := `{"model":"deepseek-flash","stream":true,"user":"buyer-1","metadata":{"user":"buyer-1"},"messages":[{"role":"user","content":"hi"}]}`
	c := f.newRelayContext(t, []byte(body))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	// The channel setting override enables raw-body forwarding per model.
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
	ApplyRelayChannelKey(c)
	settings, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	require.True(t, ok)
	assert.True(t, settings.PassThroughBodyEnabled, "deepseek forwards raw bodies and this model has a mapping")

	info := newChatInfo("deepseek-flash", nil)
	info.RelayFormat = types.RelayFormatOpenAI
	info.IsStream = true
	require.Nil(t, PrepareBilling(c, info, stubEstimator(1000)))

	storage, gerr := common.GetBodyStorage(c)
	require.NoError(t, gerr)
	raw, berr := storage.Bytes()
	require.NoError(t, berr)
	assert.Equal(t, "deepseek-flash-renamed", gjson.GetBytes(raw, "model").String())
	assert.True(t, gjson.GetBytes(raw, "stream_options.include_usage").Bool())
	assert.Equal(t, CurrentRelaySession(c).maxOutputTokens, gjson.GetBytes(raw, "max_tokens").Int())
	pseudo := gjson.GetBytes(raw, "user").String()
	assert.True(t, strings.HasPrefix(pseudo, "mp_"))
	assert.Equal(t, pseudo, gjson.GetBytes(raw, "metadata.user").String())
	assert.NotContains(t, string(raw), "buyer-1")

	// Without an admin mapping the model forwards under its own id, so raw-body
	// pass-through stays on (seller-chosen models need no mapping row).
	require.NoError(t, f.db.Where("channel_slug = ? AND model = ?", SeedVendorDeepSeek, "deepseek-flash").
		Delete(&ChannelModel{}).Error)
	ApplyRelayChannelKey(c)
	settings, _ = common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	assert.True(t, settings.PassThroughBodyEnabled)
}

func TestPrepareBillingRejectsMalformedPassthroughBody(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)

	info := newChatInfo("deepseek-flash", nil)
	info.RelayFormat = types.RelayFormatOpenAI
	apiErr := PrepareBilling(c, info, stubEstimator(10))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Nil(t, info.Billing)
}

func TestFilterAnthropicBeta(t *testing.T) {
	f := newRelayFixture(t, 0)
	_ = f
	s := &RelaySession{}
	newCtx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		c.Request.Header.Add("anthropic-beta", "context-1m-2025-08-07")
		c.Request.Header.Add("anthropic-beta", "extended-cache-ttl-2025-04-11")
		c.Request.Header.Add("anthropic-beta", "made-up-beta-2099-01-01")
		return c
	}

	// deepseek-flash advertises a 128K window, so the 1M beta is not priced.
	c := newCtx()
	s.filterAnthropicBeta(c, "deepseek-flash")
	assert.Equal(t, "extended-cache-ttl-2025-04-11", c.Request.Header.Get("anthropic-beta"))

	// kimi-k3 advertises a 1M window, so the 1M beta is allowed.
	c = newCtx()
	s.filterAnthropicBeta(c, "kimi-k3")
	assert.Contains(t, c.Request.Header.Get("anthropic-beta"), "context-1m-2025-08-07")
	assert.Contains(t, c.Request.Header.Get("anthropic-beta"), "extended-cache-ttl-2025-04-11")
	assert.NotContains(t, c.Request.Header.Get("anthropic-beta"), "made-up")
}

func TestForceClaudeAdaptor(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeOpenAI},
	}
	ForceClaudeAdaptor(c, info)
	assert.Equal(t, constant.APITypeOpenAI, info.ApiType, "no marketplace session → untouched")

	c.Set(relaySessionContextKey, &RelaySession{})
	ForceClaudeAdaptor(c, info)
	assert.Equal(t, constant.APITypeAnthropic, info.ApiType)

	chat := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeOpenAI},
	}
	ForceClaudeAdaptor(c, chat)
	assert.Equal(t, constant.APITypeOpenAI, chat.ApiType, "only /v1/messages is forced")
}

// TestProcessRelayChannelErrorClassification locks the §3.5 vendor error-code
// matrix: only a dead key or an exhausted upstream balance pauses the listing;
// a rate limit cools the circuit down; moderation and buyer errors leave the
// seller untouched.
func TestProcessRelayChannelErrorClassification(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		errType     string
		code        any
		message     string
		wantPaused  bool
		wantBreaker bool
	}{
		{
			name: "401 invalid_api_key pauses", status: http.StatusUnauthorized,
			errType: "invalid_request_error", code: "invalid_api_key",
			message: "Incorrect API key provided", wantPaused: true,
		},
		{
			name: "anthropic authentication_error pauses", status: http.StatusUnauthorized,
			errType: "authentication_error", code: nil,
			message: "invalid x-api-key", wantPaused: true,
		},
		{
			name: "402 insufficient_quota pauses", status: http.StatusPaymentRequired,
			errType: "insufficient_quota", code: "insufficient_quota",
			message: "You exceeded your current quota", wantPaused: true,
		},
		{
			name: "deepseek insufficient_balance pauses", status: http.StatusPaymentRequired,
			errType: "unknown_error", code: "insufficient_balance",
			message: "Insufficient Balance", wantPaused: true,
		},
		{
			name: "zhipu 1113 is a balance failure", status: http.StatusBadRequest,
			errType: "unknown_error", code: 1113,
			message: "余额不足", wantPaused: true,
		},
		{
			name: "429 rate_limit_exceeded only cools down", status: http.StatusTooManyRequests,
			errType: "rate_limit_error", code: "rate_limit_exceeded",
			message: "Rate limit reached", wantBreaker: true,
		},
		{
			name: "400 content_filter does not pause", status: http.StatusBadRequest,
			errType: "invalid_request_error", code: "content_filter",
			message: "Content filter triggered",
		},
		{
			name: "403 permission_error does not pause", status: http.StatusForbidden,
			errType: "permission_error", code: nil,
			message: "permission denied for this model",
		},
		{
			name: "generic 403 does not pause", status: http.StatusForbidden,
			errType: "unknown_error", code: nil,
			message: "forbidden",
		},
		{
			name: "403 invalid_api_key still pauses", status: http.StatusForbidden,
			errType: "invalid_request_error", code: "invalid_api_key",
			message: "invalid api key", wantPaused: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRelayFixture(t, 10_000_000)
			listing := f.addListing(t, 500)
			f.addKeyModel(t, 77, "deepseek-flash", 0)
			c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
			_, handled, _, err := BeginDistribute(c, "deepseek-flash")
			require.NoError(t, err)
			require.True(t, handled)

			apiErr := types.WithOpenAIError(types.OpenAIError{
				Message: tc.message, Type: tc.errType, Code: tc.code,
			}, tc.status)
			ProcessRelayChannelError(c, apiErr)

			reloaded, gerr := GetListing(f.db, listing.ID)
			require.NoError(t, gerr)
			if tc.wantPaused {
				assert.Equal(t, ListingPaused, reloaded.Status)
			} else {
				assert.Equal(t, ListingLive, reloaded.Status, "the listing must stay in rotation")
			}
			breakerOpen := !f.selector.Breaker.Allow(listing.ID, time.Now())
			assert.Equal(t, tc.wantBreaker, breakerOpen)
		})
	}
}

// --- Responses state binding (P1) -------------------------------------------

// TestResponseBindingRecordAndResume locks the §3.7 binding contract: a
// delivered response id is recorded with its routing, a follow-up
// previous_response_id is forced onto that listing even when a cheaper supplier
// exists, and an unavailable bound key produces the fixed 503 instead of a
// silent re-route.
func TestResponseBindingRecordAndResume(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	bound := f.addListing(t, 800)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContextAt(t, "/v1/responses", []byte(`{"model":"deepseek-flash","max_output_tokens":10}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, bound.ID, CurrentRelaySession(c).CurrentListing().ID)

	// Simulate the upstream Responses stream reaching the buyer.
	_, werr := c.Writer.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_bind_1\"}}\n\n"))
	require.NoError(t, werr)
	CurrentRelaySession(c).recordResponseBinding(c, f.db)

	var row ResponseBinding
	require.NoError(t, f.db.Where("response_id = ?", "resp_bind_1").Take(&row).Error)
	assert.Equal(t, bound.ID, row.ListingID)
	assert.Equal(t, bound.ChannelID, row.ChannelID)
	assert.Equal(t, string(relayconstant.EndpointOpenAIResponses), row.Endpoint)
	assert.Equal(t, bound.KeyFingerprint, row.KeyFingerprint)
	assert.Equal(t, BackendTypeOpenAI, row.BackendType)
	assert.True(t, row.ExpiresAt.After(time.Now().Add(29*24*time.Hour)),
		"a Responses binding follows the upstream 30-day retention")

	// A cheaper listing appears, but the follow-up must stay on the bound one.
	cheaper := f.addListing(t, 400)
	c2 := f.newRelayContextAt(t, "/v1/responses", []byte(`{"model":"deepseek-flash","previous_response_id":"resp_bind_1"}`))
	ch, handled, _, err := BeginDistribute(c2, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	assert.Equal(t, int(bound.ChannelID), ch.Id)
	assert.Equal(t, bound.ID, CurrentRelaySession(c2).CurrentListing().ID)
	assert.NotEqual(t, cheaper.ID, CurrentRelaySession(c2).CurrentListing().ID)

	// The bound key becomes unusable (paused) → the fixed 503, never a switch.
	require.NoError(t, f.db.Model(&Listing{}).Where("id = ?", bound.ID).Update("status", ListingPaused).Error)
	c3 := f.newRelayContextAt(t, "/v1/responses", []byte(`{"model":"deepseek-flash","previous_response_id":"resp_bind_1"}`))
	_, handled, status, err := BeginDistribute(c3, "deepseek-flash")
	require.Error(t, err)
	assert.True(t, handled)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, err.Error(), "previous response's upstream is unavailable")

	// An unknown id cannot be pinned to any supplier either.
	c4 := f.newRelayContextAt(t, "/v1/responses", []byte(`{"model":"deepseek-flash","previous_response_id":"resp_unknown"}`))
	_, handled, status, err = BeginDistribute(c4, "deepseek-flash")
	require.Error(t, err)
	assert.True(t, handled)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, err.Error(), "previous response's upstream is unavailable")
}

// TestResponseBindingExpiresLazily covers the TTL cleanup: an expired row is
// ignored and removed on lookup, with the endpoint-specific retention window.
func TestResponseBindingExpiresLazily(t *testing.T) {
	f := newRelayFixture(t, 0)
	require.NoError(t, f.db.Create(&ResponseBinding{
		ResponseID: "resp_expired", ListingID: 1, ChannelID: 1,
		Endpoint:    string(relayconstant.EndpointOpenAIResponses),
		BackendType: BackendTypeOpenAI,
		CreatedAt:   time.Now().Add(-31 * 24 * time.Hour),
		ExpiresAt:   time.Now().Add(-time.Hour),
	}).Error)

	_, found, err := lookupResponseBinding(f.db, "resp_expired", time.Now())
	require.NoError(t, err)
	assert.False(t, found)

	var count int64
	require.NoError(t, f.db.Model(&ResponseBinding{}).Where("response_id = ?", "resp_expired").Count(&count).Error)
	assert.Zero(t, count, "the expired row is deleted on lookup")

	assert.Equal(t, 30*24*time.Hour, responseBindingTTL(relayconstant.EndpointOpenAIResponses))
	assert.Equal(t, time.Hour, responseBindingTTL(relayconstant.EndpointAnthropicMessages))
}

// TestThinkingSignatureConstraint locks the backend-family rule: a signature
// may only be replayed inside its own family, and empty legacy state stays
// unrestrained.
func TestThinkingSignatureConstraint(t *testing.T) {
	assert.False(t, thinkingSignatureCompatible(BackendTypeAnthropic, relayconstant.EndpointOpenAIChat))
	assert.True(t, thinkingSignatureCompatible(BackendTypeAnthropic, relayconstant.EndpointAnthropicMessages))
	assert.True(t, thinkingSignatureCompatible(BackendTypeOpenAI, relayconstant.EndpointOpenAIResponses))
	assert.True(t, thinkingSignatureCompatible("", relayconstant.EndpointOpenAIChat),
		"a binding/session written before the field existed stays unrestrained")

	assert.Equal(t, BackendTypeAnthropic, BackendTypeForEndpoint(relayconstant.EndpointAnthropicMessages))
	assert.Equal(t, BackendTypeOpenAI, BackendTypeForEndpoint(relayconstant.EndpointOpenAIResponses))
}

// TestRelayRequestMetadataDetectsPreviousResponseAndSignature reads both
// signals from the raw inbound body.
func TestRelayRequestMetadataDetectsPreviousResponseAndSignature(t *testing.T) {
	body := `{"model":"m","previous_response_id":"resp_1","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"sig-123"}]}]}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(body)))

	prev, sig := relayRequestMetadata(c)
	assert.Equal(t, "resp_1", prev)
	assert.True(t, sig)

	plain, _ := gin.CreateTestContext(httptest.NewRecorder())
	plain.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"m"}`)))
	prev, sig = relayRequestMetadata(plain)
	assert.Empty(t, prev)
	assert.False(t, sig)
}

// --- retry rules (P1) --------------------------------------------------------

// TestIsTransientUpstreamErrorSwitchable locks the marketplace retry matrix:
// timeouts and 5xx switch suppliers, and a 400 naming a channel/model
// incompatibility does too, while ordinary buyer 400s and 401s stop.
func TestIsTransientUpstreamErrorSwitchable(t *testing.T) {
	cases := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{"nil", nil, false},
		{"500", apiErrorWithStatus(500), true},
		{"504", apiErrorWithStatus(504), true},
		{"524", apiErrorWithStatus(524), true},
		{"408", apiErrorWithStatus(408), true},
		{"network error (no status)", types.NewError(errors.New("dial failed"), types.ErrorCodeDoRequestFailed), true},
		{"400 generic", apiErrorWithStatus(400), false},
		{"401", apiErrorWithStatus(401), false},
		{"400 channel_incompatible", types.WithOpenAIError(types.OpenAIError{
			Message: "the selected channel is not compatible", Code: "channel_incompatible",
		}, http.StatusBadRequest), true},
		{"400 model_not_supported", types.WithOpenAIError(types.OpenAIError{
			Message: "model is not supported here", Code: "model_not_supported",
		}, http.StatusBadRequest), true},
		{"400 message wording", types.WithOpenAIError(types.OpenAIError{
			Message: "channel incompatible with this request", Code: "unknown_error",
		}, http.StatusBadRequest), true},
		{"400 content filter", types.WithOpenAIError(types.OpenAIError{
			Message: "content filter triggered", Code: "content_filter",
		}, http.StatusBadRequest), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTransientUpstreamError(tc.err))
		})
	}
}

// TestReselectChannelRefusesAfterResponseCommitted is the first-byte guard: a
// response already written to the buyer must never be retried on another
// supplier (§3.7).
func TestReselectChannelRefusesAfterResponseCommitted(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	f.addListing(t, 500)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","max_tokens":10}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	maxTokens := uint(10)
	info := newChatInfo("deepseek-flash", &maxTokens)
	require.Nil(t, PrepareBilling(c, info, stubEstimator(100)))

	_, werr := c.Writer.Write([]byte("partial"))
	require.NoError(t, werr)

	info.LastError = apiErrorWithStatus(500)
	_, apiErr := ReselectChannel(c, info, func(*gin.Context, *model.Channel, string) *types.NewAPIError { return nil })
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "already started")
}

// TestBeginDistributeBuyerBalancePrecheck locks P4-A's coarse buyer-side gate:
// a zero or negative users.quota is a 402 before any listing selection, so no
// slot is acquired and no supplier lookup work is spent.
func TestBeginDistributeBuyerBalancePrecheck(t *testing.T) {
	for _, quota := range []int{0, -5} {
		t.Run(fmt.Sprintf("quota=%d", quota), func(t *testing.T) {
			f := newRelayFixture(t, quota)
			listing := f.addListing(t, 500)
			f.addKeyModel(t, 77, "deepseek-flash", 0)
			c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))

			_, handled, status, err := BeginDistribute(c, "deepseek-flash")
			require.Error(t, err)
			assert.True(t, handled)
			assert.Equal(t, http.StatusPaymentRequired, status)
			assert.ErrorIs(t, err, ErrInsufficientBalance)
			assert.False(t, IsRelayRequest(c), "no session is opened")
			assert.Equal(t, 0, f.selector.Slots.InFlight(listing.ID), "selection was never reached")
		})
	}
}

// TestBeginDistributeAffinityPinsListing covers the read side of P4-A: a
// recorded session binding is looked up before selection and the bound listing
// wins even when a cheaper supplier exists.
func TestBeginDistributeAffinityPinsListing(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	pinned := f.addListing(t, 800)
	f.addListing(t, 400)
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	require.NoError(t, RecordAffinity(f.db, f.buyerID, AffinitySourceSessionHeader, "sess-pinned",
		string(relayconstant.EndpointOpenAIChat), pinned.ID))

	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
	c.Request.Header.Set("X-Session-Id", "sess-pinned")
	ch, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	require.NotNil(t, ch)
	assert.Equal(t, int(pinned.ChannelID), ch.Id, "the bound listing serves the session")
	assert.Equal(t, pinned.ID, CurrentRelaySession(c).CurrentListing().ID)
}

// TestBeginDistributeRecordsAffinityOnSuccess covers the write side: a
// successful request records the binding for its session, and a later request
// in that session stays on the recorded listing even when a cheaper one
// appears.
func TestBeginDistributeRecordsAffinityOnSuccess(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	first := f.addListing(t, 400)
	f.addListing(t, 800)
	f.addKeyModel(t, 77, "deepseek-flash", 0)

	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
	c.Request.Header.Set("X-Session-Id", "sess-record")
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	assert.Equal(t, first.ID, CurrentRelaySession(c).CurrentListing().ID)

	// Nothing is recorded while the request is still in flight.
	binding, err := LookupAffinity(f.db, f.buyerID, AffinitySourceSessionHeader, "sess-record",
		string(relayconstant.EndpointOpenAIChat))
	require.NoError(t, err)
	assert.Nil(t, binding, "a request records its binding only after it settles")

	// Simulate a successful settle and the deferred EndRelay.
	s := CurrentRelaySession(c)
	s.mu.Lock()
	s.settled = true
	s.settledMicro = 1
	s.mu.Unlock()
	s.endRelay(c)

	binding, err = LookupAffinity(f.db, f.buyerID, AffinitySourceSessionHeader, "sess-record",
		string(relayconstant.EndpointOpenAIChat))
	require.NoError(t, err)
	require.NotNil(t, binding)
	assert.Equal(t, first.ID, binding.ListingID)

	// A cheaper listing appears, but the recorded session stays pinned.
	f.addListing(t, 300)
	c2 := f.newRelayContext(t, []byte(`{"model":"deepseek-flash"}`))
	c2.Request.Header.Set("X-Session-Id", "sess-record")
	ch, handled, _, err := BeginDistribute(c2, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	assert.Equal(t, int(first.ChannelID), ch.Id)
}

// TestReselectChannelReleasesFreezeDifference covers a hop to a cheaper
// listing: the same billing session releases only the difference, and the
// buyer's balance reflects that exact release.
func TestReselectChannelReleasesFreezeDifference(t *testing.T) {
	f := newRelayFixture(t, 10_000_000)
	f.addListing(t, 800) // initially the only supplier
	f.addKeyModel(t, 77, "deepseek-flash", 0)
	c := f.newRelayContext(t, []byte(`{"model":"deepseek-flash","max_tokens":100}`))
	_, handled, _, err := BeginDistribute(c, "deepseek-flash")
	require.NoError(t, err)
	require.True(t, handled)
	maxTokens := uint(100)
	info := newChatInfo("deepseek-flash", &maxTokens)
	require.Nil(t, PrepareBilling(c, info, stubEstimator(1000)))

	s := CurrentRelaySession(c)
	billing := info.Billing
	frozenBefore := billing.GetPreConsumedQuota()
	quotaBefore := buyerQuota(t, int64(f.buyerID))

	cheaper := f.addListing(t, 400)
	info.LastError = apiErrorWithStatus(500)
	ch, apiErr := ReselectChannel(c, info, func(*gin.Context, *model.Channel, string) *types.NewAPIError { return nil })
	require.Nil(t, apiErr)
	require.NotNil(t, ch)
	assert.Equal(t, int(cheaper.ChannelID), ch.Id)
	assert.Same(t, billing, info.Billing, "the retry chain keeps one billing session")

	expected, qerr := PreFreezeQuote(f.db, f.cal, cheaper.Vendor, "deepseek-flash", 400, 1000, 100, 0, 0, s.FreezeAt())
	require.NoError(t, qerr)
	frozenAfter := info.Billing.GetPreConsumedQuota()
	assert.Equal(t, int(expected.TotalDebit), frozenAfter)
	assert.Less(t, frozenAfter, frozenBefore, "the cheaper quote released the difference")
	assert.Equal(t, quotaBefore+(frozenBefore-frozenAfter), buyerQuota(t, int64(f.buyerID)))
}

func TestConvergeOutwardErrorMapsSellerAuthTo503(t *testing.T) {
	cases := map[int]int{
		http.StatusUnauthorized:        http.StatusServiceUnavailable,
		http.StatusForbidden:           http.StatusServiceUnavailable,
		http.StatusTooManyRequests:     http.StatusTooManyRequests,
		http.StatusBadRequest:          http.StatusBadRequest,
		http.StatusInternalServerError: http.StatusInternalServerError,
	}
	for in, want := range cases {
		e := types.NewErrorWithStatusCode(errors.New("upstream says boom"), types.ErrorCodeBadResponseStatusCode, in)
		got := ConvergeOutwardError(e)
		assert.Equal(t, want, got.StatusCode, "status %d", in)
		assert.Contains(t, got.Error(), "upstream says boom", "error text passes through")
	}
	assert.Nil(t, ConvergeOutwardError(nil))
}
