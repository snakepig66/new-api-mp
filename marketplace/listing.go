package marketplace

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
)

// Listing write-path errors (§3.5). No error message from this file ever
// contains an API key, a key ciphertext or a full key fingerprint (§2.3);
// the fingerprint's first 8 hex chars may identify a listing's key.
var (
	ErrDuplicateKey      = errors.New("marketplace: this api key is already listed")
	ErrInvalidTransition = errors.New("marketplace: invalid listing status transition")
	ErrMultiplierTooSoon = errors.New("marketplace: multiplier was changed less than 10 minutes ago")
	ErrListingNotFound   = errors.New("marketplace: listing not found")
	// ErrKeyBannedDeleted marks a key whose earlier listing was banned and then
	// deleted. The ban follows the fingerprint, so the key can never be
	// re-listed under a new channel.
	ErrKeyBannedDeleted = errors.New("marketplace: this key was banned and cannot be listed again")
	// ErrListingGroupNotFound is returned when a group-scoped operation names a
	// group that does not exist or is not owned by the caller.
	ErrListingGroupNotFound = errors.New("marketplace: listing group not found")
	// ErrGroupKeyLimit is returned when a group already holds the maximum
	// number of keys (§P4: 每个渠道最多 20 个 Key).
	ErrGroupKeyLimit        = errors.New("marketplace: this channel already has the maximum number of keys")
	ErrVendorNotWhitelisted = errors.New("marketplace: vendor is not whitelisted")
	// ErrVendorKeyDenied refuses personal-plan credentials (OFFICIAL-CHANNELS §2).
	ErrVendorKeyDenied = errors.New("marketplace: this key belongs to a personal plan")
	// ErrVendorModelUnmapped refuses a model the vendor has no upstream
	// mapping for (OFFICIAL-CHANNELS §3 channel_model_map).
	ErrVendorModelUnmapped = errors.New("marketplace: model has no upstream mapping for this vendor")
	// ErrVendorModelUnpriced refuses a model without an official price book
	// for this vendor (cloud-hosted models are priced at the original price).
	ErrVendorModelUnpriced = errors.New("marketplace: model has no price book for this vendor")
	// ErrVendorKeyPattern refuses a key that does not match the channel's
	// key_pattern (plan keys pasted into the wrong channel).
	ErrVendorKeyPattern = errors.New("marketplace: key does not match this channel's key pattern")
	// ErrListingSystemPaused refuses a seller resume of a listing the platform
	// paused (patrol failure, migration, plan quota, report). Only a pause the
	// seller initiated may be lifted by the seller.
	ErrListingSystemPaused = errors.New("marketplace: listing was paused by the platform; contact support")
	// ErrPlanTermsRequired refuses a plan-channel listing until the seller
	// explicitly accepts that the plan terms allow sharing (§4). The check is
	// on the request body, not on a stored flag.
	ErrPlanTermsRequired = errors.New("marketplace: plan channel requires accepting the plan terms")
	// ErrVerificationRequired refuses a transition that would promote a
	// listing without a completed, passing verification in this state cycle
	// (fail-closed: an unverified or failed listing can never serve).
	ErrVerificationRequired = errors.New("marketplace: a passing verification is required before this transition")
)

// DuplicateKeyError distinguishes the two ways a key fingerprint can already
// be on the platform (§3.5). The API surfaces the distinction as a stable code
// so the seller console can link to their own listing; the other seller's id,
// name and listing id are never returned (only logged for operators).
type DuplicateKeyError struct {
	// Own reports whether the existing listing belongs to the caller.
	Own bool
	// ListingID is the existing listing's id. It is only exposed to the API
	// when Own is true.
	ListingID int64
	// SellerID is the existing listing's owner. Internal only: it is used for
	// the operator alert and never serialized into an API response.
	SellerID int64
	// FingerprintPrefix is the first 8 hex chars, the only key identifier the
	// console may show (§2.3).
	FingerprintPrefix string
}

func (e *DuplicateKeyError) Error() string {
	if e.Own {
		return fmt.Sprintf("marketplace: this api key is already listed by you (listing %d)", e.ListingID)
	}
	return "marketplace: this api key is already listed by another account"
}

// Unwrap keeps errors.Is(err, ErrDuplicateKey) true for every existing caller.
func (e *DuplicateKeyError) Unwrap() error { return ErrDuplicateKey }

// BeforeUpdate keeps Listing.Models a JSON array when a caller updates the
// column directly, e.g. db.Model(&Listing{}).Update("models", []string{...}).
//
// GORM applies the `serializer:json` tag when it saves a struct, but a column
// update passes Dest as map[string]any and binds the slice itself: a
// single-element slice is stored as a bare value and a longer one expands to
// an invalid row value. Verification narrows the listing's model list through
// that form, so encode it once here. Struct saves are untouched (Dest is not
// a map) and stay on the serializer path.
func (l *Listing) BeforeUpdate(tx *gorm.DB) error {
	if tx == nil || tx.Statement == nil {
		return nil
	}
	set, ok := tx.Statement.Dest.(map[string]any)
	if !ok {
		return nil
	}
	models, ok := set["models"].([]string)
	if !ok {
		return nil
	}
	encoded, err := common.Marshal(models)
	if err != nil {
		return err
	}
	set["models"] = string(encoded)
	return nil
}

