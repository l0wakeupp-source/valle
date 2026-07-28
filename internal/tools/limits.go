package tools

import (
	"bytes"
	"fmt"
)

const defaultToolInputLimit = 16 << 20
const defaultSearchOutputLimit = 4 << 20

type boundedBuffer struct {
	data      bytes.Buffer
	limit     int
	total     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.total += len(p)
	if b.limit <= b.data.Len() {
		b.truncated = b.truncated || len(p) > 0
		return len(p), nil
	}

	remaining := b.limit - b.data.Len()
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		_, _ = b.data.Write(p[:remaining])
	}
	if remaining < len(p) {
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string { return b.data.String() }

func (b *boundedBuffer) Len() int { return b.data.Len() }

func (b *boundedBuffer) Total() int { return b.total }

func (b *boundedBuffer) Truncated() bool { return b.truncated }

func (b *boundedBuffer) Output() string {
	output := b.String()
	if !b.truncated {
		return output
	}
	omitted := b.total - len(output)
	return fmt.Sprintf("%s\n\n… <%d bytes omitted> …", output, omitted)
}
