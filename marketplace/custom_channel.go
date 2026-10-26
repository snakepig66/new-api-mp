package marketplace

// custom_channel.go — 卖家自定义第三方渠道 (seller-defined third-party
// channels).
//
// A seller may open a listing against an upstream the platform has never
// registered: they submit a base URL with the listing key, and the platform
// provisions a private mp_channels row (kind=third_party, slug
// "custom-<seller>-<hash>") plus identity channel-model rows (upstream name ==
// platform model name). The listing verification probes each model with a real
// call, so a channel that does not actually serve the platform model names
// never goes live.
//
// Security (§3.11 stays in force):
//   - the base URL must be https, host-based (never an IP literal), without
//     userinfo, query or fragment, with a canonical path and only the default
//     :443 port;
//   - loopback upstreams (localhost/127.*/::1) are refused unless the
//     operator opts in with MP_ALLOW_LOOPBACK_BASE_URL=1 — a production host
//     usually has an internal gateway listening on 127.0.0.1;
//   - the host enters the relay egress whitelist only after a listing on the
//     channel is live/canary (vendors.go), and every dial still passes
//     ValidateEgressIP — private/CGNAT destinations are refused at connect
//     time, so a seller-controlled URL can never become an SSRF vector;
//   - custom channels are visible only to their owner (listVendors filters by
//     session), and each seller is capped at maxCustomChannelsPerSeller rows.
//
// Path normalization: OpenAI-compatible upstreams mount /models and
// /chat/completions either at the root or under /v1. The seller pastes
// whichever base they have; validateCustomBaseURL strips a pasted endpoint
// suffix (…/chat/completions, …/messages, …/responses, …/models) and folds
// repeated /v1 segments. The first successful verification then probes
// base+"/models" then base+"/v1/models" and rewrites the stored base to the
// form that answers, with strip_v1_prefix on — the buyer's /v1 is then always
// dropped and the upstream path is base + /chat/completions.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// customChannelSlugPrefix marks seller-created channels. The prefix is the
// visibility filter in listVendors: a custom channel appears only for its
// owner.
const customChannelSlugPrefix = "custom-"

// maxCustomChannelsPerSeller caps how many distinct upstream URLs one seller
// may register, so the pool cannot be flooded with throwaway rows.
const maxCustomChannelsPerSeller = 10

// ErrCustomBaseURL marks every refusal of the custom-URL validator. The
// message names the rule, never the URL the seller typed.
var ErrCustomBaseURL = errors.New("marketplace: custom base url is invalid")

// MPAllowLoopbackBaseURLEnv re-enables loopback custom upstreams. It exists
// for local development and httptest fixtures; production MUST leave it unset.
const MPAllowLoopbackBaseURLEnv = "MP_ALLOW_LOOPBACK_BASE_URL"

// endpointSuffixes are the trailing API paths sellers paste along with a base
// URL. They are removed so the stored base is the mount point, not one of its
// endpoints.
var endpointSuffixes = []string{"/chat/completions", "/messages", "/responses", "/models"}