// duplicateListingForKey returns a *DuplicateKeyError when fingerprint is
// already listed (optionally excluding one listing id), or nil when it is
// free. A database failure is returned as-is.
func duplicateListingForKey(db *gorm.DB, fingerprint string, sellerID, excludeID int64) error {
	if db == nil || strings.TrimSpace(fingerprint) == "" {
		return nil
	}
	q := db.Select("id", "seller_id").Where("key_fingerprint = ?", fingerprint)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var existing Listing
	err := q.Take(&existing).Error
	if err == nil {
		prefix := fingerprint
		if len(prefix) > 8 {
			prefix = prefix[:8]
		}
		return &DuplicateKeyError{
			Own:               existing.SellerID == sellerID,
			ListingID:         existing.ID,
			SellerID:          existing.SellerID,
			FingerprintPrefix: prefix,
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// A banned key keeps its ban across deletion: the fingerprint moves to
		// deleted_fingerprint, and it can never be re-listed. A normally
		// deleted key is released and may be listed again.
		var banned Listing
		berr := db.Select("id").
			Where("deleted_fingerprint = ? AND deleted_banned = ?", fingerprint, true).
			Take(&banned).Error
		if berr == nil {
			return ErrKeyBannedDeleted
		}
		if !errors.Is(berr, gorm.ErrRecordNotFound) {
			return berr
		}
		return nil
	}
	return err
}

const (
	// defaultListingConcurrency is the §3.5 default concurrent-slot count
	// applied when the seller does not set one.
	defaultListingConcurrency = 5
	// multiplierMinInterval is the §3.5 cooldown between two multiplier
	// changes; a change only affects new requests and never re-verifies.
	multiplierMinInterval = 10 * time.Minute
	// maxGroupKeys is the §P4 cap on keys (listings) in one channel group.
	maxGroupKeys = 20
)

// CreateListingInput carries everything needed to open one listing (§3.5).
// APIKey appears here only: it is encrypted and fingerprinted, and the
// plaintext is never stored, logged or returned.
type CreateListingInput struct {
	SellerID        int64
	VendorID        string
	APIKey          string   // plaintext; input only
	Models          []string // must be a subset of the vendor's Models
	MultiplierMilli int64    // 1..1000 (§3.2)
	Concurrency     int      // <= 0 → defaultListingConcurrency
	RPM             int      // 0 = unlimited
	DailyCapMicro   int64    // 0 = unlimited
	Hours           string   // JSON windows; empty = all day
	// Label is the seller's optional channel name (≤64 chars).
	Label string
	// PlanTermsAccepted is the seller's explicit acknowledgement that a plan
	// channel's terms allow sharing (required for kind=plan, §4).
	PlanTermsAccepted bool
	// CustomBaseURL opens a seller-defined third-party channel when VendorID
	// is empty: the platform provisions a private channel row for the URL and
	// derives the vendor id from it (custom_channel.go).
	CustomBaseURL string
	// KeyStrategy / Affinity configure the P4 listing group when this create
	// request also creates it. Joining an existing group keeps that group's
	// settings. Empty KeyStrategy defaults to weighted_round_robin and a nil
	// Affinity defaults to true.
	KeyStrategy string
	Affinity    *bool
	// Weight is this key's relative share inside a weighted_round_robin group
	// (0 → 100).
	Weight int
	// UpstreamModels maps a standard model id to the seller's upstream alias
	// (the name the key's /models exposes). Missing entries fall back to a
	// legacy channel mapping or the standard id.
	UpstreamModels map[string]string
	// ModelMultipliers optionally overrides MultiplierMilli per model; each
	// value is validated against the channel's bounds.
	ModelMultipliers map[string]int64
}

// normalizeKeyStrategy maps an arbitrary input onto the two supported group
// strategies; an unknown or empty value falls back to weighted round robin.
func normalizeKeyStrategy(strategy string) string {
	switch strings.TrimSpace(strategy) {
	case KeyStrategyPrimaryBackup:
		return KeyStrategyPrimaryBackup
	default:
		return KeyStrategyWeightedRoundRobin
	}
}

// ensureListingGroup returns the seller's group for vendor, creating it with
// the supplied defaults when none exists. It runs outside the listing
// transaction on purpose: a concurrent first-create that loses the unique
// (seller, vendor) race can then re-read the winner instead of aborting the
// caller's transaction (PostgreSQL marks a transaction failed after a
// duplicate key). An occasionally empty group is harmless.
func ensureListingGroup(db *gorm.DB, sellerID int64, vendor, strategy string, affinity bool) (ListingGroup, error) {
	var group ListingGroup
	err := db.Where("seller_id = ? AND vendor_slug = ?", sellerID, vendor).Take(&group).Error
	if err == nil {
		return group, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return ListingGroup{}, err
	}
	group = ListingGroup{
		SellerID:        sellerID,
		VendorSlug:      vendor,
		KeyStrategy:     normalizeKeyStrategy(strategy),
		AffinityEnabled: affinity,
	}
	if err := db.Create(&group).Error; err != nil {
		if isDuplicateKeyError(err) {
			if again := db.Where("seller_id = ? AND vendor_slug = ?", sellerID, vendor).Take(&group).Error; again == nil {
				return group, nil
			}
		}
		return ListingGroup{}, err
	}
	return group, nil
}

// UpdateListingGroup changes one group's routing strategy, affinity switch and
// channel-level limits. All limits are non-negative; 0 means unlimited.
func UpdateListingGroup(db *gorm.DB, id int64, keyStrategy string, affinity bool, concurrency, rpm int, dailyCap, cumulativeCap int64, now time.Time) error {
	if concurrency < 0 || rpm < 0 || dailyCap < 0 || cumulativeCap < 0 {
		return errors.New("marketplace: group limits must be >= 0")
	}
	// MySQL reports changed rows, not matched rows, so an idempotent update
	// would otherwise look like a missing group.
	var count int64
	if err := db.Model(&ListingGroup{}).Where("id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrListingGroupNotFound
	}
	res := db.Model(&ListingGroup{}).Where("id = ?", id).Updates(map[string]any{
		"key_strategy":                 normalizeKeyStrategy(keyStrategy),
		"affinity_enabled":             affinity,
		"channel_concurrency_limit":    concurrency,
		"channel_rpm_limit":            rpm,
		"channel_daily_cap_micro":      dailyCap,
		"channel_cumulative_cap_micro": cumulativeCap,
		"updated_at":                   now,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrListingGroupNotFound
	}
	return nil
}

// UpdateModelMultiplier is the P4-compatible entry point: it applies the new
// per-model price rules (decrease immediate, increase staged) without an
// ownership check. Seller-facing callers use SetListingModelsState directly so
// the actor is recorded in the audit trail.
func UpdateModelMultiplier(db *gorm.DB, listingID int64, model string, milli int64, now time.Time) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("marketplace: model is required")
	}
	_, err := SetListingModelsState(db, listingID, 0, []string{model},
		ModelStateInput{MultiplierMilli: &milli}, now)
	return err
}

