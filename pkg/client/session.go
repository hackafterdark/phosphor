package client

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/hackafterdark/phosphor/internal/app"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/session"
	"github.com/hackafterdark/phosphor/pkg/skills"
)

type SessionOption func(*sessionOptions)

type sessionOptions struct {
	largeModel     string
	smallModel     string
	dataDir        string
	cfg            *config.Config
	db             *sql.DB
	skipMigrations bool
	dbName         string
}

// WithModels overrides the models used for this session agent.
func WithModels(large, small string) SessionOption {
	return func(o *sessionOptions) {
		o.largeModel = large
		o.smallModel = small
	}
}

// WithDataDir overrides the .phosphor data directory.
func WithDataDir(dir string) SessionOption {
	return func(o *sessionOptions) {
		o.dataDir = dir
	}
}

// WithConfig sets a preloaded configuration to use.
func WithConfig(cfg *config.Config) SessionOption {
	return func(o *sessionOptions) {
		o.cfg = cfg
	}
}

// WithDB configures the session to use a pre-existing database connection.
// If provided, the SDK will automatically run migrations on the connection
// to ensure the schema is up to date, unless WithSkipMigrations is also provided.
// The SDK will not release/close the database when the session is closed.
func WithDB(database *sql.DB) SessionOption {
	return func(o *sessionOptions) {
		o.db = database
	}
}

// WithSkipMigrations configures the session to skip running database migrations.
func WithSkipMigrations() SessionOption {
	return func(o *sessionOptions) {
		o.skipMigrations = true
	}
}

// WithDBName configures a custom database file name (defaults to "phosphor.db").
func WithDBName(name string) SessionOption {
	return func(o *sessionOptions) {
		o.dbName = name
	}
}

type SessionHandle struct {
	ID         string
	workspace  string
	app        *app.App
	sess       session.Session
	config     *config.ConfigStore
	largeModel string
	smallModel string
	dbConn     *sql.DB
	ownsDB     bool
	dbName     string

	mu          sync.RWMutex
	subscribers map[EventType][]func(Event)
}

