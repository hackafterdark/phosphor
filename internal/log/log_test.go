package log

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompileFilters(t *testing.T) {
	t.Parallel()

	filters, err := compileFilters([]LogFilter{{Field: "message", Pattern: `Failed to save`}})
	require.NoError(t, err)
	require.Len(t, filters, 1)

	_, err = compileFilters([]LogFilter{{Field: "message", Pattern: `(unclosed`}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "rejected")

	_, err = compileFilters([]LogFilter{{Field: "message", Pattern: `(?:[A-Za-z]+)+=[A-Za-z]+`}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ReDoS")
}

// sourceGateTestFile is the source-file prefix matching this test
// file's own runtime source path.
const sourceGateTestFile = "internal/log"

func TestSourceGatedHandlerDropsGatedDebug(t *testing.T) {
	var buf bytes.Buffer
	h := &sourceGatedHandler{
		inner:   slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		gates:   map[string]slog.Level{sourceGateTestFile: slog.LevelInfo},
		maxGate: slog.LevelInfo,
	}
	logger := slog.New(h)

	logger.Debug("gated debug line")
	logger.Info("kept info line")

	require.NotContains(t, buf.String(), "gated debug line")
	require.Contains(t, buf.String(), "kept info line")
}

func TestSourceGatedHandlerKeepsUngatedSource(t *testing.T) {
	var buf bytes.Buffer
	h := &sourceGatedHandler{
		inner:   slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		gates:   map[string]slog.Level{"github.com/charmbracelet/x/powernap/pkg/transport": slog.LevelInfo},
		maxGate: slog.LevelInfo,
	}
	logger := slog.New(h)

	// This record's source is internal/log, which no gate covers, so
	// it must survive even below the gate level.
	logger.Debug("first-party debug line")

	require.Contains(t, buf.String(), "first-party debug line")
}

func TestSourceGatedMergesUserLevels(t *testing.T) {
	// Re-enabling a gated source at debug must let its debug records
	// through; the user entry is merged over the default gate.
	var buf bytes.Buffer
	h := sourceGated(
		slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
		&LogConfig{SourceLevels: map[string]string{sourceGateTestFile: "debug"}},
	)
	logger := slog.New(h)

	logger.Debug("re-enabled debug line")

	require.Contains(t, buf.String(), "re-enabled debug line")
}