// validate checks every field that does not need the database. The vendor is
// resolved by the caller from the pool (or the test-only registry fallback) so
// this method never depends on global state. Errors name the offending field
// but never echo the API key.
func (in CreateListingInput) validate(vendor Vendor) (Vendor, error) {
	if strings.TrimSpace(in.VendorID) == "" {
		return Vendor{}, fmt.Errorf("%w: empty id", ErrVendorNotWhitelisted)
	}
	if in.SellerID <= 0 {
		return Vendor{}, errors.New("marketplace: seller id is required")
	}
	if _, err := normalizeListingKey(in.APIKey); err != nil {
		// Empty after trimming, or whitespace/control characters inside the
		// key. Prefixing/suffixing spaces must not produce a second listing
		// for the same upstream credential (M5); the message never echoes it.
		return Vendor{}, err
	}
	// §2 of OFFICIAL-CHANNELS: personal-plan keys are refused before storage,
	// so a coding/token-plan subscription can never be resold through the
	// marketplace. The message names the plan, never the key.
	if VendorKeyDenied(vendor, in.APIKey) {
		return Vendor{}, fmt.Errorf("%w: %q refuses personal-plan keys", ErrVendorKeyDenied, vendor.ID)
	}
	// Plan channels are subscription quotas; sharing them is only allowed when
	// the seller confirms the plan's own terms permit it (§4). The flag is
	// explicit on every request, never inferred.
	if vendor.Tier == ChannelKindPlan && !in.PlanTermsAccepted {
		return Vendor{}, fmt.Errorf("%w: channel %q", ErrPlanTermsRequired, vendor.ID)
	}
	if len(in.Models) == 0 {
		return Vendor{}, errors.New("marketplace: at least one model is required")
	}
	// Which models may be listed is decided by the platform model pool and
	// its official prices (CreateListing), not by a per-vendor whitelist.
	// Every protocol URL must stay outside the denied personal-plan paths;
	// this is a whitelist invariant (sellers cannot supply URLs).
	for _, u := range []string{vendor.Protocols.OpenAI, vendor.Protocols.Anthropic} {
		if parsed, err := url.Parse(u); err == nil && VendorPathDenied(vendor, parsed.Path) {
			return Vendor{}, fmt.Errorf("marketplace: vendor %q base url hits a denied path", vendor.ID)
		}
	}
	minMultiplier, maxMultiplier := MultiplierBoundsForTier(vendor.Tier)
	if in.MultiplierMilli < minMultiplier || in.MultiplierMilli > maxMultiplier {
		return Vendor{}, fmt.Errorf("marketplace: multiplier must be %d..%d milli for channel %q, got %d",
			minMultiplier, maxMultiplier, vendor.ID, in.MultiplierMilli)
	}
	if in.RPM < 0 || in.DailyCapMicro < 0 {
		return Vendor{}, errors.New("marketplace: rpm and daily cap must be >= 0")
	}
	if in.Weight < 0 || in.Weight > 10000 {
		return Vendor{}, fmt.Errorf("marketplace: weight must be 0..10000, got %d", in.Weight)
	}
	if err := ValidateHours(in.Hours); err != nil {
		return Vendor{}, err
	}
	return vendor, nil
}

