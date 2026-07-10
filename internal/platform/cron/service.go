package cron

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"

	"github.com/hackafterdark/phosphor/internal/app"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/message"
)

// Service implements the platform.Service interface for the cron service.
type Service struct {
	app           *app.App
	appProvider   func() *app.App
	cfg           *config.ConfigStore
	cron          *cron.Cron
	mu            sync.RWMutex
	jobs          map[string]*cron.Entry // jobName -> cron entry
	scheduledJobs map[string]*Job        // jobName -> job metadata
	logger        *slog.Logger
	stopChan      chan struct{}
	wg            sync.WaitGroup
	failureCounts map[string]int
	failureMu     sync.Mutex
}

// NewService creates a new cron service.
func NewService(a *app.App, cfg *config.ConfigStore, logger *slog.Logger) *Service {
	return NewServiceWithProvider(func() *app.App { return a }, cfg, logger)
}

// NewServiceWithProvider creates a new cron service with a dynamic app provider.
func NewServiceWithProvider(appProvider func() *app.App, cfg *config.ConfigStore, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		appProvider:   appProvider,
		cfg:           cfg,
		cron:          cron.New(),
		logger:        logger,
		stopChan:      make(chan struct{}),
		jobs:          make(map[string]*cron.Entry),
		scheduledJobs: make(map[string]*Job),
		failureCounts: make(map[string]int),
	}
}

// getApp retrieves the active app instance.
func (s *Service) getApp() *app.App {
	if s.app != nil {
		return s.app
	}
	if s.appProvider != nil {
		s.app = s.appProvider()
		return s.app
	}
	return nil
}

// Name returns the service name.
func (s *Service) Name() string {
	return "cron"
}

// Start begins serving the cron service.
func (s *Service) Start(ctx context.Context) error {
	// Load the cron configuration from the config.
	cronConfig := s.cfg.CronConfig()
	if cronConfig == nil || !cronConfig.Enabled {
		s.logger.Info("cron service started but cron is disabled")
		// Still start the cron scheduler but with no jobs.
	} else {
		// Load jobs from the jobs directory.
		if err := s.loadJobsFromDir(ctx, cronConfig.JobsDirectory); err != nil {
			return fmt.Errorf("loading cron jobs: %w", err)
		}
	}

	// Start the cron scheduler.
	s.cron.Start()

	// Wait for stop signal.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-s.stopChan
		s.cron.Stop()
	}()

	return nil
}

// Stop gracefully shuts down the cron service.
func (s *Service) Stop(ctx context.Context) error {
	// Close the stop channel to signal the wait goroutine to exit.
	close(s.stopChan)

	// Wait for the waitgroup to finish.
	s.wg.Wait()

	return nil
}

// Describe returns a description of the service.
func (s *Service) Describe() string {
	return "Cron service for scheduled jobs"
}

// GetScheduledJobs returns the list of scheduled job metadata.
func (s *Service) GetScheduledJobs() []*Job {
	s.mu.RLock()
	jobs := make([]*Job, 0, len(s.scheduledJobs))
	for _, job := range s.scheduledJobs {
		jobs = append(jobs, job)
	}
	s.mu.RUnlock()
	return jobs
}

// loadJobsFromDir loads jobs from the given directory.
func (s *Service) loadJobsFromDir(ctx context.Context, dir string) error {
	// Make the path absolute if it's relative.
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(s.cfg.WorkingDir(), dir)
	}

	// Walk the directory.
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.EqualFold(filepath.Base(path), "job.md") {
			jobDir := filepath.Dir(path)
			jobName := filepath.Base(jobDir)
			// Load the job file.
			job, err := s.loadJobFile(path)
			if err != nil {
				s.logger.Error("failed to load job file", "job", jobName, "error", err)
				return nil // continue with other files
			}
			// Schedule the job.
			if err := s.scheduleJob(ctx, jobName, job); err != nil {
				s.logger.Error("failed to schedule job", "job", jobName, "error", err)
			}
		}
		return nil
	})
	return err
}

// loadJobFile loads a job.md file and parses its frontmatter.
func (s *Service) loadJobFile(path string) (*Job, error) {
	// Read the file.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading job file: %w", err)
	}

	// Parse frontmatter.
	// We'll use a simple approach: split by --- and parse the middle as YAML.
	// For simplicity, we assume the file starts with --- and ends with ---.
	parts := splitFrontmatter(string(data))
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid frontmatter in job file")
	}
	frontmatter := strings.TrimSpace(parts[1])
	content := strings.TrimSpace(strings.Join(parts[2:], "\n"))

	// Parse the frontmatter YAML.
	var fm JobFrontMatter
	if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}

	// The prompt is the content after the frontmatter.
	prompt := strings.TrimSpace(content)

	return &Job{
		Name:             fm.Title,
		Schedule:         fm.Schedule,
		Prompt:           prompt,
		SessionMode:      fm.SessionMode,
		Delivery:         fm.Delivery,
		SessionID:        fm.SessionID,
		AllowConcurrent:  fm.AllowConcurrent,
		FailureThreshold: fm.FailureThreshold,
	}, nil
}

// scheduleJob schedules a job with the cron scheduler.
func (s *Service) scheduleJob(ctx context.Context, jobName string, job *Job) error {
	// Add the job to the cron scheduler.
	entryID, _ := s.cron.AddFunc(job.Schedule, func() {
		s.runJob(ctx, jobName, job)
	})

		// Store the entry and job metadata.
		s.mu.Lock()
		s.jobs[jobName] = &cron.Entry{ID: entryID}
		s.scheduledJobs[jobName] = job
		s.mu.Unlock()

	s.logger.Info("scheduled job", "job", jobName, "schedule", job.Schedule)
	return nil
}

