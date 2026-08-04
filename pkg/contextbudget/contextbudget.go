// Package contextbudget manages the provider-facing conversation view:
// content-addressed deduplication of repeated tool outputs, stable cache
// boundary selection for prompt caching, reversible live-zone compression,
// and the tool_use/tool_result atomic-pair safety invariant.
//
// The budget never mutates canonical history; it only builds the view that is
// serialized into a provider request.
package contextbudget

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"rick/internal/provider"
)

// Options configures a Budget.
type Options struct {
	// Enabled is the master switch. Zero value leaves the budget disabled so
	// callers can opt in explicitly (or via New).
	Enabled bool
	// MaxLivePayloads caps the reversible live-zone store.
	MaxLivePayloads int
	// MaxCABPayloads caps the content-addressed store.
	MaxCABPayloads int
	// MinDedupBytes is the minimum tool-result size considered for
	// content-addressed deduplication.
	MinDedupBytes int
	// MinStableTurns is how many consecutive identical observations a history
	// prefix needs before it becomes a cache boundary.
	MinStableTurns int
	// LiveZoneTurns is how many newest logical turns are excluded from cache
	// boundaries (the volatile tail of the conversation).
	LiveZoneTurns int
	// MaxStableBytes is the minimum serialized prefix size worth caching.
	MaxStableBytes int
	// MaxBoundaries caps the cache breakpoints emitted per request.
	MaxBoundaries int
	// LiveZoneCapBytes bounds live-zone compressed tool output.
	LiveZoneCapBytes int
}

func (o Options) withDefaults() Options {
	o.Enabled = true
	if o.MaxLivePayloads <= 0 {
		o.MaxLivePayloads = 512
	}
	if o.MaxCABPayloads <= 0 {
		o.MaxCABPayloads = 1024
	}
	if o.MinDedupBytes <= 0 {
		o.MinDedupBytes = 2048
	}
	if o.MinStableTurns <= 0 {
		o.MinStableTurns = 2
	}
	if o.LiveZoneTurns <= 0 {
		o.LiveZoneTurns = 2
	}
	if o.MaxStableBytes <= 0 {
		o.MaxStableBytes = 4096
	}
	if o.MaxBoundaries <= 0 {
		o.MaxBoundaries = 4
	}
	if o.LiveZoneCapBytes <= 0 {
		o.LiveZoneCapBytes = 8 << 10
	}
	return o
}

// Budget is a per-session context manager. It is safe for concurrent use
// because tool execution may run in parallel.
type Budget struct {
	mu        sync.Mutex
	opts      Options
	live      map[string]string
	liveOrd   []string
	cab       map[string]string
	cabOrd    []string
	stability map[int]*prefixState
}

type prefixState struct {
	hash  string
	count int
}

// New builds an enabled Budget with defaults applied.
func New(opts Options) *Budget {
	return &Budget{
		opts:      opts.withDefaults(),
		live:      map[string]string{},
		cab:       map[string]string{},
		stability: map[int]*prefixState{},
	}
}

// Enabled reports whether the budget applies any transformation.
func (b *Budget) Enabled() bool { return b != nil && b.opts.Enabled }

// StoreLive keeps the original payload under key for reversible retrieval.
func (b *Budget) StoreLive(key, original string) {
	if b == nil || !b.opts.Enabled || key == "" || original == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.live[key]; exists {
		b.live[key] = original
		return
	}
	b.live[key] = original
	b.liveOrd = append(b.liveOrd, key)
	for len(b.liveOrd) > b.opts.MaxLivePayloads {
		oldest := b.liveOrd[0]
		b.liveOrd = b.liveOrd[1:]
		delete(b.live, oldest)
	}
}

// LiveOriginal returns a stored live-zone original, if still retained.
func (b *Budget) LiveOriginal(key string) (string, bool) {
	if b == nil {
		return "", false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	original, ok := b.live[key]
	return original, ok
}

// LiveKeys lists stored live-zone keys in insertion order.
func (b *Budget) LiveKeys() []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.liveOrd...)
}

// Hash returns the content-address of a payload (first 16 hex chars).
func Hash(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:8])
}

