package provider

import "strings"

// FilterChatModels removes models that cannot serve Rick's text conversation
// protocol. Explicit provider capability metadata wins; ids are only used as a
// fallback for older or minimal /models responses.
func FilterChatModels(models []ModelInfo) []ModelInfo {
	out := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		if strings.TrimSpace(model.ID) == "" || IsChatModel(model) {
			if strings.TrimSpace(model.ID) != "" {
				out = append(out, model)
			}
		}
	}
	return out
}

// IsChatModel reports whether a model belongs in /models and model selection.
func IsChatModel(model ModelInfo) bool {
	if model.CapabilitiesKnown {
		return model.ChatCapable
	}
	return !looksLikeNonChatModel(model.ID)
}

func looksLikeNonChatModel(id string) bool {
	id = strings.ToLower(id)
	if slash := strings.LastIndex(id, "/"); slash >= 0 {
		id = id[slash+1:]
	}

	// These families expose a different API contract (generation, speech,
	// embeddings, reranking, moderation, or video) and cannot answer Rick's
	// text/tool conversation requests.
	for _, marker := range []string{
		"embedding", "embed-", "-embed", "rerank", "moderation", "moderator",
		"text-to-speech", "text_to_speech", "tts", "transcription", "transcribe",
		"whisper", "speech-", "-speech", "audio", "image-generation",
		"image_generation", "image-edit", "image_edit", "gpt-image", "dall-e",
		"stable-diffusion", "flux", "imagen", "musicgen", "video-generation",
		"video_generation", "-video", "veo", "sora",
	} {
		if strings.Contains(id, marker) {
			return true
		}
	}
	return false
}
