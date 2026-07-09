package platform

import (
	"context"
	"log/slog"
)

// Registry manages all registered Phosphor services.
type Registry struct {
	services      []Service
	agentServices []AgentService
	started       map[string]bool
	logger        *slog.Logger
	gov           *Governance
}

// NewRegistry creates a new service registry.
func NewRegistry(logger *slog.Logger, gov *Governance) *Registry {
	return &Registry{
		started: make(map[string]bool),
		logger:  logger,
		gov:     gov,
	}
}

// Register adds a service to the registry.
func (r *Registry) Register(s Service) {
	r.services = append(r.services, s)
	if ags, ok := s.(AgentService); ok {
		r.agentServices = append(r.agentServices, ags)
	}
}

// StartAll starts all registered services in registration order, if permitted
// by the governance layer. Continues starting subsequent services on error.
func (r *Registry) StartAll(ctx context.Context) error {
	for _, s := range r.services {
		if err := r.gov.Check(s); err != nil {
			r.logger.Warn("Service blocked by governance policy", "name", s.Name(), "error", err)
			continue
		}
		r.logger.Info("Starting service", "name", s.Name())
		if err := s.Start(ctx); err != nil {
			r.logger.Error("Failed to start service", "name", s.Name(), "error", err)
			continue
		}
		r.started[s.Name()] = true
	}
	return nil
}

// StopAll stops all started services in reverse registration order.
func (r *Registry) StopAll(ctx context.Context) error {
	for i := len(r.services) - 1; i >= 0; i-- {
		s := r.services[i]
		if !r.started[s.Name()] {
			continue
		}
		r.logger.Info("Stopping service", "name", s.Name())
		if err := s.Stop(ctx); err != nil {
			r.logger.Error("Failed to stop service", "name", s.Name(), "error", err)
		}
		delete(r.started, s.Name())
	}
	return nil
}

// Services returns all registered services.
func (r *Registry) Services() []Service {
	return r.services
}

// AgentServices returns all registered agent-connected services.
func (r *Registry) AgentServices() []AgentService {
	return r.agentServices
}
