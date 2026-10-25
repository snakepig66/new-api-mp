package marketplace

// seller.go — 分享者开通状态机 (§3.1, 阶段 6 缺口 M9).
//
// The seller identity is an extension of a user account:
//
//	(no record) → pending (协议已签) → active (协议生效，人人可分享)
//
// 分享对每个账号开放：签完协议即 active，可直接开挂单。实名与支付宝收款
// 账户不再是「能不能分享」的前置条件，而是「能不能提现」的前置条件——
// 提现时由 RequestPayout 再校验。banned / closed 为保留终态。
//
// Privacy contract (§2.3/§3.1): the real name and the ID-card number are only
// format-validated — the plaintext is never stored, logged or returned. The
// Alipay account is AES-256-GCM encrypted at rest with the same key material as
// listing keys (EncryptListingKey) and is never echoed by any API.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"
)

// CurrentSellerAgreementVersion is the only 协议版本 the onboarding accepts
// (§3.1). Bump it together with the user agreement text.
const CurrentSellerAgreementVersion = "v1"

// Seller lifecycle statuses (§3.1). banned / closed are reserved.
const (
	SellerStatusPending = "pending"
	SellerStatusActive  = "active"
	SellerStatusBanned  = "banned"
	SellerStatusClosed  = "closed"
)

// Seller 实名状态 values. Only a format check is performed (§3.1): real_name
// and id_card_masked are never persisted.
const (
	SellerRealNameVerified   = "verified"
	SellerRealNameUnverified = "unverified"
)

// Seller onboarding errors. All carry semantics only, never user input.
var (
	ErrSellerNotFound             = errors.New("marketplace: seller not found")
	ErrSellerNotActive            = errors.New("marketplace: seller is not active")
	ErrSellerClosed               = errors.New("marketplace: seller account is closed")
	ErrSellerAgreementVersion     = errors.New("marketplace: unsupported agreement version")
	ErrSellerInvalidRealName      = errors.New("marketplace: invalid real name")
	ErrSellerInvalidIDCardMasked  = errors.New("marketplace: id_card_masked must be a masked id number")
	ErrSellerInvalidPayoutAccount = errors.New("marketplace: invalid alipay account")
)

// SellerOnboardingInput is the POST /seller/onboarding body (§3.1). Every
// field is optional: a call advances whatever step the body provides and the
// response tells the client the next step.
type SellerOnboardingInput struct {
	AgreementVersion string `json:"agreement_version"`
	RealName         string `json:"real_name"`
	IDCardMasked     string `json:"id_card_masked"` // format-checked only, never stored
	AlipayAccount    string `json:"alipay_account"`
}

// SellerProgress is the onboarding progress rendered to the frontend (§5
// FRONTEND-REBUILD /seller onboarding wizard).
type SellerProgress struct {
	AgreementSigned    bool   `json:"agreement_signed"`
	RealNameVerified   bool   `json:"real_name_verified"`
	PayoutAccountBound bool   `json:"payout_account_bound"`
	NextStep           string `json:"next_step"` // agreement / real_name / payout_account / done
	AgreementVersion   string `json:"agreement_version"`
}

// progress derives the wizard state from the stored row.
func (s Seller) progress() SellerProgress {
	p := SellerProgress{
		AgreementSigned:    strings.TrimSpace(s.AgreementVersion) != "",
		RealNameVerified:   s.RealNameStatus == SellerRealNameVerified,
		PayoutAccountBound: strings.TrimSpace(s.PayoutAccount) != "",
		AgreementVersion:   s.AgreementVersion,
	}
	switch {
	case !p.AgreementSigned:
		p.NextStep = "agreement"
	case !p.RealNameVerified:
		p.NextStep = "real_name"
	case !p.PayoutAccountBound:
		p.NextStep = "payout_account"
	default:
		p.NextStep = "done"
	}
	return p
}

// GetSeller returns the user's seller row, or ErrSellerNotFound.
func GetSeller(db *gorm.DB, userID int64) (Seller, error) {
	if userID <= 0 {
		return Seller{}, ErrSellerNotFound
	}
	var s Seller
	if err := db.Where("user_id = ?", userID).Take(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Seller{}, ErrSellerNotFound
		}
		return Seller{}, err
	}
	return s, nil
}

// RequireActiveSeller is the M9 write gate: listings and payouts are only
// available to an active seller. A missing row and a pending/banned/closed row
// are both rejected (ErrSellerNotFound / ErrSellerNotActive / ErrSellerClosed).
func RequireActiveSeller(db *gorm.DB, userID int64) error {
	s, err := GetSeller(db, userID)
	if err != nil {
		return err
	}
	switch s.Status {
	case SellerStatusActive:
		return nil
	case SellerStatusBanned, SellerStatusClosed:
		return ErrSellerClosed
	default:
		return ErrSellerNotActive
	}
}

