package tools

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/agent/parser"
	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/hackafterdark/phosphor/internal/otel"
	"go.opentelemetry.io/otel/attribute"
)

//go:embed structural_search.md.tpl
var structuralSearchDescriptionTmpl string // Change to string for easier template management

var structuralSearchDescriptionTpl = template.Must(
	template.New("structuralSearchDescription").
		Parse(structuralSearchDescriptionTmpl),
)

// Simplify this with the above and handle in structural_search.md.tpl instead
// var structuralSearchDescriptionTpl = template.Must(
// 	template.New("structuralSearchDescription").
// 		Funcs(template.FuncMap{
// 			"join": func(sep string, parts []string) string {
// 				if len(parts) == 0 {
// 					return ""
// 				}
// 				var sb strings.Builder
// 				sb.WriteString(parts[0])
// 				for _, p := range parts[1:] {
// 					sb.WriteString(sep)
// 					sb.WriteString(p)
// 				}
// 				return sb.String()
// 			},
// 		}).
// 		Parse(string(structuralSearchDescriptionTmpl)),
// )

// LanguageTemplates holds the template names for a single language.
type LanguageTemplates struct {
	Language  string
	Templates []string
}

type structuralSearchDescriptionData struct {
	LanguageTemplates []LanguageTemplates
}

func structuralSearchDescription() string {
	var langTemplates []LanguageTemplates
	seen := make(map[string]bool)
	for _, cap := range parser.GetCapabilities() {
		if !seen[cap.Language] {
			seen[cap.Language] = true
			names := parser.TemplateNames(cap.Language)
			if len(names) > 0 {
				langTemplates = append(langTemplates, LanguageTemplates{
					Language:  cap.Language,
					Templates: names,
				})
			}
		}
	}
	return renderTemplate(structuralSearchDescriptionTpl, structuralSearchDescriptionData{
		LanguageTemplates: langTemplates,
	})
}

// StructuralSearchParams are the parameters for the structural_search tool.
type StructuralSearchParams struct {
	// Action specifies what to do: "search" (default) or "list_templates".
	Action string `json:"action,omitempty" description:"Action to perform: 'search' (default, searches files) or 'list_templates' (returns available templates for a language)."`
	// TemplateName is the name of the pre-built query template to use.
	TemplateName string `json:"template_name,omitempty" description:"The name of the query template to use. Use action 'list_templates' with a language to discover available templates. Templates are language-specific."`
	// Path is the directory to search in. Defaults to the current working directory.
	Path string `json:"path,omitempty" description:"The directory to search in. Defaults to the current working directory."`
	// Include is a file pattern to filter by (e.g., "*.go", "*.ts").
	Include string `json:"include,omitempty" description:"File pattern to include in the search (e.g., '*.go', 'internal//*.go'). Defaults to language-specific extensions."`
	// MaxResults is the maximum number of results to return.
	MaxResults int `json:"max_results,omitempty" description:"Maximum number of results to return (default: 100)."`
	// Language is the programming language to search. If empty, auto-detected from file extensions.
	Language string `json:"language,omitempty" description:"The programming language to search (e.g., 'go', 'typescript', 'javascript', 'python'). If empty, auto-detected from file extensions."`
}

// AvailableTemplateInfo describes one available template.
type AvailableTemplateInfo struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// StructuralSearchCapture represents a single capture within a match.
type StructuralSearchCapture struct {
	// Capture name (e.g., "name", "function_name", "field_name")
	Capture string `json:"capture"`
	// The matched text
	Text string `json:"text"`
	// Line number (1-indexed)
	Line int `json:"line"`
	// Column number (0-indexed)
	Column int `json:"column"`
}

// StructuralSearchMatch represents a complete match across files.
type StructuralSearchMatch struct {
	// File path where the match was found
	File string `json:"file"`
	// Match index within the file
	MatchIndex int `json:"match_index"`
	// All captures for this match
	Captures []StructuralSearchCapture `json:"captures"`
}

// structuralSearchResponse is the metadata returned with the response.
type structuralSearchResponse struct {
	Matches       []StructuralSearchMatch `json:"matches"`
	TotalMatches  int                     `json:"total_matches"`
	FilesSearched int                     `json:"files_searched"`
}

const (
	StructuralSearchToolName = "structural_search"
)

