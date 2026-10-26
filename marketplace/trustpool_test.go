package marketplace

// trustpool_test.go — 认证池 (trustpool.go) 的匹配与分类单测。

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// createTrustPoolRow inserts one operator-maintained trust-pool row.
func createTrustPoolRow(t *testing.T, db *gorm.DB, row TrustPool) TrustPool {
	t.Helper()
	require.NoError(t, db.Create(&row).Error)
	return row
}

func TestMatchTrustPoolExactHost(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	row := createTrustPoolRow(t, db, TrustPool{
		Host: "api.vendor-one.example", Kind: TrustKindThirdParty,
		Trust: TrustTrusted, Source: "manual",
	})
	got, err := MatchTrustPoolDB(db, " API.Vendor-One.Example:443 ", "", "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, row.ID, got.ID)
}

func TestMatchTrustPoolHostPattern(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	row := createTrustPoolRow(t, db, TrustPool{
		HostPattern: `ws-[a-z0-9]+\.pattern\.example`,
		Kind:        TrustKindOfficial, Trust: TrustTrusted, Source: "manual",
	})
	got, err := MatchTrustPoolDB(db, "ws-abc.pattern.example", "", "")
	require.NoError(t, err)
	require.NotNil(t, got, "an unanchored stored pattern is anchored by the matcher")
	assert.Equal(t, row.ID, got.ID)

	for _, host := range []string{"xws-abc.pattern.example", "ws-abc.pattern.example.evil"} {
		got, err = MatchTrustPoolDB(db, host, "", "")
		require.NoError(t, err)
		assert.Nil(t, got, "pattern %q must not match %q", row.HostPattern, host)
	}
}

func TestMatchTrustPoolPathPrefixLongest(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	createTrustPoolRow(t, db, TrustPool{
		Host: "api.paths.example", Kind: TrustKindThirdParty,
		Trust: TrustWatch, Source: "manual",
	})
	long := createTrustPoolRow(t, db, TrustPool{
		Host: "api.paths.example", PathPrefix: "/api/v1",
		Kind: TrustKindThirdParty, Trust: TrustTrusted, Source: "manual",
	})

	cases := []struct {
		path string
		want int64
	}{
		{"/api/v1/chat/completions", long.ID},
		{"/api/v1", long.ID},
		{"/api/v10/chat", 0}, // segment boundary: not the /api/v1 row
		{"/api", 0},
		{"", 0},
	}
	for _, tc := range cases {
		got, err := MatchTrustPoolDB(db, "api.paths.example", tc.path, "")
		require.NoError(t, err)
		require.NotNil(t, got, "path %q must match the host-level row", tc.path)
		if tc.want == 0 {
			assert.Empty(t, got.PathPrefix, "path %q must fall back to the host-level row", tc.path)
		} else {
			assert.Equal(t, tc.want, got.ID, "path %q must select the longest prefix", tc.path)
		}
	}
}

func TestMatchTrustPoolKeyPrefix(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	generic := createTrustPoolRow(t, db, TrustPool{
		Host: "api.keys.example", Kind: TrustKindThirdParty,
		Trust: TrustTrusted, Source: "manual",
	})
	specific := createTrustPoolRow(t, db, TrustPool{
		Host: "api.keys.example", KeyPrefix: "sk-sp-",
		Kind: TrustKindPlan, Trust: TrustWatch, Source: "manual",
	})

	got, err := MatchTrustPoolDB(db, "api.keys.example", "", "sk-sp-abc")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, specific.ID, got.ID, "a matching key prefix is more specific")

	got, err = MatchTrustPoolDB(db, "api.keys.example", "", "sk-other")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, generic.ID, got.ID)

	got, err = MatchTrustPoolDB(db, "api.keys.example", "", "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, generic.ID, got.ID, "an unknown credential never matches a key-specific row")
}

