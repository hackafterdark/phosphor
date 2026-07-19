package embedded

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ModelEntry describes a known inference model (GGUF) available for download.
type ModelEntry struct {
	Name        string   // Human-readable name (e.g. "Qwen3.5-0.8B")
	RepoID      string   // HuggingFace repo ID (e.g. "Qwen/Qwen3.5-0.8B")
	Filename    string   // Filename to download (e.g. "Qwen3.5-0.8B.Q4_K_M.gguf")
	Description string   // Short description of the model
	Params      string   // Parameter count (e.g. "0.8B")
	Quants      []string // Available quantizations
}

// EmbeddingModelEntry describes a known embedding model (Safetensors directory) available for download.
type EmbeddingModelEntry struct {
	Name        string   // Human-readable name (e.g. "BGE-small-en-v1.5")
	RepoID      string   // HuggingFace repo ID (e.g. "BAAI/bge-small-en-v1.5")
	Description string   // Short description of the model
	Dimensions  int      // Embedding dimensionality
	Files       []string // Filenames that make up the model directory
}

// Registry provides a catalog of available embedded models.
type Registry struct {
	entries          []ModelEntry
	embeddingEntries []EmbeddingModelEntry
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
		embeddingEntries: []EmbeddingModelEntry{
			{
				Name:        "BGE-small-en-v1.5",
				RepoID:      "BAAI/bge-small-en-v1.5",
				Description: "BAAI BGE small English embedding model, 384 dimensions",
				Dimensions:  384,
				Files:       []string{"config.json", "tokenizer.json", "tokenizer_config.json", "special_tokens_map.json", "model.safetensors"},
			},
		},
	}
}

// List returns all available inference model entries.
func (r *Registry) List() []ModelEntry {
	result := make([]ModelEntry, len(r.entries))
	copy(result, r.entries)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ListEmbeddings returns all available embedding model entries.
func (r *Registry) ListEmbeddings() []EmbeddingModelEntry {
	result := make([]EmbeddingModelEntry, len(r.embeddingEntries))
	copy(result, r.embeddingEntries)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Get returns the inference model entry by name or repo ID.
func (r *Registry) Get(name string) (*ModelEntry, bool) {
	for i := range r.entries {
		if strings.EqualFold(r.entries[i].Name, name) || strings.EqualFold(r.entries[i].RepoID, name) {
			entry := r.entries[i]
			return &entry, true
		}
	}
	return nil, false
}

// GetEmbedding returns the embedding model entry by name or repo ID.
func (r *Registry) GetEmbedding(name string) (*EmbeddingModelEntry, bool) {
	for i := range r.embeddingEntries {
		if strings.EqualFold(r.embeddingEntries[i].Name, name) || strings.EqualFold(r.embeddingEntries[i].RepoID, name) {
			entry := r.embeddingEntries[i]
			return &entry, true
		}
	}
	return nil, false
}

// Download downloads an inference model (single file) by name or repo ID to the local data directory.
func (r *Registry) Download(downloader *ModelDownloader, name string) (string, error) {
	entry, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown model: %s. Available models: %s", name, modelNames(r))
	}

	ctx := context.TODO()
	path, err := downloader.DownloadModel(ctx, entry.RepoID, entry.Filename)
	return path, err
}

// DownloadEmbedding downloads an embedding model (directory) by name or repo ID.
func (r *Registry) DownloadEmbedding(downloader *ModelDownloader, name string) (string, error) {
	entry, ok := r.GetEmbedding(name)
	if !ok {
		return "", fmt.Errorf("unknown embedding model: %s. Available embedding models: %s", name, embeddingModelNames(r))
	}

	ctx := context.TODO()
	path, err := downloader.DownloadModelDirectory(ctx, entry.RepoID, entry.Files)
	return path, err
}

func modelNames(r *Registry) string {
	names := make([]string, len(r.entries))
	for i, e := range r.entries {
		names[i] = e.Name
	}
	return strings.Join(names, ", ")
}

func embeddingModelNames(r *Registry) string {
	names := make([]string, len(r.embeddingEntries))
	for i, e := range r.embeddingEntries {
		names[i] = e.Name
	}
	return strings.Join(names, ", ")
}