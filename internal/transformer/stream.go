package transformer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/routatic/proxy/pkg/types"
)

// ErrClientDisconnected is returned when the client disconnects during streaming.
var ErrClientDisconnected = fmt.Errorf("client disconnected")

// ErrStreamIdle is returned when no bytes arrive within idleTimeout on the
// upstream stream. The connection is stale (e.g. backend hang or network
// partition). The handler decides whether to fall back to another model.
var ErrStreamIdle = fmt.Errorf("upstream stream idle")

var ErrEmptyStream = fmt.Errorf("upstream returned empty stream")

const thinkingSignaturePlaceholder = "proxy-thinking-placeholder"

// readBufPool pools read buffers for streaming operations.
// sync.Pool reduces GC pressure under concurrent stream load by reusing
// 4KB buffers across goroutines instead of allocating fresh ones per read.
// Pool stores pointers to slices to avoid allocation on Put (SA6002).
var readBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 4096)
		return &b
	},
}

// IsIdleTimeout reports whether err is a read-timeout (network deadline
// exceeded on an otherwise live stream).
func IsIdleTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return false
}

// StreamHandler handles streaming SSE transformation from OpenAI to Anthropic format.
type StreamHandler struct {
	responseTransformer *ResponseTransformer
}

// NewStreamHandler creates a new stream handler.
func NewStreamHandler() *StreamHandler {
	return &StreamHandler{
		responseTransformer: NewResponseTransformer(),
	}
}

// EmitMessageResponse synthesizes an Anthropic-format SSE stream from a non-streaming
// MessageResponse. This is used for vision scenarios where the upstream model does not
// support streaming — the proxy fetches the full response, then emits it as SSE events
// so the client's streaming contract is preserved.
func (h *StreamHandler) EmitMessageResponse(w http.ResponseWriter, resp *types.MessageResponse) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported by response writer")
	}
	if resp == nil {
		return fmt.Errorf("nil message response")
	}
	msgStart := types.MessageEvent{
		Type:    "message_start",
		Message: resp,
	}
	if err := writeSSEEvent(w, msgStart); err != nil {
		return ErrClientDisconnected
	}
	flusher.Flush()

	for i, block := range resp.Content {
		idx := i
		startBlock := block
		switch block.Type {
		case "text":
			startBlock.Text = ""
		case "thinking":
			startBlock.Thinking = ""
			// Signature deltas accumulate onto the start block, so the start
			// block must open empty — the real signature arrives as the
			// signature_delta emitted before content_block_stop below.
			startBlock.Signature = ""
		case "tool_use":
			startBlock.Input = json.RawMessage(`{}`)
		}
		if err := writeSSEEvent(w, types.MessageEvent{
			Type:         "content_block_start",
			Index:        &idx,
			ContentBlock: &startBlock,
		}); err != nil {
			return ErrClientDisconnected
		}
		switch block.Type {
		case "text":
			if block.Text != "" {
				if err := writeSSEEvent(w, types.MessageEvent{
					Type:  "content_block_delta",
					Index: &idx,
					Delta: &types.Delta{Type: "text_delta", Text: block.Text},
				}); err != nil {
					return ErrClientDisconnected
				}
			}
		case "thinking":
			if block.Thinking != "" {
				if err := writeSSEEvent(w, types.MessageEvent{
					Type:  "content_block_delta",
					Index: &idx,
					Delta: &types.Delta{Type: "thinking_delta", Thinking: block.Thinking},
				}); err != nil {
					return ErrClientDisconnected
				}
			}
			signature := block.Signature
			if signature == "" {
				signature = thinkingSignaturePlaceholder
			}
			if err := writeSSEEvent(w, types.MessageEvent{
				Type:  "content_block_delta",
				Index: &idx,
				Delta: &types.Delta{Type: "signature_delta", Signature: signature},
			}); err != nil {
				return ErrClientDisconnected
			}
		case "tool_use":
			if len(block.Input) > 0 {
				if err := writeSSEEvent(w, types.MessageEvent{
					Type:  "content_block_delta",
					Index: &idx,
					Delta: &types.Delta{Type: "input_json_delta", PartialJSON: string(block.Input)},
				}); err != nil {
					return ErrClientDisconnected
				}
			}
		}
		if err := writeSSEEvent(w, types.MessageEvent{
			Type:  "content_block_stop",
			Index: &idx,
		}); err != nil {
			return ErrClientDisconnected
		}
		flusher.Flush()
	}

	stopReason := resp.StopReason
	if stopReason == "" {
		stopReason = "end_turn"
	}
	if err := writeSSEEvent(w, types.MessageEvent{
		Type: "message_delta",
		Delta: &types.Delta{
			StopReason: stopReason,
		},
		Usage: &types.Usage{
			InputTokens:              resp.Usage.InputTokens,
			OutputTokens:             resp.Usage.OutputTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		},
	}); err != nil {
		return ErrClientDisconnected
	}
	if err := writeSSEEvent(w, types.MessageEvent{Type: "message_stop"}); err != nil {
		return ErrClientDisconnected
	}
	flusher.Flush()
	return nil
}

