package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/pubsub"
	"github.com/stretchr/testify/require"
)

// TestSetupSubscriber_NormalFlow verifies that events published to the source
// broker are forwarded to the output broker.
func TestSetupSubscriber_NormalFlow(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	src := pubsub.NewBroker[string]()
	defer src.Shutdown()
	out := pubsub.NewBroker[tea.Msg]()
	defer out.Shutdown()

	ch := out.Subscribe(ctx)

	var wg sync.WaitGroup
	setupSubscriber(ctx, &wg, "test", src.Subscribe, out)

	// Yield so the subscriber goroutine can call src.Subscribe before we publish.
	time.Sleep(10 * time.Millisecond)

	src.Publish(pubsub.CreatedEvent, "hello")
	src.Publish(pubsub.CreatedEvent, "world")

	for range 2 {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for forwarded event")
		}
	}

	cancel()
	wg.Wait()
}

// TestSetupSubscriber_ContextCancellation verifies the goroutine exits cleanly
// when the context is cancelled.
func TestSetupSubscriber_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	src := pubsub.NewBroker[string]()
	defer src.Shutdown()
	out := pubsub.NewBroker[tea.Msg]()
	defer out.Shutdown()

	var wg sync.WaitGroup
	setupSubscriber(ctx, &wg, "test", src.Subscribe, out)

	src.Publish(pubsub.CreatedEvent, "event")
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("setupSubscriber goroutine did not exit after context cancellation")
	}
}

// TestEvents_ZeroConsumers verifies that publishing with no subscribers does
// not block or panic.
func TestEvents_ZeroConsumers(t *testing.T) {
	t.Parallel()

	broker := pubsub.NewBroker[tea.Msg]()
	defer broker.Shutdown()

	require.Equal(t, 0, broker.GetSubscriberCount())

	// Must not block.
	done := make(chan struct{})
	go func() {
		broker.Publish(pubsub.UpdatedEvent, tea.Msg("msg1"))
		broker.Publish(pubsub.UpdatedEvent, tea.Msg("msg2"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish with zero consumers blocked")
	}
}

// TestEvents_OneConsumer verifies that a single subscriber receives every event
// exactly once.
func TestEvents_OneConsumer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	broker := pubsub.NewBroker[tea.Msg]()
	defer broker.Shutdown()

	ch := broker.Subscribe(ctx)

	const n = 10
	for i := range n {
		broker.Publish(pubsub.UpdatedEvent, tea.Msg(i))
	}

	for i := range n {
		select {
		case ev := <-ch:
			require.Equal(t, tea.Msg(i), ev.Payload)
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
}

// TestEvents_NConsumers verifies that every subscriber receives every event
// exactly once, regardless of how many concurrent consumers are attached.
func TestEvents_NConsumers(t *testing.T) {
	t.Parallel()

	for _, n := range []int{2, 5, 10} {
		t.Run(fmt.Sprintf("consumers=%d", n), func(t *testing.T) {
			t.Parallel()
			testNConsumers(t, n)
		})
	}
}

func testNConsumers(t *testing.T, n int) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	broker := pubsub.NewBroker[tea.Msg]()
	defer broker.Shutdown()

	// Subscribe all N consumers before publishing.
	channels := make([]<-chan pubsub.Event[tea.Msg], n)
	for i := range n {
		channels[i] = broker.Subscribe(ctx)
	}
	require.Equal(t, n, broker.GetSubscriberCount())

	const numEvents = 20
	for i := range numEvents {
		broker.Publish(pubsub.UpdatedEvent, tea.Msg(i))
	}

	// Each consumer must receive all numEvents messages.
	var wg sync.WaitGroup
	for i, ch := range channels {
		wg.Go(func() {
			for j := range numEvents {
				select {
				case ev := <-ch:
					require.Equal(t, tea.Msg(j), ev.Payload,
						"consumer %d: wrong payload for event %d", i, j)
				case <-time.After(5 * time.Second):
					t.Errorf("consumer %d: timed out waiting for event %d", i, j)
					return
				}
			}
		})
	}
	wg.Wait()
}
// TestEnsureEmbeddedModel_NoConfig verifies that ensureEmbeddedModel returns
// immediately when embedded models are not configured.
func TestEnsureEmbeddedModel_NoConfig(t *testing.T) {
	t.Parallel()

	store := config.NewTestStore(&config.Config{})
	app := &App{
		config: store,
	}

	// Should not panic and return immediately.
	app.ensureEmbeddedModel(t.Context())
}

// TestEnsureEmbeddedModel_DisabledInference verifies that ensureEmbeddedModel
// returns when inference is not enabled.
func TestEnsureEmbeddedModel_DisabledInference(t *testing.T) {
	t.Parallel()

	store := config.NewTestStore(&config.Config{
		EmbeddedModels: &config.EmbeddedModels{
			Inference: &config.InferenceModel{
				Enabled: false,
			},
		},
	})
	app := &App{
		config: store,
	}

	app.ensureEmbeddedModel(t.Context())
}

// TestEnsureEmbeddedModel_NoModelPathRepo verifies that ensureEmbeddedModel
// returns when neither model_path nor model_repo is set.
func TestEnsureEmbeddedModel_NoModelPathRepo(t *testing.T) {
	t.Parallel()

	store := config.NewTestStore(&config.Config{
		EmbeddedModels: &config.EmbeddedModels{
			Inference: &config.InferenceModel{
				Enabled: true,
			},
		},
	})
	app := &App{
		config: store,
	}

	app.ensureEmbeddedModel(t.Context())
}

// TestEnsureEmbeddedModel_ModelExists verifies that ensureEmbeddedModel
// returns early when the model file already exists locally.
func TestEnsureEmbeddedModel_ModelExists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	modelPath := filepath.Join(tmpDir, "model.gguf")
	// Create the model file.
	f, err := os.Create(modelPath)
	require.NoError(t, err)
	f.Close()

	store := config.NewTestStore(&config.Config{
		EmbeddedModels: &config.EmbeddedModels{
			Inference: &config.InferenceModel{
				Enabled:   true,
				ModelPath: modelPath,
			},
		},
	})
	app := &App{
		config: store,
	}

	app.ensureEmbeddedModel(t.Context())
	// No error, returns immediately.
}