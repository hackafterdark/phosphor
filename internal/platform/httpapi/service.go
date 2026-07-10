package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hackafterdark/phosphor/internal/backend"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/platform/openai"
	"github.com/hackafterdark/phosphor/internal/proto"
	"github.com/hackafterdark/phosphor/internal/server"
	"github.com/labstack/echo/v5"
)

// Service wraps the existing HTTP API server and a new Echo-based OpenAI-compatible API.
type Service struct {
	cfgStore   *config.ConfigStore
	scheme     string
	host       string
	workspace  string
	srv        *server.Server
	echoSrv    *echo.Echo
	echoServer *http.Server
	logger     *slog.Logger
	stopOnce   sync.Once
}

// NewService creates a new HTTP API service.
func NewService(cfgStore *config.ConfigStore, scheme, host, workspace string, logger *slog.Logger) *Service {
	return &Service{
		cfgStore:  cfgStore,
		scheme:    scheme,
		host:      host,
		workspace: workspace,
		logger:    logger,
	}
}

// openaiConfig holds the resolved OpenAI API service configuration.
type openaiConfig struct {
	enabled              bool
	host                 string
	port                 int
	acceptSystemPrompt   bool
	logRequestBody       bool
}

// resolveOpenaiConfig reads the openai-api service entry from config and
// applies defaults for host and port.
func (s *Service) resolveOpenaiConfig() openaiConfig {
	cfg := openaiConfig{host: "127.0.0.1", port: 8643, enabled: true}
	if s.cfgStore != nil {
		if entry, ok := s.cfgStore.Config().Services["openai-api"]; ok {
			cfg.enabled = entry.Enabled
			cfg.acceptSystemPrompt = entry.AcceptSystemPrompt
			cfg.logRequestBody = entry.LogRequestBody
			if entry.Host != "" {
				cfg.host = entry.Host
			}
			if entry.Port != 0 {
				cfg.port = entry.Port
			}
		}
	}
	return cfg
}

// Name returns the service name "http-api".
func (s *Service) Name() string {
	return "http-api"
}

// Backend returns the underlying backend instance.
func (s *Service) Backend() *backend.Backend {
	if s.srv == nil {
		return nil
	}
	return s.srv.Backend()
}