// dedupModels returns the input models without duplicates, order preserved.
func dedupModels(models []string) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// CreateListing opens one listing in draft status (§3.5): it validates the
// request, encrypts the key, computes its fingerprint, rejects a key already
// listed anywhere on the platform, creates the disabled upstream channel row
// and finally the listing itself, all in one transaction. The channel's Key
// column holds only the "mp-managed:<fp8>" placeholder — the real key lives
// solely in Listing.KeyCiphertext.
//
// A seller-defined upstream that the trust pool classifies as watch or
// unlisted is created in pending_review instead of draft, so no automated
// verification can promote it.
func CreateListing(db *gorm.DB, in CreateListingInput) (Listing, error) {
	label, err := validateListingLabel(in.Label)
	if err != nil {
		return Listing{}, err
	}
	// A custom upstream is classified by the trust pool before any channel row
	// is provisioned: blocked addresses are refused, watch/unlisted addresses
	// may be listed but stay in pending_review until an operator approves them,
	// and a trusted address keeps the automated verification flow. Platform
	// channels (VendorID set) are already governed by mp_channels, so their
	// existing flow is unchanged.
	trust := TrustTrusted
	// Seller-defined upstream: provision the private third-party channel first
	// and derive the vendor id from it, then run the standard flow unchanged.
	if strings.TrimSpace(in.VendorID) == "" && strings.TrimSpace(in.CustomBaseURL) != "" {
		base, host, err := validateCustomBaseURL(in.CustomBaseURL)
		if err != nil {
			return Listing{}, err
		}
		// The credential is passed whole: a key_prefix row matches when its
		// configured prefix is a prefix of it (trustpool.go).
		trust, _, err = CheckTrustPoolDB(db, host, customBasePath(base), strings.TrimSpace(in.APIKey))
		if err != nil {
			return Listing{}, err
		}
		if trust == TrustBlocked {
			return Listing{}, fmt.Errorf("%w: host %q", ErrTrustPoolBlocked, host)
		}
		slug, err := ensureCustomChannel(db, in.SellerID, base, in.Models)
		if err != nil {
			return Listing{}, err
		}
		in.VendorID = slug
	}
	// A custom channel is private to its owner: a slug that names another
	// seller's upstream is refused even though the hash makes it unguessable.
	if isCustomChannelSlug(in.VendorID) {
		if owner, ok := customChannelOwner(in.VendorID); !ok || owner != in.SellerID {
			return Listing{}, fmt.Errorf("%w: %q", ErrVendorNotWhitelisted, in.VendorID)
		}
	}
	vendor, ok := lookupVendorForDB(db, in.VendorID)
	if !ok {
		return Listing{}, fmt.Errorf("%w: %q", ErrVendorNotWhitelisted, in.VendorID)
	}
	vendor, err = in.validate(vendor)
	if err != nil {
		return Listing{}, err
	}
	ciphertext, err := EncryptListingKey(in.APIKey)
	if err != nil {
		return Listing{}, err
	}
	fingerprint, err := ListingKeyFingerprint(in.APIKey)
	if err != nil {
		return Listing{}, err
	}
	models := dedupModels(in.Models)

	// MODEL-CHANNEL-POOL §3.2: a listing may only include certified,
	// sellable channel-model pairs, and the official price book must cover the
	// standard model under its original vendor. Admin-editable; nothing here
	// guesses a name.
	// ShareLLM-style: the seller picks any standard pool model with an official
	// flat price; the upstream name is the seller's alias, else a legacy channel
	// mapping, else the standard id. Each model is still verified by a real call.
	channelMapping := make(map[string]string, len(models))
	perModelMultiplier := make(map[string]int64, len(models))
	for _, m := range models {
		if _, ok := sellableModelPrice(db, m, time.Now()); !ok {
			return Listing{}, fmt.Errorf("%w: %q is not a priced platform model", ErrVendorModelUnpriced, m)
		}
		upstreamName := strings.TrimSpace(in.UpstreamModels[m])
		if upstreamName == "" {
			if cm, found := ChannelModelFor(db, vendor.ID, m); found && strings.TrimSpace(cm.UpstreamModel) != "" {
				upstreamName = strings.TrimSpace(cm.UpstreamModel)
			} else {
				upstreamName = m
			}
		}
		origin := vendor.ID
		if model, found := ModelPoolByName(db, m); found && strings.TrimSpace(model.OriginVendor) != "" {
			origin = model.OriginVendor
		}
		var priced int64
		if err := db.Model(&PriceBook{}).
			Where("model = ? AND vendor = ? AND effective_at <= ?", m, origin, time.Now()).
			Count(&priced).Error; err != nil {
			return Listing{}, err
		}
		if priced == 0 {
			return Listing{}, fmt.Errorf("%w: %q has no official price", ErrVendorModelUnpriced, m)
		}
		channelMapping[m] = upstreamName
		perModelMultiplier[m] = in.MultiplierMilli
		if v, ok := in.ModelMultipliers[m]; ok && v > 0 {
			if err := validateMultiplierForVendor(db, vendor.ID, v); err != nil {
				return Listing{}, err
			}
			perModelMultiplier[m] = v
		}
	}
	// Plan channels carry a key pattern; an official-plan key pasted into the
	// wrong channel is rejected before it is encrypted (MODEL-CHANNEL-POOL §4).
	if err := validateChannelAPIKey(db, vendor.ID, in.APIKey); err != nil {
		return Listing{}, err
	}

	// P4: every listing of one seller on the same vendor joins that seller's
	// group, so its key participates in the group's rotation and shares the
	// channel-level limits. The group is created with this request's defaults
	// when it does not exist yet.
	affinity := true
	if in.Affinity != nil {
		affinity = *in.Affinity
	}
	group, err := ensureListingGroup(db, in.SellerID, vendor.ID, in.KeyStrategy, affinity)
	if err != nil {
		return Listing{}, fmt.Errorf("marketplace: resolve listing group: %w", err)
	}

	var listing Listing
	err = db.Transaction(func(tx *gorm.DB) error {
		if dup := duplicateListingForKey(tx, fingerprint, in.SellerID, 0); dup != nil {
			// §2.3: the errors identify the key by the fingerprint prefix only.
			return dup
		}
		// §P4: a channel holds at most 20 keys. The check is inside the create
		// transaction; the cap is a product rule, not a DB constraint.
		var groupKeys int64
		if err := tx.Model(&Listing{}).Where("group_id = ?", group.ID).Count(&groupKeys).Error; err != nil {
			return err
		}
		if groupKeys >= maxGroupKeys {
			return fmt.Errorf("%w (group %d)", ErrGroupKeyLimit, group.ID)
		}

		// One listing = one upstream channel pinned to the vendor's fixed base
		// URL. The adaptor and path handling come from the vendor definition
		// (OFFICIAL-CHANNELS §4.1); sellers never supply an address.
		//
		// The channel BaseURL is the OpenAI chat endpoint base; the Anthropic
		// messages endpoint base is stored in the channel setting. Each adaptor
		// appends its endpoint's fixed path, so the buyer's request path never
		// decides the upstream URL.
		bases, _ := ChannelPoolEndpointBases(tx, vendor.ID)
		baseURL := strings.TrimSuffix(bases.ChatBase(), "/")
		if baseURL == "" {
			baseURL = VendorOpenAIBase(vendor)
		}
		messagesBase := strings.TrimSuffix(bases.MessagesBase(), "/")
		if messagesBase == "" {
			messagesBase = strings.TrimSuffix(vendor.Protocols.Anthropic, "/")
		}
		channelType := constant.ChannelTypeOpenAI
		if vendor.Adaptor == VendorAdaptorDeepSeek {
			channelType = constant.ChannelTypeDeepSeek
		}
		channel := model.Channel{
			Type:        channelType,
			Key:         "mp-managed:" + fingerprint[:8],
			Name:        fmt.Sprintf("mp:%s:%d", vendor.ID, in.SellerID),
			Status:      common.ChannelStatusManuallyDisabled,
			BaseURL:     &baseURL,
			Models:      strings.Join(models, ","),
			Group:       "default",
			CreatedTime: time.Now().Unix(),
		}
		// The buyer speaks the platform model id; the channel rewrites it to
		// the vendor's upstream name (standard new-api model_mapping).
		if mappingJSON, merr := common.Marshal(channelMapping); merr == nil {
			mappingText := string(mappingJSON)
			channel.ModelMapping = &mappingText
		} else {
			return fmt.Errorf("marketplace: encode model mapping: %w", merr)
		}
		// Multi-protocol vendors may relay the buyer's raw body (native OpenAI +
		// Anthropic) instead of the adaptor's conversion (§3.10). Whether a
		// request is passed through is decided per model at relay time: the
		// marketplace rewrites the raw body's model to the mapped upstream id,
		// so a renamed model is safe too (relayhook.rewritePassthroughBody).
		// Only a model with no mapping skips pass-through and uses conversion.
		setting := map[string]any{}
		if vendor.PassthroughBody {
			setting["pass_through_body_enabled"] = true
		}
		if vendor.StripV1Prefix {
			// Kept for rolling compatibility with older rows; the adaptors no
			// longer consult it (endpoint bases and fixed paths are used).
			setting["mp_strip_v1_prefix"] = true
		}
		if messagesBase != "" {
			setting["mp_anthropic_messages_url"] =
				relayconstant.BuildEndpointURL(messagesBase, relayconstant.EndpointAnthropicMessages)
		}
		if len(setting) > 0 {
			settingJSON, serr := common.Marshal(setting)
			if serr != nil {
				return fmt.Errorf("marketplace: encode channel setting: %w", serr)
			}
			settingText := string(settingJSON)
			channel.Setting = &settingText
		}
		if err := tx.Create(&channel).Error; err != nil {
			return fmt.Errorf("marketplace: create upstream channel: %w", err)
		}

		concurrency := in.Concurrency
		if concurrency <= 0 {
			concurrency = defaultListingConcurrency
		}
		// A watch/unlisted upstream cannot be verified automatically: the
		// listing exists but stays in pending_review until an operator approves
		// it. VerifyListing only accepts draft, so the async verification path
		// cannot promote it (and the host stays out of the relay whitelist until
		// a listing is live/canary).
		status := ListingDraft
		if trust == TrustWatch || trust == TrustUnlisted {
			status = ListingPendingReview
		}
		listing = Listing{
			SellerID:         in.SellerID,
			ChannelID:        int64(channel.Id),
			Vendor:           vendor.ID,
			Models:           models,
			MultiplierMilli:  in.MultiplierMilli,
			ConcurrencyLimit: concurrency,
			RPMLimit:         in.RPM,
			DailyCapMicro:    in.DailyCapMicro,
			Hours:            strings.TrimSpace(in.Hours),
			Label:            label,
			Status:           status,
			KeyFingerprint:   fingerprint,
			KeyCiphertext:    ciphertext,
			Version:          1,
			GroupID:          group.ID,
			Weight:           in.Weight,
		}
		if err := tx.Create(&listing).Error; err != nil {
			// The unique index on key_fingerprint is the concurrency
			// backstop for the lookup above. Return the raw duplicate error;
			// the caller re-resolves who owns the winning row after the
			// transaction (a failed transaction cannot query on PostgreSQL).
			return err
		}
		// P4 逐模型倍率: seed one row per listed model with the listing-level
		// multiplier. The selector and the pricing path read these rows first
		// and fall back to Listing.MultiplierMilli when a model has no row.
		perModel := make([]ListingModel, 0, len(models))
		for _, model := range models {
			perModel = append(perModel, ListingModel{
				ListingID:       listing.ID,
				Model:           model,
				MultiplierMilli: perModelMultiplier[model],
				Enabled:         true,
				// Only an explicit seller alias is pinned on the row; otherwise
				// the legacy channel mapping keeps applying (ListingUpstreamModel).
				UpstreamModel: strings.TrimSpace(in.UpstreamModels[model]),
			})
		}
		if len(perModel) > 0 {
			if err := tx.Create(&perModel).Error; err != nil {
				return fmt.Errorf("marketplace: create listing models: %w", err)
			}
		}
		// P3a shadow double-write: the legacy listing stays authoritative, but
		// the split tables are kept in sync so a later read-path switch has a
		// complete snapshot. Any failure rolls the whole create back.
		if err := createSellerChannelShadow(tx, listing, baseURL, bases, channelMapping); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		var dup *DuplicateKeyError
		if errors.As(err, &dup) {
			return Listing{}, dup
		}
		if isDuplicateKeyError(err) {
			if again := duplicateListingForKey(db, fingerprint, in.SellerID, 0); again != nil {
				return Listing{}, again
			}
			return Listing{}, fmt.Errorf("%w (fp %s)", ErrDuplicateKey, fingerprint[:8])
		}
		return Listing{}, err
	}
	return listing, nil
}

// isDuplicateKeyError reports whether err is a unique-constraint violation on
// any of the three supported dialects (SQLite / MySQL / PostgreSQL). It is
// the fallback for handles without GORM's TranslateError, which would
// otherwise surface as gorm.ErrDuplicatedKey.
func isDuplicateKeyError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || // sqlite
		strings.Contains(msg, "duplicate entry") || // mysql 1062
		strings.Contains(msg, "duplicate key value") // postgres 23505
}

