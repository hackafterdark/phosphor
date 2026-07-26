package cmd

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/hackafterdark/phosphor/internal/workspaceindex"
	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index [directory]",
	Short: "Build the workspace FTS5 index",
	Long:  "Scan a workspace directory and index all code symbols and document text into SQLite FTS5 tables.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runIndex,
}

func runIndex(cmd *cobra.Command, args []string) error {
	// Determine target directory.
	targetDir := "."
	if len(args) > 0 {
		targetDir = args[0]
	}

	absPath, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	store, err := workspaceindex.NewStore(absPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer store.Close()

	indexer := workspaceindex.NewIndexer(store, 1048576)

	ctx := cmd.Context()
	if err := indexer.IndexWorkspace(ctx, absPath, []string{}); err != nil {
		return fmt.Errorf("index workspace: %w", err)
	}

	slog.Info("Workspace index built successfully", "directory", absPath)

	return nil
}
