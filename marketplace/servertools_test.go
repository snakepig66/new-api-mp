package marketplace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

// unmarshalAny decodes JSON for order-independent comparison.
func unmarshalAny(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	require.NoError(t, common.Unmarshal(data, &v))
	return v
}

// 1. OpenAI chat body: {"type":"web_search"} is stripped, other tools and
// every other field survive (order-independent deep equality).
func TestStripOpenAIWebSearchTool(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","stream":true,"messages":[{"role":"user","content":"hi"}],` +
		`"tools":[{"type":"web_search"},{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]}`)

	stripped, removed, err := StripServerToolsFromRawRequest(body)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	want := `{"model":"deepseek-flash","stream":true,"messages":[{"role":"user","content":"hi"}],` +
		`"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]}`
	assert.Equal(t, unmarshalAny(t, []byte(want)), unmarshalAny(t, stripped))
}

// 2. A function tool whose function.name is exactly web_search/web_fetch is
// stripped; a user function that merely contains "search" is not.
func TestStripFunctionNamedServerTool(t *testing.T) {
	body := []byte(`{"tools":[` +
		`{"type":"function","function":{"name":"web_search","parameters":{}}},` +
		`{"type":"function","function":{"name":"web_fetch","parameters":{}}},` +
		`{"type":"function","function":{"name":"search_docs","parameters":{}}}` +
		`]}`)

	stripped, removed, err := StripServerToolsFromRawRequest(body)
	require.NoError(t, err)
	assert.Equal(t, 2, removed)

	want := `{"tools":[{"type":"function","function":{"name":"search_docs","parameters":{}}}]}`
	assert.Equal(t, unmarshalAny(t, []byte(want)), unmarshalAny(t, stripped))
}

// 3. Anthropic messages format: dated server tool blocks are stripped.
func TestStripAnthropicServerTool(t *testing.T) {
	body := []byte(`{"model":"claude-x","max_tokens":100,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],` +
		`"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":5},{"name":"custom","input_schema":{"type":"object"}}]}`)

	stripped, removed, err := StripServerToolsFromRawRequest(body)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	want := `{"model":"claude-x","max_tokens":100,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],` +
		`"tools":[{"name":"custom","input_schema":{"type":"object"}}]}`
	assert.Equal(t, unmarshalAny(t, []byte(want)), unmarshalAny(t, stripped))
}

// 4. Nothing to strip: byte-identical passthrough with removed=0.
func TestStripPassthrough(t *testing.T) {
	cases := map[string][]byte{
		"no tools key":   []byte(`{"model":"x"}`),
		"empty body":     {},
		"not json":       []byte(`this is not json`),
		"json array":     []byte(`[{"type":"web_search"}]`),
		"tools not list": []byte(`{"tools":"nope"}`),
		"empty tools":    []byte(`{"tools":[]}`),
		"only user fns":  []byte(`{"tools":[{"type":"function","function":{"name":"search_docs"}}]}`),
	}
	for name, body := range cases {
		stripped, removed, err := StripServerToolsFromRawRequest(body)
		require.NoError(t, err, name)
		assert.Equal(t, 0, removed, name)
		assert.Equal(t, body, stripped, name)
	}
}

// 4b. Every conservative type-string variant is stripped; stripping the last
// tool drops the (now empty) "tools" key entirely.
func TestStripServerToolTypeVariants(t *testing.T) {
	for _, typ := range []string{"web_search", "web_fetch", "web_search_preview", "web_search_20250305", "web_fetch_20250910"} {
		body := []byte(`{"model":"m","tools":[{"type":"` + typ + `"}]}`)
		stripped, removed, err := StripServerToolsFromRawRequest(body)
		require.NoError(t, err, typ)
		assert.Equal(t, 1, removed, typ)
		assert.Equal(t, unmarshalAny(t, []byte(`{"model":"m"}`)), unmarshalAny(t, stripped), typ)
	}
}

// 5. ExtractServerToolCounts over the documented candidate paths.
func TestExtractServerToolCounts(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		search, fetch int64
		ok            bool
	}{
		{"anthropic server_tool_use", `{"usage":{"server_tool_use":{"web_search_requests":3,"web_fetch_requests":1}}}`, 3, 1, true},
		{"top-level usage fields", `{"usage":{"web_search_requests":2,"web_fetch_requests":4}}`, 2, 4, true},
		{"search only", `{"usage":{"server_tool_use":{"web_search_requests":5}}}`, 5, 0, true},
		{"fetch only top-level", `{"usage":{"web_fetch_requests":7}}`, 0, 7, true},
		{"nested wins over top-level", `{"usage":{"server_tool_use":{"web_search_requests":3},"web_search_requests":9}}`, 3, 0, true},
		{"bare usage object accepted", `{"server_tool_use":{"web_search_requests":2}}`, 2, 0, true},
		{"no fields", `{"usage":{"prompt_tokens":1,"completion_tokens":2}}`, 0, 0, false},
		{"empty usage", `{"usage":{}}`, 0, 0, false},
		{"not json", `nope`, 0, 0, false},
		{"empty body", ``, 0, 0, false},
	}
	for _, tc := range cases {
		search, fetch, ok := ExtractServerToolCounts([]byte(tc.body))
		assert.Equal(t, tc.search, search, tc.name)
		assert.Equal(t, tc.fetch, fetch, tc.name)
		assert.Equal(t, tc.ok, ok, tc.name)
	}
}

// 6. The stripped body is valid JSON and equivalent to the original except
// for the removed entries (field order irrelevant).
func TestStripRoundTripEquivalence(t *testing.T) {
	body := []byte(`{"model":"m","n":1,"nested":{"a":[1,2,{"b":true}]},"tools":[{"type":"web_search"},{"type":"function","function":{"name":"f"}}]}`)

	stripped, removed, err := StripServerToolsFromRawRequest(body)
	require.NoError(t, err)
	require.Equal(t, 1, removed)

	var obj map[string]any
	require.NoError(t, common.Unmarshal(stripped, &obj), "stripped body must stay valid JSON")
	assert.Equal(t, float64(1), obj["n"])
	assert.Equal(t, unmarshalAny(t, []byte(`{"a":[1,2,{"b":true}]}`)), obj["nested"])
	assert.Equal(t, unmarshalAny(t, []byte(`[{"type":"function","function":{"name":"f"}}]`)), obj["tools"])

	// Re-serializing the decoded stripped body and decoding again is stable.
	again, err := common.Marshal(obj)
	require.NoError(t, err)
	assert.Equal(t, unmarshalAny(t, stripped), unmarshalAny(t, again))
}
