package marketplace

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/relaykit/types"
)

const testPlanChannelSlug = "volcengine-ark-coding-plan"

func testPlanChannel(t *testing.T, db *gorm.DB) ChannelPool {
	t.Helper()
	ch, ok := ChannelPoolBySlug(db, testPlanChannelSlug)
	require.True(t, ok, "seeded plan channel must exist")
	require.Equal(t, ChannelKindPlan, ch.Kind)
	return ch
}

// newPlanTestListing inserts a Listing directly (CreateListing only accepts
// static vendor ids) with the given channel slug and lifecycle status.
func newPlanTestListing(t *testing.T, db *gorm.DB, vendor, status string) Listing {
	t.Helper()
	l := Listing{
		SellerID:         1,
		ChannelID:        1,
		Vendor:           vendor,
		Models:           []string{"deepseek-v4-pro"},
		MultiplierMilli:  500,
		ConcurrencyLimit: 4,
		Status:           status,
		KeyFingerprint:   uniqueTestKey("plan-fp"),
		Version:          1,
	}
	require.NoError(t, db.Create(&l).Error)
	return l
}

func TestPlanQuotaExhausted(t *testing.T) {
	plan := ChannelPool{Kind: ChannelKindPlan, ExhaustionSignature: "Coding Plan"}

	assert.True(t, PlanQuotaExhausted(plan, "Your CODING PLAN quota is exhausted"),
		"match is a case-insensitive substring")
	assert.True(t, PlanQuotaExhausted(plan, "prefix coding plan suffix"))
	assert.False(t, PlanQuotaExhausted(plan, "rate limit exceeded"))

	assert.False(t, PlanQuotaExhausted(ChannelPool{
		Kind: ChannelKindOfficial, ExhaustionSignature: "Coding Plan",
	}, "coding plan"), "non-plan channels never match")

	assert.False(t, PlanQuotaExhausted(ChannelPool{
		Kind: ChannelKindThirdParty, ExhaustionSignature: "Coding Plan",
	}, "coding plan"))

	assert.False(t, PlanQuotaExhausted(ChannelPool{
		Kind: ChannelKindPlan, ExhaustionSignature: "",
	}, "coding plan"), "empty signature never matches")

	assert.False(t, PlanQuotaExhausted(ChannelPool{
		Kind: ChannelKindPlan, ExhaustionSignature: "   ",
	}, "coding plan"), "whitespace signature never matches")

	assert.False(t, PlanQuotaExhausted(plan, ""), "empty message never matches")
}

func TestPlanWindowNextQuotaReset(t *testing.T) {
	ch := ChannelPool{
		Kind:        ChannelKindPlan,
		QuotaWindow: `{"period_seconds":18000,"reset_timezone":"Asia/Shanghai"}`,
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)

	// 12:34:56 Shanghai → the 15:00 boundary.
	now := time.Date(2026, 10, 25, 12, 34, 56, 0, loc)
	next, ok := NextQuotaReset(ch, now)
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 10, 25, 15, 0, 0, 0, loc), next)

	// Exactly on a boundary → the following one (strictly after now).
	now = time.Date(2026, 10, 25, 15, 0, 0, 0, loc)
	next, ok = NextQuotaReset(ch, now)
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 10, 25, 20, 0, 0, 0, loc), next)

	// After the last whole-period boundary of the day → next local midnight.
	now = time.Date(2026, 10, 25, 23, 30, 0, 0, loc)
	next, ok = NextQuotaReset(ch, now)
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 10, 26, 0, 0, 0, 0, loc), next)

	// Invalid JSON / non-positive period / unknown zone.
	_, ok = NextQuotaReset(ChannelPool{Kind: ChannelKindPlan, QuotaWindow: `{`}, now)
	assert.False(t, ok)
	_, ok = NextQuotaReset(ChannelPool{Kind: ChannelKindPlan, QuotaWindow: `{"period_seconds":0}`}, now)
	assert.False(t, ok)
	_, ok = NextQuotaReset(ChannelPool{
		Kind:        ChannelKindPlan,
		QuotaWindow: `{"period_seconds":18000,"reset_timezone":"Mars/Olympus"}`,
	}, now)
	assert.False(t, ok)
}

