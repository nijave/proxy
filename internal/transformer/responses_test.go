package transformer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/pkg/types"
)

func marshalInputItems(t *testing.T, input []types.ResponsesInput) []string {
	t.Helper()
	items := make([]string, 0, len(input))
	for i, item := range input {
		b, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal input item %d: %v", i, err)
		}
		items = append(items, string(b))
	}
	return items
}

func assertNoContentlessInputItems(t *testing.T, input []types.ResponsesInput) {
	t.Helper()
	for i, item := range input {
		if item.Type == "" && len(item.Content) == 0 {
			t.Fatalf("input item %d has neither a type nor content: %+v", i, item)
		}
	}
}

func assertInputItems(t *testing.T, input []types.ResponsesInput, want []string) {
	t.Helper()
	assertNoContentlessInputItems(t, input)
	got := marshalInputItems(t, input)
	if len(got) != len(want) {
		t.Fatalf("input item count = %d, want %d\n     got: %s\n    want: %s",
			len(got), len(want), strings.Join(got, "\n          "), strings.Join(want, "\n          "))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("input item %d mismatch\n     got: %s\n    want: %s", i, got[i], want[i])
		}
	}
}

func TestTransformToResponses_ToolHistoryRoundTrip(t *testing.T) {
	transformer := NewRequestTransformer()

	req := &types.MessageRequest{
		Model:     "claude-test",
		MaxTokens: 256,
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"What's the weather in Paris?"`)},
			{Role: "assistant", Content: json.RawMessage(`[
				{"type":"text","text":"Let me check."},
				{"type":"tool_use","id":"call-1","name":"get_weather","input":{"city":"Paris"}}
			]`)},
			{Role: "user", Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"call-1","content":"Sunny, 22C"},
				{"type":"text","text":"Thanks!"}
			]`)},
		},
	}

	respReq, err := transformer.TransformToResponses(req, config.ModelConfig{ModelID: "grok-4.6"})
	if err != nil {
		t.Fatalf("TransformToResponses error: %v", err)
	}

	want := []string{
		`{"role":"user","content":"What's the weather in Paris?"}`,
		`{"role":"assistant","content":"Let me check."}`,
		`{"type":"function_call","call_id":"call-1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}`,
		`{"type":"function_call_output","call_id":"call-1","output":"Sunny, 22C"}`,
		`{"role":"user","content":"Thanks!"}`,
	}
	assertInputItems(t, respReq.Input, want)
}

func TestTransformToResponses_ToolResultOnlyUserMessage(t *testing.T) {
	// Regression for the live grok-4.6 HTTP 422: a user message containing
	// only tool_result blocks used to serialize as role:"tool" items plus a
	// contentless {"role":"user"} item, neither of which is a valid
	// Responses input item.
	transformer := NewRequestTransformer()

	req := &types.MessageRequest{
		Model:     "claude-test",
		MaxTokens: 256,
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"Check the weather in Paris and Lyon."`)},
			{Role: "assistant", Content: json.RawMessage(`[
				{"type":"tool_use","id":"call-1","name":"get_weather","input":{"city":"Paris"}},
				{"type":"tool_use","id":"call-2","name":"get_weather","input":{"city":"Lyon"}}
			]`)},
			{Role: "user", Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"call-1","content":"Sunny, 22C"},
				{"type":"tool_result","tool_use_id":"call-2","content":[{"type":"text","text":"Rainy, 15C"}]}
			]`)},
		},
	}

	respReq, err := transformer.TransformToResponses(req, config.ModelConfig{ModelID: "grok-4.6"})
	if err != nil {
		t.Fatalf("TransformToResponses error: %v", err)
	}

	want := []string{
		`{"role":"user","content":"Check the weather in Paris and Lyon."}`,
		`{"type":"function_call","call_id":"call-1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}`,
		`{"type":"function_call","call_id":"call-2","name":"get_weather","arguments":"{\"city\":\"Lyon\"}"}`,
		`{"type":"function_call_output","call_id":"call-1","output":"Sunny, 22C"}`,
		`{"type":"function_call_output","call_id":"call-2","output":"Rainy, 15C"}`,
	}
	assertInputItems(t, respReq.Input, want)
}

func TestTransformToResponses_AssistantToolUseOnly(t *testing.T) {
	transformer := NewRequestTransformer()

	req := &types.MessageRequest{
		Model:     "claude-test",
		MaxTokens: 256,
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"Check the weather."`)},
			{Role: "assistant", Content: json.RawMessage(`[
				{"type":"tool_use","id":"call-1","name":"get_weather","input":{"city":"Paris"}},
				{"type":"tool_use","id":"call-2","name":"get_time"}
			]`)},
		},
	}

	respReq, err := transformer.TransformToResponses(req, config.ModelConfig{ModelID: "grok-4.6"})
	if err != nil {
		t.Fatalf("TransformToResponses error: %v", err)
	}

	want := []string{
		`{"role":"user","content":"Check the weather."}`,
		`{"type":"function_call","call_id":"call-1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}`,
		`{"type":"function_call","call_id":"call-2","name":"get_time","arguments":"{}"}`,
	}
	assertInputItems(t, respReq.Input, want)
}

func TestTransformToResponses_MultipleToolResultsThenText(t *testing.T) {
	transformer := NewRequestTransformer()

	req := &types.MessageRequest{
		Model:     "claude-test",
		MaxTokens: 256,
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"call-1","content":"Sunny, 22C"},
				{"type":"tool_result","tool_use_id":"call-2","content":"Rainy, 15C"},
				{"type":"text","text":"Now compare them."}
			]`)},
		},
	}

	respReq, err := transformer.TransformToResponses(req, config.ModelConfig{ModelID: "grok-4.6"})
	if err != nil {
		t.Fatalf("TransformToResponses error: %v", err)
	}

	want := []string{
		`{"type":"function_call_output","call_id":"call-1","output":"Sunny, 22C"}`,
		`{"type":"function_call_output","call_id":"call-2","output":"Rainy, 15C"}`,
		`{"role":"user","content":"Now compare them."}`,
	}
	assertInputItems(t, respReq.Input, want)
}

func TestTransformToResponses_ThinkingSkippedAndSystemBecomesDeveloper(t *testing.T) {
	transformer := NewRequestTransformer()

	req := &types.MessageRequest{
		Model:     "claude-test",
		MaxTokens: 256,
		System:    json.RawMessage(`"You are a weather bot."`),
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
			{Role: "assistant", Content: json.RawMessage(`[
				{"type":"thinking","thinking":"internal reasoning","signature":"sig-1"},
				{"type":"text","text":"Hello!"}
			]`)},
			{Role: "assistant", Content: json.RawMessage(`[
				{"type":"thinking","thinking":"more reasoning","signature":"sig-2"}
			]`)},
			{Role: "user", Content: json.RawMessage(`"Bye"`)},
		},
	}

	respReq, err := transformer.TransformToResponses(req, config.ModelConfig{ModelID: "grok-4.6"})
	if err != nil {
		t.Fatalf("TransformToResponses error: %v", err)
	}

	want := []string{
		`{"role":"developer","content":"You are a weather bot."}`,
		`{"role":"user","content":"Hi"}`,
		`{"role":"assistant","content":"Hello!"}`,
		`{"role":"user","content":"Bye"}`,
	}
	assertInputItems(t, respReq.Input, want)
}
