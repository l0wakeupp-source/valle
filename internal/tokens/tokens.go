// Package tokens centralizes provider-facing token estimation.
package tokens

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/ron2111/omnitoken"
)

// Encoding identifies the tokenizer vocabulary used by a provider/model.
type Encoding string

const (
	EncodingCl100kBase Encoding = omnitoken.EncodingCL100KBase
	EncodingO200kBase  Encoding = omnitoken.EncodingO200KBase
)

// Result describes a token count and whether the exact vocabulary was used.
type Result struct {
	Count    int
	Encoding Encoding
	Exact    bool
}

var engines sync.Map

// Count returns an exact count for the built-in encodings and a conservative
// Unicode-safe estimate for unknown encodings. It never downloads assets or
// invokes an external process on the request path.
func Count(text string, encoding Encoding) Result {
	if engine, ok := engines.Load(string(encoding)); ok {
		return Result{Count: engine.(omnitoken.ModelEngine).CountTokens(text), Encoding: encoding, Exact: true}
	}

	engine, err := omnitoken.ForEncoding(string(encoding))
	if err == nil {
		engines.Store(string(encoding), engine)
		return Result{Count: engine.CountTokens(text), Encoding: encoding, Exact: true}
	}

	return Result{Count: conservativeFallback(text), Encoding: encoding}
}

// EncodingForModel selects the exact BPE vocabulary that matches the tokenizer
// family of a model id. OpenAI's modern models (gpt-4o, gpt-5, o-series,
// codex) tokenize with o200k_base; everything else falls back to cl100k_base.
func EncodingForModel(modelID string) Encoding {
	id := strings.ToLower(modelID)
	for _, marker := range []string{"gpt-4o", "gpt-5", "o1", "o3", "o4", "codex", "o200k"} {
		if strings.Contains(id, marker) {
			return EncodingO200kBase
		}
	}
	return EncodingCl100kBase
}

func conservativeFallback(text string) int {
	if text == "" {
		return 0
	}
	runeCount := utf8.RuneCountInString(text)
	byteEstimate := (len(text) + 3) / 4
	if byteEstimate > runeCount {
		return byteEstimate
	}
	return runeCount
}
