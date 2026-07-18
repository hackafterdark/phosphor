package embedded

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ModelEntry describes a known embedded model available for download.
type ModelEntry struct {
	Name        string   // Human-readable name (e.g. "Qwen3.5-0.8B")
	RepoID      string   // HuggingFace repo ID (e.g. "Qwen/Qwen3.5-0.8B")
	Filename    string   // Filename to download (e.g. "Qwen3.5-0.8B.Q4_K_M.gguf")
	Description string   // Short description of the model
	Params      string   // Parameter count (e.g. "0.8B")
	Quants      []string // Available quantizations
}

// Registry provides a catalog of available embedded models.
type Registry struct {
	entries []ModelEntry
}

// DefaultRegistry returns a Registry pre-populated with known models.
func DefaultRegistry() *Registry {
	return &Registry{
		entries: []ModelEntry{
			{
				Name:        "Qwen3.5-0.8B",
				RepoID:      "unsloth/Qwen3.5-0.8B-GGUF",
				Filename:    "Qwen3.5-0.8B-Q4_K_M.gguf",
				Description: "Lightweight Qwen3.5 model, ideal for local compaction summaries",
				Params:      "0.8B",
				Quants:      []string{"Q4_K_M", "Q4_K_S", "Q5_K_M", "Q8_0"},
			},
			{
				Name:        "Phi-3-mini",
				RepoID:      "bartowski/Phi-3-mini-4k-instruct-GGUF",
				Filename:    "Phi-3-mini-4k-instruct-Q4_K_M.gguf",
				Description: "Microsoft Phi-3-mini, compact and capable",
				Params:      "1.3B",
				Quants:      []string{"Q4_K_M", "Q4_K_S"},
			},
			{
				Name:        "Gemma-2-2B",
				RepoID:      "bartowski/gemma-2-2b-it-GGUF",
				Filename:    "gemma-2-2b-it-Q4_K_M.gguf",
				Description: "Google Gemma-2 2B instruct model",
				Params:      "2B",
				Quants:      []string{"Q4_K_M", "Q4_K_S", "Q8_0"},
			},
		},
	}
}

// List returns all available model entries.
func (r *Registry) List() []ModelEntry {
	result := make([]ModelEntry, len(r.entries))
	copy(result, r.entries)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Get returns the model entry by name or repo ID.
func (r *Registry) Get(name string) (*ModelEntry, bool) {
	for i := range r.entries {
		if strings.EqualFold(r.entries[i].Name, name) || strings.EqualFold(r.entries[i].RepoID, name) {
			entry := r.entries[i]
			return &entry, true
		}
	}
	return nil, false
}

// Download downloads a model by name to the local data directory.
func (r *Registry) Download(downloader *ModelDownloader, name string) (string, error) {
	entry, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown model: %s. Available models: %s", name, modelNames(r))
	}

	ctx := context.TODO()
	path, err := downloader.DownloadModel(ctx, entry.RepoID, entry.Filename)
	return path, err
}

func modelNames(r *Registry) string {
	names := make([]string, len(r.entries))
	for i, e := range r.entries {
		names[i] = e.Name
	}
	return strings.Join(names, ", ")
}
