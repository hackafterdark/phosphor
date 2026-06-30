package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/diff"
	"github.com/hackafterdark/phosphor/internal/filepathext"
	"github.com/hackafterdark/phosphor/internal/filetracker"
	"github.com/hackafterdark/phosphor/internal/fsext"
	"github.com/hackafterdark/phosphor/internal/history"
	"github.com/hackafterdark/phosphor/internal/lsp"
	"github.com/hackafterdark/phosphor/internal/otel"
	"github.com/hackafterdark/phosphor/internal/permission"
	"go.opentelemetry.io/otel/attribute"
)

type EditParams struct {
	FilePath   string `json:"file_path" description:"The absolute path to the file to modify"`
	OldString  string `json:"old_string" description:"The text to replace"`
	NewString  string `json:"new_string" description:"The text to replace it with"`
	ReplaceAll bool   `json:"replace_all,omitempty" description:"Replace all occurrences of old_string (default false)"`
}

type EditPermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

type EditResponseMetadata struct {
	Additions      int      `json:"additions"`
	Removals       int      `json:"removals"`
	OldContent     string   `json:"old_content,omitempty"`
	NewContent     string   `json:"new_content,omitempty"`
	NewDiagnostics []string `json:"new_diagnostics,omitempty"`
}

const EditToolName = "edit"

var (
	oldStringNotFoundErr        = fantasy.NewTextErrorResponse("old_string not found in file. Make sure it matches exactly, including whitespace and line breaks.")
	oldStringMultipleMatchesErr = fantasy.NewTextErrorResponse("old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true")
)

//go:embed edit.md
var editDescription string

type fuzzyCache struct {
	mu    sync.RWMutex
	store map[string][]matchCandidate
}

func newFuzzyCache() *fuzzyCache {
	return &fuzzyCache{
		store: make(map[string][]matchCandidate),
	}
}

func (c *fuzzyCache) Get(filePath, oldString string) ([]matchCandidate, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res, ok := c.store[filePath+":"+oldString]
	return res, ok
}

func (c *fuzzyCache) Set(filePath, oldString string, candidates []matchCandidate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[filePath+":"+oldString] = candidates
}

func (c *fuzzyCache) Invalidate(filePath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.store {
		if strings.HasPrefix(k, filePath+":") {
			delete(c.store, k)
		}
	}
}

type editContext struct {
	ctx         context.Context
	permissions permission.Service
	files       history.Service
	filetracker filetracker.Service
	workingDir  string
	lspManager  *lsp.Manager
	fuzzyCache  *fuzzyCache
}

