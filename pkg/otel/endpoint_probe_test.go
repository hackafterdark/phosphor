package otel

import (
	"context"
	"net"
	"testing"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestEndpointReachableRejectsDeadEndpoint(t *testing.T) {
	t.Parallel()

	// Port 1 on loopback is reserved and always refused; the probe must
	// fail fast rather than retry.
	require.False(t, endpointReachable(context.Background(), "127.0.0.1:1"))
}

func TestEndpointReachableAcceptsLiveListener(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	require.True(t, endpointReachable(context.Background(), ln.Addr().String()))
}

func TestDialTarget(t *testing.T) {
	t.Parallel()

	require.Equal(t, "localhost:4317", dialTarget("localhost:4317"))
	require.Equal(t, "localhost:4318", dialTarget("http://localhost:4318/v1/traces"))
	require.Equal(t, "collector:4317", dialTarget("https://collector:4317"))
}

func TestInitUnreachableEndpointFallsBackToLocal(t *testing.T) {
	// Init mutates package globals (memoryBuffer, tracer); never run in
	// parallel with the ring tests that swap them.
	prev := memoryBuffer
	defer func() { memoryBuffer = prev }()

	shutdown, err := Init(context.Background(), config.Observability{
		Endpoint:     "127.0.0.1:1",
		MemoryBuffer: 10,
	})
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))

	// The fallback keeps the in-process ring active for phosphor_trace.
	require.NotNil(t, memoryBuffer)
}

func TestInitEndpointWithBufferButNoNetwork(t *testing.T) {
	prev := memoryBuffer
	defer func() { memoryBuffer = prev }()

	// A live TCP listener that is not an OTLP collector still passes the
	// probe; export errors after startup are bounded by the exporter's
	// own timeout and reconnection period, so Init must not disable
	// tracing once the endpoint is reachable.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	shutdown, err := Init(context.Background(), config.Observability{
		Endpoint: ln.Addr().String(),
	})
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))
}
