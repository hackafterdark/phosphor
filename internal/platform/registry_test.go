package platform

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockAgentService struct {
	mockService
	connected bool
	handler   PromptHandler
}

func (m *mockAgentService) Connect(ctx context.Context) error {
	m.connected = true
	return nil
}

func (m *mockAgentService) SetPromptHandler(handler PromptHandler) {
	m.handler = handler
}

func (m *mockAgentService) SendPrompt(ctx context.Context, req PromptRequest) error {
	return nil
}

func TestRegistry_Lifecycle(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	t.Run("start and stop in correct order", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"svc-1": {Enabled: true},
				"svc-2": {Enabled: true},
			},
		}
		gov := NewGovernance(cfg)
		reg := NewRegistry(logger, gov)

		var startOrder []string
		var stopOrder []string

		svc1 := &mockService{
			name: "svc-1",
			start: func(ctx context.Context) error {
				startOrder = append(startOrder, "svc-1")
				return nil
			},
			stop: func(ctx context.Context) error {
				stopOrder = append(stopOrder, "svc-1")
				return nil
			},
		}
		svc2 := &mockService{
			name: "svc-2",
			start: func(ctx context.Context) error {
				startOrder = append(startOrder, "svc-2")
				return nil
			},
			stop: func(ctx context.Context) error {
				stopOrder = append(stopOrder, "svc-2")
				return nil
			},
		}

		reg.Register(svc1)
		reg.Register(svc2)

		ctx := context.Background()
		err := reg.StartAll(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"svc-1", "svc-2"}, startOrder)

		err = reg.StopAll(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"svc-2", "svc-1"}, stopOrder)
	})

	t.Run("skip starting blocked services", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"svc-enabled":  {Enabled: true},
				"svc-disabled": {Enabled: false},
			},
		}
		gov := NewGovernance(cfg)
		reg := NewRegistry(logger, gov)

		startCalled := make(map[string]bool)
		svcEnabled := &mockService{
			name: "svc-enabled",
			start: func(ctx context.Context) error {
				startCalled["svc-enabled"] = true
				return nil
			},
		}
		svcDisabled := &mockService{
			name: "svc-disabled",
			start: func(ctx context.Context) error {
				startCalled["svc-disabled"] = true
				return nil
			},
		}

		reg.Register(svcEnabled)
		reg.Register(svcDisabled)

		err := reg.StartAll(context.Background())
		require.NoError(t, err)

		assert.True(t, startCalled["svc-enabled"])
		assert.False(t, startCalled["svc-disabled"])

		// StopAll should only stop the enabled one.
		stopCalled := make(map[string]bool)
		svcEnabled.stop = func(ctx context.Context) error {
			stopCalled["svc-enabled"] = true
			return nil
		}
		svcDisabled.stop = func(ctx context.Context) error {
			stopCalled["svc-disabled"] = true
			return nil
		}

		err = reg.StopAll(context.Background())
		require.NoError(t, err)
		assert.True(t, stopCalled["svc-enabled"])
		assert.False(t, stopCalled["svc-disabled"])
	})

	t.Run("robustness when start fails", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"svc-fail": {Enabled: true},
				"svc-ok":   {Enabled: true},
			},
		}
		gov := NewGovernance(cfg)
		reg := NewRegistry(logger, gov)

		svcFail := &mockService{
			name: "svc-fail",
			start: func(ctx context.Context) error {
				return errors.New("fail")
			},
		}
		svcOk := &mockService{
			name: "svc-ok",
			start: func(ctx context.Context) error {
				return nil
			},
		}

		reg.Register(svcFail)
		reg.Register(svcOk)

		err := reg.StartAll(context.Background())
		require.NoError(t, err)

		assert.False(t, reg.started["svc-fail"])
		assert.True(t, reg.started["svc-ok"])
	})

	t.Run("agent service registration classification", func(t *testing.T) {
		cfg := &config.Config{}
		gov := NewGovernance(cfg)
		reg := NewRegistry(logger, gov)

		svc1 := &mockService{name: "svc-simple"}
		svc2 := &mockAgentService{mockService: mockService{name: "svc-agent"}}

		reg.Register(svc1)
		reg.Register(svc2)

		assert.Len(t, reg.Services(), 2)
		assert.Len(t, reg.AgentServices(), 1)
		assert.Equal(t, "svc-agent", reg.AgentServices()[0].Name())
	})
}
