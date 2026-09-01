package transformer

import (
	"encoding/json"
	"testing"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/internal/core"
)

func TestNormalizedToAnthropic_SystemPromptWithNewline(t *testing.T) {
	req := &core.NormalizedRequest{
		Model:        "minimax-m3",
		SystemPrompt: "Line one\nLine two\nLine three",
		MaxTokens:    100,
		Messages: []core.NormalizedMessage{
			{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "Hello"}}},
		},
	}

	anthropicReq := NormalizedToAnthropic(req, config.ModelConfig{ModelID: "minimax-m3"})

	// The bug: marshaling the request failed when the system prompt contained
	// unescaped newlines because we built the RawMessage by wrapping the raw
	// string in quotes instead of JSON-encoding it.
	_, err := json.Marshal(anthropicReq)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	if got := anthropicReq.SystemText(); got != req.SystemPrompt {
		t.Fatalf("system text mismatch: got %q, want %q", got, req.SystemPrompt)
	}
}

func TestNormalizedToAnthropic_MessageContentWithNewline(t *testing.T) {
	req := &core.NormalizedRequest{
		Model:     "minimax-m3",
		MaxTokens: 100,
		Messages: []core.NormalizedMessage{
			{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "Hello\nWorld"}}},
		},
	}

	anthropicReq := NormalizedToAnthropic(req, config.ModelConfig{ModelID: "minimax-m3"})

	_, err := json.Marshal(anthropicReq)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	blocks := anthropicReq.Messages[0].ContentBlocks()
	if len(blocks) != 1 || blocks[0].Text != "Hello\nWorld" {
		t.Fatalf("unexpected content blocks: %+v", blocks)
	}
}

func TestNormalizedToResponses_ToolHistoryRoundTrip(t *testing.T) {
	req := &core.NormalizedRequest{
		Model:     "gpt-5",
		MaxTokens: 100,
		Messages: []core.NormalizedMessage{
			{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "What's the weather in Paris?"}}},
			{Role: "assistant", Blocks: []core.NormalizedContentBlock{
				{Type: "text", Text: "Let me check."},
				{Type: "tool_use", ID: "call-1", Name: "get_weather", Input: json.RawMessage(`{"city":"Paris"}`)},
			}},
			{Role: "user", Blocks: []core.NormalizedContentBlock{
				{Type: "tool_result", ToolUseID: "call-1", Content: json.RawMessage(`"Sunny, 22C"`)},
				{Type: "text", Text: "Thanks!"},
			}},
		},
	}

	responsesReq := NormalizedToResponses(req, config.ModelConfig{ModelID: "gpt-5"})

	want := []string{
		`{"role":"user","content":"What's the weather in Paris?"}`,
		`{"role":"assistant","content":"Let me check."}`,
		`{"type":"function_call","call_id":"call-1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}`,
		`{"type":"function_call_output","call_id":"call-1","output":"Sunny, 22C"}`,
		`{"role":"user","content":"Thanks!"}`,
	}
	assertInputItems(t, responsesReq.Input, want)
}

func TestNormalizedToResponses_ToolResultOnlyUserMessage(t *testing.T) {
	// Regression for the live grok-4.6 HTTP 422: a user message containing
	// only tool_result blocks used to serialize as a contentless
	// {"role":"user"} input item, which the Responses API rejects with
	// "input[N] did not match any supported type".
	req := &core.NormalizedRequest{
		Model:     "gpt-5",
		MaxTokens: 100,
		Messages: []core.NormalizedMessage{
			{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "Check the weather."}}},
			{Role: "assistant", Blocks: []core.NormalizedContentBlock{
				{Type: "tool_use", ID: "call-1", Name: "get_weather", Input: json.RawMessage(`{"city":"Paris"}`)},
			}},
			{Role: "user", Blocks: []core.NormalizedContentBlock{
				{Type: "tool_result", ToolUseID: "call-1", Content: json.RawMessage(`"Sunny, 22C"`)},
			}},
		},
	}

	responsesReq := NormalizedToResponses(req, config.ModelConfig{ModelID: "gpt-5"})

	want := []string{
		`{"role":"user","content":"Check the weather."}`,
		`{"type":"function_call","call_id":"call-1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}`,
		`{"type":"function_call_output","call_id":"call-1","output":"Sunny, 22C"}`,
	}
	assertInputItems(t, responsesReq.Input, want)
}

