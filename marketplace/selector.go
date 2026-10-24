package marketplace

// selector.go — 选渠道 (§3.5 过滤与打分, §4.3 请求链路步骤 2): per-listing
// concurrency slots, 429 circuit breaking, RPM sliding windows, daily spend
// caps, 可接单时段, and the scored pick of one listing for one buyer
// request.
//
// SlotManager, CircuitBreaker and RPMWindow are process-local: a multi-node
// deployment must move this state to Redis (or equivalent shared storage) —
// a second-version item (§8). Until then the limits they enforce are per
// process, like the rest of new-api's in-process caches.

import (
	"cmp"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// SlotTimeout is how long a held concurrency slot lives without an explicit
// release (§3.5: 并发槽在请求结束或超时 10 分钟释放).
const SlotTimeout = 10 * time.Minute

// SlotManager enforces each listing's 并发上限 (§3.5: required, default 5).
// A full listing is skipped, never queued (满并发跳过这家，不排队).
type SlotManager struct {
	mu sync.Mutex
	// slots maps a listing id to the acquire times of its in-flight slots;
	// the slice stays chronologically ordered because appends are
	// chronological.
	slots map[int64][]time.Time
}

// NewSlotManager builds an empty SlotManager.
func NewSlotManager() *SlotManager {
	return &SlotManager{slots: make(map[int64][]time.Time)}
}

// Acquire takes one slot for listingID and reports whether it succeeded.
// Slots acquired at least SlotTimeout ago are swept first (§3.5 超时释放).
// A limit <= 0 falls back to the §3.5 default of 5.
func (m *SlotManager) Acquire(listingID int64, limit int, now time.Time) bool {
	if limit <= 0 {
		limit = defaultListingConcurrency
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked(listingID, now)
	if len(m.slots[listingID]) >= limit {
		return false
	}
	m.slots[listingID] = append(m.slots[listingID], now)
	return true
}

// Release returns one slot — the oldest outstanding one. Releasing a listing
// with nothing held is a no-op: the slot may already have timed out, and a
// caller releasing after SlotTimeout must not go negative.
func (m *SlotManager) Release(listingID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	times := m.slots[listingID]
	if len(times) == 0 {
		return
	}
	times = times[1:]
	if len(times) == 0 {
		delete(m.slots, listingID)
		return
	}
	m.slots[listingID] = times
}

// InFlight reports the number of slots currently held; it does not sweep
// expired slots (only Acquire does).
func (m *SlotManager) InFlight(listingID int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.slots[listingID])
}

// sweepLocked drops slots acquired at least SlotTimeout before now. The
// caller holds m.mu.
func (m *SlotManager) sweepLocked(listingID int64, now time.Time) {
	times := m.slots[listingID]
	cutoff := now.Add(-SlotTimeout)
	first := 0
	for first < len(times) && !times[first].After(cutoff) {
		first++
	}
	if first == 0 {
		return
	}
	remaining := slices.Clone(times[first:])
	if len(remaining) == 0 {
		delete(m.slots, listingID)
		return
	}
	m.slots[listingID] = remaining
}

// CircuitBreakDuration is how long one listing stays broken after a 429
// (§3.5: 429 只熔断这条挂单 60 秒并换家; a 429 never counts as 额度耗尽).
const CircuitBreakDuration = 60 * time.Second

// circuitState is one listing's breaker state; absence from the map means
// closed (healthy).
type circuitState struct {
	openedAt      time.Time
	halfOpen      bool
	probeInFlight bool
}

// CircuitBreaker implements the §3.5 per-listing 429 rule: a 429 breaks the
// listing for CircuitBreakDuration; past that it admits exactly one
// concurrent half-open probe (之后逐步放量) — the probe's success closes
// the circuit, another 429 re-breaks it for a full duration.
type CircuitBreaker struct {
	mu     sync.Mutex
	states map[int64]*circuitState
}

// NewCircuitBreaker builds an empty (all-closed) CircuitBreaker.
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{states: make(map[int64]*circuitState)}
}

// Record429 breaks the listing for CircuitBreakDuration from now. Called on
// an already open or half-open circuit it restarts the full break (半开中再
// 429 → 重新熔断 60s).
func (b *CircuitBreaker) Record429(listingID int64, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.states[listingID] = &circuitState{openedAt: now}
}

