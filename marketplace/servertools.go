package marketplace

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// Server-side Search/Fetch tool handling (§3.4). Server tools are billed per
// call from the usage the upstream reports; when the counts cannot be read
// the tools must not reach the upstream at all (读不到次数时不能估算收费), so
// the inbound raw request is stripped before any format conversion.

// isServerTool reports whether one tools[] entry of an inbound chat/messages
// request is a server-side search/fetch tool. The match is deliberately
// conservative:
//
//   - OpenAI built-in tool types: "web_search", "web_fetch" and any dated or
//     preview variant ("web_search_preview", "web_search_20250305", ...);
//   - OpenAI function tools whose function.name is exactly "web_search" or
//     "web_fetch";
//   - Anthropic server tool blocks, which carry the same dated type strings.
//
// Anything else — including ordinary user functions that merely contain
// "search" in their name — is left untouched.
func isServerTool(item map[string]any) bool {
	if t, ok := item["type"].(string); ok {
		if t == "web_search" || t == "web_fetch" ||
			strings.HasPrefix(t, "web_search_") || strings.HasPrefix(t, "web_fetch_") {
			return true
		}
	}
	if fn, ok := item["function"].(map[string]any); ok {
		if name, ok := fn["name"].(string); ok && (name == "web_search" || name == "web_fetch") {
			return true
		}
	}
	return false
}

// StripServerToolsFromRawRequest removes server-side search/fetch tool
// entries from the raw inbound request JSON, before any format conversion
// (§3.4). It returns the stripped body and the number of removed entries.
//
// A body that is not a JSON object, or carries no tools array, or whose tools
// array contains no server tools is returned byte-identical with removed=0.
// When every entry is stripped the now-empty "tools" key is dropped
// altogether: several upstreams reject an explicit empty tools array, and the
// absence of the key is the neutral form. JSON goes through the common
// package wrappers per AGENTS.md.
func StripServerToolsFromRawRequest(body []byte) (stripped []byte, removed int, err error) {
	if len(body) == 0 {
		return body, 0, nil
	}
	var obj map[string]any
	if uerr := common.Unmarshal(body, &obj); uerr != nil {
		// Not JSON (or not an object): relay as-is, nothing to strip.
		return body, 0, nil
	}
	toolsRaw, ok := obj["tools"]
	if !ok {
		return body, 0, nil
	}
	tools, ok := toolsRaw.([]any)
	if !ok {
		return body, 0, nil
	}
	kept := make([]any, 0, len(tools))
	for _, item := range tools {
		if m, ok := item.(map[string]any); ok && isServerTool(m) {
			removed++
			continue
		}
		kept = append(kept, item)
	}
	if removed == 0 {
		return body, 0, nil
	}
	if len(kept) == 0 {
		delete(obj, "tools")
	} else {
		obj["tools"] = kept
	}
	stripped, err = common.Marshal(obj)
	if err != nil {
		return body, 0, err
	}
	return stripped, removed, nil
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
