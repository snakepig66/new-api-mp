package marketplace

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/model"
)

// useAuditLogDB points model.LOG_DB at the test handle and creates audit_logs,
// so the risk graph reads the same in-memory database as the fixture.
func useAuditLogDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	old := model.LOG_DB
	model.LOG_DB = db
	t.Cleanup(func() { model.LOG_DB = old })
	require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
}

func addLoginAudit(t *testing.T, db *gorm.DB, eventID string, userID int, ip string) {
	t.Helper()
	require.NoError(t, db.Create(&model.AuditLog{
		EventId:   eventID,
		UserId:    userID,
		Category:  model.AuditCategoryLogin,
		Ip:        ip,
		CreatedAt: time.Now().Unix(),
		Success:   true,
	}).Error)
}

// TestFindRelatedUsers covers the simplified graph: a shared public login IP
// links accounts, a shared bound payout account links sellers, and private or
// unrelated addresses never create a relation.
func TestFindRelatedUsers(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	useAuditLogDB(t, db)
	preserveAnonSecret(t)
	SetAnonSecret("risk-graph-test-secret")

	addLoginAudit(t, db, "evt-101", 101, "203.0.113.10")
	addLoginAudit(t, db, "evt-102", 102, "203.0.113.10")
	addLoginAudit(t, db, "evt-103", 103, "198.51.100.20")
	addLoginAudit(t, db, "evt-104", 104, "10.0.0.5")
	addLoginAudit(t, db, "evt-101-private", 101, "10.0.0.5")

	aliceAccount, err := encryptPayoutAccount("alice@example.com")
	require.NoError(t, err)
	sameAccount, err := encryptPayoutAccount("alice@example.com")
	require.NoError(t, err)
	otherAccount, err := encryptPayoutAccount("bob@example.com")
	require.NoError(t, err)
	sellers := []Seller{
		{UserID: 101, Status: SellerStatusActive, RealNameStatus: SellerRealNameVerified, AgreementVersion: "v1", PayoutAccount: aliceAccount},
		{UserID: 105, Status: SellerStatusActive, RealNameStatus: SellerRealNameVerified, AgreementVersion: "v1", PayoutAccount: sameAccount},
		{UserID: 106, Status: SellerStatusActive, RealNameStatus: SellerRealNameVerified, AgreementVersion: "v1", PayoutAccount: otherAccount},
	}
	require.NoError(t, db.Create(&sellers).Error)

	related, err := FindRelatedUsers(db, 101)
	require.NoError(t, err)
	require.Len(t, related, 2)

	assert.Equal(t, RelatedUser{UserID: 102, RelationType: RiskRelationSameIP, Value: "203.0.113.10"}, related[0])
	assert.Equal(t, RelatedUser{UserID: 105, RelationType: RiskRelationSamePayoutAccount, Value: "al***om"}, related[1])
	for _, relation := range related {
		assert.NotEqual(t, int64(101), relation.UserID, "the queried user is never related to itself")
		assert.NotEqual(t, int64(104), relation.UserID, "a private LAN address is not identity evidence")
		assert.NotEqual(t, int64(106), relation.UserID, "a different payout account is unrelated")
	}
}

// TestCheckWithdrawalEligibility covers the hard withdrawal gate.
func TestCheckWithdrawalEligibility(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	useAuditLogDB(t, db)

	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&[]Seller{
		{UserID: 201, Status: SellerStatusActive, RealNameStatus: SellerRealNameVerified, AgreementVersion: "v1"},
		{UserID: 202, Status: SellerStatusActive, RealNameStatus: SellerRealNameUnverified, AgreementVersion: "v1"},
		{UserID: 203, Status: SellerStatusActive, RealNameStatus: SellerRealNameVerified, AgreementVersion: "v1"},
	}).Error)
	addLoginAudit(t, db, "evt-203", 203, "203.0.113.30")
	addLoginAudit(t, db, "evt-204", 204, "203.0.113.30")

	assert.NoError(t, CheckWithdrawalEligibility(db, 201))
	assert.NoError(t, CheckWithdrawalEligibility(db, 202), "real-name verification is removed, so unverified seller is eligible")
	assert.ErrorIs(t, CheckWithdrawalEligibility(db, 203), ErrPayoutRelatedAccounts)
	assert.ErrorIs(t, CheckWithdrawalEligibility(db, 999), ErrPayoutNotFound)

	// An unverified seller with enough balance and a bound payout account can withdraw.
	require.NoError(t, db.Create(&Seller{
		UserID: 301, Status: SellerStatusActive, RealNameStatus: SellerRealNameUnverified,
		AgreementVersion: "v1", PayoutAccount: "bound-account", CreditScore: 100,
	}).Error)
	require.NoError(t, db.Create(&LedgerEntry{
		RequestID:   "risk-payout-seed",
		EntryType:   EntrySellerCredit,
		Account:     SellerAccount(301),
		AmountMicro: 100_000_000,
		Status:      LedgerAvailable,
		CreatedAt:   now,
	}).Error)
	payout, err := RequestPayout(db, 301, MinPayoutMicro, "risk-payout-1", now)
	assert.NoError(t, err)
	assert.Equal(t, PayoutPending, payout.Status)
}

// TestAdminRelatedUsersEndpoint protects the admin route contract: user_id is
// validated and shared login IPs are returned in the console envelope.
func TestAdminRelatedUsersEndpoint(t *testing.T) {
	f := newAdminAPIFixture(t)
	useAuditLogDB(t, f.db)

	other := model.User{Username: "risk-other", Password: "placeholder-pass"}
	require.NoError(t, f.db.Create(&other).Error)
	addLoginAudit(t, f.db, "evt-buyer", int(f.buyerID), "203.0.113.44")
	addLoginAudit(t, f.db, "evt-other", other.Id, "203.0.113.44")

	w := f.do(t, http.MethodGet, "/api/marketplace/admin/risk/related?user_id="+strconv.Itoa(int(f.buyerID)), nil, f.adminID)
	require.Equal(t, http.StatusOK, w.Code)
	data := decodeAdminBody(t, w)["data"].(map[string]any)
	assert.EqualValues(t, f.buyerID, data["user_id"])
	items := data["items"].([]any)
	require.Len(t, items, 1)
	relation := items[0].(map[string]any)
	assert.EqualValues(t, other.Id, relation["user_id"])
	assert.Equal(t, RiskRelationSameIP, relation["relation_type"])
	assert.Equal(t, "203.0.113.44", relation["value"])

	w = f.do(t, http.MethodGet, "/api/marketplace/admin/risk/related?user_id=0", nil, f.adminID)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
