package marketplace

// risk.go — simplified risk graph (P5-C).
//
// The marketplace has no device or phone identity yet, so the association
// graph is built from the evidence that already exists:
//
//   - login IPs from audit_logs: two accounts that recently signed in from the
//     same public address are linked;
//   - bound payout accounts: mp_sellers stores the Alipay account encrypted
//     with a random nonce, so equal accounts never share ciphertext. There is no
//     blind index yet; this version decrypts both sides in process and compares
//     plaintext, and a row whose ciphertext cannot be decrypted is skipped.
//
// The graph is advisory plus one hard gate: a withdrawal requires a verified
// real name and no other account sharing a login IP. Private, loopback and
// link-local addresses are ignored because they usually mean a reverse proxy or
// a LAN, which would link unrelated users and block every withdrawal.

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/model"
)

const (
	// RiskRelationSameIP links two accounts that signed in from one address.
	RiskRelationSameIP = "same_ip"
	// RiskRelationSamePayoutAccount links two sellers that bound one account.
	RiskRelationSamePayoutAccount = "same_payout_account"

	// riskRecentLoginLimit bounds the user's own login history the graph reads.
	riskRecentLoginLimit = 200
	// riskMaxLinkedIPs bounds how many shared addresses are expanded, keeping
	// the IN query small and the result focused on recent activity.
	riskMaxLinkedIPs = 20
)

var (
	// ErrPayoutRealNameRequired blocks a withdrawal until real-name
	// verification is complete.
	ErrPayoutRealNameRequired = errors.New("marketplace: real-name verification is required before withdrawal")
	// ErrPayoutRelatedAccounts requires a manual risk review when another
	// account shares a login IP with the seller.
	ErrPayoutRelatedAccounts = errors.New("marketplace: linked accounts require a manual risk review before withdrawal")
)

// RelatedUser is one account linked to the queried user.
type RelatedUser struct {
	UserID       int64  `json:"user_id"`
	RelationType string `json:"relation_type"`
	Value        string `json:"value"`
}

// FindRelatedUsers returns the accounts linked to userID by shared login IP or
// shared payout account. The result is deterministic (relation type, then user
// id) and never contains the queried user.
func FindRelatedUsers(db *gorm.DB, userID int) ([]RelatedUser, error) {
	if db == nil {
		return nil, errors.New("marketplace: risk graph requires a database handle")
	}
	if userID <= 0 {
		return nil, fmt.Errorf("marketplace: invalid risk-graph user id %d", userID)
	}
	logDB := db
	if logDB == nil {
		logDB = model.LOG_DB
	}

	related := make([]RelatedUser, 0)
	if logDB.Migrator().HasTable("audit_logs") {
		ipRelations, err := sharedIPRelations(logDB, userID)
		if err != nil {
			return nil, err
		}
		related = append(related, ipRelations...)
	}
	payoutRelations, err := sharedPayoutAccountRelations(db, userID)
	if err != nil {
		return nil, err
	}
	related = append(related, payoutRelations...)

	slices.SortFunc(related, func(a, b RelatedUser) int {
		if c := cmp.Compare(a.RelationType, b.RelationType); c != 0 {
			return c
		}
		return cmp.Compare(a.UserID, b.UserID)
	})
	return related, nil
}

// sharedIPRelations expands the user's recent public login addresses and finds
// other users that signed in from any of them.
func sharedIPRelations(logDB *gorm.DB, userID int) ([]RelatedUser, error) {
	var recent []string
	if err := logDB.Model(&model.AuditLog{}).
		Where("user_id = ? AND category = ? AND ip <> ''", userID, model.AuditCategoryLogin).
		Order("id DESC").
		Limit(riskRecentLoginLimit).
		Pluck("ip", &recent).Error; err != nil {
		return nil, fmt.Errorf("marketplace: read login ip history: %w", err)
	}
	seen := make(map[string]bool, len(recent))
	sharedIPs := make([]string, 0, len(recent))
	for _, raw := range recent {
		ip := strings.TrimSpace(raw)
		if ip == "" || seen[ip] || !linkableIP(ip) {
			continue
		}
		seen[ip] = true
		sharedIPs = append(sharedIPs, ip)
		if len(sharedIPs) >= riskMaxLinkedIPs {
			break
		}
	}
	if len(sharedIPs) == 0 {
		return nil, nil
	}
	var rows []struct {
		UserID int
		Ip     string
	}
	if err := logDB.Model(&model.AuditLog{}).
		Select("user_id, ip").
		Where("category = ? AND ip IN ? AND user_id <> ?", model.AuditCategoryLogin, sharedIPs, userID).
		Group("user_id, ip").
		Order("user_id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("marketplace: query shared login ips: %w", err)
	}
	relations := make([]RelatedUser, 0, len(rows))
	seenUsers := make(map[int]bool, len(rows))
	for _, row := range rows {
		if row.UserID <= 0 || seenUsers[row.UserID] {
			continue
		}
		seenUsers[row.UserID] = true
		relations = append(relations, RelatedUser{
			UserID:       int64(row.UserID),
			RelationType: RiskRelationSameIP,
			Value:        row.Ip,
		})
	}
	return relations, nil
}

