package marketplace

// planwindow.go — 套餐渠道额度窗口 (MODEL-CHANNEL-POOL §4).
//
// A channel with kind=plan carries a quota_window JSON such as
// {"period_seconds":18000,"reset_timezone":"Asia/Shanghai"} and an
// exhaustion_signature (a case-insensitive substring of the upstream error).
// When the upstream reports the signature, every listing of that channel is
// suspended until the next window boundary and resumed automatically.
//
// Unlike a 429 breaker trip this is not a failure: it writes no failed probe,
// touches no credit score, and it must not fall back to another listing of the
// same plan channel (all of them share the exhausted quota). Buyers get a
// retryable 503 after the error is reclassified in relayhook.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// PauseReasonPlanQuotaExhausted is the Listing.PauseReason written by
// SuspendPlanListings. ResumePlanListings only touches rows carrying it, so
// other pause reasons (patrol, report, manual) are never cleared by the
// window-reset job.
const PauseReasonPlanQuotaExhausted = "plan_quota_exhausted"

// quotaWindow is the parsed quota_window JSON. reset_timezone defaults to UTC
// when absent. Supported shapes (backwards compatible):
//
//	{"period_seconds":18000,"reset_timezone":"Asia/Shanghai"}
//	    fixed windows anchored at local midnight (legacy default)
//	{"mode":"rolling","period_seconds":18000,"reset_timezone":"Asia/Shanghai"}
//	    the next reset is simply now + period_seconds
//	{"mode":"weekly","period_seconds":604800,"weekday":1,"hour":0,"minute":0,
//	 "reset_timezone":"Asia/Shanghai"}
//	    the next occurrence of the given local weekday/time (weekday 0 = Sunday)
type quotaWindow struct {
	Mode          string `json:"mode"`
	PeriodSeconds int64  `json:"period_seconds"`
	ResetTimezone string `json:"reset_timezone"`
	// Weekday/Hour/Minute drive mode=weekly. Pointers distinguish "absent"
	// from 0 (weekday 0 is Sunday; hour/minute 0 are valid values).
	Weekday *int `json:"weekday"`
	Hour    *int `json:"hour"`
	Minute  *int `json:"minute"`
}

// parseQuotaWindow parses and validates ch.QuotaWindow. It returns ok=false
// for invalid JSON, an unknown mode, a non-positive period (fixed/rolling), an
// out-of-range weekly schedule, or an unknown reset_timezone.
func parseQuotaWindow(ch ChannelPool) (quotaWindow, bool) {
	var cfg quotaWindow
	raw := strings.TrimSpace(ch.QuotaWindow)
	if raw == "" {
		return cfg, false
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return quotaWindow{}, false
	}
	switch cfg.Mode {
	case "":
		if cfg.PeriodSeconds <= 0 {
			return quotaWindow{}, false
		}
	case "rolling":
		if cfg.PeriodSeconds <= 0 {
			return quotaWindow{}, false
		}
	case "weekly":
		// period_seconds is optional here (kept for backwards compatibility);
		// the weekday/time fields define the schedule.
		if cfg.Weekday == nil || *cfg.Weekday < 0 || *cfg.Weekday > 6 {
			return quotaWindow{}, false
		}
		if cfg.Hour != nil && (*cfg.Hour < 0 || *cfg.Hour > 23) {
			return quotaWindow{}, false
		}
		if cfg.Minute != nil && (*cfg.Minute < 0 || *cfg.Minute > 59) {
			return quotaWindow{}, false
		}
	default:
		return quotaWindow{}, false
	}
	return cfg, true
}

// PlanQuotaExhausted reports whether upstreamMessage carries ch's
// exhaustion_signature. The match is a case-insensitive substring; empty
// signatures and non-plan channels never match. Callers must not forward the
// upstream message to buyers: it may contain sensitive upstream details.
func PlanQuotaExhausted(ch ChannelPool, upstreamMessage string) bool {
	if ch.Kind != ChannelKindPlan {
		return false
	}
	sig := strings.ToLower(strings.TrimSpace(ch.ExhaustionSignature))
	if sig == "" {
		return false
	}
	return strings.Contains(strings.ToLower(upstreamMessage), sig)
}