func NewEditTool(
	lspManager *lsp.Manager,
	permissions permission.Service,
	files history.Service,
	filetracker filetracker.Service,
	workingDir string,
) fantasy.AgentTool {
	cache := newFuzzyCache()
	return fantasy.NewAgentTool(
		EditToolName,
		editDescription,
		func(ctx context.Context, params EditParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool edit")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", EditToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}

			params.FilePath = filepathext.SmartJoin(workingDir, params.FilePath)

			absWorkingDir, err := filepath.Abs(workingDir)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving working directory: %w", err)
			}
			absFilePath, err := filepath.Abs(params.FilePath)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving file path: %w", err)
			}
			if !filepathext.IsInside(absFilePath, absWorkingDir) {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Security violation: path %s is outside workspace", absFilePath)), nil
			}
			params.FilePath = absFilePath

			var response fantasy.ToolResponse

			editCtx := editContext{
				ctx:         ctx,
				permissions: permissions,
				files:       files,
				filetracker: filetracker,
				workingDir:  workingDir,
				lspManager:  lspManager,
				fuzzyCache:  cache,
			}

			preDiags := getDiagnosticsList(params.FilePath, lspManager)
			preErrors := countSeverity(preDiags, "Error")

			if params.OldString == "" {
				response, err = createNewFile(editCtx, params.FilePath, params.NewString, call)
			} else if params.NewString == "" {
				response, err = deleteContent(editCtx, params.FilePath, params.OldString, params.ReplaceAll, call)
			} else {
				response, err = replaceContent(editCtx, params.FilePath, params.OldString, params.NewString, params.ReplaceAll, call)
			}

			if err != nil {
				return response, err
			}
			if response.IsError {
				// Return early if there was an error during content replacement
				// This prevents unnecessary LSP diagnostics processing
				return response, nil
			}

			notifyLSPs(ctx, lspManager, params.FilePath)
			postDiags := getDiagnosticsList(params.FilePath, lspManager)
			postErrors := countSeverity(postDiags, "Error")

			var newDiags []string
			preMap := make(map[string]bool)
			for _, d := range preDiags {
				preMap[d] = true
			}
			for _, d := range postDiags {
				if !preMap[d] {
					newDiags = append(newDiags, d)
				}
			}

			var meta EditResponseMetadata
			if response.Metadata != "" {
				_ = json.Unmarshal([]byte(response.Metadata), &meta)
			}
			meta.NewDiagnostics = newDiags
			metaBytes, _ := json.Marshal(meta)
			response.Metadata = string(metaBytes)

			text := fmt.Sprintf("<result>\n%s\n</result>\n", response.Content)
			text += getDiagnostics(params.FilePath, lspManager)
			if postErrors > preErrors {
				text = fmt.Sprintf("WARNING: ACTION REQUIRED: Your last edit introduced %d new error(s). Please prioritize fixing these newly introduced diagnostics.\n\n%s", postErrors-preErrors, text)
			}
			response.Content = text
			return response, nil
		},
	)
}

func createNewFile(edit editContext, filePath, content string, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if err := checkSecrets(content); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if err := verifySyntax(content, filePath); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Syntax error validation failed: %v. Your edit was rejected to prevent committing broken code. Please correct the edit.", err)), nil
	}

	fileInfo, err := os.Stat(filePath)
	if err == nil {
		if fileInfo.IsDir() {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", filePath)), nil
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("file already exists: %s", filePath)), nil
	} else if !os.IsNotExist(err) {
		return fantasy.ToolResponse{}, fmt.Errorf("failed to access file: %w", err)
	}

	dir := filepath.Dir(filePath)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("failed to create parent directories: %w", err)
	}

	sessionID := GetSessionFromContext(edit.ctx)
	if sessionID == "" {
		return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for creating a new file")
	}

	_, additions, removals := diff.GenerateDiff(
		"",
		content,
		strings.TrimPrefix(filePath, edit.workingDir),
	)
	p, err := edit.permissions.Request(
		edit.ctx,
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        fsext.PathOrPrefix(filePath, edit.workingDir),
			ToolCallID:  call.ID,
			ToolName:    EditToolName,
			Action:      "write",
			Description: fmt.Sprintf("Create file %s", filePath),
			Params: EditPermissionsParams{
				FilePath:   filePath,
				OldContent: "",
				NewContent: content,
			},
		},
	)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if !p {
		resp := NewPermissionDeniedResponse()
		resp = fantasy.WithResponseMetadata(resp, EditResponseMetadata{
			OldContent: "",
			NewContent: content,
			Additions:  additions,
			Removals:   removals,
		})
		return resp, nil
	}

	err = os.WriteFile(filePath, []byte(content), 0o644)
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("failed to write file: %w", err)
	}

	// File can't be in the history so we create a new file history
	_, err = edit.files.Create(edit.ctx, sessionID, filePath, "")
	if err != nil {
		// Log error but don't fail the operation
		return fantasy.ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
	}

	// Add the new content to the file history
	_, err = edit.files.CreateVersion(edit.ctx, sessionID, filePath, content)
	if err != nil {
		// Log error but don't fail the operation
		slog.Error("Error creating file history version", "error", err)
	}

	edit.filetracker.RecordRead(edit.ctx, sessionID, filePath)

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse("File created: "+filePath),
		EditResponseMetadata{
			OldContent: "",
			NewContent: content,
			Additions:  additions,
			Removals:   removals,
		},
	), nil
}

