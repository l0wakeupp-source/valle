package session

import (
	"math"
	"testing"
)

func TestOptimizationUsageTracksMeasuredSavings(t *testing.T) {
	var usage OptimizationUsage
	usage.Add(100, 70, 30)
	usage.Add(50, 50, 0)
	if usage.ToolResults != 2 || usage.OriginalTokens != 150 || usage.ProviderTokens != 120 || usage.SavedTokens != 30 {
		t.Fatalf("unexpected optimization totals: %#v", usage)
	}
	if math.Abs(usage.SavingsPercent()-20) > 0.0001 {
		t.Fatalf("savings percent = %v, want 20", usage.SavingsPercent())
	}
}

func TestOptimizationUsageZeroDenominator(t *testing.T) {
	if got := (OptimizationUsage{}).SavingsPercent(); got != 0 {
		t.Fatalf("zero-denominator savings percent = %v, want 0", got)
	}
}
