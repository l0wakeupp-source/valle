# Web Provider Resilience and Fallback Plan

## Goal

Make `/webproviders`, `/websearch`, and the web-search tool reliable when several searches happen close together or one provider returns `unavailable`, `rate limited`, or `provider budget exhausted`.

The system should:

- return useful results when at least one eligible provider is healthy;
- fail over without making the user wait through a dead provider's full timeout;
- never confuse Rick's session search budget with an upstream provider quota;
- avoid multiplying one logical search into an uncontrolled fan-out of upstream requests;
- expose the real reason a provider was skipped or failed;
- support many free, freemium, self-hosted, and domain-specific sources without pretending that all are equivalent general-web indexes.

## Current Findings

The current implementation is concentrated in:

- `internal/tools/websearch.go`
- `internal/tools/websearch_expanded.go`
- `internal/tui/websearch.go`
- `internal/config/config.go`
- `rick.json.schema.json`
- `internal/tools/websearch*_test.go`
- `internal/tui/websearch_test.go`

The important failure modes are:

1. `auto` enables several public/scraped providers by default, including `bing`, `brave`, and `searxng`, even when they are not configured by the user.
2. Search execution can fan out to four providers in parallel. Repeated agent calls can therefore create a burst against the same public endpoint or API key.
3. Provider retry classification is primarily string-based and uses a fixed retry. It does not consistently interpret HTTP status, `Retry-After`, quota-reset headers, or provider-specific usage fields.
4. The budget is checked and incremented before the upstream result is known. Fallback attempts and provider retries are not represented separately from the logical Rick search request.
5. Provider errors are collapsed too early, so the user cannot tell whether a provider is missing configuration, temporarily rate-limited, permanently exhausted, unauthorized, malformed, or simply returned no results.
6. `/webproviders` currently exposes only a small subset of the actual provider implementations. There is no complete provider health/cooldown/quota view or per-provider test/reset action.
7. The current `bing` implementation must not be treated as a viable fallback: Microsoft retired the Bing Search APIs on August 11, 2025.

Current targeted baseline is green:

```text
go test ./internal/tools ./internal/tui ./internal/config -count=1
ok  rick/internal/tools
ok  rick/internal/tui
ok  rick/internal/config
```

## Provider Inventory

### Tier A: General-web providers worth implementing first

These have structured APIs and a documented free/freemium path. They should use the common adapter contract, quota accounting, and typed error handling.

| Provider | Current free/freemium evidence | Role | Recommendation |
|---|---|---|---|
| Brave Search API | Official pricing page lists `$5` free credits every month; search is `$5/1,000` requests and capacity is listed as 50 QPS. | Independent general-web index | First-class primary/fallback |
| Tavily Search | Official docs list 1,000 free API credits/month; basic search costs 1 credit and advanced costs 2. Development keys are listed at 100 RPM. 429 responses include `retry-after`. | AI-oriented web search | First-class fallback; default to basic depth |
| Exa Search | Official pricing page lists $20 signup credits and a free tier with $10 credits/month. | AI-oriented semantic/web search | First-class fallback; use only when configured |
| Serper | Official site lists 2,500 free queries with no credit card required. | Google SERP results | First-class fallback; key required |
| You.com Search API | Official pricing/docs list $100 free credit to start and $5/1,000 search calls. | General web search for AI apps | First-class fallback; key required |
| Firecrawl Search | Official pricing lists a free plan with 1,000 monthly credits plus 1,000 search credits; search costs 2 credits per 10 results. | Search plus page extraction | First-class fallback, but reserve it for search/extract workflows |
| SerpApi | Official pricing lists 250 searches/month free and 50 throughput/hour. | SERP aggregator | Optional fallback; low monthly capacity |
| Google Custom Search JSON API | Google documentation lists 100 free queries/day. Requires a configured programmable search engine and Google credentials. | Site/general search depending on engine configuration | Optional; clearly label site-restricted/config-dependent behavior |
| Jina Search | Official Jina material documents `s.jina.ai` as a web/SERP search endpoint; the keyless path does not provide a stable quota guarantee in the material checked. | Keyless convenience search / reader ecosystem | Experimental opt-in; conservative rate limit and circuit breaker |

The exact quotas must be stored as provider metadata only for display and local budgeting. They must not be used as hard-coded truth: providers change plans, and the adapter must prefer response headers/usage data and let the user override local limits.

### Tier B: Keyless or self-hosted general-web fallbacks