func TestPlanWindowSuspendAndResume(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	ch := testPlanChannel(t, db)

	liveOnPlan := newPlanTestListing(t, db, ch.Slug, ListingLive)
	canaryOnPlan := newPlanTestListing(t, db, ch.Slug, ListingCanary)
	otherChannel := newPlanTestListing(t, db, "deepseek", ListingLive)
	manuallyPaused := newPlanTestListing(t, db, ch.Slug, ListingPaused)
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", manuallyPaused.ID).
		Updates(map[string]any{"pause_reason": "manual hold", "resume_at": past}).Error)

	// The seeded window is 5h/Shanghai; use a fixed instant inside it.
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	now := time.Date(2026, 10, 25, 12, 34, 56, 0, loc)

	n, err := SuspendPlanListings(db, ch.Slug, now)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n, "only the channel's live/canary listings are suspended")

	gotLive, err := GetListing(db, liveOnPlan.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, gotLive.Status)
	assert.Equal(t, PauseReasonPlanQuotaExhausted, gotLive.PauseReason)
	assert.Equal(t, ListingLive, gotLive.PreviousStatus)
	require.NotNil(t, gotLive.ResumeAt)
	assert.True(t, gotLive.ResumeAt.Equal(time.Date(2026, 10, 25, 15, 0, 0, 0, loc)),
		"resume_at is the next 15:00 Shanghai boundary")
	firstResumeAt := *gotLive.ResumeAt

	gotCanary, err := GetListing(db, canaryOnPlan.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, gotCanary.Status)
	assert.Equal(t, ListingCanary, gotCanary.PreviousStatus)

	gotOther, err := GetListing(db, otherChannel.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, gotOther.Status, "another channel is untouched")
	assert.Empty(t, gotOther.PauseReason)

	gotManual, err := GetListing(db, manuallyPaused.ID)
	require.NoError(t, err)
	assert.Equal(t, "manual hold", gotManual.PauseReason, "a different pause reason is untouched")
	assert.Equal(t, ListingPaused, gotManual.Status)

	// Idempotent: a second suspension leaves ResumeAt/PreviousStatus alone.
	n, err = SuspendPlanListings(db, ch.Slug, now.Add(time.Minute))
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	gotLive, err = GetListing(db, liveOnPlan.ID)
	require.NoError(t, err)
	require.NotNil(t, gotLive.ResumeAt)
	assert.True(t, gotLive.ResumeAt.Equal(firstResumeAt), "ResumeAt is not overwritten")
	assert.Equal(t, ListingLive, gotLive.PreviousStatus)

	// Before the boundary nothing resumes.
	n, err = ResumePlanListings(db, firstResumeAt.Add(-time.Second))
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)

	// At the boundary both listings return to their previous status.
	n, err = ResumePlanListings(db, firstResumeAt)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)

	gotLive, err = GetListing(db, liveOnPlan.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, gotLive.Status)
	assert.Empty(t, gotLive.PauseReason)
	assert.Nil(t, gotLive.ResumeAt)
	assert.Empty(t, gotLive.PreviousStatus)

	gotCanary, err = GetListing(db, canaryOnPlan.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingCanary, gotCanary.Status, "canary is restored to canary")

	gotOther, err = GetListing(db, otherChannel.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, gotOther.Status)

	gotManual, err = GetListing(db, manuallyPaused.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingPaused, gotManual.Status, "other pause reasons survive the resume scan")
	assert.Equal(t, "manual hold", gotManual.PauseReason)

	// A ban is never undone by a window reset, even with the plan reason set.
	banned := newPlanTestListing(t, db, ch.Slug, ListingBanned)
	require.NoError(t, db.Model(&Listing{}).Where("id = ?", banned.ID).
		Updates(map[string]any{"pause_reason": PauseReasonPlanQuotaExhausted, "resume_at": past}).Error)
	n, err = ResumePlanListings(db, now)
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	gotBanned, err := GetListing(db, banned.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingBanned, gotBanned.Status)
}

func TestPlanQuotaRelayClassification(t *testing.T) {
	f := newRelayFixture(t, 0)
	ch := testPlanChannel(t, f.db)

	first := newPlanTestListing(t, f.db, ch.Slug, ListingLive)
	second := newPlanTestListing(t, f.db, ch.Slug, ListingLive)
	official := newPlanTestListing(t, f.db, "deepseek", ListingLive)

	s := &RelaySession{listing: first}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(relaySessionContextKey, s)

	upstreamText := "your CODING PLAN quota is exhausted for this period"
	apiErr := types.NewError(errors.New(upstreamText), types.ErrorCodeDoRequestFailed)
	ProcessRelayChannelError(c, apiErr)

	// The error is reclassified as a retryable 503 with a fixed message: the
	// upstream text must not reach the buyer.
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
	assert.NotContains(t, apiErr.Error(), upstreamText)
	assert.Contains(t, apiErr.Error(), "quota window exhausted")
	assert.True(t, s.planQuotaExhausted)

	for _, id := range []int64{first.ID, second.ID} {
		l, err := GetListing(f.db, id)
		require.NoError(t, err)
		assert.Equal(t, ListingPaused, l.Status, "the whole plan channel is suspended")
		assert.Equal(t, PauseReasonPlanQuotaExhausted, l.PauseReason)
	}

	l, err := GetListing(f.db, official.ID)
	require.NoError(t, err)
	assert.Equal(t, ListingLive, l.Status, "another channel is untouched")
}