// RecordSuccess closes the circuit (半开成功后恢复). It is also how a
// half-open probe slot is handed back when the probe request ended without
// a 429.
func (b *CircuitBreaker) RecordSuccess(listingID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.states, listingID)
}

// Allow reports whether a request may go to the listing now: false while
// broken; past the break it admits exactly one concurrent half-open probe
// and holds that probe slot until Record429 or RecordSuccess resolves it.
func (b *CircuitBreaker) Allow(listingID int64, now time.Time) bool {
	allowed, _ := b.allow(listingID, now)
	return allowed
}

// allow is Allow plus whether the call consumed the half-open probe slot.
// Select needs the second return to hand the probe back (releaseProbe) for
// candidates it does not pick.
func (b *CircuitBreaker) allow(listingID int64, now time.Time) (allowed, tookProbe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.states[listingID]
	if !ok {
		return true, false
	}
	if !st.halfOpen {
		if now.Sub(st.openedAt) < CircuitBreakDuration {
			return false, false
		}
		st.halfOpen = true
	}
	if st.probeInFlight {
		return false, false
	}
	st.probeInFlight = true
	return true, true
}

// releaseProbe returns a half-open probe slot taken by allow without
// resolving the circuit; the listing stays half-open so the next attempt
// becomes the probe.
func (b *CircuitBreaker) releaseProbe(listingID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if st, ok := b.states[listingID]; ok && st.halfOpen {
		st.probeInFlight = false
	}
}

// ReleaseProbe is the exported hand-back used by the relay session when a
// half-open probe attempt ends with a non-429 failure (5xx, timeout, 401, an
// early-exit cleanup, ...). Without it probeInFlight would stay true forever
// and the listing would never receive traffic again (CODE-REVIEW H2). The
// circuit stays half-open, so the next request is admitted as the probe.
func (b *CircuitBreaker) ReleaseProbe(listingID int64) {
	b.releaseProbe(listingID)
}

// available reports whether listingID may be attempted now without mutating
// the state machine or consuming the half-open probe slot. Select ranks with
// it and only calls allow on the candidate it commits to.
func (b *CircuitBreaker) available(listingID int64, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.states[listingID]
	if !ok {
		return true
	}
	if !st.halfOpen {
		return now.Sub(st.openedAt) >= CircuitBreakDuration
	}
	return !st.probeInFlight
}

// rpmWindow is the sliding window RPMWindow limits over (§3.5 RPM 上限).
const rpmWindow = time.Minute

// RPMWindow enforces each listing's RPM limit as a 60-second sliding window
// of attempt timestamps; rpm <= 0 means unlimited.
type RPMWindow struct {
	mu   sync.Mutex
	hits map[int64][]time.Time
}

// NewRPMWindow builds an empty RPMWindow.
func NewRPMWindow() *RPMWindow {
	return &RPMWindow{hits: make(map[int64][]time.Time)}
}

// Allow records one attempt and reports whether it fits the listing's rpm
// budget: the (rpm+1)-th attempt inside the window is refused. Attempts at
// least 60 seconds old fall out of the window.
func (w *RPMWindow) Allow(listingID int64, rpm int, now time.Time) bool {
	if rpm <= 0 {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	cutoff := now.Add(-rpmWindow)
	times := w.hits[listingID]
	first := 0
	for first < len(times) && !times[first].After(cutoff) {
		first++
	}
	times = times[first:]
	if len(times) >= rpm {
		w.hits[listingID] = times
		return false
	}
	w.hits[listingID] = append(times, now)
	return true
}

// ListingDailySpendMicro returns how much buyers have been charged through
// one listing since 00:00 Asia/Shanghai of the day containing now: the
// absolute buyer_debit + tool_debit of its mp_ledger rows (§3.5
// daily_cap_micro). Select skips the listing once this reaches
// Listing.DailyCapMicro; a 0 cap means unlimited.
func ListingDailySpendMicro(db *gorm.DB, listingID int64, now time.Time) (int64, error) {
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return 0, fmt.Errorf("marketplace: load %s: %w", shanghaiTZ, err)
	}
	lt := now.In(loc)
	dayStart := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)
	var total int64
	err = db.Model(&LedgerEntry{}).
		Where("listing_id = ? AND entry_type IN ? AND created_at >= ? AND created_at < ?",
			listingID, []string{EntryBuyerDebit, EntryToolDebit}, dayStart, dayEnd).
		Select("COALESCE(SUM(ABS(amount_micro)), 0)").
		Scan(&total).Error
	if err != nil {
		return 0, err
	}
	return total, nil
}

