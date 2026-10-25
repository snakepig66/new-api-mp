package marketplace

import "time"

// Table definitions follow MARKETPLACE-DEV.md §4.2. All tables carry the mp_
// prefix and live in the primary database alongside upstream tables. Money is
// stored as integer 微元 (Micro); no float columns exist anywhere.

// Listing lifecycle (§3.5): draft → verifying → canary → live ⇄ paused → banned.
const (
	ListingDraft     = "draft"
	ListingVerifying = "verifying"
	ListingCanary    = "canary"
	ListingLive      = "live"
	ListingPaused    = "paused"
	ListingBanned    = "banned"
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
}

func (Listing) TableName() string { return "mp_listings" }

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
	ID              int64   `gorm:"primaryKey"`
	TokenID         int64   `gorm:"not null;uniqueIndex"`
	Strategy        string  `gorm:"size:16;not null"`
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
	ID                int64  `gorm:"primaryKey"`
	RequestID         string `gorm:"size:64;not null;uniqueIndex"`
	BuyerUserID       int64  `gorm:"not null;index"`
	BuyerTokenID      int64  `gorm:"not null;index"`
	SellerID          int64  `gorm:"not null;index"`
	ListingID         int64  `gorm:"not null;index"`
	Model             string `gorm:"size:128;not null;index"`
	Tier              string `gorm:"size:16;not null"`
	PriceVersion      int64  `gorm:"not null"`
	MultiplierMilli   int64  `gorm:"not null"`
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheWriteTokens  int64
	SearchCount       int64
	FetchCount        int64
	TokenDebitMicro   int64
	ToolDebitMicro    int64
	TotalDebitMicro   int64
	PlatformFeeMicro  int64
	SellerCreditMicro int64
	OfficialCostMicro int64     // full official price, for 省了多少 (§3.8)
	UsageEstimated    bool      // upstream returned no usage; tokens estimated (§3.7)
	Status            string    `gorm:"size:16;not null;index"`
	RetryChain        string    `gorm:"type:text"` // JSON array of hop events
	CreatedAt         time.Time `gorm:"index"`
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
	ID              int64  `gorm:"primaryKey"`
	RequestID       string `gorm:"size:64;not null;uniqueIndex:uk_mp_ledger_req_type,priority:1;index"`
	EntryType       string `gorm:"size:20;not null;uniqueIndex:uk_mp_ledger_req_type,priority:2"`
	Account         string `gorm:"size:40;not null;index"` // "buyer:<id>", "seller:<id>", "platform"; legacy "user:<id>"
	ListingID       int64
	AmountMicro     int64 `gorm:"not null"` // signed
	PriceVersion    int64
	MultiplierMilli int64
	Status          string     `gorm:"size:16;not null;index"`
	AvailableAt     *time.Time `gorm:"index"`
	CreatedAt       time.Time
}

func (LedgerEntry) TableName() string { return "mp_ledger" }

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