// StoredPayload returns a content-addressed payload, if still retained.
func (b *Budget) StoredPayload(hash string) (string, bool) {
	if b == nil {
		return "", false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	payload, ok := b.cab[hash]
	return payload, ok
}

// storeCAB records a payload under its content address with LRU eviction.
func (b *Budget) storeCAB(payload string) string {
	hash := Hash(payload)
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.cab[hash]; exists {
		return hash
	}
	b.cab[hash] = payload
	b.cabOrd = append(b.cabOrd, hash)
	for len(b.cabOrd) > b.opts.MaxCABPayloads {
		oldest := b.cabOrd[0]
		b.cabOrd = b.cabOrd[1:]
		delete(b.cab, oldest)
	}
	return hash
}

// DedupResult reports what ApplyDedup changed.
type DedupResult struct {
	View       []provider.Message
	SavedBytes int
	Replaced   int
}

// ApplyDedup replaces repeated large tool_result payloads within one
// transcript with a self-contained reference. Only the second and later
// occurrences are replaced, and only when the original is still present in the
// same view, so no information is ever lost relative to the sent transcript.
func (b *Budget) ApplyDedup(messages []provider.Message) DedupResult {
	result := DedupResult{View: append([]provider.Message(nil), messages...)}
	if !b.Enabled() {
		return result
	}
	seen := map[string]bool{}
	for index := range result.View {
		for blockIndex := range result.View[index].Content {
			block := &result.View[index].Content[blockIndex]
			if block.Type != "tool_result" || len(block.Content) < b.opts.MinDedupBytes {
				continue
			}
			hash := Hash(block.Content)
			if seen[hash] {
				originalLen := len(block.Content)
				block.Content = fmt.Sprintf("[duplicate payload sha256:%s; identical to an earlier tool result — retrieve via retrieve_uncompressed_context key %s]", hash, hash)
				result.SavedBytes += originalLen - len(block.Content)
				result.Replaced++
				continue
			}
			seen[hash] = true
			b.storeCAB(block.Content)
		}
	}
	return result
}

// Boundary selection ------------------------------------------------

// messageHash is a stable per-message fingerprint used to detect prefix
// stability across requests.
func messageHash(message provider.Message) string {
	raw, err := json.Marshal(message)
	if err != nil {
		return Hash(message.Text())
	}
	return Hash(string(raw))
}

// ChooseBoundaries returns message indices that delimit a stable history
// prefix worth caching. A boundary at index i means "cache everything before
// message i"; the returned map has true at those indices.
//
// Stability is measured across calls: a prefix must be byte-identical for
// MinStableTurns consecutive observations before it is proposed, and the live
// zone (the newest turns) is never a boundary.
func (b *Budget) ChooseBoundaries(messages []provider.Message) map[int]bool {
	out := map[int]bool{}
	if !b.Enabled() || len(messages) < 2 {
		return out
	}

	hashes := make([]string, len(messages))
	byteLen := make([]int, len(messages))
	for i, message := range messages {
		hashes[i] = messageHash(message)
		raw, _ := json.Marshal(message)
		byteLen[i] = len(raw)
	}

	candidates := boundaryCandidates(messages, b.opts.LiveZoneTurns)
	if len(candidates) > 32 {
		candidates = candidates[:32]
	}
	chosen := make([]int, 0, len(candidates))
	lastChosen := -1
	lastChosenBytes := 0
	for _, index := range candidates {
		prefixBytes := 0
		for i := 0; i < index; i++ {
			prefixBytes += byteLen[i]
		}
		if prefixBytes < b.opts.MaxStableBytes {
			continue
		}
		state := b.observePrefix(index, hashes[:index])
		if state.count < b.opts.MinStableTurns {
			continue
		}
		// Keep boundaries spread apart so the cache has real content between
		// consecutive breakpoints.
		if lastChosen >= 0 && prefixBytes-lastChosenBytes < b.opts.MaxStableBytes {
			continue
		}
		chosen = append(chosen, index)
		lastChosen = index
		lastChosenBytes = prefixBytes
		if len(chosen) >= b.opts.MaxBoundaries {
			break
		}
	}
	for _, index := range chosen {
		out[index] = true
	}
	return out
}

// boundaryCandidates lists message indices that end a logical group (never
// splitting a tool_use/tool_result pair) and sit outside the live zone.
func boundaryCandidates(messages []provider.Message, liveTurns int) []int {
	groups := logicalGroups(messages)
	cutoff := len(groups) - liveTurns
	if cutoff < 1 {
		return nil
	}
	var indices []int
	for index := 1; index < cutoff; index++ {
		indices = append(indices, groups[index].start)
	}
	return indices
}

// observePrefix records one observation of the prefix ending at index and
// returns the running stability state for it.
func (b *Budget) observePrefix(index int, prefixHashes []string) *prefixState {
	hash := combineHashes(prefixHashes)
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.stability[index]
	if state == nil {
		state = &prefixState{hash: hash, count: 1}
		b.stability[index] = state
		return state
	}
	if state.hash == hash {
		state.count++
	} else {
		state.hash = hash
		state.count = 1
	}
	return state
}

func combineHashes(hashes []string) string {
	sum := sha256.New()
	for _, h := range hashes {
		sum.Write([]byte(h))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil)[:8])
}