func TestNormalizedToResponses_AssistantToolUseOnly(t *testing.T) {
	req := &core.NormalizedRequest{
		Model:     "gpt-5",
		MaxTokens: 100,
		Messages: []core.NormalizedMessage{
			{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "Check the weather."}}},
			{Role: "assistant", Blocks: []core.NormalizedContentBlock{
				{Type: "tool_use", ID: "call-1", Name: "get_weather", Input: json.RawMessage(`{"city":"Paris"}`)},
				{Type: "tool_use", ID: "call-2", Name: "get_time"},
			}},
		},
	}

	responsesReq := NormalizedToResponses(req, config.ModelConfig{ModelID: "gpt-5"})

	want := []string{
		`{"role":"user","content":"Check the weather."}`,
		`{"type":"function_call","call_id":"call-1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}`,
		`{"type":"function_call","call_id":"call-2","name":"get_time","arguments":"{}"}`,
	}
	assertInputItems(t, responsesReq.Input, want)
}

func TestNormalizedToResponses_MultipleToolResultsThenText(t *testing.T) {
	req := &core.NormalizedRequest{
		Model:     "gpt-5",
		MaxTokens: 100,
		Messages: []core.NormalizedMessage{
			{Role: "user", Blocks: []core.NormalizedContentBlock{
				{Type: "tool_result", ToolUseID: "call-1", Content: json.RawMessage(`"Sunny, 22C"`)},
				{Type: "tool_result", ToolUseID: "call-2", Content: json.RawMessage(`[{"type":"text","text":"Rainy, 15C"}]`)},
				{Type: "text", Text: "Now compare them."},
			}},
		},
	}

	responsesReq := NormalizedToResponses(req, config.ModelConfig{ModelID: "gpt-5"})

	want := []string{
		`{"type":"function_call_output","call_id":"call-1","output":"Sunny, 22C"}`,
		`{"type":"function_call_output","call_id":"call-2","output":"Rainy, 15C"}`,
		`{"role":"user","content":"Now compare them."}`,
	}
	assertInputItems(t, responsesReq.Input, want)
}

func TestNormalizedToResponses_ThinkingSkippedAndSystemBecomesDeveloper(t *testing.T) {
	req := &core.NormalizedRequest{
		Model:        "gpt-5",
		SystemPrompt: "You are a weather bot.",
		MaxTokens:    100,
		Messages: []core.NormalizedMessage{
			{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "Hi"}}},
			{Role: "assistant", Blocks: []core.NormalizedContentBlock{
				{Type: "thinking", Thinking: "internal reasoning"},
				{Type: "text", Text: "Hello!"},
			}},
			{Role: "assistant", Blocks: []core.NormalizedContentBlock{
				{Type: "thinking", Thinking: "more reasoning"},
			}},
			{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "Bye"}}},
		},
	}

	responsesReq := NormalizedToResponses(req, config.ModelConfig{ModelID: "gpt-5"})

	want := []string{
		`{"role":"developer","content":"You are a weather bot."}`,
		`{"role":"user","content":"Hi"}`,
		`{"role":"assistant","content":"Hello!"}`,
		`{"role":"user","content":"Bye"}`,
	}
	assertInputItems(t, responsesReq.Input, want)
}

func TestNormalizedToResponses_SystemPromptWithNewline(t *testing.T) {
	req := &core.NormalizedRequest{
		Model:        "gpt-5",
		SystemPrompt: "Line one\nLine two",
		MaxTokens:    100,
		Messages: []core.NormalizedMessage{
			{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "Hello\nWorld"}}},
		},
	}

	responsesReq := NormalizedToResponses(req, config.ModelConfig{ModelID: "gpt-5"})

	_, err := json.Marshal(responsesReq)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	if len(responsesReq.Input) != 2 {
		t.Fatalf("input count mismatch: got %d, want 2", len(responsesReq.Input))
	}

	var systemPrompt string
	if err := json.Unmarshal(responsesReq.Input[0].Content, &systemPrompt); err != nil {
		t.Fatalf("system prompt content was not valid JSON: %v", err)
	}
	if systemPrompt != req.SystemPrompt {
		t.Fatalf("system prompt mismatch: got %q, want %q", systemPrompt, req.SystemPrompt)
	}

	var messageContent string
	if err := json.Unmarshal(responsesReq.Input[1].Content, &messageContent); err != nil {
		t.Fatalf("message content was not valid JSON: %v", err)
	}
	if messageContent != req.Messages[0].TextContent() {
		t.Fatalf("message content mismatch: got %q, want %q", messageContent, req.Messages[0].TextContent())
	}
}