// WithinHours reports whether now falls inside a listing's 可接单时段
// (§3.5): an empty hours string means all day; otherwise now (in
// Asia/Shanghai) must lie in at least one [start, end) window. Anything
// unparseable fails closed — a listing with a broken hours config takes no
// traffic. Cross-midnight windows (start >= end) are rejected by
// ValidateHours at write time and never match here either.
func WithinHours(hours string, now time.Time) bool {
	hours = strings.TrimSpace(hours)
	if hours == "" {
		return true
	}
	var windows []hourWindow
	if err := common.UnmarshalJsonStr(hours, &windows); err != nil {
		return false
	}
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return false
	}
	lt := now.In(loc)
	mins := lt.Hour()*60 + lt.Minute()
	for _, w := range windows {
		start, err1 := parseHourMinute(w.Start)
		end, err2 := parseHourMinute(w.End)
		if err1 != nil || err2 != nil || start >= end {
			continue
		}
		if mins >= start && mins < end {
			return true
		}
	}
	return false
}

// dailySpendCacheTTL is how long one listing's daily spend sum is reused
// (M7: 避免每请求全表 SUM). One second bounds staleness far below the
// minutely RPM/cap granularity while collapsing bursts of requests.
const dailySpendCacheTTL = time.Second

// dailySpendEntry is one listing's cached daily spend. loading/done implement
// a single-flight refresh so a burst of requests issues one SUM query.
type dailySpendEntry struct {
	at      time.Time
	value   int64
	err     error
	loading bool
	done    chan struct{}
}

// dailySpendCache caches ListingDailySpendMicro per listing for a short TTL.
type dailySpendCache struct {
	mu      sync.Mutex
	entries map[int64]*dailySpendEntry
}

func newDailySpendCache() *dailySpendCache {
	return &dailySpendCache{entries: make(map[int64]*dailySpendEntry)}
}

// get returns listingID's spend at now, loading it through load at most once
// per TTL (and once concurrently — later callers wait for the in-flight load).
func (c *dailySpendCache) get(listingID int64, now time.Time, load func() (int64, error)) (int64, error) {
	c.mu.Lock()
	e := c.entries[listingID]
	if e != nil && !e.loading && now.Sub(e.at) < dailySpendCacheTTL {
		v, err := e.value, e.err
		c.mu.Unlock()
		return v, err
	}
	if e != nil && e.loading {
		done := e.done
		c.mu.Unlock()
		<-done
		c.mu.Lock()
		cur := c.entries[listingID]
		c.mu.Unlock()
		if cur != nil {
			return cur.value, cur.err
		}
		return 0, nil
	}
	if e == nil {
		e = &dailySpendEntry{}
		c.entries[listingID] = e
	}
	e.loading = true
	e.done = make(chan struct{})
	done := e.done
	c.mu.Unlock()

	v, err := load()

	c.mu.Lock()
	e.value, e.err, e.at, e.loading = v, err, now, false
	close(done)
	c.mu.Unlock()
	return v, err
}

// ErrNoListingAvailable is Select's outcome when every candidate has been
// filtered out; the relay layer maps it to §3.10's 503 暂无可用供应商.
var ErrNoListingAvailable = errors.New("marketplace: no listing available")

// Selector holds the routing state used to pick one listing per buyer
// request (§4.3 step 2).
type Selector struct {
	Slots   *SlotManager
	Breaker *CircuitBreaker
	RPM     *RPMWindow
	// CanaryFraction is the share of traffic admitted to canary listings
	// (§3.5: canary 灰度默认 10% 流量); 0 admits none, 1 admits all.
	CanaryFraction float64
	// Rand is the canary dice, injected so tests stay deterministic.
	// rand.Rand is not goroutine-safe, so canaryAdmit serializes it.
	Rand *rand.Rand

	randMu sync.Mutex
	// spend caches each listing's daily spend for dailySpendCacheTTL (M7).
	spendMu sync.Mutex
	spend   *dailySpendCache
}