// sharedPayoutAccountRelations decrypts the user's payout account (when the
// user is a seller) and every other seller's account. Undecryptable rows are
// skipped: a wrong or rotated key must not invent a relation.
func sharedPayoutAccountRelations(db *gorm.DB, userID int) ([]RelatedUser, error) {
	var seller Seller
	err := db.Select("user_id", "payout_account").Where("user_id = ?", userID).Take(&seller).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("marketplace: load seller payout account: %w", err)
	}
	own, err := decryptPayoutAccount(seller.PayoutAccount)
	if err != nil || own == "" {
		return nil, nil
	}
	var sellers []Seller
	if err := db.Select("user_id", "payout_account").
		Where("user_id <> ? AND payout_account <> ''", userID).
		Find(&sellers).Error; err != nil {
		return nil, fmt.Errorf("marketplace: query seller payout accounts: %w", err)
	}
	masked := maskPayoutAccount(own)
	relations := make([]RelatedUser, 0)
	for i := range sellers {
		candidate, derr := decryptPayoutAccount(sellers[i].PayoutAccount)
		if derr != nil || candidate != own {
			continue
		}
		relations = append(relations, RelatedUser{
			UserID:       sellers[i].UserID,
			RelationType: RiskRelationSamePayoutAccount,
			Value:        masked,
		})
	}
	return relations, nil
}

// CheckWithdrawalEligibility is the hard risk gate for a withdrawal: the seller
// must exist, and share no login IP with another account. It returns ErrPayoutNotFound
// for a missing seller so callers keep RequestPayout's existing error mapping.
func CheckWithdrawalEligibility(db *gorm.DB, sellerID int64) error {
	if db == nil {
		return errors.New("marketplace: risk check requires a database handle")
	}
	var seller Seller
	err := db.Select("user_id").Where("user_id = ?", sellerID).Take(&seller).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrPayoutNotFound
	}
	if err != nil {
		return fmt.Errorf("marketplace: load seller for risk check: %w", err)
	}
	related, err := FindRelatedUsers(db, int(sellerID))
	if err != nil {
		return err
	}
	for _, relation := range related {
		if relation.RelationType == RiskRelationSameIP {
			return ErrPayoutRelatedAccounts
		}
	}
	return nil
}

// linkableIP reports whether an address is useful graph evidence. Private,
// loopback, link-local, multicast and unspecified addresses are shared
// infrastructure (proxies, NAT, LAN), not account identity.
func linkableIP(raw string) bool {
	ip := net.ParseIP(raw)
	if ip == nil {
		return false
	}
	return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsUnspecified() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}

// maskPayoutAccount keeps only the first and last two runes, so the relation can
// be recognized in the console without returning the bound account.
func maskPayoutAccount(account string) string {
	account = strings.TrimSpace(account)
	if strings.HasPrefix(account, "{") {
		var info struct {
			Account string `json:"account"`
			Name    string `json:"name"`
		}
		if err := json.Unmarshal([]byte(account), &info); err == nil {
			if info.Account != "" {
				account = info.Account
			} else if info.Name != "" {
				account = info.Name
			}
		}
	}
	runes := []rune(account)
	if len(runes) <= 4 {
		return "***"
	}
	return string(runes[:2]) + "***" + string(runes[len(runes)-2:])
}
