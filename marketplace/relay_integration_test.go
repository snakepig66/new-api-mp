package marketplace_test

// relay_integration_test.go — 阶段 4 验收集成测试 (MARKETPLACE-DEV.md §5 阶段 4).
//
// Test-stack decision: a real gin engine carrying the production middleware
// chain subset that the relay path depends on (RequestId → TokenAuth →
// Distribute → controller.Relay), served over a real httptest HTTP server, and
// driven by a real http.Client. This exercises every MP-HOOK seam for real —
// distributor selection, pre-freeze, retry/re-select, 429 breaker, server-tool
// stripping, settlement — instead of re-implementing the call order by hand.
// The mock upstreams are per-listing httptest servers standing in for the
// DeepSeek vendor API; the marketplace egress client is swapped for a plain
// client through SetRelayHTTPClientForTesting because the §3.11 egress guard
// (correctly) refuses 127.0.0.1.
//
// Every case ends with the §5 three-way reconciliation: buyer users.quota, no
// residual pre-freeze, and the mp_ledger identities (signed sum 0;
// buyer_debit+tool_debit = seller_credit+platform_fee).

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/marketplace"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const (
	itModel  = "deepseek-flash"
	itVendor = "deepseek"
	// itBuyerQuota is generous enough for every pre-freeze in the suite
	// (¥10); the insufficient-balance case builds its own buyer.
	itBuyerQuota = 10_000_000
)

// relayITGlobalsOnce guards the one-time process-global setup shared by every
// integration test in this file (see newMPHarness).
var relayITGlobalsOnce sync.Once

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// mpHarness is one test's isolated world: an in-memory database holding both
// the upstream and the marketplace schemas, a real HTTP server running the
// relay chain, and a buyer account with an API token.
type mpHarness struct {
	t          *testing.T
	db         *gorm.DB
	server     *httptest.Server
	buyerID    int
	tokenID    int64
	tokenKey   string
	startQuota int
	userSeq    int
}

// newMPHarness builds the isolated environment. buyerQuota is the buyer's
// initial users.quota (== 微元, §4.5). No t.Parallel: the model package and
// the marketplace package both hold process-global handles.
//
// The database is a per-test SQLite file built by the real model.InitDB
// migrations (the same pattern as controller/responses_websocket_test.go), so
// the upstream schema — and the model package's dialect column names — are
// exactly what production runs; marketplace.Init then layers the mp_ tables
// and seed prices on top.
func newMPHarness(t *testing.T, buyerQuota int) *mpHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// Boolean process globals are set exactly once for the whole suite and
	// never restored: the relay spawns background goroutines (perf metrics,
	// quota-cache sync) that read these flags after the request — and after
	// the test — has ended, so a per-test write/restore races those reads
	// (billing_test.go's TestMain pins common.RedisEnabled the same way).
	relayITGlobalsOnce.Do(func() {
		common.MemoryCacheEnabled = false
		common.BatchUpdateEnabled = false
		common.LogConsumeEnabled = false
		common.DataExportEnabled = false
		common.IsMasterNode = true
		// main.go calls InitEnv; the relay's stream scanner needs the
		// env-backed constants it initializes (constant.StreamingTimeout et al.).
		common.InitEnv()
	})

	// --- process globals (restored on cleanup) --------------------------------
	prevDB, prevLogDB := model.DB, model.LOG_DB
	prevRetryTimes := common.RetryTimes
	prevSQLitePath := common.SQLitePath
	t.Cleanup(func() {
		model.DB = prevDB
		model.LOG_DB = prevLogDB
		common.RetryTimes = prevRetryTimes
		common.SQLitePath = prevSQLitePath
	})

	sqlDSN, isSQLite := marketplace.NewIntegrationDSN(t)
	t.Setenv("SQL_DSN", sqlDSN)
	if isSQLite {
		common.SQLitePath = filepath.Join(t.TempDir(), "mp_relay_it.db")
	}
	require.NoError(t, model.InitDB())
	db := model.DB
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, model.InitLogDB())

	t.Setenv("MP_ENABLED", "true")
	t.Setenv("MP_KEY_SECRET", "relay-integration-test-secret")
	require.NoError(t, marketplace.Init(db))

	// Selector state is process-global; reset it so breaker/slot/RPM decisions
	// of one case never leak into the next.
	sel := marketplace.DefaultSelector()
	sel.Slots = marketplace.NewSlotManager()
	sel.Breaker = marketplace.NewCircuitBreaker()
	sel.RPM = marketplace.NewRPMWindow()
	sel.CanaryFraction = 0 // fixtures use live listings only; keep the dice out

	// The production relay client refuses loopback (§3.11) — swap it for a
	// plain client that can reach the httptest mock upstreams.
	restoreClient := marketplace.SetRelayHTTPClientForTesting(&http.Client{
		Transport: &http.Transport{
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	})
	t.Cleanup(restoreClient)

	// --- HTTP side: production middleware chain subset -----------------------
	engine := gin.New()
	engine.Use(middleware.RequestId())
	v1 := engine.Group("/v1")
	v1.Use(middleware.TokenAuth())
	v1.Use(middleware.Distribute())
	v1.POST("/chat/completions", func(c *gin.Context) {
		controller.Relay(c, types.RelayFormatOpenAI)
	})
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)

	h := &mpHarness{t: t, db: db, server: server}
	h.addBuyer(buyerQuota)
	return h
}