// OnboardSeller applies one onboarding step (§3.1). For a user without a
// seller row it creates one only when the body carries the current agreement
// version, so the "no record → pending" edge implies the agreement was signed.
// Missing fields never fail the call: they simply leave the wizard on the
// current step and the returned progress names the next one.
func OnboardSeller(db *gorm.DB, userID int64, in SellerOnboardingInput, now time.Time) (Seller, SellerProgress, error) {
	if userID <= 0 {
		return Seller{}, SellerProgress{}, ErrSellerNotFound
	}
	realName := strings.TrimSpace(in.RealName)
	idCardMasked := strings.TrimSpace(in.IDCardMasked)
	alipay := strings.TrimSpace(in.AlipayAccount)

	if realName != "" && !validRealName(realName) {
		return Seller{}, SellerProgress{}, ErrSellerInvalidRealName
	}
	if idCardMasked != "" && !isMaskedIDCard(idCardMasked) {
		return Seller{}, SellerProgress{}, ErrSellerInvalidIDCardMasked
	}
	if alipay != "" && !validAlipayAccount(alipay) {
		return Seller{}, SellerProgress{}, ErrSellerInvalidPayoutAccount
	}

	s, err := GetSeller(db, userID)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrSellerNotFound) {
		return Seller{}, SellerProgress{}, err
	}

	if !exists {
		// The record is only created once the agreement is signed.
		if strings.TrimSpace(in.AgreementVersion) == "" {
			return Seller{}, SellerProgress{NextStep: "agreement"}, nil
		}
		if in.AgreementVersion != CurrentSellerAgreementVersion {
			return Seller{}, SellerProgress{}, ErrSellerAgreementVersion
		}
		s = Seller{
			UserID:           userID,
			Status:           SellerStatusPending,
			RealNameStatus:   SellerRealNameUnverified,
			AgreementVersion: CurrentSellerAgreementVersion,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
	} else {
		if s.Status == SellerStatusBanned || s.Status == SellerStatusClosed {
			return s, s.progress(), ErrSellerClosed
		}
		if strings.TrimSpace(in.AgreementVersion) != "" {
			if in.AgreementVersion != CurrentSellerAgreementVersion {
				return s, s.progress(), ErrSellerAgreementVersion
			}
			s.AgreementVersion = CurrentSellerAgreementVersion
		}
	}

	if realName != "" {
		s.RealNameStatus = SellerRealNameVerified
	}
	if alipay != "" {
		stored, err := encryptPayoutAccount(alipay)
		if err != nil {
			return Seller{}, SellerProgress{}, err
		}
		// The plaintext never touches the row: only the AES-256-GCM blob,
		// base64-armored so the TEXT column stays valid UTF-8 on every dialect.
		s.PayoutAccount = stored
	}

	s.Status = nextSellerStatus(s)
	s.UpdatedAt = now

	if !exists {
		if err := db.Create(&s).Error; err != nil {
			return Seller{}, SellerProgress{}, err
		}
	} else {
		err := db.Model(&Seller{}).Where("user_id = ?", userID).Updates(map[string]any{
			"status":            s.Status,
			"real_name_status":  s.RealNameStatus,
			"agreement_version": s.AgreementVersion,
			"payout_account":    s.PayoutAccount,
			"updated_at":        now,
		}).Error
		if err != nil {
			return Seller{}, SellerProgress{}, err
		}
	}
	return s, s.progress(), nil
}

// encryptPayoutAccount encrypts the Alipay account with the listing-key
// AES-256-GCM helper (same key material, §3.1) and base64-encodes the blob.
// The base64 armor keeps the ciphertext valid UTF-8 in mp_sellers.payout_account
// (TEXT) on MySQL/PostgreSQL; it is not a second layer of encryption.
func encryptPayoutAccount(plaintext string) (string, error) {
	blob, err := EncryptListingKey(plaintext)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(blob), nil
}

// decryptPayoutAccount reverses encryptPayoutAccount. The plaintext is for
// internal use only (payout execution); APIs never return it (§2.3).
func decryptPayoutAccount(stored string) (string, error) {
	blob, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stored))
	if err != nil {
		return "", errors.New("marketplace: stored payout account is malformed")
	}
	return DecryptListingKey(blob)
}

// TODO(§3.5 注销结清): a seller cancellation endpoint (settle pending +
// withdrawable balances, then pause/ban every listing) is intentionally not
// implemented in this phase. The closed status is reserved for it.

// nextSellerStatus promotes to active as soon as the agreement is signed, so
// every account can share without real-name or payout onboarding. Those two
// steps stay optional here and become gates only at withdrawal time. Active is
// never downgraded implicitly and reserved terminal states are left untouched.
func nextSellerStatus(s Seller) string {
	if s.Status == SellerStatusBanned || s.Status == SellerStatusClosed {
		return s.Status
	}
	if strings.TrimSpace(s.AgreementVersion) != "" {
		return SellerStatusActive
	}
	if s.Status == SellerStatusActive {
		return s.Status
	}
	return SellerStatusPending
}

