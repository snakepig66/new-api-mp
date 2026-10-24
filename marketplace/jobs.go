package marketplace

// jobs.go — 定时任务 (§5 阶段 6): T+7 转正、canary → live 升级、live 巡检、节
// 假日日历重载. StartJobs is launched from marketplace.Init by the router seam
// (main.go); it owns four ticker goroutines, each recovered and logged, and
// stops cleanly when the stop channel is closed.
//
// All jobs share the §3.5 independent verification budget: patrol traffic runs
// through the VerifyLimiter and the egress-checked client, never the buyer
// concurrency/RPM of any listing.

import (
	"context"
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// Job scheduling parameters (§5 阶段 6). Patrol interval is configurable via
// MP_PATROL_INTERVAL_MINUTES (default 30).
const (
	// jobPromoteInterval is the T+7 maturity sweep cadence (§3.9).
	jobPromoteInterval = time.Minute
	// jobCanaryInterval is the canary → live promotion scan cadence (§3.5).
	jobCanaryInterval = 5 * time.Minute
	// jobPatrolIntervalDefault is the default live-listing patrol cadence.
	jobPatrolIntervalDefault = 30 * time.Minute
	// jobCalendarInterval is the holiday-calendar reload cadence (§3.3 后台可改).
	jobCalendarInterval = time.Hour

	// canaryMaturity is how long a listing must have been in canary before it
	// is eligible for promotion (§3.5 建单满 24h).
	canaryMaturity = 24 * time.Hour
	// canaryMinSamples is the minimum number of requests a canary listing must
	// have served in the window before promotion is even considered. Below this
	// the sample is too small to trust a 98% rate (§3.5 样本 <20 单不升级).
	canaryMinSamples = 20
	// canarySuccessRateMilli is the required success rate (98%) over the
	// window, in thousandths, for a canary listing to go live.
	canarySuccessRateMilli = 980 // 98.0%
)

// StartJobs launches the four background ticker jobs. It returns immediately;
// each goroutine exits when stop is closed. Callers (the router seam in Init)
// should close stop during shutdown. Jobs are no-ops when the marketplace is
// disabled.
func StartJobs(db *gorm.DB, stop <-chan struct{}) {
	if db == nil {
		return
	}
	patrolInterval := jobPatrolIntervalDefault
	if raw := os.Getenv("MP_PATROL_INTERVAL_MINUTES"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			patrolInterval = time.Duration(n) * time.Minute
		}
	}

	go runJobLoop("promote-available", jobPromoteInterval, stop, func() {
		if _, err := PromoteAvailable(db, time.Now()); err != nil {
			common.SysError(common.LogText("marketplace: promote-available job: %s", err.Error()))
		}
	})
	go runJobLoop("canary-promote", jobCanaryInterval, stop, func() {
		if err := promoteCanaryListings(db, time.Now()); err != nil {
			common.SysError(common.LogText("marketplace: canary-promote job: %s", err.Error()))
		}
	})
	go runJobLoop("patrol-live", patrolInterval, stop, func() {
		if err := patrolLiveListings(db, time.Now()); err != nil {
			common.SysError(common.LogText("marketplace: patrol-live job: %s", err.Error()))
		}
	})
	go runJobLoop("reload-calendar", jobCalendarInterval, stop, func() {
		if _, cal := relayState(); cal != nil {
			if err := cal.Reload(db); err != nil {
				common.SysError(common.LogText("marketplace: reload-calendar job: %s", err.Error()))
			}
		}
	})
}

// runJobLoop runs fn on every tick of interval until stop is closed. Each
// invocation is wrapped in a recover so a panic in one run never kills the
// ticker or the process.
func runJobLoop(name string, interval time.Duration, stop <-chan struct{}, fn func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						common.SysError(common.LogText("marketplace: job %s panicked: %v", name, r))
					}
				}()
				fn()
			}()
		}
	}
}

// canaryStats is the success-rate sample for one canary listing over the
// promotion window.
type canaryStats struct {
	listingID int64
	total     int64
	success   int64
}