// addBuyer creates the buyer user, an API token (unlimited token quota so the
// user-wallet pre-freeze is the only balance under test) and the §3.6
// key-model row without which BeginDistribute rejects the model.
func (h *mpHarness) addBuyer(quota int) {
	h.userSeq++
	suffix := fmt.Sprintf("%d-%d", time.Now().UnixNano(), h.userSeq)
	buyer := model.User{
		Username: "buyer-" + suffix,
		Password: "placeholder-pass",
		AffCode:  "aff-" + suffix,
		Group:    "default",
		Role:     1,
		Status:   common.UserStatusEnabled,
		Quota:    quota,
	}
	require.NoError(h.t, h.db.Create(&buyer).Error)
	token := model.Token{
		UserId:         buyer.Id,
		Key:            strings.ReplaceAll(fmt.Sprintf("itkey%s", suffix), "-", ""),
		Name:           "buyer-key",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.NoError(h.t, h.db.Create(&token).Error)
	require.NoError(h.t, h.db.Create(&marketplace.KeyModel{
		TokenID: int64(token.Id),
		Model:   itModel,
	}).Error)
	h.buyerID = buyer.Id
	h.tokenID = int64(token.Id)
	h.tokenKey = token.Key
	h.startQuota = quota
}

// addListing creates one seller user, one disabled upstream channel pointing
// at the mock's base URL, and one live marketplace listing whose encrypted key
// is sellerKey. The selector always picks the lowest multiplier first, so
// tests control the pick order through the multipliers they hand out.
func (h *mpHarness) addListing(mockBaseURL, sellerKey string, multiplierMilli int64) marketplace.Listing {
	h.userSeq++
	suffix := fmt.Sprintf("%d-%d", time.Now().UnixNano(), h.userSeq)
	seller := model.User{
		Username: "seller-" + suffix,
		Password: "placeholder-pass",
		AffCode:  "saff-" + suffix,
		Group:    "default",
		Role:     1,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(h.t, h.db.Create(&seller).Error)
	baseURL := mockBaseURL
	channel := model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Name:    "mp-ch-" + suffix,
		Key:     "placeholder-key",
		BaseURL: &baseURL,
		Status:  common.ChannelStatusManuallyDisabled, // traffic is gated by the listing, never the channel
		Models:  itModel,
		Group:   "default",
	}
	require.NoError(h.t, h.db.Create(&channel).Error)
	fingerprint, err := marketplace.ListingKeyFingerprint(sellerKey)
	require.NoError(h.t, err)
	ciphertext, err := marketplace.EncryptListingKey(sellerKey)
	require.NoError(h.t, err)
	listing := marketplace.Listing{
		SellerID:         int64(seller.Id),
		ChannelID:        int64(channel.Id),
		Vendor:           itVendor,
		Models:           []string{itModel},
		MultiplierMilli:  multiplierMilli,
		ConcurrencyLimit: 5,
		Status:           marketplace.ListingLive,
		KeyFingerprint:   fingerprint,
		KeyCiphertext:    ciphertext,
		Version:          1,
	}
	require.NoError(h.t, h.db.Create(&listing).Error)
	return listing
}

// ---------------------------------------------------------------------------
// mock upstream (stands in for the DeepSeek vendor API)
// ---------------------------------------------------------------------------

// mockUpstream is a programmable /chat/completions endpoint. Every received
// request's body and Authorization header are captured for assertions.
type mockUpstream struct {
	server *httptest.Server

	mu      sync.Mutex
	bodies  []string
	auths   []string
	handler http.HandlerFunc
}

func newMockUpstream(t *testing.T, handler http.HandlerFunc) *mockUpstream {
	t.Helper()
	m := &mockUpstream{handler: handler}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusInternalServerError)
			return
		}
		m.mu.Lock()
		m.bodies = append(m.bodies, string(body))
		m.auths = append(m.auths, r.Header.Get("Authorization"))
		m.mu.Unlock()
		m.handler(w, r)
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *mockUpstream) url() string { return m.server.URL }

func (m *mockUpstream) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.bodies)
}

func (m *mockUpstream) body(i int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bodies[i]
}

func (m *mockUpstream) auth(i int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.auths[i]
}

// --- canned responses --------------------------------------------------------

// writeUpstreamError makes the upstream answer every request with the given
// status and an OpenAI-shaped error body.
func writeUpstreamError(status int, message, typ string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":{"message":%q,"type":%q,"code":"mock_%d"}}`, message, typ, status)
	}
}

// chatUsageJSON is the usage object the mock reports; extraFields carries
// raw JSON fragments (e.g. "server_tool_use") appended inside the usage object.
type chatUsageJSON struct {
	PromptTokens     int
	CompletionTokens int
	extraFields      string
}

func (u chatUsageJSON) json() string {
	s := fmt.Sprintf(`"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d`,
		u.PromptTokens, u.CompletionTokens, u.PromptTokens+u.CompletionTokens)
	if u.extraFields != "" {
		s += "," + u.extraFields
	}
	return s
}

// writeChatSuccess answers a non-streaming chat completion.
func writeChatSuccess(content string, usage chatUsageJSON) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"chatcmpl-mock","object":"chat.completion","created":1730000000,"model":%q,`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],`+
			`"usage":{%s}}`, itModel, content, usage.json())
	}
}

// sseEvent is one SSE data frame of a streaming mock.
func sseEvent(fragment string) string {
	return fmt.Sprintf(`{"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1730000000,"model":%q,`+
		`"choices":[{"index":0,%s}]}`, itModel, fragment)
}

