package marketplace

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
