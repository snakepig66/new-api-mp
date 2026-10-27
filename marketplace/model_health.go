package marketplace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// model_health.go — model-level availability checks.
//
// A relay error that looks model-specific is not enough evidence: any buyer can
// send a request with an unsupported parameter. The relay therefore triggers a
// platform-owned minimal probe (max_tokens=1, no buyer parameters) at most once
// per model per 10 minutes, and only a probe failure disables the model. A
// balance/auth failure keeps the existing listing-level auto-pause.

// ProbeTypeModelCheck marks a platform-owned model availability probe.
const ProbeTypeModelCheck = "model_check"

// modelProbeGate deduplicates relay-triggered probes within one process. The
// database check in recentModelCheck covers restarts and multiple instances.
var modelProbeGate = struct {
	mu   sync.Mutex
	last map[string]time.Time
}{last: make(map[string]time.Time)}

func allowModelProbe(listingID int64, model string, now time.Time) bool {
	key := fmt.Sprintf("%d|%s", listingID, model)
	modelProbeGate.mu.Lock()
	defer modelProbeGate.mu.Unlock()
	if at, ok := modelProbeGate.last[key]; ok && now.Sub(at) < 10*time.Minute {
		return false
	}
	if len(modelProbeGate.last) > 2000 {
		for k, at := range modelProbeGate.last {
			if now.Sub(at) > time.Hour {
				delete(modelProbeGate.last, k)
			}
		}
	}
	modelProbeGate.last[key] = now
	return true
}

// modelProbeOutcome is one platform-owned model call result. UpstreamMessage
// is sanitized and bounded; it is stored as evidence for the seller, never
// logged.
type modelProbeOutcome struct {
	OK              bool
	PromptCache     bool
	StatusCode      int
	LatencyMs       int64
	FailReason      string
	UpstreamMessage string
}

