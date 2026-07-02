package prompt_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hackafterdark/phosphor/internal/agent/prompt"
	"github.com/hackafterdark/phosphor/internal/config"
	"github.com/hackafterdark/phosphor/internal/home"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptReflection(t *testing.T) {
	tmpl := `{{- if and .AgentConfig .AgentConfig.EnableReflection}}
<reflection_instructions>
- BEFORE yielding the final answer, perform a mandatory "Self-Critique":
</reflection_instructions>
{{- end}}`

	p, err := prompt.NewPrompt("test_prompt", tmpl)
	require.NoError(t, err)

	cfg := &config.Config{
		Options: &config.Options{
			Agent: &config.AgentConfig{
				EnableReflection: true,
			},
		},
	}
	store := config.NewTestStore(cfg)

	res, err := p.Build(context.Background(), "provider", "model", store)
	require.NoError(t, err)
	assert.Contains(t, res, "<reflection_instructions>")

	// Now test with reflection disabled
	cfg2 := &config.Config{
		Options: &config.Options{
			Agent: &config.AgentConfig{
				EnableReflection: false,
			},
		},
	}
	store2 := config.NewTestStore(cfg2)

	res2, err := p.Build(context.Background(), "provider", "model", store2)
	require.NoError(t, err)
	assert.NotContains(t, res2, "<reflection_instructions>")
}

func TestPromptProfileResolution(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Mock global home path
	globalHomeDir := filepath.Join(tmpDir, "global_home")
	err := os.MkdirAll(globalHomeDir, 0o755)
	require.NoError(t, err)
	home.DirOverride = globalHomeDir
	defer func() {
		home.DirOverride = ""
	}()

	// 2. Set up workspaces and paths
	workspaceDir := filepath.Join(tmpDir, "workspace")
	err = os.MkdirAll(workspaceDir, 0o755)
	require.NoError(t, err)

	// Create workspace custom rules
	workspaceProfileDir := filepath.Join(workspaceDir, ".phosphor", "profiles", "custom")
	err = os.MkdirAll(workspaceProfileDir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(workspaceProfileDir, "rules.md.tpl"), []byte("WORKSPACE CUSTOM RULES"), 0o644)
	require.NoError(t, err)

	// Create global custom style
	globalProfileDir := filepath.Join(globalHomeDir, ".phosphor", "profiles", "custom")
	err = os.MkdirAll(globalProfileDir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(globalProfileDir, "style.md.tpl"), []byte("GLOBAL CUSTOM STYLE"), 0o644)
	require.NoError(t, err)

	// Create prompt
	tmpl := `Rules: {{.CriticalRules}} | Style: {{.CommunicationStyle}} | Workflow: {{.Workflow}} | DecisionMaking: {{.DecisionMaking}}`
	p, err := prompt.NewPrompt("test_profile_prompt", tmpl, prompt.WithWorkingDir(workspaceDir))
	require.NoError(t, err)

	cfg := &config.Config{
		Options: &config.Options{
			Agent: &config.AgentConfig{
				ActiveProfile: "custom",
			},
		},
	}
	store := config.NewTestStore(cfg)

	// Build prompt
	res, err := p.Build(context.Background(), "provider", "model", store)
	require.NoError(t, err)

	// Verify resolution hierarchy:
	// - Workspace rules override embedded rules.
	assert.Contains(t, res, "Rules: WORKSPACE CUSTOM RULES")
	// - Global style overrides embedded style because workspace style is absent.
	assert.Contains(t, res, "Style: GLOBAL CUSTOM STYLE")
	// - Workflow is resolved from embedded default because both workspace and global workflow are absent.
	assert.Contains(t, res, "Workflow: For every task, follow this sequence internally")
}
