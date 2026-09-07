// Resolution of the per-conversation session ID required by the OpenCode Go
// upstream (x-opencode-session header).
//
// The client behind the proxy (Claude Code) already provides session identity
// on every request; this picks the best available value once per incoming
// request so the upstream client layer can attach it to every upstream call.
package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/routatic/proxy/pkg/types"
)

// upstreamSessionHeaders lists incoming headers that may carry a stable
// per-conversation session ID, in resolution priority order. The first
// non-empty value wins.
var upstreamSessionHeaders = []string{
	"x-opencode-session",
	"x-claude-code-session-id",
	"x-session-id",
	"anthropic-session-id",
}

// legacySessionSep separates the session UUID in the legacy Anthropic
// metadata.user_id format: user_<hash>_session_<uuid>.
const legacySessionSep = "_session_"

// resolveUpstreamSessionID picks the session ID to send upstream. Priority:
// incoming headers (see upstreamSessionHeaders), then metadata.user_id (a
// JSON-encoded {"session_id": ...} string, else the legacy
// user_<hash>_session_<uuid> suffix), then a per-process fallback UUID so
// header-less clients still route consistently.
func resolveUpstreamSessionID(header http.Header, anthropicReq *types.MessageRequest) string {
	for _, name := range upstreamSessionHeaders {
		if v := strings.TrimSpace(header.Get(name)); v != "" {
			return v
		}
	}
	if anthropicReq != nil && anthropicReq.Metadata != nil {
		if s := metadataSessionID(anthropicReq.Metadata.UserID); s != "" {
			return s
		}
	}
	return processFallbackSessionID()
}

// metadataSessionID extracts the session ID from metadata.user_id, which is
// observed in two shapes:
//   - a JSON-encoded string: {"device_id":"…","account_uuid":"","session_id":"<uuid>"}
//   - legacy Anthropic format: user_<hash>_session_<uuid>
func metadataSessionID(userID string) string {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return ""
	}
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(uid), &payload); err == nil && payload.SessionID != "" {
		return payload.SessionID
	}
	if i := strings.LastIndex(uid, legacySessionSep); i >= 0 {
		return uid[i+len(legacySessionSep):]
	}
	return ""
}

// processFallbackSessionID returns the fallback session ID, generated once
// per proxy process so header-less clients still route consistently.
var processFallbackSessionID = sync.OnceValue(uuid.NewString)
