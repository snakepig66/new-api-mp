package marketplace

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

func newRewriteTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, rec
}

func TestModelRewriteJSON(t *testing.T) {
	c, rec := newRewriteTestContext(t)
	installModelRewrite(c, "glm-5.3", "glm-5.3-oc")

	body := `{"id":"x","model":"glm-5.3-oc","choices":[{"message":{"role":"user","content":"{\"model\":\"glm-5.3-oc\"} is text"}}]}`
	_, err := c.Writer.Write([]byte(body))
	require.NoError(t, err)

	got := rec.Body.String()
	assert.Contains(t, got, `"model":"glm-5.3"`, "the response model is the standard name")
	assert.Contains(t, got, `{\"model\":\"glm-5.3-oc\"} is text`, "content strings are untouched")
	assert.NotContains(t, got, `"model":"glm-5.3-oc"`)
}

func TestModelRewriteUnknownModelUntouched(t *testing.T) {
	c, rec := newRewriteTestContext(t)
	installModelRewrite(c, "glm-5.3", "glm-5.3-oc")

	_, err := c.Writer.Write([]byte(`{"model":"another-model"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"model":"another-model"}`, rec.Body.String())
}

func TestModelRewriteSSEFramesAcrossWrites(t *testing.T) {
	c, rec := newRewriteTestContext(t)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	installModelRewrite(c, "glm-5.3", "glm-5.3-oc")

	// Split a frame across two writes; the model value itself is split too.
	_, err := c.Writer.Write([]byte("data: {\"model\":\"glm-5.3-"))
	require.NoError(t, err)
	assert.Empty(t, rec.Body.String(), "partial line is buffered")

	_, err = c.Writer.Write([]byte("oc\",\"choices\":[]}\n\ndata: {\"model\":\"glm-5.3-oc\"}\n\n"))
	require.NoError(t, err)

	got := rec.Body.String()
	assert.Equal(t, 2, strings.Count(got, `"model":"glm-5.3"`))
	assert.NotContains(t, got, "glm-5.3-oc")
}

func TestModelRewriteSwitchUpstream(t *testing.T) {
	c, rec := newRewriteTestContext(t)
	installModelRewrite(c, "glm-5.3", "upstream-a")
	_, err := c.Writer.Write([]byte(`{"model":"upstream-a"}`))
	require.NoError(t, err)

	// A retry switched to another upstream; both names map to the standard id.
	updateModelRewrite(c, "upstream-b")
	_, err = c.Writer.Write([]byte(`{"model":"upstream-b"}`))
	require.NoError(t, err)

	got := rec.Body.String()
	assert.Equal(t, `{"model":"glm-5.3"}{"model":"glm-5.3"}`, got)
}

// The rewrite can change the body length, so the upstream Content-Length must
// not survive: a stale value makes the client stop early (unexpected EOF).
func TestModelRewriteDropsContentLength(t *testing.T) {
	body := `{"id":"x","model":"glm-5.3-oc"}`
	for _, tc := range []struct {
		name string
		late bool
	}{
		{"set before install", false},
		{"set after install", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newRewriteTestContext(t)
			if !tc.late {
				c.Writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
			}
			installModelRewrite(c, "glm-5.3", "glm-5.3-oc")
			if tc.late {
				// Simulated upstream setting it after the wrapper is installed.
				c.Writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
			}

			_, err := c.Writer.Write([]byte(body))
			require.NoError(t, err)

			assert.Empty(t, rec.Header().Get("Content-Length"), "rewritten body length differs from upstream's")
			assert.Equal(t, `{"id":"x","model":"glm-5.3"}`, rec.Body.String())
		})
	}
}

// An upstream model with a date/version suffix still rewrites to the standard
// id, as long as the suffix starts with one of the recognized separators.
func TestModelRewriteVersionSuffixJSON(t *testing.T) {
	cases := []struct {
		standard string
		upstream string
		body     string
	}{
		{"deepseek-v4-pro", "deepseek-v4-pro", `{"model":"deepseek-v4-pro-0813"}`},
		{"kimi-k3", "kimi-k3", `{"model":"kimi-k3-20251025"}`},
		{"glm-5.3-flash", "glm-5.3-flash", `{"model":"glm-5.3-flash-0912"}`},
		{"glm-5.3", "glm-5.3", `{"model":"glm-5.3@2026"}`},
		{"kimi-k3", "kimi-k3", `{"model":"kimi-k3_20251025"}`},
		{"kimi-k3", "kimi-k3", `{"model":"kimi-k3.20251025"}`},
		{"kimi-k3", "kimi-k3", `{"model":"kimi-k3/20251025"}`},
	}
	for _, tc := range cases {
		c, rec := newRewriteTestContext(t)
		installModelRewrite(c, tc.standard, tc.upstream)
		_, err := c.Writer.Write([]byte(tc.body))
		require.NoError(t, err)
		assert.Equal(t, `{"model":"`+tc.standard+`"}`, rec.Body.String(), "body %s", tc.body)
	}
}