// ProxyStream takes an OpenAI streaming response and writes Anthropic-format SSE to the writer.
// It reads OpenAI ChatCompletionChunk SSE events and transforms them into Anthropic MessageEvent SSE events.
// The streamCtx is the per-model attempt context (carries streaming_timeout_ms); the caller
// should wrap openaiResp with NewCtxReadCloser so the body read also respects the deadline.
//
// CRITICAL: This function reads directly from resp.Body without buffering to minimize latency.
// Per deep research: "Don't use bufio.Scanner or bufio.Reader on the response body - it adds buffering"
//
// idleTimeout is the maximum gap between bytes on the upstream stream. The
// stream lives as long as data keeps flowing; only an idle period longer than
// idleTimeout is treated as a stuck connection and surfaces as ErrStreamIdle.
// Pass 0 to disable (stream lives until EOF or error).
func (h *StreamHandler) ProxyStream(
	w http.ResponseWriter,
	openaiResp io.ReadCloser,
	originalModel string,
	clientCtx context.Context,
	idleTimeout time.Duration,
	cancel context.CancelFunc,
) error {
	defer func() { _ = openaiResp.Close() }()
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported by response writer")
	}

	// Generate a unique message ID for this stream.
	msgID := "msg_" + generateID()

	// Send message_start event with the full message envelope.
	msgStart := types.MessageEvent{
		Type: "message_start",
		Message: &types.MessageResponse{
			ID:      msgID,
			Type:    "message",
			Role:    "assistant",
			Content: []types.ContentBlock{},
			Model:   originalModel,
		},
	}
	if err := writeSSEEvent(w, msgStart); err != nil {
		return ErrClientDisconnected
	}
	flusher.Flush()

	// Read directly from response body without buffering.
	// Use a tight loop with a line buffer - no bufio.Reader.
	contentIndex := 0
	var lineBuf []byte
	contentStarted := false
	reasoningStarted := false
	terminalStopReason := ""
	var terminalUsage *types.UsageInfo
	toolUseCount := 0
	startedToolCalls := make(map[int]int) // maps OpenAI tool call index → Anthropic content block index
	decodeErrors := 0                     // consecutive SSE decode failures

	// Get a buffer from the pool; return it when done.
	readBuf := readBufPool.Get().(*[]byte)
	defer readBufPool.Put(readBuf)

	// Start the idle watchdog. Each successful read pings the watchdog so
	// the stream lives as long as data keeps flowing. If no bytes arrive
	// within idleTimeout, cancel() is called, which aborts the upstream
	// HTTP request and causes the next Read to return a context error.
	ping := StartIdleWatchdog(clientCtx, cancel, idleTimeout)

	// finishStream closes whatever blocks are still open and writes the single
	// terminal message_delta plus message_stop. It runs both on a clean end of
	// stream and on an upstream failure that arrives after finish_reason.
	finishStream := func() error {
		if _, err := closeOpenBlock(w, contentIndex, &contentStarted, &reasoningStarted); err != nil {
			return ErrClientDisconnected
		}

		// Send stop events for any tool blocks not yet closed (e.g. upstream
		// disconnected without sending a finish_reason chunk).
		if len(startedToolCalls) > 0 {
			blockIndices := make([]int, 0, len(startedToolCalls))
			for _, blockIdx := range startedToolCalls {
				blockIndices = append(blockIndices, blockIdx)
			}
			sort.Ints(blockIndices)
			for _, blockIdx := range blockIndices {
				if err := writeContentBlockStop(w, blockIdx); err != nil {
					return ErrClientDisconnected
				}
			}
		}

		// Anthropic expects one terminal message_delta containing both
		// stop_reason and usage. OpenAI-compatible providers commonly send
		// those in separate chunks, so both are retained until the stream ends.
		stopReason := terminalStopReason
		if stopReason == "" {
			stopReason = "end_turn"
			if len(startedToolCalls) > 0 {
				stopReason = "tool_use"
			}
		}
		msgDelta := types.MessageEvent{
			Type: "message_delta",
			Delta: &types.Delta{
				StopReason: stopReason,
			},
			Usage: usageInfoToAnthropic(terminalUsage),
		}
		if err := writeSSEEvent(w, msgDelta); err != nil {
			return ErrClientDisconnected
		}

		// Send message_stop event to signal stream completion.
		if err := writeSSEEvent(w, types.MessageEvent{Type: "message_stop"}); err != nil {
			return ErrClientDisconnected
		}
		flusher.Flush()
		return nil
	}

	for {
		// Check if client disconnected
		select {
		case <-clientCtx.Done():
			return ErrClientDisconnected
		default:
		}

		// Read chunk from upstream
		n, err := openaiResp.Read(*readBuf)
		if n > 0 {
			// Data is flowing — reset the idle watchdog so the stream
			// lives as long as data keeps arriving.
			ping()
			// Process bytes immediately
			for i := 0; i < n; i++ {
				b := (*readBuf)[i]
				if b == '\n' {
					// Process complete line
					if err := h.processSSELine(w, flusher, lineBuf, &contentIndex, &contentStarted, &reasoningStarted, &terminalStopReason, &terminalUsage, &toolUseCount, startedToolCalls, originalModel, &decodeErrors); err != nil {
						return err
					}
					lineBuf = lineBuf[:0]
				} else {
					lineBuf = append(lineBuf, b)
				}
			}
		}

		if err == io.EOF {
			// Process any remaining data in buffer
			if len(lineBuf) > 0 {
				if err := h.processSSELine(w, flusher, lineBuf, &contentIndex, &contentStarted, &reasoningStarted, &terminalStopReason, &terminalUsage, &toolUseCount, startedToolCalls, originalModel, &decodeErrors); err != nil {
					return err
				}
			}
			break
		}
		if err != nil {
			// A read failure after the upstream already sent finish_reason
			// means the answer is complete and only the trailing bytes were
			// lost. Flush the terminal events and report success — returning
			// an error here would discard a finished response and make the
			// handler retry the whole turn on the next model.
			if terminalStopReason != "" {
				return finishStream()
			}
			if IsIdleTimeout(err) {
				return ErrStreamIdle
			}
			// When the idle watchdog fires, it cancels the upstream context
			// which produces context.Canceled on Read. Distinguish that
			// from a client disconnect by checking clientCtx.
			if (errors.Is(err, context.Canceled) || errors.Is(err, ErrStreamReadCanceled)) && clientCtx.Err() == nil {
				return ErrStreamIdle
			}
			return fmt.Errorf("failed to read stream: %w", err)
		}
	}

	return finishStream()
}

