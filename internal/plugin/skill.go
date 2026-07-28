package plugin

import (
    "os"
    "path/filepath"
    "strings"
)

// Skill is a markdown-defined capability injected into the system prompt
// when its trigger keywords match the user's message.
type Skill struct {
    Name        string
    Description string
    Trigger     []string
    Body        string
    Source      string
}

// LoadSkills scans one or more directories for .md skill files and returns
// every skill that parses successfully. Directories that do not exist are
// skipped silently.
func LoadSkills(dirs ...string) []Skill {
    var out []Skill
    seen := map[string]bool{}
    for _, dir := range dirs {
        entries, err := os.ReadDir(dir)
        if err != nil {
            continue
        }
        for _, e := range entries {
            if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
                continue
            }
            path := filepath.Join(dir, e.Name())
            data, err := os.ReadFile(path)
            if err != nil {
                continue
            }
            s, ok := parseSkill(string(data))
            if !ok {
                continue
            }
            s.Source = path
            if s.Name == "" {
                s.Name = strings.TrimSuffix(e.Name(), ".md")
            }
            if seen[s.Name] {
                continue
            }
            seen[s.Name] = true
            out = append(out, s)
        }
    }
    return out
}

// parseSkill extracts YAML-like frontmatter and the markdown body from a
// skill file. The frontmatter format is intentionally simple (key: value
// lines between --- fences) to avoid a YAML dependency.
func parseSkill(raw string) (Skill, bool) {
    raw = strings.ReplaceAll(raw, "\r\n", "\n")
    if !strings.HasPrefix(raw, "---") {
        return Skill{}, false
    }
    rest := raw[3:]
    endIdx := strings.Index(rest, "\n---")
    if endIdx < 0 {
        return Skill{}, false
    }
    front := rest[:endIdx]
    body := strings.TrimLeft(rest[endIdx+4:], "\n")

    var s Skill
    s.Body = strings.TrimSpace(body)

    for _, line := range strings.Split(front, "\n") {
        line = strings.TrimSpace(line)
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }
        colon := strings.Index(line, ":")
        if colon < 0 {
            continue
        }
        key := strings.TrimSpace(line[:colon])
        val := strings.TrimSpace(line[colon+1:])
        // Strip surrounding quotes.
        val = strings.Trim(val, `"'`)

        switch key {
        case "name":
            s.Name = val
        case "description":
            s.Description = val
        case "trigger", "triggers":
            // Comma-separated or YAML-list items.
            val = strings.TrimPrefix(val, "[")
            val = strings.TrimSuffix(val, "]")
            for _, part := range strings.Split(val, ",") {
                part = strings.TrimSpace(part)
                part = strings.Trim(part, `"'`)
                if part != "" {
                    s.Trigger = append(s.Trigger, strings.ToLower(part))
                }
            }
        }
    }
    return s, true
}

// MatchSkills returns the skills whose trigger keywords appear in text.
// A skill with no triggers never matches automatically.
func MatchSkills(skills []Skill, text string) []Skill {
    lower := strings.ToLower(text)
    var out []Skill
    for _, s := range skills {
        for _, t := range s.Trigger {
            if strings.Contains(lower, t) {
                out = append(out, s)
                break
            }
        }
    }
    return out
}

// SkillBlock renders matched skills as a system-prompt section.
func SkillBlock(skills []Skill) string {
    if len(skills) == 0 {
        return ""
    }
    var b strings.Builder
    b.WriteString("\n\n## Active skills\n")
    b.WriteString("The following skills are relevant to this request. Follow their guidance.\n\n")
    for _, s := range skills {
        b.WriteString("### " + s.Name + "\n")
        if s.Description != "" {
            b.WriteString(s.Description + "\n\n")
        }
        b.WriteString(s.Body + "\n\n")
    }
    return strings.TrimRight(b.String(), "\n")
}
