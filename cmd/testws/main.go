package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"rick/internal/tools"
)

func main() {
	t := tools.WebSearchTool{}
	in, _ := json.Marshal(map[string]any{"query": "xi jinping biography early career", "max_results": 3})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := t.Run(ctx, tools.Context{}, in)
	if err != nil {
		fmt.Println("ERR:", err)
		return
	}
	fmt.Println(res.Output)
	fmt.Println("---")
	fmt.Println(filterNarration(res.Output))
}

func filterNarration(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	narration := []string{
		"let me", "i'll", "i will", "i have sufficient", "i am researching",
		"let me use", "let me get", "let me compile", "let me dig", "let me pull",
		"i have enough", "i have the information", "based on my research",
		"here's what i found", "i found that",
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			result = append(result, line)
			continue
		}
		lower := strings.ToLower(trimmed)
		isNarration := false
		for _, n := range narration {
			if strings.HasPrefix(lower, n) {
				isNarration = true
				break
			}
		}
		if !isNarration {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}