// processSSELine processes a single SSE line from upstream.
// Per deep research: "Treat SSE primarily as a text protocol" - minimize JSON parsing.
func (h *StreamHandler) processSSELine(
	w http.ResponseWriter,
	flusher http.Flusher,
	line []byte,
	contentIndex *int,
	contentStarted *bool,
	reasoningStarted *bool,
	terminalStopReason *string,
	terminalUsage **types.UsageInfo,
	toolUseCount *int,
	startedToolCalls map[int]int,
	originalModel string,
	decodeErrors *int,
) error {
	line = bytes.TrimSpace(line)

	// Skip empty lines
	if len(line) == 0 {
		return nil
	}

	// Skip non-data lines (event: lines, id: lines, etc.)
	if !bytes.HasPrefix(line, []byte("data: ")) {
		return nil
	}

	data := line[6:]
	if len(data) == 0 {
		return nil
	}

	// Handle [DONE] marker
	if bytes.Equal(data, []byte("[DONE]")) {
		return nil
	}

	// Fast path: check if this is a content chunk without full JSON parsing.
	// Skip the fast path when reasoning_content is also present in the same
	// chunk — falling through to JSON parsing ensures both fields are handled
	// correctly. Otherwise reasoning_content gets silently dropped, and on the
	// next turn DeepSeek rejects the request with:
	//   "The reasoning_content in the thinking mode must be passed back to the API."
	if !bytes.Contains(data, []byte(`"reasoning_content"`)) &&
		!bytes.Contains(data, []byte(`"finish_reason"`)) &&
		!bytes.Contains(data, []byte(`"tool_calls"`)) &&
		!bytes.Contains(data, []byte(`"usage"`)) {
		if idx := bytes.Index(data, []byte(`"delta":{"content":"`)); idx != -1 {
			// Walk past JSON escape sequences to find the real closing
			// quote. A naive strings.Index would stop at an escaped
			// \" inside the content.
			start := idx + len(`"delta":{"content":"`)
			suffix := data[start:]
			end := -1
			for i := 0; i < len(suffix); i++ {
				if suffix[i] == '\\' {
					i++ // skip the escaped character
					continue
				}
				if suffix[i] == '"' {
					end = i
					break
				}
			}
			if end != -1 {
				content := data[start : start+end]
				if len(content) > 0 {
					if !*contentStarted {
						// If reasoning was already started, close it first
						closed, err := closeOpenBlock(w, *contentIndex, contentStarted, reasoningStarted)
						if err != nil {
							return ErrClientDisconnected
						}
						if closed {
							*contentIndex++
						}
						*contentStarted = true
						// Send content_block_start
						startEvent := types.MessageEvent{
							Type:         "content_block_start",
							Index:        contentIndex,
							ContentBlock: &types.ContentBlock{Type: "text", Text: ""},
						}
						if err := writeSSEEvent(w, startEvent); err != nil {
							return ErrClientDisconnected
						}
					}

					// Send content_block_delta
					delta := types.Delta{
						Type: "text_delta",
						Text: string(content),
					}
					event := types.MessageEvent{
						Type:  "content_block_delta",
						Index: contentIndex,
						Delta: &delta,
					}
					if err := writeSSEEvent(w, event); err != nil {
						return ErrClientDisconnected
					}
					flusher.Flush()
				}
				// Valid SSE line accepted via fast path — reset the
				// consecutive decode failure counter so interleaved valid
				// chunks don't accumulate spurious "too many failures".
				*decodeErrors = 0
				return nil
			}
		}
	}

	// For tool calls and other complex cases, fall back to full JSON parsing
	var chunk types.ChatCompletionChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		// Track consecutive decode failures. A transient glitch is tolerated,
		// but persistent corruption terminates the stream rather than silently
		// dropping content.
		*decodeErrors++
		if *decodeErrors > 3 {
			return fmt.Errorf("too many consecutive SSE decode failures (%d)", *decodeErrors)
		}
		return nil
	}
	*decodeErrors = 0
	if chunk.Usage != nil {
		*terminalUsage = chunk.Usage
	}

	if len(chunk.Choices) == 0 {
		return nil
	}

	choice := chunk.Choices[0]

	// Handle reasoning content deltas
	if choice.Delta.ReasoningContent != nil && *choice.Delta.ReasoningContent != "" {
		if !*reasoningStarted {
			// If text was already started, close it first
			if *contentStarted {
				stopEvent := types.MessageEvent{
					Type:  "content_block_stop",
					Index: contentIndex,
				}
				if err := writeSSEEvent(w, stopEvent); err != nil {
					return ErrClientDisconnected
				}
				*contentIndex++
				*contentStarted = false
			}
			*reasoningStarted = true
			startEvent := types.MessageEvent{
				Type:         "content_block_start",
				Index:        contentIndex,
				ContentBlock: &types.ContentBlock{Type: "thinking", Thinking: ""},
			}
			if err := writeSSEEvent(w, startEvent); err != nil {
				return ErrClientDisconnected
			}
		}

		delta := types.Delta{
			Type:     "thinking_delta",
			Thinking: *choice.Delta.ReasoningContent,
		}
		event := types.MessageEvent{
			Type:  "content_block_delta",
			Index: contentIndex,
			Delta: &delta,
		}
		if err := writeSSEEvent(w, event); err != nil {
			return ErrClientDisconnected
		}
		flusher.Flush()
	}

	// Handle text content deltas
	if textContent := choice.Delta.ContentText(); textContent != "" {
		if !*contentStarted {
			// If reasoning was already started, close it first
			closed, err := closeOpenBlock(w, *contentIndex, contentStarted, reasoningStarted)
			if err != nil {
				return ErrClientDisconnected
			}
			if closed {
				*contentIndex++
			}
			*contentStarted = true
			startEvent := types.MessageEvent{
				Type:         "content_block_start",
				Index:        contentIndex,
				ContentBlock: &types.ContentBlock{Type: "text", Text: ""},
			}
			if err := writeSSEEvent(w, startEvent); err != nil {
				return ErrClientDisconnected
			}
		}

		delta := types.Delta{
			Type: "text_delta",
			Text: textContent,
		}
		event := types.MessageEvent{
			Type:  "content_block_delta",
			Index: contentIndex,
			Delta: &delta,
		}
		if err := writeSSEEvent(w, event); err != nil {
			return ErrClientDisconnected
		}
		flusher.Flush()
	}

	// Handle tool call deltas.
	// OpenAI streams tool calls incrementally: the first chunk for a given
	// tool call carries id + name (+ possibly empty arguments), subsequent
	// chunks carry only incremental arguments.  We must create exactly one
	// content_block_start per tool call, then stream deltas for it.
	if len(choice.Delta.ToolCalls) > 0 {
		for _, tc := range choice.Delta.ToolCalls {
			oi := tc.Index // OpenAI tool_calls array index

			blockIdx, exists := startedToolCalls[oi]
			if !exists {
				if tc.Function.Name == "" {
					// Ghost chunk: this index was closed and recycled, but
					// has no name/id. Ignore — the real tool call was
					// already fully processed.
					continue
				}
				// Close any existing content/reasoning block before opening the
				// tool block. Whether one was open decides if contentIndex
				// advances below.
				hadStartedBlock, err := closeOpenBlock(w, *contentIndex, contentStarted, reasoningStarted)
				if err != nil {
					return ErrClientDisconnected
				}
				// First time seeing this logical tool call — start a new block.
				// Only increment contentIndex when a previous text or reasoning
				// block was already started, OR when a prior tool call has already
				// claimed index 0 (parallel or sequential tool calls).  If nothing
				// was started yet (single-tool response), the first tool block
				// keeps contentIndex at 0 so the Anthropic SSE content block
				// indices are contiguous.
				if hadStartedBlock || len(startedToolCalls) > 0 {
					*contentIndex++
				}
				*toolUseCount++
				blockIdx = *contentIndex
				startedToolCalls[oi] = blockIdx

				toolID := tc.ID
				if toolID == "" {
					toolID = fmt.Sprintf("toolu_%s", generateID())
				}
				startEvent := types.MessageEvent{
					Type:  "content_block_start",
					Index: &blockIdx,
					ContentBlock: &types.ContentBlock{
						Type:  "tool_use",
						ID:    toolID,
						Name:  tc.Function.Name,
						Input: json.RawMessage(`{}`),
					},
				}
				if err := writeSSEEvent(w, startEvent); err != nil {
					return ErrClientDisconnected
				}
			}

			// Send argument delta (if any) — whether new or continuation.
			if tc.Function.Arguments != "" {
				delta := types.Delta{
					Type:        "input_json_delta",
					PartialJSON: tc.Function.Arguments,
				}
				event := types.MessageEvent{
					Type:  "content_block_delta",
					Index: &blockIdx,
					Delta: &delta,
				}
				if err := writeSSEEvent(w, event); err != nil {
					return ErrClientDisconnected
				}
			}
			flusher.Flush()
		}
	}

	// Handle finish reason
	if choice.FinishReason != "" {
		// Close any open content block (reasoning or text)
		if _, err := closeOpenBlock(w, *contentIndex, contentStarted, reasoningStarted); err != nil {
			return ErrClientDisconnected
		}

		// Close any open tool_use blocks in ascending index order.
		if len(startedToolCalls) > 0 {
			type toolBlockEntry struct {
				oi       int
				blockIdx int
			}
			entries := make([]toolBlockEntry, 0, len(startedToolCalls))
			for oi, blockIdx := range startedToolCalls {
				entries = append(entries, toolBlockEntry{oi, blockIdx})
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].blockIdx < entries[j].blockIdx
			})
			for _, e := range entries {
				idx := e.blockIdx
				stopEvent := types.MessageEvent{
					Type:  "content_block_stop",
					Index: &idx,
				}
				if err := writeSSEEvent(w, stopEvent); err != nil {
					return ErrClientDisconnected
				}
			}
			// Clear so EOF cleanup won't emit duplicate stops
			for oi := range startedToolCalls {
				delete(startedToolCalls, oi)
			}
		}
		*toolUseCount = 0

		*terminalStopReason = h.responseTransformer.mapFinishReason(choice.FinishReason)
		flusher.Flush()
	}

	return nil
}

