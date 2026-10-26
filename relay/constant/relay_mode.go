package constant

import (
	"slices"
	"strings"
)

const (
	RelayModeUnknown = iota
	RelayModeChatCompletions
	RelayModeCompletions
	RelayModeEmbeddings
	RelayModeModerations
	RelayModeImagesGenerations
	RelayModeImagesEdits
	RelayModeEdits

	RelayModeMidjourneyImagine
	RelayModeMidjourneyDescribe
	RelayModeMidjourneyBlend
	RelayModeMidjourneyChange
	RelayModeMidjourneySimpleChange
	RelayModeMidjourneyNotify
	RelayModeMidjourneyTaskFetch
	RelayModeMidjourneyTaskImageSeed
	RelayModeMidjourneyTaskFetchByCondition
	RelayModeMidjourneyAction
	RelayModeMidjourneyModal
	RelayModeMidjourneyShorten
	RelayModeSwapFace
	RelayModeMidjourneyUpload
	RelayModeMidjourneyVideo
	RelayModeMidjourneyEdits

	RelayModeAudioSpeech        // tts
	RelayModeAudioTranscription // whisper
	RelayModeAudioTranslation   // whisper

	RelayModeVideoFetchByID
	RelayModeVideoSubmit

	RelayModeRerank

	RelayModeResponses

	RelayModeRealtime

	RelayModeGemini

	RelayModeResponsesCompact

	RelayModeAlphaSearch

	RelayModeToolsSearch
	RelayModeToolsFetch

	// RelayModeClaude is the native Anthropic Messages endpoint (POST
	// /v1/messages). It is appended at the end of the enum so the numeric
	// values of every existing mode stay unchanged.
	RelayModeClaude
)

func Path2RelayMode(path string) int {
	relayMode := RelayModeUnknown
	if strings.HasPrefix(path, "/v1/chat/completions") || strings.HasPrefix(path, "/pg/chat/completions") {
		relayMode = RelayModeChatCompletions
	} else if strings.HasPrefix(path, "/v1/completions") {
		relayMode = RelayModeCompletions
	} else if strings.HasPrefix(path, "/v1/embeddings") {
		relayMode = RelayModeEmbeddings
	} else if strings.HasSuffix(path, "embeddings") {
		relayMode = RelayModeEmbeddings
	} else if strings.HasPrefix(path, "/v1/moderations") {
		relayMode = RelayModeModerations
	} else if strings.HasPrefix(path, "/v1/images/generations") {
		relayMode = RelayModeImagesGenerations
	} else if strings.HasPrefix(path, "/v1/images/edits") {
		relayMode = RelayModeImagesEdits
	} else if strings.HasPrefix(path, "/v1/edits") {
		relayMode = RelayModeEdits
	} else if strings.HasPrefix(path, "/v1/responses/compact") {
		relayMode = RelayModeResponsesCompact
	} else if strings.HasPrefix(path, "/v1/responses") {
		relayMode = RelayModeResponses
	} else if strings.HasPrefix(path, "/v1/messages") {
		relayMode = RelayModeClaude
	} else if strings.HasPrefix(path, "/v1/alpha/search") {
		relayMode = RelayModeAlphaSearch
	} else if strings.HasPrefix(path, "/v1/tools/search") {
		relayMode = RelayModeToolsSearch
	} else if strings.HasPrefix(path, "/v1/tools/fetch") {
		relayMode = RelayModeToolsFetch
	} else if strings.HasPrefix(path, "/v1/audio/speech") {
		relayMode = RelayModeAudioSpeech
	} else if strings.HasPrefix(path, "/v1/audio/transcriptions") {
		relayMode = RelayModeAudioTranscription
	} else if strings.HasPrefix(path, "/v1/audio/translations") {
		relayMode = RelayModeAudioTranslation
	} else if strings.HasPrefix(path, "/v1/rerank") {
		relayMode = RelayModeRerank
	} else if strings.HasPrefix(path, "/v1/realtime") {
		relayMode = RelayModeRealtime
	} else if strings.HasPrefix(path, "/v1beta/models") || strings.HasPrefix(path, "/v1/models") {
		relayMode = RelayModeGemini
	} else if strings.HasPrefix(path, "/mj") {
		relayMode = Path2RelayModeMidjourney(path)
	}
	return relayMode
}