var modelKeyTokenPattern = regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`)

// sanitizeUpstreamMessage extracts the vendor's error message, removes the
// API key and key-shaped tokens, strips control characters and bounds the
// result. The full upstream body is never stored or logged.
func sanitizeUpstreamMessage(raw, apiKey string) string {
	// Replace key-shaped tokens before JSON parsing so the extracted message
	// never carries the credential; stripKey then catches a short key that the
	// pattern did not match.
	raw = modelKeyTokenPattern.ReplaceAllString(raw, "sk-***")
	raw = stripKey(raw, apiKey)
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if common.Unmarshal([]byte(raw), &env) == nil {
		switch {
		case strings.TrimSpace(env.Error.Message) != "":
			raw = env.Error.Message
		case strings.TrimSpace(env.Message) != "":
			raw = env.Message
		}
	}
	raw = modelKeyTokenPattern.ReplaceAllString(raw, "sk-***")
	var b strings.Builder
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(out) > 500 {
		runes := []rune(out)
		out = string(runes[:500])
	}
	return out
}

// probeIndicatesModelUnavailable decides from a minimal probe whether the
// model itself is unavailable. Balance, auth, rate-limit and network failures
// are deliberately excluded: those are not the model's fault.
func probeIndicatesModelUnavailable(statusCode int, message string) bool {
	if statusCode == http.StatusNotFound {
		return true
	}
	lower := strings.ToLower(message)
	for _, bad := range []string{
		"insufficient balance", "insufficient quota", "quota exceeded", "rate limit",
		"too many requests", "invalid api key", "invalid_api_key", "authentication",
		"unauthorized", "余额不足", "额度", "限流", "鉴权", "认证",
	} {
		if strings.Contains(lower, bad) {
			return false
		}
	}
	for _, good := range []string{
		"model not found", "model_not_found", "model does not exist", "model_not_exist",
		"unsupported model", "model not supported", "model_not_supported",
		"no access to model", "model access denied", "model.accessdenied",
		"invalid model", "model is not available", "模型不存在", "模型未开通",
		"无权使用该模型", "该模型不可用", "不支持该模型",
	} {
		if strings.Contains(lower, good) {
			return true
		}
	}
	return false
}

// probeModelCallOutcome runs the same minimal chat request as probeModelCall
// but keeps the upstream status and a sanitized message for the auto-disable
// evidence.
func probeModelCallOutcome(ctx context.Context, client *http.Client, baseURL, model, apiKey string) modelProbeOutcome {
	return probeModelCallOutcomeWithHeaders(ctx, client, baseURL, model, apiKey, nil)
}

// probeModelCallOutcomeWithHeaders is the header-policy variant: the candidate
// headers ride on the same platform-owned minimal request the relay will use,
// so a policy is tested against the real upstream shape.
func probeModelCallOutcomeWithHeaders(ctx context.Context, client *http.Client, baseURL, model, apiKey string, headers map[string]string) modelProbeOutcome {
	var out modelProbeOutcome
	reqBody, err := common.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1,
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
	})
	if err != nil {
		out.FailReason = "无法构造模型探活请求"
		return out
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", strings.NewReader(string(reqBody)))
	if err != nil {
		out.FailReason = "上游地址格式不正确"
		return out
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	start := time.Now()
	resp, err := client.Do(req)
	out.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		if ctx.Err() != nil {
			out.FailReason = "模型探活超时"
		} else {
			out.FailReason = "模型探活失败：" + stripKey(err.Error(), apiKey)
		}
		return out
	}
	defer resp.Body.Close()
	out.StatusCode = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		out.UpstreamMessage = sanitizeUpstreamMessage(string(raw), apiKey)
		out.FailReason = modelCallFailureReason(resp.StatusCode)
		return out
	}
	var cr struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, maxVerifyBodyBytes), &cr); err != nil {
		out.FailReason = "模型返回了无法解析的响应"
		return out
	}
	if len(cr.Choices) == 0 || len(cr.Usage) == 0 {
		out.FailReason = "模型返回的响应缺少 choices 或 usage"
		return out
	}
	if strings.TrimSpace(cr.Model) == "" {
		out.FailReason = "模型返回的响应缺少 model 字段"
		return out
	}
	_, hit := cr.Usage["prompt_cache_hit_tokens"]
	_, miss := cr.Usage["prompt_cache_miss_tokens"]
	out.OK = true
	out.PromptCache = hit || miss
	return out
}

// recentModelCheck reports whether a platform probe for this model ran after
// since. The model lives in the probe detail because mp_probes has no model
// column.
func recentModelCheck(db *gorm.DB, listingID int64, model string, since time.Time) bool {
	if db == nil || listingID <= 0 || strings.TrimSpace(model) == "" {
		return false
	}
	var probes []Probe
	if err := db.Select("id", "detail").
		Where("listing_id = ? AND type = ? AND created_at >= ?", listingID, ProbeTypeModelCheck, since).
		Find(&probes).Error; err != nil {
		return false
	}
	for _, p := range probes {
		var detail struct {
			Model string `json:"model"`
		}
		if common.Unmarshal([]byte(p.Detail), &detail) == nil && detail.Model == model {
			return true
		}
	}
	return false
}

// DisableListingModel turns one model off with the evidence the console shows.
// A manual disable is left alone; a repeat auto-disable is idempotent.
func DisableListingModel(db *gorm.DB, listingID int64, model, reason, detail string, statusCode int, upstreamMessage string, now time.Time) error {
	if db == nil || listingID <= 0 || strings.TrimSpace(model) == "" {
		return errors.New("marketplace: disable model: invalid arguments")
	}
	var listing Listing
	if err := db.Select("id", "seller_id", "status", "multiplier_milli").
		Where("id = ?", listingID).Take(&listing).Error; err != nil {
		return err
	}
	if listing.Status == ListingDeleted || listing.Status == ListingBanned {
		return nil
	}
	changed := false
	err := db.Transaction(func(tx *gorm.DB) error {
		var row ListingModel
		err := tx.Where("listing_id = ? AND model = ?", listingID, model).Take(&row).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			row = ListingModel{
				ListingID:       listingID,
				Model:           model,
				MultiplierMilli: listing.MultiplierMilli,
				Enabled:         true,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		}
		if !row.Enabled && !row.AutoDisabled {
			return nil // the seller disabled it manually; keep their intent
		}
		if !row.Enabled && row.AutoDisabled {
			return nil // already auto-disabled
		}
		changed = true
		if err := tx.Model(&ListingModel{}).Where("id = ?", row.ID).
			Updates(map[string]any{
				"enabled":                   false,
				"auto_disabled":             true,
				"disabled_reason":           reason,
				"disabled_detail":           detail,
				"disabled_status_code":      statusCode,
				"disabled_upstream_message": upstreamMessage,
				"disabled_at":               now,
				"pending_multiplier_milli":  0,
				"pending_effective_at":      nil,
				"updated_at":                now,
			}).Error; err != nil {
			return err
		}
		return writeListingAuditTx(tx, listingID, 0, ListingAuditAutoDisable, map[string]any{
			"model":       model,
			"reason":      reason,
			"status_code": statusCode,
		})
	})
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	mirrorListingUpdateLogged(db, listingID)
	resetBuyerModelVisibilityCache()
	resetPublicStatsCache()
	notifySeller(db, listingID, NotificationKindListingDegraded,
		"模型已自动停用",
		fmt.Sprintf("%s 已停止接单：%s。可在「我的渠道」中检查后恢复。", model, reason),
		fmt.Sprintf("/console/listings?edit=%d", listingID))
	common.SysLog(common.LogText("marketplace: listing %d model %s auto-disabled: %s (status=%d)",
		listingID, model, reason, statusCode))
	return nil
}

// verifyModelAvailabilityAsync runs the platform probe after a relay
// model-unavailable signal. It never uses buyer-supplied parameters.
func verifyModelAvailabilityAsync(db *gorm.DB, listingID int64, model string) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(common.LogText("marketplace: model probe %d/%s panicked: %v", listingID, model, r))
		}
	}()
	if db == nil {
		return
	}
	now := time.Now()
	if recentModelCheck(db, listingID, model, now.Add(-10*time.Minute)) {
		return
	}
	var listing Listing
	if err := db.Where("id = ?", listingID).Take(&listing).Error; err != nil {
		return
	}
	if listing.Status != ListingLive && listing.Status != ListingCanary && listing.Status != ListingPaused {
		return
	}
	apiKey, err := DecryptListingKey(listing.KeyCiphertext)
	if err != nil {
		return
	}
	vendor, ok := lookupVendorForDB(db, listing.Vendor)
	if !ok {
		return
	}
	upstreamModel := model
	if cm, found := ChannelModelFor(db, listing.Vendor, model); found && strings.TrimSpace(cm.UpstreamModel) != "" {
		upstreamModel = cm.UpstreamModel
	}
	sellerVerifyMu.RLock()
	client := sellerVerifyClient
	sellerVerifyMu.RUnlock()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out := probeModelCallOutcome(ctx, client, VendorOpenAIBase(vendor), upstreamModel, apiKey)
	detail, err := common.Marshal(map[string]any{
		"model":            model,
		"ok":               out.OK,
		"status_code":      out.StatusCode,
		"upstream_message": out.UpstreamMessage,
		"fail_reason":      out.FailReason,
		"trigger":          "relay",
	})
	if err != nil {
		detail = []byte("{}")
	}
	_ = db.Create(&Probe{
		ListingID: listingID,
		Type:      ProbeTypeModelCheck,
		OK:        out.OK,
		LatencyMs: out.LatencyMs,
		Detail:    string(detail),
	}).Error
	if out.OK {
		return
	}
	if !probeIndicatesModelUnavailable(out.StatusCode, out.UpstreamMessage+" "+out.FailReason) {
		return
	}
	reason := "model_unavailable"
	switch out.StatusCode {
	case http.StatusNotFound:
		reason = "model_not_found"
	case http.StatusForbidden:
		reason = "model_access_denied"
	}
	if err := DisableListingModel(db, listingID, model, reason, out.FailReason,
		out.StatusCode, out.UpstreamMessage, now); err != nil {
		common.SysError(common.LogText("marketplace: disable model %d/%s: %s", listingID, model, err.Error()))
	}
}

// modelFailureIsSpecific reports whether a patrol model failure names the model
// itself rather than the key/account/network. An unfamiliar reason is treated
// as key-wide: the caller then keeps the existing list-level auto-pause.
func modelFailureIsSpecific(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	if lower == "" {
		return false
	}
	for _, keyWide := range []string{
		"未能读取模型列表", "余额", "鉴权", "认证", "限流", "超时", "网络",
		"无法连接", "insufficient", "rate limit", "401", "402", "429", "500", "502", "503", "504",
	} {
		if strings.Contains(lower, keyWide) {
			return false
		}
	}
	for _, modelSpecific := range []string{
		"模型不存在", "模型未开通", "不在", "无权", "未激活", "不可用", "不支持",
		"model not found", "model_not_found", "not exist", "not support", "no access", "access denied",
	} {
		if strings.Contains(lower, modelSpecific) {
			return true
		}
	}
	return false
}

// consecutiveModelFailures counts how many of the newest patrol probes failed
// for the same model. The current probe must already be written.
func consecutiveModelFailures(db *gorm.DB, listingID int64, model string) int {
	var probes []Probe
	if err := db.Select("id", "detail").
		Where("listing_id = ? AND type = ?", listingID, ProbeTypePatrol).
		Order("id DESC").Limit(4).Find(&probes).Error; err != nil {
		return 0
	}
	streak := 0
	for _, probe := range probes {
		var detail struct {
			ModelResults []ModelVerifyResult `json:"model_results"`
		}
		if common.Unmarshal([]byte(probe.Detail), &detail) != nil {
			break
		}
		found := false
		for _, mr := range detail.ModelResults {
			if mr.Model != model {
				continue
			}
			found = true
			if !mr.OK {
				streak++
			}
			break
		}
		if !found || streak == 0 {
			break
		}
	}
	return streak
}

// disableModelsFromPatrol applies the model-level patrol rule. It returns
// true when every failure was model-specific (so the caller must not pause the
// whole listing), and disables a model only after two consecutive failures.
func disableModelsFromPatrol(db *gorm.DB, listingID int64, results []ModelVerifyResult, now time.Time) (bool, error) {
	if len(results) == 0 {
		return false, nil
	}
	anyFailure := false
	for _, mr := range results {
		if mr.OK {
			continue
		}
		anyFailure = true
		if !modelFailureIsSpecific(mr.FailReason) {
			return false, nil
		}
	}
	if !anyFailure {
		return false, nil
	}
	for _, mr := range results {
		if mr.OK {
			continue
		}
		if consecutiveModelFailures(db, listingID, mr.Model) < 2 {
			continue
		}
		if err := DisableListingModel(db, listingID, mr.Model, "patrol_failed",
			mr.FailReason, 0, "", now); err != nil {
			return false, err
		}
	}
	return true, nil
}

// RestoreResult is the outcome of restoring auto-disabled models.
type RestoreResult struct {
	Restored     int                `json:"restored"`
	StillFailing []ModelChangeError `json:"still_failing"`
	CheckedAt    time.Time          `json:"checked_at"`
}

// RestoreAutoDisabledModels probes every auto-disabled model and enables only
// the ones the platform probe confirms. Manual disables are untouched.
func RestoreAutoDisabledModels(db *gorm.DB, listingID, sellerID int64, now time.Time) (RestoreResult, error) {
	var result RestoreResult
	if db == nil {
		return result, errors.New("marketplace: restore: nil database")
	}
	var listing Listing
	if err := db.Where("id = ?", listingID).Take(&listing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, ErrListingNotFound
		}
		return result, err
	}
	if sellerID > 0 && listing.SellerID != sellerID {
		return result, ErrListingNotFound
	}
	if listing.Status == ListingDeleted {
		return result, ErrListingNotFound
	}
	if listing.Status == ListingBanned {
		return result, fmt.Errorf("%w: banned listing cannot be restored", ErrInvalidTransition)
	}
	var rows []ListingModel
	if err := db.Where("listing_id = ? AND enabled = ? AND auto_disabled = ?", listingID, false, true).
		Find(&rows).Error; err != nil {
		return result, err
	}
	result.CheckedAt = now
	if len(rows) == 0 {
		result.StillFailing = []ModelChangeError{}
		return result, nil
	}
	apiKey, err := DecryptListingKey(listing.KeyCiphertext)
	if err != nil {
		return result, err
	}
	vendor, ok := lookupVendorForDB(db, listing.Vendor)
	if !ok {
		return result, ErrVendorNotWhitelisted
	}
	sellerVerifyMu.RLock()
	client := sellerVerifyClient
	sellerVerifyMu.RUnlock()
	if client == nil {
		return result, errors.New("marketplace: restore: verification client is not configured")
	}

	type probeResult struct {
		row ListingModel
		out modelProbeOutcome
	}
	results := make([]probeResult, len(rows))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i := range rows {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			upstreamModel := rows[index].Model
			if cm, found := ChannelModelFor(db, listing.Vendor, rows[index].Model); found && strings.TrimSpace(cm.UpstreamModel) != "" {
				upstreamModel = cm.UpstreamModel
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			results[index] = probeResult{
				row: rows[index],
				out: probeModelCallOutcome(ctx, client, VendorOpenAIBase(vendor), upstreamModel, apiKey),
			}
		}(i)
	}
	wg.Wait()

	result.StillFailing = []ModelChangeError{}
	for _, probe := range results {
		detail, _ := common.Marshal(map[string]any{
			"model":            probe.row.Model,
			"ok":               probe.out.OK,
			"status_code":      probe.out.StatusCode,
			"upstream_message": probe.out.UpstreamMessage,
			"fail_reason":      probe.out.FailReason,
			"trigger":          "restore",
		})
		_ = db.Create(&Probe{
			ListingID: listingID,
			Type:      ProbeTypeModelCheck,
			OK:        probe.out.OK,
			LatencyMs: probe.out.LatencyMs,
			Detail:    string(detail),
		}).Error
		if probe.out.OK {
			if err := db.Model(&ListingModel{}).Where("id = ?", probe.row.ID).
				Updates(map[string]any{
					"enabled":                   true,
					"auto_disabled":             false,
					"disabled_reason":           "",
					"disabled_detail":           "",
					"disabled_status_code":      0,
					"disabled_upstream_message": "",
					"disabled_at":               nil,
				}).Error; err != nil {
				return result, err
			}
			writeListingAuditLogged(db, listingID, sellerID, ListingAuditModelRestore, map[string]any{
				"model": probe.row.Model,
			})
			result.Restored++
			continue
		}
		if err := db.Model(&ListingModel{}).Where("id = ?", probe.row.ID).
			Updates(map[string]any{
				"disabled_reason":           "restore_failed",
				"disabled_detail":           probe.out.FailReason,
				"disabled_status_code":      probe.out.StatusCode,
				"disabled_upstream_message": probe.out.UpstreamMessage,
				"disabled_at":               now,
			}).Error; err != nil {
			return result, err
		}
		result.StillFailing = append(result.StillFailing, ModelChangeError{
			ListingID: listingID,
			Model:     probe.row.Model,
			Code:      "RESTORE_FAILED",
			Message:   probe.out.FailReason,
		})
	}
	mirrorListingUpdateLogged(db, listingID)
	resetBuyerModelVisibilityCache()
	resetPublicStatsCache()
	return result, nil
}