| Provider | Role | Correct policy |
|---|---|---|
| Local SearXNG | Best keyless general-web fallback when the user controls the instance. Its JSON API supports `GET /search?q=...&format=json`. | First-class local provider. Default only when explicitly configured as local/self-hosted. Apply a local concurrency limit. |
| Public SearXNG instances | Useful emergency fallback, but every instance has its own operator policy, engine mix, capacity, and blocking behavior. | Never silently scrape a fixed public list. Let users add instances, health-check them, rotate only among healthy instances, and show the instance hostname in provenance. |
| DuckDuckGo HTML / Instant Answer | Already present and useful as a low-dependency fallback, but HTML parsing is brittle and Instant Answer is not a complete web index. | Keep as low-priority keyless fallback. Treat empty/partial results as a valid degraded response, not a provider crash. |
| `Whoogle`, `LibreY`, `4get` | User-hosted privacy frontends; generally HTML/front-end adapters rather than stable vendor APIs. | Optional user-hosted adapters only. Do not ship third-party public instances as defaults. |
| Marginalia Search | Independent/open search-engine software and a possible self-hosted/community option. | Research/optional adapter after the stable API-backed tier; no default public dependency until a stable endpoint and usage policy are verified. |
| Mojeek HTML | Independent search engine; official API is a paid product, so the HTML site is not a guaranteed free API. | Do not use HTML scraping as a default provider. Add the official API only as an optional paid adapter if desired. |
| Kagi API | Official Search API is paid (`$12/1,000` requests in the current pricing material). | Optional paid provider, not a free fallback. |

### Tier C: Domain-specific free APIs

These should not be blindly mixed into every general query. They should be selected by explicit provider choice, query classification, or a future `domain` option. They are excellent fallbacks for the right intent.

- GDELT DOC API: news/event search.
- MediaWiki API: Wikipedia and other Wikimedia projects.
- arXiv API: papers and preprints; respect its usage guidance and pacing.
- Crossref REST API: scholarly metadata and DOI search; use a descriptive User-Agent and polite-pool behavior.
- OpenAlex API: scholarly works/authors/institutions; support its free key/no-key modes and response limits.
- Europe PMC and NCBI E-utilities: biomedical literature.
- Semantic Scholar API: scholarly papers/citations, subject to its current request limits.
- GitHub Search API: repositories, code, issues, and documentation-related discovery; authenticated and unauthenticated limits differ.
- Stack Exchange API: programming/Q&A search.
- Hacker News Algolia API: Hacker News stories/comments.
- Internet Archive Advanced Search: archived items and metadata.
- Common Crawl Index API: historical web-crawl lookup, not live-web freshness.

These sources should return the same normalized result model, but their capability metadata must identify their corpus, freshness, and query scope so the UI and agent do not imply that a biomedical or archive result is equivalent to a current general-web search.

### Do not implement as a fallback

- Bing Search API: retired by Microsoft; remove/deprecate the current adapter and migrate old config without trying it.
- Undocumented Google/Bing/Yandex internal endpoints or CAPTCHA workarounds: brittle, likely to violate provider terms, and impossible to health-check reliably.
- Arbitrary public proxy/API-key lists: unsafe, unstable, and likely to leak user queries or credentials.

## Target Architecture

### 1. Common provider contract

Refactor the individual functions in `internal/tools/websearch_expanded.go` behind a shared interface, for example:

```go
type SearchProvider interface {
    ID() string
    Metadata() ProviderMetadata
    Search(ctx context.Context, request SearchRequest) (SearchResponse, error)
}
```

The common request/response types should include:

- query, max results, locale, safe-search, time range, domains, and timeout;
- normalized title, URL, snippet, published time, source/domain, and optional raw metadata;
- provider ID and endpoint/instance provenance;
- response duration and request ID when supplied by the upstream;
- usage: requests, credits, remaining quota, and reset time when available;
- whether results are complete, partial, stale, or degraded.

Keep provider-specific JSON parsing inside the adapter. The scheduler must never inspect arbitrary response strings to decide whether a provider is healthy.

### 2. Typed error taxonomy

Add a typed `ProviderError` with fields such as:

- provider ID;
- class: `missing_config`, `invalid_auth`, `rate_limited`, `quota_exhausted`, `temporarily_unavailable`, `timeout`, `network`, `invalid_response`, `no_results`, `not_supported`, or `permanent`;
- HTTP status and upstream request ID;
- `retryAt` / `resetAt` when known;
- safe user-facing message;
- whether another provider should be attempted;
- whether the event should open a circuit.

Classification order:

1. HTTP status and headers (`429`, `401`, `403`, `408`, `409`, `5xx`, `Retry-After`, `X-RateLimit-*`).
2. Provider JSON error code/type.
3. Provider-specific quota/auth markers.
4. Conservative fallback classification.