func deleteContent(edit editContext, filePath, oldString string, replaceAll bool, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("file not found: %s", filePath)), nil
		}
		return fantasy.ToolResponse{}, fmt.Errorf("failed to access file: %w", err)
	}

	if fileInfo.IsDir() {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", filePath)), nil
	}

	sessionID := GetSessionFromContext(edit.ctx)
	if sessionID == "" {
		return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for deleting content")
	}

	// Check if file was read before editing
	lastRead := edit.filetracker.LastReadTime(edit.ctx, sessionID, filePath)
	if lastRead.IsZero() {
		return fantasy.NewTextErrorResponse("you must read the file before editing it. Use the View tool first"), nil
	}

	// Read initial file content
	initialBytes, err := os.ReadFile(filePath)
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("failed to read file: %w", err)
	}
	initialContent := string(initialBytes)

	// Perform the edit calculation on the initial content
	oldContent, isCrlf := fsext.ToUnixLineEndings(initialContent)
	newContent, err := applyEditWithFuzzy(edit.fuzzyCache, filePath, initialContent, oldString, "", replaceAll)
	if err != nil {
		if strings.Contains(err.Error(), "multiple times") {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		return makeNotFoundError(initialContent, oldString), nil
	}

	_, additions, removals := diff.GenerateDiff(
		oldContent,
		newContent,
		strings.TrimPrefix(filePath, edit.workingDir),
	)

	// Ask for permissions
	p, err := edit.permissions.Request(
		edit.ctx,
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        fsext.PathOrPrefix(filePath, edit.workingDir),
			ToolCallID:  call.ID,
			ToolName:    EditToolName,
			Action:      "write",
			Description: fmt.Sprintf("Delete content from file %s", filePath),
			Params: EditPermissionsParams{
				FilePath:   filePath,
				OldContent: oldContent,
				NewContent: newContent,
			},
		},
	)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if !p {
		resp := NewPermissionDeniedResponse()
		resp = fantasy.WithResponseMetadata(resp, EditResponseMetadata{
			OldContent: oldContent,
			NewContent: newContent,
			Additions:  additions,
			Removals:   removals,
		})
		return resp, nil
	}

	// Atomic Read-Modify-Write Loop
	var finalContentToWrite string
	success := false
	for attempt := 0; attempt < 5; attempt++ {
		currentDiskBytes, err := os.ReadFile(filePath)
		if err != nil {
			return fantasy.ToolResponse{}, fmt.Errorf("failed to read file during atomic write check: %w", err)
		}
		currentDiskContent := string(currentDiskBytes)

		if currentDiskContent == initialContent {
			finalContentToWrite = newContent
			success = true
			break
		}

		// File has been modified concurrently! Perform silent refresh.
		slog.Info("Retrying edit: file state updated from disk, re-calculating fuzzy match", "file", filePath, "attempt", attempt+1)
		if edit.fuzzyCache != nil {
			edit.fuzzyCache.Invalidate(filePath)
		}
		initialContent = currentDiskContent
		oldContent, isCrlf = fsext.ToUnixLineEndings(initialContent)

		newContent, err = applyEditWithFuzzy(edit.fuzzyCache, filePath, initialContent, oldString, "", replaceAll)
		if err != nil {
			if strings.Contains(err.Error(), "multiple times") {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			return makeNotFoundError(initialContent, oldString), nil
		}
		finalContentToWrite = newContent
	}

	if !success {
		return fantasy.ToolResponse{}, fmt.Errorf("Edit failed after 5 retries due to persistent concurrent file modifications. Please retry.")
	}

	if isCrlf {
		finalContentToWrite, _ = fsext.ToWindowsLineEndings(finalContentToWrite)
	}

	err = os.WriteFile(filePath, []byte(finalContentToWrite), 0o644)
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("failed to write file: %w", err)
	}
	if edit.fuzzyCache != nil {
		edit.fuzzyCache.Invalidate(filePath)
	}

	// Check if file exists in history
	file, err := edit.files.GetByPathAndSession(edit.ctx, filePath, sessionID)
	if err != nil {
		_, err = edit.files.Create(edit.ctx, sessionID, filePath, oldContent)
		if err != nil {
			return fantasy.ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
		}
	}
	if file.Content != oldContent {
		// User manually changed the content; store an intermediate version
		_, err = edit.files.CreateVersion(edit.ctx, sessionID, filePath, oldContent)
		if err != nil {
			slog.Error("Error creating file history version", "error", err)
		}
	}
	// Store the new version
	_, err = edit.files.CreateVersion(edit.ctx, sessionID, filePath, newContent)
	if err != nil {
		slog.Error("Error creating file history version", "error", err)
	}

	edit.filetracker.RecordRead(edit.ctx, sessionID, filePath)

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse("Content deleted from file: "+filePath),
		EditResponseMetadata{
			OldContent: oldContent,
			NewContent: newContent,
			Additions:  additions,
			Removals:   removals,
		},
	), nil
}

