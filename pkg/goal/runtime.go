package goal

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/pkg/agent/notify"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/pubsub"
)

//go:embed continuation_prompt.md.tpl
var continuationPromptTmpl []byte

var continuationTpl = template.Must(
	template.New("continuation").Parse(string(continuationPromptTmpl)),
)

type AgentRunner interface {
	Run(ctx context.Context, sessionID string, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error)
	IsSessionBusy(sessionID string) bool
	QueuedPrompts(sessionID string) int
}

// RuntimeLimits carries the tunables the runtime reads lazily from config so
// a phosphor.json change is honored without rebuilding the runtime. Zero
// values fall back to the package defaults.
type RuntimeLimits struct {
	// WatchdogInterval is the delay between watchdog sweeps. 0 means
	// DefaultWatchdogInterval.
	WatchdogInterval time.Duration
	// MaxConsecutiveErrors is how many failed continuation turns in a row a
	// goal tolerates before the runtime auto-pauses it for review. 0 means
	// DefaultMaxConsecutiveErrors; a negative value means unlimited (the
	// transient-error retries keep going as long as the classifier keeps
	// agreeing they are transient).
	MaxConsecutiveErrors int
}

type Runtime struct {
	store  Service
	agent  AgentRunner
	notify pubsub.Publisher[notify.Notification]

	// maxContinuations resolves the per-goal continuation budget at the
	// moment a continuation is about to start. It is read lazily so a
	// config change is honored without rebuilding the runtime. A nil
	// accessor falls back to DefaultMaxContinuations.
	maxContinuations func() int

	// limits resolves the watchdog interval and consecutive-error cap,
	// lazily, like maxContinuations. A nil accessor uses all defaults.
	limits func() RuntimeLimits

	// budgetMu guards continuations.
	budgetMu      sync.Mutex
	continuations map[string]continuationState

	// claimsMu guards claims. A claim is held for the entire duration of a
	// continuation chain so the edge-triggered path (OnTurnFinished) and the
	// level-triggered path (watchdog tick) can never double-start a run for
	// the same session.
	claimsMu sync.Mutex
	claims   map[string]bool

	// errMu guards errState, the transient-error retry bookkeeping.
	errMu    sync.Mutex
	errState map[string]*turnFailure

	// retryBaseDelay is the first step of the exponential backoff. It is a
	// field rather than a constant so tests can compress the schedule.
	retryBaseDelay time.Duration

	watchMu   sync.Mutex
	watchStop chan struct{}
	watchDone chan struct{}

	// quiescing latches once shutdown begins: no new continuations start
	// and scheduled retries become no-ops. Without it, the shutdown
	// CancelAll would cancel in-flight goal runs, each cancellation would
	// look like a user cancel, and the goals would be paused on the way
	// out - defeating the restart resilience the watchdog provides.
	quiescing atomic.Bool
}

// continuationState tracks how many synthetic continuation turns have been
// started for the currently active goal of a session. It is keyed so a
// recreate of the goal (a new goal ID) automatically resets the count.
type continuationState struct {
	goalID string
	count  int
}

// turnFailure is the consecutive-failure streak for the active goal of a
// session. A new goal ID resets the streak. nextRetryAt gates MaybeContinue
// while a scheduled retry is pending; a zero-value time means "no gate".
type turnFailure struct {
	goalID      string
	failures    int
	nextRetryAt time.Time
}

// DefaultMaxContinuations is the fallback budget applied when no explicit
// budget is configured. It bounds a goal whose model never calls
// update_goal(complete) so an unattended run cannot loop indefinitely.
const DefaultMaxContinuations = 25

// DefaultWatchdogInterval is the delay between watchdog sweeps that revive
// idle sessions holding an active goal.
const DefaultWatchdogInterval = 30 * time.Second

// DefaultMaxConsecutiveErrors is how many consecutive failed continuation
// turns a goal tolerates before it is auto-paused for review.
const DefaultMaxConsecutiveErrors = 5

