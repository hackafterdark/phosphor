package agent

import (
	"context"
	_ "embed"

	"github.com/hackafterdark/phosphor/internal/agent/prompt"
	"github.com/hackafterdark/phosphor/internal/config"
)

//go:embed templates/system.md.tpl
var systemPromptTmpl []byte

//go:embed templates/task.md.tpl
var taskPromptTmpl []byte

//go:embed templates/initialize.md.tpl
var initializePromptTmpl []byte

// The main "system" prompt.
func systemPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	opts = append(opts, prompt.WithStructuralSearchAvailable(structuralSearchAvailable))
	systemPrompt, err := prompt.NewPrompt("system", string(systemPromptTmpl), opts...)
	if err != nil {
		return nil, err
	}
	return systemPrompt, nil
}

// The main "system" prompt has access to a tool named `agent` (defined in `agent_tool.go`). When the model decides to run a parallel
// search/recon task (using `glob`, `grep`, `ls`, or `view` tools) without cluttering its main execution path, it calls the `agent`
// tool, which spawns a sub-agent. That's sub-agent uses this prompt.
func taskPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	systemPrompt, err := prompt.NewPrompt("task", string(taskPromptTmpl), opts...)
	if err != nil {
		return nil, err
	}
	return systemPrompt, nil
}

// Part of "onboarding," when firsting opening a workspace, this scans the directory, identifies the project type/langauges,
// finds the build/test/lint commands, and writes/updates the workspace rules file (e.g. AGENTS.md, CLAUDE.md, or .cursorrules).
func InitializePrompt(cfg *config.ConfigStore) (string, error) {
	systemPrompt, err := prompt.NewPrompt("initialize", string(initializePromptTmpl))
	if err != nil {
		return "", err
	}
	return systemPrompt.Build(context.Background(), "", "", cfg)
}
