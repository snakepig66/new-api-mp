package marketplace

// trustpool.go — 认证池 (P2).
//
// The trust pool is the operator's list of upstream addresses the marketplace
// already knows. It answers one question before a new listing is provisioned:
// is this (host, path, key prefix) trusted, under watch, or blocked?
//
//   - trusted: no manual review; the normal per-model × endpoint verification
//     still decides which models may be sold.
//   - watch: the listing is created but parked in pending_review; an operator
//     must approve it before verification may promote it.
//   - blocked: the address is refused outright.
//   - unlisted: no row matched; the listing is parked in pending_review.
//
// Matching precedence follows P2's contract: an exact host row wins; only when
// no exact host row matches is an anchored host_pattern regex considered. Among
// the candidates of the winning kind the longest path_prefix is chosen, then a
// longer key_prefix, then the stricter trust state (blocked > watch > trusted).
//
// The JSON columns are plain text so SQLite, MySQL and PostgreSQL share one
// schema. allowed_models is maintained by the operator and is never inferred
// from the channel-model map.

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ErrTrustPoolBlocked marks a refusal backed by the trust pool: either the row
// itself is blocked or one of its deny rules matched the address/credential.
// The message never contains an API key.
var ErrTrustPoolBlocked = errors.New("marketplace: upstream address is blocked")

// Trust-pool source values (P5). "seed" is reserved for SeedTrustPool: the
// operator console may write manual/probe rows but can never mint a seeded row,
// so official rows stay owned by the code.
const (
	TrustSourceSeed   = "seed"
	TrustSourceManual = "manual"
	TrustSourceProbe  = "probe"
)

// TrustPoolSeedValidity bounds a newly seeded trusted row. Every trusted row
// must carry an expiry (P5 governance) so the official pool is reviewed
// periodically instead of trusted forever. Existing rows created before this
// bound are left untouched; SeedTrustPool only fills in a missing expiry.
const TrustPoolSeedValidity = 90 * 24 * time.Hour

// Trust-pool write validation errors. The admin API maps them to 400/409; the
// text never echoes key material.
var (
	ErrTrustPoolAddressRequired    = errors.New("exactly one of host or host_pattern is required")
	ErrTrustPoolAddressAmbiguous   = errors.New("host and host_pattern are mutually exclusive")
	ErrTrustPoolHostInvalid        = errors.New("host is not a valid hostname")
	ErrTrustPoolPatternInvalid     = errors.New("host_pattern is not a valid regular expression")
	ErrTrustPoolKindInvalid        = errors.New("kind must be official, third_party or plan")
	ErrTrustPoolTrustInvalid       = errors.New("trust must be trusted, watch or blocked")
	ErrTrustPoolOfficialSource     = errors.New("official rows can only come from the built-in seed data")
	ErrTrustPoolTrustedExpiry      = errors.New("a trusted row requires expires_at")
	ErrTrustPoolBlockedNotes       = errors.New("a blocked row requires notes (the refusal reason)")
	ErrTrustPoolSourceReserved     = errors.New("source seed is reserved for the built-in trust pool")
	ErrTrustPoolSourceImmutable    = errors.New("source cannot be changed")
	ErrTrustPoolSeedLocked         = errors.New("seed rows are managed by the code and cannot be deleted")
	ErrTrustPoolApprovalNotPending = errors.New("trust-pool change is not pending")
	ErrTrustPoolApprovalSelfReview = errors.New("submitters cannot review their own trust-pool change")
)

