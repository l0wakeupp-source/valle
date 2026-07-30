package provider

import "testing"

func TestFilterChatModelsUsesExplicitCapabilities(t *testing.T) {
	models := []ModelInfo{
		{ID: "image-model", CapabilitiesKnown: true, ChatCapable: false},
		{ID: "vision-chat", CapabilitiesKnown: true, ChatCapable: true, SupportsImages: true},
		{ID: "text-embedding-3-large"},
		{ID: "tts-1"},
		{ID: "gpt-4.1"},
	}
	got := FilterChatModels(models)
	if len(got) != 2 || got[0].ID != "vision-chat" || got[1].ID != "gpt-4.1" {
		t.Fatalf("filtered models = %#v", got)
	}
}

func TestFilterChatModelsKeepsUnknownVisionModel(t *testing.T) {
	got := FilterChatModels([]ModelInfo{{ID: "qwen-vl-max", SupportsImages: true}})
	if len(got) != 1 || got[0].ID != "qwen-vl-max" {
		t.Fatalf("vision model was filtered: %#v", got)
	}
}
