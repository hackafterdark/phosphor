package cmd

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"charm.land/lipgloss/v2/tree"
	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/embedded"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List all available models from known providers",
	Long:  `List all available models from known providers. Shows provider name and model IDs. Unconfigured providers are marked with (not configured).`,
	Example: `# List all available models
phosphor models

# Search models
phosphor models gpt5`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := ResolveCwd(cmd)
		if err != nil {
			return err
		}

		dataDir, _ := cmd.Flags().GetString("data-dir")
		debug, _ := cmd.Flags().GetBool("debug")

		cfg, err := config.Init(cwd, dataDir, debug)
		if err != nil {
			return err
		}

		term := strings.ToLower(strings.Join(args, " "))

		type providerEntry struct {
			name       string
			models     []string
			configured bool
		}

		entries := make(map[string]*providerEntry)

		// Add configured providers first.
		for providerID, provider := range cfg.Config().Providers.Seq2() {
			if provider.Disable {
				continue
			}
			entry := &providerEntry{
				name:       provider.Name,
				configured: true,
			}
			for _, model := range provider.Models {
				if term != "" {
					matched := false
					for _, s := range []string{provider.ID, provider.Name, model.ID, model.Name} {
						if strings.Contains(strings.ToLower(s), term) {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
				}
				entry.models = append(entry.models, model.ID)
			}
			if len(entry.models) > 0 {
				slices.Sort(entry.models)
				entries[providerID] = entry
			}
		}

		// Add known but unconfigured providers from catwalk.
		for _, kp := range cfg.KnownProviders() {
			providerID := string(kp.ID)
			if _, exists := entries[providerID]; exists {
				continue
			}
			entry := &providerEntry{
				name:       kp.Name,
				configured: false,
			}
			for _, model := range kp.Models {
				if term != "" {
					matched := false
					for _, s := range []string{providerID, kp.Name, model.ID, model.Name} {
						if strings.Contains(strings.ToLower(s), term) {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
				}
				entry.models = append(entry.models, model.ID)
			}
			if len(entry.models) > 0 {
				slices.Sort(entry.models)
				entries[providerID] = entry
			}
		}

		var providerIDs []string
		for id := range entries {
			providerIDs = append(providerIDs, id)
		}
		sort.Strings(providerIDs)

		if len(providerIDs) == 0 && len(args) == 0 {
			return fmt.Errorf("no providers found")
		}
		if len(providerIDs) == 0 {
			return fmt.Errorf("no providers found matching %q", term)
		}

		if !isatty.IsTerminal(os.Stdout.Fd()) {
			for _, providerID := range providerIDs {
				entry := entries[providerID]
				for _, modelID := range entry.models {
					fmt.Println(providerID + "/" + modelID)
				}
			}
			return nil
		}

		t := tree.New()
		for _, providerID := range providerIDs {
			entry := entries[providerID]
			label := providerID
			if !entry.configured {
				label += " (not configured)"
			}
			providerNode := tree.Root(label)
			for _, modelID := range entry.models {
				providerNode.Child(modelID)
			}
			t.Child(providerNode)
		}

		cmd.Println(t)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(modelsCmd)
	modelsCmd.AddCommand(modelsEmbeddedCmd)
}

// modelsEmbeddedCmd handles the 'phosphor models embedded' subcommand.
var modelsEmbeddedCmd = &cobra.Command{
	Use:   "embedded [list|install|info]",
	Short: "Manage embedded (local) models",
	Long:  `Manage embedded models that run locally via dlgo. Use 'list' to see available models, 'install <name>' to download, 'info' to show details.`,
	Args:  cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		registry := embedded.DefaultRegistry()
		downloader := embedded.NewModelDownloader("")

		action := "list"
		modelName := ""
		if len(args) > 0 {
			action = args[0]
		}
		if len(args) > 1 {
			modelName = args[1]
		}

		switch action {
		case "list":
			for _, entry := range registry.List() {
				fmt.Printf("- %s (%s): %s\n", entry.Name, entry.Params, entry.Description)
			}
			return nil

		case "install":
			if modelName == "" {
				return fmt.Errorf("model name required. Available: %s", modelNames(registry))
			}
			path, err := registry.Download(downloader, modelName)
			if err != nil {
				return err
			}

			cwd, err := ResolveCwd(cmd)
			if err != nil {
				return err
			}
			dataDir, _ := cmd.Flags().GetString("data-dir")
			debug, _ := cmd.Flags().GetBool("debug")

			cfg, err := config.Init(cwd, dataDir, debug)
			if err != nil {
				return err
			}

			entry, ok := registry.Get(modelName)
			if !ok {
				return fmt.Errorf("unknown model: %s", modelName)
			}

			scope := config.ScopeGlobal
			if _, err := os.Stat(cfg.Config().Options.DataDirectory); err == nil {
				scope = config.ScopeWorkspace
			}

			fields := map[string]any{
				"embedded_models.inference.enabled":    true,
				"embedded_models.inference.model_path": path,
				"embedded_models.inference.model_repo": entry.RepoID,
			}
			if err := cfg.SetConfigFields(scope, fields); err != nil {
				return fmt.Errorf("failed to save configuration: %w", err)
			}
			fmt.Printf("Model configured successfully at %s scope!\n", scope.String())
			return nil

		case "info":
			for _, entry := range registry.List() {
				fmt.Printf("Model: %s\n", entry.Name)
				fmt.Printf("  Repo: %s\n", entry.RepoID)
				fmt.Printf("  File: %s\n", entry.Filename)
				fmt.Printf("  Params: %s\n", entry.Params)
				fmt.Printf("  Quants: %s\n", strings.Join(entry.Quants, ", "))
				fmt.Printf("  Description: %s\n\n", entry.Description)
			}
			return nil

		default:
			return fmt.Errorf("unknown action %q. Use list, install, or info", action)
		}
	},
}

func modelNames(r *embedded.Registry) string {
	names := make([]string, len(r.List()))
	for i, e := range r.List() {
		names[i] = e.Name
	}
	return strings.Join(names, ", ")
}

func init() {
	rootCmd.AddCommand(modelsCmd)
	modelsCmd.AddCommand(modelsEmbeddedCmd)
}