func findFiles(workingDir, path, include string, lang string) ([]string, error) {
	searchPath := path
	if searchPath == "" {
		searchPath = workingDir
	}

	// Determine file extensions based on language
	var extensions []string
	switch lang {
	case "go":
		extensions = []string{".go"}
	case "c":
		extensions = []string{".c", ".h"}
	case "bash":
		extensions = []string{".sh"}
	case "hcl":
		extensions = []string{".hcl", ".tf"}
	// case "ruby": — Ruby not supported (tree-sitter-ruby v0.23.1 misparses class/method nodes)
	// case "ruby":
	// 	extensions = []string{".rb"}
	case "json":
		extensions = []string{".json"}
	case "html":
		extensions = []string{".html", ".htm"}
	case "css":
		extensions = []string{".css"}
	case "toml":
		extensions = []string{".toml"}
	// case "toml": — TOML not supported (tree-sitter-toml grammar issues)
	// case "toml":
	// 	extensions = []string{".toml"}
	case "scala":
		extensions = []string{".scala", ".sbt"}
	case "cpp":
		extensions = []string{".cpp", ".cc", ".cxx", ".hpp", ".hxx"}
	case "typescript":
		extensions = []string{".ts", ".tsx"}
	case "javascript":
		extensions = []string{".js", ".jsx"}
	case "python":
		extensions = []string{".py"}
	case "sql":
		extensions = []string{".sql"}
	case "rust":
		extensions = []string{".rs"}
	case "php":
		extensions = []string{".php"}
	// "java" — Java not supported (requires external scanner not present in vendored grammar)
	case "csharp":
		extensions = []string{".cs"}
	default:
		extensions = []string{".go"}
	}

	var files []string
	if include != "" {
		// Use filepath.Glob for glob patterns
		globPattern := filepath.Join(searchPath, include)
		matches, err := filepath.Glob(globPattern)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil {
				continue
			}
			if !info.IsDir() {
				files = append(files, m)
			}
		}
	} else {
		// Default: find files with language-specific extensions
		err := filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				for _, ext := range extensions {
					if strings.HasSuffix(path, ext) {
						files = append(files, path)
						break
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return files, nil
}

func formatTemplateList(templates []AvailableTemplateInfo) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Available templates (%d):\n\n", len(templates)))
	for _, t := range templates {
		fmt.Fprintf(&sb, "  %s: %s\n", t.ID, t.Description)
	}
	return sb.String()
}

func formatResults(results []StructuralSearchMatch, maxResults int) string {
	if len(results) == 0 {
		return "No matches found"
	}

	var sb strings.Builder
	if len(results) >= maxResults {
		fmt.Fprintf(&sb, "Found at least %d matches (truncated)\n\n", maxResults)
	} else {
		fmt.Fprintf(&sb, "Found %d matches\n\n", len(results))
	}

	currentFile := ""
	for _, result := range results {
		if currentFile != result.File {
			if currentFile != "" {
				sb.WriteString("\n")
			}
			currentFile = result.File
			sb.WriteString(fmt.Sprintf("=== %s ===\n", result.File))
		}
		for i, cap := range result.Captures {
			if i == 0 {
				fmt.Fprintf(&sb, "  Line %d, Col %d: %s\n", cap.Line, cap.Column, cap.Text)
			} else {
				fmt.Fprintf(&sb, "    %-15s: %s\n", cap.Capture, cap.Text)
			}
		}
	}

	return sb.String()
}

func executeStructuralSearch(ctx context.Context, workingDir string, params StructuralSearchParams) (fantasy.ToolResponse, error) {
	// Handle list_templates action: return available templates for a language.
	if params.Action == "list_templates" {
		lang := params.Language
		if lang == "" {
			lang = "go"
		}
		names := parser.TemplateNames(lang)
		if names == nil {
			names = []string{}
		}
		var templates []AvailableTemplateInfo
		for _, name := range names {
			if cap, ok := parser.GetCapability(lang, name); ok {
				templates = append(templates, AvailableTemplateInfo{
					ID:          cap.ID,
					Description: cap.Description,
				})
			}
		}
		if len(templates) == 0 {
			templates = []AvailableTemplateInfo{}
		}
		return fantasy.WithResponseMetadata(
			fantasy.NewTextResponse(formatTemplateList(templates)),
			structuralSearchResponse{
				TotalMatches: len(templates),
			},
		), nil
	}

	searchPath := workingDir
	if params.Path != "" {
		resolved, err := filepathext.ResolveSearchPath(workingDir, params.Path)
		if err != nil {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("error resolving search path: %v", err)), nil
		}
		searchPath = resolved
	}

	// Enforce workspace bounds
	absWorkingDir, err := filepath.Abs(workingDir)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("error resolving working directory: %v", err)), nil
	}
	absSearchPath, err := filepath.Abs(searchPath)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("error resolving search path: %v", err)), nil
	}
	if !filepathext.IsInside(absSearchPath, absWorkingDir) {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Security violation: path %s is outside workspace", absSearchPath)), nil
	}
	searchPath = absSearchPath

	// Determine language: use explicit param, or detect from first file
	lang := params.Language
	if lang == "" {
		// Detect from include pattern or default to go
		if params.Include != "" {
			// Try to infer from include pattern (e.g., "*.sql")
			ext := filepath.Ext(strings.TrimPrefix(params.Include, "*"))
			switch ext {
			case ".go":
				lang = "go"
			case ".ts", ".tsx":
				lang = "typescript"
			case ".js", ".jsx":
				lang = "javascript"
			case ".py":
				lang = "python"
			case ".sql":
				lang = "sql"
			case ".rs":
				lang = "rust"
			case ".java":
				lang = "java"
			case ".php":
				lang = "php"
			case ".cpp", ".cc", ".cxx", ".hpp", ".hxx":
				lang = "cpp"
			case ".cs":
				lang = "csharp"
			case ".hcl", ".tf":
				lang = "hcl"
			case ".css":
				lang = "css"
			case ".toml":
				lang = "toml"
			case ".scala", ".sbt":
				lang = "scala"
			case ".c", ".h":
				lang = "c"
			case ".sh":
				lang = "bash"
			case ".json":
				lang = "json"
			case ".html", ".htm":
				lang = "html"
			case ".rb":
				lang = "ruby"
			default:
				lang = "go"
			}
		} else {
			lang = "go"
		}
	}

	files, err := findFiles(workingDir, searchPath, params.Include, lang)
	if err != nil {
		return fantasy.NewTextErrorResponse("error finding files: " + err.Error()), nil
	}

	if len(files) == 0 {
		return fantasy.NewTextResponse("No files found matching the pattern"), nil
	}

	// Get the query template for the language
	_, ok := parser.GetTemplate(lang, params.TemplateName)
	if !ok && !strings.HasPrefix(strings.TrimSpace(params.TemplateName), "(") {
		available := strings.Join(parser.TemplateNames(lang), ", ")
		return fantasy.NewTextErrorResponse("unknown template: " + params.TemplateName + ". Available for " + lang + ": " + available), nil
	}

	maxResults := params.MaxResults
	if maxResults <= 0 {
		maxResults = 100
	}

	var allResults []StructuralSearchMatch
	filesSearched := 0

	for _, file := range files {
		// Read file
		code, err := os.ReadFile(file)
		if err != nil {
			os.WriteFile("F:/hackafterdark/phosphor/debug_readerr.txt", []byte("read error: "+err.Error()), 0o644)
			continue
		}

		// Parse with tree-sitter (detect language from file path for safety)
		fileLang := parser.DetectLanguage(file)
		root := parser.Parse(code, fileLang)

		// Run query
		matches, err := parser.Query(root, code, fileLang, params.TemplateName)
		if err != nil {
			os.WriteFile("F:/hackafterdark/phosphor/debug_queryerr.txt", []byte("query error: "+err.Error()), 0o644)
			continue
		}

		os.WriteFile("F:/hackafterdark/phosphor/debug_matches.txt", []byte(fmt.Sprintf("file=%s lang=%s matches=%d", file, fileLang, len(matches))), 0o644)

		filesSearched++

		// Convert matches to response format
		for _, match := range matches {
			var captures []StructuralSearchCapture
			for _, cap := range match.Captures {
				captures = append(captures, StructuralSearchCapture{
					Capture: cap.Capture,
					Text:    cap.Text,
					Line:    int(cap.StartPos.Row) + 1, // Convert to 1-indexed
					Column:  int(cap.StartPos.Column),
				})
			}
			allResults = append(allResults, StructuralSearchMatch{
				File:       file,
				MatchIndex: match.Index,
				Captures:   captures,
			})

			if len(allResults) >= maxResults {
				goto done
			}
		}
	}

