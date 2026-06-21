// Package config provides configuration management for the routatic-proxy.
package config

import (
	"context"

	"github.com/routatic/proxy/internal/auth"
)

// ConfigRef is re-exported from auth package for convenience.
type ConfigRef = auth.ConfigRef

// ConfigProvider returns runtime-ready config for authenticated requests.
// Implementations may be backed by file-based configs, cloud snapshots,
// or cached/multi-layered sources.
type ConfigProvider interface {
	// GetEffectiveConfig returns the runtime configuration for the authenticated request.
	// The auth context determines which workspace and permissions apply.
	GetEffectiveConfig(ctx context.Context, authCtx *auth.AuthContext) (*RuntimeConfig, error)

	// GetConfigByRef retrieves a specific configuration version by reference.
	// Useful for rollbacks or previewing specific config versions.
	GetConfigByRef(ctx context.Context, ref ConfigRef) (*RuntimeConfig, error)

	// Invalidate clears any cached configuration for the specified workspace and version.
	// Call this when external configuration changes are detected.
	Invalidate(ctx context.Context, workspaceID string, version string) error

	// HealthCheck verifies the provider is operational.
	HealthCheck(ctx context.Context) error
}