// Start begins serving the HTTP API and the Echo-powered OpenAI-compatible API.
func (s *Service) Start(ctx context.Context) error {
	if s.logger != nil {
		s.logger.Info("=== Service.Start() called ===")
	}

	s.srv = server.NewServer(s.cfgStore, s.scheme, s.host)
	if s.logger != nil {
		s.srv.SetLogger(s.logger)
	} else {
		s.srv.SetLogger(slog.Default())
	}

	// Extend the create-grace window for OpenAI API usage
	// (Open WebUI and other clients may have gaps between requests)
	s.srv.Backend().SetCreateGrace(5 * time.Minute)

	// 1. Start management API
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != server.ErrServerClosed {
			if s.logger != nil {
				s.logger.Error("Management API server error", "error", err)
			}
		}
	}()

	// 2. Resolve OpenAI API configuration
	openaiCfg := s.resolveOpenaiConfig()

	if s.logger != nil {
		s.logger.Info("HTTP API service starting",
			"management_addr", fmt.Sprintf("%s://%s", s.scheme, s.host),
			"openai_enabled", openaiCfg.enabled,
		)
	}

	if s.cfgStore != nil {
		cfg := s.cfgStore.Config()
		if s.logger != nil {
			s.logger.Info("Configuration loaded",
				"is_configured", cfg.IsConfigured(),
				"providers_count", len(cfg.EnabledProviders()),
				"data_directory", cfg.Options.DataDirectory,
			)
		}
		if !cfg.IsConfigured() {
			if s.logger != nil {
				s.logger.Warn("No providers configured — agent will not be initialized. " +
					"Add at least one provider to phosphor.json (e.g. openai, anthropic) " +
					"or set an API key via PHOSPHOR_PROVIDER_API_KEY.")
			}
		}
	}

	if openaiCfg.enabled {
		if s.logger != nil {
			s.logger.Info("=== Starting OpenAI Echo server ===")
		}
		s.echoSrv = echo.New()

		// Resolve API key for auth middleware
		apiKey := os.Getenv("API_SERVER_KEY")
		if apiKey == "" && s.cfgStore != nil {
			if entry, ok := s.cfgStore.Config().Services["openai-api"]; ok {
				apiKey = entry.Auth.Key
			}
		}
		if s.logger != nil {
			s.logger.Info("OpenAI auth configured", "has_key", apiKey != "")
		}

		// Add auth middleware
		s.echoSrv.Use(openai.AuthMiddleware(apiKey))

		// Ensure at least one workspace exists for the OpenAI API.
		// Workspaces are normally created by client connections via
		// POST /v1/workspaces, but the OpenAI API needs one available
		// immediately — create it from the current working directory.
		var workspaceID string
		wsList := s.srv.Backend().ListWorkspaces()
		if s.logger != nil {
			s.logger.Info("Workspace list check", "count", len(wsList))
		}
		if len(wsList) == 0 {
			cwd := s.workspace
			if cwd == "" {
				var err error
				cwd, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("failed to get working directory: %w", err)
				}
			}
			if s.logger != nil {
				s.logger.Info("No workspaces found, creating new one", "path", cwd)
			}

			dataDir := ""
			if s.cfgStore != nil {
				if cfg := s.cfgStore.Config(); cfg.Options.DataDirectory != "" {
					dataDir = cfg.Options.DataDirectory
				}
			}

			wsArgs := proto.Workspace{
				Path:     cwd,
				DataDir:  dataDir,
				ClientID: uuid.New().String(),
			}

			ws, _, err := s.srv.Backend().CreateWorkspace(wsArgs)
			if err != nil {
				if s.logger != nil {
					s.logger.Error("Failed to create workspace for OpenAI API", "error", err)
				}
				return fmt.Errorf("failed to create workspace for OpenAI API: %w", err)
			}
			if s.logger != nil {
				s.logger.Info("Workspace created successfully", "workspace_id", ws.ID)
			}

			// Verify the workspace's agent was initialized.
			wsProto, _ := s.srv.Backend().GetWorkspaceProto(ws.ID)
			if s.logger != nil {
				s.logger.Info("OpenAI API workspace ready",
					"workspace_id", ws.ID,
					"path", cwd,
					"agent_initialized", wsProto.Config != nil && wsProto.Config.IsConfigured(),
				)
			}
			if s.cfgStore != nil && !s.cfgStore.Config().IsConfigured() {
				if s.logger != nil {
					s.logger.Warn("Workspace created but no LLM providers configured — " +
						"requests will fail with 'agent not initialized'. " +
						"Configure a provider in phosphor.json to use the OpenAI API.")
				}
			}
			workspaceID = ws.ID

			// Attach a persistent OpenAI API client so the workspace stays alive
			const openaiClientID = "00000000-0000-0000-0000-000000000001"
			if err := s.srv.Backend().AttachClient(workspaceID, openaiClientID); err != nil {
				if s.logger != nil {
					s.logger.Warn("Failed to attach persistent OpenAI client", "error", err)
				}
			} else if s.logger != nil {
				s.logger.Info("Attached persistent OpenAI API client", "client_id", openaiClientID)
			}
		} else {
			if s.logger != nil {
				s.logger.Info("Using existing workspace", "workspace_id", wsList[0].ID, "path", wsList[0].Path)
			}
			workspaceID = wsList[0].ID

			// Attach a persistent OpenAI API client so the workspace stays alive
			const openaiClientID = "00000000-0000-0000-0000-000000000001"
			if err := s.srv.Backend().AttachClient(workspaceID, openaiClientID); err != nil {
				if s.logger != nil {
					s.logger.Warn("Failed to attach persistent OpenAI client", "error", err)
				}
			} else if s.logger != nil {
				s.logger.Info("Attached persistent OpenAI API client", "client_id", openaiClientID)
			}
		}

		// Create OpenAI handler with shared backend
		if s.logger != nil {
			s.logger.Info("Creating OpenAI handler", "workspace_id", workspaceID)
		}
		openaiHandler := openai.NewHandler(s.srv.Backend(), workspaceID, s.logger, openaiCfg.acceptSystemPrompt, openaiCfg.logRequestBody)
		if s.logger != nil {
			s.logger.Info("OpenAI handler created", "handler_workspace_id", openaiHandler.WorkspaceID())
		}

		// Register real OpenAI endpoints
		s.echoSrv.GET("/health", openaiHandler.HandleHealth)
		s.echoSrv.POST("/v1/chat/completions", openaiHandler.HandleChatCompletions)
		s.echoSrv.POST("/v1/responses", openaiHandler.HandleResponses)
		s.echoSrv.GET("/v1/models", openaiHandler.HandleModels)

		// Register stateless session management endpoints
		s.echoSrv.GET("/v1/stateless-sessions", s.listStatelessSessions)
		s.echoSrv.POST("/v1/stateless-sessions/:session-id/prune", s.pruneStatelessSession)

		openaiAddr := fmt.Sprintf("%s:%d", openaiCfg.host, openaiCfg.port)
		if s.logger != nil {
			s.logger.Info("Starting OpenAI-compatible API on Echo...", "addr", fmt.Sprintf("http://%s", openaiAddr))
		}

		s.echoServer = &http.Server{
			Addr:    openaiAddr,
			Handler: s.echoSrv,
		}

		go func() {
			if s.logger != nil {
				s.logger.Info("OpenAI API server starting...", "addr", openaiAddr)
			}
			if err := s.echoServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				if s.logger != nil {
					s.logger.Error("OpenAI API server error", "error", err)
				}
			} else {
				if s.logger != nil {
					s.logger.Info("OpenAI API server stopped")
				}
			}
		}()
	}

	// 3. Monitor context cancellation to shut down servers automatically
	go func() {
		<-ctx.Done()
		s.Stop(context.Background())
	}()

	return nil
}

// Stop gracefully shuts down both the management API and the OpenAI API.
func (s *Service) Stop(ctx context.Context) error {
	var shutdownErr error
	s.stopOnce.Do(func() {
		var errs []error
		if s.srv != nil {
			if err := s.srv.Shutdown(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if s.echoServer != nil {
			if err := s.echoServer.Shutdown(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			shutdownErr = fmt.Errorf("errors during shutdown: %v", errs)
		}
	})
	return shutdownErr
}

// Describe returns a description of the HTTP API service.
func (s *Service) Describe() string {
	mainAddr := fmt.Sprintf("%s://%s", s.scheme, s.host)
	openaiCfg := s.resolveOpenaiConfig()

	if !openaiCfg.enabled {
		return fmt.Sprintf("HTTP API Service (Management API: %s, OpenAI API: disabled)", mainAddr)
	}

	echoAddr := fmt.Sprintf("http://%s:%d", openaiCfg.host, openaiCfg.port)
	return fmt.Sprintf("HTTP API Service (Management API: %s, OpenAI API: %s)", mainAddr, echoAddr)
}