func Path2RelayModeMidjourney(path string) int {
	relayMode := RelayModeUnknown
	if strings.HasSuffix(path, "/mj/submit/action") {
		// midjourney plus
		relayMode = RelayModeMidjourneyAction
	} else if strings.HasSuffix(path, "/mj/submit/modal") {
		// midjourney plus
		relayMode = RelayModeMidjourneyModal
	} else if strings.HasSuffix(path, "/mj/submit/shorten") {
		// midjourney plus
		relayMode = RelayModeMidjourneyShorten
	} else if strings.HasSuffix(path, "/mj/insight-face/swap") {
		// midjourney plus
		relayMode = RelayModeSwapFace
	} else if strings.HasSuffix(path, "/submit/upload-discord-images") {
		// midjourney plus
		relayMode = RelayModeMidjourneyUpload
	} else if strings.HasSuffix(path, "/mj/submit/imagine") {
		relayMode = RelayModeMidjourneyImagine
	} else if strings.HasSuffix(path, "/mj/submit/video") {
		relayMode = RelayModeMidjourneyVideo
	} else if strings.HasSuffix(path, "/mj/submit/edits") {
		relayMode = RelayModeMidjourneyEdits
	} else if strings.HasSuffix(path, "/mj/submit/blend") {
		relayMode = RelayModeMidjourneyBlend
	} else if strings.HasSuffix(path, "/mj/submit/describe") {
		relayMode = RelayModeMidjourneyDescribe
	} else if strings.HasSuffix(path, "/mj/notify") {
		relayMode = RelayModeMidjourneyNotify
	} else if strings.HasSuffix(path, "/mj/submit/change") {
		relayMode = RelayModeMidjourneyChange
	} else if strings.HasSuffix(path, "/mj/submit/simple-change") {
		relayMode = RelayModeMidjourneyChange
	} else if strings.HasSuffix(path, "/fetch") {
		relayMode = RelayModeMidjourneyTaskFetch
	} else if strings.HasSuffix(path, "/image-seed") {
		relayMode = RelayModeMidjourneyTaskImageSeed
	} else if strings.HasSuffix(path, "/list-by-condition") {
		relayMode = RelayModeMidjourneyTaskFetchByCondition
	}
	return relayMode
}

// EndpointKind names one upstream protocol endpoint the marketplace relays.
// The endpoint base is configured per channel; the path below is fixed, so a
// buyer's original request path never decides where the upstream call goes.
type EndpointKind string

const (
	EndpointOpenAIChat        EndpointKind = "openai_chat"
	EndpointOpenAIResponses   EndpointKind = "openai_responses"
	EndpointAnthropicMessages EndpointKind = "anthropic_messages"
)

// EndpointPath returns the fixed path of an endpoint.
func EndpointPath(kind EndpointKind) string {
	switch kind {
	case EndpointOpenAIResponses:
		return "/v1/responses"
	case EndpointAnthropicMessages:
		return "/v1/messages"
	default:
		return "/v1/chat/completions"
	}
}

// BuildEndpointURL joins an endpoint base with the endpoint's fixed path.
// A base that already ends in a version segment (.../v1, .../v4, .../api/v3)
// takes the path without its own /v1, so the version is never doubled. This
// replaces the old buyer-path plus mp_strip_v1_prefix logic.
func BuildEndpointURL(base string, kind EndpointKind) string {
	base = strings.TrimSuffix(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	path := EndpointPath(kind)
	if strings.HasPrefix(path, "/v1/") && hasVersionSuffix(base) {
		if after, ok := strings.CutPrefix(path, "/v1"); ok {
			path = after
		}
	}
	return base + path
}

// hasVersionSuffix reports whether the last path segment of base is a version
// segment such as v1, v4 or v1beta.
func hasVersionSuffix(base string) bool {
	idx := strings.LastIndexByte(base, '/')
	if idx < 0 {
		return false
	}
	seg := base[idx+1:]
	if len(seg) < 2 || seg[0] != 'v' {
		return false
	}
	for _, c := range seg[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// AnthropicBetaContext1M is the 1M-context beta header value. It is only
// billable when the price table covers a 1M context window, so marketplace
// requests prune it for models without that window.
const AnthropicBetaContext1M = "context-1m-2025-08-07"

// anthropicBetaAllowlist is the set of anthropic-beta values that may reach an
// upstream. Unknown values are dropped instead of being forwarded blindly: a
// beta changes billing and protocol semantics, so it needs an explicit entry
// here and, for context-1m, a price-table check at the request site.
var anthropicBetaAllowlist = []string{
	AnthropicBetaContext1M,
	"extended-cache-ttl-2025-04-11",
	"prompt-caching-2024-07-31",
	"max-tokens-3-5-sonnet-2024-07-15",
	"fine-grained-tool-streaming-2024-05-15",
	"interleaved-thinking-2025-05-14",
	"fast-mode-2026-02-01",
	"thinking-binding-controls-2026-08-01",
}

// FilterAnthropicBetaValues flattens one or more anthropic-beta header values,
// keeps only allowlisted betas, and returns the de-duplicated result. When
// allowContext1M is false the 1M-context beta is dropped even though it is
// allowlisted (the caller's price table does not cover it).
func FilterAnthropicBetaValues(values []string, allowContext1M bool) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		for beta := range strings.SplitSeq(value, ",") {
			beta = strings.TrimSpace(beta)
			if beta == "" {
				continue
			}
			if !slices.Contains(anthropicBetaAllowlist, beta) {
				continue
			}
			if beta == AnthropicBetaContext1M && !allowContext1M {
				continue
			}
			if slices.Contains(kept, beta) {
				continue
			}
			kept = append(kept, beta)
		}
	}
	return kept
}
