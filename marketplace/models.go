package marketplace

import (
	"time"

	"gorm.io/gorm"
)

// Table definitions follow MARKETPLACE-DEV.md §4.2. All tables carry the mp_
// prefix and live in the primary database alongside upstream tables. Money is
// stored as integer 微元 (Micro); no float columns exist anywhere.

// Listing lifecycle (§3.5): draft → verifying → canary → live ⇄ paused → banned.
// ListingPendingReview is the gate for a seller-defined upstream that is not in
// the trust pool's trusted set (watch or unlisted): the listing exists but no
// automated verification may promote it until an operator approves it.
const (
	ListingDraft         = "draft"
	ListingPendingReview = "pending_review"
	ListingVerifying     = "verifying"
	ListingCanary        = "canary"
	ListingLive          = "live"
	ListingPaused        = "paused"
	ListingBanned        = "banned"
	// ListingDeleted is the seller's soft-delete state. The row and its history
	// remain readable; the key fingerprint moves to DeletedFingerprint so the
	// unique column is free for a re-listing, and the ciphertext is wiped by a
	// delayed job after in-flight requests can no longer need it.
	ListingDeleted = "deleted"
)

// Price tiers (§3.3).
const (
	TierPeak    = "peak"
	TierOffpeak = "offpeak"
	TierFlat    = "flat"
)

// Per-call billable items (§3.4).
const (
	ToolItemSearch = "search"
	ToolItemFetch  = "fetch"
)