// DefaultRetryBaseDelay is the first backoff step after a transient turn
// failure. Subsequent consecutive failures double it, capped at
// maxRetryDelay.
const DefaultRetryBaseDelay = 5 * time.Second

// maxRetryDelay caps the exponential backoff between transient-error retries.
const maxRetryDelay = 5 * time.Minute

// initialSweepDelay is how long after StartWatchdog the first sweep runs. It
// is short so a restart re-arms orphaned goals quickly, but not zero so the
// providers and models have finished warming up. It is a var so tests can
// compress the schedule.
var initialSweepDelay = 15 * time.Second

func NewRuntime(
	store Service,
	agent AgentRunner,
	notify pubsub.Publisher[notify.Notification],
	maxContinuations func() int,
	limits func() RuntimeLimits,
) *Runtime {
	return &Runtime{
		store:            store,
		agent:            agent,
		notify:           notify,
		maxContinuations: maxContinuations,
		limits:           limits,
		continuations:    make(map[string]continuationState),
		claims:           make(map[string]bool),
		errState:         make(map[string]*turnFailure),
		retryBaseDelay:   DefaultRetryBaseDelay,
	}
}

// budget returns the effective continuation budget. A configured value of 0
// means use the default; a negative value means unlimited.
func (r *Runtime) budget() int {
	if r.maxContinuations == nil {
		return DefaultMaxContinuations
	}
	v := r.maxContinuations()
	if v == 0 {
		return DefaultMaxContinuations
	}
	return v
}

func (r *Runtime) currentLimits() RuntimeLimits {
	if r.limits == nil {
		return RuntimeLimits{}
	}
	return r.limits()
}

// ResetContinuations clears the continuation counter and the consecutive-
// failure streak for a session. It is called when a goal is created,
// resumed, or cleared so the user gets a fresh budget window after
// consciously deciding to keep going.
func (r *Runtime) ResetContinuations(sessionID string) {
	if r == nil {
		return
	}
	r.budgetMu.Lock()
	delete(r.continuations, sessionID)
	r.budgetMu.Unlock()

	r.errMu.Lock()
	delete(r.errState, sessionID)
	r.errMu.Unlock()
}

// Quiesce permanently stops the goal loop for this process: the watchdog
// stops (and can no longer be started) and scheduled retries become no-ops.
// App shutdown calls it before cancelling in-flight agent runs so shutdown
// cancellations are not mistaken for user cancels and do not pause goals on
// the way out.
func (r *Runtime) Quiesce() {
	if r == nil {
		return
	}
	r.quiescing.Store(true)
	r.StopWatchdog()
}

// consumeContinuation increments and checks the budget for the session's
// active goal. It reports whether the continuation is allowed to proceed.
// When the budget is exhausted it pauses the goal, notifies the user, and
// returns false so the caller does not start another turn.
func (r *Runtime) consumeContinuation(ctx context.Context, g *Goal) bool {
	budget := r.budget()
	if budget < 0 {
		return true
	}

	r.budgetMu.Lock()
	st := r.continuations[g.SessionID]
	if st.goalID != g.GoalID {
		st = continuationState{goalID: g.GoalID}
	}
	st.count++
	r.continuations[g.SessionID] = st
	used := st.count
	r.budgetMu.Unlock()

	if used <= budget {
		return true
	}

	// Budget exhausted: pause the goal so it stops burning tokens and ask
	// the user what to do.
	if _, err := r.store.UpdateStatus(ctx, g.SessionID, g.GoalID, GoalPaused); err != nil {
		slog.Error("Failed to auto-pause goal at continuation budget", "session_id", g.SessionID, "goal_id", g.GoalID, "error", err)
	} else {
		slog.Info("Goal auto-paused at continuation budget", "session_id", g.SessionID, "goal_id", g.GoalID, "budget", budget)
	}

	if r.notify != nil {
		r.notify.Publish(pubsub.CreatedEvent, notify.Notification{
			SessionID: g.SessionID,
			Type:      notify.TypeGoalPaused,
			Message: fmt.Sprintf(
				"Goal reached the %d-continuation limit and was paused so it can't run unattended. Review the progress, then open the Commands menu (Ctrl+P) and choose \"Resume Goal\" to grant another %d continuations, or \"Clear Goal\" to finish.",
				budget, budget,
			),
		})
	}
	return false
}