// NormalizeTrustPoolAddress validates the host/host_pattern pair and returns
// the canonical values stored in the pool. Exactly one side must be non-empty:
// an exact host row wins over a pattern row in MatchTrustPoolDB, so a row that
// carries both would be ambiguous. A host is lowercased and stripped of its
// port; a pattern is anchored by the matcher, so it must compile as a regex.
func NormalizeTrustPoolAddress(host, hostPattern string) (string, string, error) {
	host = strings.TrimSpace(host)
	hostPattern = strings.TrimSpace(hostPattern)
	if (host == "") == (hostPattern == "") {
		if host == "" {
			return "", "", ErrTrustPoolAddressRequired
		}
		return "", "", ErrTrustPoolAddressAmbiguous
	}
	if host != "" {
		// The field is a bare host, not a URL: reject a scheme or any path
		// separator before normalization (SplitHostPort would otherwise treat
		// "https://host/path" as host "https").
		if strings.ContainsAny(host, "/\\?#@") {
			return "", "", ErrTrustPoolHostInvalid
		}
		normalized := normalizeEgressHost(host)
		if normalized == "" || len(normalized) > 255 || strings.ContainsAny(normalized, "/\\?#@ ") {
			return "", "", ErrTrustPoolHostInvalid
		}
		return normalized, "", nil
	}
	if len(hostPattern) > 512 {
		return "", "", ErrTrustPoolPatternInvalid
	}
	if _, err := regexp.Compile("^(?:" + hostPattern + ")$"); err != nil {
		return "", "", ErrTrustPoolPatternInvalid
	}
	return "", hostPattern, nil
}

// ValidateTrustPoolGovernance enforces the P5 invariants on an operator write:
//
//   - kind is one of official / third_party / plan;
//   - trust is one of trusted / watch / blocked;
//   - a trusted row carries an expiry, so the certification is reviewed;
//   - a blocked row explains itself in notes;
//   - an official row may only be a seeded row. The API never accepts
//     source="seed", so this guard makes official rows unreachable from the
//     console while still accepting edits to the seeded rows themselves.
func ValidateTrustPoolGovernance(row TrustPool) error {
	switch row.Kind {
	case TrustKindOfficial, TrustKindThirdParty, TrustKindPlan:
	default:
		return ErrTrustPoolKindInvalid
	}
	switch row.Trust {
	case TrustTrusted:
		if row.ExpiresAt == nil {
			return ErrTrustPoolTrustedExpiry
		}
	case TrustWatch:
	case TrustBlocked:
		if strings.TrimSpace(row.Notes) == "" {
			return ErrTrustPoolBlockedNotes
		}
	default:
		return ErrTrustPoolTrustInvalid
	}
	if row.Kind == TrustKindOfficial && row.Source != TrustSourceSeed {
		return ErrTrustPoolOfficialSource
	}
	return nil
}

// ValidateTrustPoolApprovalDecision enforces the P5-B 双人确认 guard: only a
// pending change may be decided, and the reviewer must not be the operator who
// submitted it. The API keeps RootAuth in this phase; this check is the
// authorization rule that RootAuth alone cannot express.
func ValidateTrustPoolApprovalDecision(approval TrustPoolApproval, reviewerID int64) error {
	if approval.Status != TrustPoolApprovalPending {
		return ErrTrustPoolApprovalNotPending
	}
	if reviewerID > 0 && approval.SubmitterID == reviewerID {
		return ErrTrustPoolApprovalSelfReview
	}
	return nil
}

// MatchTrustPool looks the address up in the process-wide relay database. It
// is the convenience form for callers that run inside the initialized
// marketplace; MatchTrustPoolDB is the explicit-handle form used by listing and
// channel creation.
func MatchTrustPool(host, path, keyPrefix string) (*TrustPool, error) {
	db, _ := relayState()
	return MatchTrustPoolDB(db, host, path, keyPrefix)
}

// CheckTrustPool classifies the address in the process-wide relay database.
func CheckTrustPool(host, path, keyPrefix string) (string, *TrustPool, error) {
	db, _ := relayState()
	return CheckTrustPoolDB(db, host, path, keyPrefix)
}