// NextQuotaReset returns the first quota-window boundary strictly after now,
// computed in the channel's reset_timezone.
//
// mode="" (legacy): boundaries are anchored at local midnight and repeat every
// period_seconds (e.g. 00:00, 05:00, 10:00, 15:00, 20:00 for the seeded 5-hour
// Shanghai window). When the period does not divide the local day, the last
// boundary of a day is the following local midnight, so the window never drifts
// across calendar days.
//
// mode="rolling": the next boundary is now + period_seconds.
//
// mode="weekly": the next occurrence of the configured local weekday at
// hour:minute (weekday 0 = Sunday, hour/minute default to 0).
//
// An invalid configuration returns false.
func NextQuotaReset(ch ChannelPool, now time.Time) (time.Time, bool) {
	cfg, ok := parseQuotaWindow(ch)
	if !ok {
		return time.Time{}, false
	}
	loc := time.UTC
	if tz := strings.TrimSpace(cfg.ResetTimezone); tz != "" {
		var err error
		loc, err = time.LoadLocation(tz)
		if err != nil {
			return time.Time{}, false
		}
	}
	if now.IsZero() {
		now = time.Now()
	}
	switch cfg.Mode {
	case "rolling":
		return now.Add(time.Duration(cfg.PeriodSeconds) * time.Second), true
	case "weekly":
		hour, minute := 0, 0
		if cfg.Hour != nil {
			hour = *cfg.Hour
		}
		if cfg.Minute != nil {
			minute = *cfg.Minute
		}
		local := now.In(loc)
		daysAhead := (int(*cfg.Weekday) - int(local.Weekday()) + 7) % 7
		next := time.Date(local.Year(), local.Month(), local.Day()+daysAhead,
			hour, minute, 0, 0, loc)
		if !next.After(local) {
			next = next.AddDate(0, 0, 7)
		}
		return next, true
	}
	local := now.In(loc)
	year, month, day := local.Date()
	midnight := time.Date(year, month, day, 0, 0, 0, 0, loc)
	period := time.Duration(cfg.PeriodSeconds) * time.Second
	elapsed := local.Sub(midnight)
	next := midnight.Add((elapsed/period + 1) * period)
	// Do not let a period that does not divide the day carry the boundary into
	// the next calendar day: cap it at the next local midnight.
	if nextMidnight := midnight.AddDate(0, 0, 1); !next.Before(nextMidnight) {
		next = nextMidnight
	}
	return next, true
}

// SuspendPlanListing pauses exactly one plan-channel listing until its next
// quota-window boundary. It is the runtime path used when the upstream reports
// the exhaustion signature: one bad key/quota must never stop the other
// listings of the same channel. The previous lifecycle status is preserved in
// PreviousStatus so ResumePlanListings can restore it.
//
// The call is idempotent: a listing already paused for
// PauseReasonPlanQuotaExhausted is left untouched and reports changed=false.
// A non-plan channel, a missing channel and a listing that is not live/canary
// are also reported as changed=false with no error; only a real database or
// configuration failure returns an error.
func SuspendPlanListing(db *gorm.DB, listingID int64, now time.Time) (bool, error) {
	if db == nil {
		return false, errors.New("marketplace: suspend plan listing: nil database")
	}
	if listingID <= 0 {
		return false, errors.New("marketplace: suspend plan listing: invalid listing id")
	}
	if now.IsZero() {
		now = time.Now()
	}
	l, err := GetListing(db, listingID)
	if err != nil {
		return false, err
	}
	ch, ok := ChannelPoolBySlug(db, l.Vendor)
	if !ok || ch.Kind != ChannelKindPlan {
		// Not a plan channel: never pause it from this path.
		return false, nil
	}
	if l.Status == ListingPaused && l.PauseReason == PauseReasonPlanQuotaExhausted {
		return false, nil
	}
	if l.Status != ListingLive && l.Status != ListingCanary {
		return false, nil
	}
	resumeAt, ok := NextQuotaReset(ch, now)
	if !ok {
		return false, fmt.Errorf("marketplace: suspend plan listing %d: channel %q has an invalid quota_window", listingID, l.Vendor)
	}
	res := db.Model(&Listing{}).
		Where("id = ? AND status IN ?", listingID, []string{ListingLive, ListingCanary}).
		Updates(map[string]any{
			"status":          ListingPaused,
			"pause_reason":    PauseReasonPlanQuotaExhausted,
			"resume_at":       resumeAt,
			"previous_status": gorm.Expr("status"),
			"updated_at":      now,
		})
	if res.Error != nil {
		return false, fmt.Errorf("marketplace: suspend plan listing %d: %w", listingID, res.Error)
	}
	if res.RowsAffected > 0 {
		mirrorListingUpdateLogged(db, listingID)
	}
	return res.RowsAffected > 0, nil
}