// runJob runs a single job.
func (s *Service) runJob(ctx context.Context, jobName string, job *Job) {
	s.logger.Info("running job", "job", jobName)

	appInst := s.getApp()
	if appInst == nil {
		s.logger.Error("no active workspace/app instance available to run job", "job", jobName)
		return
	}

	// Check if the job is disabled due to failure threshold.
	if job.FailureThreshold > 0 {
		failureCount := s.getFailureCount(jobName)
		if failureCount >= job.FailureThreshold {
			s.logger.Info("job is disabled due to failure threshold", "job", jobName, "failures", failureCount)
			return
		}
	}

	// Check for lock file if concurrent runs are not allowed.
	if !job.AllowConcurrent {
		jobDir := filepath.Join(s.cfg.WorkingDir(), ".phosphor/jobs", jobName)
		lockFile := filepath.Join(jobDir, ".job.lock")
		if _, err := os.Stat(lockFile); err == nil {
			s.logger.Info("job is locked, skipping run", "job", jobName)
			return
		}
		// Create lock file.
		if err := os.WriteFile(lockFile, []byte{}, 0o644); err != nil {
			s.logger.Error("failed to create lock file", "job", jobName, "error", err)
			return
		}
		// Remove lock file after the job finishes.
		defer os.Remove(lockFile)
	}

	// Determine the session ID based on session mode.
	var sessionID string
	var err error

	switch job.SessionMode {
	case "persistent":
		// Use the provided session ID or create a new one if not provided.
		if job.SessionID != "" {
			// Try to get the session.
			session, err := appInst.Sessions.Get(ctx, job.SessionID)
			if err != nil {
				s.logger.Error("failed to get session", "job", jobName, "error", err)
				// If session not found, create a new one.
				session, err = appInst.Sessions.Create(ctx, jobName)
				if err != nil {
					s.logger.Error("failed to create session", "job", jobName, "error", err)
					return
				}
			}
			sessionID = session.ID
		} else {
			// Create a new session with the job name as title.
			session, err := appInst.Sessions.Create(ctx, jobName)
			if err != nil {
				s.logger.Error("failed to create session", "job", jobName, "error", err)
				return
			}
			sessionID = session.ID
		}
		if err := appInst.Sessions.UpdateStateless(ctx, sessionID, false, "cron"); err != nil {
			s.logger.Error("failed to update session stateless status", "job", jobName, "error", err)
		}
	case "ephemeral", "per_run":
		// Create a new session for each run.
		session, err := appInst.Sessions.Create(ctx, jobName)
		if err != nil {
			s.logger.Error("failed to create session", "job", jobName, "error", err)
			return
		}
		sessionID = session.ID
		if err := appInst.Sessions.UpdateStateless(ctx, sessionID, true, "cron"); err != nil {
			s.logger.Error("failed to update session stateless status", "job", jobName, "error", err)
		}
		// We'll delete the session after the run.
		defer func() {
			if err := appInst.Sessions.Delete(ctx, sessionID); err != nil {
				s.logger.Error("failed to delete session", "job", jobName, "error", err)
			}
		}()
	default:
		s.logger.Error("unknown session mode", "job", jobName, "mode", job.SessionMode)
		return
	}

	// Send the prompt to the agent.
	var attachments []message.Attachment // TODO: handle attachments if needed
	_, err = appInst.AgentCoordinator.Run(ctx, sessionID, job.Prompt, attachments...)
	if err != nil {
		s.logger.Error("failed to run job", "job", jobName, "error", err)
		// Increment failure count.
		s.incrementFailureCount(jobName)
		return
	}

	// Success! Reset failure count.
	s.resetFailureCount(jobName)
	s.logger.Info("job completed successfully", "job", jobName)
}

// getFailureCount returns the failure count for a job.
func (s *Service) getFailureCount(jobName string) int {
	s.failureMu.Lock()
	count := s.failureCounts[jobName]
	s.failureMu.Unlock()
	return count
}

// incrementFailureCount increments the failure count for a job.
func (s *Service) incrementFailureCount(jobName string) {
	s.failureMu.Lock()
	s.failureCounts[jobName]++
	s.failureMu.Unlock()
}

// resetFailureCount resets the failure count for a job.
func (s *Service) resetFailureCount(jobName string) {
	s.failureMu.Lock()
	delete(s.failureCounts, jobName)
	s.failureMu.Unlock()
}

// splitFrontmatter splits a string by the frontmatter delimiter (---).
func splitFrontmatter(s string) []string {
	return strings.SplitN(s, "---", 3)
}

// Job represents a scheduled job.
type Job struct {
	Name             string
	Schedule         string
	Prompt           string
	SessionMode      string // ephemeral, persistent, per_run
	Delivery         []string
	SessionID        string
	AllowConcurrent  bool
	FailureThreshold int
}

// JobFrontMatter represents the frontmatter of a job.md file.
type JobFrontMatter struct {
	Title            string   `yaml:"title"`
	Description      string   `yaml:"description"`
	Schedule         string   `yaml:"schedule"`
	SessionMode      string   `yaml:"session_mode"`
	Delivery         []string `yaml:"delivery"`
	SessionID        string   `yaml:"session_id"`
	AllowConcurrent  bool     `yaml:"allow_concurrent"`
	FailureThreshold int      `yaml:"failure_threshold"`
}
