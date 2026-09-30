package goal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/agent/notify"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/pubsub"
	"github.com/stretchr/testify/require"
)

// -- fakes ------------------------------------------------------------------

type fakeStore struct {
	mu    sync.Mutex
	goals map[string]*Goal
}

func newFakeStore(g *Goal) *fakeStore {
	s := &fakeStore{goals: make(map[string]*Goal)}
	if g != nil {
		s.goals[g.SessionID] = g
	}
	return s
}

func (s *fakeStore) Subscribe(context.Context) <-chan pubsub.Event[Goal] {
	ch := make(chan pubsub.Event[Goal])
	close(ch)
	return ch
}

func (s *fakeStore) Get(_ context.Context, sessionID string) (*Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.goals[sessionID]; ok {
		cp := *g
		return &cp, nil
	}
	return nil, nil
}

func (s *fakeStore) ListActive(_ context.Context) ([]*Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Goal
	for _, g := range s.goals {
		if g.Status == GoalActive {
			cp := *g
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *fakeStore) Create(_ context.Context, sessionID, objective string) (*Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := &Goal{SessionID: sessionID, GoalID: "g-" + sessionID, Objective: objective, Status: GoalActive}
	s.goals[sessionID] = g
	return g, nil
}

func (s *fakeStore) UpdateStatus(_ context.Context, sessionID, goalID string, status GoalStatus) (*Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.goals[sessionID]
	if !ok || g.GoalID != goalID {
		return nil, errors.New("goal not found or stale goal ID")
	}
	g.Status = status
	cp := *g
	return &cp, nil
}

func (s *fakeStore) Clear(_ context.Context, sessionID string) (*Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.goals[sessionID]
	if !ok {
		return nil, nil
	}
	delete(s.goals, sessionID)
	return g, nil
}

func (s *fakeStore) status(t *testing.T, sessionID string) GoalStatus {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.goals[sessionID]
	require.True(t, ok, "expected a goal row for %s", sessionID)
	return g.Status
}

type fakeRunner struct {
	mu     sync.Mutex
	runs   int
	busy   bool
	queued int

	runErr error
	block  chan struct{}    // when non-nil, Run waits for it to close
	onRun  func(runNum int) // optional per-run hook (called with the mutex held)
}

func (f *fakeRunner) Run(ctx context.Context, _ string, _ string, _ ...message.Attachment) (*fantasy.AgentResult, error) {
	f.mu.Lock()
	f.runs++
	n := f.runs
	err := f.runErr
	block := f.block
	hook := f.onRun
	if hook != nil {
		hook(n)
	}
	f.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}
	return nil, err
}

func (f *fakeRunner) IsSessionBusy(string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy
}

func (f *fakeRunner) QueuedPrompts(string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queued
}

func (f *fakeRunner) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

func (f *fakeRunner) setBusy(b bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy = b
}

func (f *fakeRunner) setQueued(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queued = n
}

func (f *fakeRunner) setRunErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runErr = err
}

type fakeNotify struct {
	mu   sync.Mutex
	sent []notify.Notification
}

func (f *fakeNotify) Publish(_ pubsub.EventType, n notify.Notification) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
}

func (f *fakeNotify) PublishMustDeliver(_ context.Context, _ pubsub.EventType, n notify.Notification) {
	f.Publish(pubsub.CreatedEvent, n)
}

func (f *fakeNotify) paused() []notify.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []notify.Notification
	for _, n := range f.sent {
		if n.Type == notify.TypeGoalPaused {
			out = append(out, n)
		}
	}
	return out
}

// -- helpers ----------------------------------------------------------------

func activeGoal(sessionID string) *Goal {
	return &Goal{
		SessionID: sessionID,
		GoalID:    "goal-" + sessionID,
		Objective: "ship the thing",
		Status:    GoalActive,
	}
}

type runtimeOpt struct {
	budget int
	limits RuntimeLimits
}

func newTestRuntime(store *fakeStore, runner *fakeRunner, pub *fakeNotify, opt runtimeOpt) *Runtime {
	if opt.budget == 0 {
		// Cap chains by default: an unlimited budget in a test whose fake
		// runner never completes the goal would spin forever.
		opt.budget = DefaultMaxContinuations
	}
	r := NewRuntime(
		store,
		runner,
		pub,
		func() int { return opt.budget },
		func() RuntimeLimits { return opt.limits },
	)
	r.retryBaseDelay = 10 * time.Millisecond
	return r
}

