package config

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/hackafterdark/phosphor/internal/home"
)

// Interactive workspace trust gate (malicious-repository defense).
//
// A freshly cloned repository can drop MCP servers, a workspace config, or
// scheduled project tasks that Phosphor would otherwise start and expose to the
// model as tools. Those definitions run with the user's privileges, so an
// untrusted checkout must not be allowed to register them silently.
//
// This gate keeps a persistent allow-list of workspace roots the user has
// explicitly trusted (a JSON file under the user's global config directory). On
// startup, when the current workspace declares repo-local tooling and is not yet
// trusted, the operator is asked once to trust it. Declining — or running
// headless without an explicit --trust — makes Phosphor ignore the repo-local
// definitions and run with only the built-in native tools.

const trustedWorkspacesFile = "trusted_workspaces.json"

// repoLocalToolingFiles are the repository-committed paths that, when present,
// mean the workspace brings its own executable surface: MCP server definitions,
// a workspace config that can define MCP/hooks, or scheduled project tasks.
var repoLocalToolingFiles = []string{
	".mcp.json",
	filepath.Join(".phosphor", "mcp.json"),
	filepath.Join(".phosphor", "config.json"),
	filepath.Join(".phosphor", "settings.json"),
}

// repoLocalToolingDirs are directories whose presence means the repo ships tasks
// that the agent or scheduler could run.
var repoLocalToolingDirs = []string{
	filepath.Join(".phosphor", "jobs"),
}

// repoLocalConfigFiles are the repository-committed configuration files that
// lookupConfigs and Load merge into the effective configuration. Because they can
// define MCP servers ("mcp") and hook commands ("hooks"), a repository that
// declares them only through one of these files must still pass the trust gate.
// Presence alone does not imply an executable surface, so they count only when a
// non-empty executable section is present.
var repoLocalConfigFiles = []string{
	appName + ".json",
	"." + appName + ".json",
	filepath.Join(".phosphor", appName+".json"),
}

// TrustPrompt is asked once for an untrusted workspace that declares repo-local
// tooling. It receives the human-readable working directory and the rendered
// question and returns whether the user trusts the workspace. The TUI installs a
// real prompt; the default denies (fail-closed) so a headless run never trusts a
// repository on the user's behalf.
type TrustPrompt func(workingDir, question string) bool

var (
	trustMu         sync.Mutex
	trustPrompt     TrustPrompt = func(string, string) bool { return false }
	customPromptSet bool
	trustInteractve bool
	trustRequested  bool
	// trustRequestedFor is the normalized workspace key the --trust consent was
	// granted for. --trust trusts a single workspace, so the consent is bound to
	// this path and must not leak to other workspaces that share the process (a
	// multi-workspace server daemon). An empty value means the consent is
	// unbound (legacy/global) and applies to whatever workspace is consulted.
	trustRequestedFor string
	// trustedWorkspacesPathOverride lets tests point the store at a temp file.
	trustedWorkspacesPathOverride string

	// trustCache memoises the per-workspace allow decision so buildTools and the MCP
	// initializer share one answer and the operator is prompted at most once.
	trustCacheMu sync.Mutex
	trustCache   = make(map[string]bool)
)

// SetWorkspaceTrustPrompt installs the interactive prompt used to ask the operator
// whether to trust an untrusted workspace. A nil prompt restores the fail-closed
// default (deny).
func SetWorkspaceTrustPrompt(fn TrustPrompt) {
	trustMu.Lock()
	defer trustMu.Unlock()
	if fn == nil {
		trustPrompt = func(string, string) bool { return false }
		customPromptSet = false
		return
	}
	trustPrompt = fn
	customPromptSet = true
}

// SetWorkspaceTrustInteractive marks whether Phosphor is running with an interactive
// terminal the trust prompt can be shown in. Only an interactive session may be
// prompted; headless runs fall back to the deny default unless --trust was passed.
func SetWorkspaceTrustInteractive(interactive bool) {
	trustMu.Lock()
	defer trustMu.Unlock()
	trustInteractve = interactive
}

