package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/term"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/hackafterdark/phosphor/pkg/saferegex"
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
	// SourceLevels sets a minimum log level for records whose source
	// lives under the given package path prefix. Chatty third-party
	// loggers (e.g. the LSP transport) can be capped at "info" while
	// first-party debug logging stays enabled. nil applies the
	// DefaultSourceLevels.
	SourceLevels map[string]string
}

// DefaultSourceLevels caps third-party loggers that are chatty below
// their error surface: the powernap LSP transport logs every message
// and request at Debug, and gopls-style diagnostics publishing turns
// that into megabytes per session. User configuration overrides these
// entries (or re-enables them at a lower level).
var DefaultSourceLevels = map[string]string{
	"github.com/charmbracelet/x/powernap/pkg/transport": "info",
	"github.com/charmbracelet/x/powernap/pkg/lsp":       "info",
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
		// Mirror stderr into the panic log before anything can crash:
		// panic and runtime-fatal output goes straight to stderr and is
		// otherwise lost with the terminal scrollback.
		hookPanicLog(logFile)

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
		baseHandler = slog.NewJSONHandler(logRotator, handlerOpts)
		baseHandler = sourceGated(baseHandler, opts)
		if len(opts.Filters) > 0 {
			filters, err := compileFilters(opts.Filters)
			if err != nil {
				slog.Warn("Failed to compile log filters, logging without filters", "err", err)
			} else {
				baseHandler = &filteredHandler{inner: baseHandler, filters: filters}
			}
		}

		var handlers []slog.Handler
		handlers = append(handlers, baseHandler)

		for _, w := range ws {
			if w == nil {
				continue
			}
			if f, ok := w.(term.File); ok && term.IsTerminal(f.Fd()) {
				var termHandler slog.Handler
				termHandler = sourceGated(slog.NewTextHandler(w, handlerOpts), opts)
				if len(opts.Filters) > 0 {
					filters, err := compileFilters(opts.Filters)
					if err == nil {
						termHandler = &filteredHandler{inner: termHandler, filters: filters}
					}
				}
				handlers = append(handlers, termHandler)
			} else {
				var jsonHandler slog.Handler
				jsonHandler = sourceGated(slog.NewJSONHandler(w, handlerOpts), opts)
				if len(opts.Filters) > 0 {
					filters, err := compileFilters(opts.Filters)
					if err == nil {
						jsonHandler = &filteredHandler{inner: jsonHandler, filters: filters}
					}
				}
				handlers = append(handlers, jsonHandler)
			}
		}

		slog.SetDefault(slog.New(slog.NewMultiHandler(handlers...)))
		initialized.Store(true)
	})
}

// sourceGated wraps h with a per-source minimum-level gate. The
// default gates (DefaultSourceLevels) apply unless SourceLevels is set
// explicitly; user entries are merged over the defaults so a source
// can be re-enabled by naming it at a lower level.
func sourceGated(h slog.Handler, opts *LogConfig) slog.Handler {
	levels := maps.Clone(DefaultSourceLevels)
	maps.Copy(levels, opts.SourceLevels)
	gates := make(map[string]slog.Level, len(levels))
	maxGate := slog.LevelDebug
	for prefix, name := range levels {
		lvl, ok := parseLevel(name)
		if !ok {
			continue
		}
		gates[prefix] = lvl
		if lvl > maxGate {
			maxGate = lvl
		}
	}
	if len(gates) == 0 {
		return h
	}
	return &sourceGatedHandler{inner: h, gates: gates, maxGate: maxGate}
}

func parseLevel(name string) (slog.Level, bool) {
	switch strings.ToLower(name) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return 0, false
	}
}

// sourceGatedHandler drops records whose source lives under a gated
// package prefix and whose level is below the gate. Source resolution
// is skipped unless the record could possibly be gated.
type sourceGatedHandler struct {
	inner   slog.Handler
	gates   map[string]slog.Level
	maxGate slog.Level
}

func (h *sourceGatedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *sourceGatedHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < h.maxGate && r.PC != 0 {
		if file, ok := recordSourceFile(r.PC); ok {
			for prefix, lvl := range h.gates {
				if strings.Contains(file, prefix) && r.Level < lvl {
					return nil
				}
			}
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *sourceGatedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &sourceGatedHandler{inner: h.inner.WithAttrs(attrs), gates: h.gates, maxGate: h.maxGate}
}

func (h *sourceGatedHandler) WithGroup(name string) slog.Handler {
	return &sourceGatedHandler{inner: h.inner.WithGroup(name), gates: h.gates, maxGate: h.maxGate}
}

func recordSourceFile(pc uintptr) (string, bool) {
	frames := runtime.CallersFrames([]uintptr{pc})
	frame, _ := frames.Next()
	if frame.File == "" {
		return "", false
	}
	return frame.File, true
}

// compileFilters compiles regex filters from config. Returns compiled filters
// or an error if any pattern is invalid.
func compileFilters(filters []LogFilter) ([]compiledFilter, error) {
	compiled := make([]compiledFilter, len(filters))
	for i, f := range filters {
		if err := saferegex.Check(f.Pattern); err != nil {
			return nil, fmt.Errorf("filter pattern for field %q rejected: %w", f.Field, err)
		}
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
