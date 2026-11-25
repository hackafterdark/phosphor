package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTUIOptions_HistoryLimits(t *testing.T) {
	t.Run("nil options", func(t *testing.T) {
		var opts *TUIOptions
		limit, batchSize := opts.HistoryLimits()
		require.Equal(t, 100, limit)
		require.Equal(t, 50, batchSize)
	})

	t.Run("default options", func(t *testing.T) {
		opts := &TUIOptions{}
		limit, batchSize := opts.HistoryLimits()
		require.Equal(t, 100, limit)
		require.Equal(t, 50, batchSize)
	})

	t.Run("custom options", func(t *testing.T) {
		customLimit := 200
		customBatch := 75
		opts := &TUIOptions{
			HistoryLimit:     &customLimit,
			HistoryBatchSize: &customBatch,
		}
		limit, batchSize := opts.HistoryLimits()
		require.Equal(t, 200, limit)
		require.Equal(t, 75, batchSize)
	})
}

func TestTUIOptions_ThinkingVisible(t *testing.T) {
	t.Run("nil options", func(t *testing.T) {
		var opts *TUIOptions
		require.True(t, opts.ThinkingVisible())
	})

	t.Run("key absent defaults to visible", func(t *testing.T) {
		opts := &TUIOptions{}
		require.True(t, opts.ThinkingVisible())
	})

	t.Run("explicitly hidden", func(t *testing.T) {
		hidden := false
		opts := &TUIOptions{ShowThinking: &hidden}
		require.False(t, opts.ThinkingVisible())
	})

	t.Run("explicitly shown", func(t *testing.T) {
		shown := true
		opts := &TUIOptions{ShowThinking: &shown}
		require.True(t, opts.ThinkingVisible())
	})
}
