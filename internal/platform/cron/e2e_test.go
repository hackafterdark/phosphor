package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/hackafterdark/phosphor/internal/app"
	"github.com/hackafterdark/phosphor/pkg/agent"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/message"
	"github.com/hackafterdark/phosphor/pkg/session"
)

// fakeSessions implements the session.Service methods the cron service uses.
// The rest of the interface is inherited from the embedded nil value; the
// cron service must never call it.
type fakeSessions struct {
	session.Service

	mu        sync.Mutex
	titles    []string
	stateless map[string]bool
	deleted   map[string]bool
}

func (f *fakeSessions) Create(ctx context.Context, title string) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.titles = append(f.titles, title)
	return session.Session{ID: fmt.Sprintf("sess-%d", len(f.titles))}, nil
}

func (f *fakeSessions) UpdateStateless(ctx context.Context, sessionID string, stateless bool, service string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stateless[sessionID] = stateless
	return nil
}

func (f *fakeSessions) Delete(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted[id] = true
	return nil
}

type agentRun struct {
	sessionID string
	prompt    string
}

// fakeCoordinator records the prompts the cron service dispatches and
// inherits the unimplemented Coordinator methods from the embedded nil value.
type fakeCoordinator struct {
	agent.Coordinator

	ran chan agentRun
}

func (f *fakeCoordinator) Run(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	// Non-blocking send: an extra tick can never wedge the coordinator, so
	// cron.Stop() can never wait on the fake even under pathological delay.
	select {
	case f.ran <- agentRun{sessionID: sessionID, prompt: prompt}:
	default:
	}
	return nil, nil
}

// TestCronService_EndToEnd_FiresScheduledJob boots the full cron service
// against a job.md file scheduled to fire ~100ms after start and asserts the
// prompt reaches the agent coordinator with the ephemeral session lifecycle
// around it. No artificial sleeps: the wait is bounded by the schedule itself.
func TestCronService_EndToEnd_FiresScheduledJob(t *testing.T) {
	// Isolate from the developer's real global config and provider cache so
	// the test neither reads them nor dials their endpoints.
	isolated := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(isolated, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(isolated, ".local", "share"))

	workingDir := t.TempDir()
	config.SetWorkspaceTrustRequested(workingDir, true)
	t.Cleanup(func() { config.SetWorkspaceTrustRequested("", false) })

	store, err := config.Init(workingDir, filepath.Join(workingDir, "data"), false)
	require.NoError(t, err)

	jobsDir := filepath.Join(workingDir, ".phosphor", "jobs", "e2e")
	require.NoError(t, os.MkdirAll(jobsDir, 0o755))
	jobFile := "---\ntitle: \"e2e\"\nschedule: \"@every 100ms\"\n---\n\nDo the thing.\n"
	require.NoError(t, os.WriteFile(filepath.Join(jobsDir, "job.md"), []byte(jobFile), 0o644))

	sessions := &fakeSessions{stateless: map[string]bool{}, deleted: map[string]bool{}}
	coordinator := &fakeCoordinator{ran: make(chan agentRun, 8)}
	appInst := &app.App{Sessions: sessions, AgentCoordinator: coordinator}

	s := NewService(appInst, store, nil)
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	// The job file must be discovered and scheduled, and its default session
	// mode must be ephemeral (the mode whose omitted value once made every
	// fire skip as "unknown session mode").
	jobs := s.GetScheduledJobs()
	require.Len(t, jobs, 1)
	require.Equal(t, "ephemeral", jobs[0].SessionMode)

	select {
	case run := <-coordinator.ran:
		require.Equal(t, "Do the thing.", run.prompt)
		require.True(t, sessions.hasTitlePrefix("e2e "))
	case <-time.After(10 * time.Second):
		t.Fatal("scheduled job never fired")
	}

	// Ephemeral mode: the run marked its session stateless and deleted it
	// after the agent returned.
	require.Eventually(t, func() bool {
		sessions.mu.Lock()
		defer sessions.mu.Unlock()
		return len(sessions.deleted) == 1
	}, 5*time.Second, 10*time.Millisecond)
	sessions.mu.Lock()
	require.Equal(t, []string{"sess-1"}, keysOf(sessions.stateless))
	require.Equal(t, []string{"sess-1"}, keysOf(sessions.deleted))
	require.True(t, sessions.stateless["sess-1"])
	sessions.mu.Unlock()
}

func (f *fakeSessions) hasTitlePrefix(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, title := range f.titles {
		if strings.HasPrefix(title, prefix) {
			return true
		}
	}
	return false
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
