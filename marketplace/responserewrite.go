package marketplace

// responserewrite.go — 把市场请求响应里的上游模型名改回标准名 (MODEL-CHANNEL-POOL §3.4).
//
// The buyer asks for a standard model id (or an alias that normalizes to one),
// but the upstream channel sends its own name. Every marketplace request gets a
// writer wrapper that replaces only JSON `"model": "<known upstream>"` values
// with the standard name. Five properties matter:
//
//   - Only the upstream names selected for this request are replaced, so a
//     `"model"` string inside message content is never touched. An upstream
//     name followed by a pure date/version suffix (`-2024-01-01`, `-20251025`,
//     `_20251025`, `.20251025`, `/20251025`, `@2026`) is also matched. Any
//     other suffix is a different model id: it is left untouched and recorded
//     as a model fingerprint anomaly.
//   - OpenAI's `system_fingerprint` identifies the upstream deployment and is
//     removed from the response body.
//   - The raw upstream model id is captured before rewriting so the settlement
//     record can store it (`mp_requests.upstream_model`).
//   - SSE frames are rewritten line by line (the buffer keeps partial lines),
//     while a regular JSON body is rewritten per write.
//   - Rewriting can change the body length, so a survivor Content-Length from
//     the upstream would make the client stop early (unexpected EOF). The
//     header is dropped when the wrapper is installed and again before every
//     non-SSE write.
//
// Non-marketplace requests never get the wrapper.

import (
	"bytes"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

const modelRewriteContextKey = "mp_model_rewrite"

// dateSuffixExpr matches a pure date/version suffix that an upstream appends to
// its registered model id. A suffix with any other content (e.g. "-oc") names a
// different model and must not be rewritten.
const dateSuffixExpr = `(?:[-_.@/]\d{4,8}(?:[-_.]\d{1,2}){0,2})?`

var (
	dateSuffixPattern = regexp.MustCompile(`^` + dateSuffixExpr + `$`)
	// modelValuePattern reads every JSON model value in one body chunk.
	modelValuePattern = regexp.MustCompile(`"model"\s*:\s*"([^"]*)"`)
	// systemFingerprintPattern removes system_fingerprint together with its
	// separating comma so the surrounding JSON stays valid. Escaped quotes
	// (for example inside a content string) do not match.
	systemFingerprintPattern = regexp.MustCompile(`,\s*"system_fingerprint"\s*:\s*"(?:[^"\\]|\\.)*"|"system_fingerprint"\s*:\s*"(?:[^"\\]|\\.)*"\s*,\s*`)
)

// modelRewriteWriter wraps the gin writer for one marketplace request.
type modelRewriteWriter struct {
	gin.ResponseWriter

	mu       sync.Mutex
	standard string
	names    map[string]*regexp.Regexp // upstream name -> compiled replacement match
	buf      []byte
	sse      bool
	sseKnown bool

	// upstreamModel is the raw model id read from the response body before
	// rewriting. modelAnomaly records that the upstream returned an id that is
	// neither a selected upstream name nor a date-suffixed variant of one.
	upstreamModel string
	modelAnomaly  bool
}

func newModelRewriteWriter(w gin.ResponseWriter, standard string) *modelRewriteWriter {
	// The rewrite may change the body length, so the upstream's Content-Length
	// is never valid for the rewritten response.
	w.Header().Del("Content-Length")
	return &modelRewriteWriter{
		ResponseWriter: w,
		standard:       standard,
		names:          make(map[string]*regexp.Regexp),
	}
}

// setUpstream replaces the set of upstream names rewritten to the standard
// name. Called when the first listing is selected and again on every channel
// switch, so a request that fails over to another upstream still answers with
// the standard model.
func (w *modelRewriteWriter) setUpstream(names ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.names = make(map[string]*regexp.Regexp, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		// Match only a JSON string value under the exact "model" key. The
		// upstream may append a date/version suffix to its registered name, so
		// allow such a suffix (and nothing else). The value is escaped because
		// upstream ids may contain '.' or other regex metacharacters.
		pattern := `"model"\s*:\s*"` + regexp.QuoteMeta(name) + dateSuffixExpr + `"`
		w.names[name] = regexp.MustCompile(pattern)
	}
}

func (w *modelRewriteWriter) rewrite(data []byte) []byte {
	out := data
	if len(w.names) > 0 {
		w.observeModel(out)
		replacement := []byte(`"model":"` + w.standard + `"`)
		for _, re := range w.names {
			if re.Match(out) {
				// Literal replacement: a '$' in the standard id must not be read as a
				// capture-group expansion.
				out = re.ReplaceAllLiteral(out, replacement)
			}
		}
	}
	if bytes.Contains(out, []byte("system_fingerprint")) {
		out = systemFingerprintPattern.ReplaceAllLiteral(out, nil)
	}
	return out
}

// observeModel records the raw upstream model of the response and flags a
// fingerprint anomaly when the returned id is not one of the upstream names
// selected for this request (exact or date-suffixed).
func (w *modelRewriteWriter) observeModel(data []byte) {
	for _, m := range modelValuePattern.FindAllSubmatch(data, -1) {
		value := string(m[1])
		if value == "" {
			continue
		}
		w.upstreamModel = value
		if !w.isExpectedModel(value) {
			w.modelAnomaly = true
		}
	}
}

// isExpectedModel reports whether value is the standard id, a selected
// upstream name, or one of those plus a pure date/version suffix.
func (w *modelRewriteWriter) isExpectedModel(value string) bool {
	if value == w.standard {
		return true
	}
	for name := range w.names {
		if value == name {
			return true
		}
		if rest, ok := strings.CutPrefix(value, name); ok && dateSuffixPattern.MatchString(rest) {
			return true
		}
	}
	return false
}

// Write buffers SSE frames by line so a frame split across writes is still
// rewritten, and rewrites ordinary JSON writes as they arrive.
func (w *modelRewriteWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.sseKnown {
		w.sse = strings.Contains(w.Header().Get("Content-Type"), "text/event-stream")
		w.sseKnown = true
	}
	if !w.sse {
		// The upstream may set Content-Length after the wrapper was installed;
		// drop it again right before the (possibly shorter/longer) body write.
		w.Header().Del("Content-Length")
		_, err := w.ResponseWriter.Write(w.rewrite(data))
		return len(data), err
	}
	w.buf = append(w.buf, data...)
	var out bytes.Buffer
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := w.buf[:idx+1]
		out.Write(w.rewrite(line))
		w.buf = w.buf[idx+1:]
	}
	if out.Len() > 0 {
		if _, err := w.ResponseWriter.Write(out.Bytes()); err != nil {
			return len(data), err
		}
	}
	return len(data), nil
}