// claim acquires the single-flight claim for a session, reporting whether it
// was free. The holder must call release with the same sessionID.
func (r *Runtime) claim(sessionID string) bool {
	r.claimsMu.Lock()
	defer r.claimsMu.Unlock()
	if r.claims[sessionID] {
		return false
	}
	r.claims[sessionID] = true
	return true
}

func (r *Runtime) release(sessionID string) {
	r.claimsMu.Lock()
	delete(r.claims, sessionID)
	r.claimsMu.Unlock()
}

// failureFor returns the failure streak for the session's current goal,
// resetting it when the goal ID changed.
func (r *Runtime) failureFor(sessionID, goalID string) *turnFailure {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	st := r.errState[sessionID]
	if st == nil || st.goalID != goalID {
		st = &turnFailure{goalID: goalID}
		r.errState[sessionID] = st
	}
	return st
}

// awaitingRetry reports whether a transient-error retry is scheduled and not
// yet due for the session's active goal. While it returns true, MaybeContinue
// must not start a run: the scheduled callback owns the next attempt, and
// starting one here would double-spend the budget.
func (r *Runtime) awaitingRetry(sessionID string) bool {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	st := r.errState[sessionID]
	if st == nil || st.failures == 0 {
		return false
	}
	return time.Now().Before(st.nextRetryAt)
}

// recordTurnSuccess clears the consecutive-failure streak. Any completed
// turn - synthetic or user-driven - proves the pipeline is alive again.
func (r *Runtime) recordTurnSuccess(sessionID string) {
	r.errMu.Lock()
	delete(r.errState, sessionID)
	r.errMu.Unlock()
}

// retryDelay returns the backoff for the nth consecutive failure:
// base * 2^(n-1), capped at maxRetryDelay.
func (r *Runtime) retryDelay(failures int) time.Duration {
	base := r.retryBaseDelay
	if base <= 0 {
		base = DefaultRetryBaseDelay
	}
	if failures < 1 {
		failures = 1
	}
	d := base
	for i := 1; i < failures && d < maxRetryDelay; i++ {
		d *= 2
	}
	if d > maxRetryDelay {
		d = maxRetryDelay
	}
	return d
}

// pauseGoal pauses the goal (unless already gone) and tells the user why.
// It is the single "hand the wheel back to the human" exit for the runtime.
func (r *Runtime) pauseGoal(ctx context.Context, g *Goal, reason string) {
	if _, err := r.store.UpdateStatus(ctx, g.SessionID, g.GoalID, GoalPaused); err != nil {
		slog.Error("Failed to pause goal", "session_id", g.SessionID, "goal_id", g.GoalID, "error", err)
		return
	}
	if r.notify != nil {
		r.notify.Publish(pubsub.CreatedEvent, notify.Notification{
			SessionID: g.SessionID,
			Type:      notify.TypeGoalPaused,
			Message:   reason,
		})
	}
}

// OnTurnFinished is the edge trigger: a run ended cleanly, so decide whether
// the goal needs another synthetic turn. A success always clears the
// consecutive-failure streak first - the streak only counts uninterrupted
// failures.
func (r *Runtime) OnTurnFinished(ctx context.Context, sessionID string) {
	if r == nil {
		return
	}
	r.recordTurnSuccess(sessionID)
	err := r.MaybeContinue(ctx, sessionID)
	if err != nil {
		slog.Error("Goal runtime continuation failed", "session_id", sessionID, "error", err)
	}
}

