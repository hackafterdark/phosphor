package tools

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

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

//go:embed append.md
var appendDescription string

type AppendParams struct {
	FilePath string `json:"file_path" description:"The path to the file to append to"`
	Content  string `json:"content" description:"The content to append to the file"`
}

type AppendPermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

type AppendResponseMetadata struct {
	Diff      string `json:"diff"`
	Additions int    `json:"additions"`
	Removals  int    `json:"removals"`
	OldSize   int    `json:"old_size"`
	NewSize   int    `json:"new_size"`
}

const AppendToolName = "append"

func NewAppendTool(
	lspManager *lsp.Manager,
	permissions permission.Service,
	files history.Service,
	filetracker filetracker.Service,
	workingDir string,
) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		AppendToolName,
		appendDescription,
		func(ctx context.Context, params AppendParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			ctx, span := otel.StartSpan(ctx, "execute_tool append")
			defer span.End()
			span.SetAttributes(
				attribute.String("gen_ai.tool.name", AppendToolName),
				attribute.String("gen_ai.tool.call.id", call.ID),
				attribute.String("gen_ai.tool.call.arguments", call.Input),
			)
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}

			if params.Content == "" {
				return fantasy.NewTextErrorResponse("content is required"), nil
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session_id is required")
			}

			absWorkingDir, err := filepath.Abs(workingDir)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving working directory: %w", err)
			}
			filePath := filepathext.SmartJoin(absWorkingDir, params.FilePath)
			absFilePath, err := filepath.Abs(filePath)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving file path: %w", err)
			}
			relPath, err := filepath.Rel(absWorkingDir, absFilePath)
			if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
				return fantasy.NewTextErrorResponse("file_path must be within the working directory"), nil
			}
			filePath = absFilePath

			fileInfo, err := os.Stat(filePath)
			if err == nil {
				if fileInfo.IsDir() {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("Path is a directory, not a file: %s", filePath)), nil
				}

				modTime := fileInfo.ModTime().Truncate(time.Second)
				lastRead := filetracker.LastReadTime(ctx, sessionID, filePath)
				if modTime.After(lastRead) {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("File %s has been modified since it was last read.\nLast modification: %s\nLast read: %s\n\nPlease read the file again before modifying it.",
						filePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339))), nil
				}
			} else if !os.IsNotExist(err) {
				return fantasy.ToolResponse{}, fmt.Errorf("error checking file: %w", err)
			}

			dir := filepath.Dir(filePath)
			if err = os.MkdirAll(dir, 0o755); err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error creating directory: %w", err)
			}

			var oldContent string
			var oldSize int
			if fileInfo != nil && !fileInfo.IsDir() {
				oldBytes, readErr := os.ReadFile(filePath)
				if readErr == nil {
					oldContent = string(oldBytes)
					oldSize = len(oldBytes)
				}
			}

			newContent := oldContent + params.Content
			newSize := len(newContent)

			diffResult, additions, removals := diff.GenerateDiff(
				oldContent,
				newContent,
				strings.TrimPrefix(filePath, workingDir),
			)

			p, err := permissions.Request(
				ctx,
				permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        fsext.PathOrPrefix(filePath, workingDir),
					ToolCallID:  call.ID,
					ToolName:    AppendToolName,
					Action:      "write",
					Description: fmt.Sprintf("Append to file %s", filePath),
					Params: AppendPermissionsParams{
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
				resp = fantasy.WithResponseMetadata(resp, AppendResponseMetadata{
					Additions: additions,
					Removals:  removals,
					OldSize:   oldSize,
					NewSize:   newSize,
				})
				return resp, nil
			}

			f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error opening file for appending: %w", err)
			}
			_, err = f.WriteString(params.Content)
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error appending to file: %w", err)
			}

			// Check if file exists in history
			file, err := files.GetByPathAndSession(ctx, filePath, sessionID)
			if err != nil {
				_, err = files.Create(ctx, sessionID, filePath, oldContent)
				if err != nil {
					// Log error but don't fail the operation
					return fantasy.ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
				}
			}
			if file.Content != oldContent {
				// User manually changed the content; store an intermediate version
				_, err = files.CreateVersion(ctx, sessionID, filePath, oldContent)
				if err != nil {
					slog.Error("Error creating file history version", "error", err)
				}
			}
			// Store the new version
			_, err = files.CreateVersion(ctx, sessionID, filePath, newContent)
			if err != nil {
				slog.Error("Error creating file history version", "error", err)
			}

			filetracker.RecordRead(ctx, sessionID, filePath)

			notifyLSPs(ctx, lspManager, params.FilePath)

			result := fmt.Sprintf("Content successfully appended to file: %s", filePath)
			result = fmt.Sprintf("<result>\n%s\n</result>", result)
			result += getDiagnostics(filePath, lspManager)
			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(result),
				AppendResponseMetadata{
					Diff:      diffResult,
					Additions: additions,
					Removals:  removals,
					OldSize:   oldSize,
					NewSize:   newSize,
				},
			), nil
		},
	)
}