// Flush forwards any complete buffered SSE remainder and then flushes.
func (w *modelRewriteWriter) Flush() {
	w.mu.Lock()
	if w.sse && len(w.buf) > 0 {
		leftover := w.rewrite(w.buf)
		w.buf = nil
		_, _ = w.ResponseWriter.Write(leftover)
	}
	w.mu.Unlock()
	w.ResponseWriter.Flush()
}

// installModelRewrite wraps c.Writer once per marketplace request.
func installModelRewrite(c *gin.Context, standard string, upstreams ...string) *modelRewriteWriter {
	existing, ok := c.Get(modelRewriteContextKey)
	if ok {
		if w, ok := existing.(*modelRewriteWriter); ok {
			w.setUpstream(upstreams...)
			return w
		}
	}
	w := newModelRewriteWriter(c.Writer, standard)
	w.setUpstream(upstreams...)
	c.Writer = w
	c.Set(modelRewriteContextKey, w)
	return w
}

// updateModelRewrite adds upstream names to an already installed wrapper; it
// is a no-op for non-marketplace requests.
func updateModelRewrite(c *gin.Context, upstreams ...string) {
	existing, ok := c.Get(modelRewriteContextKey)
	if !ok {
		return
	}
	if w, ok := existing.(*modelRewriteWriter); ok {
		w.setUpstream(upstreams...)
	}
}

// responseIdentityFromContext returns the raw upstream model id read from the
// response body and whether it looked like a fingerprint anomaly. It returns
// the zero values for non-marketplace requests (no rewrite writer installed).
func responseIdentityFromContext(c *gin.Context) (string, bool) {
	if c == nil {
		return "", false
	}
	existing, ok := c.Get(modelRewriteContextKey)
	if !ok {
		return "", false
	}
	w, ok := existing.(*modelRewriteWriter)
	if !ok {
		return "", false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.upstreamModel, w.modelAnomaly
}
