// Package modelsdev provides context length lookup for models using an embedded
// database. The database is regenerated periodically from public sources
// (models.dev, OpenRouter, provider catalogs) using cmd/modelsdev-gen.
package modelsdev

import (
	"encoding/json"
	"strings"
	"sync"
)

var (
	dbMu sync.RWMutex
	db   = build()
)

func build() map[string]int {
	m := map[string]int{}
	// Anthropic
	for _, id := range []string{
		"claude-sonnet-4-5-20250929", "claude-opus-4-5-20251101", "claude-haiku-4-5-20251001",
		"claude-3-5-haiku-20241022", "claude-3-5-sonnet-20241022", "claude-3-opus-20240229",
		"claude-3-sonnet-20240229", "claude-3-haiku-20240307",
	} {
		m[id] = 200000
	}
	// OpenAI
	m["gpt-5"] = 400000
	m["gpt-5-mini"] = 400000
	m["gpt-5-nano"] = 400000
	m["gpt-4.1"] = 1047576
	m["gpt-4.1-mini"] = 1047576
	m["gpt-4.1-nano"] = 1047576
	m["gpt-4o"] = 128000
	m["gpt-4o-mini"] = 128000
	m["gpt-4-turbo"] = 128000
	m["gpt-4"] = 8192
	m["gpt-3.5-turbo"] = 16384
	m["o1"] = 200000
	m["o1-mini"] = 128000
	m["o1-preview"] = 128000
	m["o3"] = 200000
	m["o3-mini"] = 200000
	m["o4-mini"] = 200000
	m["codex-mini-latest"] = 200000
	// Google
	m["gemini-2.5-pro"] = 1048576
	m["gemini-2.5-flash"] = 1048576
	m["gemini-2.5-flash-lite"] = 1048576
	m["gemini-2.0-flash"] = 1048576
	m["gemini-2.0-flash-lite"] = 1048576
	m["gemini-1.5-pro"] = 2097152
	m["gemini-1.5-flash"] = 1048576
	m["gemini-1.5-flash-8b"] = 1048576
	m["gemini-1.0-pro"] = 32768
	m["gemini-exp-1206"] = 1048576
	m["gemini-3.0-pro"] = 1048576
	// DeepSeek
	m["deepseek-chat"] = 128000
	m["deepseek-coder"] = 128000
	m["deepseek-v3"] = 128000
	m["deepseek-r1"] = 128000
	m["deepseek-v2"] = 128000
	// Qwen
	m["qwen3-coder"] = 256000
	m["qwen3-235b-a22b"] = 131072
	m["qwen3-30b-a3b"] = 131072
	m["qwen2.5-coder-32b-instruct"] = 131072
	m["qwen2.5-72b-instruct"] = 131072
	m["qwen2.5-32b-instruct"] = 131072
	m["qwen2.5-14b-instruct"] = 131072
	m["qwen2.5-7b-instruct"] = 131072
	m["qwen2.5-3b-instruct"] = 131072
	m["qwen2.5-1.5b-instruct"] = 131072
	m["qwen2.5-0.5b-instruct"] = 131072
	m["qwen-max"] = 32768
	m["qwen-plus"] = 32768
	m["qwen-turbo"] = 32768
	m["qwen-vl-max"] = 32768
	m["qwen2.5-vl-72b-instruct"] = 131072
	// Kimi / Moonshot
	m["kimi-k2.5"] = 256000
	m["kimi-k2"] = 131072
	m["kimi"] = 131072
	m["moonshot-v1-8k"] = 8192
	m["moonshot-v1-32k"] = 32768
	m["moonshot-v1-128k"] = 131072
	// GLM / Zhipu
	m["glm-4.6"] = 200000
	m["glm-5"] = 200000
	m["glm-4-plus"] = 128000
	m["glm-4"] = 128000
	m["glm-4-flash"] = 128000
	m["glm-4-air"] = 128000
	m["glm-4-airx"] = 128000
	m["glm-4v"] = 128000
	m["glm-4v-plus"] = 128000
	// LongCat
	m["longcat-2.0"] = 1000000
	m["longcat"] = 1000000
	// xAI Grok
	m["grok-4"] = 256000
	m["grok-3"] = 131072
	m["grok-3-mini"] = 131072
	m["grok-2"] = 131072
	m["grok-2-vision"] = 131072
	m["grok-1.5"] = 131072
	m["grok-beta"] = 131072
	// Mistral
	m["mistral-large-latest"] = 131072
	m["mistral-large-2411"] = 131072
	m["mistral-medium-latest"] = 32768
	m["mistral-small-latest"] = 32768
	m["mistral-7b-instruct"] = 32768
	m["mixtral-8x7b-instruct"] = 32768
	m["mixtral-8x22b-instruct"] = 65536
	m["codestral-latest"] = 32768
	m["ministral-8b-instruct"] = 131072
	m["ministral-3b-instruct"] = 131072
	m["pixtral-large-latest"] = 131072
	m["pixtral-12b"] = 131072
	m["devstral-small"] = 131072
	m["devstral-medium"] = 131072
	m["mistral-nemo"] = 131072
	// Meta Llama
	m["llama-4-maverick"] = 1048576
	m["llama-4-scout"] = 1048576
	m["llama-3.3-70b-instruct"] = 131072
	m["llama-3.1-70b-instruct"] = 131072
	m["llama-3.1-8b-instruct"] = 131072
	m["llama-3.1-405b-instruct"] = 131072
	m["llama-3-70b-instruct"] = 8192
	m["llama-3-8b-instruct"] = 8192
	m["llama-2-70b-chat"] = 4096
	m["llama-2-13b-chat"] = 4096
	m["llama-2-7b-chat"] = 4096
	m["llama-3.2-1b-instruct"] = 131072
	m["llama-3.2-3b-instruct"] = 131072
	m["llama-3.2-11b-vision"] = 131072
	m["llama-3.2-90b-vision"] = 131072
	// NVIDIA Nemotron
	m["nemotron-70b-instruct"] = 32000
	m["nemotron-8b-instruct"] = 16000
	m["nemotron-4-340b-instruct"] = 131072
	m["nemotron-mini-4b-instruct"] = 131072
	m["llama-3.1-nemotron-70b"] = 131072
	m["llama-3.1-nemotron-nano-8b"] = 131072
	// Cohere
	m["command-r-plus"] = 131072
	m["command-r"] = 131072
	m["command-r7b"] = 131072
	m["command"] = 4096
	m["command-light"] = 4096
	m["aya-expanse-32b"] = 131072
	m["aya-expanse-8b"] = 131072
	// StepFun
	m["step-2-16k"] = 16384
	m["step-2"] = 128000
	m["step-1-256k"] = 262144
	m["step-1v-8k"] = 8192
	m["step-1v-32k"] = 32768
	m["step-2-mini"] = 32768
	// MiniMax
	m["minimax-m2.5"] = 1000000
	m["minimax-m2"] = 1000000
	m["minimax-m1"] = 1000000
	m["minimax-text-01"] = 1000000
	m["abab7-chat"] = 32768
	m["abab6.5s-chat"] = 32768
	m["abab6.5-chat"] = 200000
	m["abab5.5s-chat"] = 32768
	m["abab5.5-chat"] = 32768
	// Other
	m["phi-4"] = 131072
	m["phi-3.5-mini-instruct"] = 131072
	m["phi-3.5-moe-instruct"] = 131072
	m["phi-3-medium-128k-instruct"] = 131072
	m["phi-3-mini-128k-instruct"] = 131072
	m["phi-3-mini-4k-instruct"] = 4096
	m["phi-3-small-8k-instruct"] = 8192
	m["phi-3-small-128k-instruct"] = 131072
	m["reka-core"] = 131072
	m["reka-flash"] = 131072
	m["reka-edge"] = 131072
	m["ernie-4.5-turbo"] = 131072
	m["ernie-4.0"] = 131072
	m["hunyuan-t1"] = 131072
	m["hunyuan-turbos"] = 131072
	m["hunyuan-standard"] = 131072
	m["dolphin-2.9"] = 131072
	m["dolphin-mixtral"] = 32768
	m["dolphin-mistral"] = 32768
	m["mythomax-l2-13b"] = 32768
	m["mythomist-7b"] = 32768
	m["openchat-3.6"] = 131072
	m["openchat-3.5"] = 32768
	m["snowflake-arctic-instruct"] = 131072
	m["solar-pro"] = 131072
	m["solar-mini"] = 131072
	m["c4ai-aya-23-35b"] = 131072
	m["c4ai-aya-23-8b"] = 131072
	m["c4ai-aya-expanse-32b"] = 131072
	m["c4ai-aya-expanse-8b"] = 131072
	m["gemma-2-27b-it"] = 131072
	m["gemma-2-9b-it"] = 131072
	m["gemma-2-2b-it"] = 131072
	m["gemma-7b-it"] = 8192
	m["gemma-2b-it"] = 8192
	m["llama-3.1-70b-versatile"] = 131072
	m["llama-3.1-8b-instant"] = 131072
	m["llama-3.2-1b-preview"] = 131072
	m["llama-3.2-3b-preview"] = 131072
	m["llama-3.3-70b-versatile"] = 131072
	return m
}