func TestMatchTrustPoolNoMatch(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	createTrustPoolRow(t, db, TrustPool{
		Host: "api.known.example", Kind: TrustKindThirdParty,
		Trust: TrustTrusted, Source: "manual",
	})
	got, err := MatchTrustPoolDB(db, "api.unknown.example", "/v1", "sk-anything")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// TestMatchTrustPoolStricterTieBreak keeps a blocked row from losing to a
// trusted row of identical specificity.
func TestMatchTrustPoolStricterTieBreak(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	createTrustPoolRow(t, db, TrustPool{
		Host: "api.ties.example", Kind: TrustKindThirdParty,
		Trust: TrustTrusted, Source: "manual",
	})
	blocked := createTrustPoolRow(t, db, TrustPool{
		Host: "api.ties.example", Kind: TrustKindThirdParty,
		Trust: TrustBlocked, Source: "manual",
	})

	got, err := MatchTrustPoolDB(db, "api.ties.example", "", "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, blocked.ID, got.ID)
}

func TestCheckTrustPoolStates(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	createTrustPoolRow(t, db, TrustPool{Host: "blocked.state.example", Trust: TrustBlocked, Kind: TrustKindThirdParty, Source: "manual"})
	createTrustPoolRow(t, db, TrustPool{Host: "watch.state.example", Trust: TrustWatch, Kind: TrustKindThirdParty, Source: "manual"})
	createTrustPoolRow(t, db, TrustPool{Host: "trusted.state.example", Trust: TrustTrusted, Kind: TrustKindOfficial, Source: "manual"})
	createTrustPoolRow(t, db, TrustPool{Host: "future.state.example", Trust: TrustTrusted, Kind: TrustKindOfficial, Source: "manual", ExpiresAt: &future})
	createTrustPoolRow(t, db, TrustPool{Host: "expired.state.example", Trust: TrustTrusted, Kind: TrustKindOfficial, Source: "manual", ExpiresAt: &past})
	createTrustPoolRow(t, db, TrustPool{Host: "unknown.state.example", Trust: "weird", Kind: TrustKindThirdParty, Source: "manual"})
	createTrustPoolRow(t, db, TrustPool{
		Host: "denied.state.example", Trust: TrustTrusted, Kind: TrustKindOfficial, Source: "manual",
		DenyPaths: `["/api/coding"]`, DenyKeyPrefixes: `["sk-sp-"]`,
	})

	cases := []struct {
		name      string
		host      string
		path      string
		key       string
		wantTrust string
		wantErr   bool
	}{
		{name: "blocked refuses", host: "blocked.state.example", wantTrust: TrustBlocked, wantErr: true},
		{name: "watch needs review", host: "watch.state.example", wantTrust: TrustWatch},
		{name: "trusted no expiry", host: "trusted.state.example", wantTrust: TrustTrusted},
		{name: "trusted future expiry", host: "future.state.example", wantTrust: TrustTrusted},
		{name: "trusted expired downgrades", host: "expired.state.example", wantTrust: TrustWatch},
		{name: "unlisted", host: "not-listed.example", wantTrust: TrustUnlisted},
		{name: "unknown state fails closed", host: "unknown.state.example", wantTrust: TrustWatch},
		{name: "denied path refuses", host: "denied.state.example", path: "/api/coding/v3", wantTrust: TrustBlocked, wantErr: true},
		{name: "denied key prefix refuses", host: "denied.state.example", key: "sk-sp-plan", wantTrust: TrustBlocked, wantErr: true},
		{name: "unknown key is not denied", host: "denied.state.example", key: "", wantTrust: TrustTrusted},
		{name: "unrelated path is allowed", host: "denied.state.example", path: "/api/v1", wantTrust: TrustTrusted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trust, pool, err := CheckTrustPoolDB(db, tc.host, tc.path, tc.key)
			if tc.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrTrustPoolBlocked)
				if tc.key != "" {
					assert.NotContains(t, err.Error(), tc.key, "no credential may appear in the error")
				}
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantTrust, trust)
			if tc.wantTrust == TrustUnlisted {
				assert.Nil(t, pool)
			} else {
				assert.NotNil(t, pool)
			}
		})
	}
}

func TestSeedTrustPoolIdempotent(t *testing.T) {
	db := newTestDB(t)
	migrateAll(t, db)
	require.NoError(t, SeedTrustPool(db))
	require.NoError(t, SeedTrustPool(db))

	var seeded int64
	require.NoError(t, db.Model(&TrustPool{}).Where("source = ?", "seed").Count(&seeded).Error)
	assert.EqualValues(t, len(trustPoolSeeds), seeded, "re-seeding must not duplicate rows")

	trust, pool, err := CheckTrustPoolDB(db, "api.deepseek.com", "", "")
	require.NoError(t, err)
	assert.Equal(t, TrustTrusted, trust)
	require.NotNil(t, pool)
	assert.Equal(t, ChannelKindOfficial, pool.Kind)
	assert.JSONEq(t, `["openai_chat","anthropic_messages"]`, pool.Endpoints)

	// Bailian workspace domains match by anchored pattern.
	trust, _, err = CheckTrustPoolDB(db, "ws-team.cn-beijing.maas.aliyuncs.com", "/compatible-mode/v1", "")
	require.NoError(t, err)
	assert.Equal(t, TrustTrusted, trust)

	// The Ark plan paths stay refused even though the host is trusted.
	_, _, err = CheckTrustPoolDB(db, "ark.cn-beijing.volces.com", "/api/coding/v3", "")
	assert.ErrorIs(t, err, ErrTrustPoolBlocked)

	// Bailian plan keys are refused on the official host.
	_, _, err = CheckTrustPoolDB(db, "dashscope.aliyuncs.com", "/compatible-mode/v1", "sk-sp-plan-key")
	assert.ErrorIs(t, err, ErrTrustPoolBlocked)
}

// TestNormalizeTrustPoolAddress covers the input canonicalization the admin API
// applies before storing a row. The governance enum/expiry/notes branches are
// covered end-to-end by TestAdminTrustPoolCRUDAndGovernance in api_admin_test.go.
func TestNormalizeTrustPoolAddress(t *testing.T) {
	cases := []struct {
		name        string
		host        string
		pattern     string
		wantHost    string
		wantPattern string
		wantErr     error
	}{
		{name: "host is lowercased and port stripped", host: " API.Example.COM:443 ", wantHost: "api.example.com"},
		{name: "pattern is stored verbatim", pattern: `^[a-z]+\.example$`, wantPattern: `^[a-z]+\.example$`},
		{name: "neither", wantErr: ErrTrustPoolAddressRequired},
		{name: "both", host: "a.example", pattern: "^a\\.", wantErr: ErrTrustPoolAddressAmbiguous},
		{name: "pattern does not compile", pattern: "[", wantErr: ErrTrustPoolPatternInvalid},
		{name: "host is a URL", host: "https://api.example.com/v1", wantErr: ErrTrustPoolHostInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, pattern, err := NormalizeTrustPoolAddress(tc.host, tc.pattern)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantHost, host)
			assert.Equal(t, tc.wantPattern, pattern)
		})
	}
}
