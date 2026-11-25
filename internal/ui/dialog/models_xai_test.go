package dialog

import (
	"testing"

	"github.com/hackafterdark/phosphor/internal/oauth"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/csync"
	"github.com/stretchr/testify/require"
)

func xaiCfg(t *testing.T, providerConfig config.ProviderConfig) *config.Config {
	t.Helper()
	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("xai", providerConfig)
	return &config.Config{Providers: providers}
}

func TestXAIAuthNote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cfg      *config.Config
		provider string
		model    string
		want     string
	}{
		{
			name:     "subscription account rejects API-only model",
			cfg:      xaiCfg(t, config.ProviderConfig{OAuthToken: &oauth.Token{AccessToken: "tok"}}),
			provider: "xai",
			model:    "grok-4.20-0309-reasoning",
			want:     "API-only",
		},
		{
			name:     "subscription account accepts curated model",
			cfg:      xaiCfg(t, config.ProviderConfig{OAuthToken: &oauth.Token{AccessToken: "tok"}}),
			provider: "xai",
			model:    "grok-4.6",
			want:     "",
		},
		{
			name:     "api key account flags subscription-covered model",
			cfg:      xaiCfg(t, config.ProviderConfig{APIKey: "$XAI_API_KEY"}),
			provider: "xai",
			model:    "grok-4.5",
			want:     "Subscription",
		},
		{
			name:     "api key account accepts api-only model",
			cfg:      xaiCfg(t, config.ProviderConfig{APIKey: "$XAI_API_KEY"}),
			provider: "xai",
			model:    "grok-4.20",
			want:     "",
		},
		{
			name:     "unauthenticated provider shows no badge",
			cfg:      &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()},
			provider: "xai",
			model:    "grok-4.20",
			want:     "",
		},
		{
			name:     "non-xai provider is ignored",
			cfg:      xaiCfg(t, config.ProviderConfig{OAuthToken: &oauth.Token{AccessToken: "tok"}}),
			provider: "anthropic",
			model:    "claude-sonnet-4-6",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, xaiAuthNote(tt.cfg, tt.provider, tt.model))
		})
	}
}
