//go:build cgo

package parser

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// QueryCapability defines a structural search capability.
type QueryCapability struct {
	ID            string `yaml:"id" json:"id"`
	Description   string `yaml:"description" json:"description"`
	Language      string `yaml:"language" json:"language"`
	Query         string `yaml:"query" json:"query"`
	Guidance      string `yaml:"guidance,omitempty" json:"guidance,omitempty"`
	Preconditions string `yaml:"preconditions,omitempty" json:"preconditions,omitempty"`
}

// QueryRegistry manages loading, overriding, and watching queries.
type QueryRegistry struct {
	mu           sync.RWMutex
	capabilities []QueryCapability
	watcher      *fsnotify.Watcher
	workspaceDir string
	onReload     func()
}

// Global registry instance.
var Registry = NewQueryRegistry()

// NewQueryRegistry creates a registry and populates it with defaults.
func NewQueryRegistry() *QueryRegistry {
	r := &QueryRegistry{
		capabilities: []QueryCapability{},
	}
	r.loadDefaults()
	return r
}

// Default descriptions for built-in template IDs.
var defaultDescriptions = map[string]string{
	"find_functions":     "Discover all functions and methods in the current file.",
	"find_structs":       "Find structures, classes, and type definitions in the current file.",
	"find_variables":     "Find variable declarations and assignments in the current file.",
	"find_interfaces":    "Find interface and trait declarations in the current file.",
	"find_calls":         "Find function and method calls in the current file.",
	"find_imports":       "Find import declarations and included libraries in the current file.",
	"find_comments":      "Find comments in the current file.",
	"find_select_tables": "Find table references in SELECT statements.",
	"find_joins":         "Find JOIN clauses and joined tables.",
	"find_inserts":       "Find INSERT statements.",
	"find_deletes":       "Find DELETE statements.",
	"find_select_all":    "Find SELECT * wildcard statements.",
}

// loadDefaults populates the registry with built-in templates.
func (r *QueryRegistry) loadDefaults() {
	var caps []QueryCapability
	for lang, langTemplates := range Templates {
		for id, query := range langTemplates {
			desc := defaultDescriptions[id]
			if desc == "" {
				desc = fmt.Sprintf("Find %s in %s code.", strings.ReplaceAll(id, "_", " "), lang)
			}
			caps = append(caps, QueryCapability{
				ID:          id,
				Description: desc,
				Language:    lang,
				Query:       query,
			})
		}
	}
	r.capabilities = caps
}

// Reload loads built-in queries and merges them with workspace overrides.
func (r *QueryRegistry) Reload(workspaceDir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.workspaceDir = workspaceDir
	r.loadDefaults()

	queriesDir := filepath.Join(workspaceDir, ".phosphor", "queries")
	if err := os.MkdirAll(queriesDir, 0o755); err != nil {
		return fmt.Errorf("failed to create queries directory: %w", err)
	}

	err := filepath.Walk(queriesDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read query file %s: %w", path, err)
		}

		// Try unmarshaling as a list first, then a single capability.
		var slice []QueryCapability
		if err := yaml.Unmarshal(data, &slice); err == nil && len(slice) > 0 && slice[0].ID != "" {
			for _, cap := range slice {
				if cap.ID != "" {
					r.mergeCapability(cap)
				}
			}
			return nil
		}

		var single QueryCapability
		if err := yaml.Unmarshal(data, &single); err == nil && single.ID != "" {
			r.mergeCapability(single)
			return nil
		}

		return fmt.Errorf("invalid query YAML format in %s", path)
	})

	if err != nil {
		return err
	}

	return nil
}

// mergeCapability overrides an existing capability with matching ID and Language,
// or appends the new capability to the registry.
func (r *QueryRegistry) mergeCapability(c QueryCapability) {
	for i, existing := range r.capabilities {
		if existing.ID == c.ID && existing.Language == c.Language {
			r.capabilities[i] = c
			return
		}
	}
	r.capabilities = append(r.capabilities, c)
}

// GetTemplate returns the query S-expression for a given language and ID.
func (r *QueryRegistry) GetTemplate(lang, name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, cap := range r.capabilities {
		if cap.Language == lang && cap.ID == name {
			return cap.Query, true
		}
	}
	return "", false
}

// TemplateNames returns sorted IDs of all queries for a given language.
func (r *QueryRegistry) TemplateNames(lang string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var names []string
	seen := make(map[string]bool)
	for _, cap := range r.capabilities {
		if cap.Language == lang {
			if !seen[cap.ID] {
				seen[cap.ID] = true
				names = append(names, cap.ID)
			}
		}
	}
	return names
}

// GetCapabilities returns a copy of all registered query capabilities.
func (r *QueryRegistry) GetCapabilities() []QueryCapability {
	r.mu.RLock()
	defer r.mu.RUnlock()

	caps := make([]QueryCapability, len(r.capabilities))
	copy(caps, r.capabilities)
	return caps
}

// GetCapability returns a capability by language and ID.
func (r *QueryRegistry) GetCapability(lang, name string) (QueryCapability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, cap := range r.capabilities {
		if cap.Language == lang && cap.ID == name {
			return cap, true
		}
	}
	return QueryCapability{}, false
}

// StartWatcher configures a background fsnotify watcher on queries directory.
func (r *QueryRegistry) StartWatcher(workspaceDir string, onReload func()) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.watcher != nil {
		r.watcher.Close()
	}

	r.workspaceDir = workspaceDir
	r.onReload = onReload

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	r.watcher = watcher

	queriesDir := filepath.Join(workspaceDir, ".phosphor", "queries")
	if err := os.MkdirAll(queriesDir, 0o755); err != nil {
		watcher.Close()
		return err
	}

	if err := watcher.Add(queriesDir); err != nil {
		watcher.Close()
		return err
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					slog.Info("Detected change in queries directory, reloading", "path", event.Name)
					if err := r.Reload(r.workspaceDir); err == nil {
						if r.onReload != nil {
							r.onReload()
						}
					} else {
						slog.Error("Failed to reload queries on file change", "error", err)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("Query file watcher error", "error", err)
			}
		}
	}()

	return nil
}

// Close stops the fsnotify watcher.
func (r *QueryRegistry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.watcher != nil {
		r.watcher.Close()
		r.watcher = nil
	}
}

// ReloadQueries is a package-level helper to trigger reload on Global Registry.
func ReloadQueries(workspaceDir string) error {
	return Registry.Reload(workspaceDir)
}

// StartWatcher is a package-level helper to watch queries directory.
func StartWatcher(workspaceDir string, onReload func()) error {
	return Registry.StartWatcher(workspaceDir, onReload)
}

// CloseWatcher is a package-level helper to close queries watcher.
func CloseWatcher() {
	Registry.Close()
}

// GetCapabilities returns a copy of all loaded capabilities from Global Registry.
func GetCapabilities() []QueryCapability {
	return Registry.GetCapabilities()
}

// GetCapability returns a capability by language and ID from Global Registry.
func GetCapability(lang, name string) (QueryCapability, bool) {
	return Registry.GetCapability(lang, name)
}
