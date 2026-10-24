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
}

// NewSelector builds a Selector with the §3.5 default canary fraction.
func NewSelector() *Selector {
	return &Selector{
		Slots:          NewSlotManager(),
		Breaker:        NewCircuitBreaker(),
		RPM:            NewRPMWindow(),
		CanaryFraction: 0.1,
		Rand:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
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

// Select filters the live (+ gray-admitted canary) listings for in.Model and
// returns the best-scored one, holding its concurrency slot — the caller
// MUST release it via s.Slots.Release when the request ends (§3.5).
//
// Filters, in order: not excluded, serves the model, canary admitted by the
// CanaryFraction dice, multiplier within MaxMultiplierMilli when set, inside
// its 可接单时段, under its daily spend cap, RPM budget left, circuit not
// broken, and a free concurrency slot (a full listing is skipped, never
// queued).
//
// Scoring (§4.3 综合价格、可用率、延迟): multiplier ascending first; ties
// break on the most recent verify/patrol mp_probes latency ascending, with
// never-probed listings last; the final tie-break is listing id ascending,
// so the pick is deterministic.
func (s *Selector) Select(db *gorm.DB, in SelectInput) (Listing, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	var rows []Listing
	if err := db.Where("status IN ?", []string{ListingLive, ListingCanary}).Find(&rows).Error; err != nil {
		return Listing{}, err
	}
	// candidate is one listing that passed every filter; tookProbe records
	// whether it holds the breaker's half-open probe slot.
	type candidate struct {
		listing   Listing
		tookProbe bool
	}
	var cands []candidate
	release := func(cs []candidate) {
		for _, c := range cs {
			s.Slots.Release(c.listing.ID)
			if c.tookProbe {
				s.Breaker.releaseProbe(c.listing.ID)
			}
		}
	}
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
			spent, err := ListingDailySpendMicro(db, l.ID, now)
			if err != nil {
				release(cands)
				return Listing{}, err
			}
			if spent >= l.DailyCapMicro {
				continue
			}
		}
		// Side-effecting checks come last. RPM.Allow spends budget on every
		// attempt reaching it, not only on wins — the conservative direction
		// for a seller-protecting limit (§3.5).
		if !s.RPM.Allow(l.ID, l.RPMLimit, now) {
			continue
		}
		allowed, tookProbe := s.Breaker.allow(l.ID, now)
		if !allowed {
			continue
		}
		if !s.Slots.Acquire(l.ID, l.ConcurrencyLimit, now) {
			if tookProbe {
				s.Breaker.releaseProbe(l.ID)
			}
			continue
		}
		cands = append(cands, candidate{listing: l, tookProbe: tookProbe})
	}
	if len(cands) == 0 {
		return Listing{}, ErrNoListingAvailable
	}
	ids := make([]int64, len(cands))
	for i, c := range cands {
		ids[i] = c.listing.ID
	}
	latency, err := latestProbeLatencies(db, ids)
	if err != nil {
		release(cands)
		return Listing{}, err
	}
	slices.SortFunc(cands, func(a, b candidate) int {
		if a.listing.MultiplierMilli != b.listing.MultiplierMilli {
			return cmp.Compare(a.listing.MultiplierMilli, b.listing.MultiplierMilli)
		}
		la, oka := latency[a.listing.ID]
		lb, okb := latency[b.listing.ID]
		if oka != okb {
			if oka {
				return -1
			}
			return 1
		}
		if la != lb {
			return cmp.Compare(la, lb)
		}
		return cmp.Compare(a.listing.ID, b.listing.ID)
	})
	// Every non-winner hands its slot (and half-open probe, if taken) back.
	release(cands[1:])
	return cands[0].listing, nil
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