func usageInfoToAnthropic(usage *types.UsageInfo) *types.Usage {
	if usage == nil {
		return &types.Usage{
			InputTokens:  0,
			OutputTokens: 0,
		}
	}
	return &types.Usage{
		// Per Anthropic Messages API spec, `input_tokens` is the count of
		// regular input tokens — i.e. tokens that were neither read from the
		// cache nor written to the cache this turn. OpenAI's `prompt_tokens`
		// is the *total* prompt size. We must subtract the cache parts here
		// for the same reason TransformResponse does — see the longer comment
		// in response.go.
		InputTokens:              nonNegative(usage.PromptTokens - usage.CacheReadTokens() - usage.CacheCreationTokens()),
		OutputTokens:             usage.CompletionTokens,
		CacheCreationInputTokens: usage.CacheCreationTokens(),
		CacheReadInputTokens:     usage.CacheReadTokens(),
	}
}

// responsesUsageToAnthropic maps Responses API usage to Anthropic semantics
// per the same convention documented on usageInfoToAnthropic: input_tokens
// counts only non-cached input, and input_tokens_details.cached_tokens
// surfaces as cache_read_input_tokens.
func responsesUsageToAnthropic(usage *types.ResponsesUsage) *types.Usage {
	if usage == nil {
		return &types.Usage{
			InputTokens:  0,
			OutputTokens: 0,
		}
	}
	cached := 0
	if usage.InputTokensDetails != nil {
		cached = usage.InputTokensDetails.CachedTokens
	}
	return &types.Usage{
		InputTokens:          nonNegative(usage.InputTokens - cached),
		OutputTokens:         usage.OutputTokens,
		CacheReadInputTokens: cached,
	}
}

