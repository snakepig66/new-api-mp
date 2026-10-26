package openai

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The marketplace's two OpenAI endpoints are built from the configured base
// plus a fixed path. The buyer's request path and the legacy strip setting are
// deliberately ignored.
func TestGetRequestURLUsesFixedEndpointPaths(t *testing.T) {
	tests := []struct {
		name        string
		base        string
		requestPath string
		mode        int
		format      types.RelayFormat
		messages    string
		want        string
	}{
		{
			name: "chat from a bare base", base: "https://api.deepseek.com",
			requestPath: "/v1/chat/completions?client=1",
			mode:        relayconstant.RelayModeChatCompletions, format: types.RelayFormatOpenAI,
			want: "https://api.deepseek.com/v1/chat/completions",
		},
		{
			name: "chat does not double a version segment", base: "https://api.moonshot.cn/v1",
			requestPath: "/v1/chat/completions",
			mode:        relayconstant.RelayModeChatCompletions, format: types.RelayFormatOpenAI,
			want: "https://api.moonshot.cn/v1/chat/completions",
		},
		{
			name: "chat from a v3 base", base: "https://ark.cn-beijing.volces.com/api/v3",
			requestPath: "/v1/chat/completions",
			mode:        relayconstant.RelayModeChatCompletions, format: types.RelayFormatOpenAI,
			want: "https://ark.cn-beijing.volces.com/api/v3/chat/completions",
		},
		{
			name: "responses", base: "https://api.deepseek.com",
			requestPath: "/v1/responses",
			mode:        relayconstant.RelayModeResponses, format: types.RelayFormatOpenAIResponses,
			want: "https://api.deepseek.com/v1/responses",
		},
		{
			name: "responses on a v1 base", base: "https://api.openai.com/v1",
			requestPath: "/v1/responses",
			mode:        relayconstant.RelayModeResponses, format: types.RelayFormatOpenAIResponses,
			want: "https://api.openai.com/v1/responses",
		},
		{
			name: "claude format uses the stored messages endpoint", base: "https://api.deepseek.com",
			mode: relayconstant.RelayModeClaude, format: types.RelayFormatClaude,
			messages: "https://api.deepseek.com/anthropic/v1/messages",
			want:     "https://api.deepseek.com/anthropic/v1/messages",
		},
		{
			name: "claude format falls back to the fixed path", base: "https://api.deepseek.com/anthropic",
			mode: relayconstant.RelayModeClaude, format: types.RelayFormatClaude,
			want: "https://api.deepseek.com/anthropic/v1/messages",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayMode:      tt.mode,
				RelayFormat:    tt.format,
				RequestURLPath: tt.requestPath,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl: tt.base,
					ChannelSetting: dto.ChannelSettings{MPAnthropicMessagesURL: tt.messages},
				},
			}
			got, err := (&Adaptor{}).GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
