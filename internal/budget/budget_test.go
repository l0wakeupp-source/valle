package budget

import "testing"

func TestPlanReservesOutputAndSafetyMargin(t *testing.T) {
	plan := Plan(Input{
		ContextWindow:        1000,
		StableSystemTokens:   100,
		VolatileSystemTokens: 50,
		ToolSchemaTokens:     150,
		MessageTokens:        550,
		CurrentRequestTokens: 50,
		ReservedOutputTokens: 100,
		SafetyMarginTokens:   50,
	})
	if plan.TotalInputTokens != 900 {
		t.Fatalf("TotalInputTokens = %d, want 900", plan.TotalInputTokens)
	}
	if plan.AvailableTokens != 0 {
		t.Fatalf("AvailableTokens = %d, want 0", plan.AvailableTokens)
	}
	if !plan.Truncated {
		t.Fatal("Plan() did not report an over-budget request")
	}
}

func TestPlanUsesConservativeFallbackForUnknownWindow(t *testing.T) {
	plan := Plan(Input{MessageTokens: 100})
	if plan.ContextWindow != DefaultContextWindow {
		t.Fatalf("ContextWindow = %d, want %d", plan.ContextWindow, DefaultContextWindow)
	}
	if plan.AvailableTokens <= 0 {
		t.Fatalf("AvailableTokens = %d, want positive capacity", plan.AvailableTokens)
	}
}
