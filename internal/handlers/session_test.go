package handlers

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/routatic/proxy/pkg/types"
)

func TestResolveUpstreamSessionID(t *testing.T) {
	tests := []struct {
		name           string
		headers        map[string]string
		metadataUserID string
		want           string
	}{
		{
			name: "incoming x-opencode-session wins over everything",
			headers: map[string]string{
				"x-opencode-session":       "sess-explicit",
				"x-claude-code-session-id": "sess-cc",
				"x-session-id":             "sess-generic",
				"anthropic-session-id":     "sess-anthropic",
			},
			metadataUserID: `{"device_id":"d","account_uuid":"","session_id":"sess-meta"}`,
			want:           "sess-explicit",
		},
		{
			name:           "x-claude-code-session-id next",
			headers:        map[string]string{"x-claude-code-session-id": "sess-cc", "x-session-id": "sess-generic"},
			metadataUserID: "user_abc_session_sess-legacy",
			want:           "sess-cc",
		},
		{
			name:    "x-session-id next",
			headers: map[string]string{"x-session-id": "sess-generic", "anthropic-session-id": "sess-anthropic"},
			want:    "sess-generic",
		},
		{
			name:    "anthropic-session-id next",
			headers: map[string]string{"anthropic-session-id": "sess-anthropic"},
			want:    "sess-anthropic",
		},
		{
			name:           "headers beat metadata",
			headers:        map[string]string{"x-session-id": "sess-generic"},
			metadataUserID: `{"device_id":"d","account_uuid":"","session_id":"sess-meta"}`,
			want:           "sess-generic",
		},
		{
			name:           "metadata JSON device_id shape yields session_id",
			metadataUserID: `{"device_id":"dev-1","account_uuid":"","session_id":"sess-meta-42"}`,
			want:           "sess-meta-42",
		},
		{
			name:           "metadata legacy user_hash_session_uuid shape yields uuid",
			metadataUserID: "user_0a1b2c3d4e_session_sess-77f5b0",
			want:           "sess-77f5b0",
		},
		{
			name:           "metadata JSON with empty session_id falls back",
			metadataUserID: `{"device_id":"dev-1","account_uuid":"","session_id":""}`,
			want:           "",
		},
		{
			name:           "metadata that is neither JSON nor legacy falls back",
			metadataUserID: "opaque-user-id",
			want:           "",
		},
		{
			name: "empty everything falls back",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			for k, v := range tt.headers {
				header.Set(k, v)
			}
			req := &types.MessageRequest{Metadata: &types.Metadata{UserID: tt.metadataUserID}}

			got := resolveUpstreamSessionID(header, req)
			if tt.want == "" {
				// Fallback cases: expect the process-stable fallback UUID.
				if got == "" {
					t.Fatal("resolveUpstreamSessionID() = empty, want fallback session ID")
				}
				if _, err := uuid.Parse(got); err != nil {
					t.Fatalf("fallback session ID %q is not a UUID: %v", got, err)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("resolveUpstreamSessionID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveUpstreamSessionID_NilMetadata(t *testing.T) {
	got := resolveUpstreamSessionID(http.Header{}, &types.MessageRequest{})
	if got == "" {
		t.Fatal("resolveUpstreamSessionID() = empty, want fallback session ID")
	}
}

func TestResolveUpstreamSessionID_FallbackStableAcrossRequests(t *testing.T) {
	first := resolveUpstreamSessionID(http.Header{}, &types.MessageRequest{})
	second := resolveUpstreamSessionID(http.Header{}, &types.MessageRequest{})
	if first == "" || first != second {
		t.Fatalf("fallback session ID not process-stable: first=%q second=%q", first, second)
	}
}
