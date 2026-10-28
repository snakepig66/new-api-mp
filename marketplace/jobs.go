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
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
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
	// jobPlanWindowInterval is the plan quota-window resume scan cadence
	// (MODEL-CHANNEL-POOL §4: 窗口重置后自动恢复).
	jobPlanWindowInterval = time.Minute
	// jobKeyWipeInterval is the soft-delete ciphertext wipe cadence. The
	// deletion itself keeps the key for 15 minutes so in-flight requests can
	// finish; this job only clears rows whose key_wipe_at has passed.
	jobKeyWipeInterval = time.Minute
	// jobOpsMetricsInterval is the derived-metrics aggregation cadence (P5):
	// one run per hour bucket, matching opsMetricBucket.
	jobOpsMetricsInterval = time.Hour

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
	// MODEL-CHANNEL-POOL §4: restore plan-channel listings whose quota window
	// has reset. The job only touches plan_quota_exhausted pauses; other pause
	// reasons and bans are left alone.
	go runJobLoop("plan-window-resume", jobPlanWindowInterval, stop, func() {
		n, err := ResumePlanListings(db, time.Now())
		if err != nil {
			common.SysError(common.LogText("marketplace: plan-window-resume job: %s", err.Error()))
			return
		}
		if n > 0 {
			common.SysLog(common.LogText("marketplace: plan-window-resume job: resumed %d listings", n))
		}
	})
	// Soft-deleted listings keep their ciphertext for a short grace period so
	// an in-flight request can still decrypt it. This job wipes it afterwards.
	go runJobLoop("key-wipe", jobKeyWipeInterval, stop, func() {
		n, err := WipeDeletedListingKeys(db, time.Now())
		if err != nil {
			common.SysError(common.LogText("marketplace: key-wipe job: %s", err.Error()))
			return
		}
		if n > 0 {
			common.SysLog(common.LogText("marketplace: key-wipe job: wiped %d deleted keys", n))
		}
	})
	// A staged price increase becomes effective after its one-hour delay. The
	// read paths already honour a due pending value; this job persists it and
	// writes the audit row.
	go runJobLoop("model-price-promote", jobKeyWipeInterval, stop, func() {
		n, err := PromotePendingModelMultipliers(db, time.Now())
		if err != nil {
			common.SysError(common.LogText("marketplace: model-price-promote job: %s", err.Error()))
			return
		}
		if n > 0 {
			common.SysLog(common.LogText("marketplace: model-price-promote job: applied %d staged prices", n))
		}
	})
	// P5 derived metrics: aggregate the previous hour's request-derived metrics
	// into mp_ops_metrics and snapshot the process-local slot counters. Both
	// writes replace their bucket, so a repeated run is idempotent.
	go runJobLoop("ops-metrics", jobOpsMetricsInterval, stop, func() {
		now := time.Now()
		if err := AggregateOpsMetrics(db, now.Add(-jobOpsMetricsInterval)); err != nil {
			common.SysError(common.LogText("marketplace: ops-metrics aggregate job: %s", err.Error()))
		}
		if stats, ok := slotStats(); ok {
			if err := SnapshotSlotMetrics(db, now, stats); err != nil {
				common.SysError(common.LogText("marketplace: ops-metrics slot snapshot job: %s", err.Error()))
			}
		}
	})
}

