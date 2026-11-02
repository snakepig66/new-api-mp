package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateKeyFormat(t *testing.T) {
	key, err := GenerateKey()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(key, "sk_"), "key should start with sk_")
	assert.Equal(t, 51, len(key), "key should have sk_ prefix + 48 random chars")

	key2, err := GenerateKey()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(key2, "sk_"))
	assert.NotEqual(t, key, key2, "generated keys should be distinct")
}
