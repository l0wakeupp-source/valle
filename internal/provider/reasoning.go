package provider

import "strings"

// ReasoningEffort is how hard a model should think before answering.
//
// Two wire formats exist and they do not map onto each other cleanly:
//
//	OpenAI-style     reasoning_effort: "minimal" | "low" | "medium" | "high"
//	Anthropic-style  thinking: {type: "enabled", budget_tokens: N}
//
// A single enum is carried through the agent and translated by each provider,
// so the TUI never has to know which dialect a model speaks.
type ReasoningEffort string

const (
	// ReasoningOff disables reasoning entirely (omit the field).
	ReasoningOff ReasoningEffort = "off"
	// ReasoningMinimal is the smallest budget the model supports.
	ReasoningMinimal ReasoningEffort = "minimal"
	ReasoningLow     ReasoningEffort = "low"
	ReasoningMedium  ReasoningEffort = "medium"
	ReasoningHigh    ReasoningEffort = "high"
)

// ReasoningLevels lists the selectable levels in ascending order.
func ReasoningLevels() []ReasoningEffort {
	return []ReasoningEffort{
		ReasoningOff, ReasoningMinimal, ReasoningLow, ReasoningMedium, ReasoningHigh,
	}
}

// Valid reports whether e is a known level.
func (e ReasoningEffort) Valid() bool {
	switch e {
	case ReasoningOff, ReasoningMinimal, ReasoningLow, ReasoningMedium, ReasoningHigh:
		return true
	}
	return false
}

// ParseEffort reads a level from user input, tolerating shorthand.
func ParseEffort(s string) (ReasoningEffort, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "none", "no", "disabled", "false":
		return ReasoningOff, true
	case "minimal", "min", "xs":
		return ReasoningMinimal, true
	case "low", "l", "s":
		return ReasoningLow, true
	case "medium", "med", "m", "default":
		return ReasoningMedium, true
	case "high", "h", "max", "xl":
		return ReasoningHigh, true
	}
	return "", false
}

// Budget converts a level into an Anthropic thinking budget, scaled to the
// response limit. Anthropic requires budget_tokens < max_tokens, and a budget
// below 1024 is rejected outright.
func (e ReasoningEffort) Budget(maxTokens int) int {
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	var frac float64
	switch e {
	case ReasoningMinimal:
		frac = 0.15
	case ReasoningLow:
		frac = 0.25
	case ReasoningMedium:
		frac = 0.5
	case ReasoningHigh:
		frac = 0.8
	default:
		return 0
	}
	budget := int(float64(maxTokens) * frac)
	if budget < 1024 {
		budget = 1024
	}
	// Leave room for the visible answer.
	if budget > maxTokens-512 {
		budget = maxTokens - 512
	}
	if budget < 1024 {
		return 0 // the response limit is too small for thinking at all
	}
	return budget
}

// ReasoningStyle is the dialect a model expects.
type ReasoningStyle string

const (
	ReasoningStyleNone      ReasoningStyle = ""          // not a reasoning model
	ReasoningStyleOpenAI    ReasoningStyle = "effort"    // reasoning_effort
	ReasoningStyleAnthropic ReasoningStyle = "budget"    // thinking.budget_tokens
	ReasoningStyleQwen      ReasoningStyle = "enable"    // enable_thinking bool
	ReasoningStyleAlways    ReasoningStyle = "always_on" // reasons unconditionally
)

// DetectReasoning infers whether a model reasons and in which dialect, from
// its id. Endpoints rarely advertise this, so the id is the only signal
// available without a probe request.
func DetectReasoning(modelID string) (ReasoningStyle, ReasoningEffort) {
	id := strings.ToLower(modelID)
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}

	switch {
	// Anthropic extended thinking.
	case strings.Contains(id, "claude"):
		// Sonnet 3.7 and every 4.x model support thinking; older ones do not.
		if strings.Contains(id, "-4") || strings.Contains(id, "3-7") ||
			strings.Contains(id, "3.7") || strings.Contains(id, "opus-4") {
			return ReasoningStyleAnthropic, ReasoningMedium
		}
		return ReasoningStyleNone, ReasoningOff

	// OpenAI reasoning families.
	case strings.HasPrefix(id, "o1"), strings.HasPrefix(id, "o3"), strings.HasPrefix(id, "o4"):
		return ReasoningStyleOpenAI, ReasoningMedium
	case strings.HasPrefix(id, "gpt-5"), strings.Contains(id, "gpt-5"):
		return ReasoningStyleOpenAI, ReasoningMedium
	case strings.Contains(id, "codex"):
		return ReasoningStyleOpenAI, ReasoningMedium

	// DeepSeek reasoners think unconditionally.
	case strings.Contains(id, "deepseek-r"), strings.Contains(id, "deepseek-reason"):
		return ReasoningStyleAlways, ReasoningMedium

	// Qwen uses a boolean switch.
	case strings.Contains(id, "qwq"), strings.Contains(id, "qwen3"):
		return ReasoningStyleQwen, ReasoningMedium

	// Others that expose the OpenAI field.
	case strings.Contains(id, "grok-3"), strings.Contains(id, "grok-4"):
		return ReasoningStyleOpenAI, ReasoningMedium
	case strings.Contains(id, "glm-4.6"), strings.Contains(id, "glm-5"):
		return ReasoningStyleOpenAI, ReasoningMedium
	case strings.Contains(id, "minimax-m"):
		return ReasoningStyleOpenAI, ReasoningMedium
	case strings.Contains(id, "thinking"), strings.Contains(id, "reasoner"):
		return ReasoningStyleOpenAI, ReasoningMedium
	}
	return ReasoningStyleNone, ReasoningOff
}
