package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rick/internal/config"
	"rick/internal/tools"
)

func TestConnectAsyncDoesNotBlockCaller(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	manager := NewManager()
	registry := tools.NewRegistry()
	start := time.Now()
	manager.ConnectAsync(context.Background(), map[string]config.MCPServer{
		"slow": {Type: "remote", URL: server.URL},
	}, registry, nil)

	if elapsed := time.Since(start); elapsed >= 250*time.Millisecond {
		manager.Close()
		t.Fatalf("ConnectAsync took %s; it must return before dialing completes", elapsed)
	}
	manager.Close()
}
