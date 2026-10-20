package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_StartStop(t *testing.T) {
	svc := NewService(nil, "tcp", "127.0.0.1:0", t.TempDir(), nil)
	assert.Equal(t, "http-api", svc.Name())
	assert.Contains(t, svc.Describe(), "HTTP API Service (Management API: tcp://127.0.0.1:0, OpenAI API: http://127.0.0.1:8643)")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := svc.Start(ctx)
	require.NoError(t, err)

	// Stop it
	err = svc.Stop(ctx)
	require.NoError(t, err)
}
func TestMemoryGraphViewerEnabled(t *testing.T) {
	t.Parallel()

	// No config store: on by default.
	require.True(t, NewService(nil, "tcp", "127.0.0.1:0", t.TempDir(), nil).memoryGraphViewerEnabled())

	// Config present but no memory-graph entry: on by default.
	plain := config.NewTestStore(&config.Config{})
	require.True(t, NewService(plain, "tcp", "127.0.0.1:0", t.TempDir(), nil).memoryGraphViewerEnabled())

	// memory-graph entry present with enabled false: off.
	off := config.NewTestStore(&config.Config{
		Services: map[string]config.ServiceEntry{
			"memory-graph": {Enabled: false},
		},
	})
	require.False(t, NewService(off, "tcp", "127.0.0.1:0", t.TempDir(), nil).memoryGraphViewerEnabled())

	// memory-graph entry present with enabled true: on.
	on := config.NewTestStore(&config.Config{
		Services: map[string]config.ServiceEntry{
			"memory-graph": {Enabled: true},
		},
	})
	require.True(t, NewService(on, "tcp", "127.0.0.1:0", t.TempDir(), nil).memoryGraphViewerEnabled())
}