// canarySuccessRate returns the success rate in thousandths (0..1000) of a
// sample, or -1 when the sample is empty.
func canarySuccessRate(total, success int64) int64 {
	if total <= 0 {
		return -1
	}
	return success * 1000 / total
}

// promoteCanaryListings scans canary listings and promotes to live any that
// have been in canary for at least canaryMaturity and served at least
// canaryMinSamples requests over the last 24h with a success rate >= 98%
// (§3.5). The success rate is computed from mp_requests rows of that listing.
// A listing with fewer than canaryMinSamples requests in the window is not
// promoted (the sample is too small to trust), per §3.5 样本 <20 单不升级.
func promoteCanaryListings(db *gorm.DB, now time.Time) error {
	var rows []Listing
	if err := db.Where("status = ?", ListingCanary).Find(&rows).Error; err != nil {
		return err
	}
	windowStart := now.Add(-24 * time.Hour)
	for i := range rows {
		l := &rows[i]
		if now.Sub(l.CreatedAt) < canaryMaturity {
			continue
		}
		var total, success int64
		if err := db.Model(&Request{}).
			Where("listing_id = ? AND created_at >= ?", l.ID, windowStart).
			Count(&total).Error; err != nil {
			return err
		}
		if total < canaryMinSamples {
			// §3.5 样本 <20 单不升级: not enough traffic to trust the rate.
			continue
		}
		if err := db.Model(&Request{}).
			Where("listing_id = ? AND created_at >= ? AND status = ?", l.ID, windowStart, RequestSuccess).
			Count(&success).Error; err != nil {
			return err
		}
		if canarySuccessRate(total, success) >= canarySuccessRateMilli {
			if err := GoLive(db, l.ID); err != nil {
				return err
			}
			common.SysLog(common.LogText("marketplace: listing %d promoted canary -> live (%d/%d success)", l.ID, success, total))
		}
	}
	return nil
}

// patrolLiveListings runs the §3.5 巡检 over all live listings: each listing's
// key is decrypted and verified through the same VerifyListingKey flow
// (egress-checked client + VerifyLimiter), and a failure auto-pauses the
// listing. Every check writes an mp_probes row of type "patrol". Patrol
// concurrency is bounded by the VerifyLimiter.
func patrolLiveListings(db *gorm.DB, now time.Time) error {
	var rows []Listing
	if err := db.Where("status = ?", ListingLive).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	limiter := NewVerifyLimiter(VerifyMaxConcurrent, VerifyPerMinute)
	for i := range rows {
		l := &rows[i]
		if err := patrolOneListing(db, limiter, l); err != nil {
			common.SysError(common.LogText("marketplace: patrol listing %d: %s", l.ID, err.Error()))
		}
	}
	return nil
}

// patrolOneListing verifies one live listing's key and records a patrol probe;
// a failed check auto-pauses the listing (§3.5 巡检失败 → AutoPauseListing).
func patrolOneListing(db *gorm.DB, limiter *VerifyLimiter, l *Listing) error {
	apiKey, err := DecryptListingKey(l.KeyCiphertext)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := limiter.Acquire(ctx); err != nil {
		return err
	}
	vendor, ok := VendorByID(l.Vendor)
	if !ok {
		limiter.Release()
		return ErrVendorNotWhitelisted
	}
	res := VerifyListingKey(ctx, EgressHTTPClient(), vendor, apiKey)
	limiter.Release()

	detail, err := common.Marshal(map[string]any{
		"key_valid":    res.KeyValid,
		"models":       len(res.Models),
		"has_balance":  res.BalanceMicro != nil,
		"prompt_cache": res.PromptCacheSupport,
		"fail_reason":  res.FailReason,
	})
	if err != nil {
		detail = []byte("{}")
	}
	probe := Probe{
		ListingID: l.ID,
		Type:      ProbeTypePatrol,
		OK:        res.OK,
		LatencyMs: res.LatencyMs,
		Detail:    string(detail),
	}
	if err := db.Create(&probe).Error; err != nil {
		return err
	}
	if !res.OK {
		// §3.5 巡检失败 → 自动暂停. reason carries semantics only (§2.3).
		return AutoPauseListing(db, l.ID, "patrol failed: "+res.FailReason)
	}
	return nil
}
