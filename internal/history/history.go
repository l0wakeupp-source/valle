// Package history selects a provider-facing conversation view without mutating
// the canonical transcript. Tool calls and their results are atomic groups.
package history

import (
	"encoding/json"
	"regexp"
	"sort"

	"rick/internal/provider"
	"rick/internal/tokens"
)

var criticalPattern = regexp.MustCompile(`(?i)(error|fail|panic|fatal|permission|denied|diff --git|^@@|\.[a-z0-9]+:\d+(?::\d+)?)`)

type group struct {
	messages []provider.Message
	cost     int
	score    int
	index    int
}

// Retain returns an ordered, token-bounded provider view and the number of
// omitted logical groups. It never returns an orphaned tool result.
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

	// Always retain the newest logical group: it is the current user request
	// or the latest tool result needed to continue the turn.
	selected := make(map[int]bool)
	used := 0
	newest := len(groups) - 1
	selected[newest] = true
	used += groups[newest].cost

	candidates := append([]group(nil), groups[:newest]...)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].index > candidates[j].index
	})
	for _, item := range candidates {
		if selected[item.index] || used+item.cost > maxTokens {
			continue
		}
		selected[item.index] = true
		used += item.cost
	}

	retained := make([]provider.Message, 0, len(copied))
	omitted := 0
	for index, item := range groups {
		if selected[index] {
			retained = append(retained, item.messages...)
		} else {
			omitted++
		}
	}
	if len(retained) == 0 {
		return groups[newest].messages, len(groups) - 1
	}
	return retained, omitted
}

func logicalGroups(messages []provider.Message, encoding tokens.Encoding) []group {
	groups := make([]group, 0, len(messages))
	for index := 0; index < len(messages); index++ {
		item := group{messages: []provider.Message{messages[index]}, index: len(groups)}
		if hasBlock(messages[index], "tool_use") && index+1 < len(messages) && hasBlock(messages[index+1], "tool_result") {
			item.messages = append(item.messages, messages[index+1])
			index++
		}
		item.cost = messageTokens(item.messages, encoding)
		item.score = groupScore(item.messages, item.index, len(messages))
		groups = append(groups, item)
	}
	return groups
}

func groupScore(messages []provider.Message, index, total int) int {
	score := 100
	distance := total - index
	if distance <= 4 {
		score += (5 - distance) * 100
	}
	for _, message := range messages {
		if message.Role == provider.RoleSystem {
			score += 600
		}
		if hasBlock(message, "tool_use") || hasBlock(message, "tool_result") {
			score += 40
		}
		if criticalPattern.MatchString(message.Text()) {
			score += 500
		}
	}
	return score
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