// validateCustomBaseURL normalizes and validates a seller-supplied upstream
// base URL. Rules:
//
//   - absolute https URL, host-based (no IP literal except opted-in loopback),
//     no userinfo, query or fragment;
//   - loopback hosts (localhost/127.*/::1) only when
//     MP_ALLOW_LOOPBACK_BASE_URL=1;
//   - non-loopback hosts allow no port or :443 only;
//   - the path must not carry dot segments, encoded separators, empty
//     segments or path parameters;
//   - the host is lowercased and IDNA-encoded; a bare public suffix (com,
//     com.cn) is refused;
//   - the path loses a trailing endpoint suffix and repeated /v1 segments.
//
// It returns the single authoritative base and the host taken from that same
// string, so the whitelist match and the dialed URL can never disagree. An
// already-stored base is not migrated.
func validateCustomBaseURL(raw string) (string, string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", "", fmt.Errorf("%w: address is required", ErrCustomBaseURL)
	}
	u, err := url.Parse(text)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("%w: must be an absolute https address", ErrCustomBaseURL)
	}
	if u.User != nil {
		return "", "", fmt.Errorf("%w: userinfo in the address is not accepted", ErrCustomBaseURL)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.RawFragment != "" {
		return "", "", fmt.Errorf("%w: query strings and fragments are not accepted", ErrCustomBaseURL)
	}
	host := normalizeEgressHost(u.Host)
	if host == "" {
		return "", "", fmt.Errorf("%w: host is required", ErrCustomBaseURL)
	}
	loopback := host == "localhost"
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	if loopback && strings.TrimSpace(os.Getenv(MPAllowLoopbackBaseURLEnv)) != "1" {
		return "", "", fmt.Errorf("%w: loopback addresses are disabled; set %s=1 to enable them",
			ErrCustomBaseURL, MPAllowLoopbackBaseURLEnv)
	}
	if !loopback && !strings.EqualFold(u.Scheme, "https") {
		return "", "", fmt.Errorf("%w: only https is accepted", ErrCustomBaseURL)
	}
	if loopback && !strings.EqualFold(u.Scheme, "https") && !strings.EqualFold(u.Scheme, "http") {
		return "", "", fmt.Errorf("%w: only http and https are accepted", ErrCustomBaseURL)
	}
	if net.ParseIP(host) == nil {
		ascii, ierr := idna.Lookup.ToASCII(strings.ToLower(host))
		if ierr != nil || ascii == "" {
			return "", "", fmt.Errorf("%w: host is not a valid domain name", ErrCustomBaseURL)
		}
		host = ascii
	}
	if !loopback {
		if net.ParseIP(host) != nil {
			// Public IP literals are refused by the egress guard anyway;
			// rejecting here gives the seller a rule they can act on.
			return "", "", fmt.Errorf("%w: use a domain, not an ip address", ErrCustomBaseURL)
		}
		if suffix := "." + host; strings.HasSuffix(suffix, ".local") || strings.HasSuffix(suffix, ".internal") {
			return "", "", fmt.Errorf("%w: internal hostnames are not accepted", ErrCustomBaseURL)
		}
		if suffix, _ := publicsuffix.PublicSuffix(host); suffix == "" || suffix == host {
			return "", "", fmt.Errorf("%w: %q is a public suffix, not an upstream host", ErrCustomBaseURL, host)
		}
	}
	port := u.Port()
	if !loopback && port != "" && port != "443" {
		return "", "", fmt.Errorf("%w: only the default https port 443 is accepted", ErrCustomBaseURL)
	}
	if err := validateCustomBasePath(u.EscapedPath(), u.Path); err != nil {
		return "", "", err
	}
	path := normalizeCustomBasePath(u.Path)
	scheme := strings.ToLower(u.Scheme)
	authority := host
	switch {
	case port == "":
		if strings.Contains(host, ":") {
			authority = "[" + host + "]"
		}
	case scheme == "https" && port == "443":
		// The default port is dropped so one upstream has one canonical base.
	default:
		authority = net.JoinHostPort(host, port)
	}
	return scheme + "://" + authority + path, host, nil
}

// validateCustomBasePath refuses path shapes that could escape the intended
// mount point or confuse an upstream router. escaped is the raw
// (percent-encoded) path and decoded is net/url's decoded form; both are
// checked so a doubly encoded or encoded separator never slips through.
func validateCustomBasePath(escaped, decoded string) error {
	for _, path := range []string{escaped, decoded} {
		lower := strings.ToLower(path)
		for _, encoded := range []string{"%2e", "%2f", "%5c"} {
			if strings.Contains(lower, encoded) {
				return fmt.Errorf("%w: encoded path separators (%s) are not accepted", ErrCustomBaseURL, encoded)
			}
		}
		if strings.Contains(path, "//") {
			return fmt.Errorf("%w: empty path segments (//) are not accepted", ErrCustomBaseURL)
		}
		if strings.Contains(path, ";") {
			return fmt.Errorf("%w: path parameters (;) are not accepted", ErrCustomBaseURL)
		}
		for segment := range strings.SplitSeq(path, "/") {
			if segment == ".." {
				return fmt.Errorf("%w: dot segments (..) are not accepted", ErrCustomBaseURL)
			}
		}
	}
	return nil
}

// normalizeCustomBasePath turns a pasted upstream path into the canonical
// mount point: trailing slashes, a trailing endpoint suffix and consecutive
// /v1 segments are removed.
func normalizeCustomBasePath(path string) string {
	path = strings.TrimRight(path, "/")
	for changed := true; changed; {
		changed = false
		for _, suffix := range endpointSuffixes {
			if strings.HasSuffix(path, suffix) {
				path = strings.TrimRight(strings.TrimSuffix(path, suffix), "/")
				changed = true
				break
			}
		}
	}
	if path == "" {
		return ""
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	folded := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "v1" && len(folded) > 0 && folded[len(folded)-1] == "v1" {
			continue
		}
		folded = append(folded, segment)
	}
	return "/" + strings.Join(folded, "/")
}