// NewSession initializes a programmatic Phosphor session agent.
func NewSession(ctx context.Context, wsDir string, opts ...SessionOption) (*SessionHandle, error) {
	opt := &sessionOptions{}
	for _, o := range opts {
		o(opt)
	}

	wsAbs, err := filepath.Abs(wsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve workspace directory path: %w", err)
	}

	// Load or initialize config store
	var store *config.ConfigStore
	if opt.cfg != nil {
		store, err = config.Init(wsAbs, opt.dataDir, false)
		if err != nil {
			return nil, fmt.Errorf("failed to load configuration: %w", err)
		}
		*store.Config() = *opt.cfg
	} else {
		store, err = config.Init(wsAbs, opt.dataDir, false)
		if err != nil {
			return nil, fmt.Errorf("failed to load configuration: %w", err)
		}
	}

	cfg := store.Config()

	// Initialize database connection
	var conn *sql.DB
	var ownsDB bool
	if opt.db != nil {
		conn = opt.db
		store.Overrides().SkipDBRelease = true
		if !opt.skipMigrations {
			if err := db.Migrate(conn); err != nil {
				return nil, fmt.Errorf("failed to migrate database: %w", err)
			}
		}
	} else {
		var connectOpts []db.ConnectOption
		if opt.skipMigrations {
			connectOpts = append(connectOpts, db.WithSkipMigrations(true))
		}
		if opt.dbName != "" {
			connectOpts = append(connectOpts, db.WithDatabaseName(opt.dbName))
		}
		conn, err = db.Connect(ctx, cfg.Options.DataDirectory, connectOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to database: %w", err)
		}
		ownsDB = true
	}

	// Initialize skills manager
	discoveryCfg := skills.DiscoveryConfig{
		SkillsPaths:    cfg.Options.SkillsPaths,
		DisabledSkills: cfg.Options.DisabledSkills,
		WorkingDir:     store.WorkingDir(),
	}
	if r := store.Resolver(); r != nil {
		discoveryCfg.Resolver = r.ResolveValue
	}

	allSkills, activeSkills, skillStates := skills.DiscoverFromConfig(discoveryCfg)
	skillsMgr := skills.NewManager(
		allSkills, activeSkills, skillStates,
		skills.WithGlobalMirror(),
		skills.WithResolvedPaths(discoveryCfg.ResolvePaths()),
		skills.WithWorkingDir(discoveryCfg.WorkingDir),
	)

	// Create App instance
	appInstance, err := app.New(ctx, conn, store, skillsMgr)
	if err != nil {
		if ownsDB {
			var releaseOpts []db.ConnectOption
			if opt.dbName != "" {
				releaseOpts = append(releaseOpts, db.WithDatabaseName(opt.dbName))
			}
			_ = db.Release(cfg.Options.DataDirectory, releaseOpts...)
		}
		return nil, fmt.Errorf("failed to initialize application: %w", err)
	}

	// Resolve session (e.g. create a new one)
	sess, err := appInstance.Sessions.Create(ctx, "sdk-session")
	if err != nil {
		appInstance.Shutdown()
		if ownsDB {
			var releaseOpts []db.ConnectOption
			if opt.dbName != "" {
				releaseOpts = append(releaseOpts, db.WithDatabaseName(opt.dbName))
			}
			_ = db.Release(cfg.Options.DataDirectory, releaseOpts...)
		}
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return &SessionHandle{
		ID:          sess.ID,
		workspace:   wsAbs,
		app:         appInstance,
		sess:        sess,
		config:      store,
		largeModel:  opt.largeModel,
		smallModel:  opt.smallModel,
		dbConn:      conn,
		ownsDB:      ownsDB,
		dbName:      opt.dbName,
		subscribers: make(map[EventType][]func(Event)),
	}, nil
}

// SendMessage sends a user message to the session agent.
func (h *SessionHandle) SendMessage(ctx context.Context, prompt string, output io.Writer) error {
	h.trigger(EventSessionStart, Event{
		Type:      EventSessionStart,
		SessionID: h.ID,
		Timestamp: time.Now(),
	})

	err := h.app.RunNonInteractive(ctx, output, prompt, h.largeModel, h.smallModel, true, h.ID, false)

	h.trigger(EventSessionEnd, Event{
		Type:      EventSessionEnd,
		SessionID: h.ID,
		Timestamp: time.Now(),
	})

	return err
}

// Subscribe registers an event handler for the given EventType.
func (h *SessionHandle) Subscribe(ctx context.Context, eventType EventType, handler func(event Event)) (func(), error) {
	h.mu.Lock()
	h.subscribers[eventType] = append(h.subscribers[eventType], handler)
	h.mu.Unlock()

	// Create a context for the background listener if needed (for broker-based events)
	subCtx, cancel := context.WithCancel(ctx)

	// Background listener for messages / completions
	if eventType == EventMessageDelta || eventType == EventToolCall || eventType == EventToolResult {
		go func() {
			messageEvents := h.app.Messages.Subscribe(subCtx)
			textLengths := make(map[string]int)
			seenTools := make(map[string]bool)
			seenResults := make(map[string]bool)

			for {
				select {
				case <-subCtx.Done():
					return
				case ev, ok := <-messageEvents:
					if !ok {
						return
					}
					msg := ev.Payload
					if msg.SessionID != h.ID {
						continue
					}

					if eventType == EventMessageDelta {
						var textContent string
						for _, p := range msg.Parts {
							if tc, ok := p.(message.TextContent); ok {
								textContent += tc.Text
							}
						}
						if len(textContent) > textLengths[msg.ID] {
							delta := textContent[textLengths[msg.ID]:]
							textLengths[msg.ID] = len(textContent)
							handler(Event{
								Type:      EventMessageDelta,
								SessionID: h.ID,
								Timestamp: time.Now(),
								MessageID: msg.ID,
								Text:      delta,
							})
						}
					}

					if eventType == EventToolCall {
						for _, p := range msg.Parts {
							if tc, ok := p.(message.ToolCall); ok {
								if !seenTools[tc.ID] {
									seenTools[tc.ID] = true
									handler(Event{
										Type:       EventToolCall,
										SessionID:  h.ID,
										Timestamp:  time.Now(),
										MessageID:  msg.ID,
										ToolCallID: tc.ID,
										ToolName:   tc.Name,
										ToolInput:  tc.Input,
									})
								}
							}
						}
					}

					if eventType == EventToolResult {
						for _, p := range msg.Parts {
							if tr, ok := p.(message.ToolResult); ok {
								if !seenResults[tr.ToolCallID] {
									seenResults[tr.ToolCallID] = true
									handler(Event{
										Type:       EventToolResult,
										SessionID:  h.ID,
										Timestamp:  time.Now(),
										MessageID:  msg.ID,
										ToolCallID: tr.ToolCallID,
										ToolName:   tr.Name,
										ToolResult: tr.Content,
									})
								}
							}
						}
					}
				}
			}
		}()
	}

	if eventType == EventAgentComplete {
		go func() {
			runCompletions := h.app.RunCompletions().Subscribe(subCtx)
			for {
				select {
				case <-subCtx.Done():
					return
				case ev, ok := <-runCompletions:
					if !ok {
						return
					}
					rc := ev.Payload
					if rc.SessionID != h.ID {
						continue
					}
					handler(Event{
						Type:      EventAgentComplete,
						SessionID: h.ID,
						Timestamp: time.Now(),
						MessageID: rc.MessageID,
						Text:      rc.Text,
						Error:     rc.Error,
					})
				}
			}
		}()
	}

	unsubscribe := func() {
		cancel()
		h.mu.Lock()
		defer h.mu.Unlock()
		handlers := h.subscribers[eventType]
		for i, hnd := range handlers {
			if reflect.ValueOf(hnd).Pointer() == reflect.ValueOf(handler).Pointer() {
				h.subscribers[eventType] = append(handlers[:i], handlers[i+1:]...)
				break
			}
		}
	}

	return unsubscribe, nil
}

// Close closes the session and releases its resources.
func (h *SessionHandle) Close() error {
	h.app.Shutdown()
	if h.ownsDB && h.config != nil && h.config.Config() != nil {
		var releaseOpts []db.ConnectOption
		if h.dbName != "" {
			releaseOpts = append(releaseOpts, db.WithDatabaseName(h.dbName))
		}
		_ = db.Release(h.config.Config().Options.DataDirectory, releaseOpts...)
	}
	return nil
}

func (h *SessionHandle) trigger(eventType EventType, event Event) {
	h.mu.RLock()
	handlers := h.subscribers[eventType]
	h.mu.RUnlock()

	for _, handler := range handlers {
		handler(event)
	}
}