// OnTurnError is the error counterpart to OnTurnFinished: a run ended in
// failure, and the runtime - not the coordinator - decides what that means
// for the goal. Transient failures schedule a backed-off continuation;
// permanent ones, and user cancellations, pause the goal with an explanation
// instead of leaving an idle zombie the watchdog would fight over. It never
// blocks: callers sit on the run's completion path.
func (r *Runtime) OnTurnError(ctx context.Context, sessionID string, err error) {
	if r == nil || r.store == nil || r.agent == nil || err == nil {
		return
	}
	if r.quiescing.Load() {
		return
	}

	goal, gerr := r.store.Get(ctx, sessionID)
	if gerr != nil || goal == nil || goal.Status != GoalActive {
		if gerr != nil {
			slog.Error("Failed to load goal after turn error", "session_id", sessionID, "error", gerr)
		}
		return
	}

	switch ClassifyTurnError(err) {
	case TurnErrorCancelled:
		slog.Info("Goal paused after run cancellation", "session_id", sessionID, "goal_id", goal.GoalID)
		r.pauseGoal(ctx, goal, fmt.Sprintf(
			"Goal paused in %q: the run was cancelled. Open the Commands menu (Ctrl+P) and choose \"Resume Goal\" to continue, or \"Clear Goal\" to finish.",
			sessionID,
		))
	case TurnErrorPermanent:
		slog.Warn("Goal paused after permanent turn error", "session_id", sessionID, "goal_id", goal.GoalID, "error", err)
		r.pauseGoal(ctx, goal, fmt.Sprintf(
			"Goal paused after a non-retryable error: %v. Review the session, address the cause, then choose \"Resume Goal\".",
			err,
		))
	case TurnErrorTransient:
		st := r.failureFor(sessionID, goal.GoalID)
		limit := r.currentLimits().MaxConsecutiveErrors
		if limit == 0 {
			limit = DefaultMaxConsecutiveErrors
		}

		r.errMu.Lock()
		st.failures++
		failures := st.failures
		r.errMu.Unlock()

		if limit >= 0 && failures >= limit {
			slog.Warn("Goal paused after repeated transient failures", "session_id", sessionID, "goal_id", goal.GoalID, "failures", failures, "error", err)
			r.pauseGoal(ctx, goal, fmt.Sprintf(
				"Goal paused after %d consecutive failures (last error: %v). Review the session, then choose \"Resume Goal\" to retry or \"Clear Goal\" to finish.",
				failures, err,
			))
			return
		}

		delay := r.retryDelay(failures)
		r.errMu.Lock()
		st.nextRetryAt = time.Now().Add(delay)
		r.errMu.Unlock()

		slog.Warn("Goal turn failed with a retryable error; continuation scheduled",
			"session_id", sessionID, "goal_id", goal.GoalID, "consecutive_failures", failures, "retry_in", delay, "error", err)
		time.AfterFunc(delay, func() {
			if r.quiescing.Load() {
				return
			}
			if err := r.MaybeContinue(context.Background(), sessionID); err != nil {
				slog.Error("Scheduled goal retry failed", "session_id", sessionID, "error", err)
			}
		})
	}
}