func replaceContent(edit editContext, filePath, oldString, newString string, replaceAll bool, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if err := checkSecrets(newString); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("file not found: %s", filePath)), nil
		}
		return fantasy.ToolResponse{}, fmt.Errorf("failed to access file: %w", err)
	}

	if fileInfo.IsDir() {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", filePath)), nil
	}

	sessionID := GetSessionFromContext(edit.ctx)
	if sessionID == "" {
		return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for edit a file")
	}

	// Check if file was read before editing
	lastRead := edit.filetracker.LastReadTime(edit.ctx, sessionID, filePath)
	if lastRead.IsZero() {
		return fantasy.NewTextErrorResponse("you must read the file before editing it. Use the View tool first"), nil
	}

	// Read initial file content
	initialBytes, err := os.ReadFile(filePath)
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("failed to read file: %w", err)
	}
	initialContent := string(initialBytes)

	// Perform the edit calculation on the initial content
	oldContent, isCrlf := fsext.ToUnixLineEndings(initialContent)
	newContent, err := applyEditWithFuzzy(edit.fuzzyCache, filePath, initialContent, oldString, newString, replaceAll)
	if err != nil {
		if strings.Contains(err.Error(), "multiple times") {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		return makeNotFoundError(initialContent, oldString), nil
	}

	if oldContent == newContent {
		return fantasy.NewTextErrorResponse("new content is the same as old content. No changes made."), nil
	}

	if err := verifySyntax(newContent, filePath); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Syntax error validation failed: %v. Your edit was rejected to prevent committing broken code. Please correct the edit.", err)), nil
	}

	_, additions, removals := diff.GenerateDiff(
		oldContent,
		newContent,
		strings.TrimPrefix(filePath, edit.workingDir),
	)

	// Ask for permissions
	p, err := edit.permissions.Request(
		edit.ctx,
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        fsext.PathOrPrefix(filePath, edit.workingDir),
			ToolCallID:  call.ID,
			ToolName:    EditToolName,
			Action:      "write",
			Description: fmt.Sprintf("Replace content in file %s", filePath),
			Params: EditPermissionsParams{
				FilePath:   filePath,
				OldContent: oldContent,
				NewContent: newContent,
			},
		},
	)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if !p {
		resp := NewPermissionDeniedResponse()
		resp = fantasy.WithResponseMetadata(resp, EditResponseMetadata{
			OldContent: oldContent,
			NewContent: newContent,
			Additions:  additions,
			Removals:   removals,
		})
		return resp, nil
	}

	// Atomic Read-Modify-Write Loop
	var finalContentToWrite string
	success := false
	for attempt := 0; attempt < 5; attempt++ {
		currentDiskBytes, err := os.ReadFile(filePath)
		if err != nil {
			return fantasy.ToolResponse{}, fmt.Errorf("failed to read file during atomic write check: %w", err)
		}
		currentDiskContent := string(currentDiskBytes)

		if currentDiskContent == initialContent {
			finalContentToWrite = newContent
			success = true
			break
		}

		// File has been modified concurrently! Perform silent refresh.
		slog.Info("Retrying edit: file state updated from disk, re-calculating fuzzy match", "file", filePath, "attempt", attempt+1)
		if edit.fuzzyCache != nil {
			edit.fuzzyCache.Invalidate(filePath)
		}
		initialContent = currentDiskContent
		oldContent, isCrlf = fsext.ToUnixLineEndings(initialContent)

		newContent, err = applyEditWithFuzzy(edit.fuzzyCache, filePath, initialContent, oldString, newString, replaceAll)
		if err != nil {
			if strings.Contains(err.Error(), "multiple times") {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			return makeNotFoundError(initialContent, oldString), nil
		}
		if err := verifySyntax(newContent, filePath); err != nil {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("Syntax error validation failed: %v. Your edit was rejected to prevent committing broken code. Please correct the edit.", err)), nil
		}
		finalContentToWrite = newContent
	}

	if !success {
		return fantasy.ToolResponse{}, fmt.Errorf("Edit failed after 5 retries due to persistent concurrent file modifications. Please retry.")
	}

	if isCrlf {
		finalContentToWrite, _ = fsext.ToWindowsLineEndings(finalContentToWrite)
	}

	err = os.WriteFile(filePath, []byte(finalContentToWrite), 0o644)
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("failed to write file: %w", err)
	}
	if edit.fuzzyCache != nil {
		edit.fuzzyCache.Invalidate(filePath)
	}

	// Check if file exists in history
	file, err := edit.files.GetByPathAndSession(edit.ctx, filePath, sessionID)
	if err != nil {
		_, err = edit.files.Create(edit.ctx, sessionID, filePath, oldContent)
		if err != nil {
			return fantasy.ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
		}
	}
	if file.Content != oldContent {
		// User manually changed the content; store an intermediate version
		_, err = edit.files.CreateVersion(edit.ctx, sessionID, filePath, oldContent)
		if err != nil {
			slog.Debug("Error creating file history version", "error", err)
		}
	}
	// Store the new version
	_, err = edit.files.CreateVersion(edit.ctx, sessionID, filePath, newContent)
	if err != nil {
		slog.Error("Error creating file history version", "error", err)
	}

	edit.filetracker.RecordRead(edit.ctx, sessionID, filePath)

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse("Content replaced in file: "+filePath),
		EditResponseMetadata{
			OldContent: oldContent,
			NewContent: newContent,
			Additions:  additions,
			Removals:   removals,
		},
	), nil
}