// transitionListingFrom moves one listing from any of the given statuses to
// `to`. It locks the row inside a transaction and relies on the conditional
// UPDATE's affected-row count: anything but exactly one row means the listing
// is not in an allowed from-state (ErrInvalidTransition). A missing listing
// yields ErrListingNotFound.
func transitionListingFrom(db *gorm.DB, id int64, froms []string, to string) error {
	err := db.Transaction(func(tx *gorm.DB) error {
		var row Listing
		if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		res := tx.Model(&Listing{}).
			Where("id = ? AND status IN ?", id, froms).
			Update("status", to)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("%w: %s -> %s (current %s)", ErrInvalidTransition, strings.Join(froms, "|"), to, row.Status)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// P3b: the legacy row is authoritative and has committed; mirroring is a
	// best-effort side effect that never fails the transition.
	mirrorListingUpdateLogged(db, id)
	return nil
}

// TransitionListing moves one listing from status `from` to status `to`; the
// named wrappers below encode the legal edges of the §3.5 state machine
// (draft → verifying → canary → live ⇄ paused → banned). A generic caller may
// not move anything straight into live: live is reachable only from canary,
// the state a passing verification plus the gray-traffic period produces.
func TransitionListing(db *gorm.DB, id int64, from, to string) error {
	if to == ListingLive && from != ListingCanary {
		return fmt.Errorf("%w: %s -> live requires a verified canary", ErrInvalidTransition, from)
	}
	return transitionListingFrom(db, id, []string{from}, to)
}

// StartVerification moves draft → verifying (verification requested). An
// operator-approved pending_review listing takes the same edge: approval is
// what releases it into the automated flow (api_admin.go).
func StartVerification(db *gorm.DB, id int64) error {
	return transitionListingFrom(db, id, []string{ListingDraft, ListingPendingReview}, ListingVerifying)
}

// RejectListing moves pending_review → draft: the operator returned the
// seller-defined upstream for correction. The seller can change the listing
// and submit it again; the ordinary draft flow then re-runs verification.
func RejectListing(db *gorm.DB, id int64) error {
	return transitionListingFrom(db, id, []string{ListingPendingReview}, ListingDraft)
}

// PassVerification moves verifying → canary (key verified, 10% gray traffic).
// It refuses unless the listing has models and the newest type="verify" probe
// passed: the callability and fingerprint checks are evaluated fail-closed in
// VerifyListing, and its verdict is the only evidence that may promote a
// listing. A caller cannot skip the probe by calling the transition directly.
func PassVerification(db *gorm.DB, id int64) error {
	err := db.Transaction(func(tx *gorm.DB) error {
		var row Listing
		if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		if row.Status != ListingVerifying {
			return fmt.Errorf("%w: %s -> canary", ErrInvalidTransition, row.Status)
		}
		if len(row.Models) == 0 {
			return fmt.Errorf("%w: listing has no verified models", ErrVerificationRequired)
		}
		var probe Probe
		err := tx.Where("listing_id = ? AND type = ?", id, ProbeTypeVerify).
			Order("id DESC").Take(&probe).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrVerificationRequired
			}
			return err
		}
		if !probe.OK {
			return fmt.Errorf("%w: the latest verification failed", ErrVerificationRequired)
		}
		res := tx.Model(&Listing{}).
			Where("id = ? AND status = ?", id, ListingVerifying).
			Update("status", ListingCanary)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("%w: verifying -> canary", ErrInvalidTransition)
		}
		return nil
	})
	if err != nil {
		return err
	}
	mirrorListingUpdateLogged(db, id)
	return nil
}

// GoLive moves canary → live (§3.5: ≥98% success over 24h, decided by jobs).
func GoLive(db *gorm.DB, id int64) error {
	return TransitionListing(db, id, ListingCanary, ListingLive)
}

// PauseListing moves live → paused.
func PauseListing(db *gorm.DB, id int64) error {
	return TransitionListing(db, id, ListingLive, ListingPaused)
}

