package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/routatic/proxy/internal/client"
	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/internal/core"
	"github.com/routatic/proxy/pkg/types"
)

// sessionCapturingServer asserts every request carries the expected
// x-opencode-session header and replies with handler's response.
func sessionCapturingServer(t *testing.T, got *string, respond func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = r.Header.Get("x-opencode-session")
		respond(w)
	}))
}

func sessionedContext() context.Context {
	return client.WithSessionContext(context.Background(), "sess-opencode-42")
}

func normalizedHi(model string, stream bool) *core.NormalizedRequest {
	return &core.NormalizedRequest{
		Model:    model,
		Stream:   stream,
		Messages: []core.NormalizedMessage{{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "Hi"}}}},
	}
}

func TestOpenCodeGoProvider_ExecuteOpenAI_SendsSessionHeader(t *testing.T) {
	var got string
	server := sessionCapturingServer(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.ChatCompletionResponse{
			ID: "cmpl-test", Model: "deepseek-v4-pro",
			Choices: []types.Choice{{Index: 0, Message: types.ChatMessage{Role: "assistant", Content: json.RawMessage(`"hi"`)}, FinishReason: "stop"}},
		})
	})
	defer server.Close()

	cfg := &config.Config{APIKey: "test-key", OpenCodeGo: config.OpenCodeGoConfig{BaseURL: server.URL}}
	p := NewOpenCodeGoProvider(config.NewAtomicConfig(cfg, ""))

	if _, err := p.Execute(sessionedContext(), normalizedHi("deepseek-v4-pro", false), config.ModelConfig{ModelID: "deepseek-v4-pro"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got != "sess-opencode-42" {
		t.Fatalf("x-opencode-session = %q, want sess-opencode-42", got)
	}
}

func TestOpenCodeGoProvider_StreamOpenAI_SendsSessionHeader(t *testing.T) {
	var got string
	server := sessionCapturingServer(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	defer server.Close()

	cfg := &config.Config{APIKey: "test-key", OpenCodeGo: config.OpenCodeGoConfig{BaseURL: server.URL}}
	p := NewOpenCodeGoProvider(config.NewAtomicConfig(cfg, ""))

	body, err := p.Stream(sessionedContext(), normalizedHi("deepseek-v4-pro", true), config.ModelConfig{ModelID: "deepseek-v4-pro"})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = body.Close() }()
	_, _ = io.ReadAll(body)

	if got != "sess-opencode-42" {
		t.Fatalf("x-opencode-session = %q, want sess-opencode-42", got)
	}
}

func TestOpenCodeGoProvider_ExecuteResponses_SendsSessionHeader(t *testing.T) {
	var got string
	server := sessionCapturingServer(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.ResponsesResponse{
			ID: "resp-test", Object: "response", Created: 1, Model: "gpt-5.6-luna",
			Output: []types.ResponsesOutput{{Type: "message", Role: "assistant", Content: []types.ResponsesContent{{Type: "output_text", Text: "hi"}}}},
		})
	})
	defer server.Close()

	cfg := &config.Config{APIKey: "test-key", OpenCodeGo: config.OpenCodeGoConfig{ResponsesBaseURL: server.URL}}
	p := NewOpenCodeGoProvider(config.NewAtomicConfig(cfg, ""))

	if _, err := p.Execute(sessionedContext(), normalizedHi("gpt-5.6-luna", false), config.ModelConfig{ModelID: "gpt-5.6-luna"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got != "sess-opencode-42" {
		t.Fatalf("x-opencode-session = %q, want sess-opencode-42", got)
	}
}

func TestOpenCodeGoProvider_ExecuteAnthropic_SendsSessionHeader(t *testing.T) {
	var got string
	server := sessionCapturingServer(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg-test","content":[{"type":"text","text":"hi"}]}`))
	})
	defer server.Close()

	cfg := &config.Config{APIKey: "test-key", OpenCodeGo: config.OpenCodeGoConfig{AnthropicBaseURL: server.URL}}
	p := NewOpenCodeGoProvider(config.NewAtomicConfig(cfg, ""))

	if _, err := p.Execute(sessionedContext(), normalizedHi("qwen3.5-plus", false), config.ModelConfig{ModelID: "qwen3.5-plus"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got != "sess-opencode-42" {
		t.Fatalf("x-opencode-session = %q, want sess-opencode-42", got)
	}
}

func TestOpenCodeGoProvider_StreamAnthropic_SendsSessionHeader(t *testing.T) {
	var got string
	server := sessionCapturingServer(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"}}\n\n"))
	})
	defer server.Close()

	cfg := &config.Config{APIKey: "test-key", OpenCodeGo: config.OpenCodeGoConfig{AnthropicBaseURL: server.URL}}
	p := NewOpenCodeGoProvider(config.NewAtomicConfig(cfg, ""))

	body, err := p.Stream(sessionedContext(), normalizedHi("qwen3.5-plus", true), config.ModelConfig{ModelID: "qwen3.5-plus"})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = body.Close() }()
	_, _ = io.ReadAll(body)

	if got != "sess-opencode-42" {
		t.Fatalf("x-opencode-session = %q, want sess-opencode-42", got)
	}
}

