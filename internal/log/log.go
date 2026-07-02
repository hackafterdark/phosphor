package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/term"
	"gopkg.in/natefinch/lumberjack.v2"
)

// LogConfig holds logging configuration passed to Setup.
type LogConfig struct {
	Enabled    bool
	Level      string // "off", "info", "debug"
	MaxSizeMB  int
	MaxAgeDays int
	MaxBackups int
	Compress   bool
	Filters    []LogFilter
}

// LogFilter defines a regex-based exclusion rule for log entries.
type LogFilter struct {
	Field   string
	Pattern string
}

var (
	initOnce    sync.Once
	initialized atomic.Bool
	logMu       sync.Mutex
	logRotator  *lumberjack.Logger
)

// Reset clears the initialization state so Setup can be called again.
// This is used when config reloads and logging settings change.
func Reset() {
	logMu.Lock()
	if logRotator != nil {
		logRotator.Close()
		logRotator = nil
	}
	logMu.Unlock()
	initOnce = sync.Once{}
	initialized.Store(false)
}

// Setup initializes the global slog logger using the provided config.
// If opts is nil or not enabled, logging writes to a discard handler.
func Setup(logFile string, debug bool, ws ...io.Writer) {
	opts := &LogConfig{
		Level:      "info",
		MaxSizeMB:  10,
		MaxAgeDays: 30,
	}
	SetupWithConfig(logFile, opts, debug, ws...)
}

// SetupWithConfig initializes the global slog logger with explicit logging
// configuration. This is the primary entry point; Setup is a convenience
// wrapper that provides sensible defaults.
func SetupWithConfig(logFile string, opts *LogConfig, debug bool, ws ...io.Writer) {
	initOnce.Do(func() {
		// If logging is disabled, use discard handler.
		if opts == nil || !opts.Enabled {
			slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
			initialized.Store(true)
			return
		}

		// Apply defaults for zero-valued fields.
		if opts.MaxSizeMB <= 0 {
			opts.MaxSizeMB = 10
		}
		if opts.MaxAgeDays <= 0 {
			opts.MaxAgeDays = 30
		}

		// Configure lumberjack log rotator.
		rotator := &lumberjack.Logger{
			Filename:   logFile,
			MaxSize:    opts.MaxSizeMB,
			MaxBackups: opts.MaxBackups,
			MaxAge:     opts.MaxAgeDays,
			Compress:   opts.Compress,
		}

		logMu.Lock()
		logRotator = rotator
		logMu.Unlock()

		// Determine log level.
		level := slog.LevelInfo
		if debug || opts.Level == "debug" {
			level = slog.LevelDebug
		} else if opts.Level == "off" {
			slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
			initialized.Store(true)
			return
		}

		handlerOpts := &slog.HandlerOptions{
			Level:     level,
			AddSource: true,
		}

		// Build base JSON handler with optional filtering.
		var baseHandler slog.Handler
		if len(opts.Filters) > 0 {
			filters, err := compileFilters(opts.Filters)
			if err != nil {
				slog.Warn("Failed to compile log filters, logging without filters", "err", err)
				baseHandler = slog.NewJSONHandler(logRotator, handlerOpts)
			} else {
				baseHandler = slog.NewJSONHandler(logRotator, handlerOpts)
				baseHandler = &filteredHandler{inner: baseHandler, filters: filters}
			}
		} else {
			baseHandler = slog.NewJSONHandler(logRotator, handlerOpts)
		}

		var handlers []slog.Handler
		handlers = append(handlers, baseHandler)

		for _, w := range ws {
			if w == nil {
				continue
			}
			if f, ok := w.(term.File); ok && term.IsTerminal(f.Fd()) {
				var termHandler slog.Handler
				if len(opts.Filters) > 0 {
					filters, err := compileFilters(opts.Filters)
					if err != nil {
						termHandler = slog.NewTextHandler(w, handlerOpts)
					} else {
						termHandler = slog.NewTextHandler(w, handlerOpts)
						termHandler = &filteredHandler{inner: termHandler, filters: filters}
					}
				} else {
					termHandler = slog.NewTextHandler(w, handlerOpts)
				}
				handlers = append(handlers, termHandler)
			} else {
				var jsonHandler slog.Handler
				if len(opts.Filters) > 0 {
					filters, err := compileFilters(opts.Filters)
					if err != nil {
						jsonHandler = slog.NewJSONHandler(w, handlerOpts)
					} else {
						jsonHandler = slog.NewJSONHandler(w, handlerOpts)
						jsonHandler = &filteredHandler{inner: jsonHandler, filters: filters}
					}
				} else {
					jsonHandler = slog.NewJSONHandler(w, handlerOpts)
				}
				handlers = append(handlers, jsonHandler)
			}
		}

		slog.SetDefault(slog.New(slog.NewMultiHandler(handlers...)))
		initialized.Store(true)
	})
}

// compileFilters compiles regex filters from config. Returns compiled filters
// or an error if any pattern is invalid.
func compileFilters(filters []LogFilter) ([]compiledFilter, error) {
	compiled := make([]compiledFilter, len(filters))
	for i, f := range filters {
		re, err := regexp.Compile(f.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid filter pattern for field %q: %w", f.Field, err)
		}
		compiled[i] = compiledFilter{field: strings.ToLower(f.Field), pattern: re}
	}
	return compiled, nil
}

type compiledFilter struct {
	field   string
	pattern *regexp.Regexp
}

// filteredHandler wraps a slog.Handler and drops records that match any filter.
type filteredHandler struct {
	inner   slog.Handler
	filters []compiledFilter
}

func (h *filteredHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *filteredHandler) Handle(ctx context.Context, r slog.Record) error {
	// Check filters against the record's message and attributes.
	for _, f := range h.filters {
		if f.pattern.MatchString(r.Message) {
			return nil // Skip this record.
		}
		r.Attrs(func(a slog.Attr) bool {
			if strings.EqualFold(a.Key, f.field) && f.pattern.MatchString(a.Value.String()) {
				return false // Skip this record.
			}
			return true
		})
	}
	return h.inner.Handle(ctx, r)
}

func (h *filteredHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &filteredHandler{inner: h.inner.WithAttrs(attrs), filters: h.filters}
}

func (h *filteredHandler) WithGroup(name string) slog.Handler {
	return &filteredHandler{inner: h.inner.WithGroup(name), filters: h.filters}
}

func RecoverPanic(name string, cleanup func()) {
	if r := recover(); r != nil {
		// Create a timestamped panic log file.
		timestamp := time.Now().Format("20060102-150405")
		filename := fmt.Sprintf("phosphor-panic-%s-%s.log", name, timestamp)

		file, err := os.Create(filename)
		if err == nil {
			defer file.Close()

			// Write panic information and stack trace.
			fmt.Fprintf(file, "Panic in %s: %v\n\n", name, r)
			fmt.Fprintf(file, "Time: %s\n\n", time.Now().Format(time.RFC3339))
			fmt.Fprintf(file, "Stack Trace:\n%s\n", debug.Stack())

			// Execute cleanup function if provided.
			if cleanup != nil {
				cleanup()
			}
		}
	}
}

func Initialized() bool {
	return initialized.Load()
}
