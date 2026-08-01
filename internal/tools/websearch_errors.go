package tools

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProviderErrorClass is a stable, machine-readable reason for an upstream
// provider outcome. It intentionally does not expose provider response bodies.
type ProviderErrorClass string

const (
	ProviderMissingConfig      ProviderErrorClass = "missing_config"
	ProviderInvalidAuth        ProviderErrorClass = "invalid_auth"
	ProviderRateLimited        ProviderErrorClass = "rate_limited"
	ProviderQuotaExhausted     ProviderErrorClass = "quota_exhausted"
	ProviderTemporarilyUnavail ProviderErrorClass = "temporarily_unavailable"
	ProviderTimeout            ProviderErrorClass = "timeout"
	ProviderNetwork            ProviderErrorClass = "network"
	ProviderInvalidResponse    ProviderErrorClass = "invalid_response"
	ProviderNoResults          ProviderErrorClass = "no_results"
	ProviderNotSupported       ProviderErrorClass = "not_supported"
	ProviderPermanent          ProviderErrorClass = "permanent"
	ProviderCooldown           ProviderErrorClass = "cooldown"
	ProviderCanceled           ProviderErrorClass = "canceled"
)

// ProviderError is the safe boundary between an adapter and the scheduler.
// Message is authored by Rick and must never contain credentials or bodies.
type ProviderError struct {
	Provider    string
	Class       ProviderErrorClass
	Status      int
	RetryAt     time.Time
	ResetAt     time.Time
	RequestID   string
	Message     string
	Retryable   bool
	OpenCircuit bool
	TryFallback bool
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "provider error"
	}
	message := e.Message
	if message == "" {
		message = string(e.Class)
	}
	if e.Provider == "" {
		return message
	}
	return e.Provider + ": " + message
}

func (e *ProviderError) Unwrap() error { return nil }

func newProviderError(provider string, class ProviderErrorClass, message string) *ProviderError {
	return &ProviderError{
		Provider:    strings.ToLower(strings.TrimSpace(provider)),
		Class:       class,
		Message:     message,
		TryFallback: true,
	}
}

func invalidResponseError(provider, message string) *ProviderError {
	return newProviderError(provider, ProviderInvalidResponse, message)
}

func providerHTTPError(provider string, status int, headers http.Header) *ProviderError {
	class := ProviderPermanent
	retryable := false
	openCircuit := false
	message := fmt.Sprintf("HTTP %d", status)
	switch {
	case status == http.StatusUnauthorized:
		class, message = ProviderInvalidAuth, "authentication rejected"
	case status == http.StatusPaymentRequired:
		class, message, openCircuit = ProviderQuotaExhausted, "provider quota exhausted", true
	case status == http.StatusForbidden:
		class, message = ProviderInvalidAuth, "request forbidden"
	case status == http.StatusTooManyRequests:
		class, message, retryable, openCircuit = ProviderRateLimited, "rate limited", true, true
	case status == http.StatusRequestTimeout || status == http.StatusConflict:
		class, message, retryable = ProviderTemporarilyUnavail, "temporary upstream failure", true
	case status >= 500 && status <= 599:
		class, message, retryable, openCircuit = ProviderTemporarilyUnavail, "upstream unavailable", true, true
	case status >= 400 && status <= 499:
		class = ProviderPermanent
	}
	retryAt := retryAfter(headers)
	resetAt := quotaReset(headers)
	if class == ProviderRateLimited && !retryAt.IsZero() {
		openCircuit = true
	}
	return &ProviderError{
		Provider:    strings.ToLower(strings.TrimSpace(provider)),
		Class:       class,
		Status:      status,
		RetryAt:     retryAt,
		ResetAt:     resetAt,
		Message:     message,
		Retryable:   retryable,
		OpenCircuit: openCircuit,
		TryFallback: true,
		RequestID:   safeRequestID(headers),
	}
}

func retryAfter(headers http.Header) time.Time {
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if value == "" {
		return time.Time{}
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Now().Add(time.Duration(seconds) * time.Second)
	}
	if parsed, err := http.ParseTime(value); err == nil {
		return parsed
	}
	return time.Time{}
}

func quotaReset(headers http.Header) time.Time {
	for _, name := range []string{"X-RateLimit-Reset", "RateLimit-Reset", "X-Quota-Reset"} {
		value := strings.TrimSpace(headers.Get(name))
		if value == "" {
			continue
		}
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil || seconds <= 0 {
			continue
		}
		if seconds > 1_000_000_000 {
			return time.Unix(seconds, 0)
		}
		return time.Now().Add(time.Duration(seconds) * time.Second)
	}
	return time.Time{}
}

func safeRequestID(headers http.Header) string {
	for _, name := range []string{"X-Request-ID", "X-Request-Id", "Request-Id"} {
		value := strings.TrimSpace(headers.Get(name))
		if value != "" && len(value) <= 128 {
			return value
		}
	}
	return ""
}

func providerErrorFrom(err error, provider string) *ProviderError {
	if err == nil {
		return nil
	}
	var typed *ProviderError
	if errors.As(err, &typed) {
		copy := *typed
		if copy.Provider == "" {
			copy.Provider = strings.ToLower(strings.TrimSpace(provider))
		}
		return &copy
	}
	if errors.Is(err, context.Canceled) {
		return &ProviderError{Provider: provider, Class: ProviderCanceled, Message: "search canceled", TryFallback: false}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ProviderError{Provider: provider, Class: ProviderTimeout, Message: "request timed out", Retryable: true, TryFallback: true, OpenCircuit: true}
	}
	message := strings.ToLower(err.Error())
	for _, status := range []int{408, 409, 429, 500, 502, 503, 504, 529} {
		if strings.Contains(message, fmt.Sprintf("%d", status)) {
			return providerHTTPError(provider, status, nil)
		}
	}
	if strings.Contains(message, "api key") || strings.Contains(message, "not configured") || strings.Contains(message, "missing key") {
		return &ProviderError{Provider: provider, Class: ProviderMissingConfig, Message: "configuration is missing", TryFallback: true}
	}
	if strings.Contains(message, "unauthorized") || strings.Contains(message, "forbidden") || strings.Contains(message, "authentication") {
		return &ProviderError{Provider: provider, Class: ProviderInvalidAuth, Message: "authentication rejected", TryFallback: true}
	}
	if strings.Contains(message, "invalid response") || strings.Contains(message, "invalid character") || strings.Contains(message, "unexpected end") {
		return &ProviderError{Provider: provider, Class: ProviderInvalidResponse, Message: "provider returned malformed data", TryFallback: true, OpenCircuit: true}
	}
	if strings.Contains(message, "timeout") || strings.Contains(message, "deadline") {
		return &ProviderError{Provider: provider, Class: ProviderTimeout, Message: "request timed out", Retryable: true, TryFallback: true, OpenCircuit: true}
	}
	if strings.Contains(message, "connection") || strings.Contains(message, "eof") || strings.Contains(message, "temporarily") {
		return &ProviderError{Provider: provider, Class: ProviderNetwork, Message: "network request failed", Retryable: true, TryFallback: true, OpenCircuit: true}
	}
	return &ProviderError{Provider: provider, Class: ProviderPermanent, Message: "provider request failed", TryFallback: true}
}

func retryDelay(err *ProviderError) time.Duration {
	if err != nil && !err.RetryAt.IsZero() {
		if delay := time.Until(err.RetryAt); delay > 0 {
			return delay
		}
	}
	base := 300 * time.Millisecond
	return base + time.Duration(rand.Int63n(int64(300*time.Millisecond)))
}