// MatchTrustPoolDB returns the most specific trust-pool row matching the
// address, or nil when nothing matches. host is normalized (lowercase, no
// port). path is the normalized upstream path ("" for the root, or a full URL
// whose path is used). keyPrefix is the credential or its leading characters:
// a row with a key_prefix only matches when that prefix is a prefix of the
// credential. An unknown credential (""), therefore, never matches a
// key-specific row; such an address falls back to unlisted.
func MatchTrustPoolDB(db *gorm.DB, host, path, keyPrefix string) (*TrustPool, error) {
	if db == nil {
		return nil, nil
	}
	host = normalizeEgressHost(host)
	if host == "" {
		return nil, nil
	}
	path = normalizeTrustPath(path)
	keyPrefix = strings.TrimSpace(keyPrefix)

	var exactRows []TrustPool
	if err := db.Where("host = ?", host).Find(&exactRows).Error; err != nil {
		return nil, fmt.Errorf("marketplace: query trust pool: %w", err)
	}
	if best := matchTrustRows(exactRows, path, keyPrefix); best != nil {
		return best, nil
	}
	// Only when no exact-host row matches (including after the path/key filter)
	// do the anchored patterns get a chance.
	var patterned []TrustPool
	if err := db.Where("host_pattern <> ?", "").Find(&patterned).Error; err != nil {
		return nil, fmt.Errorf("marketplace: query trust pool patterns: %w", err)
	}
	matches := make([]TrustPool, 0, len(patterned))
	for i := range patterned {
		if trustHostPatternMatches(patterned[i].HostPattern, host) {
			matches = append(matches, patterned[i])
		}
	}
	return matchTrustRows(matches, path, keyPrefix), nil
}

// CheckTrustPoolDB classifies one address. It returns:
//
//   - "unlisted" and a nil row when nothing matches;
//   - "blocked" plus ErrTrustPoolBlocked when the row is blocked or its
//     DenyPaths / DenyKeyPrefixes matched;
//   - "watch" for a watch row, and for a trusted row that is expired
//     (fail-closed downgrade);
//   - "trusted" for a live trusted row.
//
// An unknown stored trust value is treated as watch.
func CheckTrustPoolDB(db *gorm.DB, host, path, keyPrefix string) (string, *TrustPool, error) {
	pool, err := MatchTrustPoolDB(db, host, path, keyPrefix)
	if err != nil {
		return "", nil, err
	}
	if pool == nil {
		return TrustUnlisted, nil, nil
	}
	normalizedPath := normalizeTrustPath(path)
	if trustPathDenied(pool.DenyPaths, normalizedPath) {
		return TrustBlocked, pool, fmt.Errorf("%w: path %q is refused for host %q", ErrTrustPoolBlocked, normalizedPath, trustHostLabel(pool))
	}
	if keyPrefix = strings.TrimSpace(keyPrefix); keyPrefix != "" && trustKeyPrefixDenied(pool.DenyKeyPrefixes, keyPrefix) {
		return TrustBlocked, pool, fmt.Errorf("%w: this credential class is refused for host %q", ErrTrustPoolBlocked, trustHostLabel(pool))
	}
	switch pool.Trust {
	case TrustBlocked:
		return TrustBlocked, pool, fmt.Errorf("%w: host %q is blocked", ErrTrustPoolBlocked, trustHostLabel(pool))
	case TrustTrusted:
		if pool.ExpiresAt != nil && !pool.ExpiresAt.After(time.Now()) {
			// A trusted certification past its expiry may not skip review.
			return TrustWatch, pool, nil
		}
		return TrustTrusted, pool, nil
	case TrustWatch:
		return TrustWatch, pool, nil
	default:
		// Unknown state fails closed: an operator must look at it.
		return TrustWatch, pool, nil
	}
}

// matchTrustRows chooses the winning candidate.
func matchTrustRows(rows []TrustPool, path, keyPrefix string) *TrustPool {
	var best *TrustPool
	bestPath, bestKey := -1, -1
	for i := range rows {
		row := &rows[i]
		if !trustPathMatches(row.PathPrefix, path) {
			continue
		}
		if !trustKeyMatches(row.KeyPrefix, keyPrefix) {
			continue
		}
		pathLen := len(strings.TrimRight(strings.TrimSpace(row.PathPrefix), "/"))
		keyLen := len(strings.TrimSpace(row.KeyPrefix))
		if best == nil ||
			pathLen > bestPath ||
			(pathLen == bestPath && keyLen > bestKey) ||
			(pathLen == bestPath && keyLen == bestKey && trustSeverity(row.Trust) > trustSeverity(best.Trust)) ||
			(pathLen == bestPath && keyLen == bestKey && trustSeverity(row.Trust) == trustSeverity(best.Trust) && row.ID < best.ID) {
			best = row
			bestPath, bestKey = pathLen, keyLen
		}
	}
	return best
}

