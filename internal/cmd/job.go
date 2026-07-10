package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/editor"
	"github.com/robfig/cron/v3"
	"github.com/spf13/cobra"
	"github.com/hackafterdark/phosphor/internal/app"
)

// NewJobCommand creates the job command with subcommands.
func NewJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage scheduled jobs",
		Long:  `Create, list, edit, and remove scheduled jobs.`,
	}

	// Add subcommands
	cmd.AddCommand(newJobCreateCommand())
	cmd.AddCommand(newJobListCommand())
	cmd.AddCommand(newJobRemoveCommand())
	cmd.AddCommand(newJobEditCommand())

	return cmd
}

func newJobCreateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "add <name>",
		Short: "Create a new job",
		Long: `Create a new scheduled job by prompting for configuration.
The job will be created in .phosphor/jobs/<name>/job.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			return createJob(name, os.Stdin, os.Stdout)
		},
	}
}

func newJobListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all jobs",
		Long:  `List all scheduled jobs and their status.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listJobs(os.Stdout)
		},
	}
}

func newJobRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a job",
		Long:  `Remove a scheduled job and its configuration files.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			return removeJob(name)
		},
	}
}

func newJobEditCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "edit <name>",
		Short: "Edit a job",
		Long:  `Open a job's configuration file in the editor for modification.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			return editJob(name)
		},
	}
}

func createJob(name string, stdin io.Reader, stdout io.Writer) error {
	// Create the app with nil parameters to use defaults
	app, err := app.New(context.Background(), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create app: %w", err)
	}

	// Validate job name
	if err := validateJobName(name); err != nil {
		return err
	}

	// Create job directory
	jobDir := filepath.Join(app.Store().WorkingDir(), ".phosphor/jobs", name)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return fmt.Errorf("failed to create job directory: %w", err)
	}

	// Create job file
	jobFile := filepath.Join(jobDir, "job.md")
	if _, err := os.Stat(jobFile); !os.IsNotExist(err) {
		return fmt.Errorf("job %s already exists", name)
	}

	// Prompt for job configuration
	title, err := promptInput(stdin, stdout, "Job title", name)
	if err != nil {
		return err
	}

	description, err := promptInput(stdin, stdout, "Job description", "")
	if err != nil {
		return err
	}

	schedule, err := promptInput(stdin, stdout, "Cron schedule (e.g., '0 9 * * *' for daily at 9 AM)", "0 9 * * *")
	if err != nil {
		return err
	}

	// Validate schedule
	if _, err := cron.ParseStandard(schedule); err != nil {
		return fmt.Errorf("invalid cron schedule: %w", err)
	}

	fmt.Fprintln(stdout, "\nSession mode options:")
	fmt.Fprintln(stdout, "  ephemeral - New session for each run (default)")
	fmt.Fprintln(stdout, "  persistent - Reuse same session across runs")
	fmt.Fprintln(stdout, "  per_run - Same as ephemeral")
	sessionMode, err := promptInput(stdin, stdout, "Session mode", "ephemeral")
	if err != nil {
		return err
	}

	// Validate session mode
	validModes := map[string]bool{
		"ephemeral": true,
		"persistent": true,
		"per_run": true,
	}
	if !validModes[sessionMode] {
		return fmt.Errorf("invalid session mode: %s", sessionMode)
	}

	delivery, err := promptInput(stdin, stdout, "Delivery methods (comma-separated, e.g., 'tui')", "tui")
	if err != nil {
		return err
	}

	sessionID, err := promptInput(stdin, stdout, "Session ID (for persistent mode, optional)", "")
	if err != nil {
		return err
	}

	allowConcurrent, err := promptInput(stdin, stdout, "Allow concurrent runs? (true/false)", "false")
	if err != nil {
		return err
	}

	failureThreshold, err := promptInput(stdin, stdout, "Failure threshold before disabling job (0=disabled, default=2)", "2")
	if err != nil {
		return err
	}

	// Write the job file
	content := fmt.Sprintf(`---
title: "%s"
description: "%s"
schedule: "%s"
session_mode: "%s"
delivery: [%s]
session_id: "%s"
allow_concurrent: %s
failure_threshold: %s
---

## Prompt
Enter your job prompt here.
`, title, description, schedule, sessionMode, delivery, sessionID, allowConcurrent, failureThreshold)

	if err := os.WriteFile(jobFile, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write job file: %w", err)
	}

	fmt.Fprintf(stdout, "Job %s created successfully at %s\n", name, jobFile)
	return nil
}

