package transformer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/routatic/proxy/pkg/types"
)

// mockResponseWriter implements http.ResponseWriter and http.Flusher for testing.
type mockResponseWriter struct {
	buf    bytes.Buffer
	header http.Header
	status int
}

func newMockResponseWriter() *mockResponseWriter {
	return &mockResponseWriter{
		header: make(http.Header),
	}
}

func (m *mockResponseWriter) Header() http.Header         { return m.header }
func (m *mockResponseWriter) Write(p []byte) (int, error) { return m.buf.Write(p) }
func (m *mockResponseWriter) WriteHeader(statusCode int)  { m.status = statusCode }
func (m *mockResponseWriter) Flush()                      {}

// sseLines builds raw SSE body from a list of data payloads.
func sseLines(lines ...string) io.ReadCloser {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteString("\n\n")
	}
	return io.NopCloser(strings.NewReader(b.String()))
}

// parseSSEEvents parses the raw response buffer into a slice of MessageEvent.
func parseSSEEvents(t *testing.T, raw string) []types.MessageEvent {
	t.Helper()
	var events []types.MessageEvent
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "" || data == "[DONE]" {
				continue
			}
			var ev types.MessageEvent
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Fatalf("unmarshal SSE event: %v (data: %s)", err, data)
			}
			events = append(events, ev)
		}
	}
	return events
}

func TestEmitMessageResponse_SynthesizesAnthropicSSE(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	resp := &types.MessageResponse{
		ID:         "msg_test",
		Type:       "message",
		Role:       "assistant",
		Model:      "qwen3.6-plus",
		StopReason: "end_turn",
		Content: []types.ContentBlock{
			{Type: "text", Text: "Vedo uno screenshot."},
		},
		Usage: types.Usage{InputTokens: 10, OutputTokens: 4},
	}

	if err := handler.EmitMessageResponse(w, resp); err != nil {
		t.Fatalf("EmitMessageResponse error: %v", err)
	}
	events := parseSSEEvents(t, w.buf.String())
	if len(events) != 6 {
		t.Fatalf("events = %d, want 6: %+v", len(events), events)
	}
	if events[0].Type != "message_start" {
		t.Fatalf("event[0] = %s, want message_start", events[0].Type)
	}
	if events[2].Type != "content_block_delta" || events[2].Delta.Type != "text_delta" {
		t.Fatalf("event[2] = %+v, want text_delta", events[2])
	}
	if got, want := events[2].Delta.Text, "Vedo uno screenshot."; got != want {
		t.Fatalf("text delta = %q, want %q", got, want)
	}
	if events[4].Type != "message_delta" || events[5].Type != "message_stop" {
		t.Fatalf("tail events = %+v %+v, want message_delta/message_stop", events[4], events[5])
	}
}

func TestProxyStream_ReasoningContentFastPath(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"reasoning_content":"Let me think"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":" step by step"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected: message_start, content_block_start, 2x thinking_delta,
	// signature_delta, content_block_stop, message_delta, message_stop.
	if len(events) != 8 {
		t.Fatalf("expected 8 events, got %d: %+v", len(events), events)
	}

	if events[0].Type != "message_start" {
		t.Errorf("event[0].Type = %q, want message_start", events[0].Type)
	}
	if events[1].Type != "content_block_start" {
		t.Errorf("event[1].Type = %q, want content_block_start", events[1].Type)
	}
	if events[1].ContentBlock == nil || events[1].ContentBlock.Type != "thinking" {
		t.Errorf("event[1].ContentBlock = %+v, want thinking block", events[1].ContentBlock)
	}
	if events[2].Type != "content_block_delta" {
		t.Errorf("event[2].Type = %q, want content_block_delta", events[2].Type)
	}
	if got := events[2].Delta.Type; got != "thinking_delta" {
		t.Errorf("event[2].Delta.Type = %q, want thinking_delta", got)
	}
	if got := events[2].Delta.Thinking; got != "Let me think" {
		t.Errorf("event[2].Delta.Thinking = %q, want %q", got, "Let me think")
	}
	if events[3].Type != "content_block_delta" {
		t.Errorf("event[3].Type = %q, want content_block_delta", events[3].Type)
	}
	if got := events[3].Delta.Thinking; got != " step by step" {
		t.Errorf("event[3].Delta.Thinking = %q, want %q", got, " step by step")
	}
	// The start block opens unsigned; the signature arrives as signature_delta
	// (event[4]) and accumulates onto the block.
	if got := events[1].ContentBlock.Signature; got != "" {
		t.Errorf("event[1].ContentBlock.Signature = %q, want empty", got)
	}
	if events[4].Type != "content_block_delta" || events[4].Delta == nil || events[4].Delta.Type != "signature_delta" {
		t.Errorf("event[4] = %+v, want signature_delta", events[4])
	}
	if got := events[4].Delta.Signature; got != thinkingSignaturePlaceholder {
		t.Errorf("event[4].Delta.Signature = %q, want %q", got, thinkingSignaturePlaceholder)
	}
	if events[5].Type != "content_block_stop" {
		t.Errorf("event[5].Type = %q, want content_block_stop", events[5].Type)
	}
	if events[6].Type != "message_delta" {
		t.Errorf("event[6].Type = %q, want message_delta", events[6].Type)
	}
	if events[7].Type != "message_stop" {
		t.Errorf("event[7].Type = %q, want message_stop", events[7].Type)
	}
}

func TestProxyStream_ReasoningSignatureAndMergedMessageDelta(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"reasoning_content":"Thinking..."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d: %+v", len(events), events)
	}
	if events[3].Delta == nil || events[3].Delta.Type != "signature_delta" || events[3].Delta.Signature == "" {
		t.Fatalf("event[3] = %+v, want non-empty signature_delta", events[3])
	}
	if events[4].Type != "content_block_stop" {
		t.Fatalf("event[4] = %+v, want content_block_stop", events[4])
	}
	if events[5].Type != "message_delta" || events[5].Delta == nil || events[5].Delta.StopReason != "end_turn" {
		t.Fatalf("event[5] = %+v, want terminal message_delta", events[5])
	}
	if events[5].Usage == nil || events[5].Usage.InputTokens != 100 || events[5].Usage.OutputTokens != 20 {
		t.Fatalf("event[5].Usage = %+v, want input=100 output=20", events[5].Usage)
	}
}

