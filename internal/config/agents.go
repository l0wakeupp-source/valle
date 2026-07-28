package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LoadMarkdownAgents reads .rick/agents/*.md files with YAML-ish frontmatter:
//
//	---
//	description: Reviews code for bugs
//	mode: subagent
//	model: anthropic/claude-haiku-4-5
//	temperature: 0.2
//	tools:
//	  write: false
//	  edit: false
//	permission:
//	  bash: ask
//	---
//	You are a code reviewer. …
//
// The body becomes the agent's system prompt.
func LoadMarkdownAgents(dirs ...string) map[string]Agent {
	out := map[string]Agent{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			out[name] = ParseAgentMarkdown(string(data))
		}
	}
	return out
}

// ParseAgentMarkdown splits frontmatter from the prompt body.
func ParseAgentMarkdown(src string) Agent {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	a := Agent{}

	if !strings.HasPrefix(src, "---") {
		a.Prompt = strings.TrimSpace(src)
		return a
	}
	rest := src[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		a.Prompt = strings.TrimSpace(src)
		return a
	}
	front := rest[:end]
	a.Prompt = strings.TrimLeft(rest[end+4:], "\n")
	a.Prompt = strings.TrimSpace(a.Prompt)

	// Minimal YAML subset: key: value, plus one level of nesting.
	var section string
	for _, line := range strings.Split(front, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indented := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
		trimmed := strings.TrimSpace(line)
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)

		if !indented {
			section = ""
			if val == "" {
				section = key
				continue
			}
			switch key {
			case "description":
				a.Description = val
			case "mode":
				a.Mode = val
			case "model":
				a.Model = val
			case "prompt":
				a.Prompt = val
			case "temperature":
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					a.Temperature = &f
				}
			case "tools":
				// inline JSON form: tools: {"write": false}
				var m map[string]bool
				if json.Unmarshal([]byte(val), &m) == nil {
					a.Tools = m
				}
			}
			continue
		}

		switch section {
		case "tools":
			if a.Tools == nil {
				a.Tools = map[string]bool{}
			}
			a.Tools[key] = val == "true" || val == "yes" || val == "on"
		case "permission":
			if a.Permission == nil {
				a.Permission = &Permission{}
			}
			switch key {
			case "edit":
				a.Permission.Edit = val
			case "write":
				a.Permission.Write = val
			case "read":
				a.Permission.Read = val
			case "webfetch":
				a.Permission.WebF = val
			case "default":
				a.Permission.Default = val
			case "bash":
				if a.Permission.Bash == nil {
					a.Permission.Bash = map[string]string{}
				}
				a.Permission.Bash["*"] = val
			}
		}
	}
	return a
}

// LoadMarkdownCommands reads .rick/commands/*.md into Command entries.
func LoadMarkdownCommands(dirs ...string) map[string]Command {
	out := map[string]Command{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			a := ParseAgentMarkdown(string(data))
			out[name] = Command{
				Description: a.Description,
				Template:    a.Prompt,
				Agent:       a.Mode,
				Model:       a.Model,
			}
		}
	}
	return out
}