// writeContentBlockStop writes a content_block_stop SSE event at the given index.
func writeContentBlockStop(w http.ResponseWriter, index int) error {
	return writeSSEEvent(w, types.MessageEvent{
		Type:  "content_block_stop",
		Index: &index,
	})
}

// closeThinkingBlock ends a thinking block at the given index. Clients running
// the extended-thinking beta require a non-empty signature_delta before the
// stop, and discard the whole response when it is missing.
func closeThinkingBlock(w http.ResponseWriter, index int) error {
	if err := writeSSEEvent(w, types.MessageEvent{
		Type:  "content_block_delta",
		Index: &index,
		Delta: &types.Delta{Type: "signature_delta", Signature: thinkingSignaturePlaceholder},
	}); err != nil {
		return err
	}
	return writeContentBlockStop(w, index)
}

// closeOpenBlock closes whichever of the thinking or text block is currently
// open at index and clears its flag, reporting whether anything was closed so
// the caller can decide about advancing the content index.
func closeOpenBlock(w http.ResponseWriter, index int, contentStarted, reasoningStarted *bool) (bool, error) {
	switch {
	case *reasoningStarted:
		*reasoningStarted = false
		return true, closeThinkingBlock(w, index)
	case *contentStarted:
		*contentStarted = false
		return true, writeContentBlockStop(w, index)
	default:
		return false, nil
	}
}

// writeSSEEvent writes a single SSE event to the HTTP response writer.
// Format: "event: <type>\ndata: <json>\n\n"
func writeSSEEvent(w http.ResponseWriter, event types.MessageEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, string(data))
	return err
}

// generateID creates a unique identifier based on current time.
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// responsesStreamState tracks the Anthropic content-block lifecycle while
// converting an OpenAI Responses SSE stream. The Responses grammar addresses
// output items by upstream item_id and interleaves text, reasoning, and
// function_call items; Anthropic SSE requires contiguous block indices with
// exactly one content_block_stop per started block.
type responsesStreamState struct {
	nextIndex  int  // next Anthropic content block index to assign
	textOpen   bool // a text block is open at textIndex
	textIndex  int
	toolBlocks map[string]int  // upstream item key → Anthropic block index
	openTools  []string        // item keys of tool blocks not yet stopped, in open order
	argsSent   map[string]bool // item key → any input_json_delta emitted
	toolCount  int             // tool_use blocks started this stream
	usage      *types.ResponsesUsage
	stopSent   bool // terminal message_delta already emitted
}

func newResponsesStreamState() *responsesStreamState {
	return &responsesStreamState{
		toolBlocks: make(map[string]int),
		argsSent:   make(map[string]bool),
	}
}

// closeTextBlock stops the open text block, if any.
func (s *responsesStreamState) closeTextBlock(w http.ResponseWriter) error {
	if !s.textOpen {
		return nil
	}
	s.textOpen = false
	return writeContentBlockStop(w, s.textIndex)
}

// startToolBlock closes any open text block and opens a tool_use block for the
// given upstream item key.
func (s *responsesStreamState) startToolBlock(w http.ResponseWriter, key, toolID, name string) error {
	if err := s.closeTextBlock(w); err != nil {
		return err
	}
	idx := s.nextIndex
	s.nextIndex++
	s.toolBlocks[key] = idx
	s.openTools = append(s.openTools, key)
	s.toolCount++
	return writeSSEEvent(w, types.MessageEvent{
		Type:  "content_block_start",
		Index: &idx,
		ContentBlock: &types.ContentBlock{
			Type:  "tool_use",
			ID:    toolID,
			Name:  name,
			Input: json.RawMessage(`{}`),
		},
	})
}

