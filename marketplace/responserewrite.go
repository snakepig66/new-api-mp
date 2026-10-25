package marketplace

// responserewrite.go — 把市场请求响应里的上游模型名改回标准名 (MODEL-CHANNEL-POOL §3.4).
//
// The buyer asks for a standard model id (or an alias that normalizes to one),
// but the upstream channel sends its own name. Every marketplace request gets a
// writer wrapper that replaces only JSON `"model": "<known upstream>"` values
// with the standard name. Two properties matter:
//
//   - Only the exact upstream names selected for this request are replaced, so
//     a `"model"` string inside message content is never touched.
//   - SSE frames are rewritten line by line (the buffer keeps partial lines),
//     while a regular JSON body is rewritten per write.
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

// modelRewriteWriter wraps the gin writer for one marketplace request.
type modelRewriteWriter struct {
	gin.ResponseWriter

	mu       sync.Mutex
	standard string
	names    map[string]*regexp.Regexp // upstream name -> compiled replacement match
	buf      []byte
	sse      bool
	sseKnown bool
}

func newModelRewriteWriter(w gin.ResponseWriter, standard string) *modelRewriteWriter {
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
		// value is escaped because upstream ids may contain '.' or other
		// regex metacharacters.
		pattern := `"model"\s*:\s*"` + regexp.QuoteMeta(name) + `"`
		w.names[name] = regexp.MustCompile(pattern)
	}
}

func (w *modelRewriteWriter) rewrite(data []byte) []byte {
	if len(w.names) == 0 {
		return data
	}
	out := data
	for _, re := range w.names {
		if re.Match(out) {
			out = re.ReplaceAll(out, []byte(`"model":"`+w.standard+`"`))
		}
	}
	return out
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