// NewSelector builds a Selector with the §3.5 default canary fraction.
func NewSelector() *Selector {
	return &Selector{
		Slots:          NewSlotManager(),
		Breaker:        NewCircuitBreaker(),
		RPM:            NewRPMWindow(),
		CanaryFraction: 0.1,
		Rand:           rand.New(rand.NewSource(time.Now().UnixNano())),
		spend:          newDailySpendCache(),
	}
}

// dailySpend returns the listing's daily spend, served from the short-TTL
// process cache (single-flight) instead of one SUM per candidate per request.
func (s *Selector) dailySpend(db *gorm.DB, listingID int64, now time.Time) (int64, error) {
	s.spendMu.Lock()
	if s.spend == nil {
		s.spend = newDailySpendCache()
	}
	cache := s.spend
	s.spendMu.Unlock()
	return cache.get(listingID, now, func() (int64, error) {
		return ListingDailySpendMicro(db, listingID, now)
	})
}

// SelectInput is one buyer request's routing context (§4.3 step 2).
type SelectInput struct {
	Model string
	Now   time.Time // zero → time.Now()
	// ExcludeListingIDs carries the listings this request's retry chain has
	// already tried (§3.7 换家).
	ExcludeListingIDs []int64
	// MaxMultiplierMilli is the buyer key's price cap expressed as a
	// multiplier (§3.6: 兜底换家同样受价格上限约束); 0 means no cap.
	MaxMultiplierMilli int64
}

// selectorListingColumns deliberately omits key_ciphertext: Select runs over
// every live listing on every request, and the encrypted seller key is only
// needed for the winning listing (hydrateListingKey). M7: 挂单全表加载不取密文.
const selectorListingColumns = "id, seller_id, channel_id, vendor, models, multiplier_milli, " +
	"concurrency_limit, status, hours, daily_cap_micro, rpm_limit, key_fingerprint, version"

// Select filters the live (+ gray-admitted canary) listings for in.Model and
// returns the best-scored one, holding its concurrency slot — the caller
// MUST release it via s.Slots.Release when the request ends (§3.5).
//
// Filters, in order: not excluded, serves the model, canary admitted by the
// CanaryFraction dice, multiplier within MaxMultiplierMilli when set, inside
// its 可接单时段, under its daily spend cap, and circuit not broken. Scoring
// (§4.3 综合价格、可用率、延迟): multiplier ascending first; ties break on the
// most recent verify/patrol mp_probes latency ascending, with never-probed
// listings last; the final tie-break is listing id ascending.
//
// Side-effecting limits (RPM, half-open probe, concurrency slot) are applied
// only to candidates in ranked order, and only the eventual winner keeps
// them: a candidate whose RPM budget is exhausted is skipped in favour of the
// next best instead of consuming budget it never uses (M7). Use
// SelectWithProbe when the caller must know it holds a half-open probe.
func (s *Selector) Select(db *gorm.DB, in SelectInput) (Listing, error) {
	listing, _, err := s.SelectWithProbe(db, in)
	return listing, err
}