// writeSSE writes the frames (one per line group) and optionally the terminal
// [DONE]; a flusher pushes every frame out immediately.
func writeSSE(w http.ResponseWriter, frames []string, done bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	for _, f := range frames {
		_, _ = io.WriteString(w, "data: "+f+"\n\n")
		if fl != nil {
			fl.Flush()
		}
	}
	if done {
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}
}

// streamSuccessFrames is a well-formed stream: role chunk, one content chunk,
// finish chunk, usage frame.
func streamSuccessFrames(content string, usage chatUsageJSON) []string {
	return []string{
		sseEvent(`"delta":{"role":"assistant","content":""}`),
		sseEvent(fmt.Sprintf(`"delta":{"content":%q}`, content)),
		sseEvent(`"delta":{},"finish_reason":"stop"`),
		fmt.Sprintf(`{"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1730000000,"model":%q,"choices":[],"usage":{%s}}`, itModel, usage.json()),
	}
}

// ---------------------------------------------------------------------------
// drivers
// ---------------------------------------------------------------------------

// chatResult is what the client observed of one relayed request.
type chatResult struct {
	status    int
	requestID string
	body      []byte
}

// doChat posts one chat completion through the whole relay chain and reads the
// response to the end (for streams: until the server closes the body, which
// happens after settlement ran on the handler goroutine).
func (h *mpHarness) doChat(body string) chatResult {
	h.t.Helper()
	return h.doChatTo(h.server.URL+"/v1/chat/completions", body)
}