Never call an upstream quota failure “Rick search budget exhausted.” The final error must distinguish:

```text
Rick session search budget exhausted
Provider quota exhausted: tavily (reset unknown)
Provider temporarily rate-limited: brave (retry after 42s)
No configured/healthy provider succeeded
```

### 3. Scheduler instead of unconditional fan-out

Introduce a shared `SearchScheduler` in `internal/tools`:

- one global concurrency semaphore for all logical web searches;
- one token bucket/rate limiter per provider and per endpoint/instance;
- provider priority and capability matching;
- circuit breaker state: closed, open, half-open;
- a context deadline for the whole logical search;
- a small bounded fallback chain.

Default `auto` behavior:

1. Build eligible providers from explicit configuration, local providers, and safe keyless defaults.
2. Drop providers that are missing keys, disabled, open-circuit, or locally quota-exhausted before launching requests.
3. Try the highest-priority healthy provider.
4. Start at most one hedge provider after a short delay only if the first provider is slow and the request allows hedging.
5. On retryable failure, move immediately to the next healthy provider rather than waiting through repeated retries.
6. Use parallel fan-out only when the user explicitly requests `parallel`/multi-source mode, with a strict provider count and global concurrency cap.
7. Stop when a quality threshold is met or the logical deadline is reached.

This prevents four providers from being hit for every request and prevents several simultaneous agent tool calls from overwhelming the same API key.

### 4. Retry, cooldown, and circuit-breaker rules

- Parse `Retry-After` as both delta-seconds and HTTP date.
- Parse provider reset headers and usage fields where available.
- Retry a 429 only if the wait fits inside the logical deadline; otherwise fail over immediately.
- Retry transient network/5xx failures with bounded exponential backoff plus jitter, normally at most once per provider per logical search.
- Do not retry invalid credentials, missing configuration, hard quota exhaustion, unsupported parameters, or malformed responses.
- Open a provider circuit on repeated transient failures, confirmed quota exhaustion, or a rate limit with a future reset. Use the server-provided reset when available, otherwise a bounded cooldown.
- Half-open with one probe request; do not let concurrent callers all probe at once.
- Persist only non-secret health state if desired; never persist API keys in health logs.
- Include the provider's safe error and next retry time in `/webproviders`.

### 5. Logical-search budget and deduplication

Change accounting so one user/agent web-search invocation is one logical search budget unit. Fallback attempts, hedges, and retries are upstream-attempt metrics, not additional Rick search-budget units.

Also add:

- singleflight for identical normalized query + options while a request is in flight;
- short-lived result cache, with a conservative TTL and query/options/provider-mode key;
- optional stale-while-revalidate only when the caller permits stale results;
- URL canonicalization and deduplication when combining providers;
- per-provider attempt counters for diagnostics.

If no provider is eligible before an upstream attempt begins, do not consume the logical budget. If a logical request starts and all providers fail, report that one logical search was attempted and list the provider outcomes separately.

## `/webproviders` UX

Expand `internal/tui/websearch.go` from a small credential form into a provider control panel.

Each provider row should show:

```text
[on] Brave API       READY       priority 10   0 errors   quota: local/unknown
[on] Tavily          COOLDOWN    retry in 42s quota: 0/1000 credits
[on] SearXNG local   READY       priority 20   endpoint: localhost
[off] Exa             MISSING KEY
```

Required actions:

- enable/disable provider;
- edit API key or environment-variable reference with masked display;
- edit base URL or SearXNG instance list;
- edit priority, timeout, max concurrency, local RPM, and monthly/daily local budget;
- select auto mode: sequential fallback, one hedge, or explicit bounded parallel;
- test one provider with a harmless query;
- test all configured providers sequentially with a strict test budget;
- reset a circuit/cooldown manually;
- view the last safe error, last success, latency, result count, and reset time;
- view the final fallback order and why each provider is currently eligible/ineligible.

Add a compact `/webstatus` or an equivalent detail view if the panel becomes too dense. The main search error should offer the provider status view instead of only printing “unavailable.”

Do not print or persist raw API keys. Environment-variable references should be preferred for shared machines and logs.

## Configuration Shape

Extend the config model in `internal/config/config.go` and schema in `rick.json.schema.json` with a versioned provider configuration. Preserve existing fields and names during migration.

Suggested shape:

```json
{
  "web_search": {
    "mode": "auto",
    "max_parallel": 1,
    "hedge_after_ms": 900,
    "logical_budget": 10,
    "cache_ttl_seconds": 60,
    "providers": {
      "brave": {
        "enabled": true,
        "priority": 10,
        "api_key_env": "BRAVE_SEARCH_API_KEY",
        "max_rpm": 30,
        "monthly_budget": 1000
      },
      "searxng": {
        "enabled": true,
        "priority": 20,
        "base_url": "http://localhost:8080",
        "instances": []
      }
    }
  }
}
```

Use an explicit `source`/`kind` field for `api`, `local`, `public_instance`, and `domain`. Do not infer safety or quota behavior from a provider display name.

## Implementation Phases

### Phase 0: Freeze behavior and fixtures

Files:

- `internal/tools/websearch*_test.go`
- `internal/tui/websearch_test.go`
- `internal/config/websearch_config_test.go`

Tasks:

1. Add `httptest.Server` fixtures for success, empty result, 401, 403, 408, 429 with seconds/date `Retry-After`, 5xx, malformed JSON, and quota-reset headers.
2. Add a baseline test for the current logical budget behavior so the migration is intentional rather than accidental.
3. Add provider response fixtures for every existing adapter before moving code.
4. Keep all live-provider tests opt-in through an environment variable; the normal suite must not depend on the public internet.

### Phase 1: Provider interface and typed outcomes

Files:

- `internal/tools/websearch_provider.go` (new)
- `internal/tools/websearch_errors.go` (new)
- `internal/tools/websearch_expanded.go`
- `internal/tools/websearch.go`

Tasks:

1. Define normalized request/result/usage/metadata types.
2. Wrap existing DuckDuckGo, DuckDuckGo Instant, SearXNG, Brave HTML, and Ollama behavior behind adapters.
3. Add typed error classification and response metadata.
4. Preserve existing provider names and config compatibility.
5. Remove the Bing adapter from automatic selection and add a migration warning for old `bing` config.

### Phase 2: Scheduler, rate limits, and budget separation

Files:

- `internal/tools/websearch_scheduler.go` (new)
- `internal/tools/websearch_circuit.go` (new)
- `internal/tools/websearch_budget.go` (new or extracted)
- `internal/tools/websearch.go`

Tasks:

1. Implement eligibility filtering, priority ordering, per-provider limiters, and global concurrency.
2. Implement sequential fallback as the default.
3. Add bounded hedging and explicit bounded parallel mode.
4. Implement `Retry-After`/reset handling and circuit states.
5. Make logical budget accounting independent from upstream attempts.
6. Add singleflight, short result caching, and canonical URL deduplication.
7. Produce an aggregated diagnostic containing every attempted/skipped provider and its typed reason.

### Phase 3: Stable free/freemium API adapters

Files:

- `internal/tools/providers/brave.go`
- `internal/tools/providers/tavily.go`
- `internal/tools/providers/exa.go`
- `internal/tools/providers/serper.go`
- `internal/tools/providers/you.go`
- `internal/tools/providers/firecrawl.go`
- `internal/tools/providers/serpapi.go`
- `internal/tools/providers/google_cse.go`
- `internal/tools/providers/jina.go`

Tasks:

1. Add one adapter at a time with fixture tests and usage/error mapping.
2. Normalize provider-specific result fields and preserve source provenance.
3. Apply provider-specific request cost defaults, but allow user overrides.
4. Add `Retry-After`, reset, request-ID, and usage extraction where the provider exposes them.
5. Register each provider only when configured or explicitly enabled; no hidden network calls.

Recommended first implementation order:

1. Brave
2. Tavily
3. Serper
4. Exa
5. You.com
6. Google CSE
7. Firecrawl
8. SerpApi
9. Jina experimental

### Phase 4: Local/public-instance adapters

Files:

- `internal/tools/providers/searxng.go`
- `internal/tools/providers/duckduckgo.go`
- `internal/tools/providers/userhosted_frontend.go` (only if needed)
- `internal/tools/searxng_instances.go` (new)

Tasks:

1. Make local SearXNG a first-class configured provider.
2. Support multiple user-supplied SearXNG instances with per-host health and cooldown state.
3. Add a strict per-instance concurrency and request rate limit.
4. Do not ship a hard-coded list of public instances as an automatic pool.
5. Keep DuckDuckGo as a low-priority degraded fallback and classify parser changes as `invalid_response`, not `quota_exhausted`.
6. Add optional user-hosted Whoogle/LibreY/4get only after explicit base-URL configuration and legal/operational review.

### Phase 5: Domain-specific search

Files:

- `internal/tools/providers/gdelt.go`
- `internal/tools/providers/mediawiki.go`
- `internal/tools/providers/arxiv.go`
- `internal/tools/providers/crossref.go`
- `internal/tools/providers/openalex.go`
- `internal/tools/providers/biomedical.go`
- `internal/tools/providers/github_search.go`
- `internal/tools/providers/stackexchange.go`
- `internal/tools/providers/hn.go`
- `internal/tools/providers/archive.go`

Tasks:

1. Add explicit capability tags and query-domain selection.
2. Respect each service's request pacing, User-Agent, authentication, and attribution rules.
3. Never select a domain-specific provider for a generic query unless the user requests that domain or classifier confidence is high.
4. Add source labels such as `Wikipedia`, `arXiv`, `GitHub`, or `GDELT` to every result.

### Phase 6: `/webproviders` and diagnostics

Files:

- `internal/tui/websearch.go`
- `internal/tui/websearch_test.go`
- `internal/config/config.go`
- `rick.json.schema.json`

Tasks:

1. Render every registered provider, not just the original credential subset.
2. Add ready/missing/cooldown/quota/error states.
3. Add test, reset, enable/disable, priority, and fallback-order actions.
4. Add masked credential editing and environment-variable support.
5. Add a final diagnostic view for failed searches.
6. Add migration for old fields and obsolete `bing` settings.

### Phase 7: Verification and rollout

Tasks:

1. Run unit and `httptest` tests for all adapters and scheduler paths.
2. Run race detection on scheduler/circuit/cache tests.
3. Run the full project test suite and vet.
4. Run opt-in live probes sequentially with one query per provider, bounded timeouts, and no quota-burning test loops.
5. Test repeated identical searches to confirm singleflight/cache behavior.
6. Test concurrent distinct searches to confirm global and per-provider limits.
7. Test all failure classes and verify the final user message distinguishes Rick budget, provider quota, auth, timeout, and no-results.
8. Build the actual Windows binary and test both the executable and PATH-resolved extensionless launcher as required by the project workflow.

## Acceptance Criteria

- A configured healthy provider returns results even when another provider is rate-limited or exhausted.
- One logical Rick search consumes one logical budget unit regardless of fallback attempts.
- Repeated identical concurrent searches generate one upstream request.
- Default auto mode does not fan out to four public providers.
- A 429 with `Retry-After` is either waited on within deadline or bypassed; it is never retried immediately in a loop.
- A quota-exhausted provider is cooled down until reset and is not selected on every subsequent search.
- `/webproviders` shows all providers and the reason each is ready, skipped, or cooling down.
- API keys never appear in UI logs, error text, test output, or persisted health state.
- Provider output is normalized and deduplicated while retaining provider provenance.
- No live network dependency is added to the ordinary test suite.
- Bing is no longer attempted.

## Research Sources

Primary/current sources consulted:

- Brave Search API: https://brave.com/search/api/
- Tavily credits: https://docs.tavily.com/documentation/api-credits
- Tavily limits and 429 handling: https://docs.tavily.com/documentation/rate-limits
- Exa pricing: https://exa.ai/pricing
- Serper: https://serper.dev/
- You.com docs/pricing: https://you.com/docs/welcome and https://you.com/pricing
- Firecrawl pricing: https://www.firecrawl.dev/pricing
- SerpApi pricing: https://serpapi.com/pricing
- Google Custom Search JSON API: https://developers.google.com/custom-search/v1/overview
- Jina Reader/Search: https://jina.ai/reader/ and https://github.com/jina-ai/reader
- SearXNG search API: https://docs.searxng.org/dev/search_api.html
- Mojeek Search API: https://www.mojeek.com/services/search/web-search-api/
- Kagi Search API: https://help.kagi.com/kagi/api/search.html
- Bing Search API retirement: https://learn.microsoft.com/en-us/lifecycle/announcements/bing-search-api-retirement
- GDELT data/API overview: https://www.gdeltproject.org/data.html
- MediaWiki Search API: https://www.mediawiki.org/wiki/API:Search
- arXiv API usage: https://info.arxiv.org/help/api/tou.html
- Crossref REST API access: https://www.crossref.org/documentation/retrieve-metadata/rest-api/access-and-authentication/
- OpenAlex authentication: https://developers.openalex.org/guides/authentication
- GitHub Search API: https://docs.github.com/rest/search/search
- Stack Exchange API search: https://api.stackexchange.com/docs/search
- Hacker News Algolia API: https://hn.algolia.com/api
- Common Crawl index: https://index.commoncrawl.org/
- Internet Archive Advanced Search: https://archive.org/advancedsearch.php

Quota and pricing information is a research snapshot, not a permanent contract. The implementation must use live response headers/usage data, local caps, and user-configurable overrides.
