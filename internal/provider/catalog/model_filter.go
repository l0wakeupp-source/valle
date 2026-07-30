package catalog

import "rick/internal/provider"

// FilterChatModels applies the same model policy used by the TUI to live probe
// results before they are cached.
func FilterChatModels(models []Model) []Model {
	out := make([]Model, 0, len(models))
	for _, model := range models {
		if IsChatModel(model) {
			out = append(out, model)
		}
	}
	return out
}

func IsChatModel(model Model) bool {
	return provider.IsChatModel(provider.ModelInfo{
		ID:                model.ID,
		CapabilitiesKnown: model.CapabilitiesKnown,
		ChatCapable:       model.ChatCapable,
	})
}
