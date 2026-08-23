package config

import "testing"

func TestCloudflareEffectiveAPIKeys(t *testing.T) {
	tests := []struct {
		name string
		cfg  CloudflareConfig
		want []string
	}{
		{"single key", CloudflareConfig{APIKey: "k1"}, []string{"k1"}},
		{"keys win", CloudflareConfig{APIKey: "k1", APIKeys: []string{"a", "b"}}, []string{"a", "b"}},
		{"empty", CloudflareConfig{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.EffectiveAPIKeys()
			if len(got) != len(tt.want) {
				t.Fatalf("EffectiveAPIKeys() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("EffectiveAPIKeys()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestCloudflareEffectiveBaseURL(t *testing.T) {
	tests := []struct {
		name string
		cfg  CloudflareConfig
		want string
	}{
		{
			name: "built from account id",
			cfg:  CloudflareConfig{AccountID: "abc123"},
			want: "https://api.cloudflare.com/client/v4/accounts/abc123/ai/v1/chat/completions",
		},
		{
			name: "base_url override wins",
			cfg:  CloudflareConfig{AccountID: "abc123", BaseURL: "https://proxy.corp/cf/chat"},
			want: "https://proxy.corp/cf/chat",
		},
		{
			name: "nothing configured",
			cfg:  CloudflareConfig{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.EffectiveBaseURL(); got != tt.want {
				t.Errorf("EffectiveBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