// customChannelSlug derives the deterministic slug for one (seller, base URL)
// pair, so re-listing the same upstream reuses the channel row instead of
// creating a duplicate.
func customChannelSlug(sellerID int64, base string) string {
	sum := sha256.Sum256([]byte(strconv.FormatInt(sellerID, 10) + "|" + base))
	return fmt.Sprintf("%s%d-%s", customChannelSlugPrefix, sellerID, hex.EncodeToString(sum[:])[:10])
}

// isCustomChannelSlug reports whether the slug belongs to a seller-created
// channel.
func isCustomChannelSlug(slug string) bool {
	return strings.HasPrefix(slug, customChannelSlugPrefix)
}

// customChannelOwner extracts the owning seller id from a custom slug.
func customChannelOwner(slug string) (int64, bool) {
	if !isCustomChannelSlug(slug) {
		return 0, false
	}
	rest := strings.TrimPrefix(slug, customChannelSlugPrefix)
	idText, _, ok := strings.Cut(rest, "-")
	id, err := strconv.ParseInt(idText, 10, 64)
	if !ok || err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// countCustomChannels returns how many custom channels the seller already
// registered. A read error fails closed at the cap.
func countCustomChannels(db *gorm.DB, sellerID int64) int64 {
	var n int64
	prefix := fmt.Sprintf("%s%d-%%", customChannelSlugPrefix, sellerID)
	if err := db.Model(&ChannelPool{}).Where("slug LIKE ?", prefix).Count(&n).Error; err != nil {
		return maxCustomChannelsPerSeller
	}
	return n
}

// ensureCustomChannel provisions (or reuses) the seller's custom channel for
// one upstream base URL and adds identity channel-model rows for the requested
// models. Idempotent: the same (seller, URL) always maps to the same slug, and
// the model rows are upserted. An existing channel row is left untouched —
// verification may already have normalized its base and strip flag. Models
// outside the platform pool are refused: the catalogue and its price books
// stay the single source of what can be sold.
func ensureCustomChannel(db *gorm.DB, sellerID int64, rawURL string, models []string) (string, error) {
	if db == nil {
		return "", errors.New("marketplace: nil database")
	}
	if sellerID <= 0 {
		return "", errors.New("marketplace: seller id is required")
	}
	base, host, err := validateCustomBaseURL(rawURL)
	if err != nil {
		return "", err
	}
	slug := customChannelSlug(sellerID, base)
	if _, ok := ChannelPoolBySlug(db, slug); !ok {
		if countCustomChannels(db, sellerID) >= maxCustomChannelsPerSeller {
			return "", fmt.Errorf("%w: at most %d custom channels per account", ErrCustomBaseURL, maxCustomChannelsPerSeller)
		}
		protocols, merr := common.Marshal(VendorProtocols{OpenAI: base})
		if merr != nil {
			return "", fmt.Errorf("marketplace: encode custom protocols: %w", merr)
		}
		if _, uerr := ChannelPoolCreateOrUpdate(db, ChannelPool{
			Slug:      slug,
			Name:      host,
			Kind:      ChannelKindThirdParty,
			Protocols: string(protocols),
			Adaptor:   VendorAdaptorOpenAI,
			Status:    PoolStatusActive,
		}); uerr != nil {
			return "", fmt.Errorf("marketplace: register custom channel: %w", uerr)
		}
	}
	for _, m := range dedupModels(models) {
		if _, merr := UpsertChannelModelPool(db, ChannelModel{
			ChannelSlug:      slug,
			UpstreamModel:    m,
			Model:            m,
			CoefficientMilli: 1000,
			// The listing verification probes every model with a real call
			// before the listing can leave draft, which is the certification
			// for seller-defined upstreams.
			Verified: true,
			Status:   PoolStatusActive,
		}); merr != nil {
			return "", fmt.Errorf("marketplace: qualify model %q on custom channel: %w", m, merr)
		}
	}
	// Reload the registry now so the new pool row is resolvable at once. The
	// host is still only admitted to the relay whitelist once a listing on it
	// is live/canary (vendors.go); verification uses the whitelist-exempt
	// EgressVerifyHTTPClient.
	refreshVendorRegistry(true)
	return slug, nil
}

// probeCustomChannelKey is the dry-run branch of POST /seller/listings/probe
// for a not-yet-registered upstream: validate the URL, detect which /models
// form answers, and intersect the upstream's model names with the platform
// catalogue (identity mapping — a custom upstream must serve the platform
// model names verbatim). Returns the detected base, the raw key result and the
// sellable platform models.
func probeCustomChannelKey(ctx context.Context, client *http.Client, db *gorm.DB, rawURL, apiKey string) (string, VerifyResult, []string) {
	base, _, err := validateCustomBaseURL(rawURL)
	if err != nil {
		return "", VerifyResult{FailReason: err.Error()}, nil
	}
	res := VerifyChannelKey(ctx, client, Vendor{Protocols: VendorProtocols{OpenAI: base}}, apiKey)
	detected := base
	if !res.KeyValid {
		// The upstream may mount /models under /v1; retry before giving up.
		alt := strings.TrimSuffix(base, "/") + "/v1"
		altRes := VerifyChannelKey(ctx, client, Vendor{Protocols: VendorProtocols{OpenAI: alt}}, apiKey)
		if altRes.KeyValid {
			res = altRes
			detected = alt
		}
	}
	if db == nil || !res.KeyValid {
		return detected, res, nil
	}
	catalogue := make(map[string]bool)
	for _, m := range ModelPoolAll(db) {
		catalogue[strings.TrimSpace(m.Name)] = true
	}
	sellable := make([]string, 0, len(res.Models))
	for _, m := range res.Models {
		if catalogue[m] && !containsString(sellable, m) {
			sellable = append(sellable, m)
		}
	}
	return detected, res, sellable
}

// normalizeCustomChannel runs the /models detection for one listing's custom
// channel, persists the normalized base (mp_channels protocols +
// strip_v1_prefix, and the listing's placeholder channel row so the relay
// dials the same base), and returns the updated vendor. When neither /models
// form answers the vendor is returned unchanged and the standard verification
// flow reports the upstream error.
func normalizeCustomChannel(ctx context.Context, client *http.Client, db *gorm.DB, listing *Listing, vendor Vendor, apiKey string) (Vendor, error) {
	if !isCustomChannelSlug(listing.Vendor) {
		return vendor, nil
	}
	base := VendorOpenAIBase(vendor)
	detected := base
	if !customModelsRespond(ctx, client, base, apiKey) {
		alt := strings.TrimSuffix(base, "/") + "/v1"
		if customModelsRespond(ctx, client, alt, apiKey) {
			detected = alt
		} else {
			return vendor, nil
		}
	}
	return pinCustomVendor(db, listing, vendor, detected)
}

// customModelsRespond GETs candidate+"/models" with the key and reports
// whether it answered 200.
func customModelsRespond(ctx context.Context, client *http.Client, candidate, apiKey string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(candidate, "/")+"/models", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK
}

// pinCustomVendor persists one normalized base for the listing's custom
// channel — the pool row (protocols + strip flag) and the placeholder relay
// channel row (base_url + setting) — and returns the vendor with the base and
// strip flag applied.
func pinCustomVendor(db *gorm.DB, listing *Listing, vendor Vendor, base string) (Vendor, error) {
	protocols, err := common.Marshal(VendorProtocols{OpenAI: base})
	if err != nil {
		return vendor, fmt.Errorf("marketplace: encode custom protocols: %w", err)
	}
	if err := db.Model(&ChannelPool{}).Where("slug = ?", listing.Vendor).Updates(map[string]any{
		"protocols":       string(protocols),
		"strip_v1_prefix": true,
	}).Error; err != nil {
		return vendor, fmt.Errorf("marketplace: normalize custom channel: %w", err)
	}
	setting, err := channelSettingWithStrip(db, listing.ChannelID)
	if err != nil {
		return vendor, fmt.Errorf("marketplace: normalize relay channel setting: %w", err)
	}
	if err := db.Table("channels").Where("id = ?", listing.ChannelID).Updates(map[string]any{
		"base_url": base,
		"setting":  setting,
	}).Error; err != nil {
		return vendor, fmt.Errorf("marketplace: normalize relay channel: %w", err)
	}
	vendor.Protocols.OpenAI = base
	vendor.BaseURL = base
	vendor.StripV1Prefix = true
	return vendor, nil
}

// channelSettingWithStrip reads the placeholder channel's setting JSON from
// the channels table and returns it with mp_strip_v1_prefix=true. A missing
// row or setting yields the flag-only JSON: the strip flag is what the relay
// needs.
func channelSettingWithStrip(db *gorm.DB, channelID int64) (string, error) {
	var raw *string
	if err := db.Table("channels").Select("setting").Where("id = ?", channelID).
		Scan(&raw).Error; err != nil || raw == nil {
		return `{"mp_strip_v1_prefix":true}`, nil
	}
	setting := map[string]any{}
	if text := strings.TrimSpace(*raw); text != "" {
		if err := common.Unmarshal([]byte(text), &setting); err != nil {
			setting = map[string]any{}
		}
	}
	setting["mp_strip_v1_prefix"] = true
	out, err := common.Marshal(setting)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