func listJobs(stdout io.Writer) error {
	// Create the app with nil parameters to use defaults
	app, err := app.New(context.Background(), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create app: %w", err)
	}

	// Get the jobs directory
	jobsDir := filepath.Join(app.Store().WorkingDir(), ".phosphor/jobs")

	// Check if directory exists
	if _, err := os.Stat(jobsDir); os.IsNotExist(err) {
		fmt.Fprintln(stdout, "No jobs found.")
		return nil
	}

	// Walk the directory to find job.md files
	files, err := os.ReadDir(jobsDir)
	if err != nil {
		return fmt.Errorf("failed to read jobs directory: %w", err)
	}

	var foundAny bool
	for _, file := range files {
		if !file.IsDir() {
			continue
		}

		jobDir := filepath.Join(jobsDir, file.Name())
		jobFile := filepath.Join(jobDir, "job.md")
		if _, err := os.Stat(jobFile); os.IsNotExist(err) {
			continue
		}

		foundAny = true
		fmt.Fprintf(stdout, "- %s\n", file.Name())

		// Read and display basic info from the job file
		data, err := os.ReadFile(jobFile)
		if err != nil {
			fmt.Fprintf(stdout, "  (error reading job file: %v)\n", err)
			continue
		}

		// Extract title from frontmatter
		lines := strings.Split(string(data), "\n")
		inFrontmatter := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "---" {
				if !inFrontmatter {
					inFrontmatter = true
				} else {
					break
				}
				continue
			}
			if inFrontmatter && strings.HasPrefix(trimmed, "title:") {
				title := strings.TrimSpace(strings.TrimPrefix(trimmed, "title:"))
				title = strings.Trim(title, `"`) // Remove quotes if present
				fmt.Fprintf(stdout, "  Title: %s\n", title)
				break
			}
		}
	}

	if !foundAny {
		fmt.Fprintln(stdout, "No jobs found.")
	}

	return nil
}

func removeJob(name string) error {
	// Create the app with nil parameters to use defaults
	app, err := app.New(context.Background(), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create app: %w", err)
	}

	// Validate job name
	if err := validateJobName(name); err != nil {
		return err
	}

	// Construct job path
	jobDir := filepath.Join(app.Store().WorkingDir(), ".phosphor/jobs", name)
	if _, err := os.Stat(jobDir); os.IsNotExist(err) {
		return fmt.Errorf("job %s not found", name)
	}

	// Remove the job directory and all its contents
	if err := os.RemoveAll(jobDir); err != nil {
		return fmt.Errorf("failed to remove job: %w", err)
	}

	fmt.Printf("Job %s removed successfully\n", name)
	return nil
}

func editJob(name string) error {
	// Create the app with nil parameters to use defaults
	app, err := app.New(context.Background(), nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create app: %w", err)
	}

	// Validate job name
	if err := validateJobName(name); err != nil {
		return err
	}

	// Construct job path
	jobFile := filepath.Join(app.Store().WorkingDir(), ".phosphor/jobs", name, "job.md")
	if _, err := os.Stat(jobFile); os.IsNotExist(err) {
		return fmt.Errorf("job %s not found", name)
	}

	// Open the file in the editor
	cmd, err := editor.Command("phosphor", jobFile)
	if err != nil {
		return fmt.Errorf("failed to create editor command: %w", err)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor exited with error: %w", err)
	}

	return nil
}

func validateJobName(name string) error {
	if name == "" {
		return errors.New("job name cannot be empty")
	}
	// Additional validation could be added here (e.g., no special characters)
	return nil
}

func promptInput(stdin io.Reader, stdout io.Writer, label, defaultValue string) (string, error) {
	fmt.Fprintf(stdout, "%s [%s]: ", label, defaultValue)
	var input string
	_, err := fmt.Fscanln(stdin, &input)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if input == "" {
		return defaultValue, nil
	}
	return input, nil
}