// ResumeListing moves paused → draft. A pause (patrol failure, operator
// action, migration) invalidates the evidence a listing was serving on, so a
// resume must run the complete verification again: draft → verifying →
// canary. It deliberately never restores live directly. It is the
// platform-side edge and does not ask who paused the listing, so only
// jobs/admin paths may use it.
func ResumeListing(db *gorm.DB, id int64) error {
	err := db.Transaction(func(tx *gorm.DB) error {
		var row Listing
		if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		if row.Status != ListingPaused {
			return fmt.Errorf("%w: %s -> draft", ErrInvalidTransition, row.Status)
		}
		res := tx.Model(&Listing{}).
			Where("id = ? AND status = ?", id, ListingPaused).
			Updates(map[string]any{
				"status":          ListingDraft,
				"pause_reason":    "",
				"resume_at":       nil,
				"previous_status": "",
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("%w: paused -> draft", ErrInvalidTransition)
		}
		return nil
	})
	if err != nil {
		return err
	}
	mirrorListingUpdateLogged(db, id)
	return nil
}

// ResumeSellerListing is the seller-facing resume edge: it refuses a listing
// the platform paused (non-empty pause_reason or a pending resume_at), and it
// refuses a listing whose latest verification failed. A seller-initiated
// pause of a certified listing is a pause-lift, not a promotion: the listing
// was live immediately before the pause. Admin/system paths that resume a
// non-certified listing go through ResumeListing (paused → draft), which
// requires a complete re-verification.
func ResumeSellerListing(db *gorm.DB, id int64) error {
	err := db.Transaction(func(tx *gorm.DB) error {
		var row Listing
		if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		if row.Status != ListingPaused {
			return fmt.Errorf("%w: %s -> live", ErrInvalidTransition, row.Status)
		}
		if strings.TrimSpace(row.PauseReason) != "" || row.ResumeAt != nil {
			return ErrListingSystemPaused
		}
		// A seller pause never restores a listing whose last recorded
		// verification failed: that would put an uncertified key back in
		// front of buyers. (A fresh full verification remains the only way to
		// re-certify; this check is a floor, not a substitute for it.)
		var latest Probe
		err := tx.Where("listing_id = ? AND type = ?", id, ProbeTypeVerify).
			Order("id DESC").Take(&latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && !latest.OK {
			return fmt.Errorf("%w: the latest verification failed", ErrVerificationRequired)
		}
		res := tx.Model(&Listing{}).
			Where("id = ? AND status = ? AND (pause_reason IS NULL OR pause_reason = '')", id, ListingPaused).
			Updates(map[string]any{
				"status":          ListingLive,
				"pause_reason":    "",
				"resume_at":       nil,
				"previous_status": "",
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("%w: paused -> live", ErrInvalidTransition)
		}
		return nil
	})
	if err != nil {
		return err
	}
	mirrorListingUpdateLogged(db, id)
	return nil
}

// validateChannelAPIKey applies a pool channel's deny prefixes and key_pattern
// to a plaintext key. It never includes the key in an error.
func validateChannelAPIKey(db *gorm.DB, channelSlug, apiKey string) error {
	if db == nil {
		return nil
	}
	ch, ok := ChannelPoolBySlug(db, channelSlug)
	if !ok {
		return nil
	}
	for _, prefix := range parseStringList(ch.DenyKeyPrefixes) {
		if prefix != "" && strings.HasPrefix(apiKey, prefix) {
			return fmt.Errorf("%w: %q refuses personal-plan keys", ErrVendorKeyDenied, ch.Slug)
		}
	}
	pattern := strings.TrimSpace(ch.KeyPattern)
	if pattern == "" {
		return nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		// An invalid admin pattern must not block every listing; the channel
		// save path already validates it, so treat a stale bad pattern as
		// unset.
		return nil
	}
	if !re.MatchString(apiKey) {
		return ErrVendorKeyPattern
	}
	return nil
}

// RefreshListingChannelMapping rewrites the upstream name of one
// (channel, standard model) pair in every listing channel that carries the
// model. Called after an administrator edits the pool mapping so live listings
// do not keep forwarding the old name until the next restart.
func RefreshListingChannelMapping(db *gorm.DB, channelSlug, modelName, upstream string) error {
	if db == nil {
		return nil
	}
	channelSlug = strings.TrimSpace(channelSlug)
	modelName = strings.TrimSpace(modelName)
	upstream = strings.TrimSpace(upstream)
	if channelSlug == "" || modelName == "" {
		return nil
	}
	var rows []Listing
	if err := db.Select("id", "channel_id", "models").Where("vendor = ?", channelSlug).Find(&rows).Error; err != nil {
		return err
	}
	for _, l := range rows {
		if !slices.Contains(l.Models, modelName) {
			continue
		}
		var ch model.Channel
		if err := db.Select("id", "model_mapping").Where("id = ?", l.ChannelID).Take(&ch).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		mapping := map[string]string{}
		if raw := strings.TrimSpace(ch.GetModelMapping()); raw != "" {
			_ = common.Unmarshal([]byte(raw), &mapping)
		}
		if upstream == "" {
			delete(mapping, modelName)
		} else {
			mapping[modelName] = upstream
		}
		encoded, err := common.Marshal(mapping)
		if err != nil {
			return err
		}
		text := string(encoded)
		if err := db.Model(&model.Channel{}).Where("id = ?", ch.Id).
			Update("model_mapping", text).Error; err != nil {
			return err
		}
	}
	return nil
}

// BanListing moves live/paused/canary/verifying/pending_review → banned.
// Banned is terminal. pending_review is included so an operator can reject a
// listing outright for a severe violation instead of returning it to draft.
func BanListing(db *gorm.DB, id int64) error {
	return transitionListingFrom(db, id,
		[]string{ListingLive, ListingPaused, ListingCanary, ListingVerifying, ListingPendingReview}, ListingBanned)
}

// writeListingAuditTx appends one audit row inside the caller's transaction.
func writeListingAuditTx(tx *gorm.DB, listingID, actorID int64, action string, detail map[string]any) error {
	raw, err := common.Marshal(detail)
	if err != nil {
		raw = []byte("{}")
	}
	return tx.Create(&ListingAudit{
		ListingID: listingID,
		ActorID:   actorID,
		Action:    action,
		Detail:    string(raw),
	}).Error
}

// writeListingAuditLogged is the best-effort form for actions whose main write
// already committed. An audit failure is logged, never surfaced to the seller.
func writeListingAuditLogged(db *gorm.DB, listingID, actorID int64, action string, detail map[string]any) {
	if err := writeListingAuditTx(db, listingID, actorID, action, detail); err != nil {
		common.SysError(common.LogText("marketplace: listing %d audit %s: %s", listingID, action, err.Error()))
	}
}

// DeleteListingForSeller soft-deletes one listing owned by sellerID. The key
// fingerprint moves to deleted_fingerprint and key_fingerprint becomes a
// unique per-row tombstone, freeing the unique value for a legitimate
// re-listing. The ciphertext is deliberately kept for 15 minutes so an
// in-flight request can still finish; a background job wipes it afterwards.
func DeleteListingForSeller(db *gorm.DB, id, sellerID int64, now time.Time) (Listing, error) {
	var out Listing
	err := db.Transaction(func(tx *gorm.DB) error {
		var row Listing
		if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		if row.SellerID != sellerID {
			return ErrListingNotFound
		}
		if row.Status == ListingDeleted {
			out = row
			return nil
		}
		tombstone := fmt.Sprintf("deleted:%d", row.ID)
		wipeAt := now.Add(15 * time.Minute)
		res := tx.Model(&Listing{}).
			Where("id = ? AND version = ?", id, row.Version).
			Updates(map[string]any{
				"deleted_fingerprint":        row.KeyFingerprint,
				"key_fingerprint":            tombstone,
				"deleted_banned":             row.Status == ListingBanned,
				"status":                     ListingDeleted,
				"deleted_at":                 now,
				"key_wipe_at":                wipeAt,
				"pending_headers_ciphertext": nil,
				"pending_headers_version":    0,
				"pending_headers_at":         nil,
				"version":                    row.Version + 1,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("marketplace: listing %d was modified concurrently", id)
		}
		if err := tx.Model(&ListingModel{}).
			Where("listing_id = ?", id).
			Updates(map[string]any{
				"pending_multiplier_milli": 0,
				"pending_effective_at":     nil,
			}).Error; err != nil {
			return err
		}
		// Free the shadow key's unique fingerprint in the same transaction so a
		// concurrent re-listing cannot collide with a soft-deleted row.
		if tx.Migrator().HasTable(&ChannelKey{}) {
			if err := tombstoneChannelKeysTx(tx, id, now); err != nil {
				return err
			}
		}
		if err := writeListingAuditTx(tx, id, sellerID, ListingAuditDelete, map[string]any{
			"previous_status": row.Status,
			"banned":          row.Status == ListingBanned,
			"key_wipe_at":     wipeAt,
		}); err != nil {
			return err
		}
		return tx.Where("id = ?", id).First(&out).Error
	})
	if err != nil {
		return Listing{}, err
	}
	return out, nil
}

// WipeDeletedListingKeys clears the ciphertext of deleted listings whose
// grace period has elapsed. It is safe to run repeatedly and returns the
// number of rows wiped.
func WipeDeletedListingKeys(db *gorm.DB, now time.Time) (int64, error) {
	if db == nil {
		return 0, nil
	}
	res := db.Model(&Listing{}).
		Where("status = ? AND key_wipe_at IS NOT NULL AND key_wipe_at <= ? AND key_ciphertext IS NOT NULL",
			ListingDeleted, now).
		Update("key_ciphertext", nil)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// UpdateListingSettingsInput carries the mutable listing-level settings. Nil
// pointers mean "leave unchanged", so the drawer can submit only dirty fields.
type UpdateListingSettingsInput struct {
	Label            *string
	ConcurrencyLimit *int
	RPMLimit         *int
	DailyCapMicro    *int64
	Hours            *string
}

// UpdateListingSettings applies the mutable listing-level settings of one
// seller-owned listing. The version column is the optimistic-lock backstop.
func UpdateListingSettings(db *gorm.DB, id, sellerID int64, in UpdateListingSettingsInput, now time.Time) (Listing, error) {
	updates := map[string]any{}
	audit := map[string]any{}
	if in.Label != nil {
		label, err := validateListingLabel(*in.Label)
		if err != nil {
			return Listing{}, err
		}
		updates["label"] = label
		audit["label"] = label
	}
	if in.ConcurrencyLimit != nil {
		if *in.ConcurrencyLimit < 1 {
			return Listing{}, errors.New("marketplace: concurrency_limit must be >= 1")
		}
		updates["concurrency_limit"] = *in.ConcurrencyLimit
		audit["concurrency_limit"] = *in.ConcurrencyLimit
	}
	if in.RPMLimit != nil {
		if *in.RPMLimit < 0 {
			return Listing{}, errors.New("marketplace: rpm_limit must be >= 0")
		}
		updates["rpm_limit"] = *in.RPMLimit
		audit["rpm_limit"] = *in.RPMLimit
	}
	if in.DailyCapMicro != nil {
		if *in.DailyCapMicro < 0 {
			return Listing{}, errors.New("marketplace: daily_cap_micro must be >= 0")
		}
		updates["daily_cap_micro"] = *in.DailyCapMicro
		audit["daily_cap_micro"] = *in.DailyCapMicro
	}
	if in.Hours != nil {
		hours := strings.TrimSpace(*in.Hours)
		if err := ValidateHours(hours); err != nil {
			return Listing{}, err
		}
		updates["hours"] = hours
		audit["hours"] = hours
	}
	if len(updates) == 0 {
		return GetListingForSeller(db, id, sellerID)
	}
	var out Listing
	err := db.Transaction(func(tx *gorm.DB) error {
		var row Listing
		if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		if row.SellerID != sellerID || row.Status == ListingDeleted {
			return ErrListingNotFound
		}
		if row.Status == ListingBanned {
			return fmt.Errorf("%w: banned listing cannot be edited", ErrInvalidTransition)
		}
		updates["version"] = row.Version + 1
		res := tx.Model(&Listing{}).Where("id = ? AND version = ?", id, row.Version).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("marketplace: listing %d was modified concurrently", id)
		}
		if err := writeListingAuditTx(tx, id, sellerID, ListingAuditUpdateLimits, audit); err != nil {
			return err
		}
		return tx.Where("id = ?", id).First(&out).Error
	})
	if err != nil {
		return Listing{}, err
	}
	mirrorListingUpdateLogged(db, id)
	resetBuyerModelVisibilityCache()
	return out, nil
}

// UpdateMultiplier changes the listing's 倍率 (§3.5): it never re-verifies
// the key, two changes must be at least 10 minutes apart (measured on
// MultiplierUpdatedAt), and the new value affects only new requests. The
// version field is the optimistic-lock backstop behind the row lock.
func UpdateMultiplier(db *gorm.DB, id int64, milli int64, now time.Time) error {
	if milli < MultiplierAbsoluteMinMilli || milli > MultiplierAbsoluteMaxMilli {
		return fmt.Errorf("marketplace: multiplier must be %d..%d milli, got %d",
			MultiplierAbsoluteMinMilli, MultiplierAbsoluteMaxMilli, milli)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		var row Listing
		if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		if row.Status == ListingDeleted {
			return ErrListingNotFound
		}
		if row.Status == ListingBanned {
			return fmt.Errorf("%w: banned listing cannot be repriced", ErrInvalidTransition)
		}
		if err := validateMultiplierForVendor(tx, row.Vendor, milli); err != nil {
			return err
		}
		if row.MultiplierUpdatedAt != nil && now.Sub(*row.MultiplierUpdatedAt) < multiplierMinInterval {
			return ErrMultiplierTooSoon
		}
		res := tx.Model(&Listing{}).
			Where("id = ? AND version = ?", id, row.Version).
			Updates(map[string]any{
				"multiplier_milli":      milli,
				"multiplier_updated_at": now,
				"version":               row.Version + 1,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("marketplace: listing %d was modified concurrently", id)
		}
		// Per-model rows inherit the listing default until a model is priced
		// separately. Keep the inherited rows in step, otherwise a legacy
		// listing-level price edit would have no effect on quote or selection.
		// Rows with an explicit price keep it, and any staged candidate on an
		// inherited row is superseded by this immediate edit.
		return tx.Model(&ListingModel{}).
			Where("listing_id = ? AND multiplier_milli = ?", id, row.MultiplierMilli).
			Updates(map[string]any{
				"multiplier_milli":         milli,
				"pending_multiplier_milli": 0,
				"pending_effective_at":     nil,
				"last_changed_at":          now,
				"last_changed_date":        shanghaiDate(now),
			}).Error
	})
	if err != nil {
		return err
	}
	mirrorListingUpdateLogged(db, id)
	resetBuyerModelVisibilityCache()
	resetPublicStatsCache()
	return nil
}

// ReplaceKey swaps in a new API key (§3.5): the new key is encrypted and
// fingerprinted, must not be listed anywhere else on the platform
// (ErrDuplicateKey), and the listing drops back to verifying — a key change
// always re-triggers verification. Banned listings are terminal and reject
// key replacement with ErrInvalidTransition.
func ReplaceKey(db *gorm.DB, id int64, newAPIKey string) error {
	if _, err := normalizeListingKey(newAPIKey); err != nil {
		// Same normalization as CreateListing: a padded key maps onto the
		// existing fingerprint; interior whitespace/control characters are
		// rejected before anything is encrypted.
		return err
	}
	ciphertext, err := EncryptListingKey(newAPIKey)
	if err != nil {
		return err
	}
	fingerprint, err := ListingKeyFingerprint(newAPIKey)
	if err != nil {
		return err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		var row Listing
		if err := lockForUpdate(tx).Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrListingNotFound
			}
			return err
		}
		if row.Status == ListingBanned {
			return fmt.Errorf("%w: banned -> verifying", ErrInvalidTransition)
		}
		// The key still has to belong to this channel: switching a listing
		// from an official channel to a plan key (or vice versa) is refused
		// before the row is touched.
		if err := validateChannelAPIKey(tx, row.Vendor, newAPIKey); err != nil {
			return err
		}
		if dup := duplicateListingForKey(tx, fingerprint, row.SellerID, id); dup != nil {
			return dup
		}
		res := tx.Model(&Listing{}).
			Where("id = ? AND version = ?", id, row.Version).
			Updates(map[string]any{
				"key_ciphertext":  ciphertext,
				"key_fingerprint": fingerprint,
				"status":          ListingVerifying,
				"version":         row.Version + 1,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("marketplace: listing %d was modified concurrently", id)
		}
		return nil
	})
	if err != nil {
		var dup *DuplicateKeyError
		if errors.As(err, &dup) {
			return dup
		}
		if isDuplicateKeyError(err) {
			ownerID := int64(0)
			if row, gerr := GetListing(db, id); gerr == nil {
				ownerID = row.SellerID
			}
			if again := duplicateListingForKey(db, fingerprint, ownerID, id); again != nil {
				return again
			}
		}
		return err
	}
	mirrorListingUpdateLogged(db, id)
	return nil
}

// hourWindow is one 可接单时段 entry: {"start":"09:00","end":"18:00"}.
type hourWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// validateListingLabel trims and validates the optional seller-facing channel
// name. It rejects control characters so the label is safe in the console and
// in logs.
func validateListingLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if utf8.RuneCountInString(label) > 64 {
		return "", errors.New("marketplace: label must be at most 64 characters")
	}
	for _, r := range label {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("marketplace: label contains control characters")
		}
	}
	return label, nil
}