// resolveTool maps an upstream item key to its still-open block. An empty key
// falls back to the most recently opened open tool block; unknown or already
// stopped keys resolve to false (callers skip them silently).
func (s *responsesStreamState) resolveTool(key string) (string, int, bool) {
	if key != "" {
		idx, known := s.toolBlocks[key]
		if !known {
			return "", 0, false
		}
		for _, k := range s.openTools {
			if k == key {
				return key, idx, true
			}
		}
		return "", 0, false // already stopped
	}
	if len(s.openTools) == 0 {
		return "", 0, false
	}
	last := s.openTools[len(s.openTools)-1]
	return last, s.toolBlocks[last], true
}

// emitToolDelta streams one input_json_delta fragment onto the block for key.
func (s *responsesStreamState) emitToolDelta(w http.ResponseWriter, key, partial string) error {
	k, idx, ok := s.resolveTool(key)
	if !ok {
		return nil
	}
	s.argsSent[k] = true
	return writeSSEEvent(w, types.MessageEvent{
		Type:  "content_block_delta",
		Index: &idx,
		Delta: &types.Delta{Type: "input_json_delta", PartialJSON: partial},
	})
}

// stopToolBlock stops the block for key exactly once (both
// response.function_call_arguments.done and response.output_item.done may
// fire for the same item). When no argument deltas were streamed but the full
// arguments string is known — done events carry it — it is emitted as a
// single delta first so the client still receives the tool input.
func (s *responsesStreamState) stopToolBlock(w http.ResponseWriter, key, fullArgs string) error {
	k, idx, ok := s.resolveTool(key)
	if !ok {
		return nil
	}
	if !s.argsSent[k] && fullArgs != "" {
		if err := writeSSEEvent(w, types.MessageEvent{
			Type:  "content_block_delta",
			Index: &idx,
			Delta: &types.Delta{Type: "input_json_delta", PartialJSON: fullArgs},
		}); err != nil {
			return err
		}
	}
	for i, open := range s.openTools {
		if open == k {
			s.openTools = append(s.openTools[:i], s.openTools[i+1:]...)
			break
		}
	}
	return writeContentBlockStop(w, idx)
}

// closeToolsFromResponse finalizes tool blocks still open when
// response.completed/response.done arrives, using the full output items the
// terminal event carries. This covers upstreams that skip the per-item done
// events entirely.
func (s *responsesStreamState) closeToolsFromResponse(w http.ResponseWriter, resp *types.ResponsesResponse) error {
	if resp == nil {
		return nil
	}
	for i := range resp.Output {
		item := &resp.Output[i]
		if item.Type != "function_call" {
			continue
		}
		key := responsesToolItemKey(item)
		if _, _, ok := s.resolveTool(key); ok {
			if err := s.stopToolBlock(w, key, item.Arguments); err != nil {
				return err
			}
		}
	}
	return nil
}

// finish closes every open block (text first, then tool blocks in ascending
// index order) and emits the terminal message_delta, at most once. It runs at
// response.completed/response.done and again at EOF as a no-op guard.
func (s *responsesStreamState) finish(w http.ResponseWriter, flusher http.Flusher) error {
	if err := s.closeTextBlock(w); err != nil {
		return err
	}
	if len(s.openTools) > 0 {
		indices := make([]int, 0, len(s.openTools))
		for _, k := range s.openTools {
			indices = append(indices, s.toolBlocks[k])
		}
		s.openTools = nil
		sort.Ints(indices)
		for _, idx := range indices {
			if err := writeContentBlockStop(w, idx); err != nil {
				return err
			}
		}
	}
	if !s.stopSent {
		s.stopSent = true
		stopReason := "end_turn"
		if s.toolCount > 0 {
			stopReason = "tool_use"
		}
		if err := writeSSEEvent(w, types.MessageEvent{
			Type:  "message_delta",
			Delta: &types.Delta{StopReason: stopReason},
			Usage: responsesUsageToAnthropic(s.usage),
		}); err != nil {
			return err
		}
		flusher.Flush()
	}
	return nil
}

// responsesToolItemKey derives the correlation key for a function_call output
// item. Streaming events address items by item_id, which is the item's id
// field; call_id is a fallback for upstreams that omit it.
func responsesToolItemKey(item *types.ResponsesOutput) string {
	if item.ID != "" {
		return item.ID
	}
	return item.CallID
}