// SetWorkspaceTrustRequested records that the operator explicitly passed --trust,
// which trusts the given workspace without a prompt (including headless). The
// consent is bound to workingDir so that a --trust granted for one workspace can
// not silently trust a different one that happens to be decided later in the same
// process; only the workspace the operator named is trusted automatically. An
// empty workingDir records an unbound (global) consent for callers that have no
// specific path at set time.
func SetWorkspaceTrustRequested(workingDir string, requested bool) {
	key := ""
	if requested && strings.TrimSpace(workingDir) != "" {
		if normalized, ok := normalizeWorkspacePath(workingDir); ok {
			key = normalized
		}
	}
	trustMu.Lock()
	defer trustMu.Unlock()
	trustRequested = requested
	trustRequestedFor = key
}

// trustGateState reads the mutable gate settings under the lock. requestedFor is
// the normalized workspace key the current --trust consent is bound to, or "" when
// the consent is unbound.
func trustGateState() (prompt TrustPrompt, interactive, requested, customPrompt bool, requestedFor string) {
	trustMu.Lock()
	defer trustMu.Unlock()
	return trustPrompt, trustInteractve, trustRequested, customPromptSet, trustRequestedFor
}

// stdinTrustPrompt is the fallback used when an interactive session has not
// installed a richer prompt: it renders the question and reads a single y/N line
// from the terminal, defaulting to No.
func stdinTrustPrompt(workingDir, question string) bool {
	_, _ = os.Stdout.WriteString(question + " ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// RepoDefinesCustomTooling reports whether the working directory contains any
// repository-committed file or directory that would register an executable surface
// (repo MCP servers, a workspace tool config, or scheduled project tasks).
func RepoDefinesCustomTooling(workingDir string) bool {
	if workingDir == "" {
		return false
	}
	for _, rel := range repoLocalToolingFiles {
		if fileExists(filepath.Join(workingDir, filepath.FromSlash(rel))) {
			return true
		}
	}
	for _, rel := range repoLocalToolingDirs {
		if dirNonEmpty(filepath.Join(workingDir, filepath.FromSlash(rel))) {
			return true
		}
	}
	for _, rel := range repoLocalConfigFiles {
		if declaresExecutableConfig(filepath.Join(workingDir, filepath.FromSlash(rel))) {
			return true
		}
	}
	return false
}

// declaresExecutableConfig reports whether a repository configuration file defines
// a non-empty "mcp" or "hooks" section, i.e. an executable surface that must pass
// the trust gate. A missing or empty file registers nothing; a non-empty file
// that cannot be parsed is treated as declaring tooling so the gate fails closed.
func declaresExecutableConfig(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return false
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(data, &sections); err != nil {
		return true
	}
	for _, key := range []string{"mcp", "hooks"} {
		raw, ok := sections[key]
		if !ok {
			continue
		}
		switch strings.TrimSpace(string(raw)) {
		case "", "null", "{}", "[]":
		default:
			return true
		}
	}
	return false
}

// WorkspaceToolingAllowed is the single decision the agent and MCP wiring consult
// before registering repo-defined tools. It returns true when the workspace may use
// its repo-local tooling (trusted, explicitly requested, or it declares none), and
// false when those definitions must be ignored and only native tools run.
//
// The decision is computed once per workspace and cached: buildTools and the MCP
// initializer both consult it, but the operator is prompted at most once.
func WorkspaceToolingAllowed(workingDir string) bool {
	// Consult the repository surface before the cache: the executable files can
	// appear mid-session (config reload, agent writes, git operations), and a
	// stale allow decision from a moment when nothing was declared must not
	// silently bless them without operator consent. Only real decisions (made
	// while tooling was present) are cached, so the cache never stores the
	// "nothing to protect against" answer.
	if !RepoDefinesCustomTooling(workingDir) {
		return true
	}

	key, ok := normalizeWorkspacePath(workingDir)
	if !ok {
		// Could not resolve the path: fail closed and treat it as needing trust.
		return decideWorkspaceTooling(workingDir)
	}

	// Hold the cache lock across the whole decision so the (one-time) prompt cannot
	// fire twice when the MCP initializer and the agent both consult the gate on the
	// same workspace concurrently.
	trustCacheMu.Lock()
	defer trustCacheMu.Unlock()
	if cached, seen := trustCache[key]; seen {
		return cached
	}

	decision := decideWorkspaceTooling(workingDir)
	trustCache[key] = decision
	return decision
}

// decideWorkspaceTooling performs the uncached trust decision, prompting when an
// interactive session needs the operator's consent.
func decideWorkspaceTooling(workingDir string) bool {
	if !RepoDefinesCustomTooling(workingDir) {
		// Nothing repo-local to protect against.
		return true
	}
	if IsWorkspaceTrusted(workingDir) {
		return true
	}

	key, _ := normalizeWorkspacePath(workingDir)
	prompt, interactive, requested, customPrompt, requestedFor := trustGateState()
	// --trust only consents the single workspace the operator named. An unbound
	// consent (requestedFor == "") still applies everywhere for back-compat, but a
	// consent bound to a different path must not bless this one: in a shared,
	// multi-workspace server daemon a sticky global --trust would otherwise
	// auto-trust (and persist) every other workspace opened through it.
	if requested && (requestedFor == "" || requestedFor == key) {
		if err := TrustWorkspace(workingDir); err != nil {
			slog.Warn("Failed to persist trusted workspace", "path", workingDir, "error", err)
		}
		return true
	}

	question := "Untrusted workspace: This repository contains custom tools/MCP configuration. Do you trust this workspace? [y/N]"
	if interactive {
		prompter := prompt
		if !customPrompt {
			prompter = stdinTrustPrompt
		}
		if prompter(workingDir, question) {
			if err := TrustWorkspace(workingDir); err != nil {
				slog.Warn("Failed to persist trusted workspace", "path", workingDir, "error", err)
			}
			slog.Info("Workspace trusted by user", "path", workingDir)
			return true
		}
		slog.Warn("Workspace tooling disabled: user declined to trust an untrusted workspace", "path", workingDir)
		return false
	}

	slog.Warn("Ignoring repo-local tooling: untrusted workspace in non-interactive mode (pass --trust to allow)", "path", workingDir)
	return false
}

// WorkspaceTrustStore persists the set of trusted workspace roots as JSON under the
// user's global config directory. It is safe for concurrent use.
type WorkspaceTrustStore struct {
	mu      sync.Mutex
	path    string
	loaded  bool
	trusted map[string]struct{}
	// display keeps the first-seen original spelling of a normalized key so the
	// file is readable and the original-case path can be recovered on load.
	display map[string]string
}

// TrustedWorkspaces returns a store backed by the persisted trust file, loading any
// existing entries.
func TrustedWorkspaces() *WorkspaceTrustStore {
	return &WorkspaceTrustStore{
		path:    TrustedWorkspacesPath(),
		trusted: make(map[string]struct{}),
		display: make(map[string]string),
	}
}

// TrustedWorkspacesPath is the absolute path to the persisted trust file in the
// user's global config directory (or the test override).
func TrustedWorkspacesPath() string {
	if trustedWorkspacesPathOverride != "" {
		return trustedWorkspacesPathOverride
	}
	return filepath.Join(home.Config(), appName, trustedWorkspacesFile)
}

// IsTrusted reports whether dir (after normalization) is in the persisted set.
func (s *WorkspaceTrustStore) IsTrusted(dir string) bool {
	key, ok := normalizeWorkspacePath(dir)
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoadedLocked(); err != nil {
		return false
	}
	_, ok = s.trusted[key]
	return ok
}

// Trust adds dir to the persisted trusted set and writes the file.
func (s *WorkspaceTrustStore) Trust(dir string) error {
	key, display, ok := normalizeWorkspacePathWithDisplay(dir)
	if !ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoadedLocked(); err != nil {
		return err
	}
	if _, exists := s.trusted[key]; exists {
		return nil
	}
	s.trusted[key] = struct{}{}
	s.display[key] = display
	return s.saveLocked()
}

// Untrust removes dir from the persisted trusted set and writes the file.
func (s *WorkspaceTrustStore) Untrust(dir string) error {
	key, ok := normalizeWorkspacePath(dir)
	if !ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoadedLocked(); err != nil {
		return err
	}
	if _, exists := s.trusted[key]; !exists {
		return nil
	}
	delete(s.trusted, key)
	delete(s.display, key)
	return s.saveLocked()
}

// List returns the trusted workspace roots in their stored display form, sorted.
func (s *WorkspaceTrustStore) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoadedLocked(); err != nil {
		return nil
	}
	out := make([]string, 0, len(s.display))
	for _, d := range s.display {
		out = append(out, d)
	}
	slices.Sort(out)
	return out
}

// trustedFile is the on-disk shape of the trust file.
type trustedFile struct {
	Workspaces []string `json:"workspaces"`
}

// ensureLoadedLocked reads the file once into memory. A missing file is an empty
// set; a corrupt file is treated as empty rather than failing the gate closed.
func (s *WorkspaceTrustStore) ensureLoadedLocked() error {
	if s.trusted != nil && s.loaded {
		return nil
	}
	if s.trusted == nil {
		s.trusted = make(map[string]struct{})
	}
	if s.display == nil {
		s.display = make(map[string]string)
	}
	s.loaded = true

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		slog.Warn("Failed to read trusted workspaces file", "path", s.path, "error", err)
		return nil
	}
	var tf trustedFile
	if err := json.Unmarshal(data, &tf); err != nil {
		slog.Warn("Ignoring corrupt trusted workspaces file", "path", s.path, "error", err)
		return nil
	}
	for _, w := range tf.Workspaces {
		if key, display, ok := normalizeWorkspacePathWithDisplay(w); ok {
			s.trusted[key] = struct{}{}
			s.display[key] = display
		}
	}
	return nil
}

