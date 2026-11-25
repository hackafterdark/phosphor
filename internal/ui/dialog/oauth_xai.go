package dialog

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/hackafterdark/phosphor/internal/oauth/xai"
	"github.com/hackafterdark/phosphor/internal/ui/common"
	"github.com/hackafterdark/phosphor/pkg/config"
)

func NewOAuthXAI(
	com *common.Common,
	isOnboarding bool,
	provider catwalk.Provider,
	model config.SelectedModel,
	modelType config.SelectedModelType,
) (*OAuth, tea.Cmd) {
	return newOAuth(com, isOnboarding, provider, model, modelType, &OAuthXAI{})
}

type OAuthXAI struct {
	deviceCode *xai.DeviceCode
	cancelFunc func()
}

var _ OAuthProvider = (*OAuthXAI)(nil)

func (m *OAuthXAI) name() string {
	return "xAI (Grok)"
}

// supportsAPIKeyFallback lets users without a Grok/X subscription fall back
// to API-key entry by dismissing the device flow.
func (m *OAuthXAI) supportsAPIKeyFallback() bool {
	return true
}

func (m *OAuthXAI) initiateAuth() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deviceCode, err := xai.RequestDeviceCode(ctx)
	if err != nil {
		return ActionOAuthErrored{Error: fmt.Errorf("failed to initiate device auth: %w", err)}
	}

	m.deviceCode = deviceCode

	return ActionInitiateOAuth{
		DeviceCode:      deviceCode.DeviceCode,
		UserCode:        deviceCode.UserCode,
		VerificationURL: deviceCode.VerificationURI,
		ExpiresIn:       deviceCode.ExpiresIn,
		Interval:        deviceCode.Interval,
	}
}

func (m *OAuthXAI) startPolling(deviceCode string, expiresIn int) tea.Cmd {
	// Set up cancellation synchronously, while Update is still running, so a
	// close that lands before the polling goroutine starts can still cancel
	// it. Commands must not mutate model state from their goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	return func() tea.Msg {
		defer cancel()

		token, err := xai.PollForToken(ctx, m.deviceCode)
		if err != nil {
			if ctx.Err() != nil {
				return nil // cancelled, don't report error.
			}
			return ActionOAuthErrored{Error: err}
		}

		return ActionCompleteOAuth{Token: token}
	}
}

func (m *OAuthXAI) stopPolling() tea.Msg {
	if m.cancelFunc != nil {
		m.cancelFunc()
	}
	return nil
}
