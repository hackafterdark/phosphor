package httpapi

import (
	"context"
	"testing"
	"time"

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
