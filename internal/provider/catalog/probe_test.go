package catalog

import "testing"

func TestParseModelsReadsImageCapability(t *testing.T) {
	body := []byte(`{"object":"list","data":[
		{"id":"text-model","architecture":{"input_modalities":["text"]}},
		{"id":"vision-model:free","architecture":{"input_modalities":["text","image"]}}
	]}`)
	models, _, err := ParseModels(body)
	if err != nil {
		t.Fatal(err)
	}
	if !models[0].ModalitiesKnown || models[0].SupportsImages {
		t.Fatalf("text model capabilities: %+v", models[0])
	}
	if !models[1].ModalitiesKnown || !models[1].SupportsImages {
		t.Fatalf("vision model capabilities: %+v", models[1])
	}
}