// doChatTo posts the standard buyer request to an arbitrary relay URL.
func (h *mpHarness) doChatTo(url string, body ...string) chatResult {
	h.t.Helper()
	payload := chatBody(false, 100)
	if len(body) > 0 {
		payload = body[0]
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
	require.NoError(h.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-"+h.tokenKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	return chatResult{status: resp.StatusCode, requestID: resp.Header.Get(common.RequestIdKey), body: data}
}

// newPanicServer runs the same production middleware chain as the harness but
// ends in a handler that panics, so the H1 deferred cleanup can be exercised
// through the middleware seam.
func (h *mpHarness) newPanicServer() *httptest.Server {
	h.t.Helper()
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(middleware.RequestId())
	v1 := engine.Group("/v1")
	v1.Use(middleware.TokenAuth())
	v1.Use(middleware.Distribute())
	v1.POST("/chat/completions", func(c *gin.Context) {
		panic("marketplace handler panic")
	})
	server := httptest.NewServer(engine)
	h.t.Cleanup(server.Close)
	return server
}

// ---------------------------------------------------------------------------
// three-way reconciliation (§5 阶段 4: 买家余额 / 冻结 / 流水)
// ---------------------------------------------------------------------------

// hopsOf parses the recorded retry chain.
func hopsOf(t *testing.T, rec marketplace.Request) []marketplace.RelayHop {
	t.Helper()
	var hops []marketplace.RelayHop
	require.NoError(t, common.UnmarshalJsonStr(rec.RetryChain, &hops))
	return hops
}

// hopEvents flattens the retry chain to its event names for assertions.
func hopEvents(hops []marketplace.RelayHop) []string {
	events := make([]string, len(hops))
	for i, h := range hops {
		events[i] = h.Event
	}
	return events
}

// recordOf loads the single mp_requests row of the request (§3.8). Settlement
// can commit after the client already holds the full response (the adaptor
// flushes the answer before PostTextConsumeQuota runs), so this polls until
// the row appears.
func (h *mpHarness) recordOf(requestID string) marketplace.Request {
	h.t.Helper()
	require.NotEmpty(h.t, requestID, "response must carry the request id header")
	deadline := time.Now().Add(10 * time.Second)
	for {
		var rec marketplace.Request
		err := h.db.Where("request_id = ?", requestID).Take(&rec).Error
		if err == nil {
			return rec
		}
		if time.Now().After(deadline) {
			var all []marketplace.Request
			_ = h.db.Find(&all).Error
			h.t.Fatalf("mp_requests row for %q never appeared: %v (table holds %d rows: %+v)",
				requestID, err, len(all), all)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ledgerOf loads the request's mp_ledger rows keyed by entry type.
func (h *mpHarness) ledgerOf(requestID string) map[string]marketplace.LedgerEntry {
	h.t.Helper()
	var rows []marketplace.LedgerEntry
	require.NoError(h.t, h.db.Where("request_id = ?", requestID).Find(&rows).Error)
	out := make(map[string]marketplace.LedgerEntry, len(rows))
	for _, r := range rows {
		out[r.EntryType] = r
	}
	return out
}

// buyerQuota reads the buyer's current users.quota from the database.
func (h *mpHarness) buyerQuota() int {
	h.t.Helper()
	quota, err := model.GetUserQuota(h.buyerID, true)
	require.NoError(h.t, err)
	return quota
}

// expectQuote recomputes the single pricing function's answer for the recorded
// request — the same Quote the display price, the debit and the record share
// (§3.7). Pricing at the record's creation instant keeps the assertion
// independent of the wall-clock tier boundary.
func (h *mpHarness) expectQuote(t *testing.T, rec marketplace.Request, inputTokens, outputTokens, searchCount int64) marketplace.QuoteResult {
	t.Helper()
	cal, err := marketplace.LoadCalendar(h.db)
	require.NoError(t, err)
	quote, err := marketplace.Quote(h.db, cal, marketplace.QuoteInput{
		Model:           itModel,
		Vendor:          itVendor,
		MultiplierMilli: rec.MultiplierMilli,
		At:              rec.CreatedAt,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		SearchCount:     searchCount,
	})
	require.NoError(t, err)
	return quote
}

// reconcileSuccess asserts the full success-path invariant set:
//   - mp_requests: one success row with the expected listing, tokens and
//     price identity, priced by the single Quote function;
//   - mp_ledger: buyer_debit (+ tool_debit) vs seller_credit + platform_fee,
//     signed sum zero, seller credit pending with a T+7 available_at;
//   - users.quota: exactly initial − Quote total (no residual pre-freeze).
func (h *mpHarness) reconcileSuccess(requestID string, listing marketplace.Listing, inputTokens, outputTokens, searchCount int64) {
	h.t.Helper()
	rec := h.recordOf(requestID)
	assert.Equal(h.t, marketplace.RequestSuccess, rec.Status)
	assert.Equal(h.t, listing.ID, rec.ListingID)
	assert.Equal(h.t, listing.SellerID, rec.SellerID)
	assert.Equal(h.t, int64(h.buyerID), rec.BuyerUserID)
	assert.Equal(h.t, itModel, rec.Model)
	assert.Equal(h.t, listing.MultiplierMilli, rec.MultiplierMilli)
	assert.Equal(h.t, inputTokens, rec.InputTokens)
	assert.Equal(h.t, outputTokens, rec.OutputTokens)
	assert.Equal(h.t, searchCount, rec.SearchCount)
	assert.False(h.t, rec.UsageEstimated)
	// The recorded tier must be the calendar's tier at the pricing instant (§3.3).
	cal, err := marketplace.LoadCalendar(h.db)
	require.NoError(h.t, err)
	assert.Equal(h.t, cal.TierAt(rec.CreatedAt), rec.Tier)
	assert.Equal(h.t, int64(1), rec.PriceVersion)

	quote := h.expectQuote(h.t, rec, inputTokens, outputTokens, searchCount)
	assert.Equal(h.t, int64(quote.TokenDebit), rec.TokenDebitMicro)
	assert.Equal(h.t, int64(quote.ToolDebit), rec.ToolDebitMicro)
	assert.Equal(h.t, int64(quote.TotalDebit), rec.TotalDebitMicro)
	fee, credit, err := marketplace.SplitPlatformFee(quote.TotalDebit)
	require.NoError(h.t, err)
	assert.Equal(h.t, int64(fee), rec.PlatformFeeMicro)
	assert.Equal(h.t, int64(credit), rec.SellerCreditMicro)
	assert.Equal(h.t, int64(quote.OfficialCost), rec.OfficialCostMicro)

	// The record row lands before PostSettlement and the wallet settle, so the
	// ledger and the buyer balance are awaited, not read once.
	wantRows := 3
	if quote.ToolDebit > 0 {
		wantRows = 4
	}
	var rows map[string]marketplace.LedgerEntry
	require.Eventually(h.t, func() bool {
		rows = h.ledgerOf(requestID)
		return len(rows) == wantRows
	}, 10*time.Second, 20*time.Millisecond, "ledger rows never settled to %d", wantRows)
	var signedSum int64
	debit := int64(quote.TokenDebit) + int64(quote.ToolDebit)
	if quote.ToolDebit > 0 {
		require.Len(h.t, rows, 4)
	} else {
		require.Len(h.t, rows, 3)
	}
	buyerDebit, ok := rows[marketplace.EntryBuyerDebit]
	require.True(h.t, ok, "buyer_debit row missing")
	assert.Equal(h.t, -int64(quote.TokenDebit), buyerDebit.AmountMicro)
	assert.Equal(h.t, marketplace.BuyerAccount(int64(h.buyerID)), buyerDebit.Account)
	assert.Equal(h.t, marketplace.LedgerAvailable, buyerDebit.Status)
	if quote.ToolDebit > 0 {
		toolDebit, ok := rows[marketplace.EntryToolDebit]
		require.True(h.t, ok, "tool_debit row missing")
		assert.Equal(h.t, -int64(quote.ToolDebit), toolDebit.AmountMicro)
		assert.Equal(h.t, marketplace.BuyerAccount(int64(h.buyerID)), toolDebit.Account)
	}
	sellerCredit, ok := rows[marketplace.EntrySellerCredit]
	require.True(h.t, ok, "seller_credit row missing")
	assert.Equal(h.t, int64(credit), sellerCredit.AmountMicro)
	assert.Equal(h.t, marketplace.SellerAccount(listing.SellerID), sellerCredit.Account)
	assert.Equal(h.t, marketplace.LedgerPending, sellerCredit.Status)
	require.NotNil(h.t, sellerCredit.AvailableAt)
	assert.WithinDuration(h.t, time.Now().Add(marketplace.DefaultMaturity), *sellerCredit.AvailableAt, time.Minute)
	platformFee, ok := rows[marketplace.EntryPlatformFee]
	require.True(h.t, ok, "platform_fee row missing")
	assert.Equal(h.t, int64(fee), platformFee.AmountMicro)
	assert.Equal(h.t, marketplace.PlatformAccount(), platformFee.Account)
	for _, r := range rows {
		signedSum += r.AmountMicro
	}
	assert.Zero(h.t, signedSum, "ledger signed sum must be zero")
	assert.Equal(h.t, debit, int64(credit)+int64(fee), "buyer_debit+tool_debit = seller_credit+platform_fee")

	// Buyer wallet: exactly the Quote total left the account; nothing stays frozen.
	require.Eventually(h.t, func() bool {
		return h.buyerQuota() == h.startQuota-int(quote.TotalDebit)
	}, 10*time.Second, 20*time.Millisecond, "buyer quota never converged to initial − debit")
}

// reconcileFailed asserts the all-failed invariant set (§3.7 失败不扣费): a
// failed record with the retry chain folded in, zero ledger rows, and the
// buyer wallet back at its initial balance (pre-freeze fully refunded).
func (h *mpHarness) reconcileFailed(requestID string) marketplace.Request {
	h.t.Helper()
	rec := h.recordOf(requestID)
	assert.Equal(h.t, marketplace.RequestFailed, rec.Status)
	assert.Zero(h.t, rec.TotalDebitMicro)
	assert.Empty(h.t, h.ledgerOf(requestID), "a failed request writes no ledger rows")
	// The record row lands before the refund; wait for the wallet to converge.
	require.Eventually(h.t, func() bool {
		return h.buyerQuota() == h.startQuota
	}, 10*time.Second, 20*time.Millisecond, "the pre-freeze must be fully refunded")
	return rec
}

// ---------------------------------------------------------------------------
// request bodies
// ---------------------------------------------------------------------------

func chatBody(stream bool, maxTokens int) string {
	return fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_tokens":%d,"stream":%t}`,
		itModel, maxTokens, stream)
}

// ---------------------------------------------------------------------------
// §5 阶段 4 cases
// ---------------------------------------------------------------------------

// Case 1: non-streaming success — buyer debit == Quote, seller credit pending
// T+7, 7% platform fee, one success record with correct pricing identity.
func TestRelayIT_NonStreamSuccess(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	usage := chatUsageJSON{PromptTokens: 1000, CompletionTokens: 500}
	upstream := newMockUpstream(t, writeChatSuccess("hello back", usage))
	sellerKey := "sk-seller-alpha"
	listing := h.addListing(upstream.url(), sellerKey, 1000)

	res := h.doChat(chatBody(false, 100))
	require.Equal(h.t, http.StatusOK, res.status, string(res.body))
	require.Equal(h.t, 1, upstream.callCount())
	// The listing's real key — not the channel placeholder — went upstream (§4.4 A).
	assert.Equal(h.t, "Bearer "+sellerKey, upstream.auth(0))

	h.reconcileSuccess(res.requestID, listing, 1000, 500, 0)
	rec := h.recordOf(res.requestID)
	assert.Equal(h.t, []string{marketplace.HopEventSelect}, hopEvents(hopsOf(t, rec)))
}

// Case 2: streaming success — SSE with a usage frame settles exactly like the
// non-stream case; PrepareBilling must have forced stream_options.include_usage.
func TestRelayIT_StreamSuccess(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	usage := chatUsageJSON{PromptTokens: 1000, CompletionTokens: 500}
	upstream := newMockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, streamSuccessFrames("hello stream", usage), true)
	})
	sellerKey := "sk-seller-stream"
	listing := h.addListing(upstream.url(), sellerKey, 1000)

	res := h.doChat(chatBody(true, 100))
	require.Equal(h.t, http.StatusOK, res.status, string(res.body))
	bodyStr := string(res.body)
	assert.Contains(h.t, bodyStr, "data: [DONE]")
	assert.Contains(h.t, bodyStr, "hello stream")
	require.Equal(h.t, 1, upstream.callCount())
	// §3.7: the relay forced stream_options.include_usage onto the upstream request.
	assert.Contains(h.t, upstream.body(0), `"include_usage":true`)

	h.reconcileSuccess(res.requestID, listing, 1000, 500, 0)
}

// Case 3: first listing 500 → second listing succeeds; the retry chain folds
// into one record; the buyer is charged once, at the SECOND listing's price.
func TestRelayIT_SwitchListingOn500(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	first := newMockUpstream(t, writeUpstreamError(http.StatusInternalServerError, "upstream exploded", "server_error"))
	usage := chatUsageJSON{PromptTokens: 800, CompletionTokens: 200}
	second := newMockUpstream(t, writeChatSuccess("second ok", usage))
	// The selector picks the lowest multiplier first: A=500 is the first hop,
	// B=1000 the fallback — the buyer must end up paying B's price.
	listingA := h.addListing(first.url(), "sk-seller-a", 500)
	listingB := h.addListing(second.url(), "sk-seller-b", 1000)

	res := h.doChat(chatBody(false, 100))
	require.Equal(h.t, http.StatusOK, res.status, string(res.body))
	assert.Equal(h.t, 1, first.callCount())
	assert.Equal(h.t, 1, second.callCount())

	h.reconcileSuccess(res.requestID, listingB, 800, 200, 0)
	rec := h.recordOf(res.requestID)
	assert.NotEqual(h.t, listingA.ID, rec.ListingID)
	events := hopEvents(hopsOf(t, rec))
	assert.Equal(h.t, []string{marketplace.HopEventSelect, marketplace.HopEventError, marketplace.HopEventSwitch}, events)

	// The first hop's pre-freeze was refunded; only B's price left the wallet.
	cal, err := marketplace.LoadCalendar(h.db)
	require.NoError(h.t, err)
	assert.Equal(h.t, cal.TierAt(rec.CreatedAt), rec.Tier, "re-priced at the ORIGINAL freeze instant (§3.7)")
}

// Case 4: first listing 429 → circuit-broken for 60s, second listing serves;
// the 429 never pauses the listing (§3.5).
func TestRelayIT_429CircuitBreaker(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	ratelimited := newMockUpstream(t, writeUpstreamError(http.StatusTooManyRequests, "rate limited", "rate_limit_error"))
	usage := chatUsageJSON{PromptTokens: 100, CompletionTokens: 50}
	healthy := newMockUpstream(t, writeChatSuccess("after breaker", usage))
	listingA := h.addListing(ratelimited.url(), "sk-seller-429", 500)
	listingB := h.addListing(healthy.url(), "sk-seller-ok", 1000)

	res := h.doChat(chatBody(false, 100))
	require.Equal(h.t, http.StatusOK, res.status, string(res.body))
	assert.Equal(h.t, 1, ratelimited.callCount())
	assert.Equal(h.t, 1, healthy.callCount())

	// Breaker: closed to A for 60s, then a half-open probe is admitted (§3.5).
	breaker := marketplace.DefaultSelector().Breaker
	assert.False(t, breaker.Allow(listingA.ID, time.Now()), "429 must circuit-break the listing for 60s")
	assert.True(t, breaker.Allow(listingA.ID, time.Now().Add(marketplace.CircuitBreakDuration+time.Second)),
		"after 60s a half-open probe must be admitted")

	// 429 is not 额度耗尽: the listing row stays live.
	var reloaded marketplace.Listing
	require.NoError(t, h.db.Where("id = ?", listingA.ID).Take(&reloaded).Error)
	assert.Equal(t, marketplace.ListingLive, reloaded.Status)

	h.reconcileSuccess(res.requestID, listingB, 100, 50, 0)
	rec := h.recordOf(res.requestID)
	events := hopEvents(hopsOf(t, rec))
	assert.Contains(h.t, events, marketplace.HopEventBreaker429)
}

// Case 5a: client cancels a streaming request mid-flight → failed, no charge,
// full pre-freeze refund (§3.7 截断的流判失败; §10-1 客户端断开不收费).
func TestRelayIT_StreamClientCancel(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	upstream := newMockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		// Produce frames steadily; the client abandons the stream after the
		// first one, so most of these writes land on a dead connection — the
		// relay must classify the end as client_gone (the relay forwards each
		// frame one frame late, so a two-frame stream would race the upstream
		// EOF instead of exercising the cancel).
		for i := 0; i < 40; i++ {
			if r.Context().Err() != nil {
				return // relay hung up after the client went away
			}
			_, _ = io.WriteString(w, "data: "+sseEvent(fmt.Sprintf(`"delta":{"content":"tok%d "}`, i))+"\n\n")
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	h.addListing(upstream.url(), "sk-seller-cancel", 1000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.server.URL+"/v1/chat/completions", strings.NewReader(chatBody(true, 100)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-"+h.tokenKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	requestID := resp.Header.Get(common.RequestIdKey)
	// Read the first SSE frame, then abandon the request.
	reader := bufio.NewReader(resp.Body)
	_, _ = reader.ReadString('\n')
	cancel()
	_ = resp.Body.Close()

	rec := h.reconcileFailed(requestID)
	assert.Contains(t, hopEvents(hopsOf(t, rec)), marketplace.HopEventStreamCut)
}

// Case 5b: the UPSTREAM cuts the SSE stream mid-flight (no usage frame, no
// [DONE]) → failed, no charge, full refund (§3.7 截断的流判失败).
func TestRelayIT_StreamUpstreamCut(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	upstream := newMockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		// Two content frames, then the connection simply ends: no usage frame,
		// no [DONE] — a truncated stream.
		writeSSE(w, []string{
			sseEvent(`"delta":{"role":"assistant","content":"partial answer"}`),
			sseEvent(`"delta":{"content":" keeps going"}`),
		}, false)
	})
	h.addListing(upstream.url(), "sk-seller-cut", 1000)

	res := h.doChat(chatBody(true, 100))
	require.Equal(t, http.StatusOK, res.status, string(res.body))

	rec := h.reconcileFailed(res.requestID)
	assert.Contains(t, hopEvents(hopsOf(t, rec)), marketplace.HopEventStreamCut)
}

// Case 6: the upstream ends normally but reports completion_tokens=0 → failed,
// no charge (§3.7 0 输出 token 判失败).
func TestRelayIT_ZeroOutputTokens(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	usage := chatUsageJSON{PromptTokens: 800, CompletionTokens: 0}
	upstream := newMockUpstream(t, writeChatSuccess("", usage))
	h.addListing(upstream.url(), "sk-seller-zero", 1000)

	res := h.doChat(chatBody(false, 100))
	require.Equal(h.t, http.StatusOK, res.status, string(res.body))
	rec := h.reconcileFailed(res.requestID)
	assert.Contains(t, hopEvents(hopsOf(t, rec)), marketplace.HopEventZeroOutput)
}

// Case 7: both listings fail 500 → ¥0, full refund, one failed record whose
// retry chain covers both listings (§3.7 全部失败).
func TestRelayIT_AllListingsFail(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	first := newMockUpstream(t, writeUpstreamError(http.StatusInternalServerError, "boom-a", "server_error"))
	second := newMockUpstream(t, writeUpstreamError(http.StatusInternalServerError, "boom-b", "server_error"))
	listingA := h.addListing(first.url(), "sk-seller-fail-a", 500)
	listingB := h.addListing(second.url(), "sk-seller-fail-b", 1000)

	res := h.doChat(chatBody(false, 100))
	// M8/§3.10: the all-failed path exposes a fixed 503, never the upstream's
	// 500 or its error text.
	assert.Equal(h.t, http.StatusServiceUnavailable, res.status, string(res.body))
	assert.Contains(h.t, string(res.body), `"code":"no_supplier_available"`)
	assert.NotContains(h.t, string(res.body), "boom")
	assert.Equal(h.t, 1, first.callCount())
	// B serves its first attempt plus the single in-place retry (§3.7).
	assert.Equal(h.t, 2, second.callCount())

	rec := h.reconcileFailed(res.requestID)
	hops := hopsOf(t, rec)
	events := hopEvents(hops)
	assert.Contains(h.t, events, marketplace.HopEventSwitch)
	assert.Contains(h.t, events, marketplace.HopEventInPlaceRetry)
	// The chain names both listings.
	seenListings := map[int64]bool{}
	for _, hop := range hops {
		if hop.ListingID != 0 {
			seenListings[hop.ListingID] = true
		}
	}
	assert.True(t, seenListings[listingA.ID], "retry chain must contain listing A")
	assert.True(t, seenListings[listingB.ID], "retry chain must contain listing B")
}

// Case 8: upstream usage carries server_tool_use.web_search_requests=2 → a
// separate tool_debit ledger row (count × official unit price × multiplier),
// same 7%/93% split, three-way reconciliation (§3.4). The per-call price is
// seeded effective 2026-11-01, so this case inserts its own earlier-effective
// price rows (the freeze instant is time.Now()).
func TestRelayIT_ServerToolCallBilling(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	// Earlier-effective per-call prices shadow the 2026-11-01 seed rows.
	effective := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, h.db.Create(&marketplace.PerCallPrice{
		Vendor: itVendor, Item: marketplace.ToolItemSearch, Version: 2,
		UnitPriceMicro: 5_000, EffectiveAt: effective,
	}).Error)
	require.NoError(t, h.db.Create(&marketplace.PerCallPrice{
		Vendor: itVendor, Item: marketplace.ToolItemFetch, Version: 2,
		UnitPriceMicro: 5_000, EffectiveAt: effective,
	}).Error)

	usage := chatUsageJSON{
		PromptTokens:     1000,
		CompletionTokens: 500,
		extraFields:      `"server_tool_use":{"web_search_requests":2}`,
	}
	upstream := newMockUpstream(t, writeChatSuccess("search-augmented answer", usage))
	listing := h.addListing(upstream.url(), "sk-seller-search", 1000)

	res := h.doChat(chatBody(false, 100))
	require.Equal(t, http.StatusOK, res.status, string(res.body))
	h.reconcileSuccess(res.requestID, listing, 1000, 500, 2)

	rec := h.recordOf(res.requestID)
	// 2 searches × 5,000 微元 × multiplier 1.0.
	assert.Equal(t, int64(10_000), rec.ToolDebitMicro)
	rows := h.ledgerOf(res.requestID)
	toolDebit, ok := rows[marketplace.EntryToolDebit]
	require.True(t, ok, "tool_debit must be a ledger row of its own (§3.4)")
	assert.Equal(t, int64(-10_000), toolDebit.AmountMicro)
}

// Case 9: the vendor's tool counts are not readable from usage → the inbound
// server tools are stripped before relaying (the mock must not see them) and
// no tool fee is charged (§3.4 读不到次数时不能估算收费).
func TestRelayIT_ServerToolsStripped(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	usage := chatUsageJSON{PromptTokens: 100, CompletionTokens: 50}
	upstream := newMockUpstream(t, writeChatSuccess("no tools needed", usage))
	listing := h.addListing(upstream.url(), "sk-seller-strip", 1000)

	body := `{"model":"` + itModel + `","messages":[{"role":"user","content":"hi"}],"max_tokens":100,` +
		`"tools":[{"type":"web_search"},{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]}`
	res := h.doChat(body)
	require.Equal(t, http.StatusOK, res.status, string(res.body))
	require.Equal(t, 1, upstream.callCount())
	upstreamBody := upstream.body(0)
	assert.NotContains(t, upstreamBody, "web_search", "server tools must be stripped before relaying (§3.4)")
	assert.Contains(t, upstreamBody, "get_weather", "ordinary function tools must survive stripping")

	h.reconcileSuccess(res.requestID, listing, 100, 50, 0)
	rec := h.recordOf(res.requestID)
	assert.Zero(t, rec.ToolDebitMicro)
	assert.Contains(t, hopEvents(hopsOf(t, rec)), marketplace.HopEventStripTools)
}

// Case 10: buyer balance below the pre-freeze → 402; the H1 cleanup releases
// the listing slot and writes exactly one failed record, the balance is
// untouched and no ledger row exists (§3.7 余额不足报错; §10-3 402).
func TestRelayIT_InsufficientBalance(t *testing.T) {
	h := newMPHarness(t, 1_000) // ¥0.001 — far below any pre-freeze
	upstream := newMockUpstream(t, writeChatSuccess("never reached", chatUsageJSON{PromptTokens: 1, CompletionTokens: 1}))
	listing := h.addListing(upstream.url(), "sk-seller-poor", 1000)

	res := h.doChat(chatBody(false, 100_000))
	assert.Equal(h.t, http.StatusPaymentRequired, res.status, string(res.body))
	assert.Contains(t, string(res.body), `"error"`, "the error must use the OpenAI error format (§3.10)")
	assert.Equal(t, 0, upstream.callCount(), "nothing may reach the upstream")

	// H1: the early exit must not leak the slot acquired during selection.
	assert.Equal(h.t, 0, marketplace.DefaultSelector().Slots.InFlight(listing.ID))
	rec := h.recordOf(res.requestID)
	assert.Equal(h.t, marketplace.RequestFailed, rec.Status)
	assert.Zero(h.t, rec.TotalDebitMicro)
	assert.Contains(h.t, hopEvents(hopsOf(h.t, rec)), marketplace.HopEventEarlyExit)

	var ledgerCount int64
	require.NoError(h.t, h.db.Model(&marketplace.LedgerEntry{}).Count(&ledgerCount).Error)
	assert.Zero(h.t, ledgerCount, "a 402 writes no ledger rows")
	assert.Equal(h.t, h.startQuota, h.buyerQuota())
}

// H1: a request rejected after selection (valid JSON, invalid payload) still
// releases the slot, refunds nothing (no pre-freeze happened) and leaves a
// failed record.
func TestRelayIT_BadRequestBodyReleasesSlot(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	upstream := newMockUpstream(t, writeChatSuccess("never reached", chatUsageJSON{PromptTokens: 1, CompletionTokens: 1}))
	listing := h.addListing(upstream.url(), "sk-seller-badbody", 1000)

	// `messages` must be an array; the distributor only inspects `model`, so the
	// listing slot is acquired before the relay body validation rejects it.
	res := h.doChat(`{"model":"deepseek-flash","messages":"not-an-array"}`)
	assert.Equal(h.t, http.StatusBadRequest, res.status, string(res.body))
	assert.Equal(h.t, 0, upstream.callCount())

	assert.Equal(h.t, 0, marketplace.DefaultSelector().Slots.InFlight(listing.ID))
	rec := h.recordOf(res.requestID)
	assert.Equal(h.t, marketplace.RequestFailed, rec.Status)
	assert.Contains(h.t, hopEvents(hopsOf(h.t, rec)), marketplace.HopEventEarlyExit)
	assert.Equal(h.t, h.startQuota, h.buyerQuota())
	var ledgerCount int64
	require.NoError(h.t, h.db.Model(&marketplace.LedgerEntry{}).Count(&ledgerCount).Error)
	assert.Zero(h.t, ledgerCount)
}

// H1: a panic in the relay handler must still release the slot, refund the
// pre-freeze (if any) and write the failed record; gin's recovery keeps the
// usual 500 response.
func TestRelayIT_HandlerPanicReleasesSlot(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	upstream := newMockUpstream(t, writeChatSuccess("never reached", chatUsageJSON{PromptTokens: 1, CompletionTokens: 1}))
	listing := h.addListing(upstream.url(), "sk-seller-panic", 1000)
	panicServer := h.newPanicServer()

	res := h.doChatTo(panicServer.URL + "/v1/chat/completions")
	assert.Equal(h.t, http.StatusInternalServerError, res.status)
	assert.Equal(h.t, 0, upstream.callCount())

	assert.Equal(h.t, 0, marketplace.DefaultSelector().Slots.InFlight(listing.ID),
		"a panicking request must not keep its concurrency slot")
	assert.Equal(h.t, h.startQuota, h.buyerQuota())
	rec := h.recordOf(res.requestID)
	assert.Equal(h.t, marketplace.RequestFailed, rec.Status)
	assert.Zero(h.t, rec.TotalDebitMicro)
}

// Case 11: a single listing with a transient failure is retried in place
// exactly once; a second failure ends at ¥0 with 暂无可用供应商 (§3.7).
func TestRelayIT_SingleListingInPlaceRetry(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	upstream := newMockUpstream(t, writeUpstreamError(http.StatusInternalServerError, "always broken", "server_error"))
	listing := h.addListing(upstream.url(), "sk-seller-lonely", 1000)

	res := h.doChat(chatBody(false, 100))
	assert.Equal(h.t, http.StatusServiceUnavailable, res.status, string(res.body))
	assert.Equal(t, 2, upstream.callCount(), "one initial attempt + one in-place retry (§3.7)")

	rec := h.reconcileFailed(res.requestID)
	hops := hopsOf(t, rec)
	events := hopEvents(hops)
	assert.Contains(t, events, marketplace.HopEventInPlaceRetry)
	assert.NotContains(t, events, marketplace.HopEventSwitch)
	for _, hop := range hops {
		if hop.ListingID != 0 {
			assert.Equal(t, listing.ID, hop.ListingID, "only the single listing may appear in the chain")
		}
	}
}

// ---------------------------------------------------------------------------
// guards against harness regressions
// ---------------------------------------------------------------------------

// The mock upstream must never receive the marketplace placeholder key and
// must never see a buyer token key (§2.3).
func TestRelayIT_NoKeyLeakageToUpstream(t *testing.T) {
	h := newMPHarness(t, itBuyerQuota)
	usage := chatUsageJSON{PromptTokens: 10, CompletionTokens: 5}
	upstream := newMockUpstream(t, writeChatSuccess("ok", usage))
	sellerKey := "sk-seller-leakcheck"
	h.addListing(upstream.url(), sellerKey, 1000)

	res := h.doChat(chatBody(false, 100))
	require.Equal(t, http.StatusOK, res.status, string(res.body))
	require.Equal(t, 1, upstream.callCount())
	auth := upstream.auth(0)
	assert.Equal(t, "Bearer "+sellerKey, auth)
	assert.NotContains(t, auth, h.tokenKey)
	assert.NotContains(t, auth, "placeholder")
}