done:
	if len(allResults) == 0 {
		msg := "No matches found."
		if cap, ok := parser.GetCapability(lang, params.TemplateName); ok {
			var guidanceParts []string
			if cap.Guidance != "" {
				guidanceParts = append(guidanceParts, "Guidance: "+cap.Guidance)
			}
			if cap.Preconditions != "" {
				guidanceParts = append(guidanceParts, "Preconditions: "+cap.Preconditions)
			}
			if len(guidanceParts) > 0 {
				msg += "\n" + strings.Join(guidanceParts, "\n")
			}
		}
		return fantasy.WithResponseMetadata(
			fantasy.NewTextResponse(msg),
			structuralSearchResponse{
				Matches:       nil,
				TotalMatches:  0,
				FilesSearched: filesSearched,
			},
		), nil
	}

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse(formatResults(allResults, maxResults)),
		structuralSearchResponse{
			Matches:       allResults,
			TotalMatches:  len(allResults),
			FilesSearched: filesSearched,
		},
	), nil
}

// NewStructuralSearchTool creates a new structural search tool.
func NewStructuralSearchTool(workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		"structural_search",
		structuralSearchDescription(),
		func(ctx context.Context, params StructuralSearchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			os.WriteFile("C:/tmp/phosphor_debug.txt", []byte("handler called\n"+fmt.Sprintf("call.Input=%q\nparams=%+v\n", call.Input, params)), 0o644)
			ctx, span := otel.StartSpan(ctx, "execute_tool structural_search")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", StructuralSearchToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)
			return executeStructuralSearch(ctx, workingDir, params)
		},
	)
}