// slotStats returns the default selector's slot counters. The second return
// is false while the process has no selector (the marketplace is disabled), so
// the metrics job publishes nothing rather than a zero snapshot.
func slotStats() (map[string]int64, bool) {
	relayStateMu.Lock()
	selector := defaultSelector
	relayStateMu.Unlock()
	if selector == nil || selector.Slots == nil {
		return nil, false
	}
	return selector.Slots.Stats(), true
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
// When a platform key exists for the model's origin vendor, it also runs the
// tokenizer fingerprint against the official endpoint: two consecutive *real*
// mismatches pause the listing. Three strikes still only pause the listing and
// raise a SysError alert — the channel-model qualification is never banned
// automatically; a ban requires an operator decision in the admin console
// (MODEL-CHANNEL-POOL §4, P0).
func patrolOneListing(db *gorm.DB, limiter *VerifyLimiter, l *Listing) error {
	// Verification/patrol traffic uses the egress-verify client: dial-time
	// public-IP validation without requiring the relay whitelist (a listing is
	// checked before it is approved into that whitelist). Redirects are never
	// followed.
	return patrolOneListingWithClient(db, limiter, EgressVerifyHTTPClient(), l)
}

// patrolOneListingWithClient is patrolOneListing with the egress client
// injected so tests can point every probe (verification and fingerprint) at a
// mock upstream.
func patrolOneListingWithClient(db *gorm.DB, limiter *VerifyLimiter, client *http.Client, l *Listing) error {
	apiKey, err := DecryptListingKey(l.KeyCiphertext)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := limiter.Acquire(ctx); err != nil {
		return err
	}
	vendor, ok := lookupVendorForDB(db, l.Vendor)
	if !ok {
		limiter.Release()
		return ErrVendorNotWhitelisted
	}
	// Probe every listed model, not just the first: a channel is only as
	// reliable as its worst listed model, and the old first-model probe hid
	// failures on everything after it. The /models key check runs once; each
	// model then gets its own real max_tokens=1 call.
	base := VendorOpenAIBase(vendor)
	keyRes := VerifyListingKeyWithModel(ctx, client, vendor, apiKey, "")
	passed := 0
	modelResults := make([]ModelVerifyResult, 0, len(l.Models))
	for _, m := range l.Models {
		mr := ModelVerifyResult{Model: m, UpstreamModel: ListingUpstreamModel(db, l.ID, l.Vendor, m)}
		switch {
		case !keyRes.KeyValid || len(keyRes.Models) == 0:
			mr.FailReason = "Key 未能读取模型列表，未进行调用检测"
		case !keyExposesModel(keyRes.Models, mr.UpstreamModel):
			mr.FailReason = "该模型不在这个 Key 的可用模型列表中"
		default:
			mr.OK, mr.PromptCacheSupport, mr.LatencyMs, mr.FailReason =
				probeModelCall(ctx, client, base, mr.UpstreamModel, apiKey)
		}
		if mr.OK {
			passed++
		}
		modelResults = append(modelResults, mr)
	}
	res := keyRes
	res.ModelResults = modelResults
	res.OK = keyRes.KeyValid && passed > 0
	if !res.OK && res.FailReason == "" {
		for _, mr := range modelResults {
			if !mr.OK && mr.FailReason != "" {
				res.FailReason = mr.FailReason
				break
			}
		}
		if res.FailReason == "" {
			res.FailReason = "no listed model could be called"
		}
	}
	// Fail-closed fingerprint comparison: every callable listed model must
	// match the platform's official endpoint. A comparison that did not
	// complete (no platform key, official error, missing usage, tiny prompt)
	// is not health evidence, so the probe is never OK and the listing is
	// paused; a completed mismatch is written as not-OK and counts toward the
	// mismatch streak below.
	fingerprintConfigured := PlatformFingerprintConfigured()
	fingerprints := make([]FingerprintModelResult, 0, len(modelResults))
	fingerprintIncomplete := false
	fingerprintMismatch := false
	fingerprintReason := ""
	if res.OK && fingerprintConfigured {
		for _, mr := range modelResults {
			if !mr.OK {
				continue
			}
			fr := FingerprintModelResult{Model: mr.Model, UpstreamModel: mr.UpstreamModel}
			model, found := ModelPoolByName(db, mr.Model)
			officialKey, hasKey := "", false
			if found {
				officialKey, hasKey = PlatformKeyForVendor(model.OriginVendor)
			}
			switch {
			case !found:
				fr.FailReason = "model is not in the platform pool; no official reference"
			case !hasKey:
				fr.FailReason = "no platform key for origin vendor " + model.OriginVendor
			default:
				officialVendor, foundVendor := lookupVendorForDB(db, model.OriginVendor)
				if !foundVendor {
					fr.FailReason = "origin vendor " + model.OriginVendor + " is not whitelisted"
					break
				}
				officialUpstream := model.Name
				if cm, foundCM := ChannelModelFor(db, model.OriginVendor, model.Name); foundCM {
					officialUpstream = cm.UpstreamModel
				}
				prompt := model.VerifyPrompt
				if strings.TrimSpace(prompt) == "" {
					prompt = defaultFingerprintPrompt
				}
				fp := CompareFingerprint(ctx, client,
					officialVendor, officialKey, officialUpstream,
					vendor, apiKey, mr.UpstreamModel, prompt)
				fr.Checked = fp.Checked
				fr.Matched = fp.Passed()
				fr.Ratio = fp.Ratio
				fr.FailReason = fp.FailReason
			}
			if !fr.Checked {
				fingerprintIncomplete = true
				if fingerprintReason == "" {
					reason := fr.FailReason
					if reason == "" {
						reason = "comparison did not complete"
					}
					fingerprintReason = mr.Model + "：" + reason
				}
			} else if !fr.Matched {
				fingerprintMismatch = true
				if fingerprintReason == "" {
					reason := fr.FailReason
					if reason == "" {
						reason = "fingerprint mismatch"
					}
					fingerprintReason = mr.Model + "：" + reason
				}
			}
			fingerprints = append(fingerprints, fr)
		}
	}
	limiter.Release()

	fingerprintChecked := len(fingerprints) > 0
	fingerprintOK := len(fingerprints) > 0
	for _, fr := range fingerprints {
		fingerprintChecked = fingerprintChecked && fr.Checked
		fingerprintOK = fingerprintOK && fr.Matched
	}
	probeOK := res.OK
	if fingerprintIncomplete || fingerprintMismatch {
		probeOK = false
	}
	detailMap := map[string]any{
		"key_valid":            res.KeyValid,
		"models":               len(res.Models),
		"has_balance":          res.BalanceMicro != nil,
		"prompt_cache":         res.PromptCacheSupport,
		"fail_reason":          res.FailReason,
		"fingerprint_required": fingerprintConfigured,
		"fingerprint_checked":  fingerprintChecked,
		"model_results":        modelResults,
	}
	if len(fingerprints) > 0 {
		detailMap["fingerprints"] = fingerprints
	}
	if fingerprintChecked {
		detailMap["fingerprint_ok"] = fingerprintOK
	}
	if fingerprintReason != "" {
		detailMap["fingerprint_reason"] = fingerprintReason
	}
	detail, err := common.Marshal(detailMap)
	if err != nil {
		detail = []byte("{}")
	}
	probe := Probe{
		ListingID: l.ID,
		Type:      ProbeTypePatrol,
		OK:        probeOK,
		LatencyMs: res.LatencyMs,
		Detail:    string(detail),
	}
	if err := db.Create(&probe).Error; err != nil {
		return err
	}
	if !res.OK {
		// A model-specific failure disables that model (after two consecutive
		// patrols) and keeps the listing live for its other models. A key-wide
		// failure keeps the existing listing-level auto-pause.
		if res.KeyValid && len(res.Models) > 0 {
			handled, derr := disableModelsFromPatrol(db, l.ID, modelResults, time.Now())
			if derr != nil {
				return derr
			}
			if handled {
				return nil
			}
		}
		// §3.5 巡检失败 → 自动暂停. reason carries semantics only (§2.3).
		return AutoPauseListing(db, l.ID, "patrol failed: "+res.FailReason)
	}
	if fingerprintIncomplete {
		// Without a completed official comparison the listing cannot be
		// certified healthy (fail-closed); a transient official failure thus
		// pauses it until the next verification succeeds.
		return AutoPauseListing(db, l.ID, "patrol failed: fingerprint comparison incomplete: "+fingerprintReason)
	}
	if fingerprintMismatch {
		consecutive, err := consecutiveFingerprintMismatches(db, l.ID)
		if err != nil {
			return err
		}
		switch {
		case consecutive >= 3:
			// Third real mismatch: keep the listing paused (it usually was
			// already paused by strike two, so ErrInvalidTransition is fine)
			// and alert an operator. Never ban the ChannelModel here.
			if err := AutoPauseListing(db, l.ID, "fingerprint mismatch (3 consecutive); manual review required"); err != nil &&
				!errors.Is(err, ErrInvalidTransition) {
				return err
			}
			common.SysError(common.LogText(
				"marketplace: fingerprint mismatch (3 consecutive): listing %d channel %s (%s); listing kept paused, channel-model NOT banned (manual review required)",
				l.ID, l.Vendor, fingerprintReason))
		case consecutive >= 2:
			if err := AutoPauseListing(db, l.ID, "fingerprint mismatch (2 consecutive)"); err != nil &&
				!errors.Is(err, ErrInvalidTransition) {
				return err
			}
		}
	}
	return nil
}

// fingerprintProbeDetail is the subset of a patrol probe's JSON detail needed
// to tell a real comparison from an unchecked/failed one.
type fingerprintProbeDetail struct {
	Checked *bool `json:"fingerprint_checked"`
	OK      *bool `json:"fingerprint_ok"`
}

// consecutiveFingerprintMismatches counts, walking backwards from the newest
// patrol probe, how many *checked* comparisons failed. Records with
// fingerprint_checked=false (billing errors, 429, network problems, tiny
// prompts, auto-pause annotations) are skipped without breaking the streak;
// the first checked comparison that passed ends the streak. At most
// fingerprintStreakLookback rows are examined.
func consecutiveFingerprintMismatches(db *gorm.DB, listingID int64) (int, error) {
	const fingerprintStreakLookback = 5
	var recent []Probe
	if err := db.Where("listing_id = ? AND type = ?", listingID, ProbeTypePatrol).
		Order("id DESC").Limit(fingerprintStreakLookback).Find(&recent).Error; err != nil {
		return 0, err
	}
	consecutive := 0
	for i := range recent {
		var d fingerprintProbeDetail
		if err := json.Unmarshal([]byte(recent[i].Detail), &d); err != nil {
			continue
		}
		if d.Checked == nil || !*d.Checked {
			// Not a real comparison: neither count it nor break the streak.
			continue
		}
		if d.OK == nil {
			// A checked row without a verdict is malformed; it must not
			// masquerade as either outcome.
			continue
		}
		if *d.OK {
			break
		}
		consecutive++
	}
	return consecutive, nil
}