// ValidateHours checks the listing's 可接单时段 (§3.5): an empty string means
// all day; otherwise it must be a JSON array of {"start","end"} windows in
// zero-padded 24h HH:MM form with start strictly before end.
func ValidateHours(hours string) error {
	if strings.TrimSpace(hours) == "" {
		return nil
	}
	var windows []hourWindow
	if err := common.UnmarshalJsonStr(hours, &windows); err != nil {
		return fmt.Errorf("marketplace: hours must be a JSON array of time windows: %v", err)
	}
	if len(windows) == 0 {
		return errors.New("marketplace: hours must contain at least one window or be empty")
	}
	for _, w := range windows {
		start, err := parseHourMinute(w.Start)
		if err != nil {
			return fmt.Errorf("marketplace: hours start %q is not HH:MM 24h", w.Start)
		}
		end, err := parseHourMinute(w.End)
		if err != nil {
			return fmt.Errorf("marketplace: hours end %q is not HH:MM 24h", w.End)
		}
		if start >= end {
			return fmt.Errorf("marketplace: hours window %q-%q must have start before end", w.Start, w.End)
		}
	}
	return nil
}

// parseHourMinute parses a strict zero-padded 24h "HH:MM" into minutes since
// midnight. time.Parse("15:04", …) alone is too lenient — it accepts "9:00" —
// so the shape is checked character by character.
func parseHourMinute(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, errors.New("not HH:MM")
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return 0, errors.New("not HH:MM")
		}
	}
	hour := int(s[0]-'0')*10 + int(s[1]-'0')
	minute := int(s[3]-'0')*10 + int(s[4]-'0')
	if hour > 23 || minute > 59 {
		return 0, errors.New("not 24h")
	}
	return hour*60 + minute, nil
}

// GetListing returns one listing by id, or ErrListingNotFound.
func GetListing(db *gorm.DB, id int64) (Listing, error) {
	var row Listing
	if err := db.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Listing{}, ErrListingNotFound
		}
		return Listing{}, err
	}
	return row, nil
}

// GetListingForSeller returns one listing owned by the given seller; any
// other owner's listing is indistinguishable from a missing one
// (ErrListingNotFound), so sellers cannot probe foreign listing ids.
func GetListingForSeller(db *gorm.DB, id, sellerID int64) (Listing, error) {
	var row Listing
	if err := db.Where("id = ? AND seller_id = ? AND status <> ?", id, sellerID, ListingDeleted).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Listing{}, ErrListingNotFound
		}
		return Listing{}, err
	}
	return row, nil
}

// ListListingsBySeller returns all of one seller's live (not soft-deleted)
// listings, oldest first.
func ListListingsBySeller(db *gorm.DB, sellerID int64) ([]Listing, error) {
	var rows []Listing
	if err := db.Where("seller_id = ? AND status <> ?", sellerID, ListingDeleted).
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