// Lookup returns the context length for a model id (lowercase, trimmed).
// Returns 0, false if the model is not in the database.
func Lookup(modelID string) (int, bool) {
	key := strings.ToLower(strings.TrimSpace(modelID))
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}

	dbMu.RLock()
	ctx, ok := db[key]
	dbMu.RUnlock()
	if ok {
		return ctx, true
	}

	// Try without version suffixes like "-20250929"
	if idx := strings.LastIndex(key, "-"); idx > 0 {
		prefix := key[:idx]
		dbMu.RLock()
		ctx, ok = db[prefix]
		dbMu.RUnlock()
		if ok {
			return ctx, true
		}
	}

	return 0, false
}

// Load replaces the database with data from a JSON blob.
func Load(data []byte) error {
	var newDB map[string]int
	if err := json.Unmarshal(data, &newDB); err != nil {
		return err
	}
	dbMu.Lock()
	db = newDB
	dbMu.Unlock()
	return nil
}

// Export returns the current database as JSON.
func Export() []byte {
	dbMu.RLock()
	defer dbMu.RUnlock()
	b, _ := json.MarshalIndent(db, "", "  ")
	return b
}

// Len returns the number of entries in the database.
func Len() int {
	dbMu.RLock()
	defer dbMu.RUnlock()
	return len(db)
}
