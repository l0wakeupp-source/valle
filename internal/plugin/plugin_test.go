package plugin

import "testing"

func TestActivePluginSnapshotUpdatesOnRegistryChanges(t *testing.T) {
	registry := NewRegistry()
	registry.Register(Hooks{Name: "test"})

	first := registry.activePlugins()
	second := registry.activePlugins()
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("active plugin snapshots = %d and %d, want 1", len(first), len(second))
	}
	if &first[0] != &second[0] {
		t.Fatal("activePlugins rebuilt the snapshot during dispatch reads")
	}

	registry.SetEnabled("test", false)
	if active := registry.activePlugins(); len(active) != 0 {
		t.Fatalf("disabled plugin remained active: %d", len(active))
	}

	registry.SetEnabled("test", true)
	if active := registry.activePlugins(); len(active) != 1 {
		t.Fatalf("re-enabled plugin count = %d, want 1", len(active))
	}
}
