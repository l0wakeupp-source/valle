package catalog

import "testing"

func TestRegistryContainsNoDuplicateIDs(t *testing.T) {
	seen := make(map[string]struct{}, len(Registry))
	for _, entry := range Registry {
		if _, exists := seen[entry.ID]; exists {
			t.Fatalf("duplicate provider id %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
	}
}

func TestAppendUniquePreservesCuratedEntry(t *testing.T) {
	curated := []Entry{{ID: "same", Name: "curated"}}
	got := appendUnique(curated, []Entry{{ID: "same", Name: "generated"}, {ID: "new"}})
	if len(got) != 2 || got[0].Name != "curated" {
		t.Fatalf("deduplicated registry = %+v", got)
	}
}
