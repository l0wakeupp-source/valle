// Package history selects a provider-facing conversation view without mutating
// the canonical transcript. Tool calls and their results are atomic groups.
package history

import (
	"encoding/json"

	"rick/internal/provider"
	"rick/internal/tokens"
)

type group struct {
	messages []provider.Message
	cost     int
}

// Retain returns an ordered, token-bounded provider view and the number of
// omitted logical groups. It never returns an orphaned tool result.
//
// Trimming is prefix-preserving: whole logical groups are dropped only from
// the oldest end, so the surviving messages keep their exact bytes from the
// previous turn and the provider prompt-cache prefix stays warm.
func Retain(messages []provider.Message, maxTokens int, encoding tokens.Encoding) ([]provider.Message, int) {
	copied := append([]provider.Message(nil), messages...)
	if len(copied) == 0 || maxTokens <= 0 {
		return copied, 0
	}

	groups := logicalGroups(copied, encoding)
	total := 0
	for _, item := range groups {
		total += item.cost
	}
	if total <= maxTokens {
		return copied, 0
	}

	// The newest logical group always survives: it is the current user
	// request or the latest tool result needed to continue the turn.
	keep := 1
	used := groups[len(groups)-1].cost
	for keep < len(groups) {
		next := groups[len(groups)-1-keep].cost
		if used+next > maxTokens {
			break
		}
		used += next
		keep++
	}

	start := len(groups) - keep
	retained := make([]provider.Message, 0, len(copied))
	for _, item := range groups[start:] {
		retained = append(retained, item.messages...)
	}
	return retained, start
}

func logicalGroups(messages []provider.Message, encoding tokens.Encoding) []group {
	groups := make([]group, 0, len(messages))
	for index := 0; index < len(messages); index++ {
		item := group{messages: []provider.Message{messages[index]}}
		if hasBlock(messages[index], "tool_use") && index+1 < len(messages) && hasBlock(messages[index+1], "tool_result") {
			item.messages = append(item.messages, messages[index+1])
			index++
		}
		item.cost = messageTokens(item.messages, encoding)
		groups = append(groups, item)
	}
	return groups
}

func messageTokens(messages []provider.Message, encoding tokens.Encoding) int {
	total := 0
	for _, message := range messages {
		encoded, err := json.Marshal(message)
		if err != nil {
			total += tokens.Count(message.Text(), encoding).Count
			continue
		}
		total += tokens.Count(string(encoded), encoding).Count + 4
	}
	return total
}

func hasBlock(message provider.Message, kind string) bool {
	for _, block := range message.Content {
		if block.Type == kind {
			return true
		}
	}
	return false
}
