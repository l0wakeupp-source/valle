package openai

import (
	"testing"

	"rick/internal/provider"
)

func TestVisionImagesRequestLowDetail(t *testing.T) {
	messages := []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{
		provider.TextBlock("inspect"),
		provider.ImageBlock("image/png", "aGVsbG8="),
	}}}

	wire := toWire("", messages)
	content, ok := wire[0].Content.([]map[string]interface{})
	if !ok || len(content) != 2 {
		t.Fatalf("content = %#v", wire[0].Content)
	}
	imageURL, ok := content[1]["image_url"].(wireImageURL)
	if !ok {
		t.Fatalf("image_url = %#v", content[1]["image_url"])
	}
	if imageURL.Detail != "low" {
		t.Fatalf("detail = %q", imageURL.Detail)
	}
}

func TestVisionImageOnlyMessageIsNotDropped(t *testing.T) {
	messages := []provider.Message{{Role: provider.RoleUser, Content: []provider.ContentBlock{
		provider.ImageBlock("image/png", "aGVsbG8="),
	}}}

	wire := toWire("", messages)
	if len(wire) != 1 {
		t.Fatalf("message count = %d, want 1", len(wire))
	}
	content, ok := wire[0].Content.([]map[string]interface{})
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v", wire[0].Content)
	}
	if content[0]["type"] != "image_url" {
		t.Fatalf("image type = %#v", content[0]["type"])
	}
}