func applyEditWithFuzzy(cache *fuzzyCache, filePath, content, oldString, newString string, replaceAll bool) (string, error) {
	oldContent, _ := fsext.ToUnixLineEndings(content)
	oldStringNormalized, _ := fsext.ToUnixLineEndings(oldString)
	newStringNormalized, _ := fsext.ToUnixLineEndings(newString)

	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(oldContent, oldStringNormalized, newStringNormalized)
		if newContent == oldContent {
			return "", fmt.Errorf("old_string not found in file")
		}
	} else {
		index := strings.Index(oldContent, oldStringNormalized)
		if index == -1 {
			// exact match failed, try fuzzy
			var candidates []matchCandidate
			var cached bool
			if cache != nil {
				candidates, cached = cache.Get(filePath, oldStringNormalized)
			}
			if !cached {
				candidates = findCloseMatches(oldContent, oldStringNormalized)
				if cache != nil && len(candidates) > 0 {
					cache.Set(filePath, oldStringNormalized, candidates)
				}
			}

			if len(candidates) == 0 {
				return "", fmt.Errorf("old_string not found in file")
			}
			best := candidates[0]
			if best.similarity < 0.8 && !best.isWhitespaceOnly {
				return "", fmt.Errorf("old_string not found in file (best match similarity %.0f%% is below threshold)", best.similarity*100)
			}
			index = strings.Index(oldContent, best.text)
			if index == -1 {
				return "", fmt.Errorf("failed to locate fuzzy match candidate in file")
			}

			// Quote preservation
			finalNewString := preserveQuoteStyle(best.text, newStringNormalized)
			newContent = oldContent[:index] + finalNewString + oldContent[index+len(best.text):]
		} else {
			lastIndex := strings.LastIndex(oldContent, oldStringNormalized)
			if index != lastIndex {
				return "", fmt.Errorf("old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true")
			}

			// Quote preservation
			finalNewString := preserveQuoteStyle(oldStringNormalized, newStringNormalized)
			newContent = oldContent[:index] + finalNewString + oldContent[index+len(oldStringNormalized):]
		}
	}

	return newContent, nil
}