// SuspendPlanListings pauses every live/canary listing of channelSlug until
// the next quota-window boundary and returns the number of rows changed. It is
// retained for explicit administrator action only: runtime quota-exhaustion
// handling calls SuspendPlanListing, so one exhausted key cannot take the whole
// channel down. The previous lifecycle status is preserved in PreviousStatus so
// the resume job can restore canary listings to canary. It is idempotent: rows
// already marked PauseReasonPlanQuotaExhausted are left untouched (their
// ResumeAt and PreviousStatus are not overwritten).
//
// The whole update is one statement, so a concurrently selected listing either
// sees its old status or the suspended one; no probe, credit score or failure
// counter is written (MODEL-CHANNEL-POOL §4: 不算故障).
func SuspendPlanListings(db *gorm.DB, channelSlug string, now time.Time) (int64, error) {
	if db == nil {
		return 0, errors.New("marketplace: suspend plan listings: nil database")
	}
	slug := strings.TrimSpace(channelSlug)
	if slug == "" {
		return 0, errors.New("marketplace: suspend plan listings: empty channel slug")
	}
	ch, ok := ChannelPoolBySlug(db, slug)
	if !ok {
		return 0, fmt.Errorf("marketplace: suspend plan listings: channel %q not found", slug)
	}
	if now.IsZero() {
		now = time.Now()
	}
	resumeAt, ok := NextQuotaReset(ch, now)
	if !ok {
		return 0, fmt.Errorf("marketplace: suspend plan listings: channel %q has an invalid quota_window", slug)
	}
	// Capture the rows this run may pause so their shadow entries can be
	// mirrored after the update commits. A candidate that lost a concurrent
	// race is mirrored in whatever state it ended in, which is still correct.
	var candidateIDs []int64
	if err := db.Model(&Listing{}).
		Where("vendor = ? AND status IN ?", slug, []string{ListingLive, ListingCanary}).
		Where("pause_reason IS NULL OR pause_reason <> ?", PauseReasonPlanQuotaExhausted).
		Pluck("id", &candidateIDs).Error; err != nil {
		return 0, fmt.Errorf("marketplace: suspend plan listings: %w", err)
	}
	res := db.Model(&Listing{}).
		Where("vendor = ? AND status IN ?", slug, []string{ListingLive, ListingCanary}).
		Where("pause_reason IS NULL OR pause_reason <> ?", PauseReasonPlanQuotaExhausted).
		Updates(map[string]any{
			"status":          ListingPaused,
			"pause_reason":    PauseReasonPlanQuotaExhausted,
			"resume_at":       resumeAt,
			"previous_status": gorm.Expr("status"),
			"updated_at":      now,
		})
	if res.Error != nil {
		return 0, fmt.Errorf("marketplace: suspend plan listings: %w", res.Error)
	}
	if res.RowsAffected > 0 {
		for _, id := range candidateIDs {
			mirrorListingUpdateLogged(db, id)
		}
	}
	return res.RowsAffected, nil
}

// ResumePlanListings restores listings whose quota-window suspension has
// expired: status=paused with PauseReasonPlanQuotaExhausted and
// ResumeAt<=now. A listing that was canary is restored to canary; every other
// previous status (including an empty one) is restored to live. The three
// plan-window fields are cleared. Listings paused for any other reason (or
// banned) are never touched.
func ResumePlanListings(db *gorm.DB, now time.Time) (int64, error) {
	if db == nil {
		return 0, errors.New("marketplace: resume plan listings: nil database")
	}
	if now.IsZero() {
		now = time.Now()
	}
	var rows []Listing
	if err := db.Where("status = ? AND pause_reason = ? AND resume_at IS NOT NULL AND resume_at <= ?",
		ListingPaused, PauseReasonPlanQuotaExhausted, now).
		Find(&rows).Error; err != nil {
		return 0, fmt.Errorf("marketplace: resume plan listings: %w", err)
	}
	var resumed int64
	for i := range rows {
		l := &rows[i]
		// The scan already requires status=paused; bans can never reach here,
		// so a window reset must not undo one.
		target := ListingLive
		if l.PreviousStatus == ListingCanary {
			target = ListingCanary
		}
		res := db.Model(&Listing{}).
			Where("id = ? AND status = ? AND pause_reason = ?", l.ID, ListingPaused, PauseReasonPlanQuotaExhausted).
			Updates(map[string]any{
				"status":          target,
				"pause_reason":    "",
				"resume_at":       nil,
				"previous_status": "",
				"updated_at":      now,
			})
		if res.Error != nil {
			return resumed, fmt.Errorf("marketplace: resume plan listing %d: %w", l.ID, res.Error)
		}
		if res.RowsAffected > 0 {
			mirrorListingUpdateLogged(db, l.ID)
		}
		resumed += res.RowsAffected
	}
	return resumed, nil
}