// Suffix matching is prefix-bounded: a name that merely starts with the
// registered upstream id but continues without a separator is not rewritten,
// and unrelated model values stay untouched.
func TestModelRewriteSuffixDoesNotOvermatch(t *testing.T) {
	c, rec := newRewriteTestContext(t)
	installModelRewrite(c, "glm-5.3", "glm-5.3-oc")

	body := `{"model":"glm-5.3-ocx"}{"model":"another-model-0813"}`
	_, err := c.Writer.Write([]byte(body))
	require.NoError(t, err)
	assert.Equal(t, body, rec.Body.String())
}

func TestModelRewriteVersionSuffixSSE(t *testing.T) {
	c, rec := newRewriteTestContext(t)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	installModelRewrite(c, "kimi-k3", "kimi-k3")

	_, err := c.Writer.Write([]byte("data: {\"model\":\"kimi-k3-20251025\"}\n\ndata: {\"model\":\"glm-5.3\"}\n\n"))
	require.NoError(t, err)

	got := rec.Body.String()
	assert.Contains(t, got, `data: {"model":"kimi-k3"}`)
	assert.Contains(t, got, `data: {"model":"glm-5.3"}`, "unregistered upstream values stay untouched")
}

// system_fingerprint identifies the upstream deployment, so it must never reach
// the buyer. Removing it must leave the surrounding JSON valid.
func TestModelRewriteStripsSystemFingerprint(t *testing.T) {
	cases := []string{
		`{"id":"x","model":"glm-5.3","system_fingerprint":"fp_upstream","choices":[]}`,
		`{"system_fingerprint":"fp_upstream","id":"x","model":"glm-5.3"}`,
		`{"id":"x","model":"glm-5.3","system_fingerprint":"fp_upstream"}`,
	}
	for _, body := range cases {
		c, rec := newRewriteTestContext(t)
		installModelRewrite(c, "glm-5.3", "glm-5.3")

		_, err := c.Writer.Write([]byte(body))
		require.NoError(t, err)

		got := rec.Body.String()
		assert.NotContains(t, got, "system_fingerprint", "body %s", body)
		assert.NotContains(t, got, "fp_upstream", "body %s", body)
		var decoded map[string]any
		require.NoError(t, common.Unmarshal([]byte(got), &decoded), "body %s", got)
		assert.Equal(t, "glm-5.3", decoded["model"], "body %s", got)
		assert.Equal(t, "x", decoded["id"], "body %s", got)
	}
}

func TestModelRewriteStripsSystemFingerprintSSE(t *testing.T) {
	c, rec := newRewriteTestContext(t)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	installModelRewrite(c, "kimi-k3", "kimi-k3")

	_, err := c.Writer.Write([]byte("data: {\"model\":\"kimi-k3\",\"system_fingerprint\":\"fp_s\"}\n\n"))
	require.NoError(t, err)

	got := rec.Body.String()
	assert.Equal(t, "data: {\"model\":\"kimi-k3\"}\n\n", got)
}

// A suffix that is not a pure date identifies a different model id: it is not
// rewritten and is recorded as a fingerprint anomaly, while the raw upstream
// model is still captured for mp_requests.
func TestModelRewriteNonDateSuffixIsAnomaly(t *testing.T) {
	c, rec := newRewriteTestContext(t)
	installModelRewrite(c, "glm-5.3", "glm-5.3-oc")

	body := `{"model":"glm-5.3-oc-beta"}`
	_, err := c.Writer.Write([]byte(body))
	require.NoError(t, err)

	assert.Equal(t, body, rec.Body.String(), "a non-date suffix names another model")
	upstream, anomaly := responseIdentityFromContext(c)
	assert.Equal(t, "glm-5.3-oc-beta", upstream)
	assert.True(t, anomaly)
}

// The raw upstream model is captured before rewriting; exact ids and pure date
// suffixes are expected, anything else is an anomaly.
func TestModelRewriteRecordsRawUpstreamModel(t *testing.T) {
	cases := []struct {
		value   string
		anomaly bool
	}{
		{"glm-5.3-oc", false},
		{"glm-5.3-oc-2024-01-01", false},
		{"glm-5.3", false},
		{"glm-5.3-oc-beta", true},
	}
	for _, tc := range cases {
		c, _ := newRewriteTestContext(t)
		installModelRewrite(c, "glm-5.3", "glm-5.3-oc")
		_, err := c.Writer.Write([]byte(`{"model":"` + tc.value + `"}`))
		require.NoError(t, err)

		upstream, anomaly := responseIdentityFromContext(c)
		assert.Equal(t, tc.value, upstream, "raw upstream model is recorded")
		assert.Equal(t, tc.anomaly, anomaly, "anomaly flag for %s", tc.value)
	}
}

// A model value the buyer never sees is not rewritten; the writer records the
// mismatch instead of corrupting the body.
func TestModelRewriteUnknownModelRecordsAnomaly(t *testing.T) {
	c, rec := newRewriteTestContext(t)
	installModelRewrite(c, "glm-5.3", "glm-5.3-oc")

	_, err := c.Writer.Write([]byte(`{"model":"another-model"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"model":"another-model"}`, rec.Body.String())

	upstream, anomaly := responseIdentityFromContext(c)
	assert.Equal(t, "another-model", upstream)
	assert.True(t, anomaly)
}