func TestProxyStream_ReasoningThenText(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"reasoning_content":"Thinking..."}}]}`,
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{"content":" world"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected: message_start, content_block_start(thinking, idx=0), thinking_delta,
	// signature_delta, content_block_stop(idx=0), content_block_start(text, idx=1),
	// text_delta x2, content_block_stop(idx=1), message_delta, message_stop.
	if len(events) != 11 {
		t.Fatalf("expected 11 events, got %d: %+v", len(events), events)
	}

	// Verify indexes
	if got := *events[1].Index; got != 0 {
		t.Errorf("thinking start index = %d, want 0", got)
	}
	if got := *events[4].Index; got != 0 {
		t.Errorf("thinking stop index = %d, want 0", got)
	}
	if got := *events[5].Index; got != 1 {
		t.Errorf("text start index = %d, want 1", got)
	}
	if got := *events[8].Index; got != 1 {
		t.Errorf("text stop index = %d, want 1", got)
	}

	// Verify types
	if events[1].ContentBlock == nil || events[1].ContentBlock.Type != "thinking" {
		t.Errorf("event[1].ContentBlock = %+v, want thinking block", events[1].ContentBlock)
	}
	if got := events[2].Delta.Type; got != "thinking_delta" {
		t.Errorf("event[2].Delta.Type = %q, want thinking_delta", got)
	}
	if events[3].Delta == nil || events[3].Delta.Type != "signature_delta" {
		t.Errorf("event[3] = %+v, want signature_delta", events[3])
	}
	if events[5].ContentBlock == nil || events[5].ContentBlock.Type != "text" {
		t.Errorf("event[5].ContentBlock = %+v, want text block", events[5].ContentBlock)
	}
	if got := events[6].Delta.Type; got != "text_delta" {
		t.Errorf("event[6].Delta.Type = %q, want text_delta", got)
	}
}

func TestProxyStream_TextOnlyStillWorks(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{"content":" world"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected: message_start, content_block_start, 2x content_block_delta, content_block_stop, message_delta, message_stop
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d: %+v", len(events), events)
	}

	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Errorf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta.Type != "text_delta" {
		t.Errorf("event[2] = %+v, want content_block_delta(text_delta)", events[2])
	}
	if events[2].Delta.Text != "Hello" {
		t.Errorf("event[2].Delta.Text = %q, want Hello", events[2].Delta.Text)
	}
}

func TestProxyStream_ContentArrayTextDelta(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":[{"type":"text","text":"Vedo uno screenshot."}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "qwen3.6-plus", ctx, 5*time.Second, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
	}
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Errorf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta.Type != "text_delta" {
		t.Errorf("event[2] = %+v, want content_block_delta(text_delta)", events[2])
	}
	if got, want := events[2].Delta.Text, "Vedo uno screenshot."; got != want {
		t.Errorf("event[2].Delta.Text = %q, want %q", got, want)
	}
}

func TestProxyStream_UsageOnlyChunk(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":123,"completion_tokens":45,"total_tokens":168,"prompt_cache_hit_tokens":100,"prompt_cache_miss_tokens":23}}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-pro", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	var usage *types.Usage
	for _, event := range events {
		if event.Usage != nil {
			usage = event.Usage
		}
	}
	if usage == nil {
		t.Fatalf("no usage event found in stream: %+v", events)
		return
	}
	// Per Anthropic spec, input_tokens excludes cache reads AND cache
	// creations. Upstream prompt_tokens=123 split as 100 hit + 23 miss
	// means everything was accounted for by the cache → input_tokens = 0.
	if got, want := usage.InputTokens, 0; got != want {
		t.Fatalf("InputTokens = %d, want %d", got, want)
	}
	if got, want := usage.OutputTokens, 45; got != want {
		t.Fatalf("OutputTokens = %d, want %d", got, want)
	}
	if got, want := usage.CacheReadInputTokens, 100; got != want {
		t.Fatalf("CacheReadInputTokens = %d, want %d", got, want)
	}
	if got, want := usage.CacheCreationInputTokens, 23; got != want {
		t.Fatalf("CacheCreationInputTokens = %d, want %d", got, want)
	}
}

// TestProxyStream_NestedCacheTokens covers providers (e.g. GLM/Zhipu) that
// report cache accounting via the standard OpenAI prompt_tokens_details
// shape instead of DeepSeek's flat prompt_cache_hit_tokens/miss_tokens
// fields. Before this fix, PromptTokensDetails was never parsed in the
// streaming path either, so these providers' cache hits reported as zero.
func TestProxyStream_NestedCacheTokens(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":33487,"completion_tokens":57,"total_tokens":33544,"prompt_tokens_details":{"cached_tokens":28224}}}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "glm-5.2", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	var usage *types.Usage
	for _, event := range events {
		if event.Usage != nil {
			usage = event.Usage
		}
	}
	if usage == nil {
		t.Fatalf("no usage event found in stream: %+v", events)
		return
	}
	if got, want := usage.CacheReadInputTokens, 28224; got != want {
		t.Fatalf("CacheReadInputTokens = %d, want %d", got, want)
	}
	if got, want := usage.CacheCreationInputTokens, 0; got != want {
		t.Fatalf("CacheCreationInputTokens = %d, want %d", got, want)
	}
	if got, want := usage.InputTokens, 33487-28224; got != want {
		t.Fatalf("InputTokens = %d, want %d", got, want)
	}
}

// TestProxyStream_PartialCacheTokensStreaming covers the case where
// hit + miss < prompt_tokens in a streaming context. The leftover tokens
// should map to input_tokens.
func TestProxyStream_PartialCacheTokensStreaming(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Partial cache"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105,"prompt_cache_hit_tokens":60,"prompt_cache_miss_tokens":30}}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-pro", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	var usage *types.Usage
	for _, event := range events {
		if event.Usage != nil {
			usage = event.Usage
		}
	}
	if usage == nil {
		t.Fatalf("no usage event found in stream: %+v", events)
		return
	}
	// 100 - 60 - 30 = 10 tokens are neither cached nor newly cached.
	if got, want := usage.InputTokens, 10; got != want {
		t.Errorf("InputTokens = %d, want %d", got, want)
	}
	if got, want := usage.CacheReadInputTokens, 60; got != want {
		t.Errorf("CacheReadInputTokens = %d, want %d", got, want)
	}
	if got, want := usage.CacheCreationInputTokens, 30; got != want {
		t.Errorf("CacheCreationInputTokens = %d, want %d", got, want)
	}
}

// TestProxyStream_NoDuplicateMessageDelta verifies that finish_reason and usage
// arriving in separate chunks are merged into exactly one message_delta.
func TestProxyStream_NoDuplicateMessageDelta(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-pro", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	var messageDeltas []types.MessageEvent
	for _, ev := range events {
		if ev.Type == "message_delta" {
			messageDeltas = append(messageDeltas, ev)
		}
	}

	if len(messageDeltas) != 1 {
		t.Fatalf("expected exactly 1 message_delta, got %d: %+v", len(messageDeltas), messageDeltas)
	}
	if messageDeltas[0].Delta == nil || messageDeltas[0].Delta.StopReason != "end_turn" {
		t.Fatalf("message_delta = %+v, want stop_reason=end_turn", messageDeltas[0])
	}

	totalUsage := messageDeltas[0].Usage
	if totalUsage == nil {
		t.Fatalf("no usage found in stream: %+v", events)
		return
	}
	if got, want := totalUsage.InputTokens, 100; got != want {
		t.Errorf("InputTokens = %d, want %d", got, want)
	}
}

func TestProxyStream_ReasoningJSONFallback(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	// This payload does NOT match the fast-path string pattern because of extra whitespace,
	// forcing the JSON fallback path.
	body := sseLines(
		fmt.Sprintf(`{"choices":[{"delta":%s}]}`, mustJSON(t, types.ChatMessage{ReasoningContent: strPtr("Reasoning via JSON")})),
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected: message_start, thinking start/delta/signature/stop, message_delta, message_stop.
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d: %+v", len(events), events)
	}

	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "thinking" {
		t.Errorf("event[1] = %+v, want content_block_start(thinking)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta.Type != "thinking_delta" {
		t.Errorf("event[2] = %+v, want content_block_delta(thinking_delta)", events[2])
	}
	if events[2].Delta.Thinking != "Reasoning via JSON" {
		t.Errorf("event[2].Delta.Thinking = %q, want %q", events[2].Delta.Thinking, "Reasoning via JSON")
	}
	if events[3].Delta == nil || events[3].Delta.Type != "signature_delta" {
		t.Errorf("event[3] = %+v, want signature_delta", events[3])
	}
}

func TestProxyStream_EmptyReasoningContentSkipped(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		fmt.Sprintf(`{"choices":[{"delta":%s}]}`, mustJSON(t, types.ChatMessage{ReasoningContent: strPtr("")})),
		`{"choices":[{"delta":{"content":"Only text"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Empty reasoning should be skipped; only one text chunk -> 6 events total
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
	}

	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Errorf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if *events[1].Index != 0 {
		t.Errorf("text start index = %d, want 0", *events[1].Index)
	}
}

func TestProxyStream_ReasoningAndContentInSameChunk(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		fmt.Sprintf(`{"choices":[{"delta":%s}]}`, mustJSON(t, types.ChatMessage{
			ReasoningContent: strPtr("Thinking..."),
			Content:          contentText("Hello"),
		})),
		`{"choices":[{"delta":{"content":" world"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// message_start + thinking_start + thinking_delta + signature_delta + thinking_stop +
	// text_start + text_delta("Hello") + text_delta(" world") + text_stop +
	// message_delta + message_stop = 11
	if len(events) != 11 {
		t.Fatalf("expected 11 events, got %d: %+v", len(events), events)
	}

	// Block 0: thinking
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "thinking" {
		t.Errorf("event[1] = %+v, want content_block_start(thinking)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta.Type != "thinking_delta" {
		t.Errorf("event[2] = %+v, want content_block_delta(thinking_delta)", events[2])
	}
	if events[2].Delta.Thinking != "Thinking..." {
		t.Errorf("event[2].Delta.Thinking = %q, want %q", events[2].Delta.Thinking, "Thinking...")
	}
	if events[3].Delta == nil || events[3].Delta.Type != "signature_delta" {
		t.Errorf("event[3] = %+v, want signature_delta", events[3])
	}
	if events[4].Type != "content_block_stop" {
		t.Errorf("event[4].Type = %q, want content_block_stop", events[4].Type)
	}

	// Block 1: text
	if events[5].Type != "content_block_start" || events[5].ContentBlock == nil || events[5].ContentBlock.Type != "text" {
		t.Errorf("event[5] = %+v, want content_block_start(text)", events[5])
	}
	if events[6].Type != "content_block_delta" || events[6].Delta.Type != "text_delta" {
		t.Errorf("event[6] = %+v, want content_block_delta(text_delta)", events[6])
	}
	if events[6].Delta.Text != "Hello" {
		t.Errorf("event[6].Delta.Text = %q, want Hello", events[6].Delta.Text)
	}
	if events[7].Type != "content_block_delta" || events[7].Delta.Type != "text_delta" {
		t.Errorf("event[7] = %+v, want content_block_delta(text_delta)", events[7])
	}
	if events[7].Delta.Text != " world" {
		t.Errorf("event[7].Delta.Text = %q, want \" world\"", events[7].Delta.Text)
	}
	if events[8].Type != "content_block_stop" {
		t.Errorf("event[8].Type = %q, want content_block_stop", events[8].Type)
	}
}

// TestProxyStream_ReasoningBeforeContentFastPathRegression ensures that when
// a provider sends reasoning_content BEFORE content in the same delta (with no
// role field), the fast path for content is skipped and reasoning_content is
// not silently dropped. If it were dropped, the next turn would fail on
// DeepSeek with "reasoning_content must be passed back".
func TestProxyStream_ReasoningBeforeContentFastPathRegression(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	// Hand-crafted JSON: reasoning_content appears before content, no role field.
	// Before the fix, the fast path matched "delta":{"content":" and returned
	// early, discarding reasoning_content entirely.
	body := sseLines(
		`{"choices":[{"delta":{"reasoning_content":"Thinking...","content":"Hello"}}]}`,
		`{"choices":[{"delta":{"content":" world"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-flash", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// message_start + thinking_start + thinking_delta + signature_delta + thinking_stop +
	// text_start + text_delta("Hello") + text_delta(" world") + text_stop +
	// message_delta + message_stop = 11
	if len(events) != 11 {
		t.Fatalf("expected 11 events, got %d: %+v", len(events), events)
	}

	// Block 0: thinking (must NOT be lost)
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "thinking" {
		t.Errorf("event[1] = %+v, want content_block_start(thinking)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta.Type != "thinking_delta" {
		t.Errorf("event[2] = %+v, want content_block_delta(thinking_delta)", events[2])
	}
	if events[2].Delta.Thinking != "Thinking..." {
		t.Errorf("event[2].Delta.Thinking = %q, want %q", events[2].Delta.Thinking, "Thinking...")
	}
	if events[3].Delta == nil || events[3].Delta.Type != "signature_delta" {
		t.Errorf("event[3] = %+v, want signature_delta", events[3])
	}

	// Block 1: text
	if events[5].Type != "content_block_start" || events[5].ContentBlock == nil || events[5].ContentBlock.Type != "text" {
		t.Errorf("event[5] = %+v, want content_block_start(text)", events[5])
	}
	if events[6].Delta.Text != "Hello" {
		t.Errorf("event[6].Delta.Text = %q, want Hello", events[6].Delta.Text)
	}
}

// TestProxyStream_ToolCallFinishReasonWithUsage verifies that when finish_reason
// arrives (fast path) followed by a usage-only chunk, tool blocks are closed
// exactly once — no duplicate content_block_stop from EOF cleanup.
func TestProxyStream_ToolCallFinishReasonWithUsage(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"toolu_a","type":"function","function":{"name":"fn_a","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"toolu_b","type":"function","function":{"name":"fn_b","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"x\":1}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"y\":2}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_use"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Count content_block_stop events — should be exactly 2 (one per tool)
	var stopCount int
	for _, ev := range events {
		if ev.Type == "content_block_stop" {
			stopCount++
		}
	}
	if stopCount != 2 {
		t.Fatalf("expected 2 content_block_stop events, got %d: %+v", stopCount, events)
	}

	// Verify usage is present
	var hasUsage bool
	for _, ev := range events {
		if ev.Usage != nil {
			hasUsage = true
		}
	}
	if !hasUsage {
		t.Error("expected usage in stream, found none")
	}
}

// TestProxyStream_SingleToolCall verifies a single tool call streamed
// incrementally produces exactly one content_block_start, argument deltas,
// and a content_block_stop.
func TestProxyStream_SingleToolCall(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"toolu_abc","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loc"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\":\"NYC\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_use"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected: message_start, tool_start(idx=0), 2x input_json_delta,
	// tool_stop(idx=0), message_delta, message_stop = 7
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d: %+v", len(events), events)
	}

	// Verify tool_use block start
	if events[1].Type != "content_block_start" {
		t.Errorf("event[1].Type = %q, want content_block_start", events[1].Type)
	}
	if events[1].ContentBlock == nil || events[1].ContentBlock.Type != "tool_use" {
		t.Errorf("event[1].ContentBlock = %+v, want tool_use", events[1].ContentBlock)
	}
	if events[1].ContentBlock.ID != "toolu_abc" {
		t.Errorf("event[1].ContentBlock.ID = %q, want toolu_abc", events[1].ContentBlock.ID)
	}
	if events[1].ContentBlock.Name != "get_weather" {
		t.Errorf("event[1].ContentBlock.Name = %q, want get_weather", events[1].ContentBlock.Name)
	}

	// Verify argument deltas
	if events[2].Delta == nil || events[2].Delta.Type != "input_json_delta" {
		t.Errorf("event[2] = %+v, want input_json_delta", events[2])
	}
	if events[2].Delta.PartialJSON != `{"loc` {
		t.Errorf("event[2].Delta.PartialJSON = %q, want %q", events[2].Delta.PartialJSON, `{"loc`)
	}
	if events[3].Delta == nil || events[3].Delta.Type != "input_json_delta" {
		t.Errorf("event[3] = %+v, want input_json_delta", events[3])
	}

	// Verify tool block stop
	if events[4].Type != "content_block_stop" {
		t.Errorf("event[4].Type = %q, want content_block_stop", events[4].Type)
	}

	// Verify stop reason
	if events[5].Type != "message_delta" {
		t.Errorf("event[5].Type = %q, want message_delta", events[5].Type)
	}
	if events[5].Delta == nil || events[5].Delta.StopReason != "tool_use" {
		t.Errorf("event[5].Delta.StopReason = %q, want tool_use", events[5].Delta.StopReason)
	}
	if events[6].Type != "message_stop" {
		t.Errorf("event[6].Type = %q, want message_stop", events[6].Type)
	}
}

// TestProxyStream_MultipleParallelToolCalls verifies that two concurrent tool
// calls produce two content_block_start events, each with their own argument
// deltas, and that content_block_stop events are emitted in ascending index
// order (not random map iteration order).
func TestProxyStream_MultipleParallelToolCalls(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	// Two tool calls: index 0 and index 1, interleaved as OpenAI sends them
	body := sseLines(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"toolu_1","type":"function","function":{"name":"search","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"toolu_2","type":"function","function":{"name":"lookup","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"id"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"uery\":\"go\"}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\":\"42\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_use"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Count content_block_start events (should be exactly 2)
	var startEvents []types.MessageEvent
	for _, ev := range events {
		if ev.Type == "content_block_start" {
			startEvents = append(startEvents, ev)
		}
	}
	if len(startEvents) != 2 {
		t.Fatalf("expected 2 content_block_start events, got %d", len(startEvents))
	}

	// Both should be tool_use blocks
	for i, se := range startEvents {
		if se.ContentBlock == nil || se.ContentBlock.Type != "tool_use" {
			t.Errorf("start event[%d].ContentBlock = %+v, want tool_use", i, se.ContentBlock)
		}
	}
	if startEvents[0].ContentBlock.Name != "search" {
		t.Errorf("first tool name = %q, want search", startEvents[0].ContentBlock.Name)
	}
	if startEvents[1].ContentBlock.Name != "lookup" {
		t.Errorf("second tool name = %q, want lookup", startEvents[1].ContentBlock.Name)
	}

	// Count content_block_stop events (should be exactly 2)
	var stopIndices []int
	for _, ev := range events {
		if ev.Type == "content_block_stop" && ev.Index != nil {
			stopIndices = append(stopIndices, *ev.Index)
		}
	}
	if len(stopIndices) != 2 {
		t.Fatalf("expected 2 content_block_stop events, got %d", len(stopIndices))
	}
	// Verify ascending order
	if stopIndices[0] >= stopIndices[1] {
		t.Errorf("stop indices not ascending: %v", stopIndices)
	}
}

// TestProxyStream_ToolCallGhostChunk verifies that a ghost chunk (tool call
// index with empty name) is ignored and does not produce a content_block_start.
func TestProxyStream_ToolCallGhostChunk(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"toolu_a","type":"function","function":{"name":"real_func","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"x\":1}"}}]}}]}`,
		// Ghost chunk: index 0 recycled but no name
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":""}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_use"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Should have exactly 1 content_block_start for the real tool call
	var startEvents []types.MessageEvent
	for _, ev := range events {
		if ev.Type == "content_block_start" {
			startEvents = append(startEvents, ev)
		}
	}
	if len(startEvents) != 1 {
		t.Fatalf("expected 1 content_block_start, got %d: %+v", len(startEvents), startEvents)
	}
}

// TestProxyStream_MixedTextAndToolCall verifies a response that starts with
// text content and then transitions to a tool call.
func TestProxyStream_MixedTextAndToolCall(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Let me check that for you."}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"toolu_x","type":"function","function":{"name":"get_data","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"id\":1}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_use"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Verify text block at index 0
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Errorf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if *events[1].Index != 0 {
		t.Errorf("text start index = %d, want 0", *events[1].Index)
	}

	// Verify tool_use block at index 1
	if events[4].Type != "content_block_start" || events[4].ContentBlock == nil || events[4].ContentBlock.Type != "tool_use" {
		t.Errorf("event[4] = %+v, want content_block_start(tool_use)", events[4])
	}
	if *events[4].Index != 1 {
		t.Errorf("tool start index = %d, want 1", *events[4].Index)
	}
	if events[4].ContentBlock.Name != "get_data" {
		t.Errorf("tool name = %q, want get_data", events[4].ContentBlock.Name)
	}

	var stopIndices []int
	for _, ev := range events {
		if ev.Type == "content_block_stop" && ev.Index != nil {
			stopIndices = append(stopIndices, *ev.Index)
		}
	}
	if len(stopIndices) != 2 {
		t.Fatalf("expected text and tool blocks to be stopped exactly once, got %v", stopIndices)
	}
	if stopIndices[0] != 0 || stopIndices[1] != 1 {
		t.Fatalf("stop indices = %v, want [0 1]", stopIndices)
	}
}

// TestProxyStream_MixedReasoningAndToolCall verifies that a reasoning block is
// closed before the stream starts a tool_use block.
func TestProxyStream_MixedReasoningAndToolCall(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		fmt.Sprintf(`{"choices":[{"delta":%s}]}`, mustJSON(t, types.ChatMessage{ReasoningContent: strPtr("Need a tool.")})),
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"toolu_x","type":"function","function":{"name":"get_data","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"id\":1}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_use"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "thinking" {
		t.Errorf("event[1] = %+v, want content_block_start(thinking)", events[1])
	}
	if events[3].Delta == nil || events[3].Delta.Type != "signature_delta" {
		t.Errorf("event[3] = %+v, want signature_delta", events[3])
	}
	if events[4].Type != "content_block_stop" || events[4].Index == nil || *events[4].Index != 0 {
		t.Errorf("event[4] = %+v, want content_block_stop(index=0)", events[4])
	}
	if events[5].Type != "content_block_start" || events[5].ContentBlock == nil || events[5].ContentBlock.Type != "tool_use" {
		t.Errorf("event[5] = %+v, want content_block_start(tool_use)", events[5])
	}

	var stopIndices []int
	for _, ev := range events {
		if ev.Type == "content_block_stop" && ev.Index != nil {
			stopIndices = append(stopIndices, *ev.Index)
		}
	}
	if len(stopIndices) != 2 {
		t.Fatalf("expected reasoning and tool blocks to be stopped exactly once, got %v", stopIndices)
	}
	if stopIndices[0] != 0 || stopIndices[1] != 1 {
		t.Fatalf("stop indices = %v, want [0 1]", stopIndices)
	}
}

// TestProxyStream_ToolCallFinishReasonFastPath verifies that when a tool call
// finish reason arrives in a chunk matching the fast path, the stop reason
// is correctly set to "tool_use".
func TestProxyStream_ToolCallFinishReasonFastPath(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"toolu_xyz","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected events: message_start, content_block_start, content_block_stop, message_delta, message_stop = 5
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d: %+v", len(events), events)
	}

	// Verify message_delta has StopReason set to tool_use
	msgDelta := events[3]
	if msgDelta.Type != "message_delta" {
		t.Errorf("expected event[3] to be message_delta, got %q", msgDelta.Type)
	}
	if msgDelta.Delta == nil || msgDelta.Delta.StopReason != "tool_use" {
		t.Errorf("stop reason = %q, want tool_use", msgDelta.Delta.StopReason)
	}
}

// TestProxyStream_ContentAndFinishReasonInSameChunk verifies that when a chunk
// contains both a text content delta and a finish reason, both are handled.
func TestProxyStream_ContentAndFinishReasonInSameChunk(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Hello"},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected events:
	// 0: message_start
	// 1: content_block_start (index 0, type text)
	// 2: content_block_delta (index 0, text "Hello")
	// 3: content_block_stop (index 0)
	// 4: message_delta (stop_reason: end_turn)
	// 5: message_stop
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
	}

	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Errorf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta == nil || events[2].Delta.Text != "Hello" {
		t.Errorf("event[2] = %+v, want content_block_delta(Hello)", events[2])
	}
	if events[3].Type != "content_block_stop" || events[3].Index == nil || *events[3].Index != 0 {
		t.Errorf("event[3] = %+v, want content_block_stop(0)", events[3])
	}
	if events[4].Type != "message_delta" || events[4].Delta == nil || events[4].Delta.StopReason != "end_turn" {
		t.Errorf("event[4] = %+v, want message_delta(end_turn)", events[4])
	}
}

// TestProxyStream_ToolCallAndFinishReasonInSameChunk verifies that when a chunk
// contains both a tool call arguments delta and a finish reason, both are handled.
func TestProxyStream_ToolCallAndFinishReasonInSameChunk(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"toolu_xyz","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loc\":\"Beijing\"}"}}]},"finish_reason":"tool_calls"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected events:
	// 0: message_start
	// 1: content_block_start (index 1, type tool_use)
	// 2: content_block_delta (index 0, partial_json "{\"loc\":\"Beijing\"}")
	// 3: content_block_stop (index 0)
	// 4: message_delta (stop_reason: tool_use)
	// 5: message_stop
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
	}

	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "tool_use" {
		t.Errorf("event[1] = %+v, want content_block_start(tool_use)", events[1])
	}
	if events[2].Type != "content_block_delta" || events[2].Delta == nil || events[2].Delta.PartialJSON != `{"loc":"Beijing"}` {
		t.Errorf("event[2] = %+v, want content_block_delta", events[2])
	}
	if events[3].Type != "content_block_stop" || events[3].Index == nil || *events[3].Index != 0 {
		t.Errorf("event[3] = %+v, want content_block_stop(0)", events[3])
	}
	if events[4].Type != "message_delta" || events[4].Delta == nil || events[4].Delta.StopReason != "tool_use" {
		t.Errorf("event[4] = %+v, want message_delta(tool_use)", events[4])
	}
}

func TestProxyStream_NoUsageFallback(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "qwen3.6-plus", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	var messageDeltaEvent *types.MessageEvent
	for _, event := range events {
		if event.Type == "message_delta" {
			messageDeltaEvent = &event
			break
		}
	}

	if messageDeltaEvent == nil {
		t.Fatalf("expected message_delta event, got none: %+v", events)
		return
	}

	if messageDeltaEvent.Usage == nil {
		t.Fatal("expected message_delta event to have non-nil Usage, but it was nil")
		return
	}

	if messageDeltaEvent.Usage.InputTokens != 0 || messageDeltaEvent.Usage.OutputTokens != 0 {
		t.Errorf("Usage = %+v, want InputTokens: 0, OutputTokens: 0", messageDeltaEvent.Usage)
	}
}

func TestProxyStream_NoFinishReasonFallback(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "qwen3.6-plus", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	// Expected events:
	// 0: message_start
	// 1: content_block_start
	// 2: content_block_delta
	// 3: content_block_stop
	// 4: message_delta (fallback stop_reason: end_turn)
	// 5: message_stop
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
	}

	if events[4].Type != "message_delta" || events[4].Delta == nil || events[4].Delta.StopReason != "end_turn" {
		t.Errorf("event[4] = %+v, want message_delta(end_turn)", events[4])
	}
}

// TestProxyStream_EOFFallbackStopReasonToolUse verifies that when the stream
// ends mid-tool-call (no finish_reason), the EOF fallback sets stop_reason
// to "tool_use" rather than "end_turn".
func TestProxyStream_EOFFallbackStopReasonToolUse(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"toolu_abc","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"/tmp/test\"}"}}]}}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	var msgDelta *types.MessageEvent
	for i := range events {
		if events[i].Type == "message_delta" {
			msgDelta = &events[i]
			break
		}
	}
	if msgDelta == nil {
		t.Fatalf("expected message_delta event, got none: %+v", events)
		return
	}
	if msgDelta.Delta == nil || msgDelta.Delta.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use (stream ended mid-tool-call)", msgDelta.Delta.StopReason)
	}
}

// TestProxyStream_ToolUseFirstContentBlock verifies that when the first
// assistant output is a direct tool call (no preceding text or reasoning),
// the tool_use block is emitted at index 0 per Anthropic SSE spec.
func TestProxyStream_ToolUseFirstContentBlock(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"toolu_abc","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"/tmp/x\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_use"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// 0: message_start
	// 1: content_block_start (index 0, type tool_use) — first content block
	// 2: content_block_delta (index 0)
	// 3: content_block_stop (index 0)
	// 4: message_delta
	// 5: message_stop
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
	}

	if events[1].Type != "content_block_start" {
		t.Fatalf("event[1].Type = %q, want content_block_start", events[1].Type)
	}
	if events[1].ContentBlock == nil || events[1].ContentBlock.Type != "tool_use" {
		t.Fatalf("event[1].ContentBlock = %+v, want tool_use", events[1].ContentBlock)
	}
	if events[1].Index == nil || *events[1].Index != 0 {
		t.Fatalf("tool_use content_block_start index = %v, want 0", events[1].Index)
	}

	if events[3].Type != "content_block_stop" || events[3].Index == nil || *events[3].Index != 0 {
		t.Fatalf("tool_use content_block_stop index = %v, want 0", events[3].Index)
	}
	if events[4].Type != "message_delta" || events[4].Delta == nil || events[4].Delta.StopReason != "tool_use" {
		t.Errorf("event[4] = %+v, want message_delta(tool_use)", events[4])
	}
}

// helpers

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func strPtr(s string) *string { return &s }

// bodyThenErrReader yields body on the first Read, then fails with err. It
// simulates an upstream that stalls or drops after delivering a complete
// response (finish_reason already sent).
type bodyThenErrReader struct {
	body []byte
	err  error
	sent bool
}

func (r *bodyThenErrReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.body), nil
	}
	return 0, r.err
}

func (r *bodyThenErrReader) Close() error { return nil }

func TestProxyStream_TerminalEventsSurviveUpstreamErrorAfterFinishReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "read error", err: io.ErrUnexpectedEOF},
		{name: "idle cancel", err: context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewStreamHandler()
			w := newMockResponseWriter()
			body := "data: " + `{"choices":[{"delta":{"reasoning_content":"Thinking..."}}]}` + "\n\n" +
				"data: " + `{"choices":[{"delta":{"content":"42"},"finish_reason":"stop"}]}` + "\n\n"

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			err := handler.ProxyStream(w, &bodyThenErrReader{body: []byte(body), err: tt.err}, "kimi-k2.6", ctx, 0, cancel)
			if err != nil {
				t.Fatalf("ProxyStream() error = %v, want nil (response already complete)", err)
			}

			events := parseSSEEvents(t, w.buf.String())
			if len(events) < 2 {
				t.Fatalf("expected terminal events, got %+v", events)
			}
			last := events[len(events)-1]
			penultimate := events[len(events)-2]
			if last.Type != "message_stop" {
				t.Errorf("last event = %q, want message_stop", last.Type)
			}
			if penultimate.Type != "message_delta" {
				t.Fatalf("penultimate event = %q, want message_delta", penultimate.Type)
			}
			if penultimate.Delta == nil || penultimate.Delta.StopReason != "end_turn" {
				t.Errorf("message_delta.Delta = %+v, want stop_reason end_turn", penultimate.Delta)
			}
		})
	}
}

func TestProxyStream_UpstreamErrorBeforeFinishReasonStillFails(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := "data: " + `{"choices":[{"delta":{"content":"partial"}}]}` + "\n\n"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := handler.ProxyStream(w, &bodyThenErrReader{body: []byte(body), err: io.ErrUnexpectedEOF}, "kimi-k2.6", ctx, 0, cancel)
	if err == nil {
		t.Fatal("ProxyStream() error = nil, want error so the handler can fall back")
	}
}

func TestProxyStream_ThinkingBlockStartHasEmptySignature(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"reasoning_content":"Thinking..."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil {
		t.Fatalf("event[1] = %+v, want content_block_start", events[1])
	}
	// Signature deltas accumulate onto the start block, so the start block must
	// not pre-seed the placeholder or the client sees it twice.
	if got := events[1].ContentBlock.Signature; got != "" {
		t.Errorf("content_block_start signature = %q, want empty", got)
	}
}

func TestEmitMessageResponse_ThinkingSignatureOnlyInDelta(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	resp := &types.MessageResponse{
		ID:   "msg_1",
		Type: "message",
		Role: "assistant",
		Content: []types.ContentBlock{
			{Type: "thinking", Thinking: "hmm", Signature: "upstream-sig"},
		},
		Model: "kimi-k2.6",
	}

	if err := handler.EmitMessageResponse(w, resp); err != nil {
		t.Fatalf("EmitMessageResponse error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	var start, sigDelta *types.MessageEvent
	for i := range events {
		switch {
		case events[i].Type == "content_block_start":
			start = &events[i]
		case events[i].Type == "content_block_delta" && events[i].Delta != nil && events[i].Delta.Type == "signature_delta":
			sigDelta = &events[i]
		}
	}
	if start == nil || start.ContentBlock == nil {
		t.Fatalf("no content_block_start in %+v", events)
	}
	if got := start.ContentBlock.Signature; got != "" {
		t.Errorf("content_block_start signature = %q, want empty", got)
	}
	if sigDelta == nil {
		t.Fatalf("no signature_delta in %+v", events)
	}
	if got := sigDelta.Delta.Signature; got != "upstream-sig" {
		t.Errorf("signature_delta = %q, want the upstream signature", got)
	}
}

// --- ProxyResponsesStream: OpenAI Responses SSE → Anthropic SSE ---

// eventsOfType returns the indices of events matching t.
func eventsOfType(events []types.MessageEvent, typ string) []int {
	var idxs []int
	for i, ev := range events {
		if ev.Type == typ {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

// contentBlockStopIndices returns the block indices of all content_block_stop
// events, in emission order.
func contentBlockStopIndices(events []types.MessageEvent) []int {
	var idxs []int
	for _, ev := range events {
		if ev.Type == "content_block_stop" && ev.Index != nil {
			idxs = append(idxs, *ev.Index)
		}
	}
	return idxs
}

// concatenatedPartialJSON joins all input_json_delta partial_json fragments
// targeted at the given content block index.
func concatenatedPartialJSON(events []types.MessageEvent, blockIdx int) string {
	var b strings.Builder
	for _, ev := range events {
		if ev.Type == "content_block_delta" && ev.Index != nil && *ev.Index == blockIdx &&
			ev.Delta != nil && ev.Delta.Type == "input_json_delta" {
			b.WriteString(ev.Delta.PartialJSON)
		}
	}
	return b.String()
}

// TestProxyResponsesStream_ToolOnlyStream reproduces a grok-4.6 tool-call
// capture (reasoning summary + function_call, no text) and verifies the tool
// call reaches the client as a tool_use block with reconstructed arguments.
func TestProxyResponsesStream_ToolOnlyStream(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"type":"response.created","sequence_number":0,"response":{"id":"c831","status":"in_progress","usage":null}}`,
		`{"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"in_progress","summary":[]}}`,
		`{"type":"response.reasoning_summary_text.delta","sequence_number":4,"output_index":0,"summary_index":0,"item_id":"rs_1","delta":"The user asked"}`,
		`{"type":"response.output_item.done","sequence_number":19,"output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"The user asked"}]}}`,
		`{"type":"response.output_item.added","sequence_number":20,"output_index":1,"item":{"id":"fc_1","type":"function_call","status":"in_progress","name":"get_weather","call_id":"call-912fb0d5","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":21,"output_index":1,"item_id":"fc_1","delta":"{\"city\":"}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":22,"output_index":1,"item_id":"fc_1","delta":"\"Paris\"}"}`,
		`{"type":"response.function_call_arguments.done","sequence_number":23,"output_index":1,"item_id":"fc_1","arguments":"{\"city\":\"Paris\"}","name":"get_weather"}`,
		`{"type":"response.output_item.done","sequence_number":24,"output_index":1,"item":{"id":"fc_1","type":"function_call","status":"completed","name":"get_weather","call_id":"call-912fb0d5","arguments":"{\"city\":\"Paris\"}"}}`,
		`{"type":"response.completed","sequence_number":25,"response":{"id":"c831","status":"completed","usage":{"input_tokens":302,"output_tokens":180,"total_tokens":482,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":168}}}}`,
		`{"type":"ping","cost":"0"}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyResponsesStream(w, body, "grok-4.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyResponsesStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// Expected: message_start, tool_start(0), 2x input_json_delta,
	// tool_stop(0), message_delta(tool_use + usage), message_stop = 7
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != "message_start" {
		t.Fatalf("event[0] = %q, want message_start", events[0].Type)
	}

	// tool_use block start: id from call_id, name, empty input
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "tool_use" {
		t.Fatalf("event[1] = %+v, want content_block_start(tool_use)", events[1])
	}
	if events[1].Index == nil || *events[1].Index != 0 {
		t.Fatalf("tool block index = %v, want 0", events[1].Index)
	}
	if got := events[1].ContentBlock.ID; got != "call-912fb0d5" {
		t.Errorf("tool block id = %q, want call-912fb0d5", got)
	}
	if got := events[1].ContentBlock.Name; got != "get_weather" {
		t.Errorf("tool block name = %q, want get_weather", got)
	}
	if got := string(events[1].ContentBlock.Input); got != "{}" {
		t.Errorf("tool block input = %q, want {}", got)
	}

	// argument deltas reconstruct the full JSON
	if got := concatenatedPartialJSON(events, 0); got != `{"city":"Paris"}` {
		t.Errorf("partial_json concatenation = %q, want %q", got, `{"city":"Paris"}`)
	}

	// exactly one stop for the tool block (args.done AND item.done both fire)
	stops := contentBlockStopIndices(events)
	if len(stops) != 1 || stops[0] != 0 {
		t.Errorf("content_block_stop indices = %v, want [0]", stops)
	}

	// terminal message_delta: tool_use + real usage
	md := events[5]
	if md.Type != "message_delta" || md.Delta == nil || md.Delta.StopReason != "tool_use" {
		t.Fatalf("event[5] = %+v, want message_delta(tool_use)", md)
	}
	if md.Usage == nil || md.Usage.InputTokens != 302 || md.Usage.OutputTokens != 180 {
		t.Errorf("usage = %+v, want input 302 output 180", md.Usage)
	}
	if events[6].Type != "message_stop" {
		t.Errorf("event[6] = %q, want message_stop", events[6].Type)
	}

	// reasoning summaries must not leak as text deltas
	for _, ev := range events {
		if ev.Delta != nil && ev.Delta.Type == "text_delta" {
			t.Errorf("unexpected text_delta in tool-only stream: %+v", ev)
		}
	}
}

// TestProxyResponsesStream_TextOnlyWithUsage verifies the text-only shape is
// unchanged and response.completed usage maps to Anthropic semantics
// (input_tokens - cached_tokens, cache_read_input_tokens = cached_tokens).
func TestProxyResponsesStream_TextOnlyWithUsage(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"type":"response.output_item.added","sequence_number":24,"output_index":1,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","sequence_number":26,"output_index":1,"content_index":0,"item_id":"msg_1","delta":"Hello"}`,
		`{"type":"response.output_text.delta","sequence_number":27,"output_index":1,"content_index":0,"item_id":"msg_1","delta":" world"}`,
		`{"type":"response.output_text.done","sequence_number":34,"output_index":1,"content_index":0,"item_id":"msg_1","text":"Hello world"}`,
		`{"type":"response.completed","sequence_number":37,"response":{"id":"r1","status":"completed","usage":{"input_tokens":307,"output_tokens":169,"total_tokens":476,"input_tokens_details":{"cached_tokens":128},"output_tokens_details":{"reasoning_tokens":161}}}}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyResponsesStream(w, body, "grok-4.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyResponsesStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	// message_start, text_start, 2x text_delta, text_stop, message_delta, message_stop
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d: %+v", len(events), events)
	}
	if events[1].Type != "content_block_start" || events[1].ContentBlock == nil || events[1].ContentBlock.Type != "text" {
		t.Fatalf("event[1] = %+v, want content_block_start(text)", events[1])
	}
	var text strings.Builder
	for _, ev := range events {
		if ev.Delta != nil && ev.Delta.Type == "text_delta" {
			text.WriteString(ev.Delta.Text)
		}
	}
	if got := text.String(); got != "Hello world" {
		t.Errorf("text deltas = %q, want %q", got, "Hello world")
	}

	// the text block must be stopped before the terminal message_delta
	stopIdxs := eventsOfType(events, "content_block_stop")
	deltaIdxs := eventsOfType(events, "message_delta")
	if len(stopIdxs) != 1 || len(deltaIdxs) != 1 {
		t.Fatalf("expected 1 content_block_stop and 1 message_delta, got %v/%v", stopIdxs, deltaIdxs)
	}
	if stopIdxs[0] > deltaIdxs[0] {
		t.Errorf("content_block_stop (pos %d) must precede message_delta (pos %d)", stopIdxs[0], deltaIdxs[0])
	}

	md := events[deltaIdxs[0]]
	if md.Delta == nil || md.Delta.StopReason != "end_turn" {
		t.Fatalf("message_delta = %+v, want stop_reason end_turn", md)
	}
	if md.Usage == nil {
		t.Fatal("message_delta usage is nil")
	}
	if md.Usage.InputTokens != 179 {
		t.Errorf("input_tokens = %d, want 179 (307 total - 128 cached)", md.Usage.InputTokens)
	}
	if md.Usage.CacheReadInputTokens != 128 {
		t.Errorf("cache_read_input_tokens = %d, want 128", md.Usage.CacheReadInputTokens)
	}
	if md.Usage.OutputTokens != 169 {
		t.Errorf("output_tokens = %d, want 169", md.Usage.OutputTokens)
	}
}

// TestProxyResponsesStream_MixedTextThenToolCall verifies a text block is
// closed before the tool block opens, with contiguous indices.
func TestProxyResponsesStream_MixedTextThenToolCall(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"type":"response.output_text.delta","sequence_number":1,"output_index":0,"item_id":"msg_1","delta":"Checking."}`,
		`{"type":"response.output_item.added","sequence_number":2,"output_index":1,"item":{"id":"fc_1","type":"function_call","status":"in_progress","name":"get_data","call_id":"call-mixed-1","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":3,"output_index":1,"item_id":"fc_1","delta":"{\"id\":1}"}`,
		`{"type":"response.function_call_arguments.done","sequence_number":4,"output_index":1,"item_id":"fc_1","arguments":"{\"id\":1}"}`,
		`{"type":"response.output_item.done","sequence_number":5,"output_index":1,"item":{"id":"fc_1","type":"function_call","status":"completed","name":"get_data","call_id":"call-mixed-1","arguments":"{\"id\":1}"}}`,
		`{"type":"response.completed","sequence_number":6,"response":{"id":"r1","status":"completed","usage":{"input_tokens":10,"output_tokens":5}}}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyResponsesStream(w, body, "gpt-5.6-luna", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyResponsesStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	starts := eventsOfType(events, "content_block_start")
	if len(starts) != 2 {
		t.Fatalf("expected 2 content_block_start events, got %d: %+v", len(starts), events)
	}
	if events[starts[0]].ContentBlock == nil || events[starts[0]].ContentBlock.Type != "text" || *events[starts[0]].Index != 0 {
		t.Errorf("first block = %+v, want text at index 0", events[starts[0]])
	}
	if events[starts[1]].ContentBlock == nil || events[starts[1]].ContentBlock.Type != "tool_use" || *events[starts[1]].Index != 1 {
		t.Errorf("second block = %+v, want tool_use at index 1", events[starts[1]])
	}
	if events[starts[1]].ContentBlock.Name != "get_data" {
		t.Errorf("tool name = %q, want get_data", events[starts[1]].ContentBlock.Name)
	}

	// text stop (index 0) must be emitted before the tool block starts
	textStop := -1
	for i, ev := range events {
		if ev.Type == "content_block_stop" && ev.Index != nil && *ev.Index == 0 {
			textStop = i
			break
		}
	}
	if textStop == -1 {
		t.Fatalf("no content_block_stop for text block: %+v", events)
	}
	if textStop > starts[1] {
		t.Errorf("text stop (pos %d) must precede tool start (pos %d)", textStop, starts[1])
	}

	stops := contentBlockStopIndices(events)
	if len(stops) != 2 || stops[0] != 0 || stops[1] != 1 {
		t.Errorf("content_block_stop indices = %v, want [0 1]", stops)
	}

	var md *types.MessageEvent
	for i := range events {
		if events[i].Type == "message_delta" {
			md = &events[i]
			break
		}
	}
	if md == nil || md.Delta == nil || md.Delta.StopReason != "tool_use" {
		t.Fatalf("message_delta = %+v, want stop_reason tool_use", md)
	}
}

// TestProxyResponsesStream_ParallelToolCalls verifies two function_call items
// each get their own tool_use block, in order, each stopped exactly once.
func TestProxyResponsesStream_ParallelToolCalls(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc_a","type":"function_call","status":"in_progress","name":"search","call_id":"call-a","arguments":""}}`,
		`{"type":"response.output_item.added","sequence_number":2,"output_index":1,"item":{"id":"fc_b","type":"function_call","status":"in_progress","name":"lookup","call_id":"call-b","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":3,"output_index":0,"item_id":"fc_a","delta":"{\"q\":\"go\"}"}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":4,"output_index":1,"item_id":"fc_b","delta":"{\"id\":42}"}`,
		`{"type":"response.function_call_arguments.done","sequence_number":5,"output_index":0,"item_id":"fc_a","arguments":"{\"q\":\"go\"}"}`,
		`{"type":"response.output_item.done","sequence_number":6,"output_index":0,"item":{"id":"fc_a","type":"function_call","status":"completed","name":"search","call_id":"call-a","arguments":"{\"q\":\"go\"}"}}`,
		`{"type":"response.function_call_arguments.done","sequence_number":7,"output_index":1,"item_id":"fc_b","arguments":"{\"id\":42}"}`,
		`{"type":"response.output_item.done","sequence_number":8,"output_index":1,"item":{"id":"fc_b","type":"function_call","status":"completed","name":"lookup","call_id":"call-b","arguments":"{\"id\":42}"}}`,
		`{"type":"response.completed","sequence_number":9,"response":{"id":"r1","status":"completed","usage":{"input_tokens":20,"output_tokens":8}}}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyResponsesStream(w, body, "muse-spark-1.2", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyResponsesStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())

	starts := eventsOfType(events, "content_block_start")
	if len(starts) != 2 {
		t.Fatalf("expected 2 tool blocks, got %d: %+v", len(starts), events)
	}
	if events[starts[0]].ContentBlock == nil || events[starts[0]].ContentBlock.Type != "tool_use" ||
		events[starts[0]].ContentBlock.ID != "call-a" || events[starts[0]].ContentBlock.Name != "search" || *events[starts[0]].Index != 0 {
		t.Errorf("first tool block = %+v, want search/call-a at 0", events[starts[0]])
	}
	if events[starts[1]].ContentBlock == nil || events[starts[1]].ContentBlock.Type != "tool_use" ||
		events[starts[1]].ContentBlock.ID != "call-b" || events[starts[1]].ContentBlock.Name != "lookup" || *events[starts[1]].Index != 1 {
		t.Errorf("second tool block = %+v, want lookup/call-b at 1", events[starts[1]])
	}

	if got := concatenatedPartialJSON(events, 0); got != `{"q":"go"}` {
		t.Errorf("block 0 partial_json = %q, want %q", got, `{"q":"go"}`)
	}
	if got := concatenatedPartialJSON(events, 1); got != `{"id":42}` {
		t.Errorf("block 1 partial_json = %q, want %q", got, `{"id":42}`)
	}

	stops := contentBlockStopIndices(events)
	if len(stops) != 2 || stops[0] != 0 || stops[1] != 1 {
		t.Errorf("content_block_stop indices = %v, want [0 1]", stops)
	}

	var md *types.MessageEvent
	for i := range events {
		if events[i].Type == "message_delta" {
			md = &events[i]
			break
		}
	}
	if md == nil || md.Delta == nil || md.Delta.StopReason != "tool_use" {
		t.Fatalf("message_delta = %+v, want stop_reason tool_use", md)
	}
}

// TestProxyResponsesStream_EOFFallbackWithoutCompleted verifies streams that
// end without response.completed: every started block is stopped exactly once
// and the fallback message_delta is still emitted (end_turn for text, tool_use
// when a tool block was started).
func TestProxyResponsesStream_EOFFallbackWithoutCompleted(t *testing.T) {
	t.Run("text stream falls back to end_turn", func(t *testing.T) {
		handler := NewStreamHandler()
		w := newMockResponseWriter()
		body := sseLines(
			`{"type":"response.output_text.delta","sequence_number":1,"output_index":0,"item_id":"msg_1","delta":"Hello"}`,
		)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := handler.ProxyResponsesStream(w, body, "grok-4.6", ctx, 0, cancel); err != nil {
			t.Fatalf("ProxyResponsesStream error: %v", err)
		}

		events := parseSSEEvents(t, w.buf.String())
		// message_start, text_start, text_delta, text_stop, message_delta, message_stop
		if len(events) != 6 {
			t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
		}
		stops := contentBlockStopIndices(events)
		if len(stops) != 1 || stops[0] != 0 {
			t.Errorf("content_block_stop indices = %v, want [0]", stops)
		}
		if events[4].Type != "message_delta" || events[4].Delta == nil || events[4].Delta.StopReason != "end_turn" {
			t.Errorf("event[4] = %+v, want message_delta(end_turn)", events[4])
		}
		if events[5].Type != "message_stop" {
			t.Errorf("event[5] = %q, want message_stop", events[5].Type)
		}
	})

	t.Run("tool stream falls back to tool_use", func(t *testing.T) {
		handler := NewStreamHandler()
		w := newMockResponseWriter()
		body := sseLines(
			`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","name":"read_file","call_id":"call-eof","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","sequence_number":2,"output_index":0,"item_id":"fc_1","delta":"{\"path\":\"/tmp/x\"}"}`,
		)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := handler.ProxyResponsesStream(w, body, "grok-4.6", ctx, 0, cancel); err != nil {
			t.Fatalf("ProxyResponsesStream error: %v", err)
		}

		events := parseSSEEvents(t, w.buf.String())
		// message_start, tool_start, input_json_delta, tool_stop, message_delta, message_stop
		if len(events) != 6 {
			t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
		}
		stops := contentBlockStopIndices(events)
		if len(stops) != 1 || stops[0] != 0 {
			t.Errorf("content_block_stop indices = %v, want [0]", stops)
		}
		if events[4].Type != "message_delta" || events[4].Delta == nil || events[4].Delta.StopReason != "tool_use" {
			t.Errorf("event[4] = %+v, want message_delta(tool_use)", events[4])
		}
		if events[5].Type != "message_stop" {
			t.Errorf("event[5] = %q, want message_stop", events[5].Type)
		}
	})
}

func TestProxyStream_NoDuplicateToolStopsOnErrorAfterFinishReason(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := "data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read","arguments":"{}"}}]}}]}` + "\n\n" +
		"data: " + `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, &bodyThenErrReader{body: []byte(body), err: io.ErrUnexpectedEOF}, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream() error = %v, want nil", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	stops := 0
	for _, ev := range events {
		if ev.Type == "content_block_stop" {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("content_block_stop count = %d, want 1: %+v", stops, events)
	}
	last := events[len(events)-1]
	if last.Type != "message_stop" {
		t.Errorf("last event = %q, want message_stop", last.Type)
	}
	if got := events[len(events)-2]; got.Type != "message_delta" || got.Delta == nil || got.Delta.StopReason != "tool_use" {
		t.Errorf("penultimate event = %+v, want message_delta with stop_reason tool_use", got)
	}
}
