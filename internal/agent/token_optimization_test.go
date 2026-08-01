package agent

import (
	"strings"
	"testing"
)

func TestCapModelToolOutputPreservesMarkerAndUTF8(t *testing.T) {
	input := strings.Repeat("данные ", maxModelToolResultBytes)
	output := capModelToolOutput(input)
	if len(output) > maxModelToolResultBytes {
		t.Fatalf("capped output length = %d, want <= %d", len(output), maxModelToolResultBytes)
	}
	if !strings.Contains(output, "tool output truncated") || !strings.Contains(output, "bytes omitted") {
		t.Fatalf("output lacks truncation marker: %q", output[len(output)-120:])
	}
	if !strings.Contains(output, "данные") {
		t.Fatal("capped output lost the original UTF-8 content")
	}
}
