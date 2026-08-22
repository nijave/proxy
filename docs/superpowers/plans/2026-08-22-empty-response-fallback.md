# Empty-Response Fallback Patch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Treat "stream ended with reasoning but zero answer tokens" as a model failure so routatic-proxy silently falls back to the next model in the chain instead of forwarding a blank turn to Claude Code.

**Architecture:** All changes live in the handler layer (`internal/handlers/messages.go`) plus a small config block. A hold-back buffer inside `responseWriter` withholds client-visible SSE bytes until an answer marker (`text_delta` / `input_json_delta` / `tool_use` block) arrives or a byte cap is exceeded. While holding, nothing has been sent, so the existing per-model fallback machinery can retry cleanly. No changes to `internal/transformer/stream.go` or its 27+ tests; the hold-back works uniformly on transformed OpenAI SSE and raw Anthropic passthrough because content detection is substring-based.

**Tech Stack:** Go 1.22+ (see `go.mod`), stdlib `net/http/httptest` for tests, existing Makefile targets (`make test`, `make lint`, `make build`). No new dependencies.

**Spec:** This plan embeds its own spec (Background below) — the investigation was completed in Claude Code session `a39e3457-8e6b-447c-acc8-7215ce118934`; no separate spec doc exists. Evidence summary travels with the plan.

## Background (spec)

**Symptom:** Claude Code sessions routed through routatic-proxy to `ox-alpha-free` (and other free-tier models) randomly "stop": the user sees a blank turn and must type "continue".

**Root cause:** Upstream free-tier models frequently emit a stream containing **only reasoning tokens**, then close normally with `finish_reason=stop` and **zero answer tokens**. The HTTP request technically succeeded, so nothing logs an error. Claude Code renders a complete-but-empty turn — a silent stop.

**Measured evidence (proxy log `~/.config/routatic-proxy/routatic-proxy.log`, Aug 21–22 2026):**
- ox-alpha-free: 81 of 389 stream completions had `output_tokens=0` (21%, climbing 4% → 36% overnight)
- Free-tier-wide: qwen3.7-plus 66% empty, nemotron-3-ultra-free 24%, minimax-m3 33%
- Paid models clean: deepseek-v4-flash 0.3%, gpt-5.6-luna 0%, glm-5.2 0%
- Upstream corroboration: opencode#44092 (server-side content filter cuts streams mid-reasoning, `rawFinish:"sensitive"`), #41469 (large uncached prefill → gateway holds ~280s then returns empty stream), #37735 (`finish:"stop"` + usage but no content recorded as success)

**Three gaps in the proxy (all in `internal/handlers/messages.go`):**

| # | Gap | Location |
|---|-----|----------|
| 1 | `detectContentInSSE` counts `"thinking_delta"` (plus overly-broad `"content_block_start"`/`"content_block_delta"`/`"content":""` markers) as content, so a thinking-only stream looks valuable | messages.go:149-159 |
| 2 | `isLowValueResponse` only fires for `long_context`/`complex` scenarios; all Claude Code main-session traffic routes as `scenario=default`, so the guard never applies | messages.go:208-224 |
| 3 | Once any byte is forwarded, `ssePayloadWritten=true` makes silent fallback impossible — and `ProxyStream` writes `message_start` immediately, so by stream end it is *always* true. Even when the guard fired, fallback degraded to abort-with-error | messages.go:703,713,721 + transformer/stream.go:203-216 |