// trustSeverity orders trust states for tie-breaking: a stricter state wins.
func trustSeverity(trust string) int {
	switch trust {
	case TrustBlocked:
		return 2
	case TrustWatch:
		return 1
	default:
		return 0
	}
}

// trustHostPatternMatches compiles an anchored host regex. Patterns are always
// wrapped in ^(?:…)$ so an operator cannot accidentally accept a substring;
// backslashes in the stored pattern are used verbatim. An invalid pattern never
// matches (the address falls back to unlisted and therefore review).
func trustHostPatternMatches(pattern, host string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	if err != nil {
		return false
	}
	return re.MatchString(host)
}

// normalizeTrustPath turns "", "/", a path or a full URL into the canonical
// upstream path: a leading slash and no trailing slash.
func normalizeTrustPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.Contains(path, "://") {
		if u, err := url.Parse(path); err == nil {
			path = u.Path
		}
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

// trustPathMatches applies a path_prefix on segment boundaries: /api/v1
// matches /api/v1 and /api/v1/chat but never /api/v10. An empty prefix matches
// every path.
func trustPathMatches(prefix, path string) bool {
	prefix = strings.TrimRight(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return true
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	path = normalizeTrustPath(path)
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// trustKeyMatches applies a key_prefix to the supplied credential. An empty
// configured prefix accepts every credential; a configured prefix requires the
// caller to supply a credential that starts with it.
func trustKeyMatches(configured, key string) bool {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return true
	}
	return key != "" && strings.HasPrefix(key, configured)
}

// trustPathDenied reports whether path hits one of the JSON deny paths, on a
// segment boundary. Malformed JSON yields no deny rules.
func trustPathDenied(raw, path string) bool {
	if path == "" {
		return false
	}
	for _, deny := range parseStringList(raw) {
		deny = strings.TrimRight(strings.TrimSpace(deny), "/")
		if deny == "" {
			continue
		}
		if !strings.HasPrefix(deny, "/") {
			deny = "/" + deny
		}
		if path == deny || strings.HasPrefix(path, deny+"/") {
			return true
		}
	}
	return false
}

// trustKeyPrefixDenied reports whether the credential carries one of the JSON
// deny prefixes (case-insensitive, matching VendorKeyDenied).
func trustKeyPrefixDenied(raw, key string) bool {
	lower := strings.ToLower(key)
	for _, prefix := range parseStringList(raw) {
		prefix = strings.ToLower(strings.TrimSpace(prefix))
		if prefix != "" && strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// trustHostLabel identifies a row in an error without ever including a key.
func trustHostLabel(pool *TrustPool) string {
	if pool == nil {
		return ""
	}
	if pool.Host != "" {
		return pool.Host
	}
	return pool.HostPattern
}

// trustPoolSeeds are the official vendor addresses of the initial channel
// pool. They are deliberately explicit: allowed_models is the operator's
// statement of what may be sold on that address, never a derivation from
// mp_channel_models. source="seed" lets the seeder recognize its own rows.
var trustPoolSeeds = []TrustPool{
	{
		Host:          "api.deepseek.com",
		VendorSlug:    SeedVendorDeepSeek,
		Kind:          ChannelKindOfficial,
		Trust:         TrustTrusted,
		Endpoints:     `["openai_chat","anthropic_messages"]`,
		AllowedModels: `["deepseek-flash","deepseek-v4-pro"]`,
		Source:        "seed",
		Notes:         "DeepSeek official API",
	},
	{
		Host:          "ark.cn-beijing.volces.com",
		VendorSlug:    "volcengine-ark",
		Kind:          ChannelKindOfficial,
		Trust:         TrustTrusted,
		Endpoints:     `["openai_chat","anthropic_messages"]`,
		AllowedModels: `["deepseek-flash","deepseek-v4-pro","doubao-seed-evolving"]`,
		DenyPaths:     `["/api/coding","/api/plan"]`,
		Source:        "seed",
		Notes:         "Volcengine Ark official API; coding/plan paths are personal-plan only",
	},
	{
		Host:            "dashscope.aliyuncs.com",
		VendorSlug:      "aliyun-bailian",
		Kind:            ChannelKindOfficial,
		Trust:           TrustTrusted,
		Endpoints:       `["openai_chat","anthropic_messages"]`,
		AllowedModels:   `["deepseek-flash","deepseek-v4-pro","qwen3.8-max","qwen3.7-plus","qwen3.8-flash"]`,
		DenyPaths:       `["/apps/coding"]`,
		DenyKeyPrefixes: `["sk-sp-","sk-ws-"]`,
		Source:          "seed",
		Notes:           "Alibaba Bailian official API; Token/Coding plan keys are refused",
	},
	{
		HostPattern:     `^[a-z0-9-]+\.cn-beijing\.maas\.aliyuncs\.com$`,
		PathPrefix:      "/compatible-mode/v1",
		VendorSlug:      "aliyun-bailian",
		Kind:            ChannelKindOfficial,
		Trust:           TrustTrusted,
		Endpoints:       `["openai_chat"]`,
		AllowedModels:   `["deepseek-flash","deepseek-v4-pro","qwen3.8-max","qwen3.7-plus","qwen3.8-flash"]`,
		DenyPaths:       `["/apps/coding"]`,
		DenyKeyPrefixes: `["sk-sp-","sk-ws-"]`,
		Source:          "seed",
		Notes:           "Bailian workspace domain; only the compatible-mode mount is trusted",
	},
	{
		Host:          "api.moonshot.cn",
		VendorSlug:    "moonshot",
		Kind:          ChannelKindOfficial,
		Trust:         TrustTrusted,
		Endpoints:     `["openai_chat","anthropic_messages"]`,
		AllowedModels: `["kimi-k2","kimi-k3.1-pro","kimi-k3","kimi-k2.7-code","kimi-k2.6"]`,
		Source:        "seed",
		Notes:         "Moonshot official API",
	},
	{
		Host:          "open.bigmodel.cn",
		VendorSlug:    "zhipu",
		Kind:          ChannelKindOfficial,
		Trust:         TrustTrusted,
		Endpoints:     `["openai_chat","anthropic_messages"]`,
		AllowedModels: `["glm-5.3","glm-5.3-flash","glm-5.3-flashx","glm-5.2","glm-4.7-flash","glm-4.6"]`,
		DenyPaths:     `["/api/coding/paas/v4"]`,
		Source:        "seed",
		Notes:         "Zhipu BigModel official API; coding-plan paths are refused",
	},
	{
		Host:          "api.minimaxi.com",
		VendorSlug:    "minimax",
		Kind:          ChannelKindOfficial,
		Trust:         TrustTrusted,
		Endpoints:     `["openai_chat","anthropic_messages"]`,
		AllowedModels: `["minimax-m3","minimax-m3.1-flash","minimax-m2"]`,
		Source:        "seed",
		Notes:         "MiniMax official API",
	},
}

// SeedTrustPool idempotently writes the official seed rows. It only inserts a
// row whose (source, host, host_pattern, path_prefix, key_prefix) is absent, so
// operator edits are never overwritten and repeated startup is a no-op.
func SeedTrustPool(db *gorm.DB) error {
	if db == nil {
		return errors.New("marketplace: seed trust pool: nil database")
	}
	for i := range trustPoolSeeds {
		seed := trustPoolSeeds[i]
		var cnt int64
		err := db.Model(&TrustPool{}).
			Where("source = ? AND host = ? AND host_pattern = ? AND path_prefix = ? AND key_prefix = ?",
				seed.Source, seed.Host, seed.HostPattern, seed.PathPrefix, seed.KeyPrefix).
			Count(&cnt).Error
		if err != nil {
			return fmt.Errorf("marketplace: seed trust pool: %w", err)
		}
		if cnt > 0 {
			continue
		}
		row := seed
		// P5 governance: a trusted row must carry an expiry. The seeder applies
		// the same rule so the built-in official rows are reviewed, and an
		// existing operator-set expiry is never overwritten.
		if row.Trust == TrustTrusted && row.ExpiresAt == nil {
			expires := time.Now().Add(TrustPoolSeedValidity)
			row.ExpiresAt = &expires
		}
		if err := db.Create(&row).Error; err != nil {
			return fmt.Errorf("marketplace: seed trust pool: %w", err)
		}
	}
	return nil
}