// -- MaybeContinue ------------------------------------------------------------

func TestMaybeContinueRunsWhileGoalActive(t *testing.T) {
	store := newFakeStore(activeGoal("s1"))
	runner := &fakeRunner{}
	r := newTestRuntime(store, runner, &fakeNotify{}, runtimeOpt{})

	// End the chain deterministically: the second run completes the goal.
	runner.onRun = func(n int) {
		if n == 2 {
			_, _ = store.UpdateStatus(context.Background(), "s1", "goal-s1", GoalComplete)
		}
	}

	require.NoError(t, r.MaybeContinue(context.Background(), "s1"))
	require.Equal(t, 2, runner.runCount())
	require.Equal(t, GoalComplete, store.status(t, "s1"))
}

func TestMaybeContinueNoopsWhenNotRunnable(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeRunner, *fakeStore)
	}{
		{"busy", func(f *fakeRunner, _ *fakeStore) { f.setBusy(true) }},
		{"queued", func(f *fakeRunner, _ *fakeStore) { f.setQueued(1) }},
		{"no goal", func(_ *fakeRunner, s *fakeStore) { delete(s.goals, "s1") }},
		{"paused", func(_ *fakeRunner, s *fakeStore) { s.goals["s1"].Status = GoalPaused }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore(activeGoal("s1"))
			runner := &fakeRunner{}
			tc.setup(runner, store)
			r := newTestRuntime(store, runner, &fakeNotify{}, runtimeOpt{})
			require.NoError(t, r.MaybeContinue(context.Background(), "s1"))
			require.Zero(t, runner.runCount())
		})
	}
}

func TestMaybeContinueSingleFlight(t *testing.T) {
	store := newFakeStore(activeGoal("s1"))
	block := make(chan struct{})
	runner := &fakeRunner{block: block}
	r := newTestRuntime(store, runner, &fakeNotify{}, runtimeOpt{})

	first := make(chan struct{})
	go func() {
		defer close(first)
		_ = r.MaybeContinue(context.Background(), "s1")
	}()

	// Wait until the first chain is inside Run, then contend.
	require.Eventually(t, func() bool { return runner.runCount() == 1 }, time.Second, time.Millisecond)
	require.NoError(t, r.MaybeContinue(context.Background(), "s1")) // must not double-start
	require.Equal(t, 1, runner.runCount())

	// Complete the goal so the chain exits after releasing the block.
	_, _ = store.UpdateStatus(context.Background(), "s1", "goal-s1", GoalComplete)
	close(block)
	<-first
	require.Equal(t, 1, runner.runCount())
}

func TestMaybeContinueBudgetAutoPauses(t *testing.T) {
	store := newFakeStore(activeGoal("s1"))
	runner := &fakeRunner{}
	pub := &fakeNotify{}
	r := newTestRuntime(store, runner, pub, runtimeOpt{budget: 2})

	require.NoError(t, r.MaybeContinue(context.Background(), "s1"))
	require.Equal(t, 2, runner.runCount())
	require.Equal(t, GoalPaused, store.status(t, "s1"))
	require.NotEmpty(t, pub.paused())
}

// -- error handling -----------------------------------------------------------

func TestOnTurnErrorTransientSchedulesRetry(t *testing.T) {
	store := newFakeStore(activeGoal("s1"))
	runner := &fakeRunner{}
	pub := &fakeNotify{}
	// The first scheduled run completes the goal so the chain terminates.
	runner.onRun = func(n int) {
		if n == 1 {
			_, _ = store.UpdateStatus(context.Background(), "s1", "goal-s1", GoalComplete)
		}
	}
	r := newTestRuntime(store, runner, pub, runtimeOpt{})

	r.OnTurnError(context.Background(), "s1", &fantasy.ProviderError{StatusCode: 429, Message: "slow down"})
	// Not yet due is fine; the scheduled callback drives the retry.
	require.Eventually(t, func() bool { return runner.runCount() >= 1 }, 2*time.Second, 2*time.Millisecond)
	require.Empty(t, pub.paused(), "a transient error must not pause the goal")
}

