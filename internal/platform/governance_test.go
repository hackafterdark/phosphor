package platform

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/assert"
)

type mockService struct {
	name  string
	start func(ctx context.Context) error
	stop  func(ctx context.Context) error
	desc  string
}

func (m *mockService) Name() string { return m.name }
func (m *mockService) Start(ctx context.Context) error {
	if m.start != nil {
		return m.start(ctx)
	}
	return nil
}
func (m *mockService) Stop(ctx context.Context) error {
	if m.stop != nil {
		return m.stop(ctx)
	}
	return nil
}
func (m *mockService) Describe() string { return m.desc }

func TestGovernance_Check(t *testing.T) {
	t.Parallel()

	_ = slog.New(slog.NewJSONHandler(io.Discard, nil))

	t.Run("service not in config", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{},
		}
		gov := NewGovernance(cfg)
		svc := &mockService{name: "unknown"}

		err := gov.Check(svc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not enabled in config")
	})

	t.Run("service disabled in config", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"test-service": {Enabled: false},
			},
		}
		gov := NewGovernance(cfg)
		svc := &mockService{name: "test-service"}

		err := gov.Check(svc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not enabled in config")
	})

	t.Run("http egress blocked", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"http-api": {Enabled: true},
			},
			Security: &config.SecurityConfig{
				AllowedEgress: config.AllowedEgressConfig{
					HTTP: false,
				},
			},
		}
		gov := NewGovernance(cfg)
		svc := &mockService{name: "http-api"}

		err := gov.Check(svc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP egress blocked")
	})

	t.Run("discord egress blocked", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"discord": {Enabled: true},
			},
			Security: &config.SecurityConfig{
				AllowedEgress: config.AllowedEgressConfig{
					Discord: false,
				},
			},
		}
		gov := NewGovernance(cfg)
		svc := &mockService{name: "discord"}

		err := gov.Check(svc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Discord egress blocked")
	})

	t.Run("missing bearer key", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"http-api": {
					Enabled: true,
					Auth: config.AuthConfig{
						Type: "bearer",
						Key:  "",
					},
				},
			},
			Security: &config.SecurityConfig{
				AllowedEgress: config.AllowedEgressConfig{
					HTTP: true,
				},
			},
		}
		gov := NewGovernance(cfg)
		svc := &mockService{name: "http-api"}

		err := gov.Check(svc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requires bearer token key")
	})

	t.Run("valid config passing check", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"http-api": {
					Enabled: true,
					Port:    8642,
					Auth: config.AuthConfig{
						Type: "bearer",
						Key:  "secret-key",
					},
				},
			},
			Security: &config.SecurityConfig{
				AllowedEgress: config.AllowedEgressConfig{
					HTTP: true,
				},
			},
		}
		gov := NewGovernance(cfg)
		svc := &mockService{name: "http-api"}

		err := gov.Check(svc)
		assert.NoError(t, err)
	})

	t.Run("invalid http-api port negative", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"http-api": {
					Enabled: true,
					Port:    -1,
				},
			},
			Security: &config.SecurityConfig{
				AllowedEgress: config.AllowedEgressConfig{
					HTTP: true,
				},
			},
		}
		gov := NewGovernance(cfg)
		svc := &mockService{name: "http-api"}

		err := gov.Check(svc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port -1")
	})

	t.Run("invalid http-api port too large", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"http-api": {
					Enabled: true,
					Port:    70000,
				},
			},
			Security: &config.SecurityConfig{
				AllowedEgress: config.AllowedEgressConfig{
					HTTP: true,
				},
			},
		}
		gov := NewGovernance(cfg)
		svc := &mockService{name: "http-api"}

		err := gov.Check(svc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port 70000")
	})

	t.Run("invalid openai-api port negative", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"openai-api": {
					Enabled: true,
					Port:    -1,
				},
			},
			Security: &config.SecurityConfig{
				AllowedEgress: config.AllowedEgressConfig{
					HTTP: true,
				},
			},
		}
		gov := NewGovernance(cfg)
		svc := &mockService{name: "openai-api"}

		err := gov.Check(svc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port -1 for service \"openai-api\"")
	})

	t.Run("invalid openai-api port too large", func(t *testing.T) {
		cfg := &config.Config{
			Services: map[string]config.ServiceEntry{
				"openai-api": {
					Enabled: true,
					Port:    70000,
				},
			},
			Security: &config.SecurityConfig{
				AllowedEgress: config.AllowedEgressConfig{
					HTTP: true,
				},
			},
		}
		gov := NewGovernance(cfg)
		svc := &mockService{name: "openai-api"}

		err := gov.Check(svc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port 70000 for service \"openai-api\"")
	})
}
