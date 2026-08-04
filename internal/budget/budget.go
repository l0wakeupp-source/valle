// Package budget plans provider-facing context usage without mutating canonical state.
package budget

const (
	// DefaultContextWindow is deliberately conservative for models that do not
	// advertise a context limit.
	DefaultContextWindow = 32_000
	DefaultSafetyMargin  = 512
)

// Input contains token estimates for one provider request.
type Input struct {
	ContextWindow        int
	StableSystemTokens   int
	VolatileSystemTokens int
	ToolSchemaTokens     int
	MessageTokens        int
	CurrentRequestTokens int
	ReservedOutputTokens int
	SafetyMarginTokens   int
}

// Result is a deterministic context decision. MessageTokens may be reduced to
// RetainedMessageTokens by a caller that owns logical history records.
type Result struct {
	ContextWindow         int
	TotalInputTokens      int
	RetainedMessageTokens int
	AvailableTokens       int
	Truncated             bool
}

// Plan calculates the request envelope. It treats an unknown context window as
// bounded, and never treats zero as unlimited capacity.
func Plan(input Input) Result {
	contextWindow := input.ContextWindow
	if contextWindow <= 0 {
		contextWindow = DefaultContextWindow
	}
	safetyMargin := input.SafetyMarginTokens
	if safetyMargin <= 0 {
		safetyMargin = DefaultSafetyMargin
	}

	fixedTokens := nonNegative(input.StableSystemTokens) +
		nonNegative(input.VolatileSystemTokens) +
		nonNegative(input.ToolSchemaTokens) +
		nonNegative(input.CurrentRequestTokens)
	messageTokens := nonNegative(input.MessageTokens)
	totalInput := fixedTokens + messageTokens
	reserved := nonNegative(input.ReservedOutputTokens)
	available := contextWindow - totalInput - reserved - safetyMargin
	if available < 0 {
		available = 0
	}

	messageCapacity := contextWindow - fixedTokens - reserved - safetyMargin
	if messageCapacity < 0 {
		messageCapacity = 0
	}
	retained := messageTokens
	if retained > messageCapacity {
		retained = messageCapacity
	}

	return Result{
		ContextWindow:         contextWindow,
		TotalInputTokens:      totalInput,
		RetainedMessageTokens: retained,
		AvailableTokens:       available,
		Truncated:             totalInput+reserved+safetyMargin > contextWindow,
	}
}

func nonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