// validRealName accepts 2..64 letters (Han included) with the usual name
// separators. Digits and control characters are rejected.
func validRealName(name string) bool {
	runes := []rune(name)
	if len(runes) < 2 || len(runes) > 64 {
		return false
	}
	for _, r := range runes {
		switch {
		case unicode.IsLetter(r):
		case r == ' ' || r == '·' || r == '-' || r == '.' || r == '•':
		default:
			return false
		}
	}
	return true
}

// isMaskedIDCard reports whether the value is a *masked* Chinese ID number:
// only digits / X / '*' with at least four digits and four asterisks. A full
// unmasked number (no asterisk) is rejected, so the API never accepts plaintext
// identity numbers (§3.1 only format-checks a masked value).
func isMaskedIDCard(s string) bool {
	if len(s) < 8 || len(s) > 32 {
		return false
	}
	digits, stars := 0, 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '*':
			stars++
		case r == 'x' || r == 'X':
		default:
			return false
		}
	}
	return digits >= 4 && stars >= 4
}

// validAlipayAccount accepts the two account forms Alipay uses for payouts:
// an email address or an 11-digit mainland mobile number.
func validAlipayAccount(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	if at := strings.IndexByte(s, '@'); at > 0 {
		domain := s[at+1:]
		return at == strings.LastIndexByte(s, '@') &&
			domain != "" && strings.Contains(domain, ".") &&
			!strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
	}
	if len(s) != 11 || s[0] != '1' {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// L4: seller-side retry-chain redaction
// ---------------------------------------------------------------------------

// sellerRetryHop is the only shape a share-record detail exposes from the
// request's retry chain: seq/event/at plus a redacted detail. listing_id and
// every listing/seller identifier inside detail are removed so one seller can
// never learn about another seller's listings (L4).
type sellerRetryHop struct {
	Seq    int    `json:"seq"`
	Event  string `json:"event"`
	At     string `json:"at"`
	Detail string `json:"detail,omitempty"`
}

// sellerRetryDetailRedaction matches "listing 42", "listing_id=42",
// "listing_id": 42, "seller_id: 7" and "seller #7" forms.
var sellerRetryDetailRedaction = regexp.MustCompile(
	`(?i)(?:\b(?:listing|seller)[_\s-]?id\s*"?\s*[:=]\s*"?\s*\d+)|(?:\b(?:listing|seller)\s*#?\s*\d+)`)

// sellerRetryChain returns the retry chain as a JSON array containing only
// seq/event/at and a redacted detail for every hop (L4). Unparseable input
// yields "[]" — raw text is never passed through, so a malformed chain cannot
// leak identifiers wholesale.
//
// api_records.go's getShareRecord should wrap r.RetryChain with this function
// (the file is owned by another agent; see the M9 report for wiring).
func sellerRetryChain(chain string) string {
	chain = strings.TrimSpace(chain)
	if chain == "" {
		return ""
	}
	var hops []RelayHop
	if err := json.Unmarshal([]byte(chain), &hops); err != nil {
		return "[]"
	}
	out := make([]sellerRetryHop, 0, len(hops))
	for _, h := range hops {
		out = append(out, sellerRetryHop{
			Seq:    h.Seq,
			Event:  h.Event,
			At:     h.At,
			Detail: sellerRetryDetailRedaction.ReplaceAllString(h.Detail, "<redacted>"),
		})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// sellerGateStatus maps a seller write-gate error onto the console HTTP
// semantics: missing / non-active / closed → 403.
func sellerGateStatus(err error) (int, string) {
	switch {
	case errors.Is(err, ErrSellerNotFound), errors.Is(err, ErrSellerNotActive):
		return 403, "seller account is not active"
	case errors.Is(err, ErrSellerClosed):
		return 403, "seller account is closed"
	default:
		return 500, "failed to check seller status"
	}
}

// sellerOnboardingStatus maps onboarding validation errors onto 400/403.
func sellerOnboardingStatus(err error) (int, string) {
	switch {
	case errors.Is(err, ErrSellerAgreementVersion):
		return 400, fmt.Sprintf("agreement_version must be %q", CurrentSellerAgreementVersion)
	case errors.Is(err, ErrSellerInvalidRealName):
		return 400, "real_name is invalid"
	case errors.Is(err, ErrSellerInvalidIDCardMasked):
		return 400, "id_card_masked must be a masked id number"
	case errors.Is(err, ErrSellerInvalidPayoutAccount):
		return 400, "alipay_account is invalid"
	case errors.Is(err, ErrSellerClosed):
		return 403, "seller account is closed"
	case errors.Is(err, ErrSellerNotFound):
		return 401, "unauthorized"
	default:
		return 500, "failed to save seller onboarding"
	}
}