func preserveQuoteStyle(matchedText, newString string) string {
	singleMatch := strings.Count(matchedText, "'")
	doubleMatch := strings.Count(matchedText, "\"")
	backtickMatch := strings.Count(matchedText, "`")

	if singleMatch == 0 && doubleMatch == 0 && backtickMatch == 0 {
		return newString
	}

	var targetQuote rune
	if singleMatch >= doubleMatch && singleMatch >= backtickMatch {
		targetQuote = '\''
	} else if doubleMatch >= singleMatch && doubleMatch >= backtickMatch {
		targetQuote = '"'
	} else {
		targetQuote = '`'
	}

	singleNew := strings.Count(newString, "'")
	doubleNew := strings.Count(newString, "\"")
	backtickNew := strings.Count(newString, "`")

	var sourceQuote rune
	if singleNew >= doubleNew && singleNew >= backtickNew {
		sourceQuote = '\''
	} else if doubleNew >= singleNew && doubleNew >= backtickNew {
		sourceQuote = '"'
	} else {
		sourceQuote = '`'
	}

	if targetQuote == sourceQuote {
		return newString
	}

	return convertQuotes(newString, sourceQuote, targetQuote)
}

func convertQuotes(s string, from, to rune) string {
	var sb strings.Builder
	runes := []rune(s)
	n := len(runes)
	escaped := false

	for i := 0; i < n; i++ {
		r := runes[i]
		if escaped {
			if r == from || r == to {
				sb.WriteRune('\\')
				sb.WriteRune(to)
			} else {
				sb.WriteRune('\\')
				sb.WriteRune(r)
			}
			escaped = false
			continue
		}

		if r == '\\' {
			escaped = true
			continue
		}

		if r == from {
			sb.WriteRune(to)
		} else {
			sb.WriteRune(r)
		}
	}
	if escaped {
		sb.WriteRune('\\')
	}
	return sb.String()
}

type matchCandidate struct {
	lineStart        int
	lineEnd          int
	text             string
	similarity       float64
	isWhitespaceOnly bool
}

func normalizeWhitespace(s string) string {
	var sb strings.Builder
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			if !inSpace {
				sb.WriteByte(' ')
				inSpace = true
			}
		} else {
			sb.WriteRune(r)
			inSpace = false
		}
	}
	return strings.TrimSpace(sb.String())
}

func levenshteinDistance(s1, s2 string) int {
	if len(s1) == 0 {
		return len(s2)
	}
	if len(s2) == 0 {
		return len(s1)
	}
	r1, r2 := []rune(s1), []rune(s2)
	len1, len2 := len(r1), len(r2)
	row := make([]int, len2+1)
	for i := 0; i <= len2; i++ {
		row[i] = i
	}
	for i := 1; i <= len1; i++ {
		prev := i
		for j := 1; j <= len2; j++ {
			val := row[j-1]
			if r1[i-1] != r2[j-1] {
				val++
			}
			d := row[j] + 1
			ins := prev + 1
			if d < val {
				val = d
			}
			if ins < val {
				val = ins
			}
			row[j-1] = prev
			prev = val
		}
		row[len2] = prev
	}
	return row[len2]
}