// SelectWithProbe is Select plus tookProbe: true when the returned listing's
// half-open circuit probe slot was consumed for this request. The caller MUST
// eventually resolve it via CircuitBreaker.RecordSuccess/Record429/
// ReleaseProbe (see RelaySession).
func (s *Selector) SelectWithProbe(db *gorm.DB, in SelectInput) (Listing, bool, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	var rows []Listing
	if err := db.Where("status IN ?", []string{ListingLive, ListingCanary}).
		Select(selectorListingColumns).Find(&rows).Error; err != nil {
		return Listing{}, false, err
	}
	var cands []Listing
	for i := range rows {
		l := rows[i]
		if slices.Contains(in.ExcludeListingIDs, l.ID) {
			continue
		}
		if !slices.Contains(l.Models, in.Model) {
			continue
		}
		if l.Status == ListingCanary && !s.canaryAdmit() {
			continue
		}
		if in.MaxMultiplierMilli > 0 && l.MultiplierMilli > in.MaxMultiplierMilli {
			continue
		}
		if !WithinHours(l.Hours, now) {
			continue
		}
		if l.DailyCapMicro > 0 {
			spent, err := s.dailySpend(db, l.ID, now)
			if err != nil {
				return Listing{}, false, err
			}
			if spent >= l.DailyCapMicro {
				continue
			}
		}
		// Non-mutating breaker check; the probe slot is only consumed for a
		// candidate that reaches the commit loop below.
		if !s.Breaker.available(l.ID, now) {
			continue
		}
		cands = append(cands, l)
	}
	if len(cands) == 0 {
		return Listing{}, false, ErrNoListingAvailable
	}
	ids := make([]int64, len(cands))
	for i := range cands {
		ids[i] = cands[i].ID
	}
	latency, err := latestProbeLatencies(db, ids)
	if err != nil {
		return Listing{}, false, err
	}
	slices.SortFunc(cands, func(a, b Listing) int {
		if a.MultiplierMilli != b.MultiplierMilli {
			return cmp.Compare(a.MultiplierMilli, b.MultiplierMilli)
		}
		la, oka := latency[a.ID]
		lb, okb := latency[b.ID]
		if oka != okb {
			if oka {
				return -1
			}
			return 1
		}
		if la != lb {
			return cmp.Compare(la, lb)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	// Commit side effects in ranked order: the winner keeps the slot, the RPM
	// budget and (when half-open) the probe; every loser is handed back before
	// the next candidate is considered.
	for i := range cands {
		l := cands[i]
		if !s.Slots.Acquire(l.ID, l.ConcurrencyLimit, now) {
			continue
		}
		if !s.RPM.Allow(l.ID, l.RPMLimit, now) {
			s.Slots.Release(l.ID)
			continue
		}
		allowed, tookProbe := s.Breaker.allow(l.ID, now)
		if !allowed {
			s.Slots.Release(l.ID)
			continue
		}
		return l, tookProbe, nil
	}
	return Listing{}, false, ErrNoListingAvailable
}

// canaryAdmit rolls the gray-release dice for one canary candidate.
func (s *Selector) canaryAdmit() bool {
	f := s.CanaryFraction
	if f <= 0 {
		return false
	}
	if f >= 1 {
		return true
	}
	s.randMu.Lock()
	defer s.randMu.Unlock()
	if s.Rand == nil {
		return rand.Float64() < f
	}
	return s.Rand.Float64() < f
}

// latestProbeLatencies returns the LatencyMs of each listing's most recent
// verify/patrol mp_probes row (§4.2); listings without any probe are absent
// from the map and rank last in scoring.
func latestProbeLatencies(db *gorm.DB, listingIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(listingIDs))
	if len(listingIDs) == 0 {
		return out, nil
	}
	latest := db.Model(&Probe{}).
		Select("MAX(id)").
		Where("listing_id IN ? AND type IN ?", listingIDs, []string{ProbeTypeVerify, ProbeTypePatrol}).
		Group("listing_id")
	var rows []struct {
		ListingID int64
		LatencyMs int64
	}
	if err := db.Model(&Probe{}).
		Select("listing_id, latency_ms").
		Where("id IN (?)", latest).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ListingID] = r.LatencyMs
	}
	return out, nil
}

// Probe types written outside verification (§4.2 验真/巡检/举报):
// ProbeTypeVerify lives in verify.go.
const (
	// ProbeTypePatrol marks rows from the background 巡检 job and from
	// AutoPauseListing.
	ProbeTypePatrol = "patrol"
	// ProbeTypeReport marks rows from buyer 举报 (arrives with phase 6).
	ProbeTypeReport = "report"
)

// AutoPauseListing applies the §3.5 rule for an upstream reporting
// insufficient balance or a rejected key: the listing goes live → paused at
// once, an mp_probes row records the semantic reason, and one SysLog line
// flags it. reason must carry semantics only (e.g. "upstream reported
// insufficient balance"), never any key material (§2.3).
//
// TODO(phase 6): notify the seller through the console notification channel
// (§3.5 立即自动暂停并通知分享者) once the phase-6 notification work lands.
func AutoPauseListing(db *gorm.DB, listingID int64, reason string) error {
	if err := PauseListing(db, listingID); err != nil {
		return err
	}
	detail, err := common.Marshal(map[string]any{
		"auto_paused": true,
		"reason":      reason,
	})
	if err != nil {
		detail = []byte("{}")
	}
	probe := Probe{
		ListingID: listingID,
		Type:      ProbeTypePatrol,
		OK:        false,
		Detail:    string(detail),
	}
	if err := db.Create(&probe).Error; err != nil {
		return fmt.Errorf("marketplace: record auto-pause probe: %w", err)
	}
	common.SysLog(fmt.Sprintf("marketplace: listing %d auto-paused: %s", listingID, reason))
	return nil
}
