// Session context threading for the x-opencode-session upstream header.
//
// The OpenCode Go upstream requires a stable per-conversation session ID in
// the x-opencode-session header on every request so it can route efficiently.
// The messages handler resolves the ID once per incoming request (incoming
// headers, then body metadata.user_id, then a per-process fallback UUID) and
// attaches it here; every upstream request builder copies it onto the
// outgoing request. Upstreams that do not use the header ignore it.
package client

import (
	"context"
	"fmt"
	"net/http"
)

// UpstreamSessionHeader is the per-conversation session header required by
// the OpenCode Go upstream.
const UpstreamSessionHeader = "x-opencode-session"

// sessionCtxKey carries the resolved session ID through the request context.
// A dedicated key type avoids collisions between context users.
type sessionCtxKeyType struct{}

var sessionCtxKey sessionCtxKeyType

// WithSessionContext returns ctx with the resolved upstream session ID
// attached. Empty session IDs attach nothing.
func WithSessionContext(ctx context.Context, sessionID string) context.Context {
	if ctx == nil || sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionCtxKey, sessionID)
}

// SessionFromContext returns the upstream session ID attached to ctx, or ""
// when ctx carries none. Nil-safe.
func SessionFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	return extractSessionID(ctx.Value(sessionCtxKey))
}

// extractSessionID converts a context value to a string session ID
// (nil-safe), mirroring extractRequestID.
func extractSessionID(v interface{}) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// SetSessionHeader copies the session ID from ctx onto h as
// x-opencode-session. It is a no-op when ctx carries no session ID.
func SetSessionHeader(ctx context.Context, h http.Header) {
	if s := SessionFromContext(ctx); s != "" {
		h.Set(UpstreamSessionHeader, s)
	}
}
