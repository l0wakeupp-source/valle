package tools

import (
	"bytes"
	"fmt"
	"sync"
)

const defaultToolInputLimit = 16 << 20
const defaultSearchOutputLimit = 4 << 20

type boundedBuffer struct {
	mu        sync.Mutex
	data      bytes.Buffer
	limit     int
	total     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
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

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

func (b *boundedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Len()
}

func (b *boundedBuffer) Total() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}

func (b *boundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func (b *boundedBuffer) Output() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	output := b.data.String()
	if !b.truncated {
		return output
	}
	omitted := b.total - len(output)
	return fmt.Sprintf("%s\n\n… <%d bytes omitted> …", output, omitted)
}