// MaybeContinue is the level-triggered heart of the runtime: if the session
// holds an active goal and is otherwise idle, run continuation turns until
// the goal finishes, pauses, hits its budget, the session gains work, or a
// continuation fails. Every entry point (edge trigger, watchdog tick,
// scheduled retry, explicit GoalSet/Resume/Start) funnels through here, and
// the per-session claim makes concurrent entries collapse into one chain.
func (r *Runtime) MaybeContinue(ctx context.Context, sessionID string) error {
	if r == nil || r.store == nil || r.agent == nil {
		return nil
	}
	if r.quiescing.Load() {
		return nil
	}
	if !r.claim(sessionID) {
		// Another chain is already driving this session; it re-checks the
		// goal after every turn, so this request is covered.
		return nil
	}
	defer r.release(sessionID)

	for {
		if r.quiescing.Load() {
			return nil
		}
		if r.agent.IsSessionBusy(sessionID) {
			return nil
		}
		if r.agent.QueuedPrompts(sessionID) > 0 {
			return nil
		}
		if r.awaitingRetry(sessionID) {
			// A transient-error retry is scheduled and not yet due; the
			// scheduled callback owns the next attempt.
			return nil
		}

		goal, err := r.store.Get(ctx, sessionID)
		if err != nil || goal == nil {
			return err
		}
		if goal.Status != GoalActive {
			return nil
		}

		// Enforce the continuation budget before starting another synthetic
		// turn. When exhausted this pauses the goal and asks the user what to
		// do instead of looping forever.
		if !r.consumeContinuation(ctx, goal) {
			return nil
		}

		prompt, err := r.RenderContinuationPrompt(goal)
		if err != nil {
			return fmt.Errorf("rendering continuation prompt: %w", err)
		}

		slog.Info("Starting synthetic continuation turn", "session_id", sessionID, "goal_id", goal.GoalID)

		if r.notify != nil {
			r.notify.Publish(pubsub.CreatedEvent, notify.Notification{
				SessionID: sessionID,
				Type:      notify.TypeGoalContinue,
			})
		}

		// Inject the current GoalID into the context for stale update protection.
		goalCtx := context.WithValue(ctx, GoalIDContextKey, goal.GoalID)

		if _, err := r.agent.Run(goalCtx, sessionID, prompt); err != nil {
			// The coordinator has already routed this through OnTurnError,
			// which either scheduled a backed-off retry or paused the goal.
			// Stop the chain; the retry or a human resume picks it up.
			return err
		}

		// Clean turn: the streak is dead, and the loop re-reads the goal to
		// see whether the model called it complete or the objective still
		// stands.
		r.recordTurnSuccess(sessionID)
	}
}

// StartWatchdog launches the level-triggered sweep that revives sessions
// holding an active goal but running nothing: the process restarted, a
// continuation goroutine died, or an error left the session idle while the
// goal still says "active". It is idempotent; call StopWatchdog (or
// Quiesce) to shut it down. A non-positive interval uses
// DefaultWatchdogInterval.
func (r *Runtime) StartWatchdog(interval time.Duration) {
	if r == nil {
		return
	}
	r.watchMu.Lock()
	defer r.watchMu.Unlock()
	if r.quiescing.Load() || r.watchStop != nil {
		return
	}
	if interval <= 0 {
		if cfg := r.currentLimits().WatchdogInterval; cfg > 0 {
			interval = cfg
		} else {
			interval = DefaultWatchdogInterval
		}
	}
	r.watchStop = make(chan struct{})
	r.watchDone = make(chan struct{})
	stop, done := r.watchStop, r.watchDone

	slog.Info("Goal watchdog started", "interval", interval.String())
	go func() {
		defer close(done)
		timer := time.NewTimer(initialSweepDelay)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				slog.Debug("Goal watchdog stopped")
				return
			case <-timer.C:
				r.scanOnce(context.Background())
				timer.Reset(interval)
			}
		}
	}()
}

// StopWatchdog halts the sweep and waits for the in-flight tick to finish.
// Idempotent: safe to call when the watchdog was never started.
func (r *Runtime) StopWatchdog() {
	if r == nil {
		return
	}
	r.watchMu.Lock()
	stop, done := r.watchStop, r.watchDone
	r.watchStop = nil
	r.watchDone = nil
	r.watchMu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	if done != nil {
		<-done
	}
}

// scanOnce sweeps every active goal and asks MaybeContinue to drive any
// session that is idle. MaybeContinue holds all the gates (busy, queued,
// backoff, claim, budget), so the sweep itself is allowed to be brute
// force.
func (r *Runtime) scanOnce(ctx context.Context) {
	if r == nil || r.store == nil {
		return
	}
	if r.quiescing.Load() {
		return
	}
	scanCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	goals, err := r.store.ListActive(scanCtx)
	if err != nil {
		slog.Error("Goal watchdog failed to list active goals", "error", err)
		return
	}
	for _, g := range goals {
		if err := r.MaybeContinue(scanCtx, g.SessionID); err != nil {
			slog.Error("Goal watchdog continuation failed", "session_id", g.SessionID, "error", err)
		}
	}
}

func (r *Runtime) RenderContinuationPrompt(g *Goal) (string, error) {
	var buf bytes.Buffer
	if err := continuationTpl.Execute(&buf, g); err != nil {
		return "", err
	}
	return buf.String(), nil
}
