package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/hackafterdark/phosphor/internal/fsext"
	"github.com/stretchr/testify/require"
)

// G15: CRLF and UTF-8 BOM fidelity across the edit/multiedit/write tools.

func TestEditToolPreservesCRLF(t *testing.T) {
	t.Parallel()
	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "crlf.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("line A\r\nline B\r\nline C\r\n"), 0o644))

	tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerForEdit{}, workingDir, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	input, err := json.Marshal(EditParams{
		FilePath:  filePath,
		OldString: "line B",
		NewString: "line B modified",
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c", Name: EditToolName, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "line A\r\nline B modified\r\nline C\r\n", string(data))
}

func TestEditToolPreservesBOM(t *testing.T) {
	t.Parallel()
	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "bom.txt")
	require.NoError(t, os.WriteFile(filePath, []byte(fsext.UTF8BOM+"line A\nline B\nline C\n"), 0o644))

	tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerForEdit{}, workingDir, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	input, err := json.Marshal(EditParams{
		FilePath:  filePath,
		OldString: "line B",
		NewString: "line B modified",
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c", Name: EditToolName, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, fsext.UTF8BOM+"line A\nline B modified\nline C\n", string(data))
}

func TestEditToolPreservesBOMAndCRLF(t *testing.T) {
	t.Parallel()
	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "bom_crlf.txt")
	require.NoError(t, os.WriteFile(filePath, []byte(fsext.UTF8BOM+"line A\r\nline B\r\nline C\r\n"), 0o644))

	tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerForEdit{}, workingDir, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// Fuzzy match: whitespace differs, encoding quirks must still survive.
	input, err := json.Marshal(EditParams{
		FilePath:  filePath,
		OldString: "line B",
		NewString: "line B modified",
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c", Name: EditToolName, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, fsext.UTF8BOM+"line A\r\nline B modified\r\nline C\r\n", string(data))
}

func TestEditToolBOMHashlineRoundTrip(t *testing.T) {
	t.Parallel()
	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "bom_hash.txt")
	// BOM sits on line 1; the view tool strips it, so the hash covers the
	// BOM-free line. The edit must accept that hashline and re-apply the BOM.
	require.NoError(t, os.WriteFile(filePath, []byte(fsext.UTF8BOM+"line A\nline B\nline C\n"), 0o644))

	hashA := crc32.ChecksumIEEE([]byte("line A"))
	tagged := fmt.Sprintf("     1:%08x|line A", hashA)

	tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerForEdit{}, workingDir, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	input, err := json.Marshal(EditParams{
		FilePath:  filePath,
		OldString: tagged,
		NewString: "line A modified",
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c", Name: EditToolName, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, fsext.UTF8BOM+"line A modified\nline B\nline C\n", string(data))
}

func TestEditToolDeletePreservesBOMAndCRLF(t *testing.T) {
	t.Parallel()
	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "bom_del.txt")
	require.NoError(t, os.WriteFile(filePath, []byte(fsext.UTF8BOM+"line A\r\nline B\r\nline C\r\n"), 0o644))

	tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerForEdit{}, workingDir, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	input, err := json.Marshal(EditParams{
		FilePath:  filePath,
		OldString: "line B",
		NewString: "",
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c", Name: EditToolName, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	// Deleting the text leaves the surrounding newlines in place.
	require.Equal(t, fsext.UTF8BOM+"line A\r\n\r\nline C\r\n", string(data))
}

func TestMultiEditToolPreservesBOMAndCRLF(t *testing.T) {
	t.Parallel()
	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "bom_multi.txt")
	require.NoError(t, os.WriteFile(filePath, []byte(fsext.UTF8BOM+"line A\r\nline B\r\nline C\r\n"), 0o644))

	tool := NewMultiEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerForEdit{}, workingDir, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	input, err := json.Marshal(MultiEditParams{
		FilePath: filePath,
		Edits: []MultiEditOperation{
			{OldString: "line A", NewString: "LINE A"},
			{OldString: "line C", NewString: "LINE C"},
		},
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c", Name: MultiEditToolName, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, fsext.UTF8BOM+"LINE A\r\nline B\r\nLINE C\r\n", string(data))
}

func TestWriteToolPreservesBOM(t *testing.T) {
	t.Parallel()
	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "bom_write.txt")
	require.NoError(t, os.WriteFile(filePath, []byte(fsext.UTF8BOM+"original\n"), 0o644))

	tool := NewWriteTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerForEdit{}, workingDir, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	input, err := json.Marshal(WriteParams{
		FilePath: filePath,
		Content:  "rewritten\n",
	})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c", Name: WriteToolName, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, fsext.UTF8BOM+"rewritten\n", string(data))
}

// G4: fuzzy fallback ladder.

func TestFuzzyMatchUnicodeQuotes(t *testing.T) {
	t.Parallel()
	content := "let msg = \"hello world\";"

	// File has straight quotes; old_string carries curly quotes.
	res, err := applyEditWithFuzzy(nil, "t.js", content, "let msg = \u201Chello world\u201D;", "let msg = \"hi\";", false)
	require.NoError(t, err)
	require.Contains(t, res, `let msg = "hi";`)

	// File has curly quotes; old_string carries straight quotes.
	curly := "let msg = \u201Chello world\u201D;"
	res, err = applyEditWithFuzzy(nil, "t.js", curly, `let msg = "hello world";`, "let msg = \u201Chi\u201D;", false)
	require.NoError(t, err)
	require.Contains(t, res, "let msg = \u201Chi\u201D;")
}

func TestFuzzyMatchNBSPAndZeroWidth(t *testing.T) {
	t.Parallel()
	content := "value\u00A0=\u00A042;"

	res, err := applyEditWithFuzzy(nil, "t.txt", content, "value = 42;", "value = 43;", false)
	require.NoError(t, err)
	require.Contains(t, res, "value = 43;")

	// Zero-width characters in old_string must not block the match.
	content = "val\u200Bue = 42;"
	res, err = applyEditWithFuzzy(nil, "t.txt", content, "value = 42;", "value = 43;", false)
	require.NoError(t, err)
	require.Contains(t, res, "value = 43;")
}

func TestFuzzyMatchCRLFContentAgainstLFString(t *testing.T) {
	t.Parallel()
	content := "line one\r\nline two\r\nline three"

	res, err := applyEditWithFuzzy(nil, "t.txt", content, "line one\nline two", "line A\nline B", false)
	require.NoError(t, err)
	require.Contains(t, res, "line A\nline B")
}

func TestNotFoundErrorIncludesHashlineSnippet(t *testing.T) {
	t.Parallel()
	content := "alpha\nbeta gamma\ndelta"

	resp := makeNotFoundError(content, "beta  ganmma")
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Retry using the current on-disk content of the closest region")

	// The snippet must carry hashline tags so the model can retry directly.
	require.Contains(t, resp.Content, fmt.Sprintf("2:%08x|beta gamma", crc32.ChecksumIEEE([]byte("beta gamma"))))

	// Replaying the tagged snippet must succeed against the same content.
	tag := fmt.Sprintf("     2:%08x|beta gamma", crc32.ChecksumIEEE([]byte("beta gamma")))
	clean, hashInfo, hasTags := parseTaggedContent(tag)
	require.True(t, hasTags)
	require.NoError(t, verifyHashes(content, hashInfo))
	_, err := applyEditWithFuzzy(nil, "t.txt", content, clean, "beta GAMMA", false)
	require.NoError(t, err)
}

func TestViewStripsBOMFromDisplayedContent(t *testing.T) {
	t.Parallel()
	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "bom_view.txt")
	require.NoError(t, os.WriteFile(filePath, []byte(fsext.UTF8BOM+"line A\nline B\n"), 0o644))

	tool := NewViewTool(nil, &mockPermissionService{}, mockFileTrackerForEdit{}, nil, workingDir, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	input, err := json.Marshal(ViewParams{FilePath: filePath, UseHashline: true})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c", Name: ViewToolName, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.NotContains(t, resp.Content, fsext.UTF8BOM)
	require.True(t, strings.Contains(resp.Content, fmt.Sprintf("1:%08x|line A", crc32.ChecksumIEEE([]byte("line A")))))
}
