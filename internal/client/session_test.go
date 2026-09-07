package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/pkg/types"
)

func TestWithSessionContext_RoundTrip(t *testing.T) {
	ctx := WithSessionContext(context.Background(), "sess-123")
	if got := SessionFromContext(ctx); got != "sess-123" {
		t.Fatalf("SessionFromContext() = %q, want %q", got, "sess-123")
	}
}

func TestWithSessionContext_EmptyIsNoop(t *testing.T) {
	ctx := WithSessionContext(context.Background(), "")
	if got := SessionFromContext(ctx); got != "" {
		t.Fatalf("SessionFromContext() = %q, want empty", got)
	}
}

func TestSessionFromContext_Absent(t *testing.T) {
	if got := SessionFromContext(context.Background()); got != "" {
		t.Fatalf("SessionFromContext() = %q, want empty", got)
	}
}

func TestSetSessionHeader_SetsWhenPresent(t *testing.T) {
	h := http.Header{}
	SetSessionHeader(WithSessionContext(context.Background(), "sess-abc"), h)
	if got := h.Get("x-opencode-session"); got != "sess-abc" {
		t.Fatalf("x-opencode-session = %q, want %q", got, "sess-abc")
	}
}

func TestSetSessionHeader_AbsentLeavesHeaderUnset(t *testing.T) {
	h := http.Header{}
	SetSessionHeader(context.Background(), h)
	if got := h.Get("x-opencode-session"); got != "" {
		t.Fatalf("x-opencode-session = %q, want empty", got)
	}
}

// newSessionTestClient points an OpenCodeClient at a capturing upstream.
func newSessionTestClient(t *testing.T, captured *string) *OpenCodeClient {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = r.Header.Get("x-opencode-session")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	cfg := &config.Config{
		APIKey: "test-key",
		OpenCodeGo: config.OpenCodeGoConfig{
			BaseURL:          upstream.URL,
			AnthropicBaseURL: upstream.URL,
		},
		OpenCodeZen: config.OpenCodeZenConfig{
			BaseURL:          upstream.URL,
			AnthropicBaseURL: upstream.URL,
			ResponsesBaseURL: upstream.URL,
			GeminiBaseURL:    upstream.URL,
		},
	}
	return NewOpenCodeClient(config.NewAtomicConfig(cfg, ""), nil)
}

func TestChatCompletion_SendsSessionHeader(t *testing.T) {
	var captured string
	c := newSessionTestClient(t, &captured)
	ctx := WithSessionContext(context.Background(), "sess-chat")

	resp, err := c.ChatCompletion(ctx, "kimi-k2.6", &types.ChatCompletionRequest{Model: "kimi-k2.6"}, config.ModelConfig{Provider: "opencode-go", ModelID: "kimi-k2.6"})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if captured != "sess-chat" {
		t.Fatalf("upstream x-opencode-session = %q, want %q", captured, "sess-chat")
	}
}

func TestSendAnthropicRequest_SendsSessionHeader(t *testing.T) {
	var captured string
	c := newSessionTestClient(t, &captured)
	ctx := WithSessionContext(context.Background(), "sess-anthropic")

	resp, err := c.SendAnthropicRequest(ctx, []byte(`{}`), false, config.ModelConfig{Provider: "opencode-go", ModelID: "minimax-m3"})
	if err != nil {
		t.Fatalf("SendAnthropicRequest() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if captured != "sess-anthropic" {
		t.Fatalf("upstream x-opencode-session = %q, want %q", captured, "sess-anthropic")
	}
}

func TestResponsesCompletion_SendsSessionHeader(t *testing.T) {
	var captured string
	c := newSessionTestClient(t, &captured)
	ctx := WithSessionContext(context.Background(), "sess-responses")

	resp, err := c.ResponsesCompletion(ctx, "grok-4.6", &types.ResponsesRequest{Model: "grok-4.6"}, config.ModelConfig{Provider: "opencode-go", ModelID: "grok-4.6"})
	if err != nil {
		t.Fatalf("ResponsesCompletion() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if captured != "sess-responses" {
		t.Fatalf("upstream x-opencode-session = %q, want %q", captured, "sess-responses")
	}
}

func TestGeminiCompletion_SendsSessionHeader(t *testing.T) {
	var captured string
	c := newSessionTestClient(t, &captured)
	ctx := WithSessionContext(context.Background(), "sess-gemini")

	resp, err := c.GeminiCompletion(ctx, "gemini-2.5-pro", &types.GeminiRequest{}, config.ModelConfig{Provider: "opencode-zen", ModelID: "gemini-2.5-pro"})
	if err != nil {
		t.Fatalf("GeminiCompletion() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.ReadAll(resp.Body)

	if captured != "sess-gemini" {
		t.Fatalf("upstream x-opencode-session = %q, want %q", captured, "sess-gemini")
	}
}

func TestChatCompletion_NoSessionContext_OmitsHeader(t *testing.T) {
	var captured string
	c := newSessionTestClient(t, &captured)

	resp, err := c.ChatCompletion(context.Background(), "kimi-k2.6", &types.ChatCompletionRequest{Model: "kimi-k2.6"}, config.ModelConfig{Provider: "opencode-go", ModelID: "kimi-k2.6"})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if captured != "" {
		t.Fatalf("upstream x-opencode-session = %q, want empty", captured)
	}
}
