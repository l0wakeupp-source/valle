package tools

import (
	"context"
	"encoding/json"
	"testing"
)

type namedTool struct{ name string }

func (t namedTool) Name() string                                                { return t.name }
func (namedTool) Description() string                                           { return "" }
func (namedTool) Schema() map[string]any                                        { return map[string]any{} }
func (namedTool) ReadOnly() bool                                                { return true }
func (namedTool) Run(context.Context, Context, json.RawMessage) (Result, error) { return Result{}, nil }

func TestSwarmRegistryExcludesNestedDelegation(t *testing.T) {
	base := NewRegistry()
	for _, name := range []string{"read", "swarm", "task", "parallel_tasks"} {
		base.Register(namedTool{name: name})
	}
	r := NewSwarmRegistry(base)
	for _, name := range []string{"swarm", "task", "parallel_tasks"} {
		if _, ok := r.Get(name); ok {
			t.Fatalf("worker registry exposes %s", name)
		}
	}
}

func TestSwarmRegistryEnforcesWorkerAllowlistForLookupAndSchemas(t *testing.T) {
	base := NewRegistry()
	for _, name := range []string{"read", "write", "bash"} {
		base.Register(namedTool{name: name})
	}
	registry := NewSwarmRegistry(base, "read")
	registry.Register(namedTool{name: "team"})
	if _, ok := registry.Get("read"); !ok {
		t.Fatal("allowed base tool is missing")
	}
	for _, name := range []string{"write", "bash"} {
		if _, ok := registry.Get(name); ok {
			t.Fatalf("disallowed tool %q is available through Get", name)
		}
	}
	if _, ok := registry.Get("team"); !ok {
		t.Fatal("team coordination tool was removed by base allowlist")
	}
	for _, schema := range registry.Schemas(nil) {
		if schema.Name == "write" || schema.Name == "bash" {
			t.Fatalf("disallowed tool %q is exposed in schemas", schema.Name)
		}
	}
}

func TestFilteredSwarmRegistryEnforcesEffectivePolicyDuringLookup(t *testing.T) {
	base := NewRegistry()
	for _, name := range []string{"read", "write"} {
		base.Register(namedTool{name: name})
	}
	registry := NewFilteredSwarmRegistry(base, func(name string) bool { return name != "write" })
	registry.Register(namedTool{name: "team"})

	if _, ok := registry.Get("write"); ok {
		t.Fatal("effective policy denied write but lookup returned it")
	}
	if _, ok := registry.Get("read"); !ok {
		t.Fatal("effective policy unexpectedly denied read")
	}
	if _, ok := registry.Get("team"); !ok {
		t.Fatal("mandatory team coordination tool was filtered")
	}
	for _, name := range registry.Names() {
		if name == "write" {
			t.Fatal("effective policy denied write but Names exposed it")
		}
	}
	for _, schema := range registry.Schemas(nil) {
		if schema.Name == "write" {
			t.Fatal("effective policy denied write but Schemas exposed it")
		}
	}
}