**Fix strategy:** put a hold-back buffer in `responseWriter` (handler layer). While armed, `Write()` appends to an internal buffer instead of the wire; `ssePayloadWritten` stays false. The buffer releases when answer content is detected anywhere in it, or when it exceeds a byte cap (degenerate long-thinking case degrades to today's behavior). If a stream ends while still holding, the "no answer content" guard fires, the buffer is discarded, and the loop silently retries the next model — the client never saw the failed attempt.

**Goals:**
- Thinking-only streams trigger fallback to the next chain model, silently when possible
- Cap-exceeded thinking-only streams produce a visible SSE `error` event instead of a fake clean end (honest failure + circuit-breaker credit)
- Non-streaming responses without text/tool_use content also fall back
- Feature is config-gated: `empty_response_fallback.enabled` (default **true**, opt-out), `empty_response_fallback.holdback_limit_bytes` (default 32768)

**Non-goals:**
- No changes to the transformer's public API or event shapes (all 27+ `stream_test.go` tests must keep passing untouched)
- No per-attempt history records or new metrics counters (reuse `metrics.RecordFailureForModel`)
- No reordering of the user's fallback chain (that is config, done separately)
- No hold-back logic for Gemini/Responses wire formats (they emit no reasoning events today)

## Global Constraints

- Go stdlib only; no new module dependencies
- Every task ends with `make test` (race detector) green; final task runs `make lint` and `make build`
- Do not modify `internal/transformer/stream.go`; all 27+ existing `TestProxyStream_*` tests must pass unchanged
- SSE wire compatibility: a client must never observe duplicate `message_start` events or interleaved fragments of two models' streams
- Concurrency: `responseWriter` methods are called from the heartbeat goroutine and the stream goroutine; every new field is guarded by the existing `w.mu`
- Commits follow repo convention (Conventional Commits, lowercase scope, e.g. `feat(config): add thinking_mode field with enum validation`)
- Stage explicit file paths only; no blanket `git add`

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/handlers/messages.go` | Modify | Content detection, low-value guard, hold-back buffer, streaming-loop wiring, non-streaming validation |
| `internal/handlers/messages_test.go` | Modify | Unit + integration tests for all of the above |
| `internal/config/config.go` | Modify | `EmptyResponseFallbackConfig` type, accessor methods, `Config` field |
| `internal/config/config_test.go` | Modify | Accessor defaults tests |
| `internal/server/server.go` | Modify | Pass config block into `NewMessagesHandler` |
| `configs/config.example.json` | Modify | Document the new config block |

---

### Task 1: Answer-only content detection in `detectContentInSSE`

**Files:**
- Modify: `internal/handlers/messages.go:149-159`
- Test: `internal/handlers/messages_test.go`

**Interfaces:**
- Consumes: existing `responseWriter.Write` / `hasContent` (unchanged signatures)
- Produces: `hasContent()` now returns true **only** for answer-bearing SSE payloads: `"text_delta"`, `"input_json_delta"`, or a `"type":"tool_use"` block start. Thinking deltas, block starts/stops, message envelopes, and `"stop_reason":"tool_use"` no longer count.

- [ ] **Step 1: Write the failing test**

Append to `internal/handlers/messages_test.go`:

```go
func TestDetectContentInSSE_AnswerMarkersOnly(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		want  bool
	}{
		{
			name:  "message start is not content",
			frame: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"content\":[]}}\n\n",
			want:  false,
		},
		{
			name:  "thinking block start is not content",
			frame: "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n",
			want:  false,
		},
		{
			name:  "thinking delta is not content",
			frame: "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"reasoning\"}}\n\n",
			want:  false,
		},
		{
			name:  "tool_use stop reason is not content",
			frame: "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":10}}\n\n",
			want:  false,
		},
		{
			name:  "text delta is content",
			frame: "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
			want:  true,
		},
		{
			name:  "tool_use block start is content",
			frame: "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"Bash\",\"input\":{}}}\n\n",
			want:  true,
		},
		{
			name:  "input_json_delta is content",
			frame: "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"cmd\\\":\"}}\n\n",
			want:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rw := &responseWriter{ResponseWriter: httptest.NewRecorder()}
			if _, err := rw.Write([]byte(tc.frame)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if got := rw.hasContent(); got != tc.want {
				t.Errorf("hasContent() = %v, want %v for frame %q", got, tc.want, tc.frame)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handlers/ -run TestDetectContentInSSE_AnswerMarkersOnly -v`
Expected: FAIL — the four "not content" cases report `hasContent() = true` because the current matcher includes `"thinking_delta"`, `"content_block_start"`, `"content_block_delta"`.

- [ ] **Step 3: Replace the marker list**

In `internal/handlers/messages.go`, replace `detectContentInSSE` (lines 149-159) with:

```go
// detectContentInSSE scans an outgoing SSE payload for user-visible answer
// content: text deltas, tool-use blocks, or incremental tool arguments.
// Thinking deltas deliberately do NOT count — a stream that produced reasoning
// but no answer must be classifiable as empty so it can trigger model fallback.
// Markers are matched against compact JSON ("type":"tool_use" with no space)
// which is what encoding/json and our upstreams emit.
func (w *responseWriter) detectContentInSSE(b []byte) {
	data := string(b)
	if strings.Contains(data, `"text_delta"`) ||
		strings.Contains(data, `"input_json_delta"`) ||
		strings.Contains(data, `"type":"tool_use"`) {
		w.contentWritten = true
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/handlers/ -run TestDetectContentInSSE_AnswerMarkersOnly -v`
Expected: PASS (all 7 cases)

- [ ] **Step 5: Run the full handlers suite**

Run: `go test ./internal/handlers/ `
Expected: PASS — no existing test depends on thinking deltas setting `contentWritten` (verified by grep: no test references `contentWritten`/`detectContent`).

- [ ] **Step 6: Commit**

```bash
git add internal/handlers/messages.go internal/handlers/messages_test.go
git commit -m "fix(handlers): classify thinking-only SSE as no answer content"
```

---

### Task 2: Low-value guard applies to every scenario

The existing guard `isLowValueResponse(scenario, outputTokens, hasContent)` gates on scenario and token count. Both gates are wrong for this failure mode: CC traffic is always `scenario=default`, and upstreams attribute reasoning tokens to `output_tokens` inconsistently (the observed empty streams logged 0, but we must not depend on that). The only reliable signal is `hasContent` from Task 1.

**Files:**
- Modify: `internal/handlers/messages.go:208-224` (delete function) and `:778-786` (call site)
- Test: `internal/handlers/messages_test.go`

**Interfaces:**
- Consumes: `rw.hasContent()` (Task 1 semantics), `transformer.ErrEmptyStream`, `handleStreamError` closure
- Produces: after any successful-looking stream, `!rw.hasContent()` triggers the standard fallback path (`ErrEmptyStream`). Function `isLowValueResponse` is deleted.

- [ ] **Step 1: Write the failing integration test**

Append to `internal/handlers/messages_test.go` (harness mirrors `TestHandleStreaming_PerModelTimeoutFallback` at messages_test.go:1438):

```go
func TestHandleStreaming_DefaultScenario_ThinkingOnly_AbortsWithError(t *testing.T) {
	var callCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"ONLY REASONING, NO ANSWER\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	cfg := &config.Config{
		APIKey: "test-key",
		OpenCodeGo: config.OpenCodeGoConfig{
			BaseURL:   upstream.URL,
			TimeoutMs: 300000,
		},
	}
	atomicCfg := config.NewAtomicConfig(cfg, "/tmp/test-config.json")
	ocClient := client.NewOpenCodeClient(atomicCfg, nil)

	handler := &MessagesHandler{
		client:              ocClient,
		logger:              slog.Default(),
		metrics:             metrics.New(),
		streamHandler:       transformer.NewStreamHandler(),
		requestTransformer:  transformer.NewRequestTransformer(),
		responseTransformer: transformer.NewResponseTransformer(),
	}

	rawBody := json.RawMessage(`{"model":"kimi-k2.6","stream":true,"max_tokens":256,"messages":[{"role":"user","content":"hello"}]}`)
	var anthropicReq types.MessageRequest
	if err := json.Unmarshal(rawBody, &anthropicReq); err != nil {
		t.Fatalf("unmarshal rawBody: %v", err)
	}

	// Single-model chain: fallback cannot proceed, so the request must end
	// with a visible SSE error rather than a clean-but-empty turn.
	chain := []config.ModelConfig{{Provider: "opencode-go", ModelID: "kimi-k2.6"}}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	handler.handleStreaming(recorder, req, &anthropicReq, &core.NormalizedRequest{Stream: true}, chain, rawBody, router.ScenarioDefault, "")

	body := recorder.Body.String()
	if got := atomic.LoadInt32(&callCount); got != 1 {
		t.Errorf("expected exactly 1 upstream call, got %d", got)
	}
	if !strings.Contains(body, `"type":"error"`) {
		t.Errorf("expected SSE error event for thinking-only stream under default scenario, body:\n%s", body)
	}
	if strings.Contains(body, "message_stop") {
		t.Errorf("stream must not complete normally after empty-answer detection, body:\n%s", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handlers/ -run TestHandleStreaming_DefaultScenario_ThinkingOnly_AbortsWithError -v`
Expected: FAIL — today the guard skips `scenario=default`, `recordStreamSuccess` runs, the stream completes with `message_stop` and no error event.

- [ ] **Step 3: Delete `isLowValueResponse` and harden the call site**

Delete `isLowValueResponse` (messages.go:208-224 including its doc comment). Replace the call site (messages.go:778-786):

```go
			if isLowValueResponse(scenario, rw.getOutputTokens(), rw.hasContent()) {
				h.logger.Warn("upstream returned low-value response, triggering fallback",
					"model", model.ModelID, "provider", model.Provider,
					"scenario", scenario, "output_tokens", rw.getOutputTokens())
				if !handleStreamError(transformer.ErrEmptyStream, model, wireFormat.String()) {
					return
				}
				continue
			}
```

with:

```go
			if !rw.hasContent() {
				h.logger.Warn("upstream stream ended without answer content, triggering fallback",
					"model", model.ModelID, "provider", model.Provider,
					"scenario", scenario, "output_tokens", rw.getOutputTokens())
				if !handleStreamError(transformer.ErrEmptyStream, model, wireFormat.String()) {
					return
				}
				continue
			}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/handlers/ -v`
Expected: PASS — new test passes; full package green. (If any pre-existing test asserted low-value behavior for long_context scenarios, update it to the new unconditional semantics — grep confirms none exist.)

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/messages.go internal/handlers/messages_test.go
git commit -m "fix(handlers): treat missing answer content as stream failure in all scenarios"
```

> Note: at this point the failure is *visible* (SSE error event) rather than *recovered*. Silent recovery requires Task 3 + Task 4.

---

### Task 3: Hold-back buffer in `responseWriter`

**Files:**
- Modify: `internal/handlers/messages.go` (struct at :57-69, `Write` at :80-93, new methods near :161)
- Test: `internal/handlers/messages_test.go`

**Interfaces:**
- Produces (consumed by Task 4):
  - `func (w *responseWriter) ArmHoldback(limitBytes int)` — arm buffering; `limitBytes <= 0` selects `defaultHoldbackLimitBytes`
  - `func (w *responseWriter) DiscardHoldback()` — drop buffered bytes and disarm
  - Invariant: while armed and unreleased, `Write` sends nothing to the wire and `ssePayloadWritten` stays false; `hasContent()`/usage extraction still observe every byte
  - Const `const defaultHoldbackLimitBytes = 32 * 1024`

- [ ] **Step 1: Write the failing tests**

Append to `internal/handlers/messages_test.go`:

```go
func TestResponseWriter_Holdback_BuffersUntilAnswerMarker(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec}
	rw.ArmHoldback(0) // 0 -> default limit

	mustWrite := func(t *testing.T, rw *responseWriter, frame string) {
		t.Helper()
		if _, err := rw.Write([]byte(frame)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	mustWrite(t, rw, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
	if rec.Body.Len() != 0 {
		t.Fatalf("bytes leaked to client while holding: %q", rec.Body.String())
	}
	if rw.ssePayloadWritten {
		t.Error("ssePayloadWritten must stay false while holding")
	}

	mustWrite(t, rw, "event: content_block_delta\ndata: {\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"hm\"}}\n\n")
	if rec.Body.Len() != 0 {
		t.Fatalf("thinking frame leaked to client while holding: %q", rec.Body.String())
	}

	mustWrite(t, rw, "event: content_block_delta\ndata: {\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")

	body := rec.Body.String()
	for _, want := range []string{"message_start", "thinking_delta", "text_delta"} {
		if !strings.Contains(body, want) {
			t.Errorf("flushed stream missing %q; body:\n%s", want, body)
		}
	}
	start := strings.Index(body, "message_start")
	thinking := strings.Index(body, "thinking_delta")
	text := strings.Index(body, "text_delta")
	if !(start < thinking && thinking < text) {
		t.Errorf("flushed frames out of order:\n%s", body)
	}
	if !rw.hasContent() {
		t.Error("hasContent() = false after answer marker")
	}
	if !rw.ssePayloadWritten {
		t.Error("ssePayloadWritten = false after release")
	}
}

func TestResponseWriter_Holdback_LimitReleasesWithoutContent(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec}
	rw.ArmHoldback(8)

	big := strings.Repeat("x", 100) // no answer markers
	if _, err := rw.Write([]byte(big)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rec.Body.Len() != 100 {
		t.Errorf("expected buffer flush at limit, client got %d bytes", rec.Body.Len())
	}
	if rw.ssePayloadWritten != true {
		t.Error("ssePayloadWritten must be true after limit release")
	}
	if rw.hasContent() {
		t.Error("limit release must not fabricate content")
	}
	// Disarmed by release: subsequent writes pass straight through.
	if _, err := rw.Write([]byte("second")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasSuffix(rec.Body.String(), "second") {
		t.Errorf("post-release write not passed through: %q", rec.Body.String())
	}
}

func TestResponseWriter_Holdback_DiscardDropsBuffer(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec}
	rw.ArmHoldback(0)
	if _, err := rw.Write([]byte("event: message_start\ndata: {}\n\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rw.DiscardHoldback()
	if rec.Body.Len() != 0 {
		t.Fatalf("discarded bytes reached client: %q", rec.Body.String())
	}
	// Disarmed by discard: next write passes through untouched.
	if _, err := rw.Write([]byte("fresh")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := rec.Body.String(); got != "fresh" {
		t.Errorf("post-discard write = %q, want %q", got, "fresh")
	}
}

func TestResponseWriter_UnarmedPassthroughUnchanged(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec}
	if _, err := rw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := rec.Body.String(); got != "hello" {
		t.Errorf("body = %q, want %q", got, "hello")
	}
	if !rw.ssePayloadWritten {
		t.Error("unarmed write must set ssePayloadWritten")
	}
	if !rw.wroteHeader {
		t.Error("first write must write header")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/handlers/ -run 'TestResponseWriter_(Holdback|Unarmed)' -v`
Expected: FAIL to compile — `ArmHoldback`/`DiscardHoldback` undefined.

- [ ] **Step 3: Implement the hold-back buffer**

In `internal/handlers/messages.go`:

Extend the struct (lines 57-69) — add three fields after `contentWritten`:

```go
type responseWriter struct {
	http.ResponseWriter
	mu                sync.Mutex
	wroteHeader       bool
	ssePayloadWritten bool
	contentWritten    bool
	holdbackArmed     bool
	holdbackBuf       []byte
	holdbackLimit     int
	usage             struct {
		inputTokens              int
		outputTokens             int
		cacheReadInputTokens     int
		cacheCreationInputTokens int
	}
}
```

Add the constant near `defaultKeepaliveInterval` (line 246):

```go
// defaultHoldbackLimitBytes caps how many SSE bytes may be withheld while
// waiting for the first answer token. Streams exceeding the cap are released
// progressively and lose the ability to fall back silently (same as today).
const defaultHoldbackLimitBytes = 32 * 1024
```

Replace `Write` (lines 80-93) and add the helpers:

```go
// ArmHoldback puts the writer in hold-back mode: SSE payloads are buffered
// until answer content is detected or the byte limit is reached. Used before
// each upstream attempt so a thinking-only stream can be discarded and retried
// on another model without the client ever seeing it. limitBytes <= 0 selects
// defaultHoldbackLimitBytes.
func (w *responseWriter) ArmHoldback(limitBytes int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if limitBytes <= 0 {
		limitBytes = defaultHoldbackLimitBytes
	}
	w.holdbackArmed = true
	w.holdbackBuf = nil
	w.holdbackLimit = limitBytes
}

// DiscardHoldback drops any buffered bytes and disarms hold-back mode. Called
// when an attempt failed before releasing its buffer, so the next model starts
// with a clean stream.
func (w *responseWriter) DiscardHoldback() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.holdbackArmed = false
	w.holdbackBuf = nil
}

// Write intercepts every outbound byte. While hold-back is armed and no
// answer content has been seen, bytes accumulate in holdbackBuf instead of
// reaching the client. Usage extraction and content detection run on every
// byte regardless of hold state.
func (w *responseWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(b) > 0 {
		w.extractUsageFromSSE(b)
		w.detectContentInSSE(b)
		if w.holdbackArmed {
			w.holdbackBuf = append(w.holdbackBuf, b...)
			if w.contentWritten || len(w.holdbackBuf) >= w.holdbackLimit {
				return w.flushHoldbackLocked()
			}
			return len(b), nil
		}
		n, err := w.ResponseWriter.Write(b)
		if n > 0 {
			w.ssePayloadWritten = true
		}
		return n, err
	}
	return 0, nil
}

// flushHoldbackLocked writes the held prefix to the client and disarms
// hold-back. Callers must hold w.mu.
func (w *responseWriter) flushHoldbackLocked() (int, error) {
	buf := w.holdbackBuf
	w.holdbackBuf = nil
	w.holdbackArmed = false
	n, err := w.ResponseWriter.Write(buf)
	if n > 0 {
		w.ssePayloadWritten = true
	}
	if err == nil && n < len(buf) {
		return n, io.ErrShortWrite
	}
	return n, err
}
```

Notes:
- `io` is already imported (messages.go:11).
- Header handling is unchanged: `handleStreaming` calls `rw.WriteHeader(200)` before the loop, and `WriteHeader` still short-circuits duplicates. Buffered writes happen after headers exist, which is fine — headers-only does not commit the body.
- Keepalives bypass `Write` entirely (`WriteKeepalive` writes `":keepalive\n\n"` straight to `ResponseWriter`), so clients receive comment frames during long holds. SSE comments are ignored by clients and do not set `ssePayloadWritten`.
- Deliberate limitation (do not fix here): usage extracted from a discarded attempt can linger until the successful attempt's `message_delta` overwrites it. Same accumulation exists across attempts today.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/handlers/ -race`
Expected: PASS — new tests plus the concurrency test `TestResponseWriter_ConcurrentWrites` (messages_test.go:1683) stay green.

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/messages.go internal/handlers/messages_test.go
git commit -m "feat(handlers): add hold-back buffer to response writer for clean stream fallback"
```

---

### Task 4: Wire hold-back into the streaming loop + config knobs

**Files:**
- Modify: `internal/config/config.go` (new type + `Config` field after line 33)
- Modify: `internal/config/config_test.go`
- Modify: `internal/handlers/messages.go` (constructor :323-353, `handleStreaming` loop :630-, `handleStreamError` :694-727, tail :910-920)
- Modify: `internal/server/server.go:117-129`
- Test: `internal/config/config_test.go`, `internal/handlers/messages_test.go`

**Interfaces:**
- Consumes: `ArmHoldback`/`DiscardHoldback` (Task 3), `!rw.hasContent()` guard (Task 2)
- Produces:
  - `config.EmptyResponseFallbackConfig` with `IsEnabled() bool` (nil receiver → true) and `LimitBytes() int` (≤0 → 32768)
  - `Config.EmptyResponseFallback *EmptyResponseFallbackConfig` JSON key `empty_response_fallback`
  - `MessagesHandler.emptyRespFallback *config.EmptyResponseFallbackConfig` field; `NewMessagesHandler` gains one trailing parameter of that type

- [ ] **Step 1: Write failing config tests**

Append to `internal/config/config_test.go`:

```go
func TestEmptyResponseFallbackConfig_Defaults(t *testing.T) {
	var nilCfg *EmptyResponseFallbackConfig
	if !nilCfg.IsEnabled() {
		t.Error("nil config must default to enabled (opt-out)")
	}
	if got := nilCfg.LimitBytes(); got != 32*1024 {
		t.Errorf("nil config LimitBytes() = %d, want %d", got, 32*1024)
	}

	disabled := &EmptyResponseFallbackConfig{Enabled: n(false)}
	if disabled.IsEnabled() {
		t.Error("explicitly disabled config must not be enabled")
	}
	if got := (&EmptyResponseFallbackConfig{}).LimitBytes(); got != 32*1024 {
		t.Errorf("zero limit must default to %d, got %d", 32*1024, got)
	}
	custom := &EmptyResponseFallbackConfig{HoldbackLimitBytes: 1024}
	if got := custom.LimitBytes(); got != 1024 {
		t.Errorf("custom limit = %d, want 1024", got)
	}
}
```

(If `n(bool) *bool` does not exist in this package's tests, add `func n(b bool) *bool { return &b }` — the handlers tests already define one at messages_test.go.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/config/ -run TestEmptyResponseFallbackConfig_Defaults -v`
Expected: FAIL to compile — type undefined.

- [ ] **Step 3: Implement the config type**

In `internal/config/config.go`, after `CostRoutingConfig` (line 43):

```go
// EmptyResponseFallbackConfig controls treating reasoning-only responses
// (streams that end with no text or tool_use content) as failures and
// falling back to the next model in the chain. Enabled by default; opt out
// with {"enabled": false}.
type EmptyResponseFallbackConfig struct {
	Enabled            *bool `json:"enabled,omitempty"`
	HoldbackLimitBytes int   `json:"holdback_limit_bytes,omitempty"`
}

// IsEnabled reports whether empty-response fallback should act. A nil config
// or unset flag means enabled.
func (c *EmptyResponseFallbackConfig) IsEnabled() bool {
	if c == nil || c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// LimitBytes returns the hold-back buffer cap. Values <= 0 select the
// 32 KiB default.
func (c *EmptyResponseFallbackConfig) LimitBytes() int {
	const def = 32 * 1024
	if c == nil || c.HoldbackLimitBytes <= 0 {
		return def
	}
	return c.HoldbackLimitBytes
}
```

Add to the `Config` struct (after line 33, `Storage`):

```go
	EmptyResponseFallback          *EmptyResponseFallbackConfig `json:"empty_response_fallback,omitempty"`
```

- [ ] **Step 4: Run config tests**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Write failing streaming integration tests**

Append to `internal/handlers/messages_test.go`:

```go
func TestHandleStreaming_ThinkingOnlyPrimary_FallsBackSilently(t *testing.T) {
	var callCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if count == 1 {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"SECRET_THINKING_MODEL_ONE\"}}]}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"FINAL_ANSWER_FROM_MODEL_TWO\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	cfg := &config.Config{
		APIKey: "test-key",
		OpenCodeGo: config.OpenCodeGoConfig{
			BaseURL:   upstream.URL,
			TimeoutMs: 300000,
		},
	}
	atomicCfg := config.NewAtomicConfig(cfg, "/tmp/test-config.json")
	ocClient := client.NewOpenCodeClient(atomicCfg, nil)

	handler := &MessagesHandler{
		client:              ocClient,
		logger:              slog.Default(),
		metrics:             metrics.New(),
		streamHandler:       transformer.NewStreamHandler(),
		requestTransformer:  transformer.NewRequestTransformer(),
		responseTransformer: transformer.NewResponseTransformer(),
		emptyRespFallback:   &config.EmptyResponseFallbackConfig{},
	}

	rawBody := json.RawMessage(`{"model":"kimi-k2.6","stream":true,"max_tokens":256,"messages":[{"role":"user","content":"hello"}]}`)
	var anthropicReq types.MessageRequest
	if err := json.Unmarshal(rawBody, &anthropicReq); err != nil {
		t.Fatalf("unmarshal rawBody: %v", err)
	}

	chain := []config.ModelConfig{
		{Provider: "opencode-go", ModelID: "kimi-k2.6"},
		{Provider: "opencode-go", ModelID: "glm-5"},
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	handler.handleStreaming(recorder, req, &anthropicReq, &core.NormalizedRequest{Stream: true}, chain, rawBody, router.ScenarioDefault, "")

	body := recorder.Body.String()
	if got := atomic.LoadInt32(&callCount); got != 2 {
		t.Errorf("expected 2 upstream calls (empty primary + fallback), got %d", got)
	}
	if n := strings.Count(body, "message_start"); n != 1 {
		t.Errorf("client must see exactly one message_start, got %d:\n%s", n, body)
	}
	if !strings.Contains(body, "FINAL_ANSWER_FROM_MODEL_TWO") {
		t.Errorf("fallback answer missing from client stream:\n%s", body)
	}
	if strings.Contains(body, "SECRET_THINKING_MODEL_ONE") {
		t.Errorf("primary model reasoning leaked to client:\n%s", body)
	}
	if strings.Contains(body, `"type":"error"`) {
		t.Errorf("silent fallback must not surface an error event:\n%s", body)
	}
}

func TestHandleStreaming_EmptyResponseFallbackDisabled_KeepsOldBehavior(t *testing.T) {
	var callCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"VISIBLE_THINKING\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	cfg := &config.Config{
		APIKey: "test-key",
		OpenCodeGo: config.OpenCodeGoConfig{
			BaseURL:   upstream.URL,
			TimeoutMs: 300000,
		},
	}
	atomicCfg := config.NewAtomicConfig(cfg, "/tmp/test-config.json")
	ocClient := client.NewOpenCodeClient(atomicCfg, nil)

	handler := &MessagesHandler{
		client:              ocClient,
		logger:              slog.Default(),
		metrics:             metrics.New(),
		streamHandler:       transformer.NewStreamHandler(),
		requestTransformer:  transformer.NewRequestTransformer(),
		responseTransformer: transformer.NewResponseTransformer(),
		emptyRespFallback:   &config.EmptyResponseFallbackConfig{Enabled: n(false)},
	}

	rawBody := json.RawMessage(`{"model":"kimi-k2.6","stream":true,"max_tokens":256,"messages":[{"role":"user","content":"hello"}]}`)
	var anthropicReq types.MessageRequest
	if err := json.Unmarshal(rawBody, &anthropicReq); err != nil {
		t.Fatalf("unmarshal rawBody: %v", err)
	}

	chain := []config.ModelConfig{{Provider: "opencode-go", ModelID: "kimi-k2.6"}}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	handler.handleStreaming(recorder, req, &anthropicReq, &core.NormalizedRequest{Stream: true}, chain, rawBody, router.ScenarioDefault, "")

	body := recorder.Body.String()
	if got := atomic.LoadInt32(&callCount); got != 1 {
		t.Errorf("disabled feature must not retry, calls = %d", got)
	}
	if !strings.Contains(body, "VISIBLE_THINKING") {
		t.Errorf("disabled feature must stream thinking straight through:\n%s", body)
	}
	if !strings.Contains(body, `"type":"error"`) {
		t.Errorf("disabled feature keeps Task-2 behavior: visible error event expected:\n%s", body)
	}
}
```

- [ ] **Step 6: Run to verify failure**

Run: `go test ./internal/handlers/ -run 'ThinkingOnlyPrimary_FallsBackSilently|EmptyResponseFallbackDisabled' -v`
Expected: FAIL to compile — `emptyRespFallback` field undefined.

- [ ] **Step 7: Wire the handler**

In `internal/handlers/messages.go`:

a) Add field to `MessagesHandler` (after `storage` at line 50):

```go
	emptyRespFallback    *config.EmptyResponseFallbackConfig // optional: nil means enabled-by-default via accessor
```

b) Extend `NewMessagesHandler` signature (add trailing param) and assignment:

```go
func NewMessagesHandler(
	openCodeClient *client.OpenCodeClient,
	providerRegistry *core.ProviderRegistry,
	modelRouter *router.ModelRouter,
	fallbackHandler *router.FallbackHandler,
	tokenCounter *token.Counter,
	metrics *metrics.Metrics,
	captureLogger *debug.CaptureLogger,
	hist *history.History,
	storage StorageWriter,
	emptyRespFallback *config.EmptyResponseFallbackConfig,
) *MessagesHandler {
```

and in the returned literal:

```go
		storage:             storage,
		emptyRespFallback:   emptyRespFallback,
```

c) Arm per attempt in `handleStreaming`: immediately after the blocked-provider skip and **before** `h.logger.Info("attempting streaming model", ...)` (around line 643):

```go
		if h.emptyRespFallback.IsEnabled() {
			rw.ArmHoldback(h.emptyRespFallback.LimitBytes())
		}
```

(nil receiver is safe — `IsEnabled` handles nil.)

d) Discard on fallback: in the `handleStreamError` closure (lines 694-727), insert `rw.DiscardHoldback()` immediately before **each** of the three `return true // continue to next model` statements (idle branch, empty branch, generic-failure branch). Example for the empty branch:

```go
			if err == transformer.ErrEmptyStream {
				h.logger.Warn("upstream "+action+" stream empty, trying next model",
					"model", model.ModelID)
				if rw.ssePayloadWritten {
					h.sendStreamError(rw, "empty stream after SSE payload started")
					h.metrics.RecordFailureForModel(model.ModelID)
					return false // abort
				}
				rw.DiscardHoldback()
				return true // continue to next model
			}
```

(Abort branches leave the buffer alone: `ssePayloadWritten == true` implies the buffer already released.)

e) Chain-exhausted tail (lines 910-920): disarm before the final error write so the error event is not swallowed by an armed buffer. Insert `rw.DiscardHoldback()` immediately after `h.metrics.RecordFailure()`:

```go
	h.metrics.RecordFailure()
	rw.DiscardHoldback()
	if rw.ssePayloadWritten {
```

f) In `internal/server/server.go`, update the `handlers.NewMessagesHandler(...)` call (lines 117-128) to pass the config block as the last argument (adjust the receiver variable to whatever the atomic config handle is named in scope — it is used as `router.NewModelRouter(atomic, ...)` just above):

```go
	messagesHandler := handlers.NewMessagesHandler(
		openCodeClient,
		providerRegistry,
		modelRouter,
		fallbackHandler,
		tokenCounter,
		metrics,
		captureLogger,
		hist,
		storageWriter,
		atomic.Get().EmptyResponseFallback,
	)
```

- [ ] **Step 8: Run all tests**

Run: `go test ./... -race`
Expected: PASS — both new integration tests green; Task 2's `TestHandleStreaming_DefaultScenario_ThinkingOnly_AbortsWithError` still green (its single-model chain now exhausts, hits the tail, and still emits the error event after discard); entire repo suite green.

- [ ] **Step 9: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/handlers/messages.go internal/handlers/messages_test.go internal/server/server.go
git commit -m "feat(handlers): silently fall back when a stream produces no answer content"
```

---

### Task 5: Non-streaming empty-answer guard

**Files:**
- Modify: `internal/handlers/messages.go` (`handleNonStreaming` :1166-1236, helper near `extractTextFromBlocks`)
- Test: `internal/handlers/messages_test.go`

**Interfaces:**
- Consumes: `config.EmptyResponseFallbackConfig.IsEnabled()` (Task 4)
- Produces: `func responseHasAnswerContent(body []byte) bool` — true when the Anthropic-format body parses and contains a non-empty `text` block or any `tool_use` block; false for thinking-only/empty content; true for bodies that fail to parse (never guess on opaque payloads).

- [ ] **Step 1: Write failing tests**

Append to `internal/handlers/messages_test.go`:

```go
func TestResponseHasAnswerContent(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"invalid json counts as content", `not-json{`, true},
		{"empty content array", `{"id":"m","type":"message","role":"assistant","content":[],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":0}}`, false},
		{"thinking only", `{"id":"m","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"deep thoughts"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":0}}`, false},
		{"whitespace text", `{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"  "}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":0}}`, false},
		{"real text", `{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"answer"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`, true},
		{"tool use", `{"id":"m","type":"message","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := responseHasAnswerContent([]byte(tc.body)); got != tc.want {
				t.Errorf("responseHasAnswerContent() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHandleNonStreaming_EmptyAnswer_FallsBackToNextModel(t *testing.T) {
	var callCount int32
	bodies := []string{
		`{"id":"msg_1","type":"message","role":"assistant","model":"m1","content":[{"type":"thinking","thinking":"EMPTY_ANSWER_ATTEMPT"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":0}}`,
		`{"id":"msg_2","type":"message","role":"assistant","model":"m2","content":[{"type":"text","text":"REAL_NONSTREAM_ANSWER"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`,
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[min(int(count), len(bodies))-1]))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		APIKey: "test-key",
		OpenCodeGo: config.OpenCodeGoConfig{
			BaseURL:   upstream.URL,
			TimeoutMs: 300000,
		},
	}
	atomicCfg := config.NewAtomicConfig(cfg, "/tmp/test-config.json")
	ocClient := client.NewOpenCodeClient(atomicCfg, nil)

	handler := &MessagesHandler{
		client:              ocClient,
		logger:              slog.Default(),
		metrics:             metrics.New(),
		streamHandler:       transformer.NewStreamHandler(),
		requestTransformer:  transformer.NewRequestTransformer(),
		responseTransformer: transformer.NewResponseTransformer(),
		emptyRespFallback:   &config.EmptyResponseFallbackConfig{},
	}

	rawBody := json.RawMessage(`{"model":"qwen3.7-max","stream":false,"max_tokens":256,"messages":[{"role":"user","content":"hello"}]}`)
	var anthropicReq types.MessageRequest
	if err := json.Unmarshal(rawBody, &anthropicReq); err != nil {
		t.Fatalf("unmarshal rawBody: %v", err)
	}

	chain := []config.ModelConfig{
		{Provider: "opencode-go", ModelID: "qwen3.7-max"},
		{Provider: "opencode-go", ModelID: "minimax-m3"},
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	handler.handleNonStreaming(recorder, req, &anthropicReq, &core.NormalizedRequest{}, chain, rawBody, router.ScenarioDefault, "")

	if got := atomic.LoadInt32(&callCount); got != 2 {
		t.Errorf("expected 2 upstream calls (empty + fallback), got %d", got)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "REAL_NONSTREAM_ANSWER") {
		t.Errorf("fallback body missing from response:\n%s", body)
	}
	if strings.Contains(body, "EMPTY_ANSWER_ATTEMPT") {
		t.Errorf("failed attempt leaked into response:\n%s", body)
	}
	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", recorder.Code)
	}
}
```

Setup notes for the implementer: `qwen3.7-max` and `minimax-m3` take the raw-Anthropic passthrough path (`client.IsAnthropicModel`), so the httptest upstream receives the original Anthropic body and its raw JSON reply flows back verbatim — exactly what the assertions above expect. Mirror the setup style of `TestHandleNonStreaming_GoAnthropicModel_ReplacesModelInBody` (messages_test.go:1023) if request routing needs adjustment.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/handlers/ -run 'ResponseHasAnswerContent|NonStreaming_EmptyAnswer' -v`
Expected: FAIL to compile — `responseHasAnswerContent` undefined.

- [ ] **Step 3: Implement**

In `internal/handlers/messages.go`, add the helper near `extractTextFromBlocks` (bottom of file):

```go
// responseHasAnswerContent reports whether an Anthropic-format response body
// carries user-visible answer content: a non-empty text block or any tool_use
// block. Bodies that fail to parse count as content — opaque payloads are not
// classified as empty here.
func responseHasAnswerContent(body []byte) bool {
	var resp types.MessageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return true
	}
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				return true
			}
		case "tool_use":
			return true
		}
	}
	return false
}
```

Then in `handleNonStreaming` (starting line 1179), wrap the executor so each attempt validates before success is declared. Replace:

```go
	result, responseBody, err := h.fallbackHandler.ExecuteWithFallback(
		ctx,
		modelChain,
		func(ctx context.Context, model config.ModelConfig) ([]byte, error) {
```

with:

```go
	attemptFn := func(ctx context.Context, model config.ModelConfig) ([]byte, error) {
```

…leave the existing closure body untouched, close it after its final `}` as today, then add:

```go
	exec := attemptFn
	if h.emptyRespFallback.IsEnabled() {
		exec = func(ctx context.Context, model config.ModelConfig) ([]byte, error) {
			body, err := attemptFn(ctx, model)
			if err != nil {
				return nil, err
			}
			if !responseHasAnswerContent(body) {
				h.logger.Warn("model returned no answer content, trying next model", "model", model.ModelID)
				return nil, fmt.Errorf("%w: non-streaming response had no answer content", transformer.ErrEmptyStream)
			}
			return body, nil
		}
	}

	result, responseBody, err := h.fallbackHandler.ExecuteWithFallback(ctx, modelChain, exec)
```

`ExecuteWithFallback` (internal/router/fallback.go:201) feeds executor errors into the per-model circuit breaker and advances to the next model, and the chain-exhausted error path at messages.go:1228-1235 already maps to a 502 — no other changes needed.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/handlers/ -race -v`
Expected: PASS — both new tests plus full package green.

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/messages.go internal/handlers/messages_test.go
git commit -m "feat(handlers): fall back when non-streaming responses lack answer content"
```

---

### Task 6: Documentation + full verification

**Files:**
- Modify: `configs/config.example.json`
- Modify: `CLAUDE.md` (Long-running stream policy section)

**Interfaces:** none (docs only).

- [ ] **Step 1: Document the config block**

In `configs/config.example.json`, add next to the other provider/tuning blocks:

```json
  "empty_response_fallback": {
    "enabled": true,
    "holdback_limit_bytes": 32768
  },
```

In `CLAUDE.md`, append to the **Long-running stream policy** paragraph:

> **Empty-response fallback:** a stream that ends with reasoning but no answer content (zero text/tool_use blocks — observed on free-tier models during launch-week load) is treated as a model failure. While a model attempt is in flight the proxy withholds client-visible SSE bytes up to `empty_response_fallback.holdback_limit_bytes` (default 32 KiB), so a thinking-only stream can be discarded and retried on the next model without the client seeing it. Streams that exceed the cap stream through as today and surface a visible SSE error instead of a fake clean end. Disable with `"empty_response_fallback": {"enabled": false}`.

- [ ] **Step 2: Full verification**

Run:
```bash
go vet ./...
make test
make build
```
Expected: vet clean; all packages pass with `-race`; binary builds to `bin/routatic-proxy`.

Manual smoke (optional but recommended, proxy running locally on :3456 with a free-tier primary):
```bash
curl -sS -N http://127.0.0.1:3456/v1/messages \
  -H 'content-type: application/json' -H 'x-api-key: unused' -H 'anthropic-version: 2023-06-01' \
  -d '{"model":"ox-alpha-free","max_tokens":512,"stream":true,"messages":[{"role":"user","content":"Reply with exactly: ok"}]}' \
  | grep -c "message_start"   # expect exactly 1 even if the primary came back thinking-only
```

- [ ] **Step 3: Commit**

```bash
git add configs/config.example.json CLAUDE.md
git commit -m "docs: document empty_response_fallback configuration"
```

---

## Self-Review Checklist (completed during planning)

- **Spec coverage:** gap 1 → Task 1; gap 2 → Task 2; gap 3 → Tasks 3+4; non-streaming variant → Task 5; config gating + docs → Tasks 4+6. Circuit-breaker side effects come free via existing `RecordFailureForModel` / `ExecuteWithFallback` paths — noted, no extra task needed.
- **Placeholder scan:** every code step contains complete code; the only external references are existing test functions cited as harness patterns, with full standalone test code provided.
- **Type consistency:** `ArmHoldback(int)`/`DiscardHoldback()` names match between Task 3 definition and Task 4 usage; `EmptyResponseFallbackConfig` field/method names match across config, handler, server, and tests; `responseHasAnswerContent([]byte) bool` matches Task 5 usage.