func TestOnTurnErrorTransientCapPauses(t *testing.T) {
	store := newFakeStore(activeGoal("s1"))
	runner := &fakeRunner{busy: true} // keep scheduled retries from running
	pub := &fakeNotify{}
	r := newTestRuntime(store, runner, pub, runtimeOpt{limits: RuntimeLimits{MaxConsecutiveErrors: 3}})

	err := &fantasy.ProviderError{StatusCode: 503, Message: "unavailable"}
	r.OnTurnError(context.Background(), "s1", err)
	r.OnTurnError(context.Background(), "s1", err)
	require.Equal(t, GoalActive, store.status(t, "s1"), "two failures stay under the cap")
	r.OnTurnError(context.Background(), "s1", err)

	require.Equal(t, GoalPaused, store.status(t, "s1"))
	require.NotEmpty(t, pub.paused())
	require.Zero(t, runner.runCount())
}

func TestOnTurnErrorPermanentPausesWithoutRetry(t *testing.T) {
	store := newFakeStore(activeGoal("s1"))
	runner := &fakeRunner{}
	pub := &fakeNotify{}
	r := newTestRuntime(store, runner, pub, runtimeOpt{})

	r.OnTurnError(context.Background(), "s1", &fantasy.ProviderError{StatusCode: 400, Message: "invalid parameters"})
	require.Equal(t, GoalPaused, store.status(t, "s1"))
	require.NotEmpty(t, pub.paused())

	time.Sleep(100 * time.Millisecond) // give any (wrongly) scheduled retry a chance
	require.Zero(t, runner.runCount())
}

func TestOnTurnErrorCancelledPauses(t *testing.T) {
	store := newFakeStore(activeGoal("s1"))
	pub := &fakeNotify{}
	r := newTestRuntime(store, &fakeRunner{}, pub, runtimeOpt{})

	r.OnTurnError(context.Background(), "s1", context.Canceled)
	require.Equal(t, GoalPaused, store.status(t, "s1"))
	require.NotEmpty(t, pub.paused())
}

func TestOnTurnErrorIgnoresInactiveGoals(t *testing.T) {
	store := newFakeStore(activeGoal("s1"))
	_, _ = store.UpdateStatus(context.Background(), "s1", "goal-s1", GoalPaused)
	pub := &fakeNotify{}
	r := newTestRuntime(store, &fakeRunner{}, pub, runtimeOpt{})

	r.OnTurnError(context.Background(), "s1", &fantasy.ProviderError{StatusCode: 429})
	require.Empty(t, pub.paused())
}

func TestSuccessClearsFailureStreak(t *testing.T) {
	store := newFakeStore(activeGoal("s1"))
	runner := &fakeRunner{busy: true}
	r := newTestRuntime(store, runner, &fakeNotify{}, runtimeOpt{limits: RuntimeLimits{MaxConsecutiveErrors: 3}})

	err := &fantasy.ProviderError{StatusCode: 500}
	r.OnTurnError(context.Background(), "s1", err) // failures=1
	r.OnTurnError(context.Background(), "s1", err) // failures=2, still under the cap

	// A successful turn resets the streak, so the two errors below the cap
	// must not count toward the next pause decision.
	r.OnTurnFinished(context.Background(), "s1")
	r.OnTurnError(context.Background(), "s1", err) // failures=1 again, not 3
	require.Equal(t, GoalActive, store.status(t, "s1"))
}

func TestQuiesceStopsEverything(t *testing.T) {
	store := newFakeStore(activeGoal("s1"))
	runner := &fakeRunner{}
	pub := &fakeNotify{}
	r := newTestRuntime(store, runner, pub, runtimeOpt{})

	r.Quiesce()
	r.OnTurnError(context.Background(), "s1", &fantasy.ProviderError{StatusCode: 500})
	require.NoError(t, r.MaybeContinue(context.Background(), "s1"))

	time.Sleep(50 * time.Millisecond)
	require.Zero(t, runner.runCount())
	require.Empty(t, pub.paused())
	require.Equal(t, GoalActive, store.status(t, "s1"), "quiesce must not pause goals on the way out")
}

// -- watchdog ------------------------------------------------------------------

func TestWatchdogRevivesIdleActiveGoal(t *testing.T) {
	oldDelay, oldSweep := initialSweepDelay, initialSweepDelay
	defer func() { initialSweepDelay = oldDelay }()
	initialSweepDelay = 5 * time.Millisecond
	_ = oldSweep

	store := newFakeStore(activeGoal("s1"))
	runner := &fakeRunner{}
	r := newTestRuntime(store, runner, &fakeNotify{}, runtimeOpt{budget: 1})
	r.StartWatchdog(20 * time.Millisecond)
	defer r.StopWatchdog()

	require.Eventually(t, func() bool { return runner.runCount() >= 1 }, 2*time.Second, 2*time.Millisecond)
}