func TestOpenCodeGoProvider_NoSessionContext_OmitsHeader(t *testing.T) {
	var got string
	server := sessionCapturingServer(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.ChatCompletionResponse{
			ID: "cmpl-test", Model: "deepseek-v4-pro",
			Choices: []types.Choice{{Index: 0, Message: types.ChatMessage{Role: "assistant", Content: json.RawMessage(`"hi"`)}, FinishReason: "stop"}},
		})
	})
	defer server.Close()

	cfg := &config.Config{APIKey: "test-key", OpenCodeGo: config.OpenCodeGoConfig{BaseURL: server.URL}}
	p := NewOpenCodeGoProvider(config.NewAtomicConfig(cfg, ""))

	if _, err := p.Execute(context.Background(), normalizedHi("deepseek-v4-pro", false), config.ModelConfig{ModelID: "deepseek-v4-pro"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got != "" {
		t.Fatalf("x-opencode-session = %q, want empty", got)
	}
}

func TestOpenCodeZenProvider_ExecuteOpenAI_SendsSessionHeader(t *testing.T) {
	var got string
	server := sessionCapturingServer(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.ChatCompletionResponse{
			ID: "cmpl-test", Model: "deepseek-v4-flash-free",
			Choices: []types.Choice{{Index: 0, Message: types.ChatMessage{Role: "assistant", Content: json.RawMessage(`"hi"`)}, FinishReason: "stop"}},
		})
	})
	defer server.Close()

	cfg := &config.Config{APIKey: "test-key", OpenCodeZen: config.OpenCodeZenConfig{BaseURL: server.URL}}
	p := NewOpenCodeZenProvider(config.NewAtomicConfig(cfg, ""))

	if _, err := p.Execute(sessionedContext(), normalizedHi("deepseek-v4-flash-free", false), config.ModelConfig{ModelID: "deepseek-v4-flash-free"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got != "sess-opencode-42" {
		t.Fatalf("x-opencode-session = %q, want sess-opencode-42", got)
	}
}

func TestOpenCodeZenProvider_ExecuteAnthropic_SendsSessionHeader(t *testing.T) {
	var got string
	server := sessionCapturingServer(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg-test","content":[{"type":"text","text":"hi"}]}`))
	})
	defer server.Close()

	cfg := &config.Config{APIKey: "test-key", OpenCodeZen: config.OpenCodeZenConfig{AnthropicBaseURL: server.URL}}
	p := NewOpenCodeZenProvider(config.NewAtomicConfig(cfg, ""))

	if _, err := p.Execute(sessionedContext(), normalizedHi("claude-sonnet-4.5", false), config.ModelConfig{ModelID: "claude-sonnet-4.5"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got != "sess-opencode-42" {
		t.Fatalf("x-opencode-session = %q, want sess-opencode-42", got)
	}
}

func TestOpenCodeZenProvider_ExecuteResponses_SendsSessionHeader(t *testing.T) {
	var got string
	server := sessionCapturingServer(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.ResponsesResponse{
			ID: "resp-test", Object: "response", Created: 1, Model: "gpt-5.4",
			Output: []types.ResponsesOutput{{Type: "message", Role: "assistant", Content: []types.ResponsesContent{{Type: "output_text", Text: "hi"}}}},
		})
	})
	defer server.Close()

	cfg := &config.Config{APIKey: "test-key", OpenCodeZen: config.OpenCodeZenConfig{ResponsesBaseURL: server.URL}}
	p := NewOpenCodeZenProvider(config.NewAtomicConfig(cfg, ""))

	if _, err := p.Execute(sessionedContext(), normalizedHi("gpt-5.4", false), config.ModelConfig{ModelID: "gpt-5.4"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got != "sess-opencode-42" {
		t.Fatalf("x-opencode-session = %q, want sess-opencode-42", got)
	}
}

func TestOpenCodeZenProvider_ExecuteGemini_SendsSessionHeader(t *testing.T) {
	var got string
	server := sessionCapturingServer(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`))
	})
	defer server.Close()

	cfg := &config.Config{APIKey: "test-key", OpenCodeZen: config.OpenCodeZenConfig{GeminiBaseURL: server.URL}}
	p := NewOpenCodeZenProvider(config.NewAtomicConfig(cfg, ""))

	if _, err := p.Execute(sessionedContext(), normalizedHi("gemini-2.5-pro", false), config.ModelConfig{ModelID: "gemini-2.5-pro"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got != "sess-opencode-42" {
		t.Fatalf("x-opencode-session = %q, want sess-opencode-42", got)
	}
}

func TestOpenCodeZenProvider_StreamResponses_SendsSessionHeader(t *testing.T) {
	var got string
	server := sessionCapturingServer(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	defer server.Close()

	cfg := &config.Config{APIKey: "test-key", OpenCodeZen: config.OpenCodeZenConfig{ResponsesBaseURL: server.URL}}
	p := NewOpenCodeZenProvider(config.NewAtomicConfig(cfg, ""))

	body, err := p.Stream(sessionedContext(), normalizedHi("gpt-5.4", true), config.ModelConfig{ModelID: "gpt-5.4"})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = body.Close() }()
	_, _ = io.ReadAll(body)

	if got != "sess-opencode-42" {
		t.Fatalf("x-opencode-session = %q, want sess-opencode-42", got)
	}
}