// ProxyResponsesStream takes an OpenAI Responses streaming response and writes Anthropic-format SSE.
// streamCtx is the per-model attempt context (carries streaming_timeout_ms); the caller should
// wrap responsesResp with NewCtxReadCloser so the body read also respects the deadline.
func (h *StreamHandler) ProxyResponsesStream(
	w http.ResponseWriter,
	responsesResp io.ReadCloser,
	originalModel string,
	clientCtx context.Context,
	idleTimeout time.Duration,
	cancel context.CancelFunc,
) error {
	defer func() { _ = responsesResp.Close() }()
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported by response writer")
	}

	msgID := "msg_" + generateID()
	msgStart := types.MessageEvent{
		Type: "message_start",
		Message: &types.MessageResponse{
			ID:      msgID,
			Type:    "message",
			Role:    "assistant",
			Content: []types.ContentBlock{},
			Model:   originalModel,
		},
	}
	if err := writeSSEEvent(w, msgStart); err != nil {
		return ErrClientDisconnected
	}
	flusher.Flush()

	st := newResponsesStreamState()
	var lineBuf []byte
	readBuf := readBufPool.Get().(*[]byte)
	defer readBufPool.Put(readBuf)

	ping := StartIdleWatchdog(clientCtx, cancel, idleTimeout)

	for {
		select {
		case <-clientCtx.Done():
			return ErrClientDisconnected
		default:
		}

		n, err := responsesResp.Read(*readBuf)
		if n > 0 {
			ping()
			for i := 0; i < n; i++ {
				b := (*readBuf)[i]
				if b == '\n' {
					if err := h.processResponsesSSELine(w, flusher, lineBuf, st); err != nil {
						return err
					}
					lineBuf = lineBuf[:0]
				} else {
					lineBuf = append(lineBuf, b)
				}
			}
		}

		if err == io.EOF {
			if len(lineBuf) > 0 {
				if err := h.processResponsesSSELine(w, flusher, lineBuf, st); err != nil {
					return err
				}
			}
			break
		}
		if err != nil {
			if IsIdleTimeout(err) {
				return ErrStreamIdle
			}
			if (errors.Is(err, context.Canceled) || errors.Is(err, ErrStreamReadCanceled)) && clientCtx.Err() == nil {
				return ErrStreamIdle
			}
			return fmt.Errorf("failed to read stream: %w", err)
		}
	}

	// Streams that ended without response.completed still need every started
	// block stopped exactly once and a terminal message_delta. When the
	// terminal event already arrived, finish is a no-op guard.
	if err := st.finish(w, flusher); err != nil {
		return ErrClientDisconnected
	}

	stopEvent := types.MessageEvent{
		Type: "message_stop",
	}
	if err := writeSSEEvent(w, stopEvent); err != nil {
		return ErrClientDisconnected
	}
	flusher.Flush()

	return nil
}

func (h *StreamHandler) processResponsesSSELine(
	w http.ResponseWriter,
	flusher http.Flusher,
	line []byte,
	st *responsesStreamState,
) error {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || !bytes.HasPrefix(line, []byte("data: ")) {
		return nil
	}

	data := line[6:]
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return nil
	}

	var chunk types.ResponsesChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		// Malformed payload — skip silently, matching the tolerant handling
		// of unhandled event types.
		return nil
	}

	switch chunk.Type {
	case "response.output_text.delta":
		if chunk.Delta == "" {
			return nil
		}
		if !st.textOpen {
			st.textOpen = true
			st.textIndex = st.nextIndex
			st.nextIndex++
			startEvent := types.MessageEvent{
				Type:         "content_block_start",
				Index:        &st.textIndex,
				ContentBlock: &types.ContentBlock{Type: "text", Text: ""},
			}
			if err := writeSSEEvent(w, startEvent); err != nil {
				return ErrClientDisconnected
			}
		}
		delta := types.Delta{
			Type: "text_delta",
			Text: chunk.Delta,
		}
		event := types.MessageEvent{
			Type:  "content_block_delta",
			Index: &st.textIndex,
			Delta: &delta,
		}
		if err := writeSSEEvent(w, event); err != nil {
			return ErrClientDisconnected
		}
		flusher.Flush()

	case "response.output_item.added":
		if chunk.Item == nil || chunk.Item.Type != "function_call" {
			return nil
		}
		key := responsesToolItemKey(chunk.Item)
		if key == "" {
			// Neither id nor call_id — later deltas cannot be correlated.
			return nil
		}
		if _, _, open := st.resolveTool(key); open {
			// Duplicate added event for an item already in flight.
			return nil
		}
		toolID := chunk.Item.CallID
		if toolID == "" {
			toolID = chunk.Item.ID
		}
		if toolID == "" {
			toolID = fmt.Sprintf("toolu_%s", generateID())
		}
		if err := st.startToolBlock(w, key, toolID, chunk.Item.Name); err != nil {
			return ErrClientDisconnected
		}
		flusher.Flush()

	case "response.function_call_arguments.delta":
		if chunk.Delta == "" {
			return nil
		}
		if err := st.emitToolDelta(w, chunk.ItemID, chunk.Delta); err != nil {
			return ErrClientDisconnected
		}
		flusher.Flush()

	case "response.function_call_arguments.done":
		if err := st.stopToolBlock(w, chunk.ItemID, chunk.Arguments); err != nil {
			return ErrClientDisconnected
		}
		flusher.Flush()

	case "response.output_item.done":
		if chunk.Item == nil || chunk.Item.Type != "function_call" {
			return nil
		}
		if err := st.stopToolBlock(w, responsesToolItemKey(chunk.Item), chunk.Item.Arguments); err != nil {
			return ErrClientDisconnected
		}
		flusher.Flush()

	case "response.completed", "response.done":
		if chunk.Response != nil {
			// Only capture non-zero usage so a later bare response.done does
			// not clobber the numbers from response.completed.
			if u := chunk.Response.Usage; u.InputTokens > 0 || u.OutputTokens > 0 {
				st.usage = &u
			}
			if err := st.closeToolsFromResponse(w, chunk.Response); err != nil {
				return ErrClientDisconnected
			}
		}
		if err := st.finish(w, flusher); err != nil {
			return ErrClientDisconnected
		}
	}

	// All other event types (response.created, response.in_progress,
	// reasoning summary deltas, ping, ...) are skipped: they carry content we
	// do not forward, and no partial Anthropic SSE may be emitted for them.
	return nil
}