func TestWatchdogSkipsBusySessions(t *testing.T) {
	defer func(v time.Duration) { initialSweepDelay = v }(initialSweepDelay)
	initialSweepDelay = time.Millisecond

	store := newFakeStore(activeGoal("s1"))
	runner := &fakeRunner{busy: true}
	r := newTestRuntime(store, runner, &fakeNotify{}, runtimeOpt{})
	r.StartWatchdog(5 * time.Millisecond)
	defer r.StopWatchdog()

	time.Sleep(100 * time.Millisecond)
	require.Zero(t, runner.runCount())
}

func TestWatchdogStartStopIsIdempotent(t *testing.T) {
	store := newFakeStore(nil)
	r := newTestRuntime(store, &fakeRunner{}, &fakeNotify{}, runtimeOpt{})
	r.StartWatchdog(time.Millisecond)
	r.StartWatchdog(time.Millisecond)
	r.StopWatchdog()
	r.StopWatchdog()
}

// -- reset semantics -------------------------------------------------------------

func TestResetContinuationsClearsStreakAndBudget(t *testing.T) {
	store := newFakeStore(activeGoal("s1"))
	runner := &fakeRunner{busy: true}
	r := newTestRuntime(store, runner, &fakeNotify{}, runtimeOpt{limits: RuntimeLimits{MaxConsecutiveErrors: 2}})

	err := &fantasy.ProviderError{StatusCode: 429}
	r.OnTurnError(context.Background(), "s1", err)
	r.ResetContinuations("s1")
	r.OnTurnError(context.Background(), "s1", err)
	require.Equal(t, GoalActive, store.status(t, "s1"), "reset must restart the streak")
}

func TestRetryDelayBackoff(t *testing.T) {
	r := NewRuntime(nil, nil, nil, nil, nil)
	r.retryBaseDelay = 100 * time.Millisecond
	require.Equal(t, 100*time.Millisecond, r.retryDelay(1))
	require.Equal(t, 200*time.Millisecond, r.retryDelay(2))
	require.Equal(t, 400*time.Millisecond, r.retryDelay(3))
	require.Equal(t, maxRetryDelay, r.retryDelay(40))
}

// -- classification -----------------------------------------------------------------

type stubNetError struct{}

func (stubNetError) Error() string   { return "network flap" }
func (stubNetError) Timeout() bool   { return false }
func (stubNetError) Temporary() bool { return true }

func TestClassifyTurnError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want TurnErrorClass
	}{
		{"nil", nil, TurnErrorPermanent},
		{"canceled", context.Canceled, TurnErrorCancelled},
		{"canceled wrapped", fmtWrap(context.Canceled), TurnErrorCancelled},
		{"deadline", context.DeadlineExceeded, TurnErrorTransient},
		{"429", &fantasy.ProviderError{StatusCode: 429}, TurnErrorTransient},
		{"500", &fantasy.ProviderError{StatusCode: 500}, TurnErrorTransient},
		{"502", &fantasy.ProviderError{StatusCode: 502}, TurnErrorTransient},
		{"501 is permanent", &fantasy.ProviderError{StatusCode: 501}, TurnErrorPermanent},
		{"transient flag", &fantasy.ProviderError{StatusCode: 200, TransientError: true}, TurnErrorTransient},
		{"transport", &fantasy.ProviderError{StatusCode: 0}, TurnErrorTransient},
		{"auth flag", &fantasy.ProviderError{AuthError: true}, TurnErrorPermanent},
		{"401", &fantasy.ProviderError{StatusCode: 401}, TurnErrorPermanent},
		{"400", &fantasy.ProviderError{StatusCode: 400, Message: "bad schema"}, TurnErrorPermanent},
		{"context too large", &fantasy.ProviderError{StatusCode: 400, ContextTooLargeErr: true}, TurnErrorPermanent},
		{"net error", stubNetError{}, TurnErrorTransient},
		{"unknown", errors.New("mystery"), TurnErrorTransient},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ClassifyTurnError(tc.err))
		})
	}
}

func fmtWrap(inner error) error { return wrapErr{inner} }

type wrapErr struct{ inner error }

func (w wrapErr) Error() string { return "wrapped: " + w.inner.Error() }
func (w wrapErr) Unwrap() error { return w.inner }