// saveLocked writes the current set back to the trust file, creating its directory
// as needed.
func (s *WorkspaceTrustStore) saveLocked() error {
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	list := make([]string, 0, len(s.display))
	for _, d := range s.display {
		list = append(list, d)
	}
	slices.Sort(list)
	data, err := json.MarshalIndent(trustedFile{Workspaces: list}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// loaded records that ensureLoadedLocked has run so the file is read at most once
// per store instance.

// Convenience package-level helpers used by the app/agent wiring.

// IsWorkspaceTrusted reports whether the working directory is in the persisted trust
// list.
func IsWorkspaceTrusted(workingDir string) bool {
	return TrustedWorkspaces().IsTrusted(workingDir)
}

// TrustWorkspace persists the working directory as trusted.
func TrustWorkspace(workingDir string) error {
	return TrustedWorkspaces().Trust(workingDir)
}

// UntrustWorkspace removes the working directory from the persisted trust list.
func UntrustWorkspace(workingDir string) error {
	return TrustedWorkspaces().Untrust(workingDir)
}

// normalizeWorkspacePath resolves dir to an absolute, symlink-resolved, cleaned,
// and (on case-insensitive platforms) case-folded key used for trust comparison.
func normalizeWorkspacePath(dir string) (string, bool) {
	key, _, ok := normalizeWorkspacePathWithDisplay(dir)
	return key, ok
}

func normalizeWorkspacePathWithDisplay(dir string) (key, display string, ok bool) {
	if strings.TrimSpace(dir) == "" {
		return "", "", false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)
	// Drop a trailing separator so "/a/b" and "/a/b/" key identically.
	abs = strings.TrimRight(abs, string(filepath.Separator))

	display = abs
	key = abs
	if isCaseInsensitivePlatform() {
		key = strings.ToLower(abs)
	}
	if key == "" {
		return "", "", false
	}
	return key, display, true
}

// isCaseInsensitivePlatform reports whether path comparison should ignore case.
// Only Windows is assumed case-insensitive: macOS volumes may be formatted
// case-sensitively, where /repo/Foo and /repo/foo are distinct workspaces, so
// folding their keys together would let consent for one silently trust the other.
// A case-insensitive macoS volume then just re-prompts alternate spellings, which
// fails closed and is the safe direction for a mistaken guess.
func isCaseInsensitivePlatform() bool {
	return runtime.GOOS == "windows"
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirNonEmpty(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !isIgnorableName(e.Name()) {
			return true
		}
	}
	return false
}

func isIgnorableName(name string) bool {
	switch name {
	case ".git", ".DS_Store", "Thumbs.db", "Desktop.ini":
		return true
	}
	return false
}

// resetWorkspaceTrustStateForTest clears the in-process trust gate state so unit
// tests run against a known baseline. It is not part of the public surface and is
// only referenced from tests.
func resetWorkspaceTrustStateForTest() {
	trustMu.Lock()
	trustPrompt = func(string, string) bool { return false }
	customPromptSet = false
	trustInteractve = false
	trustRequested = false
	trustRequestedFor = ""
	trustedWorkspacesPathOverride = ""
	trustMu.Unlock()

	trustCacheMu.Lock()
	trustCache = make(map[string]bool)
	trustCacheMu.Unlock()
}