// ProxyGeminiStream takes a Gemini streaming response and writes Anthropic-format SSE.
// streamCtx is the per-model attempt context (carries streaming_timeout_ms); the caller should
// wrap geminiResp with NewCtxReadCloser so the body read also respects the deadline.
func (h *StreamHandler) ProxyGeminiStream(
	w http.ResponseWriter,
	geminiResp io.ReadCloser,
	originalModel string,
	clientCtx context.Context,
	idleTimeout time.Duration,
	cancel context.CancelFunc,
) error {
	defer func() { _ = geminiResp.Close() }()
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported by response writer")
	}

	msgID := "msg_" + generateID()
	msgStart := types.MessageEvent{
		Type: "message_start",
		Message: &types.MessageResponse{
			ID:      msgID,
			Type:    "message",
			Role:    "assistant",
			Content: []types.ContentBlock{},
			Model:   originalModel,
		},
	}
	if err := writeSSEEvent(w, msgStart); err != nil {
		return ErrClientDisconnected
	}
	flusher.Flush()

	contentIndex := 0
	var lineBuf []byte
	contentStarted := false
	stopSent := false
	readBuf := readBufPool.Get().(*[]byte)
	defer readBufPool.Put(readBuf)

	ping := StartIdleWatchdog(clientCtx, cancel, idleTimeout)

	for {
		select {
		case <-clientCtx.Done():
			return ErrClientDisconnected
		default:
		}

		n, err := geminiResp.Read(*readBuf)
		if n > 0 {
			ping()
			for i := 0; i < n; i++ {
				b := (*readBuf)[i]
				if b == '\n' {
					if err := h.processGeminiSSELine(w, flusher, lineBuf, &contentIndex, &contentStarted, &stopSent, originalModel); err != nil {
						return err
					}
					lineBuf = lineBuf[:0]
				} else {
					lineBuf = append(lineBuf, b)
				}
			}
		}

		if err == io.EOF {
			if len(lineBuf) > 0 {
				if err := h.processGeminiSSELine(w, flusher, lineBuf, &contentIndex, &contentStarted, &stopSent, originalModel); err != nil {
					return err
				}
			}
			break
		}
		if err != nil {
			if IsIdleTimeout(err) {
				return ErrStreamIdle
			}
			if (errors.Is(err, context.Canceled) || errors.Is(err, ErrStreamReadCanceled)) && clientCtx.Err() == nil {
				return ErrStreamIdle
			}
			return fmt.Errorf("failed to read stream: %w", err)
		}
	}

	if contentStarted {
		stopEvent := types.MessageEvent{
			Type:  "content_block_stop",
			Index: &contentIndex,
		}
		if err := writeSSEEvent(w, stopEvent); err != nil {
			return ErrClientDisconnected
		}
	}

	if !stopSent {
		msgDelta := types.MessageEvent{
			Type: "message_delta",
			Delta: &types.Delta{
				StopReason: "end_turn",
			},
			Usage: &types.Usage{InputTokens: 0, OutputTokens: 0},
		}
		if err := writeSSEEvent(w, msgDelta); err != nil {
			return ErrClientDisconnected
		}
		stopSent = true
	}

	stopEvent := types.MessageEvent{
		Type: "message_stop",
	}
	if err := writeSSEEvent(w, stopEvent); err != nil {
		return ErrClientDisconnected
	}
	flusher.Flush()

	return nil
}

func (h *StreamHandler) processGeminiSSELine(
	w http.ResponseWriter,
	flusher http.Flusher,
	line []byte,
	contentIndex *int,
	contentStarted *bool,
	stopSent *bool,
	originalModel string,
) error {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || !bytes.HasPrefix(line, []byte("data: ")) {
		return nil
	}

	data := line[6:]
	if len(data) == 0 {
		return nil
	}

	var chunk types.GeminiStreamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil
	}

	if len(chunk.Candidates) > 0 {
		candidate := chunk.Candidates[0]
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				if !*contentStarted {
					*contentStarted = true
					startEvent := types.MessageEvent{
						Type:         "content_block_start",
						Index:        contentIndex,
						ContentBlock: &types.ContentBlock{Type: "text", Text: ""},
					}
					if err := writeSSEEvent(w, startEvent); err != nil {
						return ErrClientDisconnected
					}
				}

				delta := types.Delta{
					Type: "text_delta",
					Text: part.Text,
				}
				event := types.MessageEvent{
					Type:  "content_block_delta",
					Index: contentIndex,
					Delta: &delta,
				}
				if err := writeSSEEvent(w, event); err != nil {
					return ErrClientDisconnected
				}
				flusher.Flush()
			}
		}

		if candidate.FinishReason != "" && !*stopSent {
			if *contentStarted {
				stopEvent := types.MessageEvent{
					Type:  "content_block_stop",
					Index: contentIndex,
				}
				if err := writeSSEEvent(w, stopEvent); err != nil {
					return ErrClientDisconnected
				}
				*contentStarted = false
			}

			stopReason := "end_turn"
			if candidate.FinishReason == "MAX_TOKENS" {
				stopReason = "max_tokens"
			}

			msgDelta := types.MessageEvent{
				Type: "message_delta",
				Delta: &types.Delta{
					StopReason: stopReason,
				},
				Usage: usageInfoToAnthropic(nil),
			}
			if err := writeSSEEvent(w, msgDelta); err != nil {
				return ErrClientDisconnected
			}
			*stopSent = true
			flusher.Flush()
		}
	}

	return nil
}
