//go:build !cgo

package parser

// QueryCapability defines a structural search capability.
type QueryCapability struct {
	ID            string `yaml:"id" json:"id"`
	Description   string `yaml:"description" json:"description"`
	Language      string `yaml:"language" json:"language"`
	Query         string `yaml:"query" json:"query"`
	Guidance      string `yaml:"guidance,omitempty" json:"guidance,omitempty"`
	Preconditions string `yaml:"preconditions,omitempty" json:"preconditions,omitempty"`
}

// Match represents a single query match result.
type Match struct {
	Index    int
	Captures []QueryResult
}

// QueryResult represents a captured node in a match.
type QueryResult struct {
	Capture   string
	Text      string
	StartByte uint32
	EndByte   uint32
	StartPos  Pos
	EndPos    Pos
}

// Pos represents a position in source code.
type Pos struct {
	Row    uint
	Column uint
}

// Node is a stub structure replacing sitter.Node when CGO is disabled.
type Node struct{}

// QueryRegistry is a stub structure replacing the real QueryRegistry.
type QueryRegistry struct{}

// Reload is a stub implementation.
func (r *QueryRegistry) Reload(workspaceDir string) error {
	return nil
}

// GetTemplate is a stub implementation.
func (r *QueryRegistry) GetTemplate(lang, name string) (string, bool) {
	return "", false
}

// TemplateNames is a stub implementation.
func (r *QueryRegistry) TemplateNames(lang string) []string {
	return nil
}

// GetCapability is a stub implementation.
func (r *QueryRegistry) GetCapability(lang, name string) (QueryCapability, bool) {
	return QueryCapability{}, false
}

// Registry is a global registry instance stub.
var Registry = &QueryRegistry{}

// CloseWatcher is a stub implementation.
func CloseWatcher() {}

// ReloadQueries is a stub implementation.
func ReloadQueries(workspaceDir string) error {
	return nil
}

// StartWatcher is a stub implementation.
func StartWatcher(workspaceDir string, onReload func()) error {
	return nil
}

// GetCapabilities is a stub implementation.
func GetCapabilities() []QueryCapability {
	return nil
}

// GetCapability is a stub implementation.
func GetCapability(lang, name string) (QueryCapability, bool) {
	return QueryCapability{}, false
}

// DetectLanguage returns a dummy language name when CGO is disabled.
func DetectLanguage(filePath string) string {
	return ""
}

// Parse is a stub implementation.
func Parse(code []byte, lang string) *Node {
	return nil
}

// Query is a stub implementation.
func Query(root *Node, code []byte, lang, id string) ([]Match, error) {
	return nil, nil
}

// SupportedLanguages is a stub implementation.
func SupportedLanguages() []string {
	return nil
}
