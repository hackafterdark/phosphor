package logo

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/mbndr/figlet4go"
)

//go:embed *.flf
var fontFS embed.FS

var (
	fontCache map[string][]byte
	once      sync.Once
)

// getRenderer returns a new AsciiRender with all embedded fonts loaded.
func getRenderer() *figlet4go.AsciiRender {
	once.Do(initFonts)
	ar := figlet4go.NewAsciiRender()

	for name, data := range fontCache {
		_ = ar.LoadBindataFont(data, strings.TrimSuffix(name, ".flf"))
	}

	return ar
}

// initFonts loads embedded fonts into memory.
func initFonts() {
	fontCache = make(map[string][]byte)

	entries, err := fs.ReadDir(fontFS, ".")
	if err != nil {
		return
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		data, err := fs.ReadFile(fontFS, name)
		if err != nil {
			continue
		}
		fontCache[name] = normalizeLineEndings(data)
	}
}

// loadFontAtPath loads a font from a file path on disk.
func loadFontAtPath(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return normalizeLineEndings(data)
}

// normalizeLineEndings converts CRLF line endings to LF line endings and normalizes characters.
func normalizeLineEndings(data []byte) []byte {
	// First, convert CRLF to LF.
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) < 1 {
		return data
	}

	header := strings.Split(lines[0], " ")
	if len(header) < 6 {
		return []byte(content)
	}

	commentLines, err := strconv.Atoi(header[5])
	if err != nil {
		return []byte(content)
	}

	charHeight, err := strconv.Atoi(header[1])
	if err != nil {
		return []byte(content)
	}

	// Detect end-of-line character from the last line of the first character definition.
	// First character definition starts at line index: commentLines + 1.
	// Its last line is at index: commentLines + charHeight.
	eolIndex := commentLines + charHeight
	if eolIndex >= len(lines) {
		return []byte(content)
	}

	eolLine := lines[eolIndex]
	if len(eolLine) == 0 {
		return []byte(content)
	}

	// The last character of the line is the EOL character.
	eolChar := string(eolLine[len(eolLine)-1])
	eolSuffix := eolChar
	eolDoubleSuffix := eolChar + eolChar

	// Loop through all lines after the header and comment lines to handle internal "@" replacement.
	for i := commentLines + 1; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}

		// Find and strip the custom EOL characters
		var stripped string
		var suffix string
		if strings.HasSuffix(line, eolDoubleSuffix) {
			stripped = strings.TrimSuffix(line, eolDoubleSuffix)
			suffix = "@@" // Convert custom EOL to standard '@' for figlet4go
		} else if strings.HasSuffix(line, eolSuffix) {
			stripped = strings.TrimSuffix(line, eolSuffix)
			suffix = "@" // Convert custom EOL to standard '@' for figlet4go
		} else {
			stripped = line
			suffix = ""
		}

		// Replace any internal "@" in the glyph with a private-use placeholder character
		// so that figlet4go does not strip it, allowing us to restore it dynamically.
		processed := strings.ReplaceAll(stripped, "@", "\uE000")

		// Re-assemble the line with standard '@' EOL markers.
		lines[i] = processed + suffix
	}

	return []byte(strings.Join(lines, "\n"))
}

// isFontPath checks if the given string looks like a file path to a font.
func isFontPath(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".flf") ||
		strings.HasPrefix(name, "/") ||
		strings.HasPrefix(name, ".") ||
		strings.HasPrefix(name, "..") ||
		strings.HasPrefix(name, ".\\")
}

// getFontData returns font data by name or from a file path.
func getFontData(nameOrPath string) []byte {
	// Check if it's a file path.
	if isFontPath(nameOrPath) {
		data := loadFontAtPath(nameOrPath)
		if data != nil {
			return data
		}
		// Try without extension.
		if !strings.HasSuffix(strings.ToLower(nameOrPath), ".flf") {
			data := loadFontAtPath(nameOrPath + ".flf")
			if data != nil {
				return data
			}
		}
		return nil
	}

	// It's a font name.
	return fontCache[nameOrPath]
}

// FigletText renders text using FIGlet and returns the rendered string
// along with its dimensions (width and height in character cells).
// fontName can be an embedded font name or a path to a .flf file on disk.
// If fontName is empty, "Pagga" is used as the default.
func FigletText(text, fontName string, treatAtAsBlock bool) (rendered string, width, height int, err error) {
	if fontName == "" {
		fontName = "Pagga"
	}
	ar := getRenderer()

	// Check if fontName is a file path.
	if isFontPath(fontName) {
		data := loadFontAtPath(fontName)
		if data == nil {
			// Try with .flf extension.
			data = loadFontAtPath(fontName + ".flf")
		}
		if data != nil {
			_ = ar.LoadBindataFont(data, strings.TrimSuffix(filepath.Base(fontName), ".flf"))
		}
		fontName = strings.TrimSuffix(filepath.Base(fontName), ".flf")
	}

	opt := figlet4go.NewRenderOptions()
	opt.FontName = fontName

	result, err := ar.RenderOpts(text, opt)
	if err != nil {
		opt.FontName = "standard"
		result, err = ar.RenderOpts(text, opt)
		if err != nil {
			return "", 0, 0, err
		}
	}

	if treatAtAsBlock {
		result = strings.ReplaceAll(result, "\uE000", "█")
	} else {
		result = strings.ReplaceAll(result, "\uE000", "@")
	}

	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	height = len(lines)
	width = 0
	for _, line := range lines {
		if l := lipgloss.Width(line); l > width {
			width = l
		}
	}

	return result, width, height, nil
}