// logicalGroups groups messages so tool_use and its tool_result are atomic.
type group struct {
	start int
}

func logicalGroups(messages []provider.Message) []group {
	groups := make([]group, 0, len(messages))
	for index := 0; index < len(messages); index++ {
		start := index
		if hasBlock(messages[index], "tool_use") && index+1 < len(messages) && hasBlock(messages[index+1], "tool_result") {
			index++
		}
		groups = append(groups, group{start: start})
	}
	return groups
}

func hasBlock(message provider.Message, kind string) bool {
	for _, block := range message.Content {
		if block.Type == kind {
			return true
		}
	}
	return false
}

// Pair safety ------------------------------------------------------

// VerifyPairSafety returns an error when any tool_result in messages lacks its
// paired tool_use in the immediately preceding message of the same logical
// group, or when any tool_use is the final message of the transcript.
func VerifyPairSafety(messages []provider.Message) error {
	for index := 0; index < len(messages); index++ {
		message := messages[index]
		if hasBlock(message, "tool_use") {
			if index+1 >= len(messages) {
				return fmt.Errorf("contextbudget: tool_use at message %d has no tool_result", index)
			}
			next := messages[index+1]
			if !hasBlock(next, "tool_result") {
				return fmt.Errorf("contextbudget: tool_use at message %d not followed by tool_result", index)
			}
			index++ // skip the paired result
		}
		if hasBlock(message, "tool_result") {
			// The pairing above already consumed results after a tool_use.
			// A result here means its tool_use was evicted.
			return fmt.Errorf("contextbudget: orphaned tool_result at message %d", index)
		}
	}
	return nil
}

// Live-zone compression ---------------------------------------------

// CompressLive returns a compact, reversible view of a fresh tool result. The
// original payload is stored under key so the model can pull it back. The
// boolean reports whether the output actually changed.
func (b *Budget) CompressLive(key, text string) (string, bool) {
	if !b.Enabled() || text == "" {
		return text, false
	}
	compressed := minifyJSON(text)
	if compressed == text {
		compressed = capLive(text, b.opts.LiveZoneCapBytes)
	}
	changed := compressed != text
	if changed {
		b.StoreLive(key, text)
	}
	return compressed, changed
}

// minifyJSON applies a structural mask to bulky JSON: whitespace is removed
// and long arrays/objects are summarized while preserving shape.
func minifyJSON(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return text
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return text
	}
	return maskJSON(value, 0)
}

func maskJSON(value any, depth int) string {
	if depth > 3 {
		return "…"
	}
	switch value := value.(type) {
	case nil:
		return "null"
	case bool:
		if value {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%v", value)
	case string:
		if len(value) > 160 {
			truncated, _ := json.Marshal(value[:157])
			return string(truncated) + "…"
		}
		encoded, _ := json.Marshal(value)
		return string(encoded)
	case []any:
		if len(value) == 0 {
			return "[]"
		}
		if len(value) <= 6 {
			parts := make([]string, 0, len(value))
			for _, item := range value {
				parts = append(parts, maskJSON(item, depth+1))
			}
			return "[" + strings.Join(parts, ",") + "]"
		}
		head := value[:2]
		parts := make([]string, 0, len(head)+1)
		for _, item := range head {
			parts = append(parts, maskJSON(item, depth+1))
		}
		return fmt.Sprintf("[%s,…<%d more items>]", strings.Join(parts, ","), len(value)-2)
	case map[string]any:
		if len(value) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 8 {
			keys = keys[:8]
		}
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%q:%s", key, maskJSON(value[key], depth+1)))
		}
		if len(keys) < len(value) {
			parts = append(parts, fmt.Sprintf("…<%d more keys>", len(value)-len(keys)))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	return "…"
}

func capLive(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	omitted := len(text) - limit
	marker := fmt.Sprintf("\n… <live-zone compressed; %d bytes omitted; retrieve original via retrieve_uncompressed_context>", omitted)
	if len(marker) >= limit {
		return marker
	}
	keep := limit - len(marker)
	headBytes := keep / 2
	tailBytes := keep - headBytes
	head := safeCut(text, headBytes, false)
	tail := safeCut(text, tailBytes, true)
	return head + marker + tail
}

// safeCut returns a UTF-8-safe prefix (fromStart) or suffix (toEnd) of text.
func safeCut(text string, limit int, toEnd bool) string {
	if limit <= 0 {
		return ""
	}
	if limit >= len(text) {
		return text
	}
	if !toEnd {
		for limit > 0 && !utf8.RuneStart(text[limit]) {
			limit--
		}
		return text[:limit]
	}
	start := len(text) - limit
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}
