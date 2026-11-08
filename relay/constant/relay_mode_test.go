package constant

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPath2RelayMode(t *testing.T) {
	tests := []struct {
		path string
		want int
	}{
		{path: "/v1/alpha/search", want: RelayModeAlphaSearch},
		{path: "/v1/alpha/search?foo=1", want: RelayModeAlphaSearch},
		{path: "/v1/tools/search", want: RelayModeToolsSearch},
		{path: "/v1/tools/fetch", want: RelayModeToolsFetch},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.want, Path2RelayMode(tt.path))
		})
	}
}

func TestPath2RelayModeClaudeMessages(t *testing.T) {
	assert.Equal(t, RelayModeClaude, Path2RelayMode("/v1/messages"))
	assert.Equal(t, RelayModeClaude, Path2RelayMode("/v1/messages?beta=true"))
}

func TestBuildEndpointURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		kind EndpointKind
		want string
	}{
		{"bare base gets the fixed path", "https://api.deepseek.com", EndpointOpenAIChat, "https://api.deepseek.com/v1/chat/completions"},
		{"trailing slash is ignored", "https://api.deepseek.com/", EndpointOpenAIResponses, "https://api.deepseek.com/v1/responses"},
		{"v1 base is not doubled", "https://api.moonshot.cn/v1", EndpointOpenAIChat, "https://api.moonshot.cn/v1/chat/completions"},
		{"v1 responses base is not doubled", "https://api.openai.com/v1", EndpointOpenAIResponses, "https://api.openai.com/v1/responses"},
		{"ark v3 base", "https://ark.cn-beijing.volces.com/api/v3", EndpointOpenAIChat, "https://ark.cn-beijing.volces.com/api/v3/chat/completions"},
		{"zhipu v4 base", "https://open.bigmodel.cn/api/paas/v4", EndpointOpenAIChat, "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"anthropic base", "https://api.deepseek.com/anthropic", EndpointAnthropicMessages, "https://api.deepseek.com/anthropic/v1/messages"},
		{"anthropic v1 base", "https://api.anthropic.com/v1", EndpointAnthropicMessages, "https://api.anthropic.com/v1/messages"},
		{"empty base", "", EndpointOpenAIChat, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, BuildEndpointURL(tt.base, tt.kind))
		})
	}
}

func TestFilterAnthropicBetaValues(t *testing.T) {
	values := []string{"context-1m-2025-08-07, extended-cache-ttl-2025-04-11", "unknown-beta-2099"}
	assert.Equal(t, []string{"context-1m-2025-08-07", "extended-cache-ttl-2025-04-11"},
		FilterAnthropicBetaValues(values, true))
	assert.Equal(t, []string{"extended-cache-ttl-2025-04-11"},
		FilterAnthropicBetaValues(values, false), "an unpriced 1M window drops the beta")
	assert.Empty(t, FilterAnthropicBetaValues([]string{"unknown-beta-2099"}, true))
}
