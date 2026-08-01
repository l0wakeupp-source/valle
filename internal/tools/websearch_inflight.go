package tools

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type inFlightSearch struct {
	done    chan struct{}
	batches []providerBatch
}

var inFlightSearches = struct {
	sync.Mutex
	calls map[string]*inFlightSearch
}{calls: map[string]*inFlightSearch{}}

func normalizedCacheQuery(query string) string {
	return strings.ToLower(strings.Join(strings.Fields(query), " "))
}

func normalizedSearchKey(query string, maxResults int, forced, variant string) string {
	return strings.Join([]string{strings.ToLower(strings.Join(strings.Fields(query), " ")), string(rune(maxResults)), strings.ToLower(forced), variant}, "\x00")
}

func beginInFlightSearch(key string) (*inFlightSearch, bool) {
	inFlightSearches.Lock()
	defer inFlightSearches.Unlock()
	if existing := inFlightSearches.calls[key]; existing != nil {
		return existing, false
	}
	call := &inFlightSearch{done: make(chan struct{})}
	inFlightSearches.calls[key] = call
	return call, true
}

func finishInFlightSearch(key string, call *inFlightSearch, batches []providerBatch) {
	inFlightSearches.Lock()
	call.batches = cloneProviderBatches(batches)
	delete(inFlightSearches.calls, key)
	close(call.done)
	inFlightSearches.Unlock()
}

func cloneProviderBatches(batches []providerBatch) []providerBatch {
	cloned := make([]providerBatch, len(batches))
	for index, batch := range batches {
		cloned[index] = batch
		cloned[index].results = append([]searchResult(nil), batch.results...)
	}
	return cloned
}

func (t WebSearchTool) collectProviderBatches(ctx context.Context, providers []configuredSearchProvider, query string, maxResults, maxParallel int, parallel bool, variant string) []providerBatch {
	if parallel {
		return t.runParallelProviders(ctx, providers, query, maxResults, maxParallel, variant)
	}
	batches := make([]providerBatch, 0, len(providers))
	for _, provider := range providers {
		batch := t.runProvider(ctx, provider, query, maxResults, variant)
		batches = append(batches, batch)
		if batch.err != nil && (isCanceledError(batch.err) || ctx.Err() != nil) {
			break
		}
		if len(batch.results) > 0 {
			break
		}
	}
	return batches
}

func isCanceledError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
