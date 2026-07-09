package platform

import (
	"fmt"

	"github.com/hackafterdark/phosphor/internal/config"
)

// Governance enforces security policies and validates service start-up
// manifests against the phosphor.json config constraints.
type Governance struct {
	cfg *config.Config
}

// NewGovernance creates a new Governance checker with the given config.
func NewGovernance(cfg *config.Config) *Governance {
	return &Governance{cfg: cfg}
}

// Check verifies if a service is allowed to start and run based on:
// 1. Being explicitly enabled in phosphor.json.
// 2. The security allowed_egress policies for network transports.
// 3. Complete authentication configuration if required.
func (g *Governance) Check(s Service) error {
	if g.cfg == nil {
		return fmt.Errorf("configuration is nil")
	}

	// 1. Check if the service is configured and enabled.
	entry, ok := g.cfg.Services[s.Name()]
	if !ok || !entry.Enabled {
		return fmt.Errorf("service %q not enabled in config", s.Name())
	}

	// 2. Check security manifest allowed egress for network/outbound
	// platforms.
	if g.cfg.Security != nil {
		switch s.Name() {
		case "http-api":
			if !g.cfg.Security.AllowedEgress.HTTP {
				return fmt.Errorf("HTTP egress blocked by security policy")
			}
		case "discord":
			if !g.cfg.Security.AllowedEgress.Discord {
				return fmt.Errorf("Discord egress blocked by security policy")
			}
		case "slack":
			if !g.cfg.Security.AllowedEgress.Slack {
				return fmt.Errorf("Slack egress blocked by security policy")
			}
		case "telegram":
			if !g.cfg.Security.AllowedEgress.Telegram {
				return fmt.Errorf("Telegram egress blocked by security policy")
			}
		}
	}

	// 3. Validate auth options if bearer type is chosen.
	if entry.Auth.Type == "bearer" && entry.Auth.Key == "" {
		return fmt.Errorf("service %q requires bearer token key but none is configured", s.Name())
	}

	// 4. Validate service-specific config.
	switch s.Name() {
	case "http-api":
		if entry.Port < 0 || entry.Port > 65535 {
			return fmt.Errorf("invalid port %d for service %q", entry.Port, s.Name())
		}
	case "openai-api":
		if entry.Port < 0 || entry.Port > 65535 {
			return fmt.Errorf("invalid port %d for service %q", entry.Port, s.Name())
		}
	}

	return nil
}
