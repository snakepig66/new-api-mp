package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

// snapshotGitHubOAuth restores the GitHub login globals after a test, so the
// option-map state never leaks into another test in this package.
func snapshotGitHubOAuth(t *testing.T) {
	t.Helper()
	enabled, id, secret := common.GitHubOAuthEnabled, common.GitHubClientId, common.GitHubClientSecret
	common.OptionMapRWMutex.RLock()
	enabledOption, hasEnabled := common.OptionMap["GitHubOAuthEnabled"]
	idOption, hasID := common.OptionMap["GitHubClientId"]
	secretOption, hasSecret := common.OptionMap["GitHubClientSecret"]
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		common.GitHubOAuthEnabled, common.GitHubClientId, common.GitHubClientSecret = enabled, id, secret
		setOrDelete := func(key, value string, had bool) {
			if had {
				common.OptionMap[key] = value
			} else {
				delete(common.OptionMap, key)
			}
		}
		setOrDelete("GitHubOAuthEnabled", enabledOption, hasEnabled)
		setOrDelete("GitHubClientId", idOption, hasID)
		setOrDelete("GitHubClientSecret", secretOption, hasSecret)
	})
}

func TestApplyOAuthEnvOverrides(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantEnabled bool
		wantID      string
		wantSecret  string
	}{
		{
			name:        "credentials alone enable GitHub login",
			env:         map[string]string{"GITHUB_CLIENT_ID": "Iv1.abc", "GITHUB_CLIENT_SECRET": "s3cr3t"},
			wantEnabled: true,
			wantID:      "Iv1.abc",
			wantSecret:  "s3cr3t",
		},
		{
			name:        "explicit switch off wins over credentials",
			env:         map[string]string{"GITHUB_CLIENT_ID": "Iv1.abc", "GITHUB_CLIENT_SECRET": "s3cr3t", "GITHUB_OAUTH_ENABLED": "false"},
			wantEnabled: false,
			wantID:      "Iv1.abc",
			wantSecret:  "s3cr3t",
		},
		{
			name:        "enabling without credentials is refused",
			env:         map[string]string{"GITHUB_OAUTH_ENABLED": "true"},
			wantEnabled: false,
		},
		{
			name:        "only one credential never enables login",
			env:         map[string]string{"GITHUB_CLIENT_ID": "Iv1.abc"},
			wantEnabled: false,
			wantID:      "Iv1.abc",
		},
		{
			name:        "unknown switch value falls back to auto",
			env:         map[string]string{"GITHUB_CLIENT_ID": "Iv1.abc", "GITHUB_CLIENT_SECRET": "s3cr3t", "GITHUB_OAUTH_ENABLED": "maybe"},
			wantEnabled: true,
			wantID:      "Iv1.abc",
			wantSecret:  "s3cr3t",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshotGitHubOAuth(t)
			common.GitHubOAuthEnabled, common.GitHubClientId, common.GitHubClientSecret = false, "", ""
			common.OptionMap = map[string]string{}
			for key, value := range test.env {
				t.Setenv(key, value)
			}

			applyOAuthEnvOverrides()

			assert.Equal(t, test.wantEnabled, common.GitHubOAuthEnabled)
			assert.Equal(t, test.wantID, common.GitHubClientId)
			assert.Equal(t, test.wantSecret, common.GitHubClientSecret)
			assert.Equal(t, test.wantEnabled, common.OptionMap["GitHubOAuthEnabled"] == "true")
			assert.Equal(t, test.wantID, common.OptionMap["GitHubClientId"])
			assert.Equal(t, test.wantSecret, common.OptionMap["GitHubClientSecret"])
		})
	}
}

// Without any GitHub environment variable the helper must leave the values the
// option table loaded untouched, so an admin-UI configuration keeps working.
func TestApplyOAuthEnvOverridesLeavesDatabaseValuesAlone(t *testing.T) {
	snapshotGitHubOAuth(t)
	common.GitHubOAuthEnabled, common.GitHubClientId, common.GitHubClientSecret = true, "db-id", "db-secret"
	common.OptionMap = map[string]string{"GitHubOAuthEnabled": "true", "GitHubClientId": "db-id", "GitHubClientSecret": "db-secret"}
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GITHUB_CLIENT_SECRET", "")
	t.Setenv("GITHUB_OAUTH_ENABLED", "")

	applyOAuthEnvOverrides()

	assert.True(t, common.GitHubOAuthEnabled)
	assert.Equal(t, "db-id", common.GitHubClientId)
	assert.Equal(t, "db-secret", common.GitHubClientSecret)
}