func findCloseMatches(fileContent, oldString string) []matchCandidate {
	normOld := normalizeWhitespace(oldString)
	if normOld == "" {
		return nil
	}
	fileLines := strings.Split(fileContent, "\n")
	oldLines := strings.Split(oldString, "\n")
	n := len(oldLines)
	if n == 0 {
		return nil
	}

	var candidates []matchCandidate
	minW := n - 1
	if minW < 1 {
		minW = 1
	}
	maxW := n + 1

	for w := minW; w <= maxW; w++ {
		for i := 0; i <= len(fileLines)-w; i++ {
			windowLines := fileLines[i : i+w]
			windowText := strings.Join(windowLines, "\n")
			normWindow := normalizeWhitespace(windowText)
			if normWindow == "" {
				continue
			}
			if normWindow == normOld {
				candidates = append(candidates, matchCandidate{
					lineStart:        i + 1,
					lineEnd:          i + w,
					text:             windowText,
					similarity:       1.0,
					isWhitespaceOnly: true,
				})
				continue
			}
			dist := levenshteinDistance(normOld, normWindow)
			maxLen := len(normOld)
			if len(normWindow) > maxLen {
				maxLen = len(normWindow)
			}
			similarity := 1.0 - float64(dist)/float64(maxLen)
			if similarity >= 0.5 {
				candidates = append(candidates, matchCandidate{
					lineStart:        i + 1,
					lineEnd:          i + w,
					text:             windowText,
					similarity:       similarity,
					isWhitespaceOnly: false,
				})
			}
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].isWhitespaceOnly && !candidates[j].isWhitespaceOnly {
			return true
		}
		if !candidates[i].isWhitespaceOnly && candidates[j].isWhitespaceOnly {
			return false
		}
		if candidates[i].similarity != candidates[j].similarity {
			return candidates[i].similarity > candidates[j].similarity
		}
		return (candidates[i].lineEnd - candidates[i].lineStart) < (candidates[j].lineEnd - candidates[j].lineStart)
	})

	var unique []matchCandidate
	seen := make(map[string]bool)
	for _, c := range candidates {
		key := fmt.Sprintf("%d:%s", c.lineStart, c.text)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, c)
			if len(unique) >= 3 {
				break
			}
		}
	}
	return unique
}

func makeNotFoundError(fileContent, oldString string) fantasy.ToolResponse {
	candidates := findCloseMatches(fileContent, oldString)
	if len(candidates) == 0 {
		return oldStringNotFoundErr
	}

	var sb strings.Builder
	sb.WriteString("old_string not found in file. Make sure it matches exactly, including whitespace and line breaks.\n")

	hasWhitespaceMatch := false
	for _, c := range candidates {
		if c.isWhitespaceOnly {
			hasWhitespaceMatch = true
			break
		}
	}

	if hasWhitespaceMatch {
		sb.WriteString("\nWe found a match with different whitespace/indentation. Did you mean:\n")
	} else {
		sb.WriteString("\nDid you mean one of these close matches?\n")
	}

	for _, c := range candidates {
		sb.WriteString(fmt.Sprintf("\n--- Match at lines %d-%d (similarity: %.0f%%) ---\n", c.lineStart, c.lineEnd, c.similarity*100))
		sb.WriteString(c.text)
		sb.WriteString("\n------------------------------------\n")
	}

	return fantasy.NewTextErrorResponse(sb.String())
}

var (
	awsSecretRegex   = regexp.MustCompile(`(?i)aws_(?:secret_)?access_key\s*[:=]\s*['"][A-Za-z0-9/\+=]{40}['"]`)
	privateKeyRegex  = regexp.MustCompile(`-----BEGIN [A-Z ]+ PRIVATE KEY-----`)
	genericApiKeyReg = regexp.MustCompile(`(?i)api_key\s*[:=]\s*['"][A-Za-z0-9_\-]{20,}['"]`)
)

func checkSecrets(content string) error {
	if awsSecretRegex.MatchString(content) {
		return fmt.Errorf("Security violation: potential AWS secret access key leak detected")
	}
	if privateKeyRegex.MatchString(content) {
		return fmt.Errorf("Security violation: potential private key leak detected")
	}
	if genericApiKeyReg.MatchString(content) {
		return fmt.Errorf("Security violation: potential API key leak detected")
	}
	return nil
}