// Seller is a user's 分享者 identity extension (§3.1). One row per user.
type Seller struct {
	UserID           int64  `gorm:"primaryKey;autoIncrement:false"` // 1:1 with users.id
	Status           string `gorm:"size:20;not null;index"`         // pending / active / suspended / closed
	RealNameStatus   string `gorm:"size:20;not null"`
	AgreementVersion string `gorm:"size:40;not null"`
	PayoutAccount    string `gorm:"type:text"` // encrypted alipay account, never returned by APIs
	CreditScore      int    `gorm:"not null"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (Seller) TableName() string { return "mp_sellers" }

// PriceBook is one official price tier of a model (§3.2/§3.3), versioned.
// Prices are 微元 per million tokens.
type PriceBook struct {
	ID                     int64     `gorm:"primaryKey"`
	Model                  string    `gorm:"size:128;not null;uniqueIndex:uk_mp_price_books,priority:1"`
	Vendor                 string    `gorm:"size:64;not null;uniqueIndex:uk_mp_price_books,priority:2"`
	Tier                   string    `gorm:"size:16;not null;uniqueIndex:uk_mp_price_books,priority:3"`
	Version                int64     `gorm:"not null;uniqueIndex:uk_mp_price_books,priority:4"`
	InputMicroPerMTok      int64     `gorm:"not null"`
	OutputMicroPerMTok     int64     `gorm:"not null"`
	CacheReadMicroPerMTok  int64     `gorm:"not null"`
	CacheWriteMicroPerMTok int64     `gorm:"not null"`
	EffectiveAt            time.Time `gorm:"not null;index"`
	CreatedAt              time.Time
}

func (PriceBook) TableName() string { return "mp_price_books" }

// PerCallPrice is the official unit price (微元/次) of a server-side tool such
// as DeepSeek Search/Fetch (§3.4), versioned.
type PerCallPrice struct {
	ID             int64     `gorm:"primaryKey"`
	Vendor         string    `gorm:"size:64;not null;uniqueIndex:uk_mp_per_call,priority:1"`
	Item           string    `gorm:"size:32;not null;uniqueIndex:uk_mp_per_call,priority:2"`
	Version        int64     `gorm:"not null;uniqueIndex:uk_mp_per_call,priority:3"`
	UnitPriceMicro int64     `gorm:"not null"`
	EffectiveAt    time.Time `gorm:"not null;index"`
	CreatedAt      time.Time
}

func (PerCallPrice) TableName() string { return "mp_per_call_prices" }

// Holiday is one day of the Chinese statutory calendar (§3.3): kind is
// "holiday" for 法定节假日 and "workday" for 调休上班的周末.
type Holiday struct {
	Date string `gorm:"primaryKey;size:10"` // YYYY-MM-DD in Asia/Shanghai
	Kind string `gorm:"size:16;not null"`
}

func (Holiday) TableName() string { return "mp_holidays" }

// Listing is one seller's listed API key (§3.5). One listing = one upstream
// channel = one key. The key itself lives in the upstream channels table,
// encrypted; only its HMAC fingerprint is stored here.
type Listing struct {
	ID               int64    `gorm:"primaryKey"`
	SellerID         int64    `gorm:"not null;index"`
	ChannelID        int64    `gorm:"not null"`
	Vendor           string   `gorm:"size:64;not null;index"`
	Models           []string `gorm:"type:text;serializer:json"`
	MultiplierMilli  int64    `gorm:"not null"` // 倍率千分值 1..1000 (§3.2)
	ConcurrencyLimit int      `gorm:"not null"`
	RPMLimit         int      `gorm:"not null"`  // 0 = unlimited
	DailyCapMicro    int64    `gorm:"not null"`  // 0 = unlimited
	Hours            string   `gorm:"type:text"` // JSON windows, empty = all day
	Status           string   `gorm:"size:16;not null;index"`
	// P4 group: listings of one seller on the same vendor share a group
	// (mp_listing_groups) that aggregates the channel-level limits. 0 means the
	// listing is not grouped (legacy rows and standalone fixtures).
	GroupID int64 `gorm:"not null;default:0;index"`
	// Weight is the relative share of this key inside a weighted_round_robin
	// group. 0 is treated as 100, so the default is an even distribution.
	Weight int `gorm:"not null;default:0"`
	// PauseReason / ResumeAt / PreviousStatus carry a time-boxed suspension
	// (MODEL-CHANNEL-POOL §4): a plan channel's quota window ran out, so every
	// listing of that channel is paused until ResumeAt and then restored to
	// PreviousStatus. They are empty for ordinary pauses and bans.
	PauseReason    string     `gorm:"size:64;not null;default:''" json:"pause_reason,omitempty"`
	ResumeAt       *time.Time `json:"resume_at,omitempty"`
	PreviousStatus string     `gorm:"size:32;not null;default:''" json:"previous_status,omitempty"`
	KeyFingerprint string     `gorm:"size:128;not null;uniqueIndex"`
	// KeyCiphertext is the AES-256-GCM blob (nonce||ciphertext) of the
	// seller's API key (§3.5); the plaintext never touches the database.
	// No explicit type: GORM maps []byte to the dialect's binary type
	// (BLOB / BYTEA), keeping PostgreSQL from choking on the literal "blob".
	KeyCiphertext       []byte
	Version             int64 `gorm:"not null"` // optimistic lock
	MultiplierUpdatedAt *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time

	// Label is the seller's optional display name. Two channels on the same
	// vendor are otherwise distinguishable only by #id.
	Label string `gorm:"size:64;not null;default:''"`
	// Soft-delete bookkeeping. DeletedFingerprint keeps the original key
	// identity after KeyFingerprint is replaced by a per-row tombstone, so a
	// banned key can never be re-listed while a normally deleted one can.
	DeletedFingerprint string `gorm:"size:128;not null;default:'';index"`
	DeletedBanned      bool   `gorm:"not null;default:false"`
	DeletedAt          *time.Time
	KeyWipeAt          *time.Time `gorm:"index"`
	// PendingHeaders* carry a candidate header policy that is verified before
	// it replaces channels.header_override. The plaintext never leaves the
	// encrypted column; the version lets a newer submission supersede an
	// in-flight verification.
	PendingHeadersCiphertext []byte
	PendingHeadersVersion    int `gorm:"not null;default:0"`
	PendingHeadersAt         *time.Time
}

func (Listing) TableName() string { return "mp_listings" }

// ListingGroup is the P4 simplified multi-key container: one seller's listings
// on the same vendor share one group, so their keys rotate inside one logical
// channel and share the channel-level limits. A listing still represents
// exactly one key.
//
// Channel* limits are aggregate limits over every listing in the group:
// 0 means unlimited. Concurrency and RPM are enforced by the selector's
// process-local state; the daily and cumulative caps are computed from
// mp_ledger.
type ListingGroup struct {
	ID          int64  `gorm:"primaryKey"`
	SellerID    int64  `gorm:"not null;uniqueIndex:uk_mp_listing_groups_seller_vendor,priority:1"`
	VendorSlug  string `gorm:"size:64;not null;uniqueIndex:uk_mp_listing_groups_seller_vendor,priority:2"`
	KeyStrategy string `gorm:"size:32;not null;default:'weighted_round_robin'"`
	// AffinityEnabled defaults to true in code (not a column default) so
	// AutoMigrate stays stable across SQLite, MySQL and PostgreSQL.
	AffinityEnabled           bool  `gorm:"not null"`
	ChannelConcurrencyLimit   int   `gorm:"not null"`
	ChannelRPMLimit           int   `gorm:"not null"`
	ChannelDailyCapMicro      int64 `gorm:"not null"`
	ChannelCumulativeCapMicro int64 `gorm:"not null"`
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

func (ListingGroup) TableName() string { return "mp_listing_groups" }

// ListingModel is one listing's per-model multiplier override. The selector
// and the pricing path read it before falling back to Listing.MultiplierMilli.
// A row with enabled=false means the listing does NOT serve the model; it is
// not a fallback to the base multiplier.
type ListingModel struct {
	ID              int64  `gorm:"primaryKey"`
	ListingID       int64  `gorm:"not null;index;uniqueIndex:uk_mp_listing_models_listing_model,priority:1"`
	Model           string `gorm:"size:128;not null;uniqueIndex:uk_mp_listing_models_listing_model,priority:2"`
	MultiplierMilli int64  `gorm:"not null"`
	Enabled         bool   `gorm:"not null"`
	CreatedAt       time.Time
	UpdatedAt       time.Time

	// Automatic disable state. A row may be disabled by the seller or by a
	// relay/patrol rule; the reason and the upstream evidence are shown in the
	// console so the seller knows what to fix.
	AutoDisabled            bool   `gorm:"not null;default:false"`
	DisabledReason          string `gorm:"size:64;not null;default:''"`
	DisabledDetail          string `gorm:"type:text;not null;default:''"`
	DisabledHTTPStatus      int    `gorm:"not null;default:0"`
	DisabledUpstreamMessage string `gorm:"type:text;not null;default:''"`
	DisabledAt              *time.Time
	// A price increase is staged for one hour. Decreases apply immediately and
	// clear the staged value. LastChangedDate (Asia/Shanghai) enforces one
	// increase per model per day; decreases do not consume it.
	PendingMultiplierMilli int64 `gorm:"not null;default:0"`
	PendingEffectiveAt     *time.Time
	LastChangedAt          *time.Time
	LastChangedDate        string `gorm:"size:10;not null;default:''"`
}

func (ListingModel) TableName() string { return "mp_listing_models" }

// P3 extension: a listing is split into a seller channel with one or more
// endpoints, keys and per-model rows. During P3a the old mp_listings tables
// remain authoritative: the new tables are written by CreateListing and by
// BackfillSellerChannels, and are never read by the selector or relay paths.
const (
	// Key strategy of a seller channel.
	KeyStrategyWeightedRoundRobin = "weighted_round_robin"
	KeyStrategyPrimaryBackup      = "primary_backup"

	// Endpoint protocols. The values match relay/constant.EndpointKind.
	EndpointProtocolOpenAIChat        = "openai_chat"
	EndpointProtocolOpenAIResponses   = "openai_responses"
	EndpointProtocolAnthropicMessages = "anthropic_messages"

	// Endpoint authentication schemes.
	EndpointAuthBearer  = "bearer"
	EndpointAuthXAPIKey = "x-api-key"

	// Per-key lifecycle states.
	ChannelKeyNormal       = "normal"
	ChannelKeyCooling      = "cooling"
	ChannelKeyFailed       = "failed"
	ChannelKeyInsufficient = "insufficient"
	ChannelKeyDraining     = "draining"
)

// SellerChannel is the P3 shadow identity of one legacy Listing. Its id is the
// listing id, so request/ledger rows can carry one channel identity across the
// migration. Status uses the same values as Listing.Status.
type SellerChannel struct {
	ID          int64 `gorm:"primaryKey"`
	SellerID    int64 `gorm:"not null;index"`
	TrustPoolID int64 `gorm:"not null;default:0;index"` // matched mp_trust_pool row, 0 when none
	// Base is the normalized OpenAI-compatible base of the channel; endpoints
	// carry the protocol-specific bases.
	Base string `gorm:"size:512;not null;default:''"`
	// HeaderPolicy is a JSON header policy for requests to this channel.
	HeaderPolicy    string `gorm:"type:text;not null;default:''"`
	KeyStrategy     string `gorm:"size:32;not null;default:'weighted_round_robin'"`
	AffinityEnabled bool   `gorm:"not null;default:false"`
	// Channel-level limits. A key-level limit may not exceed these.
	ConcurrencyLimit   int    `gorm:"not null"`
	RPMLimit           int    `gorm:"not null"`
	DailyCapMicro      int64  `gorm:"not null"`
	CumulativeCapMicro int64  `gorm:"not null"`
	Hours              string `gorm:"type:text"`
	Status             string `gorm:"size:32;not null;index"`
	PauseReason        string `gorm:"size:64;not null;default:''"`
	ResumeAt           *time.Time
	Version            int64 `gorm:"not null"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (SellerChannel) TableName() string { return "mp_seller_channels" }

// ChannelEndpoint is one protocol endpoint of a seller channel. FixedPath is
// the protocol's fixed upstream path, so a buyer's request path never decides
// the upstream URL.
type ChannelEndpoint struct {
	ID         int64  `gorm:"primaryKey"`
	ChannelID  int64  `gorm:"not null;uniqueIndex:uk_mp_channel_endpoints,priority:1"`
	Protocol   string `gorm:"size:32;not null;uniqueIndex:uk_mp_channel_endpoints,priority:2"`
	Base       string `gorm:"size:512;not null;default:''"`
	FixedPath  string `gorm:"size:255;not null;default:''"`
	AuthMethod string `gorm:"size:16;not null;default:'bearer'"`
	// StreamEndRule reports whether the upstream ends its SSE stream with
	// [DONE] (OpenAI chat completions). Responses and Messages use their own
	// terminal events.
	StreamEndRule bool   `gorm:"not null;default:false"`
	TimeoutConfig string `gorm:"type:text;not null;default:''"`
	Verified      bool   `gorm:"not null;default:false"`
	VerifiedAt    *time.Time
	// NewAPIChannelID points at the placeholder model.Channel row that the
	// legacy write path provisions for this listing.
	NewAPIChannelID int64 `gorm:"not null;default:0;index"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (ChannelEndpoint) TableName() string { return "mp_channel_endpoints" }

// ChannelKey is one upstream credential of a seller channel. KeyCiphertext is
// the same AES-256-GCM blob the legacy listing carries. DeletedAt is a soft
// delete so historical requests keep a resolvable key id.
type ChannelKey struct {
	ID             int64 `gorm:"primaryKey"`
	ChannelID      int64 `gorm:"not null;index"`
	KeyCiphertext  []byte
	KeyFingerprint string `gorm:"size:128;not null;uniqueIndex"`
	// DeletedFingerprint keeps the identity of a soft-deleted shadow key after
	// KeyFingerprint moves to a per-row tombstone. Without it, a re-listed key
	// would collide with the soft-deleted row's unique index.
	DeletedFingerprint         string `gorm:"size:128;not null;default:'';index"`
	UpstreamAccountFingerprint string `gorm:"size:128;not null;default:''"`
	Weight                     int    `gorm:"not null;default:100"`
	// Per-key limits; normalization rejects values above the channel level.
	ConcurrencyLimit int    `gorm:"not null"`
	RPMLimit         int    `gorm:"not null"`
	Status           string `gorm:"size:16;not null;index"`
	PauseReason      string `gorm:"size:64;not null;default:''"`
	ResumeAt         *time.Time
	QuotaWindow      string         `gorm:"type:text;not null;default:''"`
	DeletedAt        gorm.DeletedAt `gorm:"index"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (ChannelKey) TableName() string { return "mp_channel_keys" }

// SellerChannelModel is one platform model a seller channel may serve. It is
// the per-seller counterpart of the pool's mp_channel_models (channel ×
// upstream model qualification); the distinct table name avoids conflating the
// global pool row with a live listing row.
type SellerChannelModel struct {
	ID                        int64  `gorm:"primaryKey"`
	ChannelID                 int64  `gorm:"not null;uniqueIndex:uk_mp_seller_channel_models,priority:1"`
	Model                     string `gorm:"size:128;not null;uniqueIndex:uk_mp_seller_channel_models,priority:2"`
	UpstreamModel             string `gorm:"size:128;not null;default:''"`
	AllowedEndpoints          string `gorm:"type:text;not null;default:''"` // JSON array of protocol ids
	MultiplierMilli           int64  `gorm:"not null"`
	Enabled                   bool   `gorm:"not null"`
	VerifiedOpenAIChat        bool   `gorm:"not null;default:false"`
	VerifiedOpenAIResponses   bool   `gorm:"not null;default:false"`
	VerifiedAnthropicMessages bool   `gorm:"not null;default:false"`
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

func (SellerChannelModel) TableName() string { return "mp_seller_channel_models" }

// MultiplierChange is the append-only history of a seller channel's per-model
// multiplier edits.
type MultiplierChange struct {
	ID                 int64  `gorm:"primaryKey"`
	ChannelID          int64  `gorm:"not null;index"`
	Model              string `gorm:"size:128;not null;default:'';index"`
	OldMultiplierMilli int64  `gorm:"not null"`
	NewMultiplierMilli int64  `gorm:"not null"`
	ActorID            int64  `gorm:"not null;default:0"`
	Notes              string `gorm:"type:text;not null;default:''"`
	CreatedAt          time.Time
}

func (MultiplierChange) TableName() string { return "mp_multiplier_changes" }

// Listing audit actions. Price changes are recorded in mp_multiplier_changes
// instead; everything else that changes a listing's sellable state lands here.
const (
	ListingAuditDelete        = "delete"
	ListingAuditUpdateLimits  = "update_limits"
	ListingAuditModelEnable   = "model_enable"
	ListingAuditModelDisable  = "model_disable"
	ListingAuditModelRestore  = "model_restore"
	ListingAuditHeadersSubmit = "headers_submit"
	ListingAuditHeadersApply  = "headers_apply"
	ListingAuditHeadersReject = "headers_reject"
	ListingAuditAutoDisable   = "auto_disable"
	ListingAuditAutoPause     = "auto_pause"
)

// ListingAudit is the append-only trail of seller/operator actions that are
// not price changes: delete, limits update, model enable/disable/restore,
// header submission/apply/reject, automatic pause/disable. Price changes keep
// their dedicated mp_multiplier_changes row.
type ListingAudit struct {
	ID        int64     `gorm:"primaryKey"`
	ListingID int64     `gorm:"not null;index"`
	ActorID   int64     `gorm:"not null;default:0;index"`
	Action    string    `gorm:"size:32;not null;index"`
	Detail    string    `gorm:"type:text;not null;default:''"`
	CreatedAt time.Time `gorm:"index"`
}

func (ListingAudit) TableName() string { return "mp_listing_audit" }

// Probe is one verification / patrol / report check against a listing (§3.5).
type Probe struct {
	ID        int64  `gorm:"primaryKey"`
	ListingID int64  `gorm:"not null;index"`
	Type      string `gorm:"size:16;not null"` // verify / patrol / report
	OK        bool
	LatencyMs int64
	Detail    string `gorm:"type:text"`
	CreatedAt time.Time
}

func (Probe) TableName() string { return "mp_probes" }

// Key preferences strategies (§3.6).
const (
	StrategyCheapest = "cheapest"
	StrategyBalanced = "balanced"
	StrategyStable   = "stable"
)

// KeyPref holds a buyer token's marketplace routing preferences (§3.6).
// TokenID references the upstream tokens table.
type KeyPref struct {
	ID       int64  `gorm:"primaryKey"`
	TokenID  int64  `gorm:"not null;uniqueIndex"`
	Strategy string `gorm:"size:16;not null"`
	// ModelScope is "all" (every marketplace model) or "selected" (only the
	// rows in mp_key_models). An empty value is treated as "selected" for
	// keys created before the column existed.
	ModelScope      string  `gorm:"size:16;not null;default:selected"`
	PriceCapMilli   int64   `gorm:"not null"` // 0 = no cap
	PreferSellerIDs []int64 `gorm:"type:text;serializer:json"`
	BlockSellerIDs  []int64 `gorm:"type:text;serializer:json"`
	OnlySellerID    int64   `gorm:"not null"` // >0 = 只用某一家
	AllowFallback   bool
	// RPM is the buyer-side requests-per-minute limit of this key (§3.6:
	// 每个买家有自己的 RPM 限制); 0 = unlimited.
	RPM int `gorm:"not null"`
	// OfficialOnly restricts this key to official channels
	// (MODEL-CHANNEL-POOL §3.2).
	OfficialOnly bool `gorm:"not null;default:false"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (KeyPref) TableName() string { return "mp_key_prefs" }

// KeyModel is one model a buyer added to their key (§3.6).
type KeyModel struct {
	ID            int64  `gorm:"primaryKey"`
	TokenID       int64  `gorm:"not null;uniqueIndex:uk_mp_key_models,priority:1;index"`
	Model         string `gorm:"size:128;not null;uniqueIndex:uk_mp_key_models,priority:2"`
	PriceCapMilli int64  `gorm:"not null"` // 0 = inherit key-level cap
	CreatedAt     time.Time
}

func (KeyModel) TableName() string { return "mp_key_models" }

// Request statuses (§3.7/§3.8).
const (
	RequestSuccess = "success"
	RequestFailed  = "failed"
)

// Request is the single marketplace record of one buyer request (§3.8):
// retries and circuit-breaker events fold into RetryChain.
type Request struct {
	ID           int64  `gorm:"primaryKey"`
	RequestID    string `gorm:"size:64;not null;uniqueIndex"`
	BuyerUserID  int64  `gorm:"not null;index"`
	BuyerTokenID int64  `gorm:"not null;index"`
	SellerID     int64  `gorm:"not null;index"`
	ListingID    int64  `gorm:"not null;index;index:idx_mp_requests_listing_created,priority:1"`
	// P3 shadow identity: ChannelID mirrors ListingID (mp_seller_channels.id),
	// KeyID is the mp_channel_keys row (0 until multi-key supply exists), and
	// Endpoint is the buyer protocol the request was served on.
	ChannelID            int64  `gorm:"not null;default:0;index"`
	KeyID                int64  `gorm:"not null;default:0"`
	Endpoint             string `gorm:"size:32;not null;default:''"`
	ModelMultiplierMilli int64  `gorm:"not null;default:0"`
	Converted            bool   `gorm:"not null;default:false"`
	Model                string `gorm:"size:128;not null;index"`
	// UpstreamModel is the raw model id the upstream returned in the response
	// body before the marketplace rewrote it to Model (§3.4). Empty when the
	// response carried no model (for example an all-failed request).
	UpstreamModel string `gorm:"size:128;not null;default:''"`
	// ModelFingerprintAnomaly marks that the upstream returned a model id that
	// is neither the selected upstream name nor a date-suffixed variant of it;
	// such a response is not rewritten (§3.4).
	ModelFingerprintAnomaly bool   `gorm:"not null;default:false"`
	Tier                    string `gorm:"size:16;not null"`
	PriceVersion            int64  `gorm:"not null"`
	MultiplierMilli         int64  `gorm:"not null"`
	InputTokens             int64
	OutputTokens            int64
	CacheReadTokens         int64
	CacheWriteTokens        int64
	SearchCount             int64
	FetchCount              int64
	TokenDebitMicro         int64
	ToolDebitMicro          int64
	TotalDebitMicro         int64
	// GiftDebitMicro is the part of TotalDebitMicro paid with gifted balance
	// (redemption / invitation / admin grants). Seller income generated by a
	// gift-funded request is not withdrawable (payout.go).
	GiftDebitMicro int64 `gorm:"not null;default:0"`
	// CappedMicro is the usage charge above the pre-freeze that the platform
	// absorbed instead of collecting from the buyer (Task: settlement cap).
	CappedMicro       int64 `gorm:"not null;default:0"`
	PlatformFeeMicro  int64
	SellerCreditMicro int64
	OfficialCostMicro int64  // full official price, for 省了多少 (§3.8)
	UsageEstimated    bool   // upstream returned no usage; tokens estimated (§3.7)
	Status            string `gorm:"size:16;not null;index"`
	RetryChain        string `gorm:"type:text"` // JSON array of hop events
	// LatencyMs is the wall-clock duration of the relay attempt (request
	// received → response body finished). The public square reports its 24h
	// average; 0 means "not measured" (rows written before the field existed).
	LatencyMs int64     `gorm:"not null;default:0"`
	CreatedAt time.Time `gorm:"index;index:idx_mp_requests_listing_created,priority:2"`
}

func (Request) TableName() string { return "mp_requests" }

// Ledger entry types (§3.9/§4.2).
const (
	EntryBuyerDebit   = "buyer_debit"
	EntryToolDebit    = "tool_debit"
	EntrySellerCredit = "seller_credit"
	EntryPlatformFee  = "platform_fee"
	EntryRefund       = "refund"
	EntryReversal     = "reversal"
	// EntryFeeReversal compensates EntryPlatformFee on a 冲正; the "…" in the
	// §4.2 type list leaves room for it.
	EntryFeeReversal = "fee_reversal"
)

// Ledger entry statuses.
const (
	LedgerPending   = "pending"
	LedgerAvailable = "available"
	LedgerReversed  = "reversed"
)

// LedgerEntry is one row of the marketplace double-entry ledger (§3.9).
// Invariants: (request_id, entry_type) is unique; the signed AmountMicro of
// all rows of one request sums to zero.
type LedgerEntry struct {
	ID        int64  `gorm:"primaryKey"`
	RequestID string `gorm:"size:64;not null;uniqueIndex:uk_mp_ledger_req_type,priority:1;index"`
	EntryType string `gorm:"size:20;not null;uniqueIndex:uk_mp_ledger_req_type,priority:2"`
	Account   string `gorm:"size:40;not null;index"` // "buyer:<id>", "seller:<id>", "platform"; legacy "user:<id>"
	ListingID int64
	// P3 shadow identity, mirroring the request it settles (§4.2).
	ChannelID            int64  `gorm:"not null;default:0;index"`
	KeyID                int64  `gorm:"not null;default:0"`
	Endpoint             string `gorm:"size:32;not null;default:''"`
	ModelMultiplierMilli int64  `gorm:"not null;default:0"`
	AmountMicro          int64  `gorm:"not null"` // signed
	PriceVersion         int64
	MultiplierMilli      int64
	Status               string     `gorm:"size:16;not null;index"`
	AvailableAt          *time.Time `gorm:"index"`
	CreatedAt            time.Time
}

func (LedgerEntry) TableName() string { return "mp_ledger" }

// Response binding backend families. A thinking signature produced by one
// family is only replayable to the same family (§3.7 Responses 状态绑定).
const (
	BackendTypeOpenAI    = "openai"
	BackendTypeAnthropic = "anthropic"
)

// ResponseBinding records where one upstream response id was created, so a
// follow-up request carrying `previous_response_id` is pinned to the same
// listing, endpoint and key instead of being silently re-routed to a supplier
// that has never seen the id (§3.7 Responses 状态绑定).
//
// ExpiresAt mirrors the upstream's own retention window (OpenAI Responses: 30
// days, Anthropic Messages: 1 hour); an expired row is deleted lazily on
// lookup, so no background sweep is required.
type ResponseBinding struct {
	ResponseID     string `gorm:"size:160;primaryKey"`
	ListingID      int64  `gorm:"not null;index"`
	ChannelID      int64  `gorm:"not null"`
	Endpoint       string `gorm:"size:32;not null;default:''"`
	KeyFingerprint string `gorm:"size:128;not null;default:''"`
	BackendType    string `gorm:"size:16;not null;default:'openai'"`
	CreatedAt      time.Time
	ExpiresAt      time.Time `gorm:"not null;index"`
}

func (ResponseBinding) TableName() string { return "mp_response_bindings" }

// AffinityBinding pins one buyer session to one listing (endpoint + key) for a
// short window (P4-A 亲和). It is a performance hint only: the selector still
// applies every filter to the preferred listing, and a full concurrency slot
// may dispatch the request elsewhere without rewriting the recorded relation.
//
// AffinityKey is HMAC(secret, buyer_id ‖ source ‖ value), so a session value
// observed by one buyer never matches another buyer's binding. Endpoint is part
// of the unique key because one session may use different protocols; the pair
// (affinity_key, endpoint) is unique so RecordAffinity can upsert within one
// dialect-independent statement.
type AffinityBinding struct {
	ID          int64  `gorm:"primaryKey"`
	AffinityKey string `gorm:"size:128;not null;uniqueIndex:uk_mp_affinity_key_endpoint,priority:1"`
	BuyerID     int64  `gorm:"not null;index"`
	ListingID   int64  `gorm:"not null"`
	Endpoint    string `gorm:"size:32;not null;default:'';uniqueIndex:uk_mp_affinity_key_endpoint,priority:2"`
	// CreatedAt is the binding's first creation instant and drives the 24-hour
	// absolute upper bound; it never changes on a hit.
	CreatedAt time.Time
	// ExpiresAt is the sliding 1-hour TTL, capped by CreatedAt+24h.
	ExpiresAt time.Time `gorm:"not null;index"`
	LastHitAt time.Time
}

func (AffinityBinding) TableName() string { return "mp_affinity_bindings" }

// Payout statuses (§3.9).
const (
	PayoutPending  = "pending"
	PayoutApproved = "approved"
	PayoutPaid     = "paid"
	PayoutRejected = "rejected"
)

// Payout is one seller withdrawal request (§3.9).
type Payout struct {
	ID             int64  `gorm:"primaryKey"`
	SellerID       int64  `gorm:"not null;index"`
	AmountMicro    int64  `gorm:"not null"`
	Status         string `gorm:"size:16;not null;index"`
	IdempotencyKey string `gorm:"size:80;not null;uniqueIndex"`
	ReviewerID     int64
	ReviewedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (Payout) TableName() string { return "mp_payouts" }

// Trust pool kinds and trust states (P2).
const (
	TrustKindOfficial   = "official"
	TrustKindThirdParty = "third_party"
	TrustKindPlan       = "plan"

	TrustTrusted  = "trusted"
	TrustWatch    = "watch"
	TrustBlocked  = "blocked"
	TrustUnlisted = "unlisted" // not a stored state: no pool row matched
)

// TrustPool is one certified upstream address (P2). A match is host-exact or,
// when no exact host row matches, an anchored host_pattern regex; path_prefix
// and key_prefix narrow a row to one mount point or one credential class.
//
// Every JSON field is a text column so the schema is identical on SQLite,
// MySQL and PostgreSQL. The operator maintains allowed_models explicitly; the
// code never infers sellable models from a channel mapping.
type TrustPool struct {
	ID          int64  `gorm:"primaryKey" json:"id"`
	Host        string `gorm:"size:255;not null;default:'';index" json:"host"`
	HostPattern string `gorm:"size:512;not null;default:'';index" json:"host_pattern"`
	PathPrefix  string `gorm:"size:512;not null;default:''" json:"path_prefix"`
	KeyPrefix   string `gorm:"size:128;not null;default:''" json:"key_prefix"`
	VendorSlug  string `gorm:"size:64;not null;default:'';index" json:"vendor_slug"`
	// Kind is official / third_party / plan.
	Kind string `gorm:"size:32;not null;default:'third_party'" json:"kind"`
	// Trust is trusted / watch / blocked.
	Trust string `gorm:"size:16;not null;default:'watch';index" json:"trust"`
	// Endpoints is a JSON array of the endpoint kinds this address serves
	// (openai_chat / openai_responses / anthropic_messages).
	Endpoints string `gorm:"type:text;not null;default:''" json:"endpoints"`
	// AllowedModels is a JSON array (or object mapping) of the platform models
	// the operator explicitly allows on this address.
	AllowedModels string `gorm:"type:text;not null;default:''" json:"allowed_models"`
	// OfficialPriceBookID points at the official reference price book row.
	OfficialPriceBookID int64 `gorm:"not null;default:0" json:"official_price_book_id"`
	// QuotaWindowTemplate is a JSON quota-window template (plan channels).
	QuotaWindowTemplate string `gorm:"type:text;not null;default:''" json:"quota_window_template"`
	// DenyKeyPrefixes / DenyPaths are JSON arrays of hard refusal rules.
	DenyKeyPrefixes string `gorm:"type:text;not null;default:''" json:"deny_key_prefixes"`
	DenyPaths       string `gorm:"type:text;not null;default:''" json:"deny_paths"`
	// ErrorCodeTable is a JSON object classifying the vendor's error codes into
	// auth / balance / ratelimit / content.
	ErrorCodeTable string `gorm:"type:text;not null;default:''" json:"error_code_table"`
	// DefaultHeaders is the JSON header policy for requests to this address.
	DefaultHeaders string `gorm:"type:text;not null;default:''" json:"default_headers"`
	// AccountFingerprintRules is a JSON description of the upstream account
	// fingerprint rules used to spot shared/resold plan accounts.
	AccountFingerprintRules string `gorm:"type:text;not null;default:''" json:"account_fingerprint_rules"`
	// Source records where the row came from: seed / manual / probe.
	Source string `gorm:"size:16;not null;default:'manual'" json:"source"`
	Notes  string `gorm:"type:text;not null;default:''" json:"notes"`
	// ExpiresAt bounds a trusted row's validity. A nil value means no expiry is
	// set; P5 governance owns enforcing required expiries for official rows.
	ExpiresAt *time.Time `gorm:"index" json:"expires_at,omitempty"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (TrustPool) TableName() string { return "mp_trust_pool" }

// Trust pool audit actions (P5). The audit trail is append-only: rows are only
// ever inserted, in the same transaction as the change they describe.
const (
	TrustPoolAuditCreate = "create"
	TrustPoolAuditUpdate = "update"
	TrustPoolAuditDelete = "delete"
)

// TrustPoolAudit is one operator change to a trust-pool row (P5). Before and
// After are the full JSON snapshots of the row (never key material); both are
// empty for the create/delete side that does not exist. Notes mirrors the
// row's notes so a blocked-row reason is part of the trail.
type TrustPoolAudit struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	TrustPoolID int64     `gorm:"not null;index" json:"trust_pool_id"`
	ActorID     int64     `gorm:"not null;index" json:"actor_id"`
	Action      string    `gorm:"size:16;not null;index" json:"action"`
	Before      string    `gorm:"type:text;not null" json:"before"`
	After       string    `gorm:"type:text;not null" json:"after"`
	Notes       string    `gorm:"type:text;not null" json:"notes"`
	CreatedAt   time.Time `gorm:"index" json:"created_at"`
}

func (TrustPoolAudit) TableName() string { return "mp_trust_pool_audit" }

// Trust-pool approval actions and statuses (P5-B). Every create/update/delete
// of a trust-pool row is submitted as a pending change; a second operator must
// approve it before it takes effect. change_trust marks an update that flips
// the row's trust state, so the console can highlight the governance-sensitive
// case while both are applied identically.
const (
	TrustPoolApprovalCreate      = "create"
	TrustPoolApprovalUpdate      = "update"
	TrustPoolApprovalDelete      = "delete"
	TrustPoolApprovalChangeTrust = "change_trust"

	TrustPoolApprovalPending  = "pending"
	TrustPoolApprovalApproved = "approved"
	TrustPoolApprovalRejected = "rejected"
)

// TrustPoolApproval is one pending (or decided) trust-pool change (P5-B).
// Payload is a JSON object with the "before" and "after" TrustPool snapshots;
// the after snapshot is applied verbatim on approval, so the change reviewed by
// the second operator is exactly the change that takes effect. Neither
// snapshot ever contains key material.
type TrustPoolApproval struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	TrustPoolID int64      `gorm:"not null;index" json:"trust_pool_id"`
	Action      string     `gorm:"size:16;not null;index" json:"action"`
	Payload     string     `gorm:"type:text;not null;default:''" json:"payload"`
	SubmitterID int64      `gorm:"not null;index" json:"submitter_id"`
	ApproverID  int64      `gorm:"not null;default:0" json:"approver_id"`
	Status      string     `gorm:"size:16;not null;index" json:"status"`
	Notes       string     `gorm:"type:text;not null;default:''" json:"notes"`
	CreatedAt   time.Time  `gorm:"index" json:"created_at"`
	ApprovedAt  *time.Time `gorm:"index" json:"approved_at,omitempty"`
}

func (TrustPoolApproval) TableName() string { return "mp_trust_pool_approvals" }

// Ops metric names (P5). Every row of mp_ops_metrics is one derived metric of
// one time bucket: a request-derived rate/count aggregated from mp_requests, a
// process-local slot snapshot, or the pre-freeze deviation total.
const (
	MetricStreamCutRate      = "stream_cut_rate"
	Metric503Reason          = "503_reason"
	MetricFingerprintAnomaly = "fingerprint_anomaly"
	MetricSubscriptionSignal = "subscription_signal"
	MetricSlotUsage          = "slot_usage"
	MetricSlotRenewFailure   = "slot_renew_failure"
	MetricPreFreezeDeviation = "pre_freeze_deviation"
)

// OpsMetric is one derived operations metric of one bucket (§5 P5). The row is
// append-only: the hourly aggregation replaces the rows of the bucket it
// recomputes, so a re-run is idempotent.
//
// Dimensions is a JSON object (text so SQLite, MySQL and PostgreSQL share the
// same schema) carrying the breakdown of the value, for example
// {"listing_id":123,"endpoint":"openai_chat","reason":"content_filter"}.
type OpsMetric struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	MetricName  string    `gorm:"size:64;not null;index" json:"metric_name"`
	MetricValue float64   `gorm:"not null" json:"metric_value"`
	Dimensions  string    `gorm:"type:text;not null;default:''" json:"dimensions"`
	BucketTime  time.Time `gorm:"not null;index" json:"bucket_time"`
	CreatedAt   time.Time `json:"created_at"`
}

func (OpsMetric) TableName() string { return "mp_ops_metrics" }
