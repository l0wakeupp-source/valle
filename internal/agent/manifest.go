package agent

import (
	"fmt"
	"sort"
	"strings"

	"rick/internal/provider"
)

// toolManifest renders the registered tools as a compact TypeScript-style
// interface block for the system prompt. The provider still receives the full
// JSON Schemas on the wire (the API requires them); the manifest gives the
// model a one-glance index of every callable and its argument types without
// the JSON overhead. Returned as an empty string when there are no tools.
func toolManifest(schemas []provider.ToolSchema) string {
	if len(schemas) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Tools (TypeScript signatures)\n")
	for _, schema := range schemas {
		b.WriteString(toolSignature(schema))
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// toolSignature renders one tool call as a TS function signature.
func toolSignature(schema provider.ToolSchema) string {
	props, _ := schema.InputSchema["properties"].(map[string]any)
	required, _ := schema.InputSchema["required"].([]any)
	requiredSet := map[string]bool{}
	for _, name := range required {
		if text, ok := name.(string); ok {
			requiredSet[text] = true
		}
	}

	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	var args []string
	for _, name := range names {
		prop, _ := props[name].(map[string]any)
		if !requiredSet[name] {
			args = append(args, name+"?: "+tsType(prop))
			continue
		}
		args = append(args, name+": "+tsType(prop))
	}
	return fmt.Sprintf("%s(%s): void", schema.Name, strings.Join(args, ", "))
}

// tsType maps a JSON Schema property to a TypeScript type name.
func tsType(prop map[string]any) string {
	kind, _ := prop["type"].(string)
	switch kind {
	case "array":
		return "any[]"
	case "object":
		return "object"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "string":
		if values, ok := prop["enum"].([]any); ok && len(values) > 0 {
			var quoted []string
			for _, value := range values {
				quoted = append(quoted, fmt.Sprintf("%q", value))
			}
			return strings.Join(quoted, " | ")
		}
		return "string"
	default:
		return "any"
	}
}
