package marketplace

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/QuantumNous/new-api/common"
)

// Server-side Search/Fetch tool handling (§3.4). Server tools are billed per
// call from the usage the upstream reports; when the counts cannot be read
// the tools must not reach the upstream at all (读不到次数时不能估算收费), so
// the inbound raw request is stripped before any format conversion.
//
// The strip is a byte-level edit (gjson for inspection, sjson for deletion)
// rather than a decode/re-encode round trip: field order, whitespace and
// numeric precision of every untouched field survive exactly (M12).

// isServerToolName reports whether a type or function name identifies a
// server-side search/fetch tool. The match is deliberately conservative:
// "web_search", "web_fetch" and their dated/preview variants.
func isServerToolName(name string) bool {
	return name == "web_search" || name == "web_fetch" ||
		strings.HasPrefix(name, "web_search_") || strings.HasPrefix(name, "web_fetch_")
}

// isServerTool reports whether one tools[] entry of an inbound chat/messages
// request is a server-side search/fetch tool:
//
//   - OpenAI built-in tool types: "web_search", "web_fetch" and any dated or
//     preview variant ("web_search_preview", "web_search_20250305", ...);
//   - OpenAI function tools whose function.name is exactly "web_search" or
//     "web_fetch";
//   - Anthropic server tool blocks, which carry the same dated type strings.
//
// Anything else — including ordinary user functions that merely contain
// "search" in their name — is left untouched.
func isServerTool(item gjson.Result) bool {
	if !item.IsObject() {
		return false
	}
	if t := item.Get("type"); t.Type == gjson.String && isServerToolName(t.String()) {
		return true
	}
	if fn := item.Get("function.name"); fn.Type == gjson.String {
		name := fn.String()
		if name == "web_search" || name == "web_fetch" {
			return true
		}
	}
	return false
}

// toolChoiceTargetsServerTool reports whether tool_choice selects one of the
// server tools being stripped. All shapes seen in the wild are covered:
//
//	"web_search"                                            (OpenAI name string)
//	{"type":"web_search"}                                   (OpenAI built-in)
//	{"type":"function","function":{"name":"web_search"}}    (OpenAI function)
//	{"type":"tool","name":"web_fetch"}                      (Anthropic)
func toolChoiceTargetsServerTool(choice gjson.Result) bool {
	if choice.Type == gjson.String {
		return isServerToolName(choice.String())
	}
	if !choice.IsObject() {
		return false
	}
	if t := choice.Get("type"); t.Type == gjson.String && isServerToolName(t.String()) {
		return true
	}
	if n := choice.Get("name"); n.Type == gjson.String && isServerToolName(n.String()) {
		return true
	}
	if fn := choice.Get("function.name"); fn.Type == gjson.String && isServerToolName(fn.String()) {
		return true
	}
	return false
}

// StripServerToolsFromRawRequest removes server-side search/fetch tool
// entries from the raw inbound request JSON, before any format conversion
// (§3.4). It returns the stripped body and the number of removed items.
//
// A body that is not valid JSON, or carries no tools array, or whose tools
// array contains no server tools is returned byte-identical with removed=0.
// When every entry is stripped the now-empty "tools" key is dropped
// altogether: several upstreams reject an explicit empty tools array, and the
// absence of the key is the neutral form.
//
// Beyond tools[] the strip also drops:
//
//   - a tool_choice that names a stripped server tool (string or object form);
//     upstreams otherwise reject the request with 400;
//   - the chat-only web_search_options field, which would re-enable server
//     search on its own.
//
// removed counts every tools[] entry plus every auxiliary field dropped, so
// removed > 0 always means the returned body differs from the input.
//
// Deletions are done in place with sjson, so every field that survives keeps
// its original byte order, spelling and numeric precision (M12: a map decode/
// encode round trip reordered keys and lost precision on integers above 2^53).
func StripServerToolsFromRawRequest(body []byte) (stripped []byte, removed int, err error) {
	if len(body) == 0 {
		return body, 0, nil
	}
	if !gjson.ValidBytes(body) {
		// Not JSON: relay as-is, nothing to strip.
		return body, 0, nil
	}
	out := body

	if tools := gjson.GetBytes(out, "tools"); tools.IsArray() {
		entries := tools.Array()
		drop := make([]int, 0, len(entries))
		for i, entry := range entries {
			if isServerTool(entry) {
				drop = append(drop, i)
			}
		}
		switch {
		case len(drop) == 0:
			// Nothing to do.
		case len(drop) == len(entries):
			if out, err = sjson.DeleteBytes(out, "tools"); err != nil {
				return body, 0, err
			}
			removed += len(drop)
		default:
			// Delete from the highest index down so the remaining indices
			// stay valid.
			for i := len(drop) - 1; i >= 0; i-- {
				if out, err = sjson.DeleteBytes(out, "tools."+strconv.Itoa(drop[i])); err != nil {
					return body, 0, err
				}
			}
			removed += len(drop)
		}
	}

	if choice := gjson.GetBytes(out, "tool_choice"); choice.Exists() && toolChoiceTargetsServerTool(choice) {
		if out, err = sjson.DeleteBytes(out, "tool_choice"); err != nil {
			return body, 0, err
		}
		removed++
	}
	if gjson.GetBytes(out, "web_search_options").Exists() {
		if out, err = sjson.DeleteBytes(out, "web_search_options"); err != nil {
			return body, 0, err
		}
		removed++
	}
	if removed == 0 {
		return body, 0, nil
	}
	return out, removed, nil
}

// ExtractServerToolCounts reads the server-side Search/Fetch call counts from
// an upstream response's usage JSON (§3.4). Candidate fields, in order:
//
//	usage.server_tool_use.web_search_requests / usage.server_tool_use.web_fetch_requests
//	usage.web_search_requests / usage.web_fetch_requests
//
// Each count resolves independently: the first candidate present wins for
// that count, a missing count is 0. ok is true when at least one of the four
// fields was found; when ok is false the counts must not be estimated — the
// caller refuses to bill (and strips the tools on the next request, §3.4).
//
// The input is normally the full upstream response body; a bare usage object
// is accepted as well.
func ExtractServerToolCounts(usageJSON []byte) (search, fetch int64, ok bool) {
	if len(usageJSON) == 0 {
		return 0, 0, false
	}
	var root map[string]any
	if err := common.Unmarshal(usageJSON, &root); err != nil {
		return 0, 0, false
	}
	usage := root
	if u, ok := root["usage"].(map[string]any); ok {
		usage = u
	}
	serverToolUse, _ := usage["server_tool_use"].(map[string]any)

	search, foundSearch := toolCountField(serverToolUse, usage, "web_search_requests")
	fetch, foundFetch := toolCountField(serverToolUse, usage, "web_fetch_requests")
	return search, fetch, foundSearch || foundFetch
}

// toolCountField resolves one per-call count: the server_tool_use nested
// object first, then the usage object itself. A present but non-numeric or
// negative value counts as not found — a malformed field must not silently
// become a billable zero (§3.4 读不到不能估算收费).
func toolCountField(nested, usage map[string]any, key string) (int64, bool) {
	for _, obj := range []map[string]any{nested, usage} {
		if obj == nil {
			continue
		}
		raw, present := obj[key]
		if !present {
			continue
		}
		f, isNum := raw.(float64)
		if !isNum || f < 0 || f != float64(int64(f)) {
			continue
		}
		return int64(f), true
	}
	return 0, false
}